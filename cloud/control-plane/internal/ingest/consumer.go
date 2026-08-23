package ingest

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/contract"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
)

const streamName = "ORVOLT_TELEMETRY"

type Consumer struct {
	jetstream nats.JetStreamContext
	repository domain.Repository
	subject    string
	subscription *nats.Subscription
}

func NewConsumer(connection *nats.Conn, repository domain.Repository, subject string) (*Consumer, error) {
	if connection == nil {
		return nil, fmt.Errorf("NATS connection is required")
	}
	return &Consumer{jetstream: nats.NewJetStreamContext(connection), repository: repository, subject: subject}, nil
}

func (consumer *Consumer) Start(ctx context.Context) error {
	_, err := consumer.jetstream.AddStream(&nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{consumer.subject},
		Storage:   nats.FileStorage,
		Retention: nats.LimitsPolicy,
	})
	if err != nil && err != nats.ErrStreamNameAlreadyInUse {
		return fmt.Errorf("create telemetry stream: %w", err)
	}
	subscription, err := consumer.jetstream.Subscribe(
		consumer.subject,
		func(message *nats.Msg) {
			if err := consumer.Process(ctx, message.Data); err != nil {
				slog.Error("telemetry processing failed; message will be redelivered", "error", err)
				return
			}
			if err := message.Ack(); err != nil {
				slog.Error("telemetry acknowledgement failed", "error", err)
			}
		},
		nats.Durable("orvolt-control-plane"),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.DeliverNew(),
	)
	if err != nil {
		return fmt.Errorf("subscribe telemetry stream: %w", err)
	}
	consumer.subscription = subscription
	return nil
}

func (consumer *Consumer) Process(ctx context.Context, payload []byte) error {
	telemetry, err := contract.DecodeChargingTelemetry(payload)
	if err != nil {
		return err
	}
	if err := consumer.repository.PersistTelemetry(ctx, telemetry); err != nil {
		return fmt.Errorf("persist charging telemetry: %w", err)
	}
	slog.Info("persisted charging telemetry", "station_id", telemetry.StationID, "connector_id", telemetry.ConnectorID)
	return nil
}

func (consumer *Consumer) Stop() {
	if consumer.subscription != nil {
		if err := consumer.subscription.Drain(); err != nil {
			slog.Warn("JetStream subscription drain failed", "error", err)
		}
	}
}
