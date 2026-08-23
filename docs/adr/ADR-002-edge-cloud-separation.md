# ADR-002: Edge-cloud separation

## Status

Accepted

## Decision

The edge agent is the local telemetry validation and normalization boundary. Cloud services consume durable events and maintain fleet projections, but are excluded from electrical safety decisions.

## Consequences

Cloud outages can delay observation and management data, but cannot be relied on for emergency stop, contactor protection, electrical limits, or watchdog behavior. Future command work must remain mediated by local runtime and hardware controls.
