// Package csms translates OCPP 1.6J into ORVOLT's canonical contracts.
//
// Everything in this file is a pure function. Unit conversion and enum
// translation are where protocol adapters actually go wrong — a charge point
// reporting watt-hours where the code assumed kilowatt-hours produces an
// invoice that is wrong by a factor of a thousand — so the conversion logic is
// kept free of sockets, clocks and brokers, and is tested directly.
package csms

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"

	sessionv1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/session/evse/v1"
	evsev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/telemetry/evse/v1"
)

// PlausibleEpochMs is 2025-01-01T00:00:00Z. A charge point reporting a time
// before this has not had its clock set; the reading is still useful but must
// not be trusted for ordering. The edge agent applies the same floor.
const PlausibleEpochMs int64 = 1_735_689_600_000

// Origin identifies the gateway instance that observed the charge point. It
// plays the same role that an edge agent's identity plays for a site-local
// station.
type Origin struct {
	GatewayID string
	SiteID    string
}

// SessionID builds a stable identifier that joins a StartTransaction to its
// StopTransaction. It is derived from the charge point and the OCPP transaction
// identifier so the two ends of one transaction always agree, even if they are
// separated by a reconnect.
func SessionID(chargePointID string, transactionID int) string {
	return fmt.Sprintf("ocpp16:%s:%d", chargePointID, transactionID)
}

var chargePointStates = map[core.ChargePointStatus]evsev1.ChargingState{
	core.ChargePointStatusAvailable:   evsev1.ChargingState_CHARGING_STATE_AVAILABLE,
	core.ChargePointStatusPreparing:   evsev1.ChargingState_CHARGING_STATE_PREPARING,
	core.ChargePointStatusCharging:    evsev1.ChargingState_CHARGING_STATE_CHARGING,
	core.ChargePointStatusFinishing:   evsev1.ChargingState_CHARGING_STATE_FINISHING,
	core.ChargePointStatusFaulted:     evsev1.ChargingState_CHARGING_STATE_FAULTED,
	core.ChargePointStatusReserved:    evsev1.ChargingState_CHARGING_STATE_RESERVED,
	core.ChargePointStatusUnavailable: evsev1.ChargingState_CHARGING_STATE_UNAVAILABLE,
	// Suspended is reported separately rather than folded into CHARGING: a
	// session that stopped delivering energy but still occupies a connector is
	// a fault condition an operator needs to see.
	core.ChargePointStatusSuspendedEV:   evsev1.ChargingState_CHARGING_STATE_SUSPENDED,
	core.ChargePointStatusSuspendedEVSE: evsev1.ChargingState_CHARGING_STATE_SUSPENDED,
}

// ChargingStateOf translates an OCPP connector status.
func ChargingStateOf(status core.ChargePointStatus) evsev1.ChargingState {
	if state, known := chargePointStates[status]; known {
		return state
	}
	return evsev1.ChargingState_CHARGING_STATE_UNSPECIFIED
}

var stopReasons = map[core.Reason]sessionv1.StopReason{
	core.ReasonLocal:          sessionv1.StopReason_STOP_REASON_LOCAL,
	core.ReasonRemote:         sessionv1.StopReason_STOP_REASON_REMOTE,
	core.ReasonEVDisconnected: sessionv1.StopReason_STOP_REASON_EV_DISCONNECTED,
	core.ReasonEmergencyStop:  sessionv1.StopReason_STOP_REASON_EMERGENCY_STOP,
	core.ReasonPowerLoss:      sessionv1.StopReason_STOP_REASON_POWER_LOSS,
	core.ReasonDeAuthorized:   sessionv1.StopReason_STOP_REASON_DE_AUTHORIZED,
	// A reset or an unlock is an operational action rather than a distinct
	// billing outcome, so they share OTHER instead of inventing enum values
	// that no consumer would treat differently.
	core.ReasonHardReset:     sessionv1.StopReason_STOP_REASON_OTHER,
	core.ReasonSoftReset:     sessionv1.StopReason_STOP_REASON_OTHER,
	core.ReasonReboot:        sessionv1.StopReason_STOP_REASON_OTHER,
	core.ReasonUnlockCommand: sessionv1.StopReason_STOP_REASON_OTHER,
	core.ReasonOther:         sessionv1.StopReason_STOP_REASON_OTHER,
}

func StopReasonOf(reason core.Reason) sessionv1.StopReason {
	if mapped, known := stopReasons[reason]; known {
		return mapped
	}
	return sessionv1.StopReason_STOP_REASON_UNSPECIFIED
}

// ClockSyncOf judges the charge point's own timestamp.
func ClockSyncOf(reportedAtMs int64) evsev1.ClockSync {
	if reportedAtMs >= PlausibleEpochMs {
		return evsev1.ClockSync_CLOCK_SYNC_SYNCHRONIZED
	}
	return evsev1.ClockSync_CLOCK_SYNC_UNSYNCHRONIZED
}

// Reading is one connector's measurements assembled from a MeterValues sample.
// Absent measurands stay absent rather than defaulting to zero: a charge point
// that does not report temperature is not reporting 0 °C.
type Reading struct {
	Voltage      *float64
	Current      *float64
	PowerKW      *float64
	EnergyKWh    *float64
	SOC          *float64
	TemperatureC *float64
}

// MeterValueToReading converts one OCPP meter value into ORVOLT units.
//
// OCPP 1.6 defaults are unit-dependent: Energy.Active.Import.Register defaults
// to Wh and Power.Active.Import to W, while ORVOLT's contract carries kWh and
// kW. Getting this wrong is a thousand-fold billing error, so each measurand is
// converted explicitly and unknown units are dropped rather than guessed.
func MeterValueToReading(value types.MeterValue) Reading {
	var reading Reading
	for _, sample := range value.SampledValue {
		number, err := strconv.ParseFloat(strings.TrimSpace(sample.Value), 64)
		if err != nil {
			continue
		}
		switch measurandOf(sample) {
		case types.MeasurandVoltage:
			if converted, ok := toVolts(number, sample.Unit); ok {
				reading.Voltage = &converted
			}
		case types.MeasurandCurrentImport:
			if sample.Unit == "" || sample.Unit == types.UnitOfMeasureA {
				current := number
				reading.Current = &current
			}
		case types.MeasurandPowerActiveImport:
			if converted, ok := toKilowatts(number, sample.Unit); ok {
				reading.PowerKW = &converted
			}
		case types.MeasurandEnergyActiveImportRegister:
			if converted, ok := toKilowattHours(number, sample.Unit); ok {
				reading.EnergyKWh = &converted
			}
		case types.MeasurandSoC:
			soc := number
			reading.SOC = &soc
		case types.MeasurandTemperature:
			if converted, ok := toCelsius(number, sample.Unit); ok {
				reading.TemperatureC = &converted
			}
		}
	}
	return reading
}

// measurandOf applies the OCPP 1.6 default: a sample with no measurand is the
// cumulative active-import energy register.
func measurandOf(sample types.SampledValue) types.Measurand {
	if sample.Measurand == "" {
		return types.MeasurandEnergyActiveImportRegister
	}
	return sample.Measurand
}

func toVolts(value float64, unit types.UnitOfMeasure) (float64, bool) {
	switch unit {
	case "", types.UnitOfMeasureV:
		return value, true
	default:
		return 0, false
	}
}

func toKilowatts(value float64, unit types.UnitOfMeasure) (float64, bool) {
	switch unit {
	case "", types.UnitOfMeasureW:
		return value / 1000, true
	case types.UnitOfMeasureKW:
		return value, true
	default:
		return 0, false
	}
}

func toKilowattHours(value float64, unit types.UnitOfMeasure) (float64, bool) {
	switch unit {
	case "", types.UnitOfMeasureWh:
		return value / 1000, true
	case types.UnitOfMeasureKWh:
		return value, true
	default:
		return 0, false
	}
}

func toCelsius(value float64, unit types.UnitOfMeasure) (float64, bool) {
	switch unit {
	// The OCPP 1.6 schema and several implementations disagree on the spelling
	// of this unit, so both are accepted.
	case "", types.UnitOfMeasureCelsius, types.UnitOfMeasureCelcius:
		return value, true
	case types.UnitOfMeasureFahrenheit:
		return (value - 32) * 5 / 9, true
	case types.UnitOfMeasureK:
		return value - 273.15, true
	default:
		return 0, false
	}
}

// EnergyRegisterWh extracts the cumulative import register in watt-hours, which
// is the unit the session contract stores because it is what OCPP's
// StartTransaction and StopTransaction carry.
func EnergyRegisterWh(value types.MeterValue) (int64, bool) {
	for _, sample := range value.SampledValue {
		if measurandOf(sample) != types.MeasurandEnergyActiveImportRegister {
			continue
		}
		number, err := strconv.ParseFloat(strings.TrimSpace(sample.Value), 64)
		if err != nil {
			continue
		}
		switch sample.Unit {
		case "", types.UnitOfMeasureWh:
			return int64(number), true
		case types.UnitOfMeasureKWh:
			return int64(number * 1000), true
		}
	}
	return 0, false
}

// TelemetryFrom assembles a canonical telemetry event.
func TelemetryFrom(
	origin Origin,
	chargePointID string,
	connectorID int,
	state evsev1.ChargingState,
	reading Reading,
	reportedAt time.Time,
	observedAt time.Time,
	sequence uint64,
) *evsev1.ChargingTelemetry {
	reportedAtMs := reportedAt.UnixMilli()
	return &evsev1.ChargingTelemetry{
		StationId:    chargePointID,
		ConnectorId:  strconv.Itoa(connectorID),
		TimestampMs:  reportedAtMs,
		Voltage:      valueOr(reading.Voltage),
		Current:      valueOr(reading.Current),
		PowerKw:      valueOr(reading.PowerKW),
		EnergyKwh:    valueOr(reading.EnergyKWh),
		Soc:          valueOr(reading.SOC),
		TemperatureC: valueOr(reading.TemperatureC),
		State:        state,
		Edge: &evsev1.EdgeMetadata{
			EdgeId:       origin.GatewayID,
			SiteId:       origin.SiteID,
			ReceivedAtMs: observedAt.UnixMilli(),
			Sequence:     sequence,
			ClockSync:    ClockSyncOf(reportedAtMs),
		},
	}
}

func valueOr(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

// SessionEvent assembles a canonical charging-session event.
func SessionEvent(
	origin Origin,
	chargePointID string,
	connectorID int,
	transactionID int,
	eventType sessionv1.SessionEventType,
	occurredAt time.Time,
	authorization *sessionv1.Authorization,
	registerWh *int64,
	stopReason sessionv1.StopReason,
) *sessionv1.ChargingSessionEvent {
	event := &sessionv1.ChargingSessionEvent{
		SessionId:            SessionID(chargePointID, transactionID),
		StationId:            chargePointID,
		ConnectorId:          strconv.Itoa(connectorID),
		SiteId:               origin.SiteID,
		Type:                 eventType,
		OccurredAtMs:         occurredAt.UnixMilli(),
		Authorization:        authorization,
		StopReason:           stopReason,
		Source:               sessionv1.SourceProtocol_SOURCE_PROTOCOL_OCPP_1_6,
		TransactionReference: strconv.Itoa(transactionID),
	}
	if registerWh != nil {
		event.Meter = &sessionv1.MeterSnapshot{
			EnergyRegisterWh: *registerWh,
			MeasuredAtMs:     occurredAt.UnixMilli(),
		}
	}
	return event
}
