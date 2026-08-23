# ADR-004: Versioned Protocol Buffer contracts

## Status

Accepted

## Decision

Canonical service messages are Protocol Buffers grouped in versioned namespaces. The current implementation owns only `orvolt.telemetry.evse.v1`; event, command, and health namespaces are reserved and documented.

## Consequences

Schema changes must be additive within a version or published under a new version. Bindings are generated from `contracts/proto`, and backwards compatibility is checked with Buf when a baseline is available.
