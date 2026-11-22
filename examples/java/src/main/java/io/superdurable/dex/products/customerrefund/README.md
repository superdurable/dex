# Customer refund (Java)

Northstar Studio asks for a refund of its 1,249.00 USD subscription renewal
after a 31-hour service interruption. `CustomerRefundFlow` checks the order and
the refund policy, asks a manager because the amount is over 1,000 USD, issues
the refund, has a model write the customer message, emails it, and closes the
case.

Billing, email, and the model are fakes in this folder. They need no API key
and move no money. Billing and email remember each idempotency key in
`.customer-refund/`, so a restarted Worker sees what earlier Workers did. Each
ledger read and change holds a file lock, and a change replaces the file by an
atomic rename, so concurrent Workers never lose or duplicate a record. A file
system without atomic rename fails with a clear error. The email ledger keeps
only idempotency keys, not addresses or message text.

The idempotency keys are built from Flow IDs, such as `refund-refund1`. Give
each Dex environment its own ledger directory, or keep Flow IDs unique across
environments. When a key already belongs to a refund of a different order or
amount, billing fails with `IdempotencyKeyConflictException` instead of
reporting the old refund.

`REFUND_BILLING_MODE` affects only the Worker. `start`, `decide`, and
`list-refunds` ignore it.

| File | What it holds |
| --- | --- |
| [`CustomerRefundFlow.java`](CustomerRefundFlow.java) | The Flow, its eight Steps, and the `approveRefund` and `rejectRefund` RPCs. |
| [`FakeBillingService.java`](FakeBillingService.java) | The fake billing service and the `call-missing` stand-in. |
| [`FakeEmailService.java`](FakeEmailService.java) | The fake email service. |
| [`LockedJsonLedger.java`](LockedJsonLedger.java) | The locked JSON file that both fakes keep their records in. |
| [`MockMessageModel.java`](MockMessageModel.java) | A deterministic stand-in for a language model. |
| [`RefundSettings.java`](RefundSettings.java) | Environment settings, the scenarios, the billing mode, and the Worker options. |
| [`CustomerRefundCommand.java`](CustomerRefundCommand.java) | The `customer-refund` command: `worker`, `start`, `decide`, and `list-refunds`. |
| [`CustomerRefundController.java`](CustomerRefundController.java) | The HTTP routes of the shared Spring Boot application. |
| [`CustomerRefundConfig.java`](CustomerRefundConfig.java) | Registers the Flow, with normal billing, in the shared application. |

The integration tests are
[`CustomerRefundIntegTest.java`](../../../../../../../test/java/io/superdurable/dex/integ/CustomerRefundIntegTest.java)
for the standalone Worker and
[`CustomerRefundControllerIntegTest.java`](../../../../../../../test/java/io/superdurable/dex/integ/CustomerRefundControllerIntegTest.java)
for the HTTP routes.

You can run the example two ways: through the shared Spring Boot application
of `examples/java`, or with its own Worker and command. Only the own Worker
has the `call-missing` billing mode for the Time Travel story.

## Run it in the shared application

Start Dex and the shared application from `examples/java`:

```bash
dexcli dev
./gradlew bootRun
```

The application registers the Flow with normal billing and keeps the ledgers
in `.customer-refund/`. Then call the routes:

```bash
curl 'localhost:8080/products/customer-refund/manager-approval/start?flowId=refund1'
curl 'localhost:8080/products/customer-refund/manager-approval/decide?flowId=refund1&decision=approve&reason=Outage%20confirmed'
curl 'localhost:8080/products/customer-refund/manager-approval/describe?flowId=refund1'
```

Like the order-processing routes, each route takes the Flow ID as `flowId` or
its alias `workflowId`, and `start` returns `flowID` and `runID`. `start`
takes an optional Flow ID; without one it creates
`customer-refund-<number>`. Its `scenario` is one of the
[scenarios](#scenarios) and defaults to `normal`. `decide` does not wait: it
returns 409 until `case-status` is `awaiting-manager`, and also when another
decision holds the `case-status` lock. A missing Flow ID, a blank reason, or
an unknown decision returns 400, and an unknown Flow ID returns 404.
`describe` returns the run's `status`, and the `case-status` and `refund-id`
Attributes as `caseStatus` and `refundId`.

The shared application and the own Worker both serve this Flow type. Stop the
shared application before you follow the own-Worker stories below. Otherwise
it can serve their runs with normal billing.

## Run it with its own Worker

Run every command from `examples/java`. You need JDK 17 or newer. Start Dex
in one terminal:

```bash
dexcli dev
```

In scripts, run `dexcli dev -open=false` so it does not open a browser.

Build the command, then start the Worker in a second terminal:

```bash
./gradlew installCustomerRefund
build/install/customer-refund/bin/customer-refund worker
```

The command runs outside Gradle, so Ctrl+C stops the Worker itself.

Start a refund in a third terminal. The Flow ID is your choice:

```bash
build/install/customer-refund/bin/customer-refund start refund1
```

The run stops at **RequestManagerApproval** and sets `case-status` to
`awaiting-manager`. `dexcli flow state refund1` shows it. Approve or reject
the refund once it is there:

```bash
build/install/customer-refund/bin/customer-refund decide refund1 approve "Outage confirmed"
```

`decide` first waits up to 30 seconds for `case-status=awaiting-manager`, so
it also works right after `start`. A decision needs a non-blank reason.

List the refunds that billing recorded:

```bash
build/install/customer-refund/bin/customer-refund list-refunds
```

The Worker ID has the form `refund-worker-<random>`, so **Serving Worker ID**
in Dex Web shows no host name. Set `DEX_FLOW_SERVICE_ADDRESS` when Dex is not
at `127.0.0.1:8801`.

### No design view and no Dex Web Actions

The Java SDK does not declare Dex Web Actions yet, and `dexcli visualize`
does not read Java sources. So this Worker uploads no design. Dex Web shows the
run, its History, and its Attributes, but no **Design** or **Actions** tab.
Approve or reject with `customer-refund decide`. The Python version of this
example shows both tabs.

Both RPCs lock `case-status`: Dex takes the lock before it calls the RPC.
`approveRefund` and `rejectRefund` then reject a blank reason and check that
`case-status` is `awaiting-manager`, before they write or publish anything. So
when two decisions race, only one succeeds.

## Scenarios

`customer-refund start FLOW_ID SCENARIO` picks the run's input:

| Scenario | Order | What happens |
| --- | --- | --- |
| `normal` (default) | INV-1249, 1,249.00 USD | Waits for a manager, then refunds. |
| `fail-then-succeed` | INV-1249 | After approval, the refund fails twice, then succeeds. |
| `deny` | INV-0388, charged 47 days ago | The policy denies it; no approval, no refund. |
| `small` | INV-0049, 49.00 USD | Refunds without a manager. |

The customer message names the policy condition that denied the refund: the
30-day window, no recorded outage, or both. The fake billing also has
INV-0412 (outside the window only) and INV-0517 (no outage only); the
integration test uses them.

### A Step that fails, then succeeds

```bash
build/install/customer-refund/bin/customer-refund start refund2 fail-then-succeed
build/install/customer-refund/bin/customer-refund decide refund2 approve "Outage confirmed"
```

**IssueRefund** throws `PaymentTimeoutException` from
`FakeBillingService.java` on its first two attempts. Its retry policy waits 5
seconds between attempts, and the third attempt succeeds. In the **History**
view, the final event shows attempt 3 succeeded and keeps the details of the
last failure, with its stack trace. `dexcli flow history refund2 --all` shows
the same `finalAttempt` and `lastFailureInfo`.

### A missing billing call, fixed with Time Travel

First stop the normal Worker with Ctrl+C. Otherwise it can serve this run
and issue the refund. Then start the Worker in the `call-missing` billing
mode. It stands in for a release whose **IssueRefund** Step forgot to call
billing:

```bash
REFUND_BILLING_MODE=call-missing build/install/customer-refund/bin/customer-refund worker
```

Start and approve a refund:

```bash
build/install/customer-refund/bin/customer-refund start refund3
build/install/customer-refund/bin/customer-refund decide refund3 approve "Outage confirmed"
```

The run completes and the customer is told the refund was issued. But the
`refund-id` Attribute is empty, and `list-refunds` shows no refund with the
key `refund-refund3`.

Stop the Worker with Ctrl+C and start it again in the normal mode:

```bash
build/install/customer-refund/bin/customer-refund worker
```

Take the run back to **IssueRefund**. In Dex Web, select the **IssueRefund**
event and press **Time travel from here**, or run:

```bash
dexcli flow time-travel refund3 --type step-type --target IssueRefund --reason "billing call restored" --yes
```

Dex runs **IssueRefund** and the Steps after it again. This time billing
records one refund, and `refund-id` holds its ID, such as `rf_1043`. The email
service sees the key `email-refund3` again and does not send a second email.
Each external call carries an idempotency key because Steps after a Time
Travel point run again.

## Payload encryption

The Java SDK cannot encrypt or decrypt payloads yet, so this example has no
encryption mode. The Python version shows it.

## Test it

```bash
DEX_FLOW_SERVICE_ADDRESS=127.0.0.1:8801 ./gradlew test --tests 'io.superdurable.dex.integ.CustomerRefundIntegTest'
```

The tests cover approval, rejection, a blank reason, racing approve and reject
decisions, each policy denial, the
fail-then-succeed refund and its history, the missing billing call fixed with
`dexcli flow time-travel`, and a Time Travel that reuses an issued refund.
They need `dexcli` on `PATH` or in `DEXCLI_PATH`. `./run-integration-tests.sh`
runs them with the rest of the Java examples.

`CustomerRefundControllerIntegTest` calls the HTTP routes' controller with the
shared integration Worker: an approval, a rejection, a second decision while
the refund retries, and the 400 and 404 errors. Its ledgers live in a
temporary directory.

The ledger tests need no Dex server. In one JVM, they race billing calls and
email calls with the same key and with different keys, also through a
directory alias. Eight separate processes race billing calls:

```bash
./gradlew test --tests 'io.superdurable.dex.products.customerrefund.*'
```
