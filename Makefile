# Developer entry points. CI calls `go` directly (windows-latest has no GNU make),
# so every target here must stay a thin wrapper over a command you can also type.
.DEFAULT_GOAL := help
SHELL := /bin/sh

GOLANGCI_LINT_VERSION := $(shell cat .golangci-lint-version)
GOVULNCHECK_VERSION := v1.7.0
GOBIN := $(shell go env GOPATH)/bin
GOEXE := $(shell go env GOEXE)
GOLANGCI_LINT := $(GOBIN)/golangci-lint$(GOEXE)
GOVULNCHECK := $(GOBIN)/govulncheck$(GOEXE)

# Tests are shuffled; the seed is printed on failure, replay it with `-shuffle=<seed>`.
GOTEST_FLAGS := -timeout 5m -shuffle=on

.PHONY: help
help: ## List targets
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build every package, then bin/travis with bin/travis-broker's hash stamped in (R-23)
	go build ./...
	go build -o bin/travis-broker$(GOEXE) ./cmd/travis-broker
	go build -o bin/travis$(GOEXE) -ldflags "-X main.brokerSHA256=$$( (sha256sum bin/travis-broker$(GOEXE) 2>/dev/null || shasum -a 256 bin/travis-broker$(GOEXE)) | cut -d' ' -f1)" ./cmd/travis

.PHONY: chat
chat: build ## Build, then run `travis chat` against the current directory (pass flags with ARGS="...")
	./bin/travis$(GOEXE) chat $(ARGS)

.PHONY: audio
audio: ## Build bin/travis-audio, the optional voice-input helper; needs cgo and a C compiler (§5.10)
	CGO_ENABLED=1 go build -o bin/travis-audio$(GOEXE) ./cmd/travis-audio

.PHONY: install
install: ## Install travis and travis-broker to $(go env GOPATH)/bin with the hash stamped in, so `travis chat` works from any directory
	go build -o $(GOBIN)/travis-broker$(GOEXE) ./cmd/travis-broker
	go build -o $(GOBIN)/travis$(GOEXE) -ldflags "-X main.brokerSHA256=$$( (sha256sum $(GOBIN)/travis-broker$(GOEXE) 2>/dev/null || shasum -a 256 $(GOBIN)/travis-broker$(GOEXE)) | cut -d' ' -f1)" ./cmd/travis

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
lint: $(GOLANGCI_LINT) ## Run golangci-lint at the pinned version
	$(GOLANGCI_LINT) run

.PHONY: fmt
fmt: $(GOLANGCI_LINT) ## Apply gofmt and goimports
	$(GOLANGCI_LINT) fmt

.PHONY: tidy
tidy: ## Verify module downloads and that go.mod/go.sum are already tidy
	go mod verify
	go mod tidy -diff

.PHONY: vuln
vuln: $(GOVULNCHECK) ## Check dependencies against the Go vulnerability database
	$(GOVULNCHECK) ./...

.PHONY: check
check: build vet lint test-race tidy vuln ## Everything CI runs, on this machine only

# Version-pinned tool installs. `latest` would turn unrelated work red on a Tuesday.
$(GOLANGCI_LINT):
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(GOVULNCHECK):
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
