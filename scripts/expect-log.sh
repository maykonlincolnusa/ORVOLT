#!/usr/bin/env sh
# Wait until a Compose service logs something matching a pattern.
#
# Usage: expect-log.sh <service> <pattern> [timeout_seconds]
set -eu

service="${1:?usage: expect-log.sh <service> <pattern> [timeout]}"
pattern="${2:?missing pattern}"
timeout="${3:-60}"
deadline=$(($(date +%s) + timeout))

while [ "$(date +%s)" -lt "$deadline" ]; do
  if docker compose logs --no-color --tail 200 "$service" 2>/dev/null | grep -q -- "$pattern"; then
    echo "$service is producing output matching: $pattern"
    exit 0
  fi
  sleep 2
done

echo "timed out after ${timeout}s waiting for $service to log: $pattern" >&2
echo "--- recent $service output ---" >&2
docker compose logs --no-color --tail 100 "$service" >&2 || true
exit 1
