# ADR-003: Event-driven telemetry

## Status

Accepted

## Decision

Publish normalized telemetry to NATS JetStream rather than synchronously posting it to the cloud API. The control plane consumes with a durable subscription and persists an immutable history plus a last-known-state projection.

## Consequences

This provides replay, decoupling, and an integration point for future telemetry storage without imposing cloud availability on the edge. Consumers must tolerate at-least-once delivery and make projections idempotent by timestamp.
