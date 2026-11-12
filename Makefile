# Monorepo root helpers. Component builds stay in their module directories.

ROOT_DIR := $(abspath .)
GENERATED_CODE_PATHS := \
	sdk-go/gen/dexpb \
	sdk-java/src/main/java/io/superdurable/gen \
	sdk-python/dex/dexpb \
	sdk-typescript/src/gen

.PHONY: help ci-runner-check copyright copyright-check copyright-test generated-code generated-code-check githooks docs-prose-check docs-prose-fix release-tooling-test

release-tooling-test: ## Verify changed-component selection and release preflight
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s script/release -p '*_test.py'

.PHONY: regenerate-derived regenerate-derived-test
regenerate-derived: ## Take the upstream copy of conflicted derived files and rerun their generators (ARGS=--dry-run|--all)
	python3 script/regenerate_derived.py $(ARGS)

regenerate-derived-test: ## Verify derived-file conflict resolution against fixture repositories
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s script -p 'regenerate_derived_integ_test.py'

.PHONY: server-zd-simulation-runner-test
server-zd-simulation-runner-test: ## Verify nightly process supervision and seed partitioning
	PYTHONDONTWRITEBYTECODE=1 python3 script/test_server_zd_simulation_run.py

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-22s %s\n", $$1, $$2}'

githooks: ## Install commit-msg and pre-commit hooks (agent attribution + docs prose)
	bash script/install-githooks

docs-prose-check: ## Check product-doc MDX prose and structure (no README links, group headers open overviews, pages end with Next, positive-first openings)
	python3 script/docs_no_inline_backticks.py
	cd docs && node --no-warnings tests/docs-structure.mjs
	cd docs && node --no-warnings tests/docs-positive-first.mjs

docs-prose-fix: ## No-op: identifiers use inline code since D8
	python3 script/docs_no_inline_backticks.py --fix

ci-runner-check: ## Verify CI workflows use GitHub-hosted runners
	bash .github/scripts/check-ci-runners.sh

copyright: ## Add or upgrade license headers using the legacy manifest
	cd script/licenseheaders/copyright && GOWORK=off go run . -rootDir "$(ROOT_DIR)"

copyright-check: ## Verify license classifications, body hashes, and headers
	cd script/licenseheaders/copyright && GOWORK=off go run . -rootDir "$(ROOT_DIR)" -verifyOnly

copyright-test: ## Test license header classification and rendering
	cd script/licenseheaders/copyright && GOWORK=off go test ./...

generated-code: ## Regenerate all checked-in code derived from protos
	$(MAKE) -C protos proto

generated-code-check: generated-code ## Verify checked-in generated code is current
	@changes="$$(git status --short --untracked-files=all -- $(GENERATED_CODE_PATHS))"; \
	if [ -n "$$changes" ]; then \
		echo "Generated code is outdated. Run 'make generated-code' and commit every change:"; \
		printf '%s\n' "$$changes"; \
		exit 1; \
	fi

.PHONY: rust-rules-check
rust-rules-check: ## Check repository-wide Rust conventions and checker fixtures
	python3 script/check-rust-rules.py
	python3 -m unittest discover -s script -p check_rust_rules_test.py
