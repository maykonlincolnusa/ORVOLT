package contract

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	evsev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/telemetry/evse/v1"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
)

func DecodeChargingTelemetry(payload []byte) (domain.Telemetry, error) {
	var message evsev1.ChargingTelemetry
	if err := proto.Unmarshal(payload, &message); err != nil {
		return domain.Telemetry{}, fmt.Errorf("decode charging telemetry: %w", err)
	}
	if message.GetStationId() == "" || message.GetConnectorId() == "" || message.GetTimestampMs() <= 0 || message.GetEdge() == nil {
		return domain.Telemetry{}, fmt.Errorf("charging telemetry is missing required operational fields")
	}
	state := message.GetState().String()
	if state == "CHARGING_STATE_UNSPECIFIED" {
		return domain.Telemetry{}, fmt.Errorf("charging telemetry state is unspecified")
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
		EdgeID:       message.GetEdge().GetEdgeId(),
		SiteID:       message.GetEdge().GetSiteId(),
		ReceivedAt:   time.UnixMilli(message.GetEdge().GetReceivedAtMs()).UTC(),
	}, nil
}
