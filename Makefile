VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
export VERSION
LDFLAGS := -s -w -X github.com/shukiv/selfcloud/internal/version.Version=$(VERSION)

.PHONY: build web test check docker e2e release

# The web interface is built first: the Go build embeds web/dist.
build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/selfcloud ./cmd/selfcloud

# The release binaries for Linux, with their checksums. The VERSION becomes part of a file name, so it
# is checked before anything is built.
release:
	@case "$$VERSION" in \
	  ''|*[!A-Za-z0-9._+-]*) echo "VERSION must be non-empty and contain only A-Za-z0-9._+-: '$$VERSION'" >&2; exit 1;; \
	esac
	$(MAKE) web
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/selfcloud_$(VERSION)_linux_amd64 ./cmd/selfcloud
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/selfcloud_$(VERSION)_linux_arm64 ./cmd/selfcloud
	cd dist && sha256sum selfcloud_$(VERSION)_linux_amd64 selfcloud_$(VERSION)_linux_arm64 > SHA256SUMS
	@for arch in amd64 arm64; do \
	  f="dist/selfcloud_$(VERSION)_linux_$$arch"; \
	  echo "selfcloud_$(VERSION)_linux_$$arch: $$(wc -c < "$$f") bytes"; \
	done

# Only the web interface. Needs Node 22 and npm. Writes web/dist, which the Go build embeds.
web:
	cd web && npm ci && npm run build

test:
	go test ./...

# What CI runs: formatting, vet, and the tests with the race detector (needs a C compiler).
# The pointer checks that -race turns on stay on for this code, and off for the C-to-Go SQLite.
check:
	@unformatted="$$(gofmt -l .)"; if [ -n "$$unformatted" ]; then echo "not gofmt'ed:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	go test -race -gcflags='modernc.org/...=-d=checkptr=0' -timeout 20m ./...

docker:
	docker build --build-arg VERSION=$(VERSION) -t selfcloud:$(VERSION) .

# The end-to-end smoke test: builds, starts bin/selfcloud on a temporary data directory, and drives
# headless Chrome through the web interface. It needs Chrome or Chromium, found on PATH or named by
# SELFCLOUD_E2E_BROWSER.
e2e: build
	cd web && node e2e/run.mjs && node e2e/adminAccess.mjs en && node e2e/adminAccess.mjs he
