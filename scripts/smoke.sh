#!/usr/bin/env bash
# Smoke test of a built baresql binary, run by release.yml before publishing.
#   scripts/smoke.sh <binary> <expected version> basic|full
# basic needs no servers; full also uses the servers that
# scripts/ci-engine.sh started (BARESQL_TEST_* in the environment) and the
# sqlite3 program.
set -euo pipefail
bin=$1 want=$2 mode=$3
fail() { echo "::error::smoke: $*"; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

out=$("$bin" version | tr -d '\r')
[ "$out" = "baresql $want" ] || fail "version printed '$out', want 'baresql $want'"
echo "version ok: $out"

"$bin" help >/dev/null || fail "help failed"
n=$("$bin" rules | grep -c '^  [a-z]' || true)
[ "$n" -ge 50 ] || fail "rules listed $n rules"
# Read the whole output first: grep -q stops at the first match, and the
# rest of the output would then hit a closed pipe (SIGPIPE under pipefail).
rules=$("$bin" rules integer-division)
grep -q '3.5000' <<<"$rules" || fail "rules integer-division has no recorded MySQL answer"
echo "rules ok: $n rules"

printf 'SELECT 7 / 2;\nSELECT ROUND(7 * 1.0 / 2, 2);\n' > "$tmp/q.sql"
set +e
"$bin" lint "$tmp/q.sql" > "$tmp/lint.txt"
code=$?
set -e
[ "$code" = 1 ] || fail "lint exited $code, want 1: $(cat "$tmp/lint.txt")"
grep -q 'q.sql:1:8: warning: .*\[integer-division\]' "$tmp/lint.txt" || fail "lint output: $(cat "$tmp/lint.txt")"
[ "$(grep -c 'integer-division' "$tmp/lint.txt")" = 1 ] || fail "lint flagged the portable form too"
echo "lint ok"

[ "$mode" = full ] || exit 0

"$bin" audit --out "$tmp/report" "$BARESQL_TEST_PG" > "$tmp/audit.txt" || fail "audit failed: $(cat "$tmp/audit.txt")"
[ -s "$tmp/report/report.html" ] && [ -s "$tmp/report/audit.json" ] || fail "audit wrote no report"
"$bin" diff "$tmp/report/audit.json" "$tmp/report/audit.json" > /dev/null || fail "diff of an audit with itself exited non-zero"
echo "audit ok: $(head -1 "$tmp/audit.txt")"

set +e
"$bin" compare --sqlite --postgres "$BARESQL_TEST_PG" --mysql "$BARESQL_TEST_MYSQL" \
  --mariadb "$BARESQL_TEST_MARIADB" "$tmp/q.sql" > "$tmp/compare.txt"
code=$?
set -e
[ "$code" = 1 ] || fail "compare exited $code, want 1: $(cat "$tmp/compare.txt")"
grep -q '2 statements: 1 answered alike, 1 differ' "$tmp/compare.txt" || fail "compare output: $(cat "$tmp/compare.txt")"
echo "compare ok"
