# Customer refund (Python)

Northstar Studio asks for a refund of its 1,249.00 USD subscription renewal
after a 31-hour service interruption. `CustomerRefundFlow` checks the order and
the refund policy, asks a manager because the amount is over 1,000 USD, issues
the refund, has a model write the customer message, emails it, and closes the
case.

Billing, email, and the model are fakes in this folder. They need no API key
and move no money. Billing and email record each idempotency key in one SQLite
ledger, `.customer-refund/ledger.sqlite3`. A UNIQUE key and an atomic insert
keep one refund and one email per key, even for concurrent calls. "Once" holds
because every Worker shares this one persistent ledger; Workers with separate
ledgers would each refund. The key is built from the Flow ID, so give each Dex
environment its own ledger directory, or keep Flow IDs unique across
environments. Billing refuses a key that already refunded a different order
with `IdempotencyKeyConflict`.

| File | What it holds |
| --- | --- |
| [`refund_flow.py`](refund_flow.py) | The Flow, its eight Steps, and the Approve and Reject RPCs. |
| [`billing_service.py`](billing_service.py) | The fake billing service. Run it to list the refunds it recorded. |
| [`email_service.py`](email_service.py) | The fake email service. |
| [`ledger_database.py`](ledger_database.py) | The SQLite ledger that billing and email share. |
| [`message_model.py`](message_model.py) | A deterministic stand-in for a language model. |
| [`refund_settings.py`](refund_settings.py) | Environment settings, payload encryption, and the billing mode. |
| [`worker.py`](worker.py) | The Worker. It uploads the generated design. |
| [`start.py`](start.py) | Starts one refund with a Flow ID and a scenario. |
| [`decide.py`](decide.py) | Approves or rejects a waiting refund. |
| [`controller.py`](controller.py) | The HTTP routes on the shared example server. |

The integration tests are
[`tests/integ/test_customer_refund.py`](../../../tests/integ/test_customer_refund.py)
for the standalone Worker and
[`tests/integ/test_customer_refund_controller.py`](../../../tests/integ/test_customer_refund_controller.py)
for the shared example server.

## Run it

Run every command from `examples/python`. Start Dex in one terminal:

```bash
dexcli dev
```

In scripts, run `dexcli dev -open=false` so it does not open a browser.

Generate the design, then start the Worker in a second terminal:

```bash
uv sync --locked
dexcli visualize dex_examples/products/customer_refund/refund_flow.py --schema-version 2.0 --json --out build/customer-refund/customer-refund
uv run --frozen python -m dex_examples.products.customer_refund.worker
```

The Worker uploads every design in `build/customer-refund/`, so the run's
**Design** view in Dex Web shows the graph. Without the folder the Worker
still starts and prints a hint.

Start a refund in a third terminal. The Flow ID is your choice:

```bash
uv run --frozen python -m dex_examples.products.customer_refund.start refund1
```

The run stops at **Request manager approval** once `case-status` is
`awaiting-manager`. In Dex Web, open `refund1`, choose the **Actions** tab,
set **Working as** to `refund.manage`, enter a reason, and press **Approve
refund** or **Reject refund**. Or run:

```bash
uv run --frozen python -m dex_examples.products.customer_refund.decide refund1 approve "Outage confirmed"
```

`decide` first waits, up to 60 seconds, until `case-status` is
`awaiting-manager`, so you can run it right after `start`. Both RPCs refuse a
blank reason. The Action's `refund.manage` permission applies to Dex Web and
Work Queue only. A direct RPC call, such as `decide`, passes only the
server's public API authentication, which identifies the caller. It does not
decide who may approve a refund; a production application must authorize the
approver separately.

List the refunds that billing recorded:

```bash
uv run --frozen python -m dex_examples.products.customer_refund.billing_service
```

The Worker ID has the form `refund-worker-<random>`, so **Serving Worker ID**
in Dex Web shows no host name. Set `DEX_FLOW_SERVICE_ADDRESS` when Dex is not
at `127.0.0.1:8801`.

## Run it on the shared example server

The shared example server (`uv run --frozen python main.py`, HTTP
`127.0.0.1:8080`) also registers `CustomerRefundFlow`, always in the normal
billing mode, with the same ledger. The sync server in
[`sync-python/`](../../../sync-python/) serves the same routes on `8081`:

| Route | Query | What it does |
| --- | --- | --- |
| `GET /products/customer-refund/manager-approval/start` | `workflowId` (optional), `scenario` (default `normal`) | Starts a refund and returns its Flow ID and run ID once the policy check is done. Without `workflowId` it mints a unique `customer-refund-...` ID. Returns 504 if the policy check does not finish within 10 seconds. |
| `GET /products/customer-refund/manager-approval/decide` | `workflowId`, `decision` (`approve` or `reject`), `reason` | Calls Approve refund or Reject refund. A missing Flow ID or blank reason returns 400. A closed refund, an unknown Flow ID, or an ID for a different Flow type returns 404. A running refund that is not waiting for a manager, or a decision that another call is making, returns 409. |
| `GET /products/customer-refund/manager-approval/describe` | `workflowId` | Returns `flowID`, the run `status`, and the `case-status` and `refund-id` Attributes as `caseStatus` and `refundId`. `refundId` is `null` until a refund is issued. A missing Flow ID returns 400. An unknown Flow ID or an ID for a different Flow type returns 404. |

Every route also accepts `flowId` in place of `workflowId`.

```bash
curl 'http://127.0.0.1:8080/products/customer-refund/manager-approval/start?workflowId=refund5'
curl 'http://127.0.0.1:8080/products/customer-refund/manager-approval/decide?workflowId=refund5&decision=approve&reason=Outage%20confirmed'
curl 'http://127.0.0.1:8080/products/customer-refund/manager-approval/describe?workflowId=refund5'
```

Do not run the shared server and the standalone refund Worker against the same
Dex at once. Both serve `CustomerRefundFlow`, so either one can take a run's
tasks, with its own billing mode and payload key.

## Scenarios

`start.py FLOW_ID SCENARIO` picks the run's input:

| Scenario | Order | What happens |
| --- | --- | --- |
| `normal` (default) | INV-1249, 1,249.00 USD | Waits for a manager, then refunds. |
| `fail-then-succeed` | INV-1249 | After approval, the refund fails twice, then succeeds. |
| `deny` | INV-0388, charged 47 days ago | The policy denies it as outside the 30-day window; no approval, no refund. |
| `small` | INV-0049, 49.00 USD | Refunds without a manager. |

### A Step that fails, then succeeds

```bash
uv run --frozen python -m dex_examples.products.customer_refund.start refund2 fail-then-succeed
uv run --frozen python -m dex_examples.products.customer_refund.decide refund2 approve "Outage confirmed"
```

`decide` waits for `case-status` to be `awaiting-manager` before it approves.
**Issue refund** then raises `PaymentTimeout` from `billing_service.py` on its
first two attempts, and its retry policy waits 5 seconds between attempts. In
the **History** view, the final **Issue refund** event shows that attempt 3
succeeded and keeps the details of the last failure, with its stack trace.

### A missing billing call, fixed with Time Travel

Stop every running refund Worker first with Ctrl+C. Otherwise a normal Worker
can take the run's tasks. Then start the Worker in the `call-missing` billing
mode. It stands in for a release whose **Issue refund** Step forgot to call
billing:

```bash
REFUND_BILLING_MODE=call-missing uv run --frozen python -m dex_examples.products.customer_refund.worker
```

Start a refund and approve it; `decide` waits for `case-status` to be
`awaiting-manager` first:

```bash
uv run --frozen python -m dex_examples.products.customer_refund.start refund3
uv run --frozen python -m dex_examples.products.customer_refund.decide refund3 approve "Outage confirmed"
```

The run completes and the customer is told the refund was issued. But the
`refund-id` Attribute is empty, and the billing list shows no refund with the
key `refund-refund3`.

Stop the Worker and start it again in the normal mode:

```bash
uv run --frozen python -m dex_examples.products.customer_refund.worker
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
Travel point run again. A Time Travel to a successful **Issue refund** gets
the same refund ID back from billing and records no second refund.

## Payload encryption

Create a key file outside the repository:

```bash
dexcli codec-server keygen -o /tmp/refund-key.json
```

Give the same key file to the Worker and to each command:

```bash
export REFUND_PAYLOAD_KEY_FILE=/tmp/refund-key.json
uv run --frozen python -m dex_examples.products.customer_refund.worker
uv run --frozen python -m dex_examples.products.customer_refund.start refund4 small
```

`payload_encryption()` in `refund_settings.py` turns the file into
`PayloadEncryptionOptions` for the Worker and the Client. `case-status` and
`customer-email` are declared `plaintext=True`, so Dex Web reads them without
the key; the Action condition needs `case-status` in plaintext. In the
payloads Dex stores, Step inputs, `refund-id`, and `customer-message` are
encrypted. Encryption covers only those payloads. The local fake ledger keeps
idempotency keys, order IDs, and amounts, and no email text. The email address
is synthetic sample data.

To show the encrypted values in Dex Web, the simplest way is to start Dex with
the key file. Dex Web then finds its codec server by itself (**Settings** >
**Codec endpoint** > **Auto**):

```bash
dexcli dev -codec-key-file /tmp/refund-key.json
```

With a `dexcli dev` that is already running, start a local codec server
instead. It prints its bearer token at start, so send its output to
`/dev/null` and read the token from the mode-0600 file:

```bash
dexcli codec-server -key-file /tmp/refund-key.json -port 8805 -token-file /tmp/refund-codec-token > /dev/null
```

In Dex Web, open **Settings** > **Codec endpoint**, choose **Custom**, and
enter `http://127.0.0.1:8805` and the token from `/tmp/refund-codec-token`.
When Dex Web is not on port 8802, also pass its origin, such as
`-web-origin http://127.0.0.1:8812`. `dexcli flow state refund4
--codec-endpoint http://127.0.0.1:8805 --codec-token-file
/tmp/refund-codec-token` reads the same values.

## Test it

```bash
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 uv run --frozen pytest -vv tests/integ/test_customer_refund.py
```

The tests cover approval, rejection, a refused blank reason, each policy
denial, the fail-then-succeed refund and its attempt count in history, Time
Travel to a successful and to a missing billing call, and encryption. They
need `dexcli` on `PATH` or in `DEXCLI_PATH`.
`tests/unit/test_customer_refund_ledger.py` checks the ledger under concurrent
calls with the same key and with different keys.
