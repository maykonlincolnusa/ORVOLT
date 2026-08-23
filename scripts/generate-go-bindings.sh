#!/usr/bin/env sh
# Generate Go bindings for every canonical contract.
#
# Container builds call this instead of listing .proto files inline, so that
# adding a contract cannot leave one service's image failing to compile while
# another's succeeds.
#
# Local development uses `make generate` (Buf) instead; both read the same
# contracts/proto tree.
set -eu

output="${1:-contracts/gen/go}"
mkdir -p "$output"

# shellcheck disable=SC2046 # word splitting is the intent: one argument per file
protoc -I contracts/proto \
  --go_out="$output" \
  --go_opt=paths=source_relative \
  $(find contracts/proto -name '*.proto' | sort)

echo "generated Go bindings into $output"
