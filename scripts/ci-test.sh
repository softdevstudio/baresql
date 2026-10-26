#!/usr/bin/env bash
# Runs `go test` with the given arguments and keeps the output in test.log.
# On failure it repeats the failing lines as one ::error:: annotation: job
# logs can't be read from the maintainer's sessions, annotations can.
set -uo pipefail
go test "$@" 2>&1 | tee test.log
status=${PIPESTATUS[0]}
if [ "$status" != 0 ]; then
  echo "::error::$(grep -E -- '--- FAIL|panic:|_test\.go:[0-9]+:|\.go:[0-9]+:[0-9]+: |^FAIL' test.log | head -150 |
    awk '{gsub(/%/,"%25"); gsub(/\r/,""); printf "%s%%0A", $0}')"
fi
exit "$status"
