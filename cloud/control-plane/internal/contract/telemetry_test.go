package contract_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/contract"
	evsev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/telemetry/evse/v1"
)

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
		State:        evsev1.ChargingState_CHARGING,
		Edge:         &evsev1.EdgeMetadata{EdgeId: "edge-1", SiteId: "site-1", ReceivedAtMs: 1_700_000_000_001},
	})
	if err != nil {
		t.Fatal(err)
	}

	telemetry, err := contract.DecodeChargingTelemetry(payload)
	if err != nil {
		t.Fatal(err)
	}
	if telemetry.StationID != "station-1" || telemetry.State != "CHARGING" || telemetry.SiteID != "site-1" {
		t.Fatalf("unexpected decoded telemetry: %#v", telemetry)
	}
}
