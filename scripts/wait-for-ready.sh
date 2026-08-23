#!/usr/bin/env sh
# Poll a readiness URL until it answers 200 or the deadline expires.
# Usage: wait-for-ready.sh <url> [timeout_seconds]
set -eu

url="${1:?usage: wait-for-ready.sh <url> [timeout_seconds]}"
timeout="${2:-120}"
deadline=$(($(date +%s) + timeout))

while [ "$(date +%s)" -lt "$deadline" ]; do
  status=$(curl -s -o /dev/null -w '%{http_code}' "$url" || true)
  if [ "$status" = "200" ]; then
    echo "ready: $url"
    exit 0
  fi
  sleep 2
done

echo "timed out after ${timeout}s waiting for $url (last status: ${status:-none})" >&2
exit 1
