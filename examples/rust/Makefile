.PHONY: check test clippy fmt-check

check:
	cargo check --all-targets --locked

test:
	cargo test --all-targets --locked

clippy:
	cargo clippy --all-targets --locked -- -D warnings

fmt-check:
	cargo fmt --all --check
