# Testing, diagnosis, and operations

## Test the durable behavior

Prefer integration tests for behavior involving Dex Server, a Worker, a Client, waiting, retry, persistence, or RPCs. Useful scenarios include:

- start the Flow and verify its terminal output
- publish a Channel message and verify the waiting Step advances
- invoke an RPC and verify its response and durable state change
- verify a Timer branch without relying on a fixed sleep
- force a retryable failure and verify retry or exhausted-retry routing
- verify duplicate start behavior with a stable request or Flow ID
- restart or replace a Worker and verify an open Flow continues
- verify concurrent Step or RPC updates preserve locked invariants
- verify terminal Flows reject interactions that require an active Flow

Use unique Flow IDs per test. Poll with a deadline for asynchronous convergence. Keep the Dex environment isolated or cleanly namespaced.

## Diagnostic order

1. Capture the exact Flow ID, run ID, Flow type, SDK version, and Dex Server address.
2. Check Worker application logs for handler and connectivity errors.
3. Inspect the Flow in Dex Web.
4. Run a bounded read-only inspection:

```bash
dexcli flow inspect <flow-id> --all-history
```

5. Compare the active Step, Attributes, Channel waits, Timers, and recent semantic events with the intended graph.
6. Reproduce with the smallest matching integration test.
7. Fix application code or configuration, then decide whether existing executions need recovery.

Use **dexcli flow search**, **summary**, **state**, and **history** for narrower JSON output. Use **--no-hydrate** when payload contents are unnecessary or sensitive.

## Common failure classes

- Worker unavailable: verify bind address, advertised target, network path, and process health.
- Unknown Flow or Step type: verify registry contents and deployment version.
- Attribute or Channel rejected: verify the persistence schema and exact stable name.
- Flow does not advance: inspect the active Step's Conditions and pending Channel/Timer state.
- Repeated side effect: make **Execute** idempotent and align retry policy with the external API.
- Closed Flow interaction: handle the SDK's typed not-active error at the application boundary.
- Large payload failure: verify Worker and Client blob/payload configuration matches.
- Incompatible deployment: restore the required Step type or fix forward with a new compatible path.

## Safe recovery

Diagnosis is read-only by default. Stop, time travel, publish, invoke, skip a Timer, or mutate Attributes only when the user asks to change the Flow.

Before a mutation:

- resolve the current exact run
- explain the expected state change
- use the public Client or dexcli operation
- satisfy any explicit confirmation flag
- re-inspect the Flow afterward

Time travel is appropriate after deploying a code fix when replaying from a safe Step boundary will not duplicate an unprotected side effect. If that cannot be established, design an explicit recovery or compensation Step instead.

## Deployment changes

Open Flows can continue after new Worker code is deployed. Before removing or changing a durable contract, search for open executions that may still reference it. Keep old Step types and schemas available until those executions finish or are deliberately migrated.

Sources:

- Dex CLI: https://docs.superdurable.io/references/cli
- Application operations: https://docs.superdurable.io/production/application-operations
- Versioning: https://docs.superdurable.io/production/versioning
