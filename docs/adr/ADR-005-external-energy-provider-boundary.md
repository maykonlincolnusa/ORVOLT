# ADR-005: External Energy Provider Boundary

## Status

Accepted

## Context

Solar, storage, tariff, demand-response, and vehicle-energy providers can improve planning but have different commercial terms, consent models, availability, and data semantics. Treating any provider as a local control authority would violate ORVOLT's edge-first safety architecture.

## Decision

Use isolated cloud adapters for authorized provider data. Begin with read-only normalized observations under a future `orvolt.energy.site.v1` namespace. Keep provider-specific semantics and credentials inside the adapter. Any future command is advisory until mediated by site-local policy, EVSE runtime, and hardware constraints.

## Consequences

Provider integrations can be added independently without affecting the MQTT-to-edge-to-JetStream telemetry flow. A provider outage or revoked authorization only removes optional optimization data. ORVOLT does not claim support, interoperability, or certification for a provider until a concrete adapter is implemented and validated.
