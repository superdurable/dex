# Customer refund example

This product contains three Go Flows for the same customer-refund problem.

- `deterministic/workflow.go` is a fixed seven-Step policy. It verifies the 30-day window, uses a persisted provider idempotency key, and preserves declined or unknown outcomes.
- `agentic/workflow.go` loops through durable evidence, records a recommendation, applies a guardrail, opens a keyed approval gate, and separates intent from idempotent effects. An OpenAI Connector Step drafts the customer response before a **second** keyed gate lets a person confirm or rewrite it.
- `managerapproval/workflow.go` is the readable refund with a manager approval. It
  has its own Worker and commands. See [Manager-approval refund](#manager-approval-refund).

The first two files are self-contained FDG 2.0 sources. Every Step declares `dex:group`
and a one-sentence `dex:explanation`. Generate their definitions from the
repository root:

```bash
dexcli visualize examples/go/products/customer-refund/deterministic/workflow.go --schema-version 2.0 --json --out customer-refund-deterministic
dexcli visualize examples/go/products/customer-refund/agentic/workflow.go --schema-version 2.0 --json --out customer-refund-agentic
```

Start the example server and create runs with:

```bash
curl -X POST http://127.0.0.1:8080/products/customer-refund/deterministic/start \
  -H 'content-type: application/json' \
  -d '{"case":{"caseId":"order-42","customer":"customer-1","customerEmail":"customer-1@example.com","customerNote":"Please refund this charge","amountCents":4200,"orderAgeDays":12}}'

curl -X POST http://127.0.0.1:8080/products/customer-refund/agentic/start \
  -H 'content-type: application/json' \
  -d '{"case":{"caseId":"rule-escalation","customer":"customer-2","customerEmail":"customer-2@example.com","customerNote":"Please review this charge","amountCents":1400000,"orderAgeDays":8}}'
```

## Two human gates

The agentic Flow stops for a person twice, and a run can only ever be at one of
them:

| status | Actions | permission | Channel |
|---|---|---|---|
| `awaiting-manager-rule` / `awaiting-manager-agent` | `ApproveRefund`, `RejectRefund` | `refund.manage` | `manager-approval` |
| `awaiting-message-approval` | `ConfirmCustomerMessage`, `EditCustomerMessage` | `refund.message` | `message-approval` |

`ApproveRefund` takes no input. The other three bind the hidden
`gate-request-key` snapshot supplied by Dex Web, and every one re-checks the
current status before publishing, so a tab left open across a decision is turned
away rather than answering a question that has moved on.

Each Action condition is declared in its Go `RPCOptions.Action`. When a
successful Worker invocation writes an Action condition source, the Worker
sends the complete Action-to-permission mapping with that response. The Server
overlays the writes on the authoritative Attribute state and atomically updates
`DexWorkQueuePermissions` only when the permission history gains a new value.
Once an Action condition matches, its permission remains searchable for that
Flow execution, including after the Action is handled. Unrelated writes and
query-only RPCs do not send the mapping. Dex Web does the same when
`SetAttributes` edits an Action source.

The projection does not require every Step that writes `case-status` to lock
that Attribute. The Action RPCs still lock the business state they check and
the gate effects they publish. Dex Web Work Queue can filter by one permission;
trusted application callers can send several permissions to `/api/v2/search`
and receive their union. The projection and **Working as** selector do not
authenticate a caller or grant a permission.

Both gates share one counter and one `gate-request-key`, which is why the keys
read `case:gate:1` and `case:gate:2`. They cannot consume each other's answer
because each waits on its own Channel.

`PrepareCustomerMessageStep` builds provider input and a deterministic fallback.
`GenerateCustomerMessageStep` is an operation-specific OpenAI Connector Step.
Its completed branch stores the generated message; failed, uncertain, and defect
branches use the fallback without repeating the provider mutation. The registry
wires a deterministic in-memory OpenAI transport so the example and integration
suite need no external credential. The gate Step only reads the key. Nothing
writes inside a `wait_for` phase — a Step that does so without declaring its
loads in `StepOptions` registers an empty wait condition and parks forever.

## Searching for a run

The agentic Flow declares three Indexed Attributes, which are the only fields
Dex Web can search on:

| Attribute | index | queries it allows |
|---|---|---|
| `customer-email` | `keyword1` keyword | exact email address |
| `refund-amount` | `double1` double | ranges, in dollars rather than cents |
| `case-status` | `keyword2` keyword | exact, or one of several |

The email is synthetic sample data only. Do not index email addresses or other
PII in production; index only values your search needs.

Raw searches for these generic slots must also filter
`FlowType = 'AgenticCustomerRefundFlow'`.

Read [the Go examples README](../../README.md#run-locally) before changing any of
them: the Worker reconciles Indexed Attributes against the store at startup.

## Manager-approval refund

Northstar Studio asks for a refund of its 1,249.00 USD subscription renewal
after a 31-hour service interruption. `ManagerApprovalRefundFlow` checks the
order and the refund policy, asks a manager because the amount is over 1,000
USD, issues the refund, has a model write the customer message, emails it, and
closes the case.

Billing, email, and the model are fakes in `managerapproval/`. They need no API
key and move no money. Billing and email record each idempotency key in one
ledger, `.customer-refund/ledger.json`. A file lock around each
read-modify-write keeps one refund and one email per key, even for concurrent
calls, and a refund ID is saved with its refund record. "Once" holds because
every Worker shares this one persistent ledger; Workers with separate ledgers
would each refund. The key is built from the Flow ID, so give each Dex
environment its own ledger directory, or keep Flow IDs unique across
environments. Billing refuses a key that already refunded a different order or
amount with `IdempotencyKeyConflictError`. The ledger needs Unix file locks, so
it runs on macOS and Linux.

| File | What it holds |
| --- | --- |
| [`workflow.go`](managerapproval/workflow.go) | The Flow, its eight Steps, and the Approve refund and Reject refund RPCs. |
| [`billing.go`](managerapproval/billing.go) | The fake billing service and the `call-missing` stand-in. |
| [`email.go`](managerapproval/email.go) | The fake email service. |
| [`ledger.go`](managerapproval/ledger.go) | The ledger that billing and email share. |
| [`message_model.go`](managerapproval/message_model.go) | A deterministic stand-in for a language model. |
| [`settings.go`](managerapproval/settings.go) | Scenarios, payload encryption, the billing mode, and the Worker ID. |
| [`controller.go`](managerapproval/controller.go) | The example server routes. |
| [`cmd/worker`](managerapproval/cmd/worker/main.go) | The Worker. It uploads the generated design. |
| [`cmd/start`](managerapproval/cmd/start/main.go) | Starts one refund with a Flow ID and a scenario. |
| [`cmd/decide`](managerapproval/cmd/decide/main.go) | Approves or rejects a waiting refund. |
| [`cmd/list-refunds`](managerapproval/cmd/list-refunds/main.go) | Lists the refunds that billing recorded. |

The integration test is
[`integ/customer_refund_manager_approval_test.go`](../../integ/customer_refund_manager_approval_test.go).

The Go SDK registers a Flow by its Go type name, and `CustomerRefundFlow` is
already the deterministic Flow, so this Flow type is
`ManagerApprovalRefundFlow`. Its Step types match the other languages, such as
`IssueRefund`. Dex Web v2 Definition needs `GetDexSummary` and `GetDexDisplay`
in a Go FDG 2.0 source, so this Flow defines both; they only read Attributes.

### On the example server

`./dex-samples` registers this Flow in the normal billing mode, with the ledger
in `.customer-refund/` of its working folder. Its routes take GET query
parameters:

```bash
curl 'http://127.0.0.1:8080/products/customer-refund/manager-approval/start?flowId=refund1&scenario=normal'
curl 'http://127.0.0.1:8080/products/customer-refund/manager-approval/describe?flowId=refund1'
curl 'http://127.0.0.1:8080/products/customer-refund/manager-approval/decide?flowId=refund1&decision=approve&reason=Outage+confirmed'
```

`start` mints a Flow ID with the prefix `customer-refund` when `flowId` is
absent, and `scenario` defaults to `normal`. `describe` returns `flowID`, the
run `status`, `caseStatus`, and `refundId`. `decide` returns 400 for a blank
reason and 409 when the run is not waiting for a manager or another decision
holds the lock.

### Run it with its own Worker

The standalone Worker shows the `call-missing` story and payload encryption.
Run every command from `examples/go`, and stop `./dex-samples` first: it also
serves this Flow and could take the run's tasks. Start Dex in one terminal:

```bash
dexcli dev
```

In scripts, run `dexcli dev -open=false` so it does not open a browser.

Generate the design, then start the Worker in a second terminal:

```bash
dexcli visualize products/customer-refund/managerapproval/workflow.go --schema-version 2.0 --json --out build/customer-refund/customer-refund
GOWORK=off go run ./products/customer-refund/managerapproval/cmd/worker
```

The Worker uploads every design in `build/customer-refund/`, so the run's
**Design** view in Dex Web shows the graph. Without the folder the Worker
still starts and prints a hint.

Start a refund in a third terminal. The Flow ID is your choice:

```bash
GOWORK=off go run ./products/customer-refund/managerapproval/cmd/start refund1
```

The run stops at **Request manager approval** once `case-status` is
`awaiting-manager`. In Dex Web, open `refund1`, choose the **Actions** tab,
set **Working as** to `refund.manage`, enter a reason, and press **Approve
refund** or **Reject refund**. Or run:

```bash
GOWORK=off go run ./products/customer-refund/managerapproval/cmd/decide refund1 approve "Outage confirmed"
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
GOWORK=off go run ./products/customer-refund/managerapproval/cmd/list-refunds
```

The Worker ID has the form `refund-worker-<random>`, so **Serving Worker ID**
in Dex Web shows no host name. Set `DEX_FLOW_SERVICE_ADDRESS` when Dex is not
at `127.0.0.1:8801`.

### Scenarios

`cmd/start FLOW_ID SCENARIO` and the `scenario` query parameter pick the run's
input:

| Scenario | Order | What happens |
| --- | --- | --- |
| `normal` (default) | INV-1249, 1,249.00 USD | Waits for a manager, then refunds. |
| `fail-then-succeed` | INV-1249 | After approval, the refund fails twice, then succeeds. |
| `deny` | INV-0388, charged 47 days ago | The policy denies it as outside the 30-day window; no approval, no refund. |
| `small` | INV-0049, 49.00 USD | Refunds without a manager. |

#### A Step that fails, then succeeds

```bash
GOWORK=off go run ./products/customer-refund/managerapproval/cmd/start refund2 fail-then-succeed
GOWORK=off go run ./products/customer-refund/managerapproval/cmd/decide refund2 approve "Outage confirmed"
```

**Issue refund** then returns `PaymentTimeoutError` from `billing.go` on its
first two attempts, and its retry policy waits 5 seconds between attempts. In
the **History** view, the final **Issue refund** event shows that attempt 3
succeeded and keeps the details of the last failure.

#### A missing billing call, fixed with Time Travel

Stop every running refund Worker and `./dex-samples` first. Otherwise a normal
Worker can take the run's tasks. Then start the Worker in the `call-missing`
billing mode. It stands in for a release whose **Issue refund** Step forgot to
call billing:

```bash
REFUND_BILLING_MODE=call-missing GOWORK=off go run ./products/customer-refund/managerapproval/cmd/worker
```

Start a refund and approve it:

```bash
GOWORK=off go run ./products/customer-refund/managerapproval/cmd/start refund3
GOWORK=off go run ./products/customer-refund/managerapproval/cmd/decide refund3 approve "Outage confirmed"
```

The run completes and the customer is told the refund was issued. But the
`refund-id` Attribute is empty, and `list-refunds` shows no refund with the
key `refund-refund3`.

Stop the Worker and start it again in the normal mode:

```bash
GOWORK=off go run ./products/customer-refund/managerapproval/cmd/worker
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

### Payload encryption

Create a key file outside the repository:

```bash
dexcli codec-server keygen -o /tmp/refund-key.json
```

Give the same key file to the Worker and to each command:

```bash
export REFUND_PAYLOAD_KEY_FILE=/tmp/refund-key.json
GOWORK=off go run ./products/customer-refund/managerapproval/cmd/worker
GOWORK=off go run ./products/customer-refund/managerapproval/cmd/start refund4 small
```

`NewFilePayloadEncryption` in `settings.go` turns the file into
`dex.PayloadEncryptionOptions` for the Worker and the Client. `case-status`
and `customer-email` are declared with `dex.Plaintext()`, so Dex Web reads
them without the key; the Action condition needs `case-status` in plaintext.
In the payloads Dex stores, Step inputs, `refund-id`, and `customer-message`
are encrypted. Encryption covers only those payloads. The local fake ledger
keeps idempotency keys, order IDs, and amounts, and no email text. The email
address is synthetic sample data.

To show the encrypted values in Dex Web, start Dex with the key file. Dex Web
then finds its codec server by itself (**Settings** > **Codec endpoint** >
**Auto**):

```bash
dexcli dev -codec-key-file /tmp/refund-key.json
```

### Test it

The integration tests run a dedicated Worker per test on its own task queue
and ledger directory. They cover approval, rejection, a refused blank reason,
concurrent decisions, each policy denial, the fail-then-succeed refund and its
attempt count in history, Time Travel to a successful and to a missing billing
call, encryption, and the example server routes. Run them with the Go E2E
suite:

```bash
./run-e2e-tests.sh -run 'TestManagerApprovalRefund'
```

`managerapproval/ledger_test.go` checks the ledger under concurrent calls with
the same key and with different keys; `make unitTests` runs it.
