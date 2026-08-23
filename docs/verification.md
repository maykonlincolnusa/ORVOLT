# Verification Record

## How this is verified now

Everything below is checked by [CI](../.github/workflows/ci.yml) on every push
and pull request. That is the change that matters: the previous record listed
checks that had never been run because no automation existed to run them.

| Check | Where |
| --- | --- |
| Contract linting against the `buf` STANDARD ruleset | `contracts` job |
| Contract formatting | `contracts` job |
| Backwards-compatibility (`buf breaking`) against `main` | `contracts` job, pull requests |
| Go: `go mod tidy` is clean, `gofmt`, `go build`, `go vet`, `go test -race` | `go` job |
| Rust: `cargo fmt --check`, `clippy -D warnings`, `cargo test --locked` | `rust` job |
| C++: CMake configure, build, `ctest` | `cpp` job |
| End-to-end: `docker compose up`, readiness, telemetry reaching PostgreSQL and being served with its provenance | `e2e` job |
| Static musl binaries for `aarch64`, `armv7` and `x86_64` | [`edge-release.yml`](../.github/workflows/edge-release.yml) |

The cross-compile job asserts the binary is statically linked. A dynamically
linked agent would need a matching libc on the charger's root filesystem, which
is the thing musl exists to avoid.

## What the first record got wrong

The original version of this file recorded the Go control plane as "not run:
the `go` executable is not installed in the initial workspace". That was
accurate, and it hid two defects that had been committed:

1. There was no `go.sum` and `go.mod` listed no indirect requirements, so
   `go build` failed on every dependency.
2. Once that was fixed, `nats.NewJetStreamContext` turned out not to exist in
   the NATS client library. The entire `ingest` package had never compiled.

`buf lint` also reported 15 violations of the ruleset `buf.yaml` itself
declares.

None of these were subtle. They were invisible because "not run" was treated as
an acceptable state for a check. It is not: a check that has never run is
indistinguishable from a check that fails.

## What is still not verified automatically

Stated plainly, because the failure above came from a record that read as more
complete than it was:

- **No integration test against a real PostgreSQL.** The store layer's SQL is
  exercised only by the end-to-end Compose run, so a defect in a query path that
  run does not touch — session merging on out-of-order events, silent-station
  scanning, site demand — would not be caught.
- **No test against a real charge point.** The OCPP gateway's mapping and
  handler logic are unit-tested, but nothing validates the wire behaviour
  against certified hardware or the OCA test tool.
- **No load or soak testing.** Batching, spool rotation under sustained
  backpressure, and JetStream retention eviction are reasoned about, not
  measured.
- **No test of the systemd unit.** The watchdog and readiness integration is
  implemented and unit-tested at the parsing level; nothing boots it under
  systemd and confirms a wedged agent is actually restarted.
- **No hardware-in-the-loop testing of any kind**, and none should be attempted
  with real high-voltage equipment without the engineering and certification
  described in [architecture/system-overview.md](architecture/system-overview.md).

## Energy provider integrations

The `orvolt.energy.site.v1` contract, its JetStream consumer, persistence and
latest-observation API are implemented and tested. **No provider client exists.**
The SMA adapter remains unimplemented until approved sandbox OAuth credentials
and sanctioned response fixtures are available; its cloud-only, read-only scope
is specified in [`integrations/energy/sma/README.md`](../integrations/energy/sma/README.md).

Load management consumes whatever observations arrive and falls back to a
conservative limit when none do, so the absence of a provider reduces
optimisation rather than blocking the feature.
