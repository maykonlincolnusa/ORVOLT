# ADR-001: Multi-language architecture

## Status

Accepted

## Context and Decision

ORVOLT spans simulation/embedded-adjacent logic, asynchronous edge computing, and cloud services. Use C++20/CMake for the simulator and future embedded-adjacent modules, Rust/Tokio for the edge agent, and Go for the cloud control plane. Contract boundaries use Protobuf.

## Consequences

Each language is constrained to an appropriate domain and has independent builds. Cross-service data models come from `contracts/proto`; services must not independently redefine canonical telemetry.
