package contract_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/contract"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
	evsev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/telemetry/evse/v1"
)

var ingestedAt = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func TestDecodeChargingTelemetry(t *testing.T) {
	payload, err := proto.Marshal(&evsev1.ChargingTelemetry{
		StationId:    "station-1",
		ConnectorId:  "1",
		TimestampMs:  1_700_000_000_000,
		Voltage:      400,
		Current:      75,
		PowerKw:      30,
		EnergyKwh:    20,
		Soc:          50,
		TemperatureC: 33,
		State:        evsev1.ChargingState_CHARGING_STATE_CHARGING,
		Edge: &evsev1.EdgeMetadata{
			EdgeId:       "edge-1",
			SiteId:       "site-1",
			ReceivedAtMs: 1_700_000_000_001,
			Sequence:     42,
			ClockSync:    evsev1.ClockSync_CLOCK_SYNC_SYNCHRONIZED,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	telemetry, err := contract.DecodeChargingTelemetry(payload, ingestedAt)
	if err != nil {
		t.Fatal(err)
	}
	if telemetry.StationID != "station-1" || telemetry.State != "CHARGING" || telemetry.SiteID != "site-1" {
		t.Fatalf("unexpected decoded telemetry: %#v", telemetry)
	}
	if telemetry.EdgeSequence != 42 {
		t.Errorf("edge sequence must survive decoding, got %d", telemetry.EdgeSequence)
	}
	if telemetry.ClockSync != domain.ClockSyncSynchronized {
		t.Errorf("clock synchronisation must survive decoding, got %q", telemetry.ClockSync)
	}
	if !telemetry.IngestedAt.Equal(ingestedAt) {
		t.Errorf("the caller's arrival stamp must be used, got %s", telemetry.IngestedAt)
	}
}

// A charger that has never synchronised its clock still reports. The record is
// accepted, but it must be labelled so nothing downstream trusts its timestamp.
func TestDecodeChargingTelemetryAcceptsButFlagsUnsynchronizedClock(t *testing.T) {
	payload, err := proto.Marshal(&evsev1.ChargingTelemetry{
		StationId:   "station-1",
		ConnectorId: "1",
		TimestampMs: 1,
		State:       evsev1.ChargingState_CHARGING_STATE_AVAILABLE,
		Edge: &evsev1.EdgeMetadata{
			EdgeId:    "edge-1",
			SiteId:    "site-1",
			ClockSync: evsev1.ClockSync_CLOCK_SYNC_UNSYNCHRONIZED,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	telemetry, err := contract.DecodeChargingTelemetry(payload, ingestedAt)
	if err != nil {
		t.Fatalf("telemetry from an unsynchronised charger must still be accepted: %v", err)
	}
	if telemetry.ClockSync != domain.ClockSyncUnsynchronized {
		t.Errorf("expected the record to be flagged, got %q", telemetry.ClockSync)
	}
}

func TestDecodeChargingTelemetryRejectionsArePermanent(t *testing.T) {
	cases := map[string]*evsev1.ChargingTelemetry{
		"missing station": {
			ConnectorId: "1", TimestampMs: 1,
			State: evsev1.ChargingState_CHARGING_STATE_CHARGING,
			Edge:  &evsev1.EdgeMetadata{EdgeId: "edge-1", SiteId: "site-1"},
		},
		"unspecified state": {
			StationId: "station-1", ConnectorId: "1", TimestampMs: 1,
			Edge: &evsev1.EdgeMetadata{EdgeId: "edge-1", SiteId: "site-1"},
		},
		"missing edge provenance": {
			StationId: "station-1", ConnectorId: "1", TimestampMs: 1,
			State: evsev1.ChargingState_CHARGING_STATE_CHARGING,
			Edge:  &evsev1.EdgeMetadata{},
		},
	}

	for name, message := range cases {
		t.Run(name, func(t *testing.T) {
			payload, err := proto.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			_, err = contract.DecodeChargingTelemetry(payload, ingestedAt)
			if err == nil {
				t.Fatal("expected the payload to be rejected")
			}
			// Permanent classification is what stops a corrupt device from
			// being redelivered forever.
			if !contract.IsPermanent(err) {
				t.Fatalf("expected a permanent rejection, got %v", err)
			}
		})
	}
}
