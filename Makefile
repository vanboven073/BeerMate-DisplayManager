# BeerMate Display Manager — build orchestration.
#
# The production artifact is a single linux/arm64 binary with the frontend
# embedded. `make release` produces it; the Jetson never needs Node or a Go
# toolchain.

BINARY      := beermate-display-manager
PKG         := github.com/vanboven073/BeerMate-DisplayManager
CMD         := ./cmd/beermate-display-manager
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE        := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.BuildDate=$(DATE)

DIST        := dist

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

## ---- frontend ----------------------------------------------------------

.PHONY: frontend
frontend: ## Type-check, lint and build the frontend into internal/web/dist
	cd web && npm ci --no-audit --no-fund
	cd web && npm run typecheck
	cd web && npm run build

.PHONY: frontend-dev
frontend-dev: ## Run the Vite dev server (proxies to a local backend on :8080)
	cd web && npm run dev

## ---- backend -----------------------------------------------------------

.PHONY: fmt
fmt: ## Format Go code
	gofmt -w $(shell git ls-files '*.go')

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: test
test: ## Run Go tests
	go test ./...

.PHONY: test-race
test-race: ## Run Go tests with the race detector
	go test -race ./...

.PHONY: build
build: ## Build a native binary (frontend must be built first)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY) $(CMD)

.PHONY: build-arm64
build-arm64: ## Cross-compile the production linux/arm64 binary
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
		go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY) $(CMD)

## ---- combined ----------------------------------------------------------

.PHONY: release
release: frontend build-arm64 ## Build the frontend and the ARM64 release binary
	@echo "Release binary: $(DIST)/$(BINARY)"
	@file $(DIST)/$(BINARY) 2>/dev/null || true

.PHONY: check
check: fmt vet test ## Format, vet and test

.PHONY: clean
clean: ## Remove build output
	rm -rf $(DIST) internal/web/dist/assets internal/web/dist/index.html

.PHONY: run
run: ## Run locally with a dev data dir (browser+dpms mocked)
	BEERMATE_DATA_DIR=./.data BEERMATE_CONFIG_DIR=./.data/etc \
	BEERMATE_LISTEN_ADDR=127.0.0.1:8080 BEERMATE_BROWSER_ENABLED=false \
	BEERMATE_DPMS_DRIVER=mock BEERMATE_LOG_FORMAT=text \
		go run $(CMD)

.PHONY: verify-scripts
verify-scripts: ## Syntax-check the deployment shell scripts
	@for f in scripts/*.sh; do bash -n "$$f" && echo "ok  $$f"; done
