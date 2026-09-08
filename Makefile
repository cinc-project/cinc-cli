# cinc CLI build automation

BINARY      := cinc
CMD_PKG     := ./apps/cinc
LDFLAGS_PKG := github.com/cinc-project/cinc-cli/apps/cinc/cmd
DIST_DIR    ?= dist
PLATFORMS   := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

# Build metadata — override on the command line if needed.
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%d)

LDFLAGS := -X $(LDFLAGS_PKG).version=$(VERSION) \
           -X $(LDFLAGS_PKG).commit=$(COMMIT) \
           -X $(LDFLAGS_PKG).buildDate=$(BUILD_DATE)

.PHONY: all build dist install test test-acceptance vet lint fmt tidy clean run docs help

all: build

## build: compile the cinc binary with version metadata
build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD_PKG)

## dist: build release archives for Linux and macOS
dist:
	rm -rf $(DIST_DIR)
	mkdir -p $(DIST_DIR)
	for platform in $(PLATFORMS); do \
		goos=$${platform%/*}; \
		goarch=$${platform#*/}; \
		name="$(BINARY)_$(VERSION)_$${goos}_$${goarch}"; \
		mkdir -p "$(DIST_DIR)/$$name"; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch go build -trimpath -ldflags "$(LDFLAGS) -s -w" -o "$(DIST_DIR)/$$name/$(BINARY)" $(CMD_PKG); \
		tar -C "$(DIST_DIR)" -czf "$(DIST_DIR)/$$name.tar.gz" "$$name"; \
		rm -rf "$(DIST_DIR)/$$name"; \
	done
	cd "$(DIST_DIR)" && shasum -a 256 *.tar.gz > SHA256SUMS

## install: install cinc into the Go bin directory
# Stripped (-s -w) and trimpathed to match the release `dist` build: a
# ~25% smaller binary that's faster to page in on cold start. Go's
# runtime keeps the pclntab regardless, so panic tracebacks still carry
# function names and line numbers. `make build` stays unstripped for
# day-to-day debugging.
install:
	go install -trimpath -ldflags "$(LDFLAGS) -s -w" $(CMD_PKG)

## test: run the test suite
test:
	go test ./...

## test-acceptance: run acceptance tests against cinc-zero (auto-downloads the cinc-zero binary; set CINC_ZERO_BIN to override)
test-acceptance:
	go test -tags acceptance -count=1 ./test/...

## vet: run go vet across all packages
vet:
	go vet ./...

## lint: run golangci-lint (config in .golangci.yml). Subsumes vet and a gofmt
## check, and unlike `gofmt -l apps` it covers every package.
## Install: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
lint:
	golangci-lint run ./...

## fmt: format all Go source
fmt:
	gofmt -w apps

## tidy: tidy go module dependencies
tidy:
	go mod tidy

## clean: remove build artifacts
clean:
	rm -rf $(BINARY) $(DIST_DIR)
	go clean

## run: build and run cinc (pass flags via ARGS="...")
run: build
	./$(BINARY) $(ARGS)

## docs: regenerate the per-command Markdown reference under docs/commands/
docs:
	go run ./tools/gendocs

## help: list available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //'
