#!/usr/bin/env bash
# VERSION is the one place the version is set; the code and the changelog
# must agree with it. A version ending in -dev is work in progress: it needs
# the changelog section of the version it leads to, and is never released.
# Prints ::error:: lines so CI annotations show why.
set -euo pipefail
cd "$(dirname "$0")/.."
v=$(tr -d '[:space:]' < VERSION)
go_v=$(sed -n 's/^const Version = "\(.*\)"/\1/p' version.go)
fail=0
[ "$go_v" = "$v" ] || { echo "::error::version.go has $go_v, VERSION has $v"; fail=1; }
grep -q "^## ${v%-dev}\$" CHANGELOG.md || { echo "::error::CHANGELOG.md has no section '## ${v%-dev}'"; fail=1; }
[ $fail = 0 ] && echo "version $v everywhere"
exit $fail
