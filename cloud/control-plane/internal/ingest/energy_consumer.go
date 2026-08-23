package ingest

import (
	"context"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/contract"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
)

// NewEnergyRunner wires the external energy-observation stream to the
// repository. Energy data is optional optimisation input: a provider outage
// must never affect the telemetry path, which is why it runs as an independent
// stream and consumer rather than sharing the telemetry one.
func NewEnergyRunner(
	ctx context.Context,
	stream jetstream.JetStream,
	repository domain.Repository,
	subject string,
	deadLetter *DeadLetter,
	maxAge time.Duration,
	maxBytes int64,
	batchSize int,
	batchWait time.Duration,
) (*Runner[domain.EnergyObservation], error) {
	return NewRunner(
		ctx,
		stream,
		StreamSpec{
			Stream:      EnergyStreamName,
			Description: "Read-only energy-site observations from authorized external providers.",
			Subject:     subject,
			Durable:     "orvolt-energy-control-plane",
			MaxAge:      maxAge,
			MaxBytes:    maxBytes,
		},
		contract.DecodeEnergySiteObservation,
		repository.PersistEnergyObservationBatch,
		deadLetter,
		batchSize,
		batchWait,
	)
}
