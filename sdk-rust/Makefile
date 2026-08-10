.PHONY: fmt integration-test lint test

fmt:
	cargo fmt --all --check

lint:
	cargo clippy --workspace --all-targets --all-features --locked -- -D warnings

test:
	cargo test --workspace --locked

integration-test:
	./run-integration-tests.sh
