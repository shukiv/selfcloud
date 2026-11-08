#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later

# The browser checks of CI (decision 101). It builds the web interface and the server as make e2e does, then runs
# web/e2e/run.mjs and both adminAccess runs, with the fake document converter (convertFake.mjs). The CI job runs this
# script after npm ci in web/, and a builder's host runs it the same way.
#
# It needs Node 22, Go, openssl, and a Chrome or Chromium. SELFCLOUD_E2E_BROWSER names the browser; without it run.mjs
# looks for chromium, chromium-browser, google-chrome-stable and google-chrome on PATH.
#
# The output goes to e2e-out/ in the checkout: one log per run, and the temporary folders the runs keep
# (SELFCLOUD_E2E_KEEP=1). The CI job uploads that folder when the script fails.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
out="$root/e2e-out"
mkdir -p "$out/tmp"
export TMPDIR="$out/tmp"
export SELFCLOUD_E2E_KEEP=1

echo "== build the web interface"
(cd "$root/web" && npm run build)

echo "== build the server"
version=$(git -C "$root" describe --tags --always --dirty 2>/dev/null || echo dev)
(cd "$root" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/shukiv/selfcloud/internal/version.Version=$version" -o bin/selfcloud ./cmd/selfcloud)

cd "$root/web"
echo "== run.mjs"
node e2e/run.mjs 2>&1 | tee "$out/run.log"
echo "== adminAccess en"
node e2e/adminAccess.mjs en 2>&1 | tee "$out/adminAccess-en.log"
echo "== adminAccess he"
node e2e/adminAccess.mjs he 2>&1 | tee "$out/adminAccess-he.log"
echo "e2e: run.mjs and both adminAccess runs passed"
