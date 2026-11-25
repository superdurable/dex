# Fail Fast on Permanent Errors

**ExpenseReimbursementFlow** pays an approved expense claim through a payment
provider. **SubmitPayout** calls the provider in `execute` and classifies its
error:

- `PayoutTimeoutError` is transient. `execute` lets it raise, and Step retry
  runs the call again under the Step's retry policy, whose retry scheduling
  budget is one hour.
- `PayoutRejectedError` is permanent. The provider has confirmed that the bank
  account is invalid, so a retry can never succeed. `execute` catches it and
  goes to **RecordRejectedClaim** at once.

**RecordRejectedClaim** records the rejection in the **PayoutOutcome**
Attribute and completes the Flow.

**FakePaymentProvider** stands in for the provider. It accepts only a bank
account of eight ASCII digits and rejects any other with a fixed reason that
does not echo the account number. It times out on the first submission of every valid
claim, then pays it. The Flow ID is the claim ID and the provider's idempotency
key.

## Endpoints

- `GET /patterns/fail-fast/start?workflowId={workflowId}&amountCents={amountCents}&bankAccount={bankAccount}`
- `GET /patterns/fail-fast/get-outcome?workflowId={workflowId}`

See
[Fail Fast on Permanent Errors](https://docs.superdurable.io/design-patterns/failure-handling/fail-fast-on-permanent-errors)
for the pattern rules.
