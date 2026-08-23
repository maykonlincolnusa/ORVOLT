# OCPP 1.6J Gateway

Accepts real charge points and translates them into ORVOLT's canonical
contracts.

## Why this exists

ORVOLT's own telemetry format — JSON on a site-local MQTT topic — is not
something any commercial charger emits. A commercial charger opens a WebSocket
to a central system and speaks OCPP. Without this service, ORVOLT only works
with hardware built specifically for it.

OCPP 1.6J remains the version most deployed hardware speaks. OCPP 2.0.1 is the
bridge forward and 2.1 (published January 2025) adds V2G and distributed energy
resource control. This gateway implements the 1.6J **core profile**, which is
what carries telemetry and transactions.

## What it does not claim

It is **not** OCPP-certified, and it implements only the core profile. Smart
charging, firmware management, local authorization lists, reservations and
diagnostics are not implemented; a charge point using them will have those
messages rejected rather than silently accepted. Per
[AGENTS.md](../../AGENTS.md), no compliance is claimed without a validated,
certified implementation.

The protocol handling itself is delegated to
[`lorenzodonini/ocpp-go`](https://github.com/lorenzodonini/ocpp-go) rather than
reimplemented, in line with [protocols/README.md](../../protocols/README.md).

## Connecting a charge point

```
ws://<host>:8887/ocpp/<chargePointId>
```

The trailing path segment is the charge point's identity and becomes the
`station_id` in every event.

## Authorization

The gateway defaults to `AUTHORIZATION_MODE=deny-all` and **refuses every
token**, because ORVOLT has no authorization service. Approving unknown cards
would let anyone who points a charger at this endpoint charge for free.

`allow-all` accepts every token. It is valid on a bench or a supervised pilot on
an isolated network, and never for a station the public can reach. The service
logs a warning at startup when it is set.

## Card numbers are personal data

An OCPP `idTag` is usually an RFID card number: it identifies a person, and
anyone holding it can start a session on that account. The gateway publishes an
HMAC-SHA256 of it, never the value itself. Billing and support can recognise the
same card again; nobody can reproduce it from the database.

`TOKEN_PEPPER` must be at least 32 bytes and the service refuses to start
without one. Changing it re-identifies every card in the fleet, so it belongs in
a secret manager and must not be rotated casually.

## Failure behaviour

The distinction is deliberate and it is about what is recoverable:

| Message | If publishing fails | Why |
| --- | --- | --- |
| `StartTransaction` | The call fails | A charge point retries. A session the platform has no record of would be unbilled energy. |
| `StopTransaction` | The call fails | Same, and this one carries the closing meter reading. |
| `StatusNotification` | The call succeeds; the loss is logged | It is an observation. Making the charger retry a state it has already left is worse than losing it. |
| `MeterValues` | The call succeeds; the loss is logged | Same, and the session's register is re-reported on the next sample. |

## Transaction identifiers

OCPP 1.6 makes the central system responsible for allocating `transactionId`,
and a charge point uses it to close a transaction later — possibly after a
reboot or a gateway restart.

The bundled allocator is seeded from the wall clock so a restart resumes above
the previous run's range. That makes collisions unlikely, **not impossible**. A
replicated production deployment must implement `csms.TransactionIDs` against a
shared durable sequence, such as a PostgreSQL sequence or a NATS key-value
bucket.

## Configuration

| Variable | Default | Notes |
| --- | --- | --- |
| `OCPP_LISTEN_PORT` | `8887` | Conventional OCPP 1.6J port. |
| `OCPP_LISTEN_PATH` | `/ocpp/{ws}` | `{ws}` is the charge point id. |
| `HTTP_ADDR` | `:8081` | Serves `/health` and `/ready`. |
| `NATS_URL` | `nats://localhost:4222` | Use `tls://` outside a bench. |
| `NATS_CREDENTIALS` | — | Required for an authenticated bus. |
| `GATEWAY_ID` | `ocpp-gateway-001` | Becomes the publishing identity and subject token. |
| `SITE_ID` | `site-dev-001` | Attributed to every event this gateway produces. |
| `TOKEN_PEPPER` | — | **Required.** At least 32 bytes. |
| `AUTHORIZATION_MODE` | `deny-all` | Or `allow-all`. |
| `HEARTBEAT_INTERVAL` | `5m` | Returned to charge points in the boot answer. |

## A note on charge point clocks

`BootNotification` and `Heartbeat` answers carry the current time, which is how
an OCPP charge point without a battery-backed clock learns what time it is.
Answering accurately here directly reduces how many observations arrive flagged
as unsynchronised. See
[docs/architecture/data-flow.md](../../docs/architecture/data-flow.md).
