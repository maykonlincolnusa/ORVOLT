// Package publisher puts canonical ORVOLT events onto the event bus.
//
// The gateway does not talk to PostgreSQL. It speaks one protocol inwards and
// publishes contracts outwards, which keeps a protocol defect contained in one
// service and lets the control plane stay unaware that OCPP exists.
package publisher

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	sessionv1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/session/evse/v1"
	evsev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/telemetry/evse/v1"
)

type Publisher struct {
	stream           jetstream.JetStream
	telemetrySubject string
	sessionSubject   string
}

func New(stream jetstream.JetStream, telemetrySubject, sessionSubject string) *Publisher {
	return &Publisher{
		stream:           stream,
		telemetrySubject: telemetrySubject,
		sessionSubject:   sessionSubject,
	}
}

func (publisher *Publisher) Telemetry(ctx context.Context, telemetry *evsev1.ChargingTelemetry) error {
	return publisher.publish(ctx, publisher.telemetrySubject, telemetry, "telemetry")
}

func (publisher *Publisher) Session(ctx context.Context, event *sessionv1.ChargingSessionEvent) error {
	return publisher.publish(ctx, publisher.sessionSubject, event, "session event")
}

// publish waits for the JetStream acknowledgement.
//
// The wait is deliberate for session events: acknowledging a StopTransaction to
// a charge point before the record is durable would let the charge point
// discard its only copy of a billable transaction.
func (publisher *Publisher) publish(ctx context.Context, subject string, message proto.Message, label string) error {
	payload, err := proto.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode %s: %w", label, err)
	}
	acknowledgement, err := publisher.stream.Publish(ctx, subject, payload)
	if err != nil {
		return fmt.Errorf("publish %s to %s: %w", label, subject, err)
	}
	if acknowledgement == nil {
		return fmt.Errorf("publish %s to %s: no acknowledgement", label, subject)
	}
	return nil
}
