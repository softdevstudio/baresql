#!/usr/bin/env bash
# Smoke test of a built baresql binary, run by release.yml before publishing.
#   scripts/smoke.sh <binary> <expected version> basic|full
# basic needs no servers; full also uses the servers that
# scripts/ci-engine.sh started (BARESQL_TEST_* in the environment).
set -euo pipefail
bin=$1 want=$2 mode=$3
fail() { echo "::error::smoke: $*"; exit 1; }

out=$("$bin" version | tr -d '\r')
[ "$out" = "baresql $want" ] || fail "version printed '$out', want 'baresql $want'"
echo "version ok: $out"

[ "$mode" = full ] || exit 0
echo "full smoke test: nothing beyond the version yet"
