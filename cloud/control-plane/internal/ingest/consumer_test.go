package ingest_test

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/nats-io/nats.go"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/ingest"
	evsev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/telemetry/evse/v1"
)

type repository struct {
	telemetry []domain.Telemetry
	energy    []domain.EnergyObservation
}

func (repository *repository) PersistTelemetry(_ context.Context, telemetry domain.Telemetry) error {
	repository.telemetry = append(repository.telemetry, telemetry)
	return nil
}
func (repository *repository) PersistEnergyObservation(_ context.Context, observation domain.EnergyObservation) error {
	repository.energy = append(repository.energy, observation)
	return nil
}
func (*repository) ListStations(context.Context) ([]domain.Station, error) { return nil, nil }
func (*repository) GetStation(context.Context, string) (domain.Station, bool, error) {
	return domain.Station{}, false, nil
}
func (*repository) LatestTelemetry(context.Context, string) (domain.Telemetry, bool, error) {
	return domain.Telemetry{}, false, nil
}
func (*repository) LatestEnergyObservation(context.Context, string) (domain.EnergyObservation, bool, error) {
	return domain.EnergyObservation{}, false, nil
}

func TestConsumerProcessesCanonicalEvent(t *testing.T) {
	payload, err := proto.Marshal(&evsev1.ChargingTelemetry{
		StationId: "station-1", ConnectorId: "1", TimestampMs: 1, State: evsev1.ChargingState_CHARGING,
		Edge: &evsev1.EdgeMetadata{EdgeId: "edge-1", SiteId: "site-1", ReceivedAtMs: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := &repository{}
	consumer, err := ingest.NewConsumer(&nats.Conn{}, repository, "orvolt.telemetry.evse.v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Process(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if len(repository.telemetry) != 1 || repository.telemetry[0].StationID != "station-1" {
		t.Fatalf("telemetry was not persisted: %#v", repository.telemetry)
	}
}
