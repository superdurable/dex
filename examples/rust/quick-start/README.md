# Quick Start (Rust)

## Purpose

The Dex Quick Start in Rust: an order Flow that charges, waits on a 60-second Timer, and ships.

The code for the [Dex Quick Start](https://docs.superdurable.io/quick-start).
`OrderFlow` charges an order, waits 60 seconds on a Timer, and then ships it.
A mock payment and shipping API prints each call, so the Worker's output shows
which Steps ran.

| File | What it holds |
| --- | --- |
| [`src/order_flow.rs`](src/order_flow.rs) | The Flow and its two Steps, `Charge` and `Ship`. |
| [`src/shop_api.rs`](src/shop_api.rs) | The mock payment and shipping API. |
| [`src/bin/worker.rs`](src/bin/worker.rs) | The Worker that runs the Steps. |
| [`src/bin/start.rs`](src/bin/start.rs) | Starts one Flow with a Client. |
| [`tests/quick_start.rs`](tests/quick_start.rs) | Stops the Worker after `Charge`, starts a new one, and checks that only `Ship` runs. It skips the Timer. |

## Get the code

Download only this folder, without cloning the repository:

```bash
curl -sL https://codeload.github.com/superdurable/dex/tar.gz/main | tar -xz --strip-components=3 dex-main/examples/rust/quick-start
cd quick-start
```

The folder builds on its own. Its only Dex dependency is `dex-sdk` 1.45.0 from crates.io.

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

The Worker prints `Charged order-...`. Within 60 seconds, stop the Worker
with Ctrl+C and start it again. When the Timer fires, the new Worker prints
`Shipped order-...`, and `Charged` does not appear again. Dex Web, at the
address `dexcli dev` printed, shows the run.

Set `DEX_FLOW_SERVICE_ADDRESS` when Dex is not at `127.0.0.1:8801`.

## Stop the Worker inside a Step

The mock API remembers each idempotency key in `.shop-api-keys`, so it can
report a repeated call. Start the Worker with a pause after the payment call:

```bash
QUICK_START_CHARGE_DELAY_SECONDS=20 cargo run --locked --bin worker
```

Start a Flow. When the Worker prints `Charged order-...`, stop it at once with
`kill -9` and the process number that the Worker printed when it was ready.
Ctrl+C would let the Step finish first. Start the Worker again:

```bash
cargo run --locked --bin worker
```

Dex runs Charge again, because it never recorded the first attempt. The mock
API prints `Charge for order-... already done (key charge-order-...); not
charged again`.

## Run it as a service

[`src/bin/service.rs`](src/bin/service.rs) and [`src/order_service.rs`](src/order_service.rs) serve `POST /orders` and run the Worker in the same process:

```bash
cargo run --locked --bin service
curl -X POST localhost:8080/orders -d '{"orderId": "order-1"}'
```

The handler starts the Flow with the order ID as the Flow ID and the request ID
`start-<order ID>`, and asks Dex to return the existing run when that request
was already accepted. Sending the same request again starts nothing new. Set
`QUICK_START_HTTP_PORT` to use another port.

## Test it

```bash
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 cargo test --locked
```
