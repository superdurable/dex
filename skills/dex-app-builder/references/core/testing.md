# Testing durable behavior

Write application tests when the user asks for them or the project already has tests. Then test behavior that crosses a Worker, Client, persistence boundary, wait, Timer, RPC, Stream, or SubFlow against a real Dex Server. A handler-only unit test or a mock cannot prove durable coordination. Test the application's own behavior; retries, heartbeat timeouts, Worker registration, credentials, and read-after-write guarantees are Dex and SDK behavior that Dex tests itself. Read the selected language's testing file for its harness and commands.

## Harness

- Start an isolated local Dex with `dexcli dev` on its own ports and data directory. Do not share the user's long-lived dev server.
- A short-lived test server can use process-crash-safe durability to run faster. See [cli.md](cli.md#local-development-with-dexcli-dev).
- Build the same registry, payload codecs, and blob cache configuration the application uses.
- Start the Worker and wait for its registration. Drive the Flow only through public Client APIs or the application boundary.
- Give every test a unique Flow ID.
- Prefer tests that can run in parallel. A test that stops, kills, or reconfigures a Worker uses its own Worker, or starts the replacement Worker before it stops the old one.
- Workers that register the same Flow type share its tasks, so Dex can hand a Step to any of them. A test that must control which Worker runs its Flow, such as a crash or kill test, gives that Flow a Flow type only its own Worker registers, or uses its own dev server. A fake decides its behavior from the input, never from which Worker runs it.
- Wait for asynchronous state with bounded polling or a Dex wait API. Never use a fixed sleep to guess convergence.
- To test behavior while a Step is running, such as drain, cancellation, or Worker replacement, have the Step or its fake signal that it started. Wait on that signal with a deadline, then act. Never sleep to guess that the Step started.
- Fakes for external effects record every call and never deduplicate. A test can then see a duplicate effect and assert how many times it happened.
- Stop every process you started.

## Assertions

- Assert the user-visible SDK error type, the final Flow status, the recovery Step, and the durable state. Do not just assert that an error occurred.
- When an external effect has an unknown outcome, test that the code records and handles the unknown state.
- Read direct state once after an RPC write; keep bounded polling for documented eventual views only: search and Attribute Store tables.

## Optional correctness checks

Before handoff, ask the user whether they want to try two optional Dex checks; do not recommend them strongly or require them. For supported source, `dexcli visualize` can flag missing or unresolved required locks on writes and publishes, and Attribute writes that may lose updates without a declared lock requirement; see [Declared lock requirements](https://docs.superdurable.io/reference/web/flow-visualization#declared-lock-requirements). `dexcli dev` can drop finished Step and RPC results, so a Step runs again and an RPC caller sees a failure; rerun the tests and check the same final result, one external effect each, and correct state in the rerun Step; see [Worker result loss injection](https://docs.superdurable.io/reference/server/configuration#dev-worker-result-loss).

## Scenarios to cover

Pick the scenarios that match the application:

- Start a Flow and verify its typed terminal output.
- Replace or restart the Worker while the Flow waits, then verify it continues from durable state.
- If a Step resumes from a heartbeat checkpoint, test that it resumes correctly.
- Business failure path: when the provider confirms a permanent rejection, such as rejected input or a refused request, Execute catches it and routes to the failure Step itself. A fake that returns the rejection, under the production configuration, verifies the application's failure branch and that it does not retry.
- A direct test of the recovery Step verifies its logic. Dex scheduling the recovery Step after the real retry budget is Dex behavior; do not re-test it by capping attempts or shortening durations.
- Test your RPCs' business outcomes, including after the Flow closes when the contract allows reads then.
- Fire and skip Timers. Verify the business deadline and the timeout handler path. Skip the Flow timeout the same way instead of shortening it; [Skip a Timer](https://docs.superdurable.io/reference/server/runtime-client#skip-a-timer) shows how to select it.
- Exercise parallel branches, and SubFlows if any, with a deliberate failure and cancellation.
- Lose a Stream consumer and recover canonical state from a read-only RPC or a Client Attribute read.
- Disconnect after an external effect but before its result reaches Dex, and verify idempotent replay.
- Worker registration, readiness, and credential handling are SDK behavior; test only your own wiring when it is custom.

### Start retry identity

- Retry a start with the same identity and verify that no duplicate run starts.
- For an AlreadyStarted branch that may reconcile, assert the read follows the failed start and its facts prove the requested condition. Test a snapshot that does not satisfy it as a conflict or unknown outcome.

### Post-start milestones

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
- Keep a Worker or service failure distinct from not found.

### Data and versions

- Large Attributes: replace the serving Worker and verify a cold-cache load.
- AttributeMap concurrency: race writers on one instance and verify the lock-protected invariant.
- Attribute Store sync: verify the Flow stays authoritative and the projection reconciles after a transient failure.
- Versions: start a Flow on the old Worker, deploy the new registry, and finish that same Flow run. A test that starts after deployment does not prove open-Flow compatibility.
