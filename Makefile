.PHONY: build test lint install clean

BINARY := another
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	go build -buildvcs=false -ldflags "-X github.com/CyrusSE/agenthop/internal/cli.version=$(VERSION)" -o bin/$(BINARY) ./cmd/another

test:
	go test ./...

GOPATH_BIN := $(shell go env GOPATH)/bin
GOBIN_PATH := $(shell go env GOBIN)
INSTALL_BIN := $(if $(GOBIN_PATH),$(GOBIN_PATH),$(GOPATH_BIN))/$(BINARY)

install:
	go install -buildvcs=false -ldflags "-X github.com/CyrusSE/agenthop/internal/cli.version=$(VERSION)" ./cmd/another
	@mkdir -p $(HOME)/.local/bin
	@cp -f $(INSTALL_BIN) $(HOME)/.local/bin/$(BINARY)
	@echo "Installed $(VERSION) -> $(HOME)/.local/bin/$(BINARY)"

clean:
	rm -rf bin/ dist/

lint:
	@which golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint not installed, skipping"

release:
	goreleaser release --clean
