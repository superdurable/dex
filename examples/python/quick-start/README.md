# Quick Start (Python)

The code for the [Dex Quick Start](https://docs.superdurable.io/quick-start).
`OrderFlow` charges an order, waits 30 seconds on a Timer, and then ships it.
A mock payment and shipping API prints each call, so the Worker's output shows
which Steps ran.

| File | What it holds |
| --- | --- |
| [`quick_start/order_flow.py`](quick_start/order_flow.py) | The Flow and its two Steps, `Charge` and `Ship`. |
| [`quick_start/shop_api.py`](quick_start/shop_api.py) | The mock payment and shipping API. |
| [`worker.py`](worker.py) | The Worker that runs the Steps. |
| [`start.py`](start.py) | Starts one Flow with a Client. |
| [`tests/test_quick_start.py`](tests/test_quick_start.py) | Stops the Worker after `Charge`, starts a new one, and checks that only `Ship` runs. It skips the Timer. |

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

The Worker prints `Charged order-...`. Within 30 seconds, stop the Worker
with Ctrl+C and start it again. When the Timer fires, the new Worker prints
`Shipped order-...`, and `Charged` does not appear again. Dex Web, at the
address `dexcli dev` printed, shows the run.

Set `DEX_FLOW_SERVICE_ADDRESS` when Dex is not at `127.0.0.1:8801`.

## Test it

```bash
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 uv run --frozen pytest
```
