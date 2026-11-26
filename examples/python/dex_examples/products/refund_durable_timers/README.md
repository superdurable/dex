# Refund with durable Timers (Python)

Harbor Lane Bakery asks for a refund of its 1,860.00 USD order INV-2071.
`RefundDurableTimersFlow` collects supporting documents, waits for a manager,
issues the refund, waits for the bank to settle it, and closes the case. Each
wait uses a durable Timer, so the run keeps its place through Worker restarts
and deploys.

The Flow shows five uses of durable time, one lesson each:

| Lesson | Usage | Where |
| --- | --- | --- |
| 1 | **Reminder.** Remind the manager every 24 hours until they approve or reject. | `AwaitManagerDecision`: `Wait.any_of(Timer, Channel)`. A fired Timer sends a reminder and goes to the same Step. A decision moves on. |
| 2 | **Idle timer.** Close the case after 3 days without a customer message. | `CollectSupportingDocuments`: every message goes to the same Step, and its new wait starts a new 3-day Timer. |
| 3 | **Durable backoff and Retry-After.** Retry the payment provider with backoff, and honor its Retry-After. | `IssueRefund`: a `RetryPolicy` with explicit values, and `retry_after(seconds, cause)` for a rate limit. |
| 4a | **Polling with a WaitFor Timer.** Wait 6 hours, check once, repeat up to 12 checks. | `PollSettlementWithTimer`. Suits long intervals: no Worker is busy between checks. |
| 4b | **Polling in one long-running Step.** Check every 10 seconds in one Execute that heartbeats its check count. | `AwaitSettlementWithHeartbeat`. Suits short intervals. Its method timeout equals its retry total duration, as in the [polling pattern](../../patterns/polling/). |
| 5 | **Graceful timeout.** The whole refund must finish within 7 days. | `handle_timeout` with `FlowTimeoutPolicy.HANDLER`, set by `refund_start_options()`. It hands the case to the support team and records the outcome. |

The code uses the real business durations. Tests and demos fire a Timer with
SkipTimer instead of shortening it.

## Steps

| Step | What it does | Lesson |
| --- | --- | --- |
| `RequestSupportingDocuments` | Emails the customer for documents; `case-status` becomes `awaiting-documents`. | 2 |
| `CollectSupportingDocuments` | Waits for a customer message or 3 idle days. | 2 |
| `CloseInactiveCase` | Closes the case after 3 idle days. | 2 |
| `AwaitManagerDecision` | Waits for Approve or Reject; reminds the manager every 24 hours. | 1 |
| `IssueRefund` | Issues the refund, retrying with backoff and Retry-After. | 3 |
| `PollSettlementWithTimer` | Checks settlement once per 6-hour Timer, up to 12 times. The first check runs at once. | 4a |
| `AwaitSettlementWithHeartbeat` | Checks settlement every 10 seconds in one heartbeating Step, up to 30 times. | 4b |
| `EscalateUnsettledRefund` | Hands an unsettled refund to the support team. | 4 |
| `CloseCase` | Tells the customer the outcome and closes the case. | all |
| `handle_timeout` | Runs when the 7-day deadline passes and the case is still open. | 5 |

The RPCs are `send_customer_message`, `approve_refund`, and `reject_refund`.
Each refuses a call when `case-status` shows the case is not at that stage.
Approve and Reject are also Dex Web Actions with the `refund.manage`
permission.

## Files

| File | What it holds |
| --- | --- |
| [`refund_timers_flow.py`](refund_timers_flow.py) | The Flow, its Steps, its RPCs, the timeout handler, and `refund_start_options()`. |
| [`payment_provider.py`](payment_provider.py) | The fake payment provider. |
| [`bank_settlement_service.py`](bank_settlement_service.py) | The fake bank settlement API. |
| [`notification_service.py`](notification_service.py) | The fake email service. It prints each message. |
| [`refund_settings.py`](refund_settings.py) | The server address, the Registry, and the cache. |
| [`worker.py`](worker.py) | The Worker. It uploads the generated design. |
| [`start.py`](start.py) | Starts one refund with simulation knobs. |
| [`send_customer_message.py`](send_customer_message.py) | Sends a customer message. |
| [`decide.py`](decide.py) | Approves or rejects a refund. |
| [`skip_timer.py`](skip_timer.py) | Fires one waiting Timer now. |
| [`controller.py`](controller.py) | The HTTP routes on the shared example server. |

The fakes run in the Worker process and move no money. They need no network
or credentials. Each run's knobs travel in its start input. The fakes decide
from the Step attempt number and the durable check number, so a restarted
Worker sees the same outcomes.

## Run it

Run every command from `examples/python`. Start Dex in one terminal:

```bash
dexcli dev -open=false
```

Generate the design, then start the Worker in a second terminal:

```bash
uv sync --locked
dexcli visualize dex_examples/products/refund_durable_timers/refund_timers_flow.py --schema-version 2.0 --json --out build/refund-durable-timers/refund-durable-timers
uv run --frozen python -m dex_examples.products.refund_durable_timers.worker
```

The Worker prints each email, payment attempt, and bank check. Set
`DEX_FLOW_SERVICE_ADDRESS` when Dex is not at `127.0.0.1:8801`; pass the same
address to `dexcli` with `-server`.

Do not run this Worker and the shared example server against the same Dex at
once. Both serve `RefundDurableTimersFlow`.

### Presenter commands

`P` keeps the commands short:

```bash
P=dex_examples.products.refund_durable_timers
```

| Action | Command |
| --- | --- |
| Start a refund | `uv run --frozen python -m $P.start refund1` |
| Start with knobs | `uv run --frozen python -m $P.start refund1 --settlement-polling heartbeat --transient-failures 2 --rate-limit-retry-after 10 --settle-after-checks 3` |
| Start a refund the bank never settles | `uv run --frozen python -m $P.start refund1 --never-settle` |
| Customer message, still collecting | `uv run --frozen python -m $P.send_customer_message refund1 "I will find the invoice"` |
| Customer message, documents complete | `uv run --frozen python -m $P.send_customer_message refund1 "Attached: bank statement" --documents-complete` |
| Manager approves or rejects | `uv run --frozen python -m $P.decide refund1 approve "Receipts match"` |
| Fire a Timer now | `uv run --frozen python -m $P.skip_timer refund1 manager-reminder` |
| Show open Steps and Attributes | `dexcli flow state refund1 -output table` |
| Wait for the end | `dexcli flow wait refund1` |

`skip_timer` reads the open Step execution from the flow state, so it works
after any number of loops. With `dexcli` you name the execution yourself. Read
it from `stepExecutionId` in `dexcli flow state refund1`; for example,
`AwaitManagerDecision-2` is execution 2.

| Timer | Lesson | `skip_timer` name | `dexcli` command |
| --- | --- | --- | --- |
| Manager reminder (24 hours) | 1 | `manager-reminder` | `dexcli flow skip-timer -step-type AwaitManagerDecision -execution N -condition-id manager-reminder -yes refund1` |
| Customer idle (3 days) | 2 | `customer-idle` | `dexcli flow skip-timer -step-type CollectSupportingDocuments -execution N -condition-id customer-idle -yes refund1` |
| Settlement poll (6 hours) | 4a | `settlement-poll` | `dexcli flow skip-timer -step-type PollSettlementWithTimer -execution N -condition-id settlement-poll -yes refund1` |
| Refund deadline (7 days) | 5 | `refund-deadline` | `dexcli flow skip-timer -step-type sys:timeout_handler -execution 1 -condition-index 0 -yes refund1` |

The Flow timeout is the only Timer of the system Step `sys:timeout_handler`,
so it has no condition ID; select it by index 0 of execution 1. The flags go
before the Flow ID.

### Lesson walkthroughs

1. **Reminder.** Start `refund1` and send a message with
   `--documents-complete`. Fire `manager-reminder` twice: the Worker prints two
   reminders, and `reminders-sent` is 2. Approve; the refund completes.
2. **Idle timer.** Start `refund2`. Send a message without
   `--documents-complete`, and see `CollectSupportingDocuments-2` in the flow
   state: the message started a new wait. Fire `customer-idle`; the case
   closes as `closed-inactive`.
3. **Backoff and Retry-After.** Start `refund3 --transient-failures 2
   --rate-limit-retry-after 10`, send the documents, and approve. The payment
   fails twice, 2 then 4 seconds apart, then is rate-limited. The next attempt
   comes 10 seconds later, the provider's Retry-After, instead of the 8-second
   backoff. `dexcli flow state refund3` shows the attempt in
   `STEP_METHOD_INVOCATION_PHASE_BACKOFF` with `backoffFiresAtTime`.
4. **Two ways to poll.** Start `refund4 --settle-after-checks 3`, send the
   documents, and approve. Check 1 runs at once; fire `settlement-poll` twice
   for checks 2 and 3. Then start `refund5 --settlement-polling heartbeat
   --settle-after-checks 3`: one `AwaitSettlementWithHeartbeat` execution
   checks three times, 10 seconds apart, with no SkipTimer. With
   `--never-settle`, the Timer variant escalates after 12 checks and the
   heartbeat variant after 30.
5. **Graceful timeout.** Start `refund6` and fire `refund-deadline`. The
   timeout handler emails the support team, `case-status` becomes
   `escalated-deadline`, and the run completes with that outcome, even though
   a Step was still waiting.

## Run it on the shared example server

The shared example server (`uv run --frozen python main.py`, HTTP
`127.0.0.1:8080`) also serves this Flow. Every route takes `workflowId` (or
`flowId`):

| Route | Query | What it does |
| --- | --- | --- |
| `GET /products/refund-durable-timers/start` | `settlementPolling` (`timer` or `heartbeat`), `transientFailures`, `rateLimitRetryAfter`, `settleAfterChecks`, `neverSettle` | Starts a refund. Without `workflowId` it mints a `refund-timers-...` ID. |
| `GET /products/refund-durable-timers/send-customer-message` | `text`, `documentsComplete` | Sends a customer message. |
| `GET /products/refund-durable-timers/decide` | `decision` (`approve` or `reject`), `reason` | Approves or rejects. |
| `GET /products/refund-durable-timers/skip-timer` | `timer` | Fires one waiting Timer: `manager-reminder`, `customer-idle`, `settlement-poll`, or `refund-deadline`. |
| `GET /products/refund-durable-timers/describe` | | Returns `caseStatus`, `remindersSent`, and `refundId`. |

A refused RPC or a Timer that is not waiting returns 409. A closed or unknown
refund returns 404.

## Test it

```bash
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 uv run --frozen pytest -vv tests/integ/test_refund_durable_timers.py
```

The tests cover each lesson: two reminders then approval, a rejection, an idle
close after a message, backoff then Retry-After with measured gaps, Timer
polling to settlement and to escalation, heartbeat polling, and the timeout
handler.
