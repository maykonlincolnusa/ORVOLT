package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// DeadLetter parks payloads that can never be decoded.
//
// The previous design logged a rejection and moved on, which meant a schema
// mismatch or a corrupt device destroyed evidence silently. Parking the raw
// bytes with their failure reason keeps the operator able to answer "what did
// that charger actually send?" without turning the mistake into an infinite
// redelivery loop.
type DeadLetter struct {
	stream  jetstream.JetStream
	subject string
}

const DeadLetterStreamName = "ORVOLT_DLQ"

// NewDeadLetter creates (or updates) the dead-letter stream. Its retention is
// deliberately short compared to the live streams: it is an investigation
// buffer, not an archive.
func NewDeadLetter(ctx context.Context, stream jetstream.JetStream, subjectPrefix string, maxAge time.Duration) (*DeadLetter, error) {
	if subjectPrefix == "" {
		return nil, fmt.Errorf("dead-letter subject prefix is required")
	}
	_, err := stream.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        DeadLetterStreamName,
		Description: "Payloads rejected by contract validation, retained for investigation.",
		Subjects:    []string{subjectPrefix + ".>"},
		Storage:     jetstream.FileStorage,
		Retention:   jetstream.LimitsPolicy,
		MaxAge:      maxAge,
		MaxBytes:    1 << 30,
		Discard:     jetstream.DiscardOld,
	})
	if err != nil {
		return nil, fmt.Errorf("create dead-letter stream: %w", err)
	}
	return &DeadLetter{stream: stream, subject: subjectPrefix}, nil
}

// Park publishes the rejected payload together with the reason it failed.
func (deadLetter *DeadLetter) Park(ctx context.Context, source string, payload []byte, reason error) error {
	message := &nats.Msg{
		Subject: deadLetter.subject + "." + source,
		Data:    payload,
		Header: nats.Header{
			"Orvolt-Source-Stream": []string{source},
			"Orvolt-Reason":        []string{reason.Error()},
			"Orvolt-Parked-At":     []string{time.Now().UTC().Format(time.RFC3339Nano)},
		},
	}
	if _, err := deadLetter.stream.PublishMsg(ctx, message); err != nil {
		return fmt.Errorf("park dead-letter message: %w", err)
	}
	slog.Warn("payload parked in dead-letter stream",
		"source", source,
		"reason", reason.Error(),
		"bytes", len(payload),
	)
	return nil
}
