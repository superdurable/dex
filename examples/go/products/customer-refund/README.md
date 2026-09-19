# Customer refund example

This product contains two Go Flows for the same customer-refund problem.

- `deterministic/workflow.go` is a fixed seven-Step policy. It verifies the 30-day window, uses a persisted provider idempotency key, and preserves declined or unknown outcomes.
- `agentic/workflow.go` loops through durable evidence, records a recommendation, applies a guardrail, opens a keyed approval gate, and separates intent from idempotent effects.

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
  -d '{"case":{"caseId":"order-42","customer":"customer-1","customerNote":"Please refund this charge","amountCents":4200,"orderAgeDays":12}}'

curl -X POST http://127.0.0.1:8080/products/customer-refund/agentic/start \
  -H 'content-type: application/json' \
  -d '{"case":{"caseId":"rule-escalation","customer":"customer-2","customerNote":"Please review this charge","amountCents":1400000,"orderAgeDays":8}}'
```

The agentic Flow exposes `ApproveRefund` as a no-input Action. `RejectRefund` asks for a reason and binds the hidden `gate-request-key` snapshot supplied by Dex Web. Both RPCs re-check the current status before publishing a verdict.
