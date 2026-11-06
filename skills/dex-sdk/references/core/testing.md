# Testing durable behavior

Use a real Dex Server integration test whenever behavior crosses a Worker, Client, persistence boundary, wait, retry, Timer, RPC, Stream, or SubFlow. A handler-only unit test or a mock cannot prove durable coordination. Read the selected language's testing file for its harness and commands.

## Harness

- Start an isolated local Dex with `dexcli dev` on its own ports and data directory. Do not share the user's long-lived dev server.
- A short-lived test server can use process-crash-safe durability to run faster. See [cli.md](cli.md#local-development-with-dexcli-dev).
- Build the same registry, payload codecs, and blob cache configuration the application uses.
- Start the Worker and wait for its registration. Drive the Flow only through public Client APIs or the application boundary.
- Give every test a unique Flow ID.
- Every integration test must be able to run in parallel with the others. A test never stops, kills, or reconfigures a Worker that other tests share. A Worker replacement test starts the replacement Worker before it stops the old one, or starts its own Worker for the test. A crash or kill test always uses its own Worker process.
- Workers that register the same Flow type share its tasks, so Dex can hand a Step to any of them. A test that must control which Worker runs its Flow, such as a crash or kill test, gives that Flow a Flow type only its own Worker registers, or uses its own dev server. A fake decides its behavior from the input, never from which Worker runs it.
- Wait for asynchronous state with bounded polling or a Dex wait API. Never use a fixed sleep to guess convergence.
- To test behavior while a Step is running, such as drain, cancellation, or Worker replacement, have the Step or its fake signal that it started. Wait on that signal with a deadline, then act. Never sleep to guess that the Step started.
- Fakes for external effects record every call and never deduplicate. A test can then see a duplicate effect and assert how many times it happened.
- Stop every process you started.

## Assertions

- Assert the user-visible SDK error type, the final Flow status, the recovery Step, and the durable state. Do not just assert that an error occurred.
- When an external effect has an unknown outcome, test that the code records and handles the unknown state.
- After a direct-state RPC write, assert the read-only RPC and the Client Attribute read on the **first** read. No sleep or polling between write and read. Cover ordinary, transactional, and locked writes, loaded map instances, and pending queues.
- Use a controlled gate on a later Step or projection writer to prove that readable direct state does not imply downstream completion. Release the gate, then confirm completion with a bounded condition.
- Keep bounded polling for documented eventual views only: search and Attribute Store tables.

## Required scenarios

Core behavior:

- Start a Flow and verify its typed terminal output.
- Replace or restart the Worker while the Flow waits, then verify it continues from durable state.
- Force a retryable Execute failure. Verify the retry count, resume from the heartbeat checkpoint, and the exhausted-retry route.
- Let a handler exceed its heartbeat timeout and assert the retry.
- Publish Channel and ChannelMap messages through typed RPCs. Verify order, single consumption, stale message IDs, and rejection after closure.
- Invoke read-only and mutating RPCs. After closure, verify that query-only reads still work, that a non-transactional RPC's effects are rejected, and that transactional or locked RPCs are rejected before the handler. For transactional RPCs, verify all effects commit or none do.
- Fire and skip Timers. Verify the business deadline and the timeout handler path.
- Exercise parallel branches, and SubFlows if any, with a deliberate failure and cancellation.
- Lose a Stream consumer and recover canonical state from a read-only RPC or a Client Attribute read.

Worker lifecycle:

- Start a Flow with no Worker. Assert the Step stays pending, consumes no retry, and runs once a Worker starts.
- Request a Step type that only a not-yet-started Worker registers. Assert it waits without blocking other Step types and keeps its attempt number.
- Assert an RPC with no Worker gets UNAVAILABLE, and an unregistered RPC name gets FAILED_PRECONDITION.
- Disconnect a Worker after hand-out, for SYNC and ASYNC. Assert the attempt fails, consumes a retry, and recovery sees NOT_COMPLETED.
- Stop a Worker while a handler runs. Assert the handler finishes, its result commits, and no retry is consumed.
- Fill every Step slot with long Steps. Assert RPCs still run on reserved slots and a further Step waits pending.
- Run old and new Workers side by side. Assert a Step type only the new Worker registers runs there without consuming an attempt.
- Disconnect after an external effect but before its result reaches Dex, and verify idempotent replay.
- With authentication on, verify Client and Worker calls carry credentials, a missing or bad credential is rejected, and a rotated token works after reconnect.

### Worker registration, readiness, and connection failures

These tests cover the failure paths of the Worker and Client connections. Read the language testing file for the API names.

- Registration wait: verify the bounded registration wait succeeds only after every queue registers. Cancel the wait and assert the Worker keeps running. Cancelling the wait never stops the Worker.
- Replacement: assert replacement Workers register before the old Workers stop.
- Readiness: after the Worker is ready, disconnect and reconnect its streams. Assert the readiness probe stays ready through the transient disconnect. Stopping the Worker alone keeps the last readiness snapshot; the application's shutdown health removes readiness.
- Stream rejection: reject a surplus stream while another registered stream remains, then reject the last remaining stream permanently. Only losing the last stream changes readiness or triggers the opt-in shutdown.
- Rejection handling: test the default behavior, which keeps retrying, separately from the opt-in shutdown on permanent rejection. Cover handler drain, cancellation, and the language's typed registration error.
- Token refresh: with a credential that exchanges a key for access tokens, run a local token endpoint fixture. Verify that concurrent first calls share one exchange and that a refresh keeps calls on a valid token. Reject one call with UNAUTHENTICATED and assert a single retry with a fresh token. Reject a Worker stream and assert one reconnect, then the ordinary permanent-rejection path on a second rejection. Assert that a bad token endpoint response fails the call.
- Redaction: assert that errors and logs never contain the key or an access token.
- Capacity errors: inject RESOURCE_EXHAUSTED at the service boundary. Verify the error classification, the retry hint when one is present, bounded backoff, the overall deadline, and reconciliation of a mutation without duplicate effects.
- Controlled fixture: a real Server cannot always induce a rejection, a reconnect, or a capacity error on demand. For those cases only, use a controllable service fixture. Keep real-Server tests for registration and for the underlying start, RPC, and search operations.

### StartFlow ordering and retry identity

- Assert StartFlow is the first Dex call of a normal start, with no read, search, or status call for duplicate protection.
- Assert a successful accepted-start response makes no follow-up read to verify supplied identity or initial Attributes, and starts no dependent Flow from the API.
- With a reuse policy that rejects another run: the same Request ID with ignore-already-started returns the existing run, while active and after closure; the same ID without the option errors; a different ID conflicts even with the option.
- Lose the start response after acceptance, retry with identical identity and options, and verify convergence without a preflight read.
- For an AlreadyStarted branch that may reconcile, assert the read follows the failed start and its facts prove the requested condition. Test a snapshot that does not satisfy it as a conflict or unknown outcome.

### Required post-start milestones

For an API that promises a committed source-of-truth write, test a new start and a deduplicated start. Delay the commit and assert the response cannot claim the milestone early. Verify the actual business row afterward. Let the wait budget expire while the Flow continues, retry with the same identity, and assert no replacement execution and no false failure. For an acceptance-only API, assert no wait is issued.

### Downstream start recovery

For an operation that launches separately owned work:

- Accept the owning Flow, end the API request, then replace the Worker before the downstream start. The owning Flow must still start it.
- Crash the Worker after downstream acceptance but before the launching Step commits. Verify the retried start uses the same identity and creates no duplicate.
- Assert downstream retry exhaustion reaches the declared recovery outcome, and the API never issues the dependent start.
- A graceful Worker drain does not simulate this commit gap; kill the process.

### Terminal entity reads

For every entity whose contract allows reads after closure:

- Persist the final snapshot, close the Flow, then call the business Get through the application boundary. Assert the final committed Attributes and map instances for each readable terminal outcome.
- For a nonexistent Flow ID, assert the Get returns the declared not-found result directly, with no status probe, search, history call, retry, or short-timeout wait.
- When the Get uses the Client Attribute read, run the same cases with no Worker running after closure. Assert unset values use the language's unset representation.
- Assert the business read issues no WaitForFlow, history lookup, or Step-output decoding. A spy on the real Client boundary may count calls; count harness waits separately.
- Keep a Worker or service failure distinct from not found.

### Data and versions

- Large Attributes: replace the serving Worker and verify a cold-cache load.
- AttributeMap concurrency: race writers on one instance and verify the lock-protected invariant.
- Attribute Store sync: verify the Flow stays authoritative and the projection reconciles after a transient failure. With a row key template, verify matching Flow IDs land in the key columns and a non-matching ID writes no row; verify instance rewrite and delete in an AttributeMap table.
- Versions: start a Flow on the old Worker, deploy the new registry, and finish that same execution. A test that starts after deployment does not prove open-Flow compatibility.
- Recovery: time travel a failed Flow after a fix and verify the new run attempt.
