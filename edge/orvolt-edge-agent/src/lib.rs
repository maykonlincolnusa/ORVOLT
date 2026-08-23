use prost::Message;
use serde::Deserialize;
use thiserror::Error;

pub mod proto {
    include!(concat!(env!("OUT_DIR"), "/orvolt.telemetry.evse.v1.rs"));
}

use proto::{ChargingState, ChargingTelemetry, EdgeMetadata};

#[derive(Debug, Clone, Deserialize)]
pub struct RawTelemetry {
    pub station_id: String,
    pub connector_id: String,
    pub timestamp_ms: i64,
    pub state: String,
    pub voltage: f64,
    pub current: f64,
    pub power_kw: f64,
    pub energy_kwh: f64,
    pub soc: f64,
    pub temperature_c: f64,
}

#[derive(Debug, Error, PartialEq)]
pub enum ValidationError {
    #[error("{field} must be non-empty and at most 128 bytes")]
    InvalidIdentifier { field: &'static str },
    #[error("timestamp_ms must be positive")]
    InvalidTimestamp,
    #[error("unsupported charging state: {0}")]
    InvalidState(String),
    #[error("{field} must be finite and between {minimum} and {maximum}")]
    OutOfRange {
        field: &'static str,
        minimum: f64,
        maximum: f64,
    },
}

pub fn parse_payload(payload: &[u8]) -> Result<RawTelemetry, serde_json::Error> {
    serde_json::from_slice(payload)
}

pub fn normalize(
    raw: RawTelemetry,
    edge_id: &str,
    site_id: &str,
    received_at_ms: i64,
) -> Result<ChargingTelemetry, ValidationError> {
    validate_identifier("station_id", &raw.station_id)?;
    validate_identifier("connector_id", &raw.connector_id)?;
    validate_identifier("edge_id", edge_id)?;
    validate_identifier("site_id", site_id)?;
    if raw.timestamp_ms <= 0 || received_at_ms <= 0 {
        return Err(ValidationError::InvalidTimestamp);
    }

    validate_range("voltage", raw.voltage, 0.0, 1_000.0)?;
    validate_range("current", raw.current, 0.0, 1_000.0)?;
    validate_range("power_kw", raw.power_kw, 0.0, 500.0)?;
    validate_range("energy_kwh", raw.energy_kwh, 0.0, 10_000_000.0)?;
    validate_range("soc", raw.soc, 0.0, 100.0)?;
    validate_range("temperature_c", raw.temperature_c, -50.0, 150.0)?;

    Ok(ChargingTelemetry {
        station_id: raw.station_id,
        connector_id: raw.connector_id,
        timestamp_ms: raw.timestamp_ms,
        voltage: raw.voltage,
        current: raw.current,
        power_kw: raw.power_kw,
        energy_kwh: raw.energy_kwh,
        soc: raw.soc,
        temperature_c: raw.temperature_c,
        state: state_from_str(&raw.state)? as i32,
        edge: Some(EdgeMetadata {
            edge_id: edge_id.to_owned(),
            site_id: site_id.to_owned(),
            received_at_ms,
        }),
    })
}

pub fn encode(telemetry: &ChargingTelemetry) -> Vec<u8> {
    telemetry.encode_to_vec()
}

fn validate_identifier(field: &'static str, value: &str) -> Result<(), ValidationError> {
    if value.is_empty() || value.len() > 128 {
        return Err(ValidationError::InvalidIdentifier { field });
    }
    Ok(())
}

fn validate_range(
    field: &'static str,
    value: f64,
    minimum: f64,
    maximum: f64,
) -> Result<(), ValidationError> {
    if !value.is_finite() || value < minimum || value > maximum {
        return Err(ValidationError::OutOfRange {
            field,
            minimum,
            maximum,
        });
    }
    Ok(())
}

fn state_from_str(state: &str) -> Result<ChargingState, ValidationError> {
    match state {
        "Available" => Ok(ChargingState::Available),
        "Preparing" => Ok(ChargingState::Preparing),
        "Charging" => Ok(ChargingState::Charging),
        "Finishing" => Ok(ChargingState::Finishing),
        "Faulted" => Ok(ChargingState::Faulted),
        other => Err(ValidationError::InvalidState(other.to_owned())),
    }
}
