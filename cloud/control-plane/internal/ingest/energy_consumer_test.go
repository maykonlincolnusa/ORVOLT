package ingest

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/contract"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
	sitev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/energy/site/v1"
)

func newEnergyTestRunner(persist Persister[domain.EnergyObservation]) *Runner[domain.EnergyObservation] {
	return &Runner[domain.EnergyObservation]{
		name:       EnergyStreamName,
		decode:     contract.DecodeEnergySiteObservation,
		persist:    persist,
		batchSize:  16,
		batchWait:  time.Millisecond,
		retryDelay: time.Millisecond,
		now:        func() time.Time { return ingestedAt },
	}
}

func TestEnergyRunnerPersistsAuthorizedObservation(t *testing.T) {
	solar := 9.5
	payload, err := proto.Marshal(&sitev1.EnergySiteObservation{
		SiteId:        "site-1",
		ObservedAtMs:  1_700_000_000_000,
		RetrievedAtMs: 1_700_000_000_100,
		Source: &sitev1.SourceMetadata{
			Provider:       sitev1.EnergyProvider_ENERGY_PROVIDER_SMA,
			ProviderSiteId: "sma-site-1",
			ConsentScope:   "energy.read",
		},
		SolarGenerationKw: &solar,
		DataQuality:       sitev1.DataQuality_DATA_QUALITY_MEASURED,
	})
	if err != nil {
		t.Fatalf("marshalling observation: %v", err)
	}

	var stored []domain.EnergyObservation
	runner := newEnergyTestRunner(func(_ context.Context, batch []domain.EnergyObservation) error {
		stored = append(stored, batch...)
		return nil
	})

	received := &fakeMessage{payload: payload}
	runner.ProcessBatch(context.Background(), []message{received})

	if len(stored) != 1 {
		t.Fatalf("expected one observation, got %d", len(stored))
	}
	if stored[0].Provider != "SMA" || stored[0].ConsentScope != "energy.read" {
		t.Errorf("provider provenance was not preserved: %+v", stored[0])
	}
	if stored[0].SolarGenerationKW == nil || *stored[0].SolarGenerationKW != solar {
		t.Errorf("solar generation was not preserved: %+v", stored[0].SolarGenerationKW)
	}
	if received.acked != 1 {
		t.Errorf("expected the message to be acknowledged, got %d", received.acked)
	}
}

func TestEnergyRunnerRejectsObservationWithoutProvenance(t *testing.T) {
	// Provider provenance is mandatory: an observation whose consent scope and
	// source identity are unknown cannot be attributed to an authorization.
	payload, err := proto.Marshal(&sitev1.EnergySiteObservation{
		SiteId:        "site-1",
		ObservedAtMs:  1_700_000_000_000,
		RetrievedAtMs: 1_700_000_000_100,
		DataQuality:   sitev1.DataQuality_DATA_QUALITY_MEASURED,
	})
	if err != nil {
		t.Fatalf("marshalling observation: %v", err)
	}

	called := false
	runner := newEnergyTestRunner(func(context.Context, []domain.EnergyObservation) error {
		called = true
		return nil
	})

	received := &fakeMessage{payload: payload}
	runner.ProcessBatch(context.Background(), []message{received})

	if called {
		t.Error("an observation without provenance must never reach the repository")
	}
	if received.termed != 1 {
		t.Errorf("expected the observation to be terminated, got term=%d", received.termed)
	}
}
