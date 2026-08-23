//! ORVOLT edge agent.
//!
//! The agent is the site-local boundary between an EVSE runtime and the cloud.
//! It validates what the station reports, labels it with the site's identity and
//! clock trust, and guarantees delivery across a link that is expected to fail.
//!
//! The logic is split so that the parts which decide whether data may be
//! discarded are testable without a broker, a network or a charger:
//!
//! * [`telemetry`] — validation and normalisation into the canonical contract.
//! * [`spool`] — durable, bounded store-and-forward on local flash.
//! * [`pipeline`] — delivery policy over an abstract transport.
//! * [`clock`] — wall-clock trust and observation ordering.
//! * [`observability`] — local health and metrics for on-site diagnosis.

pub mod clock;
pub mod observability;
pub mod pipeline;
pub mod spool;
pub mod telemetry;

pub mod proto {
    include!(concat!(env!("OUT_DIR"), "/orvolt.telemetry.evse.v1.rs"));
}

pub use telemetry::{encode, normalize, parse_payload, EdgeContext, RawTelemetry, ValidationError};
