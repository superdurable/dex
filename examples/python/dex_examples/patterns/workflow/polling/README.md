# Polling Flows

Two ways to wait on an external system:

1. **`SimplePollingFlow`** — checks readiness on a fixed timer and proceeds once
   the system is ready.
2. **`BackoffPollingFlow`** — relies on step-level retry with exponential
   backoff and proceeds once the call succeeds.

## Use cases

- **Waiting on external conditions** — the flow cannot proceed until an external
  system reaches the right state.
- **Data ingestion** — periodically pull data from a partner or market-data
  provider.
- **Resource readiness** — wait for a container or database to be provisioned.
- **Payment status** — poll a payment gateway until the transaction is verified.

## Choosing between them

**Backoff polling** fits unpredictable dependencies:

- Less code, since the retry policy does the work.
- Handles highly variable startup times.
- Respects API rate limits by spacing retries out over time.
- Cheaper: each retry costs one action and adds no history events.

Drawbacks: the retry policy is fixed-interval or exponential only, never
condition-dependent; failures pollute step-API availability metrics and usually
need to be excluded from monitoring queries; and a long backoff can delay the
flow when the resource becomes available sooner than expected.

**Simple polling** fits predictable dependencies:

- Consistent, predictable check cadence.
- Fast to react when the resource appears quickly.
- Straightforward when polling is cheap.

Drawbacks: excessive calls when readiness takes longer than expected, and higher
Temporal cost — each attempt uses a timer plus two activities and produces about
ten history events.

## Steps

`SimplePollingFlow`

- `SimplePolling` — fixed 10-second timer, then a readiness check.
- `SimplePollingComplete` — completes the flow.

`BackoffPollingFlow`

- `ReadExternalDep` — calls the external dependency with retry (5 attempts,
  3-second initial interval, coefficient 2.0, 60-second cap, 3600-second total).
- `PollingComplete` — completes the flow with the external result.
