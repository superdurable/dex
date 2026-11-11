# Quick Start (Go)

The code for the [Dex Quick Start](https://docs.superdurable.io/quick-start).
`OrderFlow` charges an order, waits 30 seconds on a Timer, and then ships it.
A mock payment and shipping API prints each call, so the Worker's output shows
which Steps ran.

| File | What it holds |
| --- | --- |
| [`order_flow.go`](order_flow.go) | The Flow and its two Steps, `Charge` and `Ship`. |
| [`shop_api.go`](shop_api.go) | The mock payment and shipping API. |
| [`cmd/worker/main.go`](cmd/worker/main.go) | The Worker that runs the Steps. |
| [`cmd/start/main.go`](cmd/start/main.go) | Starts one Flow with a Client. |
| [`quickstart_test.go`](quickstart_test.go) | Stops the Worker after `Charge`, starts a new one, and checks that only `Ship` runs. It skips the Timer. |

## Run it

Start Dex in one terminal:

```bash
dexcli dev
```

Start the Worker in a second terminal:

```bash
go run ./cmd/worker
```

Start a Flow in a third terminal:

```bash
go run ./cmd/start
```

The Worker prints `Charged order-...`. Within 30 seconds, stop the Worker
with Ctrl+C and start it again. When the Timer fires, the new Worker prints
`Shipped order-...`, and `Charged` does not appear again. Dex Web, at the
address `dexcli dev` printed, shows the run.

Set `DEX_FLOW_SERVICE_ADDRESS` when Dex is not at `127.0.0.1:8801`.

## Test it

```bash
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 go test ./...
```
