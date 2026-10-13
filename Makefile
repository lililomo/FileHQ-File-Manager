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
        format check-deps clean ci vuln openapi-lint

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-16s %s\n", $$1, $$2}'

deps: ## Install Go modules, web packages and dev tools
	corepack enable
	$(PNPM) install --frozen-lockfile
	$(GO) mod download
	@command -v golangci-lint >/dev/null || echo ">> golangci-lint missing: install the prebuilt binary (see README)"
	@command -v govulncheck >/dev/null || go install golang.org/x/vuln/cmd/govulncheck@latest

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

lint-backend: ## golangci-lint
	cd backend && golangci-lint run ./...

lint-web: ## eslint + tsc + prettier check
	$(PNPM) lint
	$(PNPM) typecheck
	$(PNPM) format:check

lint: lint-backend lint-web ## All linters

format: ## gofmt + prettier --write
	cd backend && gofmt -w . && (command -v golangci-lint >/dev/null && golangci-lint fmt ./... || true)
	$(PNPM) format

vuln: ## govulncheck + pnpm audit
	cd backend && govulncheck ./...
	$(PNPM) audit --audit-level=high || true

openapi-lint: ## Validate docs/openapi.yaml (structure check; redocly/spectral in CI)
	@python3 -c "import yaml,sys; d=yaml.safe_load(open('docs/openapi.yaml')); assert d['openapi'].startswith('3.'); assert 'paths' in d; print('openapi.yaml: ok,', len(d['paths']), 'paths')"

check-deps: ## Print external tool table from the built binary
	./$(BIN) check-deps --env-file .env

ci: lint test build openapi-lint ## What CI runs (lint, test, build, openapi)

clean: ## Remove build outputs
	rm -rf bin web/dist $(WEBDIST)/* backend/coverage.out
	mkdir -p $(WEBDIST) && touch $(WEBDIST)/.gitkeep
