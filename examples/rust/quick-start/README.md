# Quick Start (Rust)

## Purpose

The Dex Quick Start in Rust: an order Flow that charges, waits on a 30-second Timer, and ships.

The code for the [Dex Quick Start](https://docs.superdurable.io/quick-start).
`OrderFlow` charges an order, waits 30 seconds on a Timer, and then ships it.
A mock payment and shipping API prints each call, so the Worker's output shows
which Steps ran.

| File | What it holds |
| --- | --- |
| [`src/order_flow.rs`](src/order_flow.rs) | The Flow and its two Steps, `Charge` and `Ship`. |
| [`src/shop_api.rs`](src/shop_api.rs) | The mock payment and shipping API. |
| [`src/bin/worker.rs`](src/bin/worker.rs) | The Worker that runs the Steps. |
| [`src/bin/start.rs`](src/bin/start.rs) | Starts one Flow with a Client. |
| [`tests/quick_start.rs`](tests/quick_start.rs) | Stops the Worker after `Charge`, starts a new one, and checks that only `Ship` runs. It skips the Timer. |

## Run it

Rust 1.97 or newer is required. Start Dex in one terminal:

```bash
dexcli dev
```

Start the Worker in a second terminal:

```bash
cargo run --locked --bin worker
```

Start a Flow in a third terminal:

```bash
cargo run --locked --bin start
```

The Worker prints `Charged order-...`. Within 30 seconds, stop the Worker
with Ctrl+C and start it again. When the Timer fires, the new Worker prints
`Shipped order-...`, and `Charged` does not appear again. Dex Web, at the
address `dexcli dev` printed, shows the run.

Set `DEX_FLOW_SERVICE_ADDRESS` when Dex is not at `127.0.0.1:8801`.

## Test it

```bash
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 cargo test --locked
```
