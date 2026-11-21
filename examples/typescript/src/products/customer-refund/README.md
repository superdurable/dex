# Customer refund (TypeScript)

Northstar Studio asks for a refund of its 1,249.00 USD subscription renewal
after a 31-hour service interruption. `CustomerRefundFlow` checks the order and
the refund policy, asks a manager because the amount is over 1,000 USD, issues
the refund, has a model write the customer message, emails it, and closes the
case.

Billing, email, and the model are fakes in this folder. They need no API key
and move no money. Billing and email remember each idempotency key in
`.customer-refund/`, so a restarted Worker sees what earlier Workers did.

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

The integration test is
[`test/integ/customer-refund.test.ts`](../../../test/integ/customer-refund.test.ts).
This example runs its own Worker. The HTTP sample in `src/main.ts` does not
register it.

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

The run stops at **Request manager approval**. In Dex Web, open `refund1`,
choose the **Actions** tab, set **Working as** to `refund.manage`, enter a
reason, and press **Approve refund** or **Reject refund**. Or run:

```bash
node dist/src/products/customer-refund/decide.js refund1 approve "Outage confirmed"
```

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
| `deny` | INV-0388, charged 47 days ago | The policy denies it; no approval, no refund. |
| `small` | INV-0049, 49.00 USD | Refunds without a manager. |

### A Step that fails, then succeeds

```bash
node dist/src/products/customer-refund/start.js refund2 fail-then-succeed
node dist/src/products/customer-refund/decide.js refund2 approve "Outage confirmed"
```

**Issue refund** throws `PaymentTimeout` from `billing-service.ts` on its
first two attempts. Its retry policy waits 5 seconds between attempts, so the
**History** view shows each failed attempt and its stack trace before the
third attempt succeeds.

### A missing billing call, fixed with Time Travel

Stop the Worker with Ctrl+C and start it in the `call-missing` billing mode.
It stands in for a release whose **Issue refund** Step forgot to call billing:

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

Stop the Worker and start it again in the normal mode:

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
Travel point run again.

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
without the key; the Action condition needs `case-status` in plaintext. Step
inputs, `refund-id`, and `customer-message` are stored encrypted. The email
address is synthetic sample data.

To show the encrypted values in Dex Web, start a local codec server:

```bash
dexcli codec-server -key-file /tmp/refund-key.json -port 8805 -token-file /tmp/refund-codec-token
```

In Dex Web, open **Settings** > **Codec endpoint**, choose **Custom**, and
enter `http://127.0.0.1:8805` and the token from `/tmp/refund-codec-token`.
`dexcli flow state refund4 --codec-endpoint http://127.0.0.1:8805
--codec-token-file /tmp/refund-codec-token` reads the same values.

## Test it

```bash
npm run build
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 node --test --experimental-test-isolation=none --test-force-exit dist/test/integ/customer-refund.test.js
```

The tests cover approval, rejection, the policy denial, the fail-then-succeed
refund, the missing billing call fixed with `dexcli flow time-travel`, and
encryption. They need `dexcli` on `PATH` or in `DEXCLI_PATH`.
`npm run test:integ` and `./run-integration-tests.sh` run them with the other
examples.
