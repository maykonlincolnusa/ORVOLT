package ingest

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/contract"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
)

const energyStreamName = "ORVOLT_ENERGY"

type EnergyConsumer struct {
	jetstream    nats.JetStreamContext
	repository   domain.Repository
	subject      string
	subscription *nats.Subscription
}

func NewEnergyConsumer(connection *nats.Conn, repository domain.Repository, subject string) (*EnergyConsumer, error) {
	if connection == nil {
		return nil, fmt.Errorf("NATS connection is required")
	}
	return &EnergyConsumer{jetstream: nats.NewJetStreamContext(connection), repository: repository, subject: subject}, nil
}

func (consumer *EnergyConsumer) Start(ctx context.Context) error {
	_, err := consumer.jetstream.AddStream(&nats.StreamConfig{
		Name:      energyStreamName,
		Subjects:  []string{consumer.subject},
		Storage:   nats.FileStorage,
		Retention: nats.LimitsPolicy,
	})
	if err != nil && err != nats.ErrStreamNameAlreadyInUse {
		return fmt.Errorf("create energy stream: %w", err)
	}
	subscription, err := consumer.jetstream.Subscribe(
		consumer.subject,
		func(message *nats.Msg) {
			if err := consumer.Process(ctx, message.Data); err != nil {
				slog.Error("energy observation processing failed; message will be redelivered", "error", err)
				return
			}
			if err := message.Ack(); err != nil {
				slog.Error("energy observation acknowledgement failed", "error", err)
			}
		},
		nats.Durable("orvolt-energy-control-plane"),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.DeliverNew(),
	)
	if err != nil {
		return fmt.Errorf("subscribe energy stream: %w", err)
	}
	consumer.subscription = subscription
	return nil
}

func (consumer *EnergyConsumer) Process(ctx context.Context, payload []byte) error {
	observation, err := contract.DecodeEnergySiteObservation(payload)
	if err != nil {
		return err
	}
	if err := consumer.repository.PersistEnergyObservation(ctx, observation); err != nil {
		return fmt.Errorf("persist energy observation: %w", err)
	}
	slog.Info("persisted energy observation", "site_id", observation.SiteID, "provider", observation.Provider)
	return nil
}

func (consumer *EnergyConsumer) Stop() {
	if consumer.subscription != nil {
		if err := consumer.subscription.Drain(); err != nil {
			slog.Warn("energy JetStream subscription drain failed", "error", err)
		}
	}
}
