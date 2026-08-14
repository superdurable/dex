.PHONY: check test clippy fmt-check integTests

check:
	cargo check --all-targets --locked

test:
	cargo test --all-targets --locked

integTests:
	cargo test --locked --test dex_integration -- --ignored --test-threads=1

clippy:
	cargo clippy --all-targets --locked -- -D warnings

fmt-check:
	cargo fmt --all --check
