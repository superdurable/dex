# Customer refund example

This product contains two Go Flows for the same customer-refund problem.

- `deterministic/workflow.go` is a fixed seven-Step policy. It verifies the 30-day window, uses a persisted provider idempotency key, and preserves declined or unknown outcomes.
- `agentic/workflow.go` loops through durable evidence, records a recommendation, applies a guardrail, opens a keyed approval gate, and separates intent from idempotent effects. An OpenAI Connector Step drafts the customer response before a **second** keyed gate lets a person confirm or rewrite it.

Both files are self-contained FDG 2.0 sources. Every Step declares `dex:group`
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
| `customer-email` | `CustomKeyword` keyword | exact email address |
| `refund-amount` | `CustomDouble` double | ranges, in dollars rather than cents |
| `case-status` | `CustomKeyword2` keyword | exact, or one of several |

The email is synthetic sample data only. Do not index email addresses or other
PII in production; follow Temporal's Search Attribute guidance.

Raw searches for these generic slots must also filter
`FlowType = 'AgenticCustomerRefundFlow'`.

Read [the Go examples README](../../README.md#run-locally) before changing any of
them: the Worker reconciles Indexed Attributes against the store at startup.
