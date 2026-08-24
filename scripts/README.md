# Developer Scripts

Executable workflows live in the root `Makefile` so they stay visible and
composable. This directory holds the pieces that do not fit a Make target —
mostly the assertions the end-to-end job is built from.

| Script | Purpose |
| --- | --- |
| `generate-go-bindings.sh` | Generates Go bindings for every contract. Shared by the service images so adding a contract cannot leave one image failing to compile. |
| `wait-for-ready.sh` | Polls a readiness URL until it answers 200. |
| `expect-mqtt.sh` | Subscribes to a topic and asserts telemetry reaches the broker. |
| `expect-metric.sh` | Polls a Prometheus endpoint until a counter or gauge reaches a threshold. |
| `expect-log.sh` | Polls a Compose service's output for a pattern. |
| `smoke-test.sh` | Asserts telemetry is persisted and served with its provenance. |

## Why the end-to-end test is many small assertions

The first version made one check at the end of the chain. When it failed, all it
could say was that nothing had arrived — true of a broken simulator, a stalled
edge agent, an unreachable bus and a database fault alike.

Each hop is now asserted separately, so a failure names the hop that broke. The
edge agent's own metrics endpoint is what makes the middle of the chain
observable, which is the same reason it exists on a real charger: the useful
question is never "is it broken" but "which part".

Prefer subscribing to the broker over reading a publisher's log. A log says the
publisher believes it sent something; a subscription proves it arrived, and does
not depend on how the publisher buffers its output.

Scripts must not encode safety decisions or deploy to physical equipment.
