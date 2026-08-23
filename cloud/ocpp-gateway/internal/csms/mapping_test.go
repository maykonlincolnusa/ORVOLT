package csms

import (
	"math"
	"testing"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"

	sessionv1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/session/evse/v1"
	evsev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/telemetry/evse/v1"
)

func sample(measurand types.Measurand, unit types.UnitOfMeasure, value string) types.SampledValue {
	return types.SampledValue{Value: value, Measurand: measurand, Unit: unit}
}

func meterValue(samples ...types.SampledValue) types.MeterValue {
	return types.MeterValue{Timestamp: types.NewDateTime(time.Unix(1_800_000_000, 0)), SampledValue: samples}
}

func requireFloat(t *testing.T, label string, actual *float64, expected float64) {
	t.Helper()
	if actual == nil {
		t.Fatalf("%s: expected %v, got no value", label, expected)
	}
	if math.Abs(*actual-expected) > 1e-9 {
		t.Errorf("%s: expected %v, got %v", label, expected, *actual)
	}
}

// OCPP 1.6 defaults the energy register to watt-hours while ORVOLT stores
// kilowatt-hours. Missing the conversion is a thousand-fold billing error.
func TestEnergyRegisterUnitsAreConverted(t *testing.T) {
	cases := map[string]struct {
		unit     types.UnitOfMeasure
		raw      string
		expected float64
	}{
		"watt-hours are converted":    {types.UnitOfMeasureWh, "18000", 18},
		"kilowatt-hours pass through": {types.UnitOfMeasureKWh, "18", 18},
		"absent unit is watt-hours":   {"", "18000", 18},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			reading := MeterValueToReading(meterValue(
				sample(types.MeasurandEnergyActiveImportRegister, testCase.unit, testCase.raw)))
			requireFloat(t, "energy", reading.EnergyKWh, testCase.expected)
		})
	}
}

func TestPowerUnitsAreConverted(t *testing.T) {
	watts := MeterValueToReading(meterValue(
		sample(types.MeasurandPowerActiveImport, types.UnitOfMeasureW, "22000")))
	requireFloat(t, "power from W", watts.PowerKW, 22)

	kilowatts := MeterValueToReading(meterValue(
		sample(types.MeasurandPowerActiveImport, types.UnitOfMeasureKW, "22")))
	requireFloat(t, "power from kW", kilowatts.PowerKW, 22)

	// OCPP's default unit for power is watts.
	implicit := MeterValueToReading(meterValue(
		sample(types.MeasurandPowerActiveImport, "", "22000")))
	requireFloat(t, "power with no unit", implicit.PowerKW, 22)
}

func TestTemperatureUnitsAreConverted(t *testing.T) {
	fahrenheit := MeterValueToReading(meterValue(
		sample(types.MeasurandTemperature, types.UnitOfMeasureFahrenheit, "212")))
	requireFloat(t, "temperature from F", fahrenheit.TemperatureC, 100)

	kelvin := MeterValueToReading(meterValue(
		sample(types.MeasurandTemperature, types.UnitOfMeasureK, "273.15")))
	requireFloat(t, "temperature from K", kelvin.TemperatureC, 0)

	// The OCPP 1.6 schema and real implementations disagree on this spelling.
	misspelled := MeterValueToReading(meterValue(
		sample(types.MeasurandTemperature, types.UnitOfMeasureCelcius, "31.5")))
	requireFloat(t, "temperature from misspelled Celsius", misspelled.TemperatureC, 31.5)
}

// A sample with no measurand is the energy register, not a voltage.
func TestSampleWithoutMeasurandIsTheEnergyRegister(t *testing.T) {
	reading := MeterValueToReading(meterValue(sample("", "", "4200")))
	requireFloat(t, "energy", reading.EnergyKWh, 4.2)
	if reading.Voltage != nil {
		t.Errorf("an unlabelled sample must not be read as voltage, got %v", *reading.Voltage)
	}
}

// Absent is not zero: a station that does not report temperature is not
// reporting 0 °C, and storing zero would be indistinguishable from a reading.
func TestUnreportedMeasurandsStayAbsent(t *testing.T) {
	reading := MeterValueToReading(meterValue(
		sample(types.MeasurandVoltage, types.UnitOfMeasureV, "400")))
	requireFloat(t, "voltage", reading.Voltage, 400)
	if reading.TemperatureC != nil || reading.SOC != nil || reading.PowerKW != nil {
		t.Errorf("unreported measurands must stay absent: %+v", reading)
	}
}

func TestUnknownUnitIsDroppedRatherThanGuessed(t *testing.T) {
	reading := MeterValueToReading(meterValue(
		sample(types.MeasurandPowerActiveImport, types.UnitOfMeasureA, "17")))
	if reading.PowerKW != nil {
		t.Errorf("a power sample reported in amperes must be dropped, got %v", *reading.PowerKW)
	}
}

func TestUnparseableValueIsIgnored(t *testing.T) {
	reading := MeterValueToReading(meterValue(
		sample(types.MeasurandVoltage, types.UnitOfMeasureV, "not-a-number")))
	if reading.Voltage != nil {
		t.Errorf("a non-numeric sample must be ignored, got %v", *reading.Voltage)
	}
}

func TestEnergyRegisterInWattHours(t *testing.T) {
	register, found := EnergyRegisterWh(meterValue(
		sample(types.MeasurandEnergyActiveImportRegister, types.UnitOfMeasureKWh, "12.5")))
	if !found || register != 12500 {
		t.Fatalf("expected 12500 Wh, got %d (found=%v)", register, found)
	}
}

// Suspended must not collapse into Charging: a session that stopped delivering
// energy while still holding a connector is exactly what an operator needs to
// see, and reporting it as Charging hides it.
func TestSuspendedStatusIsNotReportedAsCharging(t *testing.T) {
	for _, status := range []core.ChargePointStatus{
		core.ChargePointStatusSuspendedEV,
		core.ChargePointStatusSuspendedEVSE,
	} {
		if state := ChargingStateOf(status); state != evsev1.ChargingState_CHARGING_STATE_SUSPENDED {
			t.Errorf("%s mapped to %s", status, state)
		}
	}
}

func TestConnectorStatusMapping(t *testing.T) {
	cases := map[core.ChargePointStatus]evsev1.ChargingState{
		core.ChargePointStatusAvailable:   evsev1.ChargingState_CHARGING_STATE_AVAILABLE,
		core.ChargePointStatusPreparing:   evsev1.ChargingState_CHARGING_STATE_PREPARING,
		core.ChargePointStatusCharging:    evsev1.ChargingState_CHARGING_STATE_CHARGING,
		core.ChargePointStatusFinishing:   evsev1.ChargingState_CHARGING_STATE_FINISHING,
		core.ChargePointStatusFaulted:     evsev1.ChargingState_CHARGING_STATE_FAULTED,
		core.ChargePointStatusReserved:    evsev1.ChargingState_CHARGING_STATE_RESERVED,
		core.ChargePointStatusUnavailable: evsev1.ChargingState_CHARGING_STATE_UNAVAILABLE,
	}
	for status, expected := range cases {
		if state := ChargingStateOf(status); state != expected {
			t.Errorf("%s mapped to %s, expected %s", status, state, expected)
		}
	}
	if ChargingStateOf("SomethingNewInAFutureSpec") != evsev1.ChargingState_CHARGING_STATE_UNSPECIFIED {
		t.Error("an unknown status must not be guessed into a nearby state")
	}
}

func TestStopReasonMapping(t *testing.T) {
	if StopReasonOf(core.ReasonEVDisconnected) != sessionv1.StopReason_STOP_REASON_EV_DISCONNECTED {
		t.Error("EVDisconnected must survive translation")
	}
	if StopReasonOf(core.ReasonHardReset) != sessionv1.StopReason_STOP_REASON_OTHER {
		t.Error("an operational reset is not a distinct billing outcome")
	}
	if StopReasonOf("") != sessionv1.StopReason_STOP_REASON_UNSPECIFIED {
		t.Error("an omitted reason must stay unspecified")
	}
}

// The identifier is what joins a StartTransaction to the StopTransaction that
// closes it, possibly across a reconnect.
func TestSessionIDJoinsBothEndsOfATransaction(t *testing.T) {
	start := SessionID("CP-0001", 4711)
	stop := SessionID("CP-0001", 4711)
	if start != stop {
		t.Fatalf("the same transaction produced two identifiers: %s and %s", start, stop)
	}
	if SessionID("CP-0002", 4711) == start {
		t.Error("two charge points must not share a session identifier")
	}
}

func TestClockSyncFollowsTheChargePointTimestamp(t *testing.T) {
	if ClockSyncOf(0) != evsev1.ClockSync_CLOCK_SYNC_UNSYNCHRONIZED {
		t.Error("a charge point reporting the epoch has not had its clock set")
	}
	if ClockSyncOf(PlausibleEpochMs) != evsev1.ClockSync_CLOCK_SYNC_SYNCHRONIZED {
		t.Error("a plausible timestamp must be trusted")
	}
}

func TestTelemetryCarriesGatewayProvenance(t *testing.T) {
	origin := Origin{GatewayID: "gw-1", SiteID: "site-9"}
	reported := time.UnixMilli(1_800_000_000_000).UTC()
	telemetry := TelemetryFrom(origin, "CP-1", 2,
		evsev1.ChargingState_CHARGING_STATE_CHARGING, Reading{}, reported, reported, 12)

	if telemetry.GetEdge().GetEdgeId() != "gw-1" || telemetry.GetEdge().GetSiteId() != "site-9" {
		t.Errorf("provenance was lost: %+v", telemetry.GetEdge())
	}
	if telemetry.GetConnectorId() != "2" {
		t.Errorf("connector id must be rendered as text, got %q", telemetry.GetConnectorId())
	}
	if telemetry.GetEdge().GetSequence() != 12 {
		t.Errorf("sequence must survive, got %d", telemetry.GetEdge().GetSequence())
	}
}
