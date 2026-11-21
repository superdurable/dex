# Customer refund (Java)

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
| [`CustomerRefundFlow.java`](CustomerRefundFlow.java) | The Flow, its eight Steps, and the `approveRefund` and `rejectRefund` RPCs. |
| [`FakeBillingService.java`](FakeBillingService.java) | The fake billing service and the `call-missing` stand-in. |
| [`FakeEmailService.java`](FakeEmailService.java) | The fake email service. |
| [`MockMessageModel.java`](MockMessageModel.java) | A deterministic stand-in for a language model. |
| [`RefundSettings.java`](RefundSettings.java) | Environment settings, the scenarios, the billing mode, and the Worker options. |
| [`CustomerRefundCommand.java`](CustomerRefundCommand.java) | The `customer-refund` command: `worker`, `start`, `decide`, and `list-refunds`. |

The integration test is
[`CustomerRefundIntegTest.java`](../../../../../../../test/java/io/superdurable/dex/integ/CustomerRefundIntegTest.java).

This example runs its own Worker, not the Spring Boot application of
`examples/java`.

## Run it

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

The run stops at **RequestManagerApproval**. Approve or reject it:

```bash
build/install/customer-refund/bin/customer-refund decide refund1 approve "Outage confirmed"
```

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

`approveRefund` and `rejectRefund` lock `case-status` and check that it is
`awaiting-manager`, so a second decision fails.

## Scenarios

`customer-refund start FLOW_ID SCENARIO` picks the run's input:

| Scenario | Order | What happens |
| --- | --- | --- |
| `normal` (default) | INV-1249, 1,249.00 USD | Waits for a manager, then refunds. |
| `fail-then-succeed` | INV-1249 | After approval, the refund fails twice, then succeeds. |
| `deny` | INV-0388, charged 47 days ago | The policy denies it; no approval, no refund. |
| `small` | INV-0049, 49.00 USD | Refunds without a manager. |

### A Step that fails, then succeeds

```bash
build/install/customer-refund/bin/customer-refund start refund2 fail-then-succeed
build/install/customer-refund/bin/customer-refund decide refund2 approve "Outage confirmed"
```

**IssueRefund** throws `PaymentTimeoutException` from
`FakeBillingService.java` on its first two attempts. Its retry policy waits 5
seconds between attempts, so the **History** view shows each failed attempt
and its stack trace before the third attempt succeeds.

### A missing billing call, fixed with Time Travel

Start the Worker in the `call-missing` billing mode. It stands in for a
release whose **IssueRefund** Step forgot to call billing:

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

The tests cover approval, rejection, the policy denial, the fail-then-succeed
refund, and the missing billing call fixed with `dexcli flow time-travel`. They
need `dexcli` on `PATH` or in `DEXCLI_PATH`. `./run-integration-tests.sh` runs
them with the rest of the Java examples.
