package contract

import (
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
	evsev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/telemetry/evse/v1"
)

// chargingStates maps the wire enum to the stable text stored in PostgreSQL.
// The mapping is explicit so that renaming a Protobuf enum value can never
// silently rewrite historical rows.
var chargingStates = map[evsev1.ChargingState]string{
	evsev1.ChargingState_CHARGING_STATE_AVAILABLE: "AVAILABLE",
	evsev1.ChargingState_CHARGING_STATE_PREPARING: "PREPARING",
	evsev1.ChargingState_CHARGING_STATE_CHARGING:  "CHARGING",
	evsev1.ChargingState_CHARGING_STATE_FINISHING: "FINISHING",
	evsev1.ChargingState_CHARGING_STATE_FAULTED:   "FAULTED",
}

var clockSyncStates = map[evsev1.ClockSync]string{
	evsev1.ClockSync_CLOCK_SYNC_UNSPECIFIED:    domain.ClockSyncUnspecified,
	evsev1.ClockSync_CLOCK_SYNC_SYNCHRONIZED:   domain.ClockSyncSynchronized,
	evsev1.ClockSync_CLOCK_SYNC_UNSYNCHRONIZED: domain.ClockSyncUnsynchronized,
}

// DecodeChargingTelemetry validates a canonical telemetry event. ingestedAt is
// supplied by the caller rather than read from the clock here so that every
// record in one ingest batch shares a single arrival stamp and so that the
// decision is testable.
func DecodeChargingTelemetry(payload []byte, ingestedAt time.Time) (domain.Telemetry, error) {
	var message evsev1.ChargingTelemetry
	if err := proto.Unmarshal(payload, &message); err != nil {
		return domain.Telemetry{}, permanent("decode charging telemetry: %v", err)
	}
	if message.GetStationId() == "" || message.GetConnectorId() == "" || message.GetTimestampMs() <= 0 || message.GetEdge() == nil {
		return domain.Telemetry{}, permanent("charging telemetry is missing required operational fields")
	}
	state, known := chargingStates[message.GetState()]
	if !known {
		return domain.Telemetry{}, permanent("charging telemetry state is unspecified or unknown")
	}
	edge := message.GetEdge()
	if edge.GetEdgeId() == "" || edge.GetSiteId() == "" {
		return domain.Telemetry{}, permanent("charging telemetry is missing edge provenance")
	}
	clockSync, known := clockSyncStates[edge.GetClockSync()]
	if !known {
		clockSync = domain.ClockSyncUnspecified
	}

	return domain.Telemetry{
		StationID:    message.GetStationId(),
		ConnectorID:  message.GetConnectorId(),
		Timestamp:    time.UnixMilli(message.GetTimestampMs()).UTC(),
		Voltage:      message.GetVoltage(),
		Current:      message.GetCurrent(),
		PowerKW:      message.GetPowerKw(),
		EnergyKWh:    message.GetEnergyKwh(),
		SOC:          message.GetSoc(),
		TemperatureC: message.GetTemperatureC(),
		State:        state,
		EdgeID:       edge.GetEdgeId(),
		SiteID:       edge.GetSiteId(),
		ReceivedAt:   time.UnixMilli(edge.GetReceivedAtMs()).UTC(),
		EdgeSequence: edge.GetSequence(),
		ClockSync:    clockSync,
		IngestedAt:   ingestedAt,
	}, nil
}
