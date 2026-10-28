VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/shukiv/selfcloud/internal/version.Version=$(VERSION)

.PHONY: build test check docker

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/selfcloud ./cmd/selfcloud

test:
	go test ./...

# What CI runs: formatting, vet, and the tests with the race detector (needs a C compiler).
check:
	@unformatted="$$(gofmt -l .)"; if [ -n "$$unformatted" ]; then echo "not gofmt'ed:"; echo "$$unformatted"; exit 1; fi
	go vet ./...
	go test -race ./...

docker:
	docker build --build-arg VERSION=$(VERSION) -t selfcloud:$(VERSION) .
