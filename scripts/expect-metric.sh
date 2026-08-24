#!/usr/bin/env sh
# Wait until a Prometheus counter or gauge reaches a threshold.
#
# Used to check each link of the telemetry chain separately. A single assertion
# at the end of the chain can only report that nothing arrived; asserting on the
# edge agent's own counters names which hop actually broke.
#
# Usage: expect-metric.sh <metrics_url> <metric_name> <minimum> [timeout_seconds]
set -eu

url="${1:?usage: expect-metric.sh <metrics_url> <metric_name> <minimum> [timeout]}"
metric="${2:?missing metric name}"
minimum="${3:?missing minimum}"
timeout="${4:-60}"
deadline=$(($(date +%s) + timeout))

value=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  value=$(curl -s "$url" 2>/dev/null | awk -v name="$metric" '$1 == name { print $2; exit }')
  case "$value" in
    '' | *[!0-9]*) ;;
    *)
      if [ "$value" -ge "$minimum" ]; then
        echo "$metric = $value"
        exit 0
      fi
      ;;
  esac
  sleep 2
done

echo "timed out after ${timeout}s: $metric never reached $minimum (last value: ${value:-absent})" >&2
echo "--- full metrics ---" >&2
curl -s "$url" >&2 || echo "the metrics endpoint itself was unreachable" >&2
exit 1
