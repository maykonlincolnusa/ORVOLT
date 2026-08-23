use orvolt_edge_agent::{
    normalize, parse_payload, proto::ChargingState, RawTelemetry, ValidationError,
};

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
        "edge-a",
        "site-a",
        1_700_000_000_010,
    )
    .unwrap();

    assert_eq!(telemetry.state, ChargingState::Charging as i32);
    assert_eq!(telemetry.edge.unwrap().site_id, "site-a");
}

#[test]
fn rejects_out_of_range_telemetry() {
    let mut invalid = raw();
    invalid.soc = 101.0;
    assert!(matches!(
        normalize(invalid, "edge-a", "site-a", 1),
        Err(ValidationError::OutOfRange { field: "soc", .. })
    ));
}

#[test]
fn rejects_unknown_state() {
    let mut invalid = raw();
    invalid.state = "Teleporting".into();
    assert!(matches!(
        normalize(invalid, "edge-a", "site-a", 1),
        Err(ValidationError::InvalidState(_))
    ));
}
