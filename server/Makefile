# Backend test URIs. The integration suite runs against the backend selected
# by DEX_TEST_PERSISTENCE_BACKEND (postgres by default — the server default).
# The Postgres store package (internal/persistence/postgres) hard-REQUIRES
# DEX_TEST_POSTGRES_URI (its TestMain fails if unset — it must never silently
# skip), so it is excluded from targets that run without a Postgres (see
# NON_PG below). The mongo/engine packages still skip when their URI is unset.
# Default Mongo:    mongodb://localhost:27018 (mongos from dependency-docker-mongo-1shard.yaml)
# Default Postgres: postgres://dex:dex@localhost:5432 (dependency-docker-postgres.yaml)
MONGO_URI ?= mongodb://localhost:27018
POSTGRES_URI ?= postgres://dex:dex@localhost:5432/?sslmode=disable
TEST_FLAGS ?= -count=1 -timeout 600s

# NON_PG lists every package except the Postgres store package, for targets
# that run without a Postgres available.
NON_PG = $$(go list ./... | grep -v '/internal/persistence/postgres$$')

# Test packages run in parallel by default (go test uses GOMAXPROCS).
# Cross-package isolation is by per-store database prefix: every test package
# (especially integration sub-packages) routes its stores to a distinct
# `dex_test_<pkg>_*` database/schema, so packages never share state.
# See .cursor/rules/test-database-isolation.mdc for the full rules.

BINARY ?= server

.PHONY: build test test-postgres test-mongo test-unit test-integration test-cluster

## Build the server binary.
build:
	go build -o $(BINARY) ./cmd/main

## Run the full suite against BOTH backends. Requires both Postgres and MongoDB.
test: test-postgres test-mongo

## Run the full suite against Postgres (the default backend).
test-postgres:
	DEX_TEST_PERSISTENCE_BACKEND=postgres DEX_TEST_POSTGRES_URI=$(POSTGRES_URI) go test ./... $(TEST_FLAGS)

## Run the full suite against MongoDB. Excludes the Postgres store package
## (which requires a Postgres); the integration suite still runs on Mongo.
test-mongo:
	DEX_TEST_PERSISTENCE_BACKEND=mongo DEX_TEST_MONGO_URI=$(MONGO_URI) go test $(NON_PG) $(TEST_FLAGS)

## Run only unit tests (no database required). Excludes the Postgres store
## package, which requires a Postgres.
test-unit:
	go test $(NON_PG) -short $(TEST_FLAGS)

## Run integration tests only against Postgres (override BACKEND/URI for mongo).
test-integration:
	DEX_TEST_PERSISTENCE_BACKEND=postgres DEX_TEST_POSTGRES_URI=$(POSTGRES_URI) go test ./internal/integration/... -v $(TEST_FLAGS)

## Run cluster integration tests only against Postgres.
test-cluster:
	DEX_TEST_PERSISTENCE_BACKEND=postgres DEX_TEST_POSTGRES_URI=$(POSTGRES_URI) go test ./internal/integration/cluster/... -v $(TEST_FLAGS)
