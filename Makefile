# get rid of default behaviors, they're just noise
MAKEFLAGS += --no-builtin-rules
.SUFFIXES:

ROOT_DIR := $(abspath .)
SERVER_DIR := $(ROOT_DIR)/server

.PHONY: help copyright copyright-check copyright-replace \
	unit-test integ-test postgres-up postgres-down postgres-logs

default: help

help: ## Print available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}' | sort

copyright: ## Add missing AGPL license headers to .go/.proto files
	cd server && go run ./cmd/tools/copyright -rootDir "$(ROOT_DIR)"

copyright-check: ## Verify AGPL license headers (fails if any are missing)
	cd server && go run ./cmd/tools/copyright -rootDir "$(ROOT_DIR)" -verifyOnly

copyright-replace: ## Replace existing license headers from script/licenseheader.txt
	cd server && go run ./cmd/tools/copyright -rootDir "$(ROOT_DIR)" -replace

unit-test: ## Go tests without external deps (includes membership)
	cd "$(ROOT_DIR)/common-go" && go test ./... -race -count=1 -timeout 5m
	cd "$(ROOT_DIR)/protos" && go test ./... -race -count=1 -timeout 5m
	cd "$(SERVER_DIR)" && go test $$(go list ./... | grep -vE '/integTests(/|$$)') -race -count=1 -timeout 15m

integ-test: ## server/integTests (expects Postgres via postgres-up)
	@pkgs="$$(cd "$(SERVER_DIR)" && go list ./integTests/... 2>/dev/null || true)"; \
	if [ -z "$${pkgs}" ]; then echo ">> no integTests packages yet"; exit 0; fi; \
	cd "$(SERVER_DIR)" && go test ./integTests/... -race -count=1 -timeout 20m

# Postgres daemon (+ default DBs). Integ TestMain then runs setup.sh with a suffix.
postgres-up: ## Start Postgres for integ-test
	cd "$(SERVER_DIR)" && docker compose -f dependency-postgres.yaml up -d --wait

postgres-down: ## Stop Postgres and remove volumes
	cd "$(SERVER_DIR)" && docker compose -f dependency-postgres.yaml down -v

postgres-logs: ## Dump Postgres compose logs
	cd "$(SERVER_DIR)" && docker compose -f dependency-postgres.yaml logs
