# daiquiri -- Kubernetes event triage
#
# `make verify` is the gate. Nothing is "done" until it passes clean.
# See docs/engineering-standards.md for what each gate is protecting.

BINARY      := daiquiri
PKG         := ./...
BIN_DIR     := bin
OUT_DIR     := analysis
TESTDATA    := testdata
IMAGE       := daiquiri
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -X main.version=$(VERSION)
GOLANGCI_VERSION := v2.1.6

# `go install` drops binaries in $GOPATH/bin, which is not on PATH by default
# (/usr/local/go/bin is the toolchain, not the same directory). Resolve the
# linter from PATH if it is there, and fall back to $GOPATH/bin if not, so that
# `make tools && make lint` works without touching the user's shell config.
GOPATH_BIN  := $(shell go env GOPATH)/bin
GOLANGCI    := $(shell command -v golangci-lint 2>/dev/null || echo $(GOPATH_BIN)/golangci-lint)

.DEFAULT_GOAL := help

## help: list available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'

## build: compile the binary into bin/
build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) ./cmd/triage

## run: build then run against ARGS (e.g. make run ARGS=testdata/02-memory-leak.jsonl)
run: build
	@./$(BIN_DIR)/$(BINARY) $(ARGS)

## test: run the full test suite with the race detector
test:
	go test -race -count=1 $(PKG)

## test-short: fast feedback loop, skips anything marked long
test-short:
	go test -short -count=1 $(PKG)

## cover: test with coverage, print the per-function summary and total
cover:
	go test -race -count=1 -coverprofile=coverage.out -covermode=atomic $(PKG)
	@go tool cover -func=coverage.out | tail -1

## cover-html: open the coverage report in a browser
cover-html: cover
	go tool cover -html=coverage.out -o coverage.html
	@echo "wrote coverage.html"

## bench: run benchmarks with allocation counts
bench:
	go test -run '^$$' -bench=. -benchmem $(PKG)

## fmt: format all Go source
fmt:
	go fmt $(PKG)

## vet: run the built-in static analyser
vet:
	go vet $(PKG)

## lint: run golangci-lint (see tools target to install)
lint:
	@test -x "$(GOLANGCI)" || { \
		echo "golangci-lint not found at $(GOLANGCI) -- run 'make tools'"; exit 1; }
	@$(GOLANGCI) run

## tidy: sync go.mod/go.sum and fail if that produced a diff
tidy:
	go mod tidy
	@git diff --exit-code go.mod go.sum || { \
		echo "go.mod/go.sum were not tidy -- commit the update"; exit 1; }

## verify: the full gate -- vet, lint, race tests, gofmt check
verify: vet lint test
	@test -z "$$(gofmt -l . 2>/dev/null)" || { \
		echo "unformatted files:"; gofmt -l .; exit 1; }
	@echo "verify: OK"

## capture: regenerate the submitted output in analysis/ for every scenario
capture: build
	@mkdir -p $(OUT_DIR)
	@for f in $(TESTDATA)/*.jsonl; do \
		name=$$(basename $$f .jsonl); \
		./$(BIN_DIR)/$(BINARY) $$f > $(OUT_DIR)/$$name.txt 2>&1 || true; \
		./$(BIN_DIR)/$(BINARY) --json $$f > $(OUT_DIR)/$$name.json 2>&1 || true; \
		echo "captured $$name"; \
	done
	@./$(BIN_DIR)/$(BINARY) --trace auth-service $(TESTDATA)/05-test-b.jsonl \
		> $(OUT_DIR)/05-test-b-trace-auth-service.txt 2>&1 || true
	@echo "captured 05-test-b --trace auth-service"

## docker-build: build the container image
docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

## docker-run: run the image against ARGS (e.g. make docker-run ARGS=04-test-a.jsonl)
docker-run: docker-build
	docker run --rm -t -v "$(CURDIR)/$(TESTDATA):/data:ro" $(IMAGE):latest /data/$(ARGS)

## tools: install pinned development tooling
tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

## hooks: enable the versioned git hooks in .githooks/
hooks:
	@chmod +x .githooks/*
	git config core.hooksPath .githooks
	@echo "hooks: core.hooksPath -> .githooks (pre-commit runs gofmt, vet, lint)"

## clean: remove build and coverage artifacts
clean:
	rm -rf $(BIN_DIR) coverage.out coverage.html

.PHONY: help build run test test-short cover cover-html bench fmt vet lint tidy verify capture docker-build docker-run tools clean
