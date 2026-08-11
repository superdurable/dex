# Monorepo root helpers. Component builds stay in server/, sdk-*/, samples-*/.

ROOT_DIR := $(abspath .)

.PHONY: help ci-runner-check copyright copyright-check

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-22s %s\n", $$1, $$2}'

ci-runner-check: ## Verify CI workflows route main pushes to self-hosted runners
	bash .github/scripts/check-ci-runners.sh

copyright: ## Add or upgrade license headers using the legacy manifest
	cd server && GOWORK=off go run ./cmd/tools/copyright -rootDir "$(ROOT_DIR)"

copyright-check: ## Verify license classifications, body hashes, and headers
	cd server && GOWORK=off go run ./cmd/tools/copyright -rootDir "$(ROOT_DIR)" -verifyOnly
