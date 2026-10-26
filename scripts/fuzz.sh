#!/usr/bin/env bash
# Runs every fuzz target in the repo for a short while (default 20s each).
# Targets are found by name, so new ones need no change here. A crash leaves
# its input in testdata/fuzz/<Target>/ of that package; commit it, then fix.
set -euo pipefail
cd "$(dirname "$0")/.."
t=${1:-20s}
fail=0
for f in $(grep -rl --include='*_test.go' '^func Fuzz' . | sort); do
  dir=$(dirname "$f")
  for name in $(sed -n 's/^func \(Fuzz[A-Za-z0-9_]*\)(.*/\1/p' "$f"); do
    echo "== $dir $name"
    if ! go test -run '^$' -fuzz "^$name\$" -fuzztime "$t" "$dir" > fuzz.log 2>&1; then
      echo "::error::$name in $dir failed: $(grep -E 'Failing input|panic:|_test\.go:[0-9]+' fuzz.log | head -5 | tr '\n' ' ')"
      cat fuzz.log
      fail=1
    fi
  done
done
rm -f fuzz.log
exit $fail
