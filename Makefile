# Monorepo root helpers. Component builds stay in server/, sdk-*/, samples-*/.

ROOT_DIR := $(abspath .)
GENERATED_CODE_PATHS := \
	server/gen/dexpb \
	sdk-go/gen/dexpb \
	sdk-java/src/main/java/io/superdurable/gen \
	sdk-python/dex/dexpb \
	sdk-typescript/src/gen

.PHONY: help ci-runner-check copyright copyright-check generated-code generated-code-check githooks

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-22s %s\n", $$1, $$2}'

githooks: ## Install commit-msg and pre-push Git hooks
	bash script/install-githooks

ci-runner-check: ## Verify CI workflows route main pushes to self-hosted runners
	bash .github/scripts/check-ci-runners.sh

copyright: ## Add or upgrade license headers using the legacy manifest
	cd server && GOWORK=off go run ./cmd/tools/copyright -rootDir "$(ROOT_DIR)"

copyright-check: ## Verify license classifications, body hashes, and headers
	cd server && GOWORK=off go run ./cmd/tools/copyright -rootDir "$(ROOT_DIR)" -verifyOnly

generated-code: ## Regenerate all checked-in code derived from protos
	$(MAKE) -C protos proto

generated-code-check: generated-code ## Verify checked-in generated code is current
	@changes="$$(git status --short --untracked-files=all -- $(GENERATED_CODE_PATHS))"; \
	if [ -n "$$changes" ]; then \
		echo "Generated code is outdated. Run 'make generated-code' and commit every change:"; \
		printf '%s\n' "$$changes"; \
		exit 1; \
	fi
