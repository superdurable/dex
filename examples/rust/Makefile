.PHONY: check test clippy fmt-check integTests flowSmokeTests checkServerZdExclusions

# server-zd rejects Attribute Store sync, so DEX_INTEG_ENGINE=zd filters that sample.
SERVER_ZD_FLOW_SMOKE_ATTRIBUTE_STORE_SYNC_TESTS := \
	flow_smoke_patterns_entity_store

ifeq ($(DEX_INTEG_ENGINE),zd)
FLOW_SMOKE_ENGINE_FILTER_ARGS := --exact \
	$(addprefix --skip ,$(SERVER_ZD_FLOW_SMOKE_ATTRIBUTE_STORE_SYNC_TESTS))
endif

# $(1) lists test names and $(2) names the feature server-zd does not support.
define print_server_zd_exclusions
	@if [ "$(DEX_INTEG_ENGINE)" = zd ]; then \
		for test_name in $(1); do \
			echo "Excluded test $$test_name: server-zd does not support $(2)"; \
		done; \
	fi
endef

check:
	cargo check --all-targets --locked

test: checkServerZdExclusions
	cargo test --all-targets --locked

integTests:
	cargo test --locked --test dex_integration -- --ignored --test-threads=1 $(INTEG_ENGINE_FILTER_ARGS) $(TEST_RUNNER_ARGS)

flowSmokeTests:
	$(call print_server_zd_exclusions,$(SERVER_ZD_FLOW_SMOKE_ATTRIBUTE_STORE_SYNC_TESTS),Attribute Store sync)
	cargo test --locked --test flow_smoke -- --ignored --test-threads=1 $(FLOW_SMOKE_ENGINE_FILTER_ARGS) $(TEST_RUNNER_ARGS)

checkServerZdExclusions:
	./check-server-zd-exclusions.sh

clippy:
	cargo clippy --all-targets --locked -- -D warnings

fmt-check:
	cargo fmt --all --check
