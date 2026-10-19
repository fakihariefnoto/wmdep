BINARY  := wmdep
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X github.com/fakihariefnoto/wmdep/internal/cli.Version=$(VERSION) \
	-X github.com/fakihariefnoto/wmdep/internal/cli.Commit=$(COMMIT) \
	-X github.com/fakihariefnoto/wmdep/internal/cli.Date=$(DATE)

.PHONY: all build run demo demo-config test race vet fmt lint tidy install clean

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

run: build
	./$(BINARY)

# demo scaffolds a throwaway config and runs the TUI against the fake provider.
demo: build
	@test -f demo/config.yaml || ./$(BINARY) --config ./demo config init
	./$(BINARY) --config ./demo

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

lint: vet
	@test -z "$$(gofmt -l .)" || { echo "unformatted files:"; gofmt -l .; exit 1; }

tidy:
	go mod tidy

install:
	go install -ldflags "$(LDFLAGS)" .

clean:
	rm -f $(BINARY)
	rm -rf demo
