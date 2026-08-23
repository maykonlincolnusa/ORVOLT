use orvolt_edge_agent::clock::ClockTrust;
use orvolt_edge_agent::{
    normalize, parse_payload, proto::ChargingState, proto::ClockSync, EdgeContext, RawTelemetry,
    ValidationError,
};

fn context(clock: ClockTrust) -> EdgeContext<'static> {
    EdgeContext {
        edge_id: "edge-a",
        site_id: "site-a",
        received_at_ms: 1_700_000_000_010,
        sequence: 5,
        clock,
    }
}

fn raw() -> RawTelemetry {
    RawTelemetry {
        station_id: "station-a".into(),
        connector_id: "1".into(),
        timestamp_ms: 1_700_000_000_000,
        state: "Charging".into(),
        voltage: 400.0,
        current: 75.0,
        power_kw: 30.0,
        energy_kwh: 20.0,
        soc: 50.0,
        temperature_c: 33.0,
    }
}

#[test]
fn parses_and_normalizes_telemetry_with_edge_metadata() {
    let input = br#"{"station_id":"station-a","connector_id":"1","timestamp_ms":1700000000000,"state":"Charging","voltage":400.0,"current":75.0,"power_kw":30.0,"energy_kwh":20.0,"soc":50.0,"temperature_c":33.0}"#;
    let telemetry = normalize(
        parse_payload(input).unwrap(),
        context(ClockTrust::Synchronized),
    )
    .unwrap();

    assert_eq!(telemetry.state, ChargingState::Charging as i32);
    let edge = telemetry.edge.unwrap();
    assert_eq!(edge.site_id, "site-a");
    assert_eq!(edge.sequence, 5);
    assert_eq!(edge.clock_sync, ClockSync::Synchronized as i32);
}

/// A charger whose clock was never set still produces useful readings. They
/// must be published, but labelled so nothing downstream orders by their
/// timestamp.
#[test]
fn labels_telemetry_produced_with_an_untrusted_clock() {
    let telemetry = normalize(raw(), context(ClockTrust::Unsynchronized)).unwrap();
    let edge = telemetry.edge.unwrap();
    assert_eq!(edge.clock_sync, ClockSync::Unsynchronized as i32);
}

#[test]
fn rejects_out_of_range_telemetry() {
    let mut invalid = raw();
    invalid.soc = 101.0;
    assert!(matches!(
        normalize(invalid, context(ClockTrust::Synchronized)),
        Err(ValidationError::OutOfRange { field: "soc", .. })
    ));
}

#[test]
fn rejects_unknown_state() {
    let mut invalid = raw();
    invalid.state = "Teleporting".into();
    assert!(matches!(
        normalize(invalid, context(ClockTrust::Synchronized)),
        Err(ValidationError::InvalidState(_))
    ));
}

#[test]
fn rejects_missing_site_identity() {
    let mut blank = context(ClockTrust::Synchronized);
    blank.site_id = "";
    assert!(matches!(
        normalize(raw(), blank),
        Err(ValidationError::InvalidIdentifier { field: "site_id" })
    ));
}
