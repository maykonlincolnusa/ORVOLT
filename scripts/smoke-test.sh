#!/usr/bin/env sh
# Assert that simulator telemetry travelled MQTT -> edge -> JetStream -> PostgreSQL
# and is readable through the management API.
# Usage: smoke-test.sh <base_url> <station_id> [timeout_seconds]
set -eu

base="${1:?usage: smoke-test.sh <base_url> <station_id> [timeout_seconds]}"
station="${2:?missing station id}"
timeout="${3:-120}"
deadline=$(($(date +%s) + timeout))

latest="$base/api/v1/stations/$station/telemetry/latest"

while [ "$(date +%s)" -lt "$deadline" ]; do
  body=$(curl -s "$latest" || true)
  case "$body" in
    *'"station_id"'*"$station"*)
      echo "$body"
      # The row must carry the edge provenance the control plane depends on.
      case "$body" in
        *'"edge_id"'*'"site_id"'*'"ingested_at"'*)
          echo "smoke test passed: telemetry for $station is persisted and served"
          exit 0
          ;;
        *)
          echo "telemetry row is missing edge provenance or ingest stamp" >&2
          exit 1
          ;;
      esac
      ;;
  esac
  sleep 3
done

echo "timed out after ${timeout}s waiting for telemetry from $station" >&2
curl -s "$base/api/v1/stations" >&2 || true
exit 1
