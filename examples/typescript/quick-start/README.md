# Quick Start (TypeScript)

The code for the [Dex Quick Start](https://docs.superdurable.io/quick-start).
`OrderFlow` charges an order, waits 30 seconds on a Timer, and then ships it.
A mock payment and shipping API prints each call, so the Worker's output shows
which Steps ran.

| File | What it holds |
| --- | --- |
| [`order-flow.ts`](order-flow.ts) | The Flow and its two Steps, `Charge` and `Ship`. |
| [`shop-api.ts`](shop-api.ts) | The mock payment and shipping API. |
| [`worker.ts`](worker.ts) | The Worker that runs the Steps. |
| [`start.ts`](start.ts) | Starts one Flow with a Client. |
| [`test/quick-start.test.ts`](test/quick-start.test.ts) | Stops the Worker after `Charge`, starts a new one, and checks that only `Ship` runs. It skips the Timer. |

The files run directly with Node.js type stripping, so there is no build step.
Use Node.js 22.18 or later, or Node.js 24.

## Run it

Start Dex in one terminal:

```bash
dexcli dev
```

Start the Worker in a second terminal:

```bash
npm ci
npm run worker
```

Start a Flow in a third terminal:

```bash
npm run start-flow
```

The Worker prints `Charged order-...`. Within 30 seconds, stop the Worker
with Ctrl+C and start it again. When the Timer fires, the new Worker prints
`Shipped order-...`, and `Charged` does not appear again. Dex Web, at the
address `dexcli dev` printed, shows the run.

Set `DEX_FLOW_SERVICE_ADDRESS` when Dex is not at `127.0.0.1:8801`.

## Test it

```bash
npm run typecheck
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 npm test
```
