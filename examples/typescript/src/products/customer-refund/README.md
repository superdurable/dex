# Customer refund (TypeScript)

Northstar Studio asks for a refund of its 1,249.00 USD subscription renewal
after a 31-hour service interruption. `CustomerRefundFlow` checks the order and
the refund policy, asks a manager because the amount is over 1,000 USD, issues
the refund, has a model write the customer message, emails it, and closes the
case.

Billing, email, and the model are fakes in this folder. They need no API key
and move no money. Billing and email keep their records in SQLite files in
`.customer-refund/`, through the built-in `node:sqlite` module. Each call
checks its idempotency key and records it in one transaction. So a restarted
Worker, or two Workers at once, never issue a second refund or a second email
for the same key. Billing numbers refunds from a sequence that never reuses a
number, and it keeps every idempotency key even if a refund row is deleted.
A key that billing already used for another order or amount fails with
`RefundKeyConflict` instead of reporting the other refund. The email ledger
stores only idempotency keys, never an address or message text. Node.js 22
and 24 print an `ExperimentalWarning` the first time `node:sqlite` loads.

The idempotency keys are built from the Flow ID, such as `refund-refund1`.
Give each Dex environment its own `.customer-refund/` folder, by running its
Worker from its own directory, or keep Flow IDs unique across environments.
Otherwise a second environment that reuses a Flow ID meets the first
environment's refund.

| File | What it holds |
| --- | --- |
| [`refund-flow.ts`](refund-flow.ts) | The Flow, its eight Steps, and the Approve and Reject RPCs. |
| [`billing-service.ts`](billing-service.ts) | The fake billing service. |
| [`list-refunds.ts`](list-refunds.ts) | Lists the refunds that billing recorded. |
| [`email-service.ts`](email-service.ts) | The fake email service. |
| [`message-model.ts`](message-model.ts) | A deterministic stand-in for a language model. |
| [`refund-settings.ts`](refund-settings.ts) | Environment settings, scenarios, payload encryption, and the billing mode. |
| [`worker.ts`](worker.ts) | The Worker. It uploads the generated design. |
| [`start.ts`](start.ts) | Starts one refund with a Flow ID and a scenario. |
| [`decide.ts`](decide.ts) | Approves or rejects a waiting refund. |
| [`controller.ts`](controller.ts) | The HTTP routes of the shared example server. |

The integration test is
[`test/integ/customer-refund.test.ts`](../../../test/integ/customer-refund.test.ts).
The steps below run this example's own Worker and commands.

## Run it with the shared example server

The shared example server (`npm start`, from `src/main.ts`) also registers
this Flow, with the normal billing mode only. It serves these routes:

| Route | What it does |
| --- | --- |
| `GET /products/customer-refund/manager-approval/start?workflowId=<id>&scenario=<scenario>` | Starts a refund and returns `flowID` and `runID`. `workflowId` is optional; without it the server picks a `customer-refund-` Flow ID. `scenario` is `normal` (the default), `fail-then-succeed`, `deny`, or `small`. |
| `GET /products/customer-refund/manager-approval/decide?workflowId=<id>&decision=<approve or reject>&reason=<text>` | Approves or rejects a refund that waits for a manager. |
| `GET /products/customer-refund/manager-approval/describe?workflowId=<id>` | Returns the run `status`, and `caseStatus` and `refundId` from the `case-status` and `refund-id` Attributes. |

Each route also accepts the Flow ID as `flowId`, like the order-processing routes.

`decide` answers 400 for a blank reason, 409 when the refund is not waiting
for a manager or another decision holds its lock, and 404 when the run is
closed. The server keeps its ledgers in `.customer-refund/`; set
`REFUND_LEDGER_DIR` to use another folder.

Stop the shared server before you run this example's own Worker against the
same Dex. Both serve `CustomerRefundFlow`, so either one could run a Step,
and the `call-missing` story would not show the missing refund.

## Run it

Run every command from `examples/typescript`. Start Dex in one terminal:

```bash
dexcli dev
```

In scripts, run `dexcli dev -open=false` so it does not open a browser.

Generate the design, build, then start the Worker in a second terminal:

```bash
npm ci
dexcli visualize src/products/customer-refund/refund-flow.ts --schema-version 2.0 --json --out build/customer-refund/customer-refund
npm run build
node dist/src/products/customer-refund/worker.js
```

`dexcli visualize` loads the `typescript` package that this project installs.
To analyze source you do not trust, pass a separately installed compiler with
`--typescript-compiler`; see the
[Flow visualization reference](https://docs.superdurable.io/reference/web/flow-visualization).

The Worker uploads every design in `build/customer-refund/`, so the run's
**Design** view in Dex Web shows the graph. Without the folder the Worker
still starts and prints a hint.

Start a refund in a third terminal. The Flow ID is your choice:

```bash
node dist/src/products/customer-refund/start.js refund1
```

The run stops at **Request manager approval** and sets `case-status` to
`awaiting-manager`. In Dex Web, open `refund1`, choose the **Actions** tab,
set **Working as** to `refund.manage`, enter a reason, and press **Approve
refund** or **Reject refund**. The Actions appear once `case-status` is
`awaiting-manager`. Or run:

```bash
node dist/src/products/customer-refund/decide.js refund1 approve "Outage confirmed"
```

`decide.js` first waits up to 30 seconds for `case-status=awaiting-manager`,
so you can run it right after `start.js`. Both RPCs refuse a blank reason.
When two managers decide at once, both RPCs lock `case-status`, so only one
decision counts. The other gets `RpcLockConflictError`, or sees that the
refund no longer waits for a manager, and `decide.js` says so and exits with
status 1.

**Working as** and the Action's required permission only decide which
Actions Dex Web shows. Dex Server authentication identifies the caller; it
does not check that the caller may approve refunds. A production application
authorizes approvers itself before it lets a request reach these RPCs.

List the refunds that billing recorded:

```bash
node dist/src/products/customer-refund/list-refunds.js
```

The Worker ID has the form `refund-worker-<random>`, so **Serving Worker ID**
in Dex Web shows no host name. Set `DEX_FLOW_SERVICE_ADDRESS` when Dex is not
at `127.0.0.1:8801`.

## Scenarios

`start.js FLOW_ID SCENARIO` picks the run's input:

| Scenario | Order | What happens |
| --- | --- | --- |
| `normal` (default) | INV-1249, 1,249.00 USD | Waits for a manager, then refunds. |
| `fail-then-succeed` | INV-1249 | After approval, the refund fails twice, then succeeds. |
| `deny` | INV-0388, charged 47 days ago, no outage recorded | The policy denies it; no approval, no refund. The message names both reasons. |
| `small` | INV-0049, 49.00 USD | Refunds without a manager. |

### A Step that fails, then succeeds

```bash
node dist/src/products/customer-refund/start.js refund2 fail-then-succeed
node dist/src/products/customer-refund/decide.js refund2 approve "Outage confirmed"
```

**Issue refund** throws `PaymentTimeout` from `billing-service.ts` on its
first two attempts. Its retry policy waits 5 seconds between attempts, and
the third attempt succeeds. In the **History** view, the final **Issue
refund** event shows that attempt 3 succeeded and keeps the details of the
last failure, including its stack trace.

### A missing billing call, fixed with Time Travel

Stop the normal Worker first: press Ctrl+C in its terminal. A normal Worker
that is still running could take the next refund and call billing. Then
start the Worker in the `call-missing` billing mode. It stands in for a
release whose **Issue refund** Step forgot to call billing:

```bash
REFUND_BILLING_MODE=call-missing node dist/src/products/customer-refund/worker.js
```

Start and approve a refund:

```bash
node dist/src/products/customer-refund/start.js refund3
node dist/src/products/customer-refund/decide.js refund3 approve "Outage confirmed"
```

The run completes and the customer is told the refund was issued. But the
`refund-id` Attribute is empty, and the billing list shows no refund with the
key `refund-refund3`.

Stop the `call-missing` Worker with Ctrl+C and start it again in the normal
mode:

```bash
node dist/src/products/customer-refund/worker.js
```

Take the run back to **Issue refund**. In Dex Web, select the **Issue refund**
event and press **Time travel from here**, or run:

```bash
dexcli flow time-travel refund3 --type step-type --target IssueRefund --reason "billing call restored" --yes
```

Dex runs **Issue refund** and the Steps after it again. This time billing
records one refund, and `refund-id` holds its ID, such as `rf_1044`. The email
service sees the key `email-refund3` again and does not send a second email.
Each external call carries an idempotency key because Steps after a Time
Travel point run again. Time Travel to **Issue refund** of a run that was
already refunded, such as `refund1`, gets the same refund ID back from
billing and creates no second refund.

## Payload encryption

Create a key file outside the repository:

```bash
dexcli codec-server keygen -o /tmp/refund-key.json
```

Give the same key file to the Worker and to each command:

```bash
export REFUND_PAYLOAD_KEY_FILE=/tmp/refund-key.json
node dist/src/products/customer-refund/worker.js
node dist/src/products/customer-refund/start.js refund4 small
```

`newPayloadCodec()` in `refund-settings.ts` turns the file into an
`AesGcmPayloadCodec` for the Worker and the Client. `case-status` and
`customer-email` are declared with `.plaintext()`, so Dex Web reads them
without the key; the Action condition needs `case-status` in plaintext. Of
the payloads Dex stores, Step inputs, `refund-id`, and `customer-message` are
encrypted. Payload encryption covers only what Dex stores; the fake services
keep their own files, as real ones would. The email address is synthetic
sample data.

To show the encrypted values in Dex Web, the simplest way is to start Dex
with the key: `dexcli dev -codec-key-file /tmp/refund-key.json`. Or start a
local codec server:

```bash
dexcli codec-server -key-file /tmp/refund-key.json -port 8805 -token-file /tmp/refund-codec-token > /dev/null
```

The codec server prints its bearer token at each start, so the command sends
standard output to `/dev/null` and reads the token from the `0600` token file
instead. It allows Dex Web at the default port 8802. If Dex Web runs on
another port, add `-web-origin http://127.0.0.1:<port>` for it. In Dex Web,
open **Settings** > **Codec endpoint**, choose **Custom**, and enter
`http://127.0.0.1:8805` and the token from `/tmp/refund-codec-token`.
`dexcli flow state refund4 --codec-endpoint http://127.0.0.1:8805
--codec-token-file /tmp/refund-codec-token` reads the same values.

## Test it

```bash
npm run build
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 node --test --experimental-test-isolation=none --test-force-exit dist/test/integ/customer-refund.test.js
```

The tests cover approval, rejection, blank reasons, two decisions at once,
each policy denial, the fail-then-succeed refund and its History, the missing
billing call fixed with `dexcli flow time-travel`, Time Travel after a
successful refund, the design and its Actions, and encryption. They need
`dexcli` on `PATH` or in `DEXCLI_PATH`. `npm run test:integ` and
`./run-integration-tests.sh` run them with the other examples. `npm run smoke`
checks the shared server's routes in
[`test/e2e/flow-smoke.test.ts`](../../../test/e2e/flow-smoke.test.ts).
`npm test`
runs
[`test/workflow/customer-refund-ledgers.test.ts`](../../../test/workflow/customer-refund-ledgers.test.ts),
which needs no Dex. It holds each ledger's lock while several processes
start, then releases it, and checks deleted refund rows and a key reused for
another order.
