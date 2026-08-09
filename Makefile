APP       := flomo
MODULE    := github.com/songquanpeng/flomo-cli
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOFLAGS   ?=
LDFLAGS   := -s -w -X main.version=$(VERSION)
GO_INSTALL := go install $(MODULE)/cmd/flomo@latest

.DEFAULT_GOAL := help

.PHONY: help build test test-race vet fmt check install update clean

help: ## Show available development targets
	@awk 'BEGIN {FS = ":.*## "; printf "Usage: make <target>\n\nTargets:\n"} /^[a-zA-Z_-]+:.*## / {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build ./bin/flomo
	@mkdir -p bin
	go build $(GOFLAGS) -trimpath -ldflags "$(LDFLAGS)" -o bin/$(APP) ./cmd/flomo

test: ## Run all unit and integration tests
	go test $(GOFLAGS) ./...

test-race: ## Run tests with the race detector
	go test $(GOFLAGS) -race ./...

vet: ## Run Go static analysis
	go vet ./...

fmt: ## Format all Go source files
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

check: test vet ## Run the checks used before release

install: ## Install the latest released flomo into GOBIN
	$(GO_INSTALL)

update: install ## Update flomo to the latest released version

clean: ## Remove build artifacts
	rm -rf ./bin
