BINARY  := particulars-bench
MODULE  := github.com/nodelogicau/particulars-bench
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(MODULE)/internal/impl.Version=$(VERSION)
DIST    := dist

export CGO_ENABLED := 0

.PHONY: all build test lint fmt vet tidy clean

all: build

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/$(BINARY) ./cmd/particulars-bench

# Unit tests only: no test calls a model. The reference-CLI test runs when
# `particulars` is on PATH and is skipped otherwise.
test:
	go test ./... -count=1

fmt:
	gofmt -l -w .

vet:
	go vet ./...

lint: vet
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint not installed; ran go vet only"

tidy:
	go mod tidy

clean:
	rm -rf $(DIST)
