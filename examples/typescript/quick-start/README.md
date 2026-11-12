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

## Stop the Worker inside a Step

The mock API remembers each idempotency key in `.shop-api-keys`, so it can
report a repeated call. Start the Worker with a pause after the payment call:

```bash
QUICK_START_CHARGE_DELAY_SECONDS=20 npm run worker
```

Start a Flow. When the Worker prints `Charged order-...`, stop it at once with
`kill -9` and the process number that the Worker printed when it was ready.
Ctrl+C would let the Step finish first. Start the Worker again:

```bash
npm run worker
```

Dex runs Charge again, because it never recorded the first attempt. The mock
API prints `Charge for order-... already done (key charge-order-...); not
charged again`.

## Run it as a service

[`service.ts`](service.ts) and [`order-service.ts`](order-service.ts) serve `POST /orders` and run the Worker in the same process:

```bash
npm run service
curl -X POST localhost:8080/orders -d '{"orderId": "order-1"}'
```

The handler starts the Flow with the order ID as the Flow ID and the request ID
`start-<order ID>`, and asks Dex to return the existing run when that request
was already accepted. Sending the same request again starts nothing new. Set
`QUICK_START_HTTP_PORT` to use another port.

## Test it

```bash
npm run typecheck
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 npm test
```
