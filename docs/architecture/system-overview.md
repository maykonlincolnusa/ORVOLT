# System Overview

ORVOLT is organized around independently deployable trust and failure domains.

```mermaid
flowchart TB
    subgraph EVSE[EVSE and Site]
        SC[Safety controller\nfail-safe authority]
        PC[Power controller\nconverter control]
        ER[EVerest runtime\nfuture preferred EVSE runtime]
        EA[ORVOLT edge agent]
        SC -. interlocks .-> PC
        ER -. local integration .-> EA
    end
    subgraph Cloud[Cloud Control Plane]
        CP[Control plane]
        DB[(PostgreSQL)]
        TS[(Future telemetry store)]
    end
    EA -->|NATS JetStream| CP
    CP --> DB
    CP -. future export .-> TS
```

## Ownership

| Domain | Authority | Connectivity requirement |
| --- | --- | --- |
| Safety controller | Protective shutdown, interlocks, watchdog and fault state | None; must fail safe locally. |
| Power controller | Converter limits and low-level power regulation | Local-only, protected by safety interlocks. |
| EVSE runtime | Future charging-session protocol runtime | Site-local. EVerest is preferred over a new implementation. |
| Edge agent | Validate, normalize, buffer/forward observations | Operates locally; cloud loss degrades observability, not safety. |
| Cloud control plane | Persistence, fleet projections and management API | Never safety-critical. |

This repository contains only a synthetic simulator, edge telemetry bridge, and cloud telemetry projection. It contains no physical control logic.

> Software in this repository must not be connected to real high-voltage equipment without appropriate hardware engineering, protection systems, validation, and certification.
