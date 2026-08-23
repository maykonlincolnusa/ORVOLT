package ingest

import (
	"context"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/contract"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
)

// SessionStreamName holds charging transactions.
//
// Sessions are a separate stream from telemetry on purpose. They have different
// value and different retention: a missing telemetry sample is a gap in a
// chart, a missing session is a lost invoice. Keeping them apart means the
// high-volume stream's retention policy can never evict billing evidence.
const SessionStreamName = "ORVOLT_SESSIONS"

func NewSessionRunner(
	ctx context.Context,
	stream jetstream.JetStream,
	repository domain.Repository,
	subject string,
	deadLetter *DeadLetter,
	maxAge time.Duration,
	maxBytes int64,
	batchSize int,
	batchWait time.Duration,
) (*Runner[domain.SessionEvent], error) {
	return NewRunner(
		ctx,
		stream,
		StreamSpec{
			Stream:      SessionStreamName,
			Description: "Charging transaction lifecycle events from edge and protocol adapters.",
			Subject:     subject,
			Durable:     "orvolt-session-control-plane",
			MaxAge:      maxAge,
			MaxBytes:    maxBytes,
		},
		contract.DecodeChargingSessionEvent,
		repository.PersistSessionEventBatch,
		deadLetter,
		batchSize,
		batchWait,
	)
}
