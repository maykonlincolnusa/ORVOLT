#!/usr/bin/env sh
# Assert that a telemetry message actually reaches the broker.
#
# Subscribing is a stronger check than reading the publisher's own log: it
# proves the MQTT hop happened, rather than that the publisher believes it did.
# It is also independent of how the publisher buffers its output.
#
# Usage: expect-mqtt.sh <topic> [timeout_seconds]
set -eu

topic="${1:?usage: expect-mqtt.sh <topic> [timeout_seconds]}"
timeout="${2:-45}"

if message=$(docker compose exec -T mosquitto \
  mosquitto_sub -h localhost -t "$topic" -C 1 -W "$timeout" 2>&1); then
  case "$message" in
    *'"station_id"'*)
      echo "broker delivered telemetry on $topic:"
      echo "$message"
      exit 0
      ;;
    *)
      echo "unexpected payload on $topic: $message" >&2
      exit 1
      ;;
  esac
fi

echo "no telemetry reached the broker on $topic within ${timeout}s" >&2
echo "$message" >&2
echo "--- simulator ---" >&2
docker compose logs --no-color --tail 60 evse-simulator >&2 || true
echo "--- broker ---" >&2
docker compose logs --no-color --tail 30 mosquitto >&2 || true
exit 1
