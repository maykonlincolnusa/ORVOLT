//! Addressing a device's own subject.
//!
//! Every device publishes below the base subject, under its own identity:
//!
//! ```text
//! orvolt.telemetry.evse.v1.edge-0001
//! ```
//!
//! NATS enforces which subjects a credential is allowed to publish to, so this
//! turns the broker's permission model into an authenticated statement of who
//! sent each message. The control plane then compares that against the identity
//! inside the payload, which a device could otherwise set to anything.

use thiserror::Error;

/// The longest identity that still fits comfortably in a subject token.
const MAX_IDENTITY_BYTES: usize = 128;

#[derive(Debug, Error, PartialEq)]
pub enum SubjectError {
    #[error("device identity must not be empty")]
    Empty,
    #[error("device identity must be at most {MAX_IDENTITY_BYTES} bytes")]
    TooLong,
    #[error("device identity may only contain letters, digits, '-' and '_': found {0:?}")]
    IllegalCharacter(char),
}

/// Builds the subject this device publishes to.
///
/// The identity is validated rather than escaped. A '.' would silently create
/// an extra subject token and a '*' or '>' would be a wildcard, either of which
/// would let a careless identity read as a different device — or as all of
/// them.
pub fn device_subject(base: &str, identity: &str) -> Result<String, SubjectError> {
    validate_identity(identity)?;
    Ok(format!("{base}.{identity}"))
}

pub fn validate_identity(identity: &str) -> Result<(), SubjectError> {
    if identity.is_empty() {
        return Err(SubjectError::Empty);
    }
    if identity.len() > MAX_IDENTITY_BYTES {
        return Err(SubjectError::TooLong);
    }
    for character in identity.chars() {
        let allowed = character.is_ascii_alphanumeric() || character == '-' || character == '_';
        if !allowed {
            return Err(SubjectError::IllegalCharacter(character));
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn builds_a_device_subject() {
        assert_eq!(
            device_subject("orvolt.telemetry.evse.v1", "edge-0001").unwrap(),
            "orvolt.telemetry.evse.v1.edge-0001"
        );
    }

    // A dot would create an extra subject token, so the device would publish
    // somewhere the control plane reads as a different identity.
    #[test]
    fn rejects_an_identity_that_would_split_the_subject() {
        assert_eq!(
            device_subject("orvolt.telemetry.evse.v1", "edge.0001"),
            Err(SubjectError::IllegalCharacter('.'))
        );
    }

    // A wildcard identity would address every device at once.
    #[test]
    fn rejects_wildcard_identities() {
        for wildcard in ['*', '>'] {
            assert_eq!(
                device_subject("orvolt.telemetry.evse.v1", &format!("edge{wildcard}")),
                Err(SubjectError::IllegalCharacter(wildcard))
            );
        }
    }

    #[test]
    fn rejects_empty_and_oversized_identities() {
        assert_eq!(device_subject("base", ""), Err(SubjectError::Empty));
        let long = "e".repeat(MAX_IDENTITY_BYTES + 1);
        assert_eq!(device_subject("base", &long), Err(SubjectError::TooLong));
    }

    #[test]
    fn accepts_conventional_identities() {
        for identity in ["edge-0001", "edge_0001", "EDGE0001", "a1"] {
            assert!(validate_identity(identity).is_ok(), "rejected {identity}");
        }
    }
}
