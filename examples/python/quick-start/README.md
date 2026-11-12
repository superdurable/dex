# Quick Start (Python)

The code for the [Dex Quick Start](https://docs.superdurable.io/quick-start).
`OrderFlow` charges an order, waits 60 seconds on a Timer, and then ships it.
A mock payment and shipping API prints each call, so the Worker's output shows
which Steps ran.

| File | What it holds |
| --- | --- |
| [`quick_start/order_flow.py`](quick_start/order_flow.py) | The Flow and its two Steps, `Charge` and `Ship`. |
| [`quick_start/shop_api.py`](quick_start/shop_api.py) | The mock payment and shipping API. |
| [`worker.py`](worker.py) | The Worker that runs the Steps. |
| [`start.py`](start.py) | Starts one Flow with a Client. |
| [`tests/test_quick_start.py`](tests/test_quick_start.py) | Stops the Worker after `Charge`, starts a new one, and checks that only `Ship` runs. It skips the Timer. |

## Get the code

Download only this folder, without cloning the repository:

```bash
curl -sL https://codeload.github.com/superdurable/dex/tar.gz/main | tar -xz --strip-components=3 dex-main/examples/python/quick-start
cd quick-start
```

The folder builds on its own. Its only Dex dependency is `dex-python-sdk` 1.45.0 from PyPI.

## Run it

Start Dex in one terminal:

```bash
dexcli dev
```

Start the Worker in a second terminal:

```bash
uv sync --locked
uv run --frozen python worker.py
```

Start a Flow in a third terminal:

```bash
uv run --frozen python start.py
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
QUICK_START_CHARGE_DELAY_SECONDS=20 uv run --frozen python worker.py
```

Start a Flow. When the Worker prints `Charged order-...`, stop it at once with
`kill -9` and the process number that the Worker printed when it was ready.
Ctrl+C would let the Step finish first. Start the Worker again:

```bash
uv run --frozen python worker.py
```

Dex runs Charge again, because it never recorded the first attempt. The mock
API prints `Charge for order-... already done (key charge-order-...); not
charged again`.

## Run it as a service

[`service.py`](service.py) serve `POST /orders` and run the Worker in the same process:

```bash
uv run --frozen python service.py
curl -X POST localhost:8080/orders -d '{"orderId": "order-1"}'
```

The handler starts the Flow with the order ID as the Flow ID and the request ID
`start-<order ID>`, and asks Dex to return the existing run when that request
was already accepted. Sending the same request again starts nothing new. Set
`QUICK_START_HTTP_PORT` to use another port.

## Test it

```bash
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 uv run --frozen pytest
```
