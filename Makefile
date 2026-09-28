# Developer entry points. CI calls `go` directly (windows-latest has no GNU make),
# so every target here must stay a thin wrapper over a command you can also type.
.DEFAULT_GOAL := help
SHELL := /bin/sh

GOLANGCI_LINT_VERSION := $(shell cat .golangci-lint-version)
GOVULNCHECK_VERSION := v1.7.0
GOBIN := $(shell go env GOPATH)/bin

# Tests are shuffled; the seed is printed on failure, replay it with `-shuffle=<seed>`.
GOTEST_FLAGS := -timeout 5m -shuffle=on

.PHONY: help
help: ## List targets
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build every package
	go build ./...

.PHONY: test
test: ## Run the unit tests
	go test $(GOTEST_FLAGS) ./...

.PHONY: test-race
test-race: ## Run the unit tests under the race detector, as CI does
	go test -race $(GOTEST_FLAGS) ./...

.PHONY: cover
cover: ## Run the tests with coverage and print the per-function report
	go test -race $(GOTEST_FLAGS) -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

.PHONY: vet
vet: ## go vet for the host and for the other two supported OSes
	go vet ./...
	GOOS=windows go vet ./...
	GOOS=darwin go vet ./...

.PHONY: lint
lint: $(GOBIN)/golangci-lint ## Run golangci-lint at the pinned version
	$(GOBIN)/golangci-lint run

.PHONY: fmt
fmt: $(GOBIN)/golangci-lint ## Apply gofmt and goimports
	$(GOBIN)/golangci-lint fmt

.PHONY: tidy
tidy: ## Verify module downloads and that go.mod/go.sum are already tidy
	go mod verify
	go mod tidy -diff

.PHONY: vuln
vuln: $(GOBIN)/govulncheck ## Check dependencies against the Go vulnerability database
	$(GOBIN)/govulncheck ./...

.PHONY: check
check: build vet lint test-race tidy vuln ## Everything CI runs, on this machine only

# Version-pinned tool installs. `latest` would turn unrelated work red on a Tuesday.
$(GOBIN)/golangci-lint:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(GOBIN)/govulncheck:
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
