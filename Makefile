# get rid of default behaviors, they're just noise
MAKEFLAGS += --no-builtin-rules
.SUFFIXES:

ROOT_DIR := $(abspath .)

.PHONY: help copyright copyright-check copyright-replace

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
