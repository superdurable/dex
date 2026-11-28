# Testing durable behavior

Write application tests when the user asks for them or the project already has tests. Then test behavior that crosses a Worker, Client, persistence boundary, wait, Timer, RPC, Stream, or SubFlow against a real Dex Server. A handler-only unit test or a mock cannot prove durable coordination. Test the application's own behavior; retries, heartbeat timeouts, Worker registration, credentials, and read-after-write guarantees are Dex and SDK behavior that Dex tests itself. Read the selected language's testing file for its harness and commands.

## Dex facts for tests

- Workers that register the same Flow type in one task queue share its tasks, and a repeated Flow ID goes to the ID reuse policy ([Workers](../../SKILL.md#workers-attempts-and-time-bounds), [Starting a Flow](../../SKILL.md#starting-a-flow-and-id-reuse)).
- A clean Worker shutdown returns the attempt to pending without using a retry, while a crash or disconnect after delivery fails the attempt and uses a retry, so the two exercise different recovery paths ([Workers](../../SKILL.md#workers-attempts-and-time-bounds)). SkipTimer makes a waiting Timer ready at once ([Timers](../../SKILL.md#timers)).

## Assertions

- Assert the user-visible SDK error type, the final Flow status, the recovery Step, and the durable state. Do not just assert that an error occurred.
- When an external effect has an unknown outcome, test that the code records and handles the unknown state.
- A direct read after an RPC write returned sees that write ([Runs and reads](../../SKILL.md#runs-reads-and-outputs)), so read it once; search and Attribute Store tables lag commits and need bounded polling.

## Optional correctness checks

For supported source, `dexcli visualize` can flag missing or unresolved required locks on writes and publishes, and Attribute writes that may lose updates without a declared lock requirement; see [Declared lock requirements](https://docs.superdurable.io/reference/web/flow-visualization#declared-lock-requirements). `dexcli dev` can drop finished Step and RPC results, so a Step runs again and an RPC caller sees a failure; rerunning the tests then checks the same final result, one external effect each, and correct state in the rerun Step; see [Worker result loss injection](https://docs.superdurable.io/reference/server/configuration#dev-worker-result-loss).

## Scenarios to cover

Pick the scenarios that match the application:

- Start a Flow and verify its typed terminal output.
- Replace or restart the Worker while the Flow waits, then verify it continues from durable state.
- If a Step resumes from a heartbeat checkpoint, test that it resumes correctly.
- Business failure path: when the provider confirms a permanent rejection, such as rejected input or a refused request, Execute routes to the failure Step itself; verify that branch and that it does not retry.
- A direct test of the recovery Step verifies its logic. Dex scheduling the recovery Step after the real retry budget is Dex behavior; usually do not re-test it by capping attempts or shortening durations.
- Test your RPCs' business outcomes, including after the Flow closes when the contract allows reads then.
- Fire and skip Timers. Verify the business deadline and the timeout handler path. Usually skip the Flow timeout the same way instead of shortening it, as in [Real durations in tests](../../SKILL.md#suggestions); [Skip a Timer](https://docs.superdurable.io/reference/server/runtime-client#skip-a-timer) shows how to select it.
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

### Terminal entity reads

For every entity whose contract allows reads after closure:

- Persist the final snapshot, close the Flow, then call the business Get through the application boundary. Assert the final committed Attributes and map instances for each readable terminal outcome.
- For a nonexistent Flow ID, assert the Get returns the declared not-found result directly, with no status probe, search, history call, retry, or short-timeout wait.
- When the Get uses the Client Attribute read, run the same cases with no Worker running after closure. Assert unset values use the language's unset representation.
- Keep a Worker or service failure distinct from not found.

### Data and versions

- AttributeMap concurrency: race writers on one instance and verify the lock-protected invariant.
- Attribute Store sync: verify the Flow stays authoritative and the projection reconciles after a transient failure.
- Versions: start a Flow on the old Worker, deploy the new registry, and finish that same Flow run. A test that starts after deployment does not prove open-Flow compatibility.
