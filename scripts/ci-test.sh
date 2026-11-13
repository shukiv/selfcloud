#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later

# The race detector slows the API package's tests to many minutes on a hosted runner: 20m was passed on 2026-11-02 with
# no test stuck (the longest running test was 7s old), so the limit is 40m. The pointer checks that -race turns on stay
# on for this code, and off for the C-to-Go SQLite.
#
# The output goes to test-out/go-test.log and to the screen. When a test fails, the failed tests, the failed packages
# and a panic or a timeout are printed as annotations (::error workflow commands), which anyone who can read the run can
# see without signing in. scripts/ci-annotate.sh holds the escaping those notes share with scripts/ci-e2e.sh. The script
# exits with go test's own status.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
out="$root/test-out"
mkdir -p "$out"
log="$out/go-test.log"
# shellcheck source=ci-annotate.sh
. "$root/scripts/ci-annotate.sh"

echo "== go test"
status=0
set +e
go test -race -gcflags='modernc.org/...=-d=checkptr=0' -timeout 40m ./... 2>&1 | tee "$log"
status=${PIPESTATUS[0]}
set -e
if [ "$status" -ne 0 ]; then
  annotate_tests "$log"
fi
exit "$status"