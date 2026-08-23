# ORVOLT Repository Guide

ORVOLT is an edge-first platform foundation for EVSE telemetry, energy orchestration, and future grid-interactive systems.

## Boundaries

- `firmware/` owns fail-safe electrical protection and never depends on cloud connectivity.
- `power/` owns future power-conversion control and never accepts internet-originated commands.
- `simulator/` is synthetic only; it must never be presented as a physical EVSE runtime.
- `edge/` validates local telemetry and bridges it to cloud events. It contains no cloud business policy.
- `cloud/` owns persistence, protocol gateways and management APIs; it is outside local safety decisions. A protocol gateway translates one external protocol into canonical contracts and owns no business policy.
- `contracts/` owns versioned canonical messages. Do not hand-copy canonical models across services.

## Approved Stack

C++20/CMake for simulator and future embedded work, Rust/Tokio for edge, Go/PostgreSQL/NATS for cloud, Protocol Buffers for contracts, and Docker Compose for local infrastructure. Python and FastAPI are not permitted in core services.

## Safety Invariants

Cloud loss must not make a local electrical safety decision. Never create a direct internet-to-power-controller path. Do not claim OCPP, ISO 15118, IEC, or hardware compliance without a standards-compliant implementation and validation.

## Commands

`make bootstrap`, `make generate`, `make build`, `make test`, `make up`, `make simulate`, `make down`, `make fmt`, `make lint`.

## Working Rules

Keep changes incremental. New services require tests, health checks, structured logs, and documented ownership. A check that has never run is treated as a failing check, not as a pending one. Avoid large frameworks without a concrete justification. Preserve the safety/control/edge/cloud separation.
