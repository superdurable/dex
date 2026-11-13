.PHONY: check test test-without-slow-crates test-simulation test-server clippy fmt-check build-server

# Build only one crate while iterating: cargo test -p <crate>

check:
	cargo check --all-targets --locked

test:
	cargo test --all-targets --locked

# CI splits make test into these three parallel suites; together they run every test once.
SLOW_TEST_CRATES := dexzd-simulation dexzd-server

test-without-slow-crates:
	cargo test --workspace --all-targets --locked $(addprefix --exclude ,$(SLOW_TEST_CRATES))

test-simulation:
	cargo test --all-targets --locked -p dexzd-simulation

test-server:
	cargo test --all-targets --locked -p dexzd-server

clippy:
	cargo clippy --all-targets --locked -- -D warnings

fmt-check:
	cargo fmt --all --check

build-server:
	cargo build --release --locked -p dexzd-server --bin dex-server-zd

.PHONY: zdIntegTests
zdIntegTests:
	$(MAKE) -C integ zdIntegTests $(if $(dexServerZDBinary),dexServerZDBinary=$(abspath $(dexServerZDBinary))) $(if $(totalPartitions),totalPartitions=$(totalPartitions)) $(if $(partitionNum),partitionNum=$(partitionNum)) $(if $(zdIntegTestJSON),zdIntegTestJSON=$(abspath $(zdIntegTestJSON)))

.PHONY: db9ObjectStoreTests
db9ObjectStoreTests:
	cargo test --locked -p dexzd-object-store --features db9-integration --test conformance --test db9
