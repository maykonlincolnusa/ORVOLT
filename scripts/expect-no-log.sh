#!/usr/bin/env sh
# Assert that a Compose service has NOT reported a given failure.
#
# Some failures are silent in an end-to-end check: a container whose binary the
# dynamic loader rejects never publishes anything, which looks identical to a
# publisher that is simply slow. Asserting the absence of the loader's own error
# separates "it never started" from "it started but produced nothing".
#
# Usage: expect-no-log.sh <service> <pattern> [grace_seconds]
set -eu

service="${1:?usage: expect-no-log.sh <service> <pattern> [grace_seconds]}"
pattern="${2:?missing pattern}"
grace="${3:-15}"

# Give the container time to start and fail, if it is going to.
sleep "$grace"

if docker compose logs --no-color "$service" 2>/dev/null | grep -q -- "$pattern"; then
  echo "$service reported a fatal condition matching: $pattern" >&2
  echo "--- $service output ---" >&2
  docker compose logs --no-color --tail 40 "$service" >&2
  exit 1
fi

echo "$service shows no sign of: $pattern"
