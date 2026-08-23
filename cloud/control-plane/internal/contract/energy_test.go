package contract_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/contract"
	sitev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/energy/site/v1"
)

func TestDecodeEnergySiteObservation(t *testing.T) {
	solar := 12.5
	batterySOC := 74.0
	payload, err := proto.Marshal(&sitev1.EnergySiteObservation{
		SiteId:        "site-1",
		ObservedAtMs:  1_700_000_000_000,
		RetrievedAtMs: 1_700_000_000_100,
		Source: &sitev1.SourceMetadata{
			Provider:       sitev1.EnergyProvider_ENERGY_PROVIDER_SMA,
			ProviderSiteId: "sma-site-77",
			ConsentScope:   "energy.read",
		},
		SolarGenerationKw: &solar,
		BatterySoc:        &batterySOC,
		DataQuality:       sitev1.DataQuality_DATA_QUALITY_MEASURED,
	})
	if err != nil {
		t.Fatal(err)
	}

	observation, err := contract.DecodeEnergySiteObservation(payload, ingestedAt)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Provider != "SMA" || observation.SolarGenerationKW == nil || *observation.SolarGenerationKW != 12.5 {
		t.Fatalf("unexpected energy observation: %#v", observation)
	}
	if observation.GridImportKW != nil {
		t.Fatalf("missing provider value must remain absent: %#v", observation)
	}
	if !observation.IngestedAt.Equal(ingestedAt) {
		t.Fatalf("the caller's arrival stamp must be used, got %s", observation.IngestedAt)
	}
}

func TestDecodeEnergySiteObservationRejectsInvalidBatterySOC(t *testing.T) {
	batterySOC := 120.0
	payload, err := proto.Marshal(&sitev1.EnergySiteObservation{
		SiteId: "site-1", ObservedAtMs: 1, RetrievedAtMs: 2,
		Source: &sitev1.SourceMetadata{
			Provider:       sitev1.EnergyProvider_ENERGY_PROVIDER_SMA,
			ProviderSiteId: "sma-site-77",
			ConsentScope:   "energy.read",
		},
		BatterySoc:  &batterySOC,
		DataQuality: sitev1.DataQuality_DATA_QUALITY_MEASURED,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = contract.DecodeEnergySiteObservation(payload, ingestedAt)
	if err == nil {
		t.Fatal("expected invalid battery state of charge to be rejected")
	}
	if !contract.IsPermanent(err) {
		t.Fatalf("a value outside its physical range can never become valid: %v", err)
	}
}
