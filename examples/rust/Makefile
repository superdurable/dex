.PHONY: check test clippy fmt-check integTests workflowPatternsTests controlStructurePatternsTests flowSmokeTests

check:
	cargo check --all-targets --locked

test:
	cargo test --all-targets --locked

integTests:
	cargo test --locked --test dex_integration -- --ignored --test-threads=1 $(TEST_RUNNER_ARGS)

workflowPatternsTests:
	cargo test --locked --test workflow_patterns -- --ignored --test-threads=1 $(TEST_RUNNER_ARGS)

controlStructurePatternsTests:
	cargo test --locked --test control_structure_patterns -- --ignored --test-threads=1 $(TEST_RUNNER_ARGS)

flowSmokeTests:
	cargo test --locked --test flow_smoke -- --ignored --test-threads=1 $(TEST_RUNNER_ARGS)

clippy:
	cargo clippy --all-targets --locked -- -D warnings

fmt-check:
	cargo fmt --all --check
