# FileManager (file.kppbwi.app) — run every target from the repo root.
SHELL := /bin/bash
.DEFAULT_GOAL := help

export PATH := $(PATH):/usr/local/go/bin:$(HOME)/go/bin
# The production server has 1 vCPU / 1.9 GB RAM: keep Go builds serial.
export GOMAXPROCS ?= 1
# Binary is built static (CGO_ENABLED=0, ADR-01); tests keep cgo on because -race needs it.

VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)

GO      := go -C backend
PNPM    := pnpm -C web
BIN     := bin/filemanager
WEBDIST := backend/internal/webassets/dist

.PHONY: help deps dev build build-backend build-web test test-backend test-web lint lint-backend lint-web \
        format check-deps clean ci vuln audit fuzz openapi-lint sqlc sqlc-check migrate check-sort-policy bench perf \
        install update shellcheck test-deploy

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-16s %s\n", $$1, $$2}'

deps: ## Install Go modules, web packages and dev tools
	corepack enable
	$(PNPM) install --frozen-lockfile
	$(GO) mod download
	@command -v golangci-lint >/dev/null || echo ">> golangci-lint missing: install the prebuilt binary (see README)"
	@command -v govulncheck >/dev/null || go install golang.org/x/vuln/cmd/govulncheck@latest
	@command -v sqlc >/dev/null || echo ">> sqlc missing: install the prebuilt binary (see README)"

dev: ## Run backend (APP_ENV=development, ./.devdata) and Vite together
	@mkdir -p .devdata/storage
	@( cd backend && APP_ENV=development APP_BASE_URL=http://localhost:5173 LISTEN_ADDR=127.0.0.1:8080 \
	   STORAGE_ROOT=$(CURDIR)/.devdata/storage DATA_DIR=$(CURDIR)/.devdata/storage/.system \
	   APP_SECRET=$${APP_SECRET:-$$(openssl rand -hex 32)} ALLOW_ROOT=true \
	   go run ./cmd/filemanager serve ) & \
	$(PNPM) dev; kill %1 2>/dev/null || true

build-web: ## Build the frontend into web/dist
	$(PNPM) build

build-backend: ## Build the Go binary (frontend must already be in $(WEBDIST))
	@mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o ../$(BIN) ./cmd/filemanager

build: build-web ## Build frontend, embed it, build the single binary bin/filemanager
	rm -rf $(WEBDIST) && mkdir -p $(WEBDIST) && cp -r web/dist/. $(WEBDIST)/ && touch $(WEBDIST)/.gitkeep
	$(MAKE) build-backend
	@echo "built $(BIN) ($(VERSION))"

test-backend: ## Go tests with race detector and coverage
	CGO_ENABLED=1 $(GO) test ./... -race -cover

test-web: ## Vitest
	$(PNPM) test

test: test-backend test-web ## All tests

lint-backend: ## golangci-lint + sort-policy check
	cd backend && golangci-lint run ./...
	$(MAKE) check-sort-policy

check-sort-policy: ## Natural sort must not rely on collate.Numeric (golang/go#67997, SPEC §8.3)
	@if grep -rn --include='*.go' 'collate\.Numeric' backend/; then echo 'collate.Numeric is forbidden (SPEC §8.3)'; exit 1; fi; echo 'sort policy: ok'

lint-web: ## eslint + tsc + prettier check
	$(PNPM) lint
	$(PNPM) typecheck
	$(PNPM) format:check

lint: lint-backend lint-web ## All linters

format: ## gofmt + prettier --write
	cd backend && gofmt -w . && (command -v golangci-lint >/dev/null && golangci-lint fmt ./... || true)
	$(PNPM) format

vuln: audit ## Alias of audit (kept for older docs)

audit: ## govulncheck + pnpm audit (fails on reachable Go vulns and high/critical npm advisories; SPEC §10.6 item 22)
	cd backend && govulncheck ./...
	$(PNPM) audit --audit-level=high

fuzz: ## Run every Go fuzz target for FUZZTIME (default 30s) each
	cd backend && for pkg in ./internal/fsjail ./internal/archive ./internal/listing ./internal/files ./internal/media ./internal/share ./internal/httpapi; do \
		for f in $$(go test $$pkg -list "^Fuzz" | grep "^Fuzz"); do \
			echo "== $$pkg $$f"; go test $$pkg -run "^$$$$" -fuzz "^$$f$$" -fuzztime $(or $(FUZZTIME),30s) || exit 1; \
		done; \
	done

openapi-lint: ## Validate docs/openapi.yaml (structure check; redocly/spectral in CI)
	@python3 -c "import yaml,sys; d=yaml.safe_load(open('docs/openapi.yaml')); assert d['openapi'].startswith('3.'); assert 'paths' in d; print('openapi.yaml: ok,', len(d['paths']), 'paths')"

sqlc: ## Regenerate type-safe queries (backend/internal/db/sqlc) from migrations + queries
	cd backend && sqlc generate

sqlc-check: sqlc ## Fail if the committed sqlc output is stale
	git diff --exit-code -- backend/internal/db/sqlc

migrate: ## Run goose migrations against the .env database: make migrate ARGS=status|up|down
	./$(BIN) migrate $(or $(ARGS),status) --env-file .env

check-deps: ## Print external tool table from the built binary
	./$(BIN) check-deps --env-file .env

bench: ## Listing benchmarks (10k entries)
	$(GO) test ./internal/listing/ -run '^$$' -bench . -benchmem

# Step 21 harness (SPEC §11.5, docs/PERF.md): the real binary on a scratch
# .perfdata/ folder (never STORAGE_ROOT/.env), once on every CPU and once
# pinned to CPU 0 (the 1 vCPU target of SPEC §1.6) with the load generator on
# the other CPUs. Results land in .perfdata/results-<n>cpu.json; the fixtures
# (≈ 3.5 GB) are removed afterwards. FM_PERF_ASSERT=0 only reports.
PERF_CPUS := $(shell nproc)
PERF_ENV  := FM_PERF_BIN=$(CURDIR)/$(BIN) GOMAXPROCS=$(PERF_CPUS)
perf: build-backend ## Performance harness: listing 10k cold/warm, navigation, thumbnails, 1 GiB upload/zip/extract RSS
	$(MAKE) bench GOMAXPROCS=$(PERF_CPUS)
	$(MAKE) bench GOMAXPROCS=1
	@mkdir -p .perfdata
	$(PERF_ENV) FM_PERF_DIR=$(CURDIR)/.perfdata/run FM_PERF_CPUS=$(PERF_CPUS) FM_PERF_LABEL=all-cpus \
		$(GO) test ./internal/perf/ -run '^TestPerf$$' -count=1 -v -timeout 40m
	$(PERF_ENV) FM_PERF_DIR=$(CURDIR)/.perfdata/run FM_PERF_CPUS=1 FM_PERF_LABEL=cpu0 \
		$(if $(filter 1,$(PERF_CPUS)),,taskset -c 1-$(shell echo $$(( $(PERF_CPUS) - 1 )))) \
		$(GO) test ./internal/perf/ -run '^TestPerf$$' -count=1 -v -timeout 40m

# Step 22 (SPEC §12, docs/DEPLOY-AAPANEL.md). install/update change the server
# (user, storage owner, systemd): run them only on purpose, e.g.
#   make install ARGS="--dry-run"   make install ARGS="--binary /path/filemanager"
#   make update ARGS="--ref v1.0.1" make update ARGS="--release filemanager-linux-amd64.tar.gz"
SUDO := $(if $(filter 0,$(shell id -u)),,sudo)
install: ## Install/repair on this server via deploy/install.sh (ARGS="--dry-run" to only print the plan)
	$(SUDO) bash deploy/install.sh $(ARGS)

update: ## Update with DB backup + automatic rollback via deploy/update.sh (ARGS="--dry-run|--ref R|--binary F|--release T")
	$(SUDO) bash deploy/update.sh $(ARGS)

shellcheck: ## Lint the deployment scripts
	shellcheck -x deploy/*.sh deploy/test/*.sh deploy/test/shim/*

test-deploy: shellcheck ## install.sh idempotency + update.sh rollback tests (scratch dirs, systemctl shim; needs bin/filemanager)
	bash deploy/test/install_test.sh
	bash deploy/test/update_rollback_test.sh

ci: lint test build openapi-lint sqlc-check ## What CI runs (lint, test, build, openapi, sqlc freshness)

clean: ## Remove build outputs
	rm -rf bin web/dist $(WEBDIST)/* backend/coverage.out
	mkdir -p $(WEBDIST) && touch $(WEBDIST)/.gitkeep
