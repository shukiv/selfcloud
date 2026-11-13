#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later

# Tests for scripts/ci-annotate.sh: the lines a failure keeps and the escaping of GitHub's workflow commands. It runs
# by hand; no CI job calls it.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=ci-annotate.sh
. "$here/ci-annotate.sh"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail=0
check() {
  local name=$1 expected=$2 actual=$3
  if [ "$actual" != "$expected" ]; then
    printf 'not ok: %s\n--- expected\n%s\n--- actual\n%s\n' "$name" "$expected" "$actual" >&2
    fail=1
  else
    printf 'ok: %s\n' "$name"
  fi
}

passing="$work/passing.log"
printf '%s\n' \
  '=== RUN   TestOK' \
  '--- PASS: TestOK (0.00s)' \
  'PASS' \
  $'ok  \texample.com/pkg\t0.020s' > "$passing"
expected=$(printf '%s\n' \
  '::error title=failed tests::(none)' \
  '::error title=failed packages::(none)' \
  '::error title=panic::(none)')
actual=$(annotate_tests "$passing")
check 'a passing log has no failures' "$expected" "$actual"

failing="$work/failing.log"
printf '%s\n' \
  '=== RUN   TestA' \
  '    a_test.go:10: first' \
  '--- FAIL: TestA (0.00s)' \
  '    a_test.go:11: detail A' \
  '=== RUN   TestB' \
  '    b_test.go:20: second' \
  '--- FAIL: TestB (0.00s)' \
  '    b_test.go:21: detail B' \
  'FAIL' \
  $'FAIL\texample.com/pkg\t0.012s' > "$failing"
expected=$(printf '%s\n' \
  $'::error title=failed tests::=== RUN   TestA%0A    a_test.go:10: first%0A--- FAIL: TestA (0.00s)%0A    a_test.go:11: detail A%0A=== RUN   TestB%0A    b_test.go:20: second%0A--- FAIL: TestB (0.00s)' \
  $'::error title=failed packages::FAIL\texample.com/pkg\t0.012s' \
  '::error title=panic::(none)')
actual=$(annotate_tests "$failing")
check 'two failed tests keep the lines before them, and the failed package is named' "$expected" "$actual"

panicking="$work/panic.log"
printf '%s\n' \
  '=== RUN   TestX' \
  'panic: boom' \
  'goroutine 1 [running]:' \
  'main.TestX()' \
  $'\t/tmp/x_test.go:10 +0x1' > "$panicking"
expected=$(printf '%s\n' \
  '::error title=failed tests::(none)' \
  '::error title=failed packages::(none)' \
  $'::error title=panic::panic: boom%0Agoroutine 1 [running]:%0Amain.TestX()%0A\t/tmp/x_test.go:10 +0x1')
actual=$(annotate_tests "$panicking")
check 'a panic carries the lines after it' "$expected" "$actual"

e2e="$work/e2e.log"
printf '%s\n' \
  '== run.mjs' \
  'PASS alpha' \
  'PASS beta' \
  'FAIL the share dialog' \
  'check failed at once' \
  'Error: the page closed' \
  'some context' \
  'timeout waiting' \
  'final line' > "$e2e"
out="$work/out"
mkdir -p "$out"
printf '%s\n' \
  'address: about:blank' \
  'console and page errors (1):' \
  'console: no' > "$out/run-failure.txt"
expected=$(printf '%s\n' \
  '::error title=last passes::PASS alpha%0APASS beta' \
  '::error title=failure::FAIL the share dialog%0Acheck failed at once%0AError: the page closed%0Asome context%0Atimeout waiting%0Afinal line' \
  '::error title=last lines::== run.mjs%0APASS alpha%0APASS beta%0AFAIL the share dialog%0Acheck failed at once%0AError: the page closed%0Asome context%0Atimeout waiting%0Afinal line' \
  '::error title=run-failure.txt::address: about:blank%0Aconsole and page errors (1):%0Aconsole: no')
actual=$(annotate_e2e "$e2e" "$out")
check 'an e2e failure keeps the passes, the failure, the last lines and the diagnostics file' "$expected" "$actual"

special="$work/special.log"
xs=$(printf '%*s' 310 '' | tr ' ' 'x')
printf '50%% : comma, here\ncr\rhere\n%s\n' "$xs" > "$special"
expected="::error title=why%3A 1%2C 2%25::50%25 : comma, here%0Acr%0Dhere%0A${xs:0:300}"
actual=$(annotate_note 'why: 1, 2%' "$special")
check 'percent, colon, comma, a carriage return and the 300-character cut are escaped' "$expected" "$actual"

if [ "$fail" -ne 0 ]; then
  exit 1
fi
printf 'all annotation tests passed\n'