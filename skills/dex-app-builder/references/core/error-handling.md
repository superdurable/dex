# Error handling and recovery

Read this before you write any Client boundary: a Flow start, an RPC, a wait, cleanup, admission, or an external Stream write. Design each failure outcome together with the happy path. Then read the selected language's error-handling file for its exact error types.

Read [client.md](client.md#read-after-write) for which reads observe which writes. A successful direct-state RPC write followed by a typed read-only RPC or a Client Attribute read needs no polling. A failed or ambiguous write still needs reconciliation.

## Classify at the decision boundary

Branch on the SDK's typed Dex errors; the error families are below. Usually use a named status or sub-status only when the SDK has no more specific error type.

The error model is language-specific. Python, Java, and TypeScript have a remote-only service-error base. Go concrete remote errors unwrap to a service error type. Rust uses one error enum for service and local failures. Never copy a catch pattern between SDKs.

## Error families an application handles

Every SDK maps these server outcomes to typed errors. The language files give the exact names.

| Family | Meaning | Typical handling |
| --- | --- | --- |
| Flow already started | A start was rejected by the reuse policy, or retried the latest run's start without ignore-already-started. | See [start identity](#start-identity-and-duplicate-starts). |
| Combined missing-or-inactive target | No readable target, or the target is closed for an operation that needs an active Flow. | Interpret by operation. See [closed-Flow races](#closed-flow-races). |
| Flow not found | No readable run exists. Used by DescribeFlow and the Client Attribute read. | Return the contract's not-found result. |
| Flow closing | A request arrived while the Flow was closing, such as a transactional RPC, a wait, or a Channel publish. The request was not accepted. | Read the terminal state, then decide. |
| RPC lock conflict | Another Step or RPC holds a required Attribute lock now; Dex does not queue the RPC. | Retry with bounded backoff, or return a busy result to the caller. See [locks](attribute.md#locks). |
| Channel message not found | A pending-message delete or transactional move named a message that is no longer pending: consumed, deleted, or moved by an earlier attempt of this call. | Pass a stable RPC request ID so a retried move returns its recorded reply; otherwise reload the pending list before deciding. |
| Request timeout | A Client wait reached its caller-set request timeout. | Not a failure of the Flow. See [waits](client.md#waits). |
| Flow definition error | A definition is not registered or does not match. | Local defect. |
| Value mapping error | A value cannot be encoded or decoded. | Local defect. |
| Service error | Any other remote failure. | Bounded retry or translation. |

### Capacity and rate limits

The Server reports capacity and rate-limit failures as RESOURCE_EXHAUSTED, not INTERNAL. The response keeps the message and any retry-delay hint.

- Use the SDK's named status and the available details. Do not parse text.
- It is not always retryable. When it reports load, retry with backoff inside the caller's deadline when the SDK has not already retried, honoring a retry hint when present. A Flow over its active-state budget, or a value over the size limit, fails again on retry until the state or value shrinks; see [default limits](../../SKILL.md#default-limits).
- A failed mutation can still have an uncertain outcome. Reconcile through its stable identity before you repeat an effect.
- SDK-managed retries can already use up the deadline.
- A pull Worker stream rejected with RESOURCE_EXHAUSTED is a registration problem, not a Client API failure. See [application.md](application.md#worker-registration-and-readiness).

### Indexed search limits

A search backend can cap how many index keys of each type exist and how long an indexed Keyword value may be. Treat both failures as design or deployment defects, never as retryable.

- **Index capacity.** Only Worker startup registers indexes. A registration over capacity fails and the Worker does not start. Fix the index key plan. A retry fails the same way.
- **Keyword too long.** A StartFlow or RPC that writes an over-long indexed Keyword fails with INVALID_ARGUMENT and nothing from it commits. The RPC handler has already run. Validate or shorten the value before the write. Index long text as Text, or index a short derived key.
- **Step results.** A Step result that carries such a value fails the attempt without retry. The Step's failure policy then applies. A recovery Step sees a value-too-large error type with the Attribute name in the detail.

If the SDK has no typed error for these, they surface as a generic service error. Do not branch on the detail text. Limits per backend are in the [Server reference](https://docs.superdurable.io/reference/server/search).

## Start identity and duplicate starts

### StartFlow decides repeats

StartFlow decides repeats itself, atomically per Flow ID: the reuse policy and Request ID tables in [Starting a Flow and ID reuse](../../SKILL.md#starting-a-flow-and-id-reuse) give the outcome of every start, including concurrent ones. Dex compares only the Request ID, not the input or options.

- Derive the Flow ID from the logical operation or resource.
- Give each logical start one stable Request ID, such as an operation ID the caller stores or one derived from the complete logical request. Keep it the same across retries of that start. A different logical start with the same Request ID, even one with an identical payload, is treated as a retry of the earlier start.
- Starts are atomic per Flow ID only. To serialize starts across Flow IDs, start them from one owner Flow.

A stable Request ID alone does not suppress AlreadyStarted; add the SDK's ignore-already-started option, and a retry of the latest run's start returns that run. The exact option names are in the [StartFlowOptions reference](https://docs.superdurable.io/reference/python/start-flow-options#flow-id-request-id-and-already-started).

Handle each result:

- **Success.** If the contract only acknowledges acceptance, return the accepted response from validated request fields and known initial values. Success does not mean the work finished or that state is still initial.
- **AlreadyStarted.** Keep it as a domain conflict unless the contract allows reuse. When the outcome needs existing business state, read it in this error branch with a Client Attribute read or a typed read-only RPC. Verify the request identity or invariant before you report success. Do not retry a proven conflict forever or report it as unavailability.
- **Unknown acceptance** (transport failure or timeout). Retry within a bounded policy with the same Flow ID, Request ID, and options. A replay-safe start needs no read before the retry. Under a policy that starts a new run after close, a retry that arrives after a newer run started counts as a new request. Without a stable Request ID, do not claim deduplication across calls. Report an unknown outcome when the contract cannot prove acceptance.

```text
StartFlow with stable identity -> return accepted response from request fields
  -> on a relevant error, reconcile through a typed read, or return conflict/unknown
```

### Wait for a required business milestone

When the response must confirm that an important operation finished, wait for it right after StartFlow succeeds or deduplicates. Use the Attribute-match wait or the Step-completion wait. A deduplicated start does not skip this wait. Add no read between the start and the wait.

Example: an API promises that a row is committed to a database that is the source of truth. The Flow writes it in Execute. Publish a committed Attribute only after the database commit, or complete the persistence Step only after it. Wait for that Attribute value or that Step, then return. Step completion does not return Step output and does not prove the Flow finished.

- Choose the smallest durable condition that proves the promise.
- If the contract only acknowledges acceptance, return after StartFlow with no wait.
- Bound the wait by the caller's deadline. A wait timeout does not undo the start and does not prove failure. Return a pending or unknown outcome. Never start a replacement.
- An Attribute wait needs an active Flow. If the Flow can close before the wait attaches, handle the wait error through authoritative business state.

```text
Acceptance-only API: StartFlow (or dedup) -> return accepted
Commit-confirming API: StartFlow (or dedup) -> wait for committed Attribute or Step completion
                       -> return confirmed
```

### Admission after a fast close

If an API must return the first admission decision and the Flow can close quickly, wait for the named admission Step or the Flow result. Then read accepted state from its owner with a Client Attribute read or a typed read-only RPC. An active-only mutation RPC cannot be the only source of truth after closure.

### SubFlow start rejection

When Dex rejects a SubFlow start before the child begins, the parent Step's failure carries the rejection detail. Keep it visible in the failure route or recovery record. Do not replace it with a generic timeout or retry outcome. A dispatch failure, such as a Worker disconnect, carries its text into the child result and the parent's recovery detail. Branch on the error type, not the text.

## Closed-Flow races

For an operation that needs an active target, a not-active error proves only that the interaction had no active target. It does not prove the requested work succeeded.

1. Reconcile first from already loaded domain state and operation invariants.
2. Call DescribeFlow only when the outcome depends on running versus completed versus missing, and nothing else tells you.
3. Skip the status call when every possible state leads to the same idempotent outcome.

API choice for a known Flow ID:

| Need | API |
| --- | --- |
| Current or latest run status, type, start time | DescribeFlow. It does not wait. A missing Flow returns Flow not found. |
| Business state | Client Attribute read or typed read-only RPC |
| Terminal result and output | WaitForFlow |
| Find Flow IDs | SearchFlows |

Which RPCs need an active Flow:

- An RPC with no Attribute locks, no transactional execution, and no returned durable effects runs on a snapshot. It can read a retained closed run. Success does not prove the Flow is active.
- A non-transactional RPC that returns durable effects runs its handler first. If the Flow closes in between, applying the effects fails as not active after the handler ran. Keep its external effects idempotent.
- A new invocation of a locked or transactional RPC needs an active run. Dex rejects it before any handler runs. A retained recorded request ID of such an RPC still replays its reply after close; see [RPCs](../../SKILL.md#rpcs).

### Terminal read RPC rule

Before you handle a not-active error from an RPC, check its registered options, invocation locks, and handler result. A business snapshot RPC with no locks, no transactional execution, and no durable effects must read the retained run directly. Closure alone is not a reason for it to fail.

Never add a `not-active -> WaitForFlow -> decode an earlier Step output` fallback to a business read. Execution history is not the entity's read contract. An intermediate Step output can omit later committed state.

```text
Wrong: snapshot RPC fails as not active -> WaitForFlow -> find a Step output in history
Right: register the snapshot RPC as query-only over retained Attributes
       -> call it directly before and after closure
       -> surface real read failures; ask for engine status separately if needed
```

Use WaitForFlow only when the caller needs the terminal status or completion output, or to reconcile an inactive mutation that business state cannot settle. Use history only for diagnosis and operator recovery.

A Client Attribute read also reads a retained closed run, without a Worker. Use it when the snapshot is only stored Attributes and exact AttributeMap instances. A missing Flow returns Flow not found, never the combined error.

### Missing query targets

At a business Get boundary that reads through a confirmed query-only RPC or a Client Attribute read, map the combined missing-or-inactive error, or Flow not found, directly to the contract's not-found result. A retained closed run is readable on this path, so the error means no readable run exists.

- Do not follow it with WaitForFlow, DescribeFlow, search, history, a short timeout probe, or a retry to tell missing from closed.
- The RPC call maps the Server's not-found sub-status to the combined type even for a query. The name does not make the query active-only.
- No readable run does not prove the entity never existed. Keep a retention or unavailable distinction when the contract needs one.
- Keep definition, value mapping, argument, Worker, and service errors visible. Do not convert every read error to not found.

This mapping applies only at a confirmed query-only boundary. Mutating, transactional, and locked RPCs can fail because a run is closed. Their not-active error needs the operation-specific reasoning above.

```text
Wrong: pure Get -> not-active error -> WaitForFlow with a short timeout -> decide missing
Right: pure Get -> missing/not-active error -> return not found
       closed retained Flow -> the same pure Get -> return its retained snapshot
```

### Flow close cleanup rejection

While a Flow closes, Dex rejects some new requests with the Flow closing error. The [reference](https://docs.superdurable.io/reference/server/runtime-client#requests-during-flow-close) lists them. Match the typed error, not its detail. The rejected request was not accepted. Wait for or read the terminal state before you decide whether another action is needed.

## Ambiguous mutations

After an ambiguous provider or Client mutation, reconcile authoritative state before you repeat an effect whose safe replay is not established. A StartFlow retry replays as described in [start identity](#start-identity-and-duplicate-starts). Bound retries and keep the repeated mutation idempotent.

By default, the SDK generates a new request ID for each RPC call, so a retried RPC is a new request and can run the handler again. Where the SDK accepts a caller request ID, pass the same stable ID on every attempt so the retried RPC's Flow changes take effect at most once; see [RPC request IDs](https://docs.superdurable.io/reference/server/runtime-data#rpc-request-ids). Make RPC handlers safe to repeat, or reconcile with a typed read before you retry a write. A failed handler that committed nothing records nothing. A caller timeout, a cancellation, or UNAVAILABLE leaves the outcome unknown: the handler may run again on retry while the first call may still commit.

## Best-effort output

When a Stream or progress write is explicitly best effort:

- Catch only service-backed failures, using the language's service-error type.
- Log sanitized identity and phase metadata, and continue the Flow.
- Let definition, validation, serialization, and programming failures surface.
- A RESOURCE_EXHAUSTED Stream write is retryable after a short delay when the node or the Stream is full. A single message larger than the Stream allows never fits; do not retry it.
- A FAILED_PRECONDITION Stream call means the operator disabled the Stream Store.
- Stream data is never the record of business completion.

## Recovery error types

A recovery path receives the final failure that selected it. The recovery paths are:

- an Execute that runs after exhausted WaitFor retries under the proceed-on-failure policy;
- a recovery Step chosen by an Execute failure route;
- the recovery Step of an exhausted Flow timeout handler.

Rules:

- A Worker failure keeps the error type the Worker reported, such as an application error type.
- A Dex time limit (method timeout, heartbeat timeout, retry total duration) uses an error type that each Dex backend may spell differently. Never compare the error type with a fixed string.
- Where the SDK offers timeout predicates on the recovery error, use them. A Worker API failure, such as an unreachable Worker, is not a method timeout.
- Where it does not, record the detail and error type for operators. Usually branch on error types the application or its provider libraries define, and treat unknown types as infrastructure failures.
- Give application error types names that cannot collide with Dex time-limit names.

### Attempt progress

The recovery error reports the furthest progress across all attempts of the failed method:

| Value | Meaning | Recovery may |
| --- | --- | --- |
| NOT_STARTED | No attempt ran the handler. | Repeat the side effect directly. |
| NOT_COMPLETED | Some attempt may have run; none completed. | Confirm the effect from authoritative state, or make the repeat idempotent, first. |
| UNSPECIFIED | Unknown, for example a failure recorded by an older Server. | Treat as NOT_COMPLETED. |

- Only SYNC methods can report NOT_STARTED.
- An ASYNC method always reports NOT_COMPLETED, because a node crash can roll back an ASYNC start whose handler may already have run. ASYNC recovery always reconciles first.
- NOT_COMPLETED is conservative. A Worker disconnect after hand-out reports it even when the handler never started, and so does a failure while reading the handler input.
- A Worker that stops gracefully and returns a task it did not run reports nothing, because that task consumed no retry.
- Keep a Step whose recovery relies on NOT_STARTED as SYNC.

## Diagnosis

Diagnosis is read-only. Capture the Flow ID, run ID, run attempt, Flow type, SDK version, Server address, Worker build, and the last user operation. Then:

1. Check application and Worker logs for registration, handler, connection, codec, and blob errors.
2. Inspect the Flow in Dex Web: active Steps, pending reasons, routing, and history. See [web.md](web.md).
3. Use `dexcli flow summary`, `state`, `history`, or `inspect` for a bounded JSON view. Use `--no-hydrate` when payloads are not needed or are sensitive.
4. Compare the active Step, Conditions, Attributes, pending Channels, Timers, and recent events with the intended graph.
5. When the cause is in Flow code, usually reproduce it with the smallest real-Server integration test.
6. Fix code or configuration before you decide whether an open run needs recovery.

| Symptom | Check |
| --- | --- |
| Step pending: waiting for a Worker | No Worker of the Flow type took it. Check Worker health, slot use, and scaling. |
| Step pending: waiting for a Worker that supports this Step type | Connected Workers lack that Step type. Deploy one that registers it. |
| Step pending: waiting for locks | Another Step or transactional RPC holds a lock. Find the holder and narrow lock scope. |
| RPC returns UNAVAILABLE | No Worker took it within the dispatch wait. |
| RPC returns FAILED_PRECONDITION naming the RPC | No connected Worker registers that RPC name. |
| Unknown Flow, Step, RPC, or resource | Compare the Worker registry and schema with the run's stable names. |
| Flow does not advance | Inspect WaitFor Conditions and pending Channel, Timer, or SubFlow state. |
| Repeated side effect | Inspect attempts and heartbeat checkpoints. Make Execute idempotent or add compensation. |
| Failure after Worker replacement | In-memory correctness state, incompatible codecs, removed Step types, or changed schemas. |
| Large value or hydration failure | Client and Worker blob cache configuration and matching codecs. |
| Lost progress in a UI | The UI treated a best-effort Stream as durable state. |
| Stale queue mutation | Reload pending messages. A consumed message ID cannot be reused. |

A Step with no Worker and no Flow timeout waits forever. A Flow timeout still ends it.

## Operator recovery

Do not stop, time travel, clone fork, publish, invoke, edit, delete, or skip a Timer during diagnosis. Act only when the user asks to change the Flow. Before each mutation:

- resolve the exact current run and run attempt;
- explain the expected state change;
- use an application RPC or one explicit dexcli operation, with its confirmation flag;
- re-inspect the Flow afterward.

CLI success fields such as `updated` or `skipped` acknowledge acceptance only. See [client.md](client.md#read-after-write) for what a later read proves.

Time travel (reset):

- Use it after a code fix, when replaying from a safe Step boundary will not duplicate an unprotected side effect.
- It keeps the run ID and creates a new run attempt.
- Keep the original Flow ID and operation identity. Reconcile provider objects and accepted writes first. A Step execution ID created after the point can name other work, and a random operation ID committed after the point is generated again; see [Time travel](../../SKILL.md#time-travel).
- It cannot invent a missing method boundary or rewrite the original start options. Decide whether replaying later writes is safe before you skip them.
- Which points are available and what it restores are in the [time travel reference](https://docs.superdurable.io/reference/server/runtime-client#time-travel).

Clone fork:

- It creates a new Flow, with a new Flow ID, from another run's state at a point.
- It copies state, not history. The new run gets the Attributes, pending Channel messages, Steps, and waits of that point, and starts a history of its own.
- It only reads the source. A running source keeps running.
- Use a distinct Flow ID for the fork; keep the new Flow ID, Request ID, and options stable across retries.
- Steps that were running at the point run again in the new Flow. Apply the same idempotency rules as for time travel, and decide per effect: key it by the new Flow ID when the fork must make its own effect; keep the source's key when the fork must not repeat an effect the source already made.

| Need | Use |
| --- | --- |
| Return the same Flow to an earlier point after a fix | Time travel |
| Keep the original Flow running and branch a new one | Clone fork |
| Try another path from a past point and keep the original | Clone fork |
| Start a Flow from fresh input with no source state | StartFlow |

The fork has its own Flow ID; whether it is a new business resource decides its effect keys, as above. When Workers pick Flows by task queue, such as for an agent session, give the fork its own task queue, and fork an agent session with the agent session fork, not a raw clone fork.

SubFlow reset policy, for time travel and clone fork: a restored or copied Step may wait on a SubFlow Condition, and the policy decides whether that wait attaches to the original child, restarts it, fails, or rejects the call. A restart repeats the child's start, so the child's external effects need idempotency keys. Pick the policy explicitly when the default does not fit; see [SubFlows across a time travel](https://docs.superdurable.io/reference/server/runtime-client#sub-flows-across-a-time-travel) and [across a clone fork](https://docs.superdurable.io/reference/server/runtime-client#sub-flows-across-a-clone-fork).

Details of the request, point types, copied state, and errors are in the [clone fork reference](https://docs.superdurable.io/reference/server/runtime-client#clone-fork).

Validate a fix against the real failed history when safe:

1. Deploy the Worker with the corrected code.
2. Time travel the failed Flow to the last safe point before the failure.
3. Let it replay and continue.
4. Re-inspect the new run attempt and its durable state.

Do not cross an external side effect unless it is idempotent, compensated, or explicitly safe to repeat. If no safe boundary exists, add an explicit recovery or compensation Step. Do not replace the business resource or Flow ID just to escape a failed history.

## Self-check

- Are typed conflicts handled before a generic remote-error fallback?
- Can an accepted mutation lose its response? Which authoritative fact reconciles it?
- Does start deduplication rely only on Dex identity?
- Does the normal start call StartFlow first and return without a preflight or a verification read?
- Do start retries keep the Request ID and the ignore-already-started option together?
- Does a milestone-confirming API wait for a condition published after the milestone commits, with a bounded wait?
- Does one owning Flow, not the API handler, usually start dependent Flows?
- Are retries bounded and idempotent?
- Do terminal snapshot reads use query-only RPCs or Client Attribute reads, with no history fallback?
- Does a query-only Get map missing or not-active directly to not found?
- Does best-effort output stay separate from business state?
- Does recovery that relies on NOT_STARTED run only on SYNC methods?
