package ingest_test

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/nats-io/nats.go"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/ingest"
	sitev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/energy/site/v1"
)

func TestEnergyConsumerProcessesCanonicalEvent(t *testing.T) {
	solar := 9.5
	payload, err := proto.Marshal(&sitev1.EnergySiteObservation{
		SiteId: "site-1", ObservedAtMs: 1, RetrievedAtMs: 2,
		Source: &sitev1.SourceMetadata{Provider: sitev1.EnergyProvider_SMA, ProviderSiteId: "sma-site-1", ConsentScope: "energy.read"},
		SolarGenerationKw: &solar,
		DataQuality:      sitev1.DataQuality_MEASURED,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := &repository{}
	consumer, err := ingest.NewEnergyConsumer(&nats.Conn{}, repository, "orvolt.energy.site.v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Process(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	if len(repository.energy) != 1 || repository.energy[0].Provider != "SMA" {
		t.Fatalf("energy observation was not persisted: %#v", repository.energy)
	}
}
