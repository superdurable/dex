# Customer refund (Rust)

Northstar Studio asks for a refund of its 1,249.00 USD subscription renewal
after a 31-hour service interruption. `CustomerRefundFlow` checks the order and
the refund policy, asks a manager because the amount is over 1,000 USD, issues
the refund, has a model write the customer message, emails it, and closes the
case.

Billing, email, and the model are fakes in this folder. They need no API key
and move no money. Billing and email remember each idempotency key in
`.customer-refund/`, so a restarted Worker sees what earlier Workers did. Each
ledger change holds a file lock, writes and syncs a temporary file, renames it
over the ledger, and syncs the folder. Concurrent calls with the same key
record one refund or one email, and a recorded change survives a crash. The
ledger write is the simulated refund or send; the printed line is only a log.
The email ledger keeps only idempotency keys, never the message text. Refund
IDs count the ledger's rows, so deleting rows by hand can reuse an ID.

The keys are `refund-<Flow ID>` and `email-<Flow ID>`. Give each Dex
environment its own `.customer-refund/` folder, or use Flow IDs that are unique
across environments. When a key already refunded a different order or amount,
**IssueRefund** fails with `RefundKeyConflict` instead of reporting the old
refund.

| File | What it holds |
| --- | --- |
| [`refund_flow.rs`](refund_flow.rs) | The Flow, its eight Steps, and the `ApproveRefund` and `RejectRefund` RPCs. |
| [`billing_service.rs`](billing_service.rs) | The fake billing service. |
| [`email_service.rs`](email_service.rs) | The fake email service. |
| [`json_ledger.rs`](json_ledger.rs) | The locked JSON files behind both fakes. |
| [`message_model.rs`](message_model.rs) | A deterministic stand-in for a language model. |
| [`refund_config.rs`](refund_config.rs) | Environment settings and the billing mode. |
| [`refund_scenarios.rs`](refund_scenarios.rs) | The refund requests that `start` can send. |
| [`refund_worker.rs`](refund_worker.rs) | Builds the Flow, runs its Worker, and opens a Client. |
| [`../../bin/customer-refund.rs`](../../bin/customer-refund.rs) | The `worker`, `start`, `decide`, and `list-refunds` commands. |

The integration test is
[`tests/customer_refund.rs`](../../../tests/customer_refund.rs).

## What the Rust version leaves out

The Python version of this example has three features that this Rust version
does not have:

- **Dex Web Actions.** The Rust SDK cannot declare an Action on an RPC, so Dex
  Web shows no **Approve refund** or **Reject refund** button. Approve or
  reject with the `decide` command instead.
- **The design graph.** `dexcli visualize` generates a design from Go, Python,
  and TypeScript sources, not from Rust. This example uses `dex-sdk` 1.44.0,
  which has no design upload, so the run has no **Design** graph in Dex Web.
  Since 1.45.0 the Rust SDK can upload an existing Flow Definition Graph 2.x
  JSON file from the Worker (`WorkerOptions::flow_definition_paths` or
  `flow_definitions`) or the Client. `dexcli dev -flow-rendering-dir` also lets
  Dex Web read existing graph files. History, State, and Time Travel work as
  usual.
- **Payload encryption.** The Rust SDK has no payload codec, so this example
  stores Flow values unencrypted.

`decide` approves as anyone who can reach Dex. Dex authentication does not
decide who may approve a refund; a production application checks the
approver itself before it calls the RPC.

## Run it

Run every command from `examples/rust`. Start Dex in one terminal:

```bash
dexcli dev
```

In scripts, run `dexcli dev -open=false` so it does not open a browser.

Start the Worker in a second terminal:

```bash
cargo run --locked --bin customer-refund -- worker
```

Start a refund in a third terminal. The Flow ID is your choice:

```bash
cargo run --locked --bin customer-refund -- start refund1
```

The run stops at **RequestManagerApproval**. Wait until `case-status` is
`awaiting-manager`, then approve or reject the refund:

```bash
dexcli flow state refund1 | grep -o '"case-status","value":"[a-z-]*"'
cargo run --locked --bin customer-refund -- decide refund1 approve "Outage confirmed"
```

A decision needs a reason that is not blank. Only one decision wins. A second
decision fails with `this refund is not waiting for a manager`, with
`RpcLockConflict` when it races the first, or with a not-found error once the
run has closed.

List the refunds that billing recorded:

```bash
cargo run --locked --bin customer-refund -- list-refunds
```

The Worker ID has the form `refund-worker-<random>`, so **Serving Worker ID**
in Dex Web shows no host name. Set `DEX_FLOW_SERVICE_ADDRESS` when Dex is not
at `127.0.0.1:8801`. It configures only this example's commands; pass the same
address to each `dexcli flow` command with `--server`.

## Scenarios

`start FLOW_ID SCENARIO` picks the run's input:

| Scenario | Order | What happens |
| --- | --- | --- |
| `normal` (default) | INV-1249, 1,249.00 USD | Waits for a manager, then refunds. |
| `fail-then-succeed` | INV-1249 | After approval, the refund fails twice, then succeeds. |
| `deny` | INV-0388, charged 47 days ago, no outage | The policy denies it; no approval, no refund. |
| `small` | INV-0049, 49.00 USD | Refunds without a manager. |

The customer message names each policy rule that denied the refund: the
30-day window, the missing outage, or both.

### A Step that fails, then succeeds

```bash
cargo run --locked --bin customer-refund -- start refund2 fail-then-succeed
dexcli flow state refund2 | grep -o '"case-status","value":"[a-z-]*"'
cargo run --locked --bin customer-refund -- decide refund2 approve "Outage confirmed"
```

As in [Run it](#run-it), run `decide` only after `case-status` is
`awaiting-manager`.

**IssueRefund** returns a `PaymentTimeout` error from `billing_service.rs` on
its first two attempts. Its retry policy waits 5 seconds between attempts. In
the **History** view, the final event shows attempt 3 succeeded and keeps the
details of the last failure.

### A missing billing call, fixed with Time Travel

Stop the normal Worker with Ctrl+C first, so it cannot pick up the next run.
Then start the Worker in the `call-missing` billing mode. It stands in for a
release whose **IssueRefund** Step forgot to call billing:

```bash
REFUND_BILLING_MODE=call-missing cargo run --locked --bin customer-refund -- worker
```

Start a refund, wait until `case-status` is `awaiting-manager`, and approve it:

```bash
cargo run --locked --bin customer-refund -- start refund3
dexcli flow state refund3 | grep -o '"case-status","value":"[a-z-]*"'
cargo run --locked --bin customer-refund -- decide refund3 approve "Outage confirmed"
```

The run completes and the customer is told the refund was issued. But the
`refund-id` Attribute is empty, and the billing list shows no refund with the
key `refund-refund3`.

Stop the Worker and start it again in the normal mode:

```bash
cargo run --locked --bin customer-refund -- worker
```

Take the run back to **IssueRefund**. In Dex Web, select the **IssueRefund**
event and press **Time travel from here**, or run:

```bash
dexcli flow time-travel refund3 --type step-type --target IssueRefund --reason "billing call restored" --yes
```

Dex runs **IssueRefund** and the Steps after it again. This time billing
records one refund, and `refund-id` holds its ID, such as `rf_1044`. The email
service sees the key `email-refund3` again and does not send a second email.
Each external call carries an idempotency key because Steps after a Time
Travel point run again. Time Travel to a refund that already succeeded also
gets the earlier refund ID back from billing instead of a second refund.

## Test it

```bash
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 make customerRefundTests
```

Stop the example's own Worker first; it would serve the test runs too. The
tests cover approval, rejection, a blank reason, racing approve and reject,
each policy denial, the fail-then-succeed refund and its History attempts, the
missing billing call fixed with `dexcli flow time-travel`, and Time Travel
after a refund. They need
`dexcli` on `PATH` or in `DEXCLI_PATH`. `cargo test --lib customer_refund`
runs the concurrent ledger tests without Dex.
