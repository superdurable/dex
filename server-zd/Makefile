.PHONY: check test clippy fmt-check build-server

# Build only one crate while iterating: cargo test -p <crate>

check:
	cargo check --all-targets --locked

test:
	cargo test --all-targets --locked

clippy:
	cargo clippy --all-targets --locked -- -D warnings

fmt-check:
	cargo fmt --all --check

build-server:
	cargo build --release --locked -p dexzd-server --bin dex-server-zd

.PHONY: zdIntegTests
zdIntegTests:
	$(MAKE) -C integ zdIntegTests $(if $(dexServerZDBinary),dexServerZDBinary=$(abspath $(dexServerZDBinary))) $(if $(totalPartitions),totalPartitions=$(totalPartitions)) $(if $(partitionNum),partitionNum=$(partitionNum)) $(if $(zdIntegTestJSON),zdIntegTestJSON=$(abspath $(zdIntegTestJSON)))
