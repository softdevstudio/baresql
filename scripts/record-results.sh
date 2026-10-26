#!/usr/bin/env bash
# Records what the site quotes into test/results/: the audit fixtures and
# case studies, the 1,000-table timing, the rules catalog, and lint and
# compare runs on the demo projects in test/demo. Every number on the site
# comes from these files; re-record after any change that could move one,
# then update the site from them.
#
# Needs all four engines:
#   scripts/dev-engines.sh run scripts/record-results.sh     (Claude's sandbox)
# or BARESQL_TEST_PG, BARESQL_TEST_MYSQL and BARESQL_TEST_MARIADB set and
# sqlite3 on PATH.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
out=$root/test/results
: "${BARESQL_TEST_PG:?BARESQL_TEST_PG is not set}"
: "${BARESQL_TEST_MYSQL:?BARESQL_TEST_MYSQL is not set}"
: "${BARESQL_TEST_MARIADB:?BARESQL_TEST_MARIADB is not set}"
command -v sqlite3 >/dev/null || { echo "record-results: sqlite3 is not on PATH" >&2; exit 2; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
bin=$tmp/baresql
go build -o "$bin" ./cmd/baresql

rm -rf "$out"
mkdir -p "$out/portable/rules"

# Audit: the fixtures, the case studies and the timing (Go tests, so each
# audit recorded is one the fixture tests check line by line).
BARESQL_RECORD_DIR=$out BARESQL_REQUIRE_ENGINES=pg BARESQL_SCALE=1 \
  go test ./audit -run 'TestRecordResults|TestScale' -count=1 -timeout 30m
BARESQL_RECORD_DIR=$out go test ./portable -run 'TestRecordResults' -count=1

# run DIR WANT FILE CMD...: run a command in DIR, keep what it prints in
# FILE and check its exit code.
run() {
  local dir=$1 want=$2 file=$3 got
  shift 3
  set +e
  (cd "$dir" && "$@") > "$file" 2>&1
  got=$?
  set -e
  if [ "$got" != "$want" ]; then
    echo "record-results: $* (in $dir) exited $got, want $want:" >&2
    cat "$file" >&2
    exit 1
  fi
}

p=$out/portable
demo=$root/test/demo
pg=$BARESQL_TEST_PG my=$BARESQL_TEST_MYSQL ma=$BARESQL_TEST_MARIADB
run "$root" 0 "$p/rules.txt" "$bin" rules
run "$root" 0 "$p/rules.json" "$bin" rules --format json
for code in $(grep -o '^  [a-z][a-z-]*' "$p/rules.txt"); do
  run "$root" 0 "$p/rules/$code.txt" "$bin" rules "$code"
done
run "$demo" 1 "$p/shop-lint.txt" "$bin" lint shop.sql
run "$demo" 1 "$p/shop-lint-all.txt" "$bin" lint --all shop.sql
run "$demo" 1 "$p/shop-compare.txt" "$bin" compare --sqlite --postgres "$pg" --mysql "$my" shop.sql
run "$demo" 1 "$p/shop-compare-4.txt" "$bin" compare --sqlite --postgres "$pg" --mysql "$my" --mariadb "$ma" shop.sql
run "$demo/sqlite-tests" 1 "$p/sqlite-tests-lint.txt" "$bin" lint --engines sqlite,postgres schema.sql queries.sql
run "$demo/sqlite-tests" 1 "$p/sqlite-tests-compare.txt" "$bin" compare --sqlite --postgres "$pg" schema.sql queries.sql
run "$demo/mysql-to-postgres" 1 "$p/mysql-to-postgres-lint.txt" "$bin" lint --engines mysql,postgres schema-mysql.sql queries.sql
run "$demo/mysql-to-postgres" 1 "$p/mysql-to-postgres-compare.txt" "$bin" compare --mysql "$my" --postgres "$pg" schema.sql queries.sql

{
  echo "recorded: $(date -u +%Y-%m-%d)"
  echo "commit: $(git rev-parse --short HEAD)$(git diff --quiet -- . ':!test/results' || echo ', with uncommitted changes')"
  echo "binary: $("$bin" version), $(go env GOVERSION), $(go env GOOS)/$(go env GOARCH)"
  echo "engines: $(head -1 "$p/shop-compare-4.txt" | sed 's/^Compared on //; s/\.$//')"
} > "$out/versions.txt"

cat > "$out/README.md" <<'EOF'
# Recorded results

Written by `scripts/record-results.sh`; do not edit by hand. The site
quotes these files. `versions.txt` says when, from which commit and on
which engine versions they were recorded.

- `audit/<fixture>/`: BareSQL Audit of each planted database in
  audit/testdata (shop, saas, clinic, and clean, a database set up with
  care): `report.html` and `audit.json` as `baresql audit` writes them,
  `summary.txt` as it prints, `lines.txt` as `--format lines` prints. The
  fixture tests check these audits line by line.
- `audit/counts.txt`: logins, readable columns, findings and the number of
  facts checked exactly per PostgreSQL version, per fixture.
- `audit/agent/`: an AI support agent's login (saas fixture) before
  go-live, `fix.sql`, and after; `diff.txt` is `baresql diff`.
- `audit/support/`: a support view that hands out card numbers (shop
  fixture), the fix and the audit after it.
- `audit/quarterly/`: the shop audited at the start of one quarter (`q3`)
  and the next (`q4`), with the changes made in between; `diff.txt` is
  `baresql diff q3/audit.json q4/audit.json` and `baseline.txt` what
  `baresql audit --baseline q3/audit.json` prints.
- `audit/scale.txt`: the audit of 1,000 tables and 50 logins, timed.
- `portable/rules.txt`, `rules.json`, `rules/<code>.txt`: `baresql rules`,
  `--format json` and `baresql rules <code>`.
- `portable/counts.txt`: rules, catalog rows and recorded answers.
- `portable/shop-*`, `sqlite-tests-*`, `mysql-to-postgres-*`: lint and
  compare on the demo projects in test/demo.
EOF
echo "record-results: wrote $out"
