# ============================================================================
#  immich-go — Build system for amd64 (dev) & arm64 (ODROID-C2 production)
#  Pure Go, CGO disabled everywhere: one static binary per architecture.
#
#  Quick start:
#     make deps        # download modules
#     make build-all   # compile server+migrate+installer for linux/amd64+arm64
#     make run-local   # run the server natively on this machine (x86_64)
#     make smoke       # boot server on :2283 in a temp dir + curl health/login
#     make test-upload # real multipart upload through POST /api/assets
#     make release     # tarballs ready to scp to the ODROID
#     make docker-arm64# build ARM64 docker image via buildx
#
#  Every target that compiles writes a timestamped trace into logs/build/
#  so a failing CI/operator run can be debugged from the log alone.
# ============================================================================

SHELL      := /bin/bash
VERSION    ?= 1.100.0
BUILD_DIR  := bin
LOG_DIR    := logs/build
TS         := $(shell date +%Y%m%d-%H%M%S)

GO         ?= go
GOFLAGS    := -trimpath
LDFLAGS    := -s -w -X main.version=$(VERSION)
CGO        := 0

# Module path used by `go install` style targets
MODULE     := $(shell $(GO) list -m 2>/dev/null || echo immich-go)

# Binary entrypoints (each is its own package under ./cmd)
SERVER_BIN    := cmd/server
MIGRATE_BIN   := cmd/migrate
INSTALLER_BIN := cmd/installer

PLATFORMS  := linux/amd64 linux/arm64

.DEFAULT_GOAL := help

# ----------------------------------------------------------------------------
# Meta targets
# ----------------------------------------------------------------------------

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

.PHONY: deps
deps: ## Download and verify Go module dependencies
	@mkdir -p $(LOG_DIR)
	$(GO) mod download 2>&1 | tee $(LOG_DIR)/deps-$(TS).log
	$(GO) mod verify  2>&1 | tee -a $(LOG_DIR)/deps-$(TS).log

.PHONY: tidy
tidy: ## go mod tidy
	$(GO) mod tidy

# ----------------------------------------------------------------------------
# Local development (native arch — x86_64 laptop / WSL / CI)
# ----------------------------------------------------------------------------

.PHONY: build
build: ## Build all three binaries for the NATIVE platform (fast, no cross-compile)
	@mkdir -p $(BUILD_DIR) $(LOG_DIR)
	@echo "==> native build ($(shell $(GO) env GOOS)/$(shell $(GO) env GOARCH))"
	CGO_ENABLED=$(CGO) $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/immich-go          ./$(SERVER_BIN)
	CGO_ENABLED=$(CGO) $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/immich-migrate      ./$(MIGRATE_BIN)
	CGO_ENABLED=$(CGO) $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/immich-go-installer ./$(INSTALLER_BIN)
	@ls -lh $(BUILD_DIR)

.PHONY: run-local
run-local: ## Run server locally on :2283 with data in ./dev-data (Ctrl+C to stop)
	@mkdir -p dev-data
	IMMICH_DATA_DIR=./dev-data \
	IMMICH_JWT_SECRET=dev-secret-not-for-production \
	IMMICH_ADMIN_EMAIL=admin@example.com \
	IMMICH_ADMIN_PASSWORD=admin123 \
	$(GO) run $(SERVER_BIN)

.PHONY: vet
vet: ## Static analysis (go vet ./...)
	$(GO) vet ./...

.PHONY: test
test: ## Run unit tests
	$(GO) test ./... 2>&1 | tee $(LOG_DIR)/test-$(TS).log

# ----------------------------------------------------------------------------
# Cross-compilation — amd64 + arm64 (pure Go, no toolchain needed)
# ----------------------------------------------------------------------------
# Pattern: <os>-<arch>/<binary>. We iterate PLATFORMS x BINARIES.

.PHONY: build-amd64
build-amd64: ## Build all binaries for linux/amd64
	@$(MAKE) --no-print-directory _cross GOOS=linux GOARCH=amd64

.PHONY: build-arm64
build-arm64: ## Build all binaries for linux/arm64 (ODROID-C2)
	@$(MAKE) --no-print-directory _cross GOOS=linux GOARCH=arm64

.PHONY: build-all
build-all: ## Build everything for BOTH architectures, logging each step
	@mkdir -p $(LOG_DIR)
	@set -e; ok=1; \
	for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; \
	  log="$(LOG_DIR)/build-$$os-$$arch-$(TS).log"; \
	  echo "==> $$p  (trace -> $$log)"; \
	  if $(MAKE) --no-print-directory _cross GOOS=$$os GOARCH=$$arch >"$$log" 2>&1; then \
	    echo "    PASS"; \
	  else \
	    echo "    FAIL — last lines of $$log:"; tail -n 20 "$$log"; ok=0; \
	  fi; \
	done; \
	if [ $$ok -eq 1 ]; then echo "build-all: ALL OK"; ls -lh $(BUILD_DIR); \
	else echo "build-all: FAILURES (see $(LOG_DIR))"; exit 1; fi

.PHONY: _cross
_cross: ## internal: single-platform build of the three binaries
	@mkdir -p $(BUILD_DIR)
	echo "building for $(GOOS)/$(GOARCH) version=$(VERSION)"
	CGO_ENABLED=$(CGO) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/immich-go-linux-$(GOARCH)          ./$(SERVER_BIN)
	CGO_ENABLED=$(CGO) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/immich-migrate-linux-$(GOARCH)      ./$(MIGRATE_BIN)
	CGO_ENABLED=$(CGO) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/immich-go-installer-linux-$(GOARCH) ./$(INSTALLER_BIN)
	file $(BUILD_DIR)/immich-go-linux-$(GOARCH) || true

# ----------------------------------------------------------------------------
# Verification pipeline — the "build log" you asked for
# ----------------------------------------------------------------------------

.PHONY: verify-build
verify-build: ## Full gate: deps + vet + native build + both cross builds; one combined log
	@mkdir -p $(LOG_DIR)
	@log="$(LOG_DIR)/verify-$(TS).log"; \
	echo "verify-build trace -> $$log"; \
	{ \
	  echo "=== immich-go build verification ==="; \
	  echo "date:    $$(date -Is)"; \
	  echo "host:    $$(uname -srm)"; \
	  echo "go:      $$($(GO) version)"; \
	  echo "version: $(VERSION)"; \
	  echo; \
	  echo "--- go.mod ---"; cat go.mod; \
	  echo "--- go env (subset) ---"; $(GO) env GOOS GOARCH CGO_ENABLED GOMODCACHE; \
	  echo; \
	} >"$$log" 2>&1; \
	step(){ printf '%s\n' "--- $$1 ---" | tee -a "$$log"; \
	        if eval "$$2" >>"$$log" 2>&1; then echo "PASS: $$1"; \
	        else echo "FAIL: $$1 (see $$log)"; tail -n 15 "$$log"; exit 1; fi; }; \
	step "go mod download"  "$(GO) mod download && $(GO) mod verify"; \
	step "go vet ./..."     "$(GO) vet ./..."; \
	step "native build"     "$(MAKE) --no-print-directory build"; \
	step "linux/amd64"      "$(MAKE) --no-print-directory build-amd64"; \
	step "linux/arm64"      "$(MAKE) --no-print-directory build-arm64"; \
	echo; echo "artifacts:" | tee -a "$$log"; ls -lh $(BUILD_DIR) | tee -a "$$log"; \
	echo "===> VERIFY-BUILD SUCCESS — full trace: $$log"

.PHONY: smoke
smoke: ## Boot server in a throwaway dir, curl health/version/login/user-me, kill it
	@bash scripts/smoke.sh $(BUILD_DIR)/immich-go

.PHONY: test-upload
test-upload: ## Real multipart upload round-trip (needs ./bin/immich-go built)
	@bash scripts/test_upload.sh $(BUILD_DIR)/immich-go

# ----------------------------------------------------------------------------
# Packaging / deployment
# ----------------------------------------------------------------------------

.PHONY: release
release: build-all ## Create per-arch tarballs in dist/
	@mkdir -p dist
	@for arch in amd64 arm64; do \
	  d="dist/immich-go-v$(VERSION)-linux-$$arch"; rm -rf "$$d"; mkdir -p "$$d/bin"; \
	  cp $(BUILD_DIR)/immich-go-linux-$$arch          "$$d/bin/"; \
	  cp $(BUILD_DIR)/immich-migrate-linux-$$arch     "$$d/bin/"; \
	  cp $(BUILD_DIR)/immich-go-installer-linux-$$arch "$$d/bin/" 2>/dev/null || true; \
	  cp README.md LICENSE "$$d/" 2>/dev/null || cp README.md "$$d/"; \
	  tar -C dist -czf "$$d.tar.gz" "$$(basename $$d)"; rm -rf "$$d"; \
	done
	@ls -lh dist/

.PHONY: clean
clean: ## Remove build outputs, logs and dev data
	rm -rf $(BUILD_DIR) $(LOG_DIR) dist dev-data

# ----------------------------------------------------------------------------
# Docker (multi-arch via buildx)
# ----------------------------------------------------------------------------

.PHONY: docker-buildx
docker-buildx: ## Build & push multi-arch image (needs docker buildx builder)
	docker buildx build --platform linux/amd64,linux/arm64 \
		-t immich-go:$(VERSION) --load . 2>&1 | tee $(LOG_DIR)/docker-$(TS).log

.PHONY: docker-arm64
docker-arm64: ## Build ARM64 image only (deploy to ODROID without registry)
	docker buildx build --platform linux/arm64 -t immich-go:$(VERSION)-arm64 --load . \
		2>&1 | tee $(LOG_DIR)/docker-arm64-$(TS).log

.PHONY: dev-up
dev-up: ## Start local test stack (server + legacy postgres) via compose
	docker compose -f docker-compose.dev.yml up --build

.PHONY: dev-down
dev-down: ## Stop local test stack
	docker compose -f docker-compose.dev.yml down
