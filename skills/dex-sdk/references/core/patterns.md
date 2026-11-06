# Design patterns

Choose the smallest tested Flow shape that meets the requirement. Prefer the nearest pattern below to an ad hoc coordination loop. Keep its Flow shape and replace the domain and integrations. Then read the selected language's pattern guidance and copy API usage from the runnable example.

Skeletons below are language-neutral pseudocode. `A.waitFor` and `A.execute` are the two methods of Step `A`.

## Runnable examples

Every pattern has a runnable example in each SDK, on the default branch:

| Language | Pattern root | Folder naming |
| --- | --- | --- |
| Go | [examples/go/patterns](https://github.com/superdurable/dex/tree/main/examples/go/patterns) | kebab-case |
| Python | [examples/python/dex_examples/patterns](https://github.com/superdurable/dex/tree/main/examples/python/dex_examples/patterns) | kebab-case |
| TypeScript | [examples/typescript/src/patterns](https://github.com/superdurable/dex/tree/main/examples/typescript/src/patterns) | kebab-case |
| Java | [examples/java/src/main/java/io/superdurable/dex/patterns](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/superdurable/dex/patterns) | lowercase, no separators |
| Rust | [examples/rust/src/patterns](https://github.com/superdurable/dex/tree/main/examples/rust/src/patterns) | snake_case |

Each section names its Go folder. The other languages use the same folder name in their own style. A few patterns live under `primitives/` or `products/`; the section says so.

## Choose a pattern

| Requirement | Pattern |
| --- | --- |
| Start one business operation from an API | [Root Flow admission](#root-flow-admission) |
| Run known, distinct branches in parallel | [Static parallel Steps](#static-parallel-steps) |
| Run one Step per input item | [Dynamic parallel Steps](#dynamic-parallel-steps) |
| Proceed only after every branch finishes | [Await parallel Steps](#await-parallel-steps) |
| Keep the first result, cancel the rest | [First-win parallel Steps](#first-win-parallel-steps) |
| Child executions with their own lifecycle, after the SubFlow gate | [Parallel SubFlows](#parallel-subflows) |
| Wait for an external system to become ready or finish | [Polling](#polling) |
| Recurring work at a fixed interval | [Cron schedule](#cron-schedule) |
| Repeat until the recipient opts out | [Reminder](#reminder) |
| Act after a period without activity | [Inactivity tracker](#inactivity-tracker) |
| Recover when WaitFor fails | [WaitFor failure recovery](#waitfor-failure-recovery) |
| Compensate after Execute retries are exhausted | [Execute failure recovery](#execute-failure-recovery) |
| Escalate a failure to a person | [Manual recovery](#manual-recovery) |
| Handle an overall Flow deadline | [Graceful timeout](#graceful-timeout) |
| Finish only after internal producers' messages are handled | [Drain internal Channels](#drain-internal-channels) |
| Process bursty external messages in a short Flow | [Drain an external Channel](#drain-an-external-channel) |
| Stop long repeating work on request | [Interruptible execution](#interruptible-execution) |
| Return to the caller at a durable milestone | [Responsive update](#responsive-update) |
| One Flow per entity, queried with SQL | [Entity store](#entity-store) |
| Append-only history that grows | [Sequentially chunked AttributeMap](#sequentially-chunked-attributemap) |
| Keyed records with direct lookup | [Hash-partitioned AttributeMap](#hash-partitioned-attributemap) |
| Durable AI agent | [Durable AI agents](#durable-ai-agents) |

Use Channels for durable commands, Attributes for durable state, and Streams for best-effort progress.

## Root Flow admission

Start one domain-named root Flow directly from the application boundary.

- Use a stable Flow ID for the logical operation, a Request ID derived from the complete start request, and an explicit ID reuse policy.
- A retry of the same request reuses both identities. The same Flow ID with a different Request ID is a conflict, unless the domain intentionally routes commands to one coordinator.
- Never add a table, row, outbox, lease, lock, cache, or admission projection to deduplicate or serialize starts. Store request fingerprints only when they are real fields of the owning domain record.
- When the caller needs durable acceptance before completion, wait for the named admission Step, then read the domain record.

Full rules: [error-handling.md](error-handling.md#start-identity-and-duplicate-starts).

## Parallel Steps

Parallel Steps are the default when branches share one Flow identity and lifecycle. All four shapes fan out with GoToMany. Do not turn a fixed graph into runtime fan-out only to shorten source code. Go folder: `parallel`.

### Static parallel Steps

Use when the branches are a fixed set of distinct Step types, each with its own responsibility and input type.

```text
Init.execute -> goToMany(ChargePayment(order), ReserveStock(order))
ChargePayment.execute -> deadEnd() or gracefulComplete(...)
ReserveStock.execute  -> deadEnd() or gracefulComplete(...)
```

Add a join only when the business outcome needs one.

### Dynamic parallel Steps

Use when the number of branches is known only at runtime and every branch runs the same Step type.

```text
Init.execute(items) -> goToMany(for each item: DoWork(item))
DoWork.execute(item) -> do the work; deadEnd()
```

- Bound the fan-out. Batch when it can grow large.
- Keep a durable correlation between each input item and its result.

### Await parallel Steps

Use when the Flow continues only after every branch finishes.

```text
Init.execute(items) -> goToMany(AwaitAll(count), for each item: DoWork(item))
DoWork.execute(item) -> do the work; Completed.publish(result); deadEnd()
AwaitAll.waitFor(count) -> Until(Completed.ForN(count))
AwaitAll.execute -> gracefulComplete(combine(results))
```

- Start the join together with the workers. Channel messages are durable, so completions published before the join waits still count.
- Each worker publishes exactly one completion.
- Decide whether an exhausted branch fails the operation, or routes to recovery while the join still waits for the rest.

### First-win parallel Steps

Use when the first acceptable result is enough.

```text
Init.execute -> goToMany(DoWork(a), DoWork(b), DoWork(c))
DoWork.execute -> gracefulComplete(result).cancelSiblingSteps(DoWork)
```

- The sibling selector cancels executions of the same Step type started by the same parent, not unrelated branches.
- Make the winning result durable. Losers must be safe to cancel or harmless to finish. Cancelling a local task does not reverse an external effect.

## Parallel SubFlows

Apply these only after a SubFlow passes the [evolution gate](subflow.md#evolution-gate). Define parent completion, child reuse, cancellation, duplicate submission, and concurrency explicitly. Go folder: `parallel-subflows`.

### Basic

One child per input; the parent waits for all.

```text
RunChildren.waitFor(requests) -> AllOf(for each request: SubFlow(ChildFlow, request))
RunChildren.execute -> gracefulComplete(results)
```

### Short-lived parent

Drain an admitted batch, then complete without racing new requests.

```text
Init.execute(requests) -> publish each to Requests; activeChildren = 0
                          goToMany(HandleRequest x N)
HandleRequest.waitFor -> Until(Requests.ForOne())
HandleRequest.execute -> activeChildren += 1 (lock activeChildren); goTo(HandleChild, request)
HandleChild.waitFor(request) -> Until(SubFlow(ChildFlow, request))
HandleChild.execute -> activeChildren -= 1 (same lock)
    if activeChildren == 0: forceCompleteIfChannelsEmpty([Requests], else goTo(HandleRequest))
    else: goTo(HandleRequest)
rpc sendRequest(request) -> if Requests.size >= buffer limit: return false
                            Requests.publish(request); return true
```

- Use the atomic complete-if-Channels-empty decision. Never check the Channel size in application code before completing.
- Both Steps that change the counter declare the same lock.

### Long-lived parent

The parent stays available to admit and coordinate children over time.

```text
HandleRequest.waitFor -> Until(Requests.ForOne())
HandleRequest.execute -> goTo(HandleChild, request)
HandleChild.waitFor(request) -> Until(SubFlow(ChildFlow, request))
HandleChild.execute -> if stopped: gracefulComplete() else goTo(HandleRequest)
rpc sendRequest(request) -> size check for back pressure; publish
rpc stop() -> stopped = true
```

### Wait for half

A quorum of children is enough; the rest are stopped.

```text
Init.execute -> goToMany(WaitForQuorum(total), RunChild x N)
RunChild.waitFor(request) -> AnyOf(SubFlow(ChildFlow, request), AllDone.ForOne())
RunChild.execute -> if child finished: ChildCompleted.publish(); gracefulComplete()
                    else: stop own child by its SubFlow ID; gracefulComplete()
WaitForQuorum.waitFor(total) -> Until(ChildCompleted.ForN((total + 1) / 2))
WaitForQuorum.execute -> publish one AllDone message per unfinished child; gracefulComplete()
```

- The parent does not complete at the threshold. Every unfinished child Step stops its own child, then completes.
- Define what happens to children outside the quorum.

### Partitioning

Spread children across several parent Flow IDs to raise total concurrency.

```text
Submit.execute(request)
    parentId = parents[stableHash(request.key) % parentCount]
    try invokeRPC(parentId, sendRequest, request)
    on missing-or-inactive: startFlow(Parent, parentId, input = request,
                                      reuse = allow if not running)
Parent.Init publishes its start input to Requests, so nothing is lost.
```

- The same key always reaches the same parent. Each parent bounds its own buffer and concurrency.
- A lost start race makes the start fail; the Step retries and then reaches the RPC.
- Changing the parent count or order remaps keys. Use a versioned parent set or consistent hashing when affinity matters.

### Back pressure

Separate admission from processing and make admission durable.

- The Client starts a short submit Flow instead of retrying an RPC in memory.
- When the parent's RPC returns false (buffer full), the submit Step returns an error so its retry policy backs off durably. An already-started error is handled the same way.
- On acceptance, the submit Flow completes with the parent ID.
- Submissions survive submitter and Worker restarts.

## Polling

Use this one pattern whenever a Flow waits for an external system to become ready or finish: a cloud build, a rollout, a provisioning job, a third-party status. Business signals inside Dex (Channels, RPCs) and business timers (TTL, reminders, inactivity) still use WaitFor; they are not polling. Go folder: `polling`.

One long-running Execute owns the whole wait:

```text
StartJob.execute(request)
    jobId = provider.startJob(idempotencyKey = flowId, timeout = short)
    return goTo(AwaitJob, jobId)

AwaitJob options:
    execute method timeout   = maximum wait
    execute retry            = a few attempts with backoff, total duration = maximum wait
    heartbeat timeout        = short, so a stalled Worker is detected quickly
    execute failure route    = RecordJobWaitFailure

AwaitJob.execute(context, jobId)
    lastReported = context.lastHeartbeatValue()
    loop:
        status = provider.getStatus(jobId, timeout = short)   // error -> return error; retry policy re-runs
        if status is succeeded: return gracefulComplete(result)
        if status is failed:    return forceFail(reason)      // a definite business failure
        if status != lastReported: JobProgress.write(status); lastReported = status
        else: context.recordHeartbeat(lastReported)
        sleep(interval)

RecordJobWaitFailure.execute(context)
    read context.recoveryError() (detail, error type); record the business failure
```

- "Not ready yet" is a normal loop branch, never an error.
- Keep `interval + call timeout` comfortably below the heartbeat timeout. Use a few seconds when a user watches something they just started, and tens of seconds for long jobs.
- Use the language's plain sleep. Do not add cancellation-select machinery around it: the call has its own timeout, and after a canceled attempt the next call fails fast.
- Every wait limit comes from the Step options, and Dex enforces each one. The Step code never computes a deadline, and neither the checkpoint nor the input carries one.
- The checkpoint is only resume state, such as the last reported status.
- A business condition that is not a fixed duration, such as a lease the Flow does not own expiring, is still checked inside the loop.
- Write Stream frames only on status changes. Consumers recover from Attributes or a read-only RPC.
- Do not hold Execute locks for the whole wait. Commit locked results in a short following Step.
- The recovery error does not carry the last status. The Stream and logs do. To branch on which limit ended the wait, follow [recovery error types](error-handling.md#recovery-error-types).
- Either durability works; the checkpoint makes ASYNC replays cheap.

Never:

- WaitFor a Timer and GoTo the same Step to poll again.
- Use retry policy, backoff, or RetryAfter as the polling loop by returning "not ready" as an error.
- Run a long loop that never heartbeats or writes progress.
- Call an external system without its own timeout.
- Hand-write a deadline that duplicates the method timeout or the retry total duration.
- Pass a page token to a new execution to iterate.

## Durable timers

A Timer is a durable Condition, not an in-process timer. Define skip, cancellation, and late-delivery behavior.

### Cron schedule

Fixed-interval recurring work in one durable Flow. It is not calendar cron. Go folder: `cron`.

```text
Init.execute(schedule) -> validate positive interval and run count, else forceFail
                          goTo(WaitForSchedule, state)
WaitForSchedule.waitFor -> AnyOf(Timer(interval), TriggerNow.ForOne(), SkipNext.ForOne())
WaitForSchedule.execute
    if SkipNext consumed: next occurrence without running (gracefulComplete if it was the last)
    else if last occurrence: goTo(Run)
    else: goToMany(Run(occurrence), WaitForSchedule(next))
Run.execute -> do the work idempotently; deadEnd() (gracefulComplete on the last run)
rpc triggerNow -> TriggerNow.publish()
```

- Start the next WaitForSchedule together with Run, so a slow run does not drift the schedule.
- Make each run's external effect idempotent per occurrence.

### Reminder

Send reminders until the recipient opts out. Go folder: `reminders`.

```text
Remind.waitFor -> AnyOf(Timer(interval), OptOut.ForOne())
Remind.execute -> if the Timer did not fire: gracefulComplete()
                  else: send the reminder; goTo(Remind)
rpc optOutReminders -> OptOut.publish()
```

### Inactivity tracker

Act only after a period with no activity. Go folder: `inactiveness-tracker-timer`.

```text
Track.waitFor -> AnyOf(Timer(window), Activity.ForOne())
Track.execute -> if the Timer fired: goTo(ProcessInactivity)
                 else: goTo(Track)          // a fresh Timer after each activity
rpc recordActivity -> Activity.publish()
```

Keep the deadline in the Flow where the activity happens. A separate Flow that needs a message on every operation is the wrong boundary.

## Failure handling

Dex already retries a failed method with backoff. Use these when retries alone are not enough. Compensation runs in reverse business order, is idempotent, and has its own failure route. Never catch an error only to report success.

### WaitFor failure recovery

A failure while establishing the wait is recoverable. Go folder: `primitives/proceed-on-wait-failure`.

```text
Lookup options: bounded WaitFor retry; WaitFor failure policy = proceed
Lookup.waitFor -> may fail (for example a lookup error)
Lookup.execute(context, input)
    if context.waitForMethodFailed(): goTo(Recover, input)    // read context.recoveryError()
    else: normal path
```

Dex does not route directly to another Step. It runs the same Step's Execute with unchanged input, and Execute decides.

### Execute failure recovery

Compensate after Execute retries are exhausted. Go folder: `recovery`.

```text
UpdateQuantity.execute -> goTo(ChargePayment)
ChargePayment options: execute retry; execute failure route = VoidPayment
VoidPayment.execute    -> compensate; goTo(RestoreQuantity)       // reads context.recoveryError()
RestoreQuantity.execute -> compensate; forceFail("payment failed")
```

- Recovery Steps get the same input, or the input the route passes, and must be idempotent; they can be retried.
- Failing the Flow after compensation is a legitimate outcome.

### Manual recovery

Escalate an exhausted failure to a person. Go folder: `intervention`.

```text
DoWork options: bounded execute retry; execute failure route = AwaitOperator
AwaitOperator.waitFor -> AnyOf(Retry.ForOne(), Skip.ForOne())
AwaitOperator.execute -> if Retry consumed: goTo(DoWork)
                         else: forceFail("skipped by operator")
rpc retry -> Retry.publish();  rpc skip -> Skip.publish()
```

Persist the failure so the operator can see it, and make the decision durable through the Channel.

### Graceful timeout

Handle the overall Flow deadline with application logic. Go folder: `timeout`.

- The deadline belongs to the Flow, not a competing Step. Start the Flow with a positive timeout and the handler policy, and implement the Flow's timeout handler.
- The handler has Execute semantics and its own options. On success, Dex applies its staged writes, deletions, publications, and decision. If it exhausts its retries, the Flow fails, or it routes to a no-input recovery Step.
- Record a business outcome and perform bounded cleanup. See [flow.md](flow.md#timeout-handler).

## Drain Channels

Choose by who publishes. Keep accepted messages durable and define when publication is rejected or redirected.

### Drain internal Channels

Producers and consumers are branches of one Flow; finish only after every published message is handled. Go folder: `drain-channels/internal-drain`.

```text
Init.execute -> goToMany(Consume, Produce)
Produce.execute -> Commands.publish(command); goTo(Produce) ... then goTo(Finalize)
Finalize.execute -> Commands.publish(final sentinel); gracefulComplete()
Consume.waitFor -> Until(Commands.ForOne())
Consume.execute -> if sentinel: gracefulComplete() else: handle; goTo(Consume)
```

- The Channel is FIFO, so the consumer sees the sentinel only after every earlier message.
- With several producers, Finalize waits for all of them before publishing the sentinel.

### Drain an external Channel

Bursty external messages; the Flow stays short and restarts on the next message. Go folder: `drain-channels/external-publishing`.

```text
Process.waitFor(message) -> if message is empty: Until(Queue.ForOne())
                            else: SkipWaitImmediately
Process.execute -> handle the message
                   forceCompleteIfChannelsEmpty([Queue], else goTo(Process, empty))
rpc publishExternalMessage -> Queue.publish(message)

Controller:
    try invokeRPC(flowId, publishExternalMessage, message)
    on missing-or-inactive: startFlow(flowId, input = message)
    other errors: return them
```

- The decision checks the Channel atomically. A message already consumed by a Step that is still in Execute counts as not empty.
- Only one Step may consume a Channel that the decision checks.

## Interruptible execution

Long repeating work that can be stopped by a durable signal. Go folder: `interruptible`.

```text
Work.waitFor(progress) -> Until(Timer(gap))
Work.execute(progress) -> if interruptSignal == cancel: gracefulComplete()
                          if done: gracefulComplete()
                          do one unit; goTo(Work, progress + 1)
rpc interrupt -> interruptSignal = cancel
```

- Write the interruption intent to an Attribute through an RPC. Each branch finishes its current unit and checks the signal before the next one.
- Persist the outcome and make cleanup explicit. Cancelling a local future does not reverse an external effect.
- To stop now and cancel running Steps, use RPC cancellation. See [rpc.md](rpc.md#cancel-running-steps-from-an-rpc).

## Responsive update

Return at the boundary the caller needs while the Flow keeps owning durable work. Set the request timeout at the HTTP or admission boundary.

### Step completion

The caller needs a durable milestone before the background work ends. Go folder: `wait-for-step-completion`.

```text
PersistRecord.execute -> commit the record; set Attribute; goTo(UpdateExternalSystem)
Controller: startFlow -> waitForStepCompletion(PersistRecord) -> read through an RPC or Client Attribute read
```

Choose a Step only after it committed the state that makes the response valid.

### Attribute match

The caller refreshes after durable business state changes. Example: `products/job-post`.

```text
revision: integer Attribute, initialized to 0
rpc updatePosting(change)        // locks revisionLock
    apply change; revision = revision + 1
Caller:
    seen = waitForAttributeMatch(revision > lastSeen)
    read canonical state with a Client Attribute read or a read RPC
    lastSeen = seen
```

- Every Step and RPC that advances the state declares the same lock and increments the revision in the same invocation.
- The revision is a watermark, not an event stream. A wait may skip intermediate values.
- The wait sees only that Attribute, not run IDs or history.

### Stream

The caller shows incremental progress. Example: `primitives/stream`.

The Flow writes to a Stream. The caller reads forward with a resume token and passes the new token to the next read. Messages are best effort, not a final result. Use a Step-completion or Attribute-match wait for a durable condition.

## Entity store

One Flow per entity, with a SQL projection for queries. Go folder: `entity-store`.

- Use when an entity needs durable state and RPC updates, and the application needs database queries over latest state.
- The Flow ID is the entity ID. It becomes the table's primary key, or its key columns with a row key template.
- Mark the projected Attributes for sync, and select the Attribute Store in the Flow configuration at start.
- Update through a typed RPC that validates, then writes every projected Attribute. Read authoritative state from the Flow.
- The SQL table is an asynchronous latest-state projection. Never make a Flow decision from it. Deleting an Attribute keeps the row and projects NULL.
- Define optimistic concurrency, deletion, retention, and open-Flow compatibility. See [external-attribute-sync.md](external-attribute-sync.md).

## Partitioned AttributeMap state

A single Attribute that grows rewrites its whole value on every write. Split it into bounded AttributeMap instances.

| | Sequentially chunked | Hash-partitioned |
| --- | --- | --- |
| Fits | append-only ordered history: messages, audit events, subscriber history | keyed records with direct lookup and update: profiles, account metadata |
| Contention | every append contends on the active chunk | independent per-partition locks; hot keys make hot partitions |
| Old data | archived chunks are immutable | any record can be updated |
| Fixed layout | chunk size | partition count and hash algorithm |

Neither creates server-side map pagination or a secondary index. The layout is persisted schema: changing it needs a migration or a new Flow version.

### Sequentially chunked AttributeMap

Go folder: `sequentially-chunked-attribute-map`.

```text
state: Attribute { nextSequence, newestArchivedToken }
chunks: AttributeMap; instance "current" is the only mutable chunk

rpc appendRecord(record)       // locks state and chunks["current"]; loads chunks["current"]
    current.append(record)
    if current is full:
        chunks[zeroPadded(firstSequence)] = current     // archive, immutable
        chunks["current"] = empty
        state.newestArchivedToken = zeroPadded(firstSequence)
    state.nextSequence += 1                             // all in one commit

rpc getPage(token)             // invocation option loads only chunks[token]
    return records newest first, plus the next older token
```

- The `current` token is always valid; an empty `current` returns an empty page.
- Archive tokens are fixed-width digits at a chunk boundary. A malformed or missing token is an explicit error.
- Retry only the rejected invocation after a lock conflict.
- Updating old records needs another pattern or an index.

### Hash-partitioned AttributeMap

Go folder: `hash-partitioned-attribute-map`.

```text
partition(key):
    canonical = trim and lowercase key; reject empty or invalid input
    return "partition-" + format(versionedHash(canonical bytes) % partitionCount)

rpc upsertProfile(profile)     // invocation options: lock and load chunks[partition(profile.email)]
    bucket = load that instance, or an empty dictionary
    bucket[canonical email] = profile
    write the instance back

rpc getProfile(email)          // invocation options: load only chunks[partition(email)]
    return bucket[canonical email] or not found
```

- Every SDK and service must use the identical canonicalization and hash.
- A collision only shares a bucket, because the bucket is a dictionary keyed by the full canonical key.
- Every writer uses the same instance-lock rule. Locks are cooperative.
- Retry an idempotent upsert after a lock conflict.
- For a map-wide invariant, use the singleton coordination lock in [attribute.md](attribute.md#whole-map-coordination).

## Durable AI agents

Use when Dex owns an agent's conversation, model calls, tools, approvals, and long waits. Example: `products/ai-agent` in Go, Python, and TypeScript.

### Own the model context in Dex

- Persist provider-neutral user, assistant, tool-call, and tool-result messages. Rebuild the model input for every call from a configured system prompt, a cumulative summary, and recent messages. Do not depend on a provider conversation ID.
- Store messages in a [sequentially chunked AttributeMap](#sequentially-chunked-attributemap). Keep the next sequence, retained range, summarized range, pending calls, and status in a small Attribute. Read known chunk keys; never enumerate the map to paginate.
- Compaction changes the model context, not the durable truth. Commit a summary that covers an explicit sequence range, then delete only whole archived chunks that the summary covers.
- Keep per-session configuration, such as model choice, effort, or tool policy, in its own Attribute, changed through a typed RPC. The first Step of a turn copies it into the turn state, so a change applies at the next turn. Store references such as a model ID or connection name, never secrets.

### Plans and output

- Keep a short plan in a regular Attribute that is replaced as one value. Derive plan completion from task states, separately from Flow activity.
- For planning-only interactions, hide executable tools rather than relying on a prompt. Bind plan approval to the current plan revision through a Channel.
- Commit completed assistant messages and tool results to Attributes. Stream token deltas, tool progress, and status. The UI recovers from durable state.
- Use separate buffered writers for provider reasoning summaries and visible text, and a third Stream for structured events. Show a Thinking surface only for an explicit provider reasoning-summary event. Never expose hidden reasoning or relabel ordinary text.
- Persist opaque encrypted reasoning items that a provider requires beside the assistant message, and replay them without interpreting them.
- Raise the heartbeat timeout for model calls that can stay silent; see [step.md](step.md#heartbeats-and-checkpoints).

### Tools

- Persist the assistant tool call before executing it. Give every call a stable ID, and run calls in a deliberate order with bounded concurrency.
- Read-only or idempotent tools may retry. Non-idempotent writes default to one attempt unless duplicates are acceptable. Exhausted retries become an explicit failure result or recovery Step.
- Pass the stable call ID to integrations that support idempotency. Surface an unknown outcome when a timeout cannot prove the effect.
- After exhausted retries, read the attempt progress: NOT_STARTED proves the tool never ran. Keep a tool Step with external effects SYNC when its recovery relies on that. See [error-handling.md](error-handling.md#attempt-progress).
- Never accept executable MCP commands, remote URLs, or credentials from untrusted Flow input. Register trusted servers in Worker configuration and let the Flow select from that list.

### Approvals and questions

- Use a ChannelMap keyed by tool-call ID for approvals. Store the pending request in an Attribute so the UI can reload it.
- Unknown, destructive, or write tools wait for approval unless trusted policy says otherwise.
- When the model needs a user reply, use a durable input tool: persist the question in an Attribute and wait on the user Channel. Offer known choices; allow free text when answers are not enumerable.

### Queued and steered messages

- Use one Channel for queued user messages and another for steered messages.
- While the agent loop runs, leave queued messages pending so the user can inspect, edit, delete, or steer them. Consume them in FIFO order once the agent reaches its input wait.
- Steer is a transactional RPC that loads the queued Channel, finds the selected message ID, and moves it to the steered Channel. A missing ID is a stale view.
- Apply steered messages at safe Step boundaries. Steering never abandons a started effect. Before the next model call, tool call, approval continuation, or Timer continuation, drain steered messages, write cancellation results for planned calls that never started, clear stale approval or Timer state, and let the model replan.
- Only steered messages and Stop interrupt a pending approval or Timer.

### Stop a turn with canceling steps

A user's Stop ends the current turn now, so it may cancel running Steps; steering may not.

- Implement Stop as a transactional RPC that records the stop request, bound to the turn ID, in the turn state Attribute. Its result cancels Step types and schedules one Step that resolves the turn. See [rpc.md](rpc.md#cancel-running-steps-from-an-rpc).
- Cancel only Step types with no external effect or that only wait: model calls, Timer waits, approval or input waits.
- Never cancel a Step type that runs a non-idempotent tool. That tool runs to the end and commits. Its Step reads the stop request when it finishes and routes to turn resolution.
- A model Step can commit between the RPC's snapshot and its commit. So every Step of the loop checks the stop request at its boundary and stops starting new work.
- The resolution Step reloads committed messages and pending calls, keeps every committed message and result, writes a canceled result for each call without one, clears approval and Timer state, and marks the turn stopped. It is idempotent.
- The Stop RPC and every Step that writes turn state declare the same turn-state lock.

### Long waits and the browser

- A durable wait tool moves to a Step whose WaitFor returns a Timer. Never keep a model call, coroutine, or Worker blocked for the delay.
- To allow interruption, race the Timer against the steered Channel. If the message wins, persist an interrupted tool result, consume the message, and replan.
- Expose one read-only snapshot RPC for the browser. It loads the conversation map and both pending Channels and returns history, description, queued, and steered messages together. Refresh it after mutations and live events, on focus or reconnect, and with a slow fallback poll. Use DescribeFlow when the response needs execution status.

### Verify

Context rebuild after Worker replacement, compaction before deletion, approval after page reload, retry exhaustion, Stream loss, Timer firing, user interruption, a configuration change that waits for the next turn, and Stop during a model call, a Timer, a non-idempotent tool, and racing a model Step commit.

## Self-check

- Does the graph expose every success, wait, retry exhaustion, cancellation, timeout, and terminal path?
- Is every runtime-sized fan-out bounded?
- Are losing or abandoned branches safe?
- Are side effects idempotent or compensated?
- Is progress classified as durable or best effort?
- Can a replacement Worker resume from durable state alone?
- Is every external wait a Polling Step, never a Timer loop or a retry loop?
