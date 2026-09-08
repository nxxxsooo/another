.PHONY: build test lint install clean release

BINARY := another
# Everything this Makefile produces is a development build, so it installs
# under its own name. Installing it as `another` puts it ahead of the released
# binary on PATH, where it is silent, survives `brew upgrade`, and answers
# every question about "is this fixed yet" with the wrong build.
DEV_BINARY := $(BINARY)-dev
# `git describe` returns a bare `v1.2.3` whenever HEAD sits exactly on a tag,
# which makes a local build read exactly like the published one. `+dev` is
# semver build metadata, and it keeps `--version` honest even at a tag.
DESCRIBE := $(shell git describe --tags --always --dirty 2>/dev/null || echo unknown)
VERSION ?= $(DESCRIBE)+dev

build:
	go build -buildvcs=false -ldflags "-X github.com/nxxxsooo/another/internal/cli.version=$(VERSION)" -o bin/$(BINARY) ./cmd/another

test:
	go test ./...

# Built here rather than through `go install`, which would drop a second
# `another` into the Go bin directory — another name collision with the
# release, and one the Claude Code title hook searches by name.
install: build
	@mkdir -p $(HOME)/.local/bin
# Replace by rename, never in place: overwriting a Mach-O that has already been
# executed invalidates its code signature and macOS then SIGKILLs it (exit 137,
# "Killed: 9") on the next run.
	@cp -f bin/$(BINARY) $(HOME)/.local/bin/.$(DEV_BINARY).new
	@chmod 0755 $(HOME)/.local/bin/.$(DEV_BINARY).new
	@mv -f $(HOME)/.local/bin/.$(DEV_BINARY).new $(HOME)/.local/bin/$(DEV_BINARY)
	@echo "Installed $(VERSION) -> $(HOME)/.local/bin/$(DEV_BINARY)"

clean:
	rm -rf bin/ dist/

lint:
	@which golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint not installed, skipping"

release:
	goreleaser release --clean
