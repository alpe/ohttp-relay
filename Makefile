SHELL := /bin/bash

# Tools & settings (override on the command line if needed)
GO ?= go

# Project paths
BIN_DIR := bin
CMD_PKGS := $(wildcard cmd/*)

# Docker settings
DOCKER_REPO := alpetest

.DEFAULT_GOAL := help

help: ## Show this help
	@grep -E '^[a-zA-Z0-9_.-]+:.*?## .+' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS=":.*?## "}; {printf "\033[36m%-22s\033[0m %s\n", $$1, $$2}'
.PHONY: help


build-all: ## Build all binaries from cmd directory into ./bin
	@mkdir -p $(BIN_DIR)
	@for pkg in $(CMD_PKGS); do \
		binary=$$(basename $$pkg); \
		echo "Building $$binary..."; \
		$(GO) build -o $(BIN_DIR)/$$binary ./$$pkg; \
	done
.PHONY: build-all

test-all: test e2e-test ## Run all unit + e2e tests
.PHONY: test-all

test: test-unit ## Run unit tests
.PHONY: test

test-unit:
	$(GO) test ./...
.PHONY: test-unit

# -----------------------------
# E2E testing with Docker Compose
# -----------------------------
COMPOSE ?= docker compose

e2e-build: ## Build Docker images used by docker-compose for local arch (no buildx)
	@for pkg in $(CMD_PKGS); do \
	  binary=$$(basename $$pkg); \
	  if [ -f "$$pkg/Dockerfile" ]; then \
	    echo "Building Docker image for $$binary..."; \
	    docker build -t $(DOCKER_REPO)/$$binary:local -f $$pkg/Dockerfile .; \
	  fi; \
	done
.PHONY: e2e-build

e2e-up: ## Start docker-compose stack for e2e tests and wait for readiness
	$(COMPOSE) up -d
	# Wait for Envoy readiness via admin endpoint inside the compose network
	set -e; \
	for i in $$(seq 1 60); do \
	  echo "[e2e] waiting for envoy admin /ready ($$i/60)..."; \
	  if docker run --rm --network ohttpnet curlimages/curl:8.11.1 -fsS http://envoy:9901/ready >/dev/null 2>&1; then \
	    echo "[e2e] envoy is up"; \
	    break; \
	  fi; \
	  sleep 2; \
	done
.PHONY: e2e-up

e2e-test: ## Run test-client against the running stack
	$(COMPOSE) run --rm test-client-rly
.PHONY: e2e-test

e2e-down: ## Stop docker-compose stack and remove volumes
	$(COMPOSE) down -v
.PHONY: e2e-down

e2e: ## Full e2e flow: build images, start stack, run client, teardown
	set -euo pipefail; \
	trap '$(COMPOSE) logs > e2e-logs.txt || true; $(COMPOSE) down -v || true' EXIT; \
	$(MAKE) e2e-build; \
	$(MAKE) e2e-up; \
	$(MAKE) e2e-test; \
	$(MAKE) e2e-down
.PHONY: e2e

fmt: ## Format Go sources
	$(GO) fmt ./...
.PHONY: fmt

docker-build-all-linux: ## cross build all docker artifacts for linux/amd64
	@for pkg in $(CMD_PKGS); do \
		binary=$$(basename $$pkg); \
		if [ -f "$$pkg/Dockerfile" ]; then \
			echo "Building Docker image for $$binary..."; \
			docker buildx build --platform=linux/amd64 -t $(DOCKER_REPO)/$$binary:local -f $$pkg/Dockerfile .; \
		fi \
	done
.PHONY: docker-build-all-linux

docker-publish-all-linux: ## publish all local dev docker builds to docker hub
	@for pkg in $(CMD_PKGS); do \
		binary=$$(basename $$pkg); \
		if [ -f "$$pkg/Dockerfile" ]; then \
			echo "pushing Docker image for $(DOCKER_REPO)/$$binary:local..."; \
			docker push $(DOCKER_REPO)/$$binary:local; \
		fi \
	done
.PHONY: docker-publish-all-linux
