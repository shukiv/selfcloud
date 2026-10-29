VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/shukiv/selfcloud/internal/version.Version=$(VERSION)

.PHONY: build web test check docker e2e

# The web interface is built first: the Go build embeds web/dist.
build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/selfcloud ./cmd/selfcloud

# Only the web interface. Needs Node 22 and npm. Writes web/dist, which the Go build embeds.
web:
	cd web && npm ci && npm run build

test:
	go test ./...

# What CI runs: formatting, vet, and the tests with the race detector (needs a C compiler).
check:
	@unformatted="$$(gofmt -l .)"; if [ -n "$$unformatted" ]; then echo "not gofmt'ed:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	go test -race ./...

docker:
	docker build --build-arg VERSION=$(VERSION) -t selfcloud:$(VERSION) .

# The end-to-end smoke test: builds, starts bin/selfcloud on a temporary data directory, and drives
# headless Chrome through the web interface. It needs Chrome or Chromium, found on PATH or named by
# SELFCLOUD_E2E_BROWSER.
e2e: build
	cd web && node e2e/run.mjs
