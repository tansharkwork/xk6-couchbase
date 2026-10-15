# xk6-couchbase — build & development tasks.
#
# Building a k6 extension requires `xk6`, which compiles a custom k6 binary with
# this extension linked in (a plain `go build` cannot produce a runnable k6).

MODULE      := github.com/thotasrinath/xk6-couchbase
BINARY      := xk6-couchbase
XK6_VERSION ?= v1.4.14
XK6         := go run go.k6.io/xk6/cmd/xk6@$(XK6_VERSION)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

$(BINARY): $(filter-out %_test.go,$(wildcard *.go)) go.mod go.sum
	$(XK6) build --output $(BINARY) --with $(MODULE)=.

.PHONY: build
build: $(BINARY) ## Build a k6 binary with this extension (from local source)

.PHONY: test
test: ## Run unit tests
	go test -count=1 ./...

.PHONY: test-race
test-race: ## Run unit tests with the race detector
	go test -count=1 -race ./...

.PHONY: cover
cover: ## Run tests and open an HTML coverage report
	go test -count=1 -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

.PHONY: vet
vet: ## Run go vet (no external tools required)
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint (install: https://golangci-lint.run)
	@command -v golangci-lint >/dev/null 2>&1 \
		|| { echo "golangci-lint not found; install from https://golangci-lint.run"; exit 1; }
	golangci-lint run

.PHONY: fmt
fmt: ## Format Go sources
	gofmt -s -w .

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy

.PHONY: check
check: fmt vet test ## Format, vet, and test (pre-commit gate)

# --- Local Couchbase & example validation ---
# Override creds/host to point at an existing cluster, e.g.:
#   make validate CB_HOST=10.0.0.5 CB_USER=admin CB_PASS=secret
CB_HOST ?= localhost
CB_USER ?= Administrator
CB_PASS ?= password
CB_BUCKET ?= test

# Pass these to the scripts through the environment rather than pasting them
# into the shell command, so spaces, quotes, and ; or & stay part of the value.
# $(value ...) also keeps a "$" literal instead of letting make expand it.
override CB_HOST := $(value CB_HOST)
override CB_USER := $(value CB_USER)
override CB_PASS := $(value CB_PASS)
override CB_BUCKET := $(value CB_BUCKET)
export CB_HOST CB_USER CB_PASS CB_BUCKET

.PHONY: couchbase-up
couchbase-up: ## Start & init a local Couchbase (reuses an existing one if reachable)
	./scripts/couchbase.sh up

.PHONY: couchbase-down
couchbase-down: ## Remove the local Couchbase container created by couchbase-up
	./scripts/couchbase.sh down

.PHONY: validate
validate: $(BINARY) couchbase-up ## Build, ensure Couchbase, and run password examples end-to-end
	./scripts/run-examples.sh

# Enterprise certificate acceptance is opt-in and separate from Community validation.
.PHONY: couchbase-enterprise-up
couchbase-enterprise-up: ## Start and configure the local Enterprise certificate fixture
	./scripts/couchbase-enterprise.sh up

.PHONY: couchbase-enterprise-down
couchbase-enterprise-down: ## Remove the managed Enterprise container and test certificates
	./scripts/couchbase-enterprise.sh down

.PHONY: validate-cert
validate-cert: $(BINARY) couchbase-enterprise-up ## Run positive and negative Enterprise certificate acceptance
	BIN="$(abspath $(BINARY))" ./scripts/run-cert-examples.sh

.PHONY: xk6-version
xk6-version: ## Print the pinned xk6 version (used by the release workflow)
	@echo $(XK6_VERSION)

.PHONY: clean
clean: ## Remove build artifacts
	rm -f $(BINARY) coverage.out
