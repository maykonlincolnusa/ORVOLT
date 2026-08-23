// Package ingest consumes canonical events from JetStream and persists them.
//
// Three properties matter more than throughput here:
//
//   - Streams are bounded. An unbounded stream fills the broker's disk and then
//     the whole fleet stops being able to publish.
//   - Undecodable payloads are parked, not retried forever. A single corrupt
//     device must not be able to stall a durable consumer.
//   - Messages are persisted in batches. One transaction per telemetry sample
//     does not survive fleet scale.
package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/contract"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/metrics"
)

const (
	TelemetryStreamName = "ORVOLT_TELEMETRY"
	EnergyStreamName    = "ORVOLT_ENERGY"
)

// message is the slice of jetstream.Msg the runner depends on. Depending on an
// interface instead of the concrete type keeps the batch logic unit-testable
// without a running broker.
type message interface {
	Data() []byte
	// Subject carries the publisher's identity. NATS enforces which subjects a
	// credential may publish to, so the subject is the one part of a message a
	// device cannot forge.
	Subject() string
	Ack() error
	NakWithDelay(delay time.Duration) error
	Term() error
}

// Decoder turns a wire payload into a domain value. ingestedAt is passed in so
// that every record in a batch shares one arrival stamp.
type Decoder[T any] func(payload []byte, ingestedAt time.Time) (T, error)

// Persister writes a decoded batch. It must be idempotent: JetStream delivery
// is at-least-once and a redelivered batch will be written again.
type Persister[T any] func(ctx context.Context, batch []T) error

// StreamSpec describes the durable stream a runner owns.
type StreamSpec struct {
	Stream      string
	Description string
	Subject     string
	Durable     string
	MaxAge      time.Duration
	MaxBytes    int64
}

// Runner consumes one subject and persists it in batches.
type Runner[T any] struct {
	name       string
	consumer   jetstream.Consumer
	decode     Decoder[T]
	persist    Persister[T]
	verify     Verifier[T]
	deadLetter *DeadLetter
	batchSize  int
	batchWait  time.Duration
	retryDelay time.Duration
	now        func() time.Time
}

// Service is the runner behaviour main depends on, so that runners over
// different payload types can be supervised uniformly.
type Service interface {
	Name() string
	Run(ctx context.Context) error
}

// NewRunner creates or updates the stream and its durable consumer.
func NewRunner[T any](
	ctx context.Context,
	stream jetstream.JetStream,
	spec StreamSpec,
	decode Decoder[T],
	persist Persister[T],
	deadLetter *DeadLetter,
	batchSize int,
	batchWait time.Duration,
) (*Runner[T], error) {
	if spec.MaxAge <= 0 || spec.MaxBytes <= 0 {
		return nil, fmt.Errorf("stream %s requires a positive retention age and size", spec.Stream)
	}
	created, err := stream.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        spec.Stream,
		Description: spec.Description,
		// Publishers address their own subject below the base one, so that the
		// broker's per-credential publish permissions become an authenticated
		// statement of who sent each message. The bare base subject stays
		// accepted for unidentified development publishers.
		Subjects:  []string{spec.Subject, spec.Subject + ".>"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    spec.MaxAge,
		MaxBytes:  spec.MaxBytes,
		// Drop the oldest observations rather than refusing new ones. Losing
		// history is recoverable; a charger unable to publish is not.
		Discard: jetstream.DiscardOld,
	})
	if err != nil {
		return nil, fmt.Errorf("create stream %s: %w", spec.Stream, err)
	}
	consumer, err := created.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       spec.Durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		MaxAckPending: 4 * batchSize,
		AckWait:       30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("create consumer for %s: %w", spec.Stream, err)
	}
	return &Runner[T]{
		name:       spec.Stream,
		consumer:   consumer,
		decode:     decode,
		persist:    persist,
		deadLetter: deadLetter,
		batchSize:  batchSize,
		batchWait:  batchWait,
		retryDelay: 5 * time.Second,
		now:        func() time.Time { return time.Now().UTC() },
	}, nil
}

func (runner *Runner[T]) Name() string { return runner.name }

// WithVerifier installs an origin check applied after decoding and before
// persistence.
func (runner *Runner[T]) WithVerifier(verify Verifier[T]) *Runner[T] {
	runner.verify = verify
	return runner
}

// Run consumes until the context is cancelled.
func (runner *Runner[T]) Run(ctx context.Context) error {
	slog.Info("ingest runner started", "stream", runner.name, "batch_size", runner.batchSize)
	for {
		if err := ctx.Err(); err != nil {
			slog.Info("ingest runner stopped", "stream", runner.name)
			return err
		}
		batch, err := runner.consumer.Fetch(runner.batchSize, jetstream.FetchMaxWait(runner.batchWait))
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Warn("JetStream fetch failed; retrying", "stream", runner.name, "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(runner.retryDelay):
			}
			continue
		}
		messages := make([]message, 0, runner.batchSize)
		for received := range batch.Messages() {
			messages = append(messages, received)
		}
		if err := batch.Error(); err != nil {
			slog.Warn("JetStream batch ended with an error", "stream", runner.name, "error", err)
		}
		if len(messages) == 0 {
			continue
		}
		runner.ProcessBatch(ctx, messages)
	}
}

// ProcessBatch decodes, persists and acknowledges one batch. It is exported so
// that the batching policy can be tested directly.
func (runner *Runner[T]) ProcessBatch(ctx context.Context, messages []message) {
	ingestedAt := runner.now()
	decoded := make([]T, 0, len(messages))
	accepted := make([]message, 0, len(messages))

	for _, received := range messages {
		value, err := runner.decode(received.Data(), ingestedAt)
		if err == nil && runner.verify != nil {
			// The payload has been read; now check that the sender is who the
			// payload says it is. A device can write anything into its own
			// message, but it cannot publish to another device's subject.
			err = runner.verify(received.Subject(), value)
		}
		if err == nil {
			decoded = append(decoded, value)
			accepted = append(accepted, received)
			continue
		}
		if contract.IsPermanent(err) {
			runner.park(ctx, received, err)
			continue
		}
		// A transient decode failure is unexpected, but redelivery is the safe
		// interpretation: never discard an observation we merely failed to read.
		slog.Warn("transient decode failure; message will be redelivered", "stream", runner.name, "error", err)
		_ = received.NakWithDelay(runner.retryDelay)
		metrics.Messages.WithLabelValues(runner.name, metrics.ResultRetried).Inc()
	}

	if len(decoded) == 0 {
		return
	}

	started := time.Now()
	if err := runner.persist(ctx, decoded); err != nil {
		slog.Error("persisting batch failed; messages will be redelivered",
			"stream", runner.name, "messages", len(accepted), "error", err)
		for _, received := range accepted {
			_ = received.NakWithDelay(runner.retryDelay)
		}
		metrics.Messages.WithLabelValues(runner.name, metrics.ResultRetried).Add(float64(len(accepted)))
		return
	}

	metrics.PersistDuration.WithLabelValues(runner.name).Observe(time.Since(started).Seconds())
	metrics.BatchSize.WithLabelValues(runner.name).Observe(float64(len(decoded)))
	metrics.Messages.WithLabelValues(runner.name, metrics.ResultPersisted).Add(float64(len(decoded)))

	for _, received := range accepted {
		if err := received.Ack(); err != nil {
			slog.Error("acknowledgement failed", "stream", runner.name, "error", err)
		}
	}
	slog.Info("persisted batch", "stream", runner.name, "messages", len(decoded))
}

// park routes a permanently invalid payload to the dead-letter stream. If
// parking itself fails the message is redelivered rather than dropped, because
// losing the evidence is worse than processing it twice.
func (runner *Runner[T]) park(ctx context.Context, received message, reason error) {
	if runner.deadLetter != nil {
		if err := runner.deadLetter.Park(ctx, runner.name, received.Data(), reason); err != nil {
			slog.Error("dead-letter park failed; message will be redelivered", "stream", runner.name, "error", err)
			_ = received.NakWithDelay(runner.retryDelay)
			metrics.Messages.WithLabelValues(runner.name, metrics.ResultRetried).Inc()
			return
		}
	} else {
		slog.Warn("no dead-letter stream configured; discarding invalid payload",
			"stream", runner.name, "reason", reason.Error())
	}
	_ = received.Term()
	metrics.Messages.WithLabelValues(runner.name, metrics.ResultDeadLettered).Inc()
}

// NewTelemetryRunner wires the charging-telemetry stream to the repository.
func NewTelemetryRunner(
	ctx context.Context,
	stream jetstream.JetStream,
	repository domain.Repository,
	subject string,
	deadLetter *DeadLetter,
	maxAge time.Duration,
	maxBytes int64,
	batchSize int,
	batchWait time.Duration,
	requireDeviceIdentity bool,
) (*Runner[domain.Telemetry], error) {
	runner, err := NewRunner(
		ctx,
		stream,
		StreamSpec{
			Stream:      TelemetryStreamName,
			Description: "Canonical EVSE charging telemetry produced by site edge agents.",
			Subject:     subject,
			Durable:     "orvolt-control-plane",
			MaxAge:      maxAge,
			MaxBytes:    maxBytes,
		},
		func(payload []byte, ingestedAt time.Time) (domain.Telemetry, error) {
			telemetry, err := contract.DecodeChargingTelemetry(payload, ingestedAt)
			if err != nil {
				return domain.Telemetry{}, err
			}
			if telemetry.ClockSync != domain.ClockSyncSynchronized {
				metrics.UnsynchronizedClock.Inc()
			}
			return telemetry, nil
		},
		repository.PersistTelemetryBatch,
		deadLetter,
		batchSize,
		batchWait,
	)
	if err != nil {
		return nil, err
	}
	return runner.WithVerifier(VerifyTelemetryOrigin(subject, requireDeviceIdentity)), nil
}
