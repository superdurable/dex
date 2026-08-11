.PHONY: docs-check fmt integration-test lint test

docs-check:
	RUSTDOCFLAGS="-D warnings" cargo doc --locked --no-deps -p dex-sdk -p dex-blob-cache
	cargo test --locked -p dex-sdk -p dex-blob-cache --doc

fmt:
	cargo fmt --all --check

lint:
	cargo clippy --workspace --all-targets --all-features --locked -- -D warnings

test:
	cargo test --workspace --locked

integration-test:
	./run-integration-tests.sh
