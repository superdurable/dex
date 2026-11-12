# Quick Start (Java)

The code for the [Dex Quick Start](https://docs.superdurable.io/quick-start).
`OrderFlow` charges an order, waits 30 seconds on a Timer, and then ships it.
A mock payment and shipping API prints each call, so the Worker's output shows
which Steps ran.

| File | What it holds |
| --- | --- |
| [`OrderFlow.java`](src/main/java/io/superdurable/dex/quickstart/OrderFlow.java) | The Flow and its two Steps, `Charge` and `Ship`. |
| [`ShopApi.java`](src/main/java/io/superdurable/dex/quickstart/ShopApi.java) | The mock payment and shipping API. |
| [`WorkerMain.java`](src/main/java/io/superdurable/dex/quickstart/WorkerMain.java) | The Worker that runs the Steps. |
| [`StartMain.java`](src/main/java/io/superdurable/dex/quickstart/StartMain.java) | Starts one Flow with a Client. |
| [`QuickStartTest.java`](src/test/java/io/superdurable/dex/quickstart/QuickStartTest.java) | Stops the Worker after `Charge`, starts a new one, and checks that only `Ship` runs. It skips the Timer. |

## Run it

You need JDK 17 or newer. Start Dex in one terminal:

```bash
dexcli dev
```

Build the two commands, then start the Worker in a second terminal:

```bash
./gradlew installDist
build/install/quick-start/bin/worker
```

Start a Flow in a third terminal:

```bash
build/install/quick-start/bin/start
```

The Worker prints `Charged order-...`. Within 30 seconds, stop the Worker
with Ctrl+C and start it again. When the Timer fires, the new Worker prints
`Shipped order-...`, and `Charged` does not appear again. Dex Web, at the
address `dexcli dev` printed, shows the run.

The commands run outside Gradle, so Ctrl+C stops the Worker itself. Set
`DEX_FLOW_SERVICE_ADDRESS` when Dex is not at `127.0.0.1:8801`.

## Stop the Worker inside a Step

The mock API remembers each idempotency key in `.shop-api-keys`, so it can
report a repeated call. Start the Worker with a pause after the payment call:

```bash
QUICK_START_CHARGE_DELAY_SECONDS=20 build/install/quick-start/bin/worker
```

Start a Flow. When the Worker prints `Charged order-...`, stop it at once with
`kill -9` and the process number that the Worker printed when it was ready.
Ctrl+C would let the Step finish first. Start the Worker again:

```bash
build/install/quick-start/bin/worker
```

Dex runs Charge again, because it never recorded the first attempt. The mock
API prints `Charge for order-... already done (key charge-order-...); not
charged again`.

## Run it as a service

[`ServiceMain.java`](src/main/java/io/superdurable/dex/quickstart/ServiceMain.java) and [`OrderService.java`](src/main/java/io/superdurable/dex/quickstart/OrderService.java) serve `POST /orders` and run the Worker in the same process:

```bash
build/install/quick-start/bin/service
curl -X POST localhost:8080/orders -d '{"orderId": "order-1"}'
```

The handler starts the Flow with the order ID as the Flow ID and the request ID
`start-<order ID>`, and asks Dex to return the existing run when that request
was already accepted. Sending the same request again starts nothing new. Set
`QUICK_START_HTTP_PORT` to use another port.

## Test it

```bash
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 ./gradlew test
```
