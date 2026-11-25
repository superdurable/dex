# Design patterns

Choose the smallest tested Flow shape that meets the requirement. Prefer the nearest pattern below to an ad hoc coordination loop. Keep its Flow shape and replace the domain and integrations. Then read the selected language's pattern guidance and copy API usage from the runnable example.

Skeletons below are language-neutral pseudocode. `A.waitFor` and `A.execute` are the two methods of Step `A`.

## Runnable examples

Every pattern has a runnable example in each SDK:

| Language | Pattern root | Folder naming |
| --- | --- | --- |
| Go | [examples/go/patterns](https://github.com/superdurable/dex/tree/main/examples/go/patterns) | kebab-case |
| Python | [examples/python/dex_examples/patterns](https://github.com/superdurable/dex/tree/main/examples/python/dex_examples/patterns) | kebab-case |
| TypeScript | [examples/typescript/src/patterns](https://github.com/superdurable/dex/tree/main/examples/typescript/src/patterns) | kebab-case |
| Java | [examples/java/src/main/java/io/superdurable/dex/patterns](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/superdurable/dex/patterns) | lowercase, no separators |
| Rust | [examples/rust/src/patterns](https://github.com/superdurable/dex/tree/main/examples/rust/src/patterns) | snake_case |

Each section names its Go folder. The other languages use the same folder name in their own style. A few patterns live under `primitives/` or `products/`; the section says so. In Rust, Decision with Deadline (Deferred choice), Critical section, and Cancel with child Flows live under [examples/rust/src/control_patterns](https://github.com/superdurable/dex/tree/main/examples/rust/src/control_patterns).

## Choose a pattern

| Requirement | Pattern |
| --- | --- |
| Start one business operation from an API | [Root Flow admission](#root-flow-admission) |
| Run known, distinct branches in parallel | [Static parallel Steps](#static-parallel-steps) |
| Run one Step per input item | [Dynamic parallel Steps](#dynamic-parallel-steps) |
| Proceed only after every branch finishes | [Await parallel Steps](#await-parallel-steps) |
| Proceed after the first branch or n of m branches, optionally in rounds | [Partial join and reset](#partial-join-and-reset) |
| Keep the first result, cancel the rest | [First-win parallel Steps](#first-win-parallel-steps) |
| Child Flow runs with their own lifecycle, after the SubFlow gate | [Parallel SubFlows](#parallel-subflows) |
| Child jobs with their own business identity, looked up, retried, or stopped by their own number | [Top-level child Flows](flow.md#child-jobs-with-their-own-identity) |
| Wait for an external system to become ready or finish | [Polling](#polling) |
| Recurring work at a fixed interval | [Cron schedule](#cron-schedule) |
| Repeat until the recipient opts out | [Reminder](#reminder) |
| Act after a period without activity | [Idle Timer](#idle-timer) (Inactivity tracker) |
| Take inbound messages or replies until a resolution or inactivity | [Idle Timer with Resolution](#idle-timer-with-resolution) (Inbound event loop) |
| Recover when WaitFor fails | [WaitFor failure recovery](#waitfor-failure-recovery) |
| Compensate after Execute retries are exhausted | [Execute failure recovery](#execute-failure-recovery) |
| Stop retrying a provider error confirmed permanent | [Fail fast on permanent errors](#fail-fast-on-permanent-errors) |
| Escalate a failure to a person | [Manual recovery](#manual-recovery) |
| Handle an overall Flow deadline | [Graceful timeout](#graceful-timeout) |
| Finish only after internal producers' messages are handled | [Drain internal Channels](#drain-internal-channels) |
| Process bursty external messages in a short Flow | [Drain an external Channel](#drain-an-external-channel) |
| Stop long repeating work on request | [Stop Long-Running Work](#stop-long-running-work) (formerly Interruptible execution) |
| Route on whichever of several events happens first, such as a decision or its deadline | [Decision with Deadline](#decision-with-deadline) |
| Let parallel branches take turns on a sequence of Steps | [Critical section](#critical-section) |
| Stop child Flows when the case is canceled | [Cancel with child Flows](#cancel-with-child-flows) |
| Map a control structure to its Dex recipe | [Control structures](#control-structures) |
| Return to the caller at a durable milestone | [Responsive update](#responsive-update) |
| One Flow per entity, queried with SQL | [Entity Flow with Attribute Sync](#entity-flow-with-attribute-sync) (formerly Entity store / Entity Flow with SQL Projection) |
| One long-lived Flow per business entity, without SQL queries | The plain Entity Flow in [Entity Flow with Attribute Sync](#entity-flow-with-attribute-sync) |
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

Parallel Steps are the default when branches share one Flow identity and lifecycle. Every shape fans out with GoToMany. Do not turn a fixed graph into runtime fan-out only to shorten source code. Go folder: `parallel`.

### Static parallel Steps

Use when the branches are a fixed set of distinct Step types, each with its own responsibility and input type.

```text
AcceptOrder.execute -> goToMany(ChargePayment(order), ReserveStock(order))
ChargePayment.execute -> deadEnd() or gracefulComplete(...)
ReserveStock.execute  -> deadEnd() or gracefulComplete(...)
```

- Add a join only when the business outcome needs one.
- Give each fixed kind of work its own domain-named Step type, even when the branches look alike. Never run fixed kinds of work as one Step type with a kind field: the graph hides them, and they cannot have their own StepOptions or failure routes.

### Dynamic parallel Steps

Use when the number of branches is known only at runtime and every branch runs the same Step type.

```text
SplitItems.execute(items) -> goToMany(for each item: ProcessItem(item))
ProcessItem.execute(item) -> do the work; deadEnd()
```

- Bound the fan-out. Batch when it can grow large.
- Keep a durable correlation between each input item and its result.

### Await parallel Steps

Use when the Flow continues only after every branch finishes. Join the branches with a Join: a named ChannelMap of branch results that counts the branches and keys each round for you.

```text
Completed = Join<Result>("work-completed")        // register it with the Flow's Channels

SplitItems.execute(items) -> goToMany(Completed.fork(
                           for each item: round -> ProcessItem(item, round),
                           round -> AwaitAllItems(round)))
ProcessItem.execute(item, round) -> do the work; return Completed.endBranch(round, result)
AwaitAllItems.waitFor(round) -> Until(Completed.forAllBranches(round))
AwaitAllItems.execute(round) -> gracefulComplete(combine(Completed.branchResults(round)))
```

- Fork starts the branches and the join Step in one decision and gives all of them one round token: the round key and the branch count. The application keeps no counter and no key. Pass the token unchanged in Step inputs.
- Each branch reports exactly one result per round, on every exit path: success, early exit, and the failure recovery route. endBranch reports and ends the branch; reportBranchResult only reports.
- A branch of several Steps passes the token to its last Step, and only that Step reports. A branch never waits on the join's conditions. Exactly one join Step waits for each round; two waiters split the results.
- Fork a given Join at most once per Step method. For a second independent join, define a second Join or fork with an explicit round key. An RPC fork needs an explicit round key; see the [Join reference](https://docs.superdurable.io/reference/server/runtime-data#join).
- Do not join an empty branch list. Continue without a join when nothing was selected. A first-branch or n-branch wait on an empty round fails; see the [Join reference](https://docs.superdurable.io/reference/server/runtime-data#join).
- A canceled branch never reports, and its failure route does not run, so a join that waits for every branch never fires. Cancel the join together with the branches, or wait `AnyOf(Completed.forAllBranches(round), Timer(deadline))` and handle the deadline in Execute.
- The Join's ChannelMap accessor reads a round's pending results, for example in a read-only RPC. Never publish to it directly.
- Decide whether an exhausted branch fails the operation, or routes to a recovery Step that reports a failed result so the join still fires.

### Partial join and reset

Use when the Flow continues after the first branch or after n of m branches. With rounds, the next round's split Step resets the previous round before it forks again. Go folder: `workflow-patterns`, in [partial_joins.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/workflow-patterns/partial_joins.go); Rust keeps it in [examples/rust/src/workflow_patterns](https://github.com/superdurable/dex/tree/main/examples/rust/src/workflow_patterns).

```text
Split.waitFor(input)
    if input.previousRound is none: skipImmediately()
    else: Until(Quotes.forRemainingBranches(input.previousRound, input.n))   // the reset
Split.execute(input)
    if input.previousRound: record Quotes.remainingBranchResults(input.previousRound)
    goToMany(Quotes.fork(for each supplier: round -> RequestQuote(supplier, round),
                         round -> AcceptQuotes(input, round)))
RequestQuote.execute(supplier, round) -> return Quotes.endBranch(round, quote)
AcceptQuotes.waitFor(input, round) -> Until(Quotes.forBranches(round, input.n))   // forFirstBranch(round) when n is 1
AcceptQuotes.execute(input, round)
    accepted = Quotes.branchResults(round)
    another round: goToMany(PlaceOrder(accepted), Split(next input with previousRound = round))
    last round:    goTo(PlaceOrder(accepted))       // late results stay pending until the Flow closes
```

Cancelling variant: the join Step cancels the rest instead of waiting for them.

```text
AcceptBids.execute(n, round)
    goTo(ConfirmBooking(Bids.branchResults(round))).cancelSiblingSteps(BidFromA, BidFromB, BidFromC)
```

A cancelling join's round is never reset. Its canceled branches never report, so a forRemainingBranches wait for them would never finish. In a loop, the next round forks a new round directly; its round key keeps it apart from the old one.

- Each branch is its own Step execution. Never collect the first k of n results inside one Execute with in-process concurrency; each branch needs its own retries, failure route, and durable result.
- In the cancelling variant, each branch handles its Step's cancellation signal as in [First-win parallel Steps](#first-win-parallel-steps).
- In a loop, reset every round whose other branches keep running. Without it, each round leaves its late results pending until the Flow closes. Never reset a cancelling join's round: its canceled branches never report, so the reset would wait forever. Fork the next round directly.
- Sibling Steps are the Steps that one decision started together. Fork starts the branches and the join together, so a sibling cancel selects exactly this round's branches, not the later Steps of a multi-Step branch. A round that an RPC forked needs an explicit cancel by Step type; see the [Join reference](https://docs.superdurable.io/reference/server/runtime-data#join).

### First-win parallel Steps

Use when the first acceptable result is enough.

```text
RequestQuotes.execute -> goToMany(FetchQuote(a), FetchQuote(b), FetchQuote(c))
FetchQuote.execute -> gracefulComplete(result).cancelSiblingSteps(FetchQuote)
```

- Never race several external calls or business branches inside one Execute with in-process concurrency, such as `Promise.any`, asyncio tasks, or threads. Use GoToMany with a sibling cancel, or a Join, so each branch has its own retries, failure route, and durable result.
- The sibling selector cancels executions of the same Step type started by the same parent, not unrelated branches.
- Make the winning result durable. Losers must be safe to cancel or harmless to finish. Cancelling a local task does not reverse an external effect.
- Each loser watches its Step's cancellation signal. Pass it to calls that accept cancellation; otherwise use short call timeouts and check the signal between calls. Withdraw a reservation that already took effect with a separate, bounded, idempotent call. A reservation that must be released uses an expiring lease or a durable compensation branch keyed by the business ID. The language reference names the signal.

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
AdmitBatch.execute(requests)                     // locks admission
    if requests is empty: gracefulComplete()     // the intake never opened
    publish each to Requests; admission = { open: true, activeChildren: 0 }
    goToMany(HandleRequest x N)
HandleRequest.waitFor -> Until(Requests.ForOne())
HandleRequest.execute -> admission.activeChildren += 1 (lock admission); goTo(HandleChild, request)
HandleChild.waitFor(request) -> Until(SubFlow(ChildFlow, request))
HandleChild.execute -> admission.activeChildren -= 1 (lock admission)
    if activeChildren == 0: forceCompleteIfChannelsEmpty([Requests], else goTo(HandleRequest))
    else: goTo(HandleRequest)
rpc sendRequest(request)                         // reads admission; takes no lock
    if not admission.open or Requests.size >= buffer limit: return false
    Requests.publish(request); return true
```

- Use the atomic complete-if-Channels-empty decision. Never check the Channel size in application code before completing.
- The Steps that change the counter declare the same `admission` lock. Keep the RPC free of locks; it only reads the gate.
- The intake opens only when the first Step has queued the start batch. An RPC before that is rejected, so an empty start batch can complete at once. Check the start batch against the limit at the start boundary.
- The buffer limit is a soft limit: concurrent RPCs can read the same size, and the Channel size counts only queued requests. A hard limit needs atomic admission, such as a locked reservation.
- When the caller must confirm each request was handled, the consuming Step records the request's business ID in the same commit and the caller reads that record; see [Confirm that a message was handled](https://docs.superdurable.io/reference/server/runtime-client#confirm-message-handled).
- The runnable example is simplified: it has no intake gate and starts the workers even for an empty batch. Follow the skeleton above.

### Long-lived parent

The parent stays available to admit and coordinate children over time.

```text
OpenIntake.execute -> goToMany(HandleRequest x N, CloseIntake)
HandleRequest.waitFor -> AnyOf(Requests.ForOne(), GracefulCompletionRequested())
HandleRequest.execute                            // locks intakeClosed
    if no request was consumed: deadEnd()        // queue empty and the Flow is closing
    if intakeClosed: record the request as returned; goTo(HandleRequest)
    goTo(HandleChild, request)
HandleChild.waitFor(request) -> Until(SubFlow(ChildFlow, request))
HandleChild.execute -> record the result; goTo(HandleRequest)
rpc sendRequest(request)                         // locks intakeClosed
    if intakeClosed or Requests.size >= buffer limit: return false
    Requests.publish(request); return true
rpc closeIntake() -> intakeClosed = true; CloseRequests.publish()   // locks intakeClosed
CloseIntake.waitFor -> Until(CloseRequests.ForOne())
CloseIntake.execute -> gracefulComplete()
```

- The admission RPC checks the Channel size, which every RPC receives. Do not keep a separate waiting counter.
- Each worker owns the request it consumed. Under the `intakeClosed` lock it either commits the move to start the child, which then starts and finishes even after the close, or, after the close, records the request as returned through an idempotent path, such as a reply to its submitter. A queued request is never silently dropped or handed back twice.
- After the close, Channel Conditions still win over the graceful completion Condition, so workers keep taking and returning queued requests. Once the queue is empty, idle workers wake through the graceful completion Condition and end with DeadEnd. Running children finish first, and the Flow completes when the last branch ends.
- An admission RPC that sees the intake closed rejects the request, so the caller can resubmit to a new parent run.
- The graceful completion Condition needs SDK and Server support; see the [Server reference](https://docs.superdurable.io/reference/server/step-and-flow-close#graceful-completion-condition) and the language's Waits reference, such as [Go](https://docs.superdurable.io/reference/go/waits#graceful-completion-requests). Without it, the close Step publishes one message per worker to a Wake Channel, and each worker waits `AnyOf(Requests.ForOne(), Wake.ForOne())` and ends when it takes a Wake message.

### Wait for half

A quorum of children is enough; the rest are stopped.

```text
StartChildren.execute -> goToMany(WaitForQuorum(total), RunChild x N)
RunChild.waitFor(request) -> AnyOf(SubFlow(ChildFlow, request), AllDone.ForOne())
RunChild.execute -> if child finished: ChildCompleted.publish(); gracefulComplete()
                    else: stop own child by its SubFlow ID
                          stopped, or not found, not active, or closing:
                              read the child through the Client
                              closed: record its terminal status and stored result
                              running or closing: return an error so the Step retries
                              missing: record "unavailable" (business policy)
                          other stop or read errors: return them so the Step retries
                          gracefulComplete()
WaitForQuorum.waitFor(total) -> Until(ChildCompleted.ForN((total + 1) / 2))
WaitForQuorum.execute -> publish one AllDone message per unfinished child; gracefulComplete()
```

- The parent does not complete at the threshold. Every unfinished child Step stops its own child, then completes.
- Define what happens to children outside the quorum.
- When the parent's result or a follow-up action depends on how each child ended, such as counting results, undoing side effects, or showing every outcome, read the child's terminal status and stored result after every stop, as in the skeleton, and record every child, finished or stopped, in one record keyed by its SubFlow ID. When the parent needs only the quorum, stop the remaining children and move on without the read. A child can finish at the same moment its stop message arrives, and a Channel Condition wins over a SubFlow Condition, so this read is what counts every child that finished. The runnable examples are simplified: they skip the read after a successful stop.
- With [top-level child Flows](flow.md#child-jobs-with-their-own-identity), the quorum Step stops each child that has not reported and, when the parent needs its outcome, reconciles it by the rules there. Normal reports and reconciled outcomes go into one record keyed by the child Flow ID.

### Partitioning

Spread children across several parent Flow IDs to raise total concurrency.

```text
Submit.execute(request)
    parentId = parents[stableHash(request.key) % parentCount]
    try invokeRPC(parentId, sendRequest, request)
    on missing-or-inactive: startFlow(Parent, parentId, input = request,
                                      reuse = allow if not running)
The parent's first Step publishes its start input to Requests, so nothing is lost.
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
    execute retry            = a few attempts (for example 3-5) with backoff,
                               total duration = maximum wait
    heartbeat timeout        = the default, or the longest silent interval plus a margin
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
- Keep `interval + call timeout <= heartbeat timeout - 10 s`, against the heartbeat timeout the Step uses. Its default is in each SDK's [StepOptions reference](https://docs.superdurable.io/reference/go/step-options#defaults).
- Keep the default heartbeat timeout when the loop fits it, so a stalled Worker is detected quickly. Raise it only when a healthy call is silent longer.
- Within that bound, use a few seconds when a user watches something they just started, and tens of seconds for long jobs.
- A business deadline that is shorter than the whole wait, or that the loop's Step options cannot hold because the Step re-enters itself to commit each status change, is a watchdog: a waiting Step that races its Timer. The watchdog and the Step that records the result declare the same Attribute lock. When the Timer fires, the watchdog reads the status under that lock and declares the work overdue only while it is still unfinished.
- The retry policy covers Worker crashes and transient errors. Use a few attempts, for example 3 to 5, with backoff, and set the total duration to the maximum wait. The total duration bounds the time. Never use a large attempt count as a time bound.
- Use the language's plain sleep. Do not add cancellation-select machinery around it: the call has its own timeout, and after a canceled attempt the next call fails fast.
- The wait limits come from the Step options, and Dex enforces each one: the method timeout ends an attempt, and after a failure the total duration decides whether to schedule another attempt. Queueing can start a scheduled attempt late, and a running attempt is never cut short, so the whole wait can end well after the total duration. The Step code never computes a deadline, and neither the checkpoint nor the input carries one. A hard business deadline uses the watchdog below.
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
ValidateSchedule.execute(schedule) -> validate positive interval and run count, else forceFail
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

### Idle Timer

Act only after a period with no activity. Go folder: `inactiveness-tracker-timer`.

```text
Track.waitFor -> AnyOf(Timer(window), Activity.ForOne())
Track.execute -> if the Timer fired: goTo(ProcessInactivity)
                 else: goTo(Track)          // a fresh Timer after each activity
rpc recordActivity -> Activity.publish()
```

Keep the deadline in the Flow where the activity happens. A separate Flow that needs a message on every operation is the wrong boundary.

### Idle Timer with Resolution

Take inbound messages, such as replies in a conversation or ticket, until a resolution arrives or the conversation goes quiet. This extends the Idle Timer. Go folder: `inbound-event-loop`. Runnable examples: [Go](https://github.com/superdurable/dex/tree/main/examples/go/patterns/inbound-event-loop), [Python](https://github.com/superdurable/dex/tree/main/examples/python/dex_examples/patterns/inbound-event-loop), [Java](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/superdurable/dex/patterns/inboundeventloop), [TypeScript](https://github.com/superdurable/dex/tree/main/examples/typescript/src/patterns/inbound-event-loop), [Rust](https://github.com/superdurable/dex/tree/main/examples/rust/src/control_patterns/inbound_event_loop).

```text
rpc sendReply(text)       -> validate text; if invalid: return a rejection
                             Replies.publish(text); return accepted
rpc resolve(reason)       -> if reason is empty: return a rejection
                             Resolutions.publish(reason)

AwaitInbound.waitFor(pendingResolution)
    if pendingResolution: Until(Replies.AtLeast(1))         // drain before closing
    else: AnyOf(Replies.AtLeast(1), Resolutions.ForOne(), Timer(inactivity))
AwaitInbound.execute(pendingResolution)
    winner = the Condition that fired
    if winner is Replies:
        Transcript.set(Transcript.get() + Replies.conditionResults())
        if pendingResolution: goTo(Resolve, pendingResolution)
        goTo(AwaitInbound, none)   // a new execution of the same Step: a fresh Timer
    if winner is Resolutions:  goTo(Resolve, Resolutions.conditionResults()[0])
    if winner is the Timer:    goTo(CloseInactive)

Resolve.execute(reason)
    forceCompleteIfChannelsEmpty([Replies], else goTo(AwaitInbound, reason))
CloseInactive.execute      // a reply or resolution committed before the empty check reopens the wait
    forceCompleteIfChannelsEmpty([Replies, Resolutions], else goTo(AwaitInbound, none))
```

- The RPC only validates its input and publishes it to a Channel. It writes no Attribute that the waiting Step reads or locks.
- The waiting Step is the only writer of the transcript. It appends every message it consumed, then loops back to itself. The new execution registers a new wait, so the inactivity Timer restarts after each message.
- Execute asks which Condition won; it never infers the winner from an empty result. When several Conditions are ready at once, AnyOf picks in a fixed order: Timers first, then Channels in declaration order. So the Timer wins over a reply or resolution that arrives at the same moment. The closing Step's ForceCompleteIfChannelsEmpty catches those messages and returns to the waiting Step, which handles them. Express business priority between Channels by their declaration order.
- Declare the reply Channel before the resolution Channel in AnyOf. When several Channel Conditions are ready at once, the first one declared wins, and only the winner's messages are consumed. Replies accepted before the resolution are therefore always recorded first. If the order must differ, drain the pending replies before you resolve.
- A Channel Condition consumes messages only when it wins. A reply that arrives during Execute stays queued for the next execution, so no reply is lost.
- Use `AtLeast(1)` on the reply Channel to take every queued reply in one execution, or `ForOne` to take one at a time.
- Keep the transcript bounded. A long conversation stores its messages in a [sequentially chunked AttributeMap](#sequentially-chunked-attributemap).
- Every closing Step completes with ForceCompleteIfChannelsEmpty on the reply Channel, as in [Drain an external Channel](#drain-an-external-channel). A reply still queued, or taken by a Step that has not finished, at the empty check blocks the close and takes the fallback. The fallback returns to the waiting Step with the resolution in its input; that Step records the remaining replies and moves to Resolve again. The inactivity close also checks the resolution Channel, because the Timer wins over a resolution that is ready at the same moment; its fallback reopens the wait. Keep the waiting Step the only consumer of the reply Channel.
- The resolution RPC rejects an empty reason, because an empty pending resolution means "none".
- When the caller must confirm each reply was handled, the waiting Step records the reply's business ID in the same commit and the caller reads that record; see [Confirm that a message was handled](https://docs.superdurable.io/reference/server/runtime-client#confirm-message-handled).

Why not write the transcript from the RPC: the waiting Step must then lock the transcript, and the RPC must lock it too for its read-modify-write. Locks do not queue. Every RPC that arrives while the Step holds the lock, or while another RPC does, is rejected with a lock conflict, and the client must retry. Publishing to a Channel needs no lock, so concurrent replies never conflict.

## Failure handling

Dex already retries a failed method with backoff. Use these when retries alone are not enough. Compensation runs in reverse business order, is idempotent, and has its own failure route. Never catch an error only to report success.

### WaitFor failure recovery

A failure while establishing the wait is recoverable. Go folder: `primitives/proceed-on-wait-failure`.

```text
Lookup options: bounded WaitFor retry; WaitFor failure policy = proceed
Lookup.waitFor -> may fail (for example a check of Dex state or input; never an external call)
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

- A Step whose retries ran out may still have taken effect, for example when the provider applied the change and then timed out. Compensation starts with that Step, as `VoidPayment` does for `ChargePayment`: undo it with its original reference, and make every undo a no-op for an action that never happened.
- A recovery Step receives the failed Step's input unchanged, and gets the failure through the recovery error on the Context. Recovery Steps must be idempotent; they can be retried.
- Failing the Flow after compensation is a legitimate outcome.

### Fail fast on permanent errors

Stop retrying when a provider confirms that an error is permanent, such as a rejected input or a closed account.

- Execute classifies the provider error. A permanent error routes to a failure-handling Step or returns ForceFail. Only an error that can succeed on a later attempt goes to Step retry.
- If WaitFor must call a provider, it records a permanent error in a Step execution local value and returns SkipWaitImmediately; Execute routes on it. See [step.md](step.md#waitfor-versus-execute).
- Runnable examples: [Go](https://github.com/superdurable/dex/tree/main/examples/go/patterns/fail-fast), [Python](https://github.com/superdurable/dex/tree/main/examples/python/dex_examples/patterns/fail-fast), [Java](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/superdurable/dex/patterns/failfast), [TypeScript](https://github.com/superdurable/dex/tree/main/examples/typescript/src/patterns/fail-fast), [Rust](https://github.com/superdurable/dex/tree/main/examples/rust/src/control_patterns/fail_fast).

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
- Active Steps keep running while the handler runs, including Steps that wait on a Channel or a Timer. When the handler commits ForceComplete or ForceFail, Dex starts closing the Flow and cancels them. GracefulComplete, or a GoTo to a Step that completes gracefully, waits until every other active Step ends. The handler can commit after the deadline, because it waits for a Worker and may retry; to start closing when the Timer fires, use the fail or cancel timeout policy.

## Drain Channels

Choose by who publishes. Keep accepted messages durable and define when publication is rejected or redirected.

### Drain internal Channels

Producers and consumers are branches of one Flow; finish only after every published message is handled. Go folder: `drain-channels/internal-drain`.

```text
StartImport.execute -> goToMany(WriteRecords, ReadPages)
ReadPages.execute -> Commands.publish(page); goTo(ReadPages) ... then goTo(PublishEndMarker)
PublishEndMarker.execute -> Commands.publish(end marker); gracefulComplete()
WriteRecords.waitFor -> Until(Commands.ForOne())
WriteRecords.execute -> if end marker: gracefulComplete(report) else: write; goTo(WriteRecords)
```

- The Channel is FIFO, so the consumer sees the end marker only after every earlier message.
- With several producers, the end-marker Step waits for all of them before publishing it.

### Drain an external Channel

Bursty external messages; the Flow stays short and restarts on the next message. Go folder: `drain-channels/external-publishing`.

```text
HandleMessage.waitFor(message) -> if message is empty: Until(Queue.ForOne())
                                  else: SkipWaitImmediately
HandleMessage.execute -> handle the message
                         forceCompleteIfChannelsEmpty([Queue], else goTo(HandleMessage, empty))
rpc publishExternalMessage -> Queue.publish(message)

Controller(entityId, message):                   // flowId = entityId; message has a stable ID
    repeat a few times:
        try startFlow(flowId, input = message, reuse = allow if not running,
                      requestId = "start:" + message.id, ignore already started)
            return accepted
        on already started:                     // a different start request owns the active run
            try invokeRPC(flowId, publishExternalMessage, message,
                          requestId = "publish:" + message.id)   // where the SDK supports it
                return accepted
            on missing-or-inactive: continue    // that run closed; start again
        other errors: return them
    return unconfirmed with message.id          // outcome unknown; the caller sends it again
```

- A retried start returns the original run while that run is still the latest run for the Flow ID and its start request ID matches.

- "Accepted" means the run accepted the message, not that a Step handled it.
- One stable Flow ID per entity, one active run at a time, messages handled in the order their publications commit. An external business order needs its own ordering mechanism.
- The RPC only validates the message and publishes it to the Channel. It writes no Attribute that a Step reads or locks, such as a counter or a per-entity status. The consuming Step owns all state, so concurrent RPCs never hit a lock conflict.
- The decision checks the Channel atomically. A message already consumed by a Step that is still in Execute counts as not empty.
- Only one Step may consume a Channel that the decision checks.
- When the caller must confirm each message was handled, the consuming Step records the message's business ID in the same commit and the caller reads that record; see [Confirm that a message was handled](https://docs.superdurable.io/reference/server/runtime-client#confirm-message-handled).

## Stop Long-Running Work

Long repeating work that can be stopped by a durable signal. Go folder: `interruptible`.

StopFlow, graceful completion, and the Step cancellation signal are the built-in ways to stop work. Graceful completion wakes only the waits that include its Condition. The Attribute flag below lets each unit of work finish before it stops.

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

## Control structures

Map each control structure of a process to its Dex recipe. Each row states the rule that decides correctness; the sections below add skeletons for the last three recipes.

| Control structure | Dex recipe | Rules |
| --- | --- | --- |
| Sequence | Execute returns GoTo to the next Step | Each Step is one commit. Split Steps by recovery boundary, not by line of code. |
| Exclusive choice | Execute returns one GoTo, chosen from its input and Attributes | Execute picks exactly one branch. |
| Parallel split | GoToMany | Each branch is its own Step execution and runs concurrently. A branch ends with DeadEnd or a closing decision. |
| Fork and join | A Join's fork starts the branches and the join Step; each branch ends with endBranch; the join waits forAllBranches, forFirstBranch, or forBranches(n) | Every branch reports exactly one result on every exit path: success, early exit, and the failure recovery route. A canceled Step reports nothing and its failure route does not run, so a join that waits for every branch never fires. Cancel the join together with the branches, wait for the join and a Timer with AnyOf, or do not cancel branches the join counts. See [Await parallel Steps](#await-parallel-steps). |
| Several waiters for one event | One message per waiter | One message satisfies one wait. It is never copied to other waiting Steps. Use a ChannelMap with one key per waiter and publish to every key, or publish one message per waiter. |
| First result of internal work | GoToMany of one Step type; the first to finish returns its result with a decision that cancels its sibling Steps | The sibling selector cancels executions of the same Step type started by the same parent. Losers must be safe to cancel or harmless to finish; cancellation does not undo an external effect. |
| First external event wins | One Step waits AnyOf over one Channel per event and a Timer for the deadline | Only the winning Condition consumes. When several Conditions are ready at once, a ready Timer wins, then the first declared Channel. Execute checks which Condition fired and moves to one branch. |
| Mutual exclusion inside one Execute | An Execute lock on a shared Attribute | A lock covers one Step method. It never covers a WaitFor or several Steps. |
| Critical section across Steps | A Channel that holds one token message | A handoff Step takes the token with ForOne and does no protected work. The last Step of the section publishes the token back, and so does every other exit: the ExecuteFailure route, an early exit, and a WaitForFailure policy. Never cancel a Step that does protected work. |
| Cancel one task | An RPC result or a decision cancels Steps by Step type and starts the replacement Step | A canceled Step is not retried, its failure route does not run, and Dex discards its later writes. Dex picks the Steps to cancel at commit time. |
| Cancel the case and its child Flows | The Step that starts the SubFlow waits AnyOf(cancel Channel, SubFlow) and, on cancel, stops the child with Client StopFlow before ForceFail | List the cancel condition first in every wait. Every later waiting Step also waits for the cancel Channel. A closing parent never stops its children, so give every child its own Flow timeout. |
| Loop | Execute returns GoTo to the same or an earlier Step with the next input | Each iteration is a new Step execution and a new commit. Bound the loop. Keep accumulated results in Attributes, not in a growing Step input. Wait between iterations with a Timer in WaitFor. Poll an external system inside one Execute instead. |
| Repeat on a schedule | A scheduler Step waits for a Timer to the next occurrence, then GoToMany starts the run Step and the next scheduler wait together | Starting the next wait with the run keeps a slow run from drifting the schedule. Make each run's external effect idempotent per occurrence. Stop the schedule with a Channel message or a closing decision. |
| Multiple instances | GoToMany of one Step type, one execution per item | Bound the fan-out and batch large inputs. When the Flow needs the results, fork a Join with one branch per item. A Join needs the instance count when it forks; instances added while others run need an explicit count and a loop. Use child Flows only for instances with their own lifecycle. |

### Decision with Deadline

Wait for several events and route on whichever happens first, such as accept, reject, or expiry of an offer. Go folder: `deferred-choice`.

```text
rpc acceptCustomerOffer(note) -> OfferAccepted.publish(note)
rpc rejectCustomerOffer(note) -> OfferRejected.publish(note)

AwaitOfferResponse.waitFor -> AnyOf(OfferAccepted.ForOne(), OfferRejected.ForOne(), Timer(3 days))
AwaitOfferResponse.execute
    winner = the Condition that fired
    if winner is OfferAccepted: goTo(FulfillOffer)
    if winner is OfferRejected: goTo(CloseRejectedOffer)
    if winner is the Timer:     goTo(ExpireOffer)
```

- Only the winning Condition consumes. A losing response stays pending in its Channel.
- In Execute, check which Condition fired. Never treat "no Channel result" as "the deadline passed".
- When several Conditions are ready at once, AnyOf picks in a fixed order: a ready Timer first, then Channels in declaration order. Declare the decision Channels in business priority order.
- A late response cannot change the chosen branch. If its RPC is still accepted, it publishes a message that nothing consumes. Once the Flow starts closing, Dex may reject the RPC. A caller that needs a definite answer waits for the committed result, such as the Flow result, instead of relying on the RPC call.
- Test the deadline branch by skipping the Timer with the Client, not by shortening the business deadline.
- Anti-patterns: one parallel Step per event that cancels the others; a Timer loop that checks an Attribute; a separate Flow or job that enforces the deadline.

### Critical section

Let parallel branches of one Flow take turns on a sequence of Steps, such as several reviewers editing one contract. Go folder: `critical-section`.

```text
OpenContractEdits.execute(edits)
    Token.publish("contract")                      // exactly one token, before any branch waits on it
    goToMany(for each edit: Enter(edit), Join(count))

Enter.waitFor  -> Until(Token.ForOne())             // options: ExecuteFailure -> Release
Enter.execute  -> goTo(Edit, edit)                  // handoff only, no protected work
Edit.execute   -> first protected work; goTo(Save, edit)   // options: ExecuteFailure -> Release
                  early exit: Done.publish(record); Token.publish("contract"); deadEnd()
Save.execute   -> last protected work               // options: ExecuteFailure -> Release
                  Done.publish(record); Token.publish("contract"); deadEnd()
Release.execute -> Done.publish(failed record); Token.publish("contract"); deadEnd()
Join.waitFor(count) -> Until(Done.ForN(count))
```

- Use an Attribute lock when the protected work fits in one Execute. Use a token when it spans Steps or a WaitFor.
- The Step that takes the token is a pure handoff. Its Execute does no protected work and only moves to the first working Step.
- Every exit of the section publishes the token back:
  - the last Step of the section, on success;
  - an ExecuteFailure route on every Step of the section, including the handoff, to a Step that publishes the token;
  - a WaitForFailure policy that proceeds to Execute on any later Step of the section that has its own WaitFor; that Execute publishes the token;
  - any Execute that leaves the section early with a business decision.
- Canceling the handoff Step is safe because it does no protected work. When Dex cancels it after its wait consumed the token but before its Execute committed, Dex returns the token to the front of the Channel, and another branch enters.
- Never cancel a Step that does protected work. A canceled Step publishes nothing and its failure route does not run. The canceller cannot publish the token for it: Dex picks the cancel set at commit time, the Step may already have published it, and a second token breaks mutual exclusion. To stop a branch early, set a stop request, such as an Attribute from an RPC, that the section's Steps check; the Step that sees it leaves through an early exit that publishes the token.
- Dex cannot fence the external writes of a canceled method. Cancellation is cooperative, so a canceled Execute can keep writing after Dex returns its token. The section stays exclusive only if no Step that does protected work is canceled.
- Closing the Flow (ForceComplete, ForceFail, a Flow timeout, or Client StopFlow) ends every branch. No token is needed then.
- Publications commit with the method's result, or not at all. A failed attempt never publishes, so a retry cannot create a second token.
- Dex does not promise which waiting branch gets the token next.
- Use the token Channel for nothing else. Keep the token value meaningless and the protected state in Attributes. Keep the section short.
- Anti-patterns: protected work in the Step that waits for the token; a boolean Attribute that Steps check and set; returning the token only on success; canceling a Step that does protected work; publishing a replacement token while the old one may still exist.

### Cancel with child Flows

Stop the child Flows a Flow started when the Flow is canceled, such as a credit check started by a loan application. Go folder: `cancel-case`.

```text
rpc cancelApplication(reason) -> ApplicationCanceled.publish(reason)

RunCreditCheck.waitFor -> AnyOf(ApplicationCanceled.ForOne(),     // cancel first
                                SubFlow(CreditCheckFlow, request, own Flow timeout))
RunCreditCheck.execute
    if ApplicationCanceled has a result:
        client.stopFlow(subFlowID())   // not found, not active, or closing counts as stopped
        forceFail("application canceled: " + reason)
    goTo(AwaitUnderwriterDecision, subFlowResult().output)

AwaitUnderwriterDecision.waitFor -> AnyOf(ApplicationCanceled.ForOne(), UnderwriterDecided.ForOne())
AwaitUnderwriterDecision.execute -> if canceled: forceFail(...); else gracefulComplete(decision)
```

- A closing parent never stops its children. A child keeps running when its parent completes, fails, times out, or is stopped.
- The recipe works only on the cancel-Channel path. A Client StopFlow on the parent, the parent's Flow timeout, or a ForceFail or ForceComplete from another Step closes the parent and leaves the children running.
- Set a Flow timeout on every child, so a child that is never stopped still ends. Clients cancel a case through the cancel RPC, not StopFlow.
- Every later Step that waits also waits on the cancel Channel, so a cancel after the child finished still closes the case.
- List the cancel condition first in every wait. When the cancel message and another condition are ready at once, the first declared condition wins, so a pending cancel is never swallowed by a normal completion.
- Declaration order ranks Channels and SubFlows only. A ready Timer always wins over a ready Channel in AnyOf, so a fallback or deadline Timer can beat a cancel or withdrawal that arrives at the same moment. Declare business priority between the Channels, and make the Timer branch handle a cancel that may still be queued, for example by waiting on the cancel Channel in its next Step.
- A cancel that arrives after the last decision was consumed, or after the Flow closed, cannot change the outcome.
- Once the decision is final, close without consuming the cancel Channel. Do not close with ForceCompleteIfChannelsEmpty on it and fall back to a cancel path: a late cancel would then override the decision. A cancel accepted after the decision stays pending until the Flow closes.
- Treat not found, not active, and closing from StopFlow as already stopped. Execute may retry, and the child may finish at the same moment.
- Inject a Client provider through the Flow constructor. Record the stopped child Flow ID in an Attribute when operators or tests need it.
- One cancel message satisfies one wait. When several Steps each hold a child, each needs its own message: a ChannelMap key per Step, or one publish per Step.
- With several children, do not ForceFail from the first Step that sees a cancel message; that cancels the other Steps before they stop their children. Every Step that holds a child stops its own child before anything closes the Flow.

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

## Entity Flow with Attribute Sync

One Flow per entity, whose Attributes the Attribute Store syncs into a SQL table for queries. Go folder: `entity-store`.

The plain Entity Flow is the same shape without the SQL table: one long-lived Flow per entity, with a Flow ID from the entity ID, state in Attributes, changes through locked RPCs, and reads through the Client. Runnable examples: [Go](https://github.com/superdurable/dex/tree/main/examples/go/patterns/entity-flow), [Python](https://github.com/superdurable/dex/tree/main/examples/python/dex_examples/patterns/entity-flow), [Java](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/superdurable/dex/patterns/entityflow), [TypeScript](https://github.com/superdurable/dex/tree/main/examples/typescript/src/patterns/entity-flow), [Rust](https://github.com/superdurable/dex/tree/main/examples/rust/src/data_storage/entity_flow).

- Use when an entity needs durable state and RPC updates, and the application needs database queries over latest state.
- The Flow ID is the entity ID. It becomes the table's primary key, or its key columns with a row key template.
- Mark the projected Attributes for sync, and select the Attribute Store in the Flow configuration at start.
- Update through a typed RPC that validates, then writes every projected Attribute. Read authoritative state from the Flow.
- The SQL table is an asynchronous latest-state projection. Never make a Flow decision from it. Deleting an Attribute keeps the row and projects NULL.
- Define optimistic concurrency, deletion, retention, and open-Flow compatibility. See [attribute.md](attribute.md#attribute-store-sync).

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
state: Attribute { nextSequence, newestArchivedToken, oldestRetainedToken }
chunks: AttributeMap; instance "current" is the only mutable chunk

rpc appendRecord(record)       // locks state and chunks["current"]; loads chunks["current"]
    current.append(record)
    if current is full:
        chunks[zeroPadded(firstSequence)] = current     // archive, immutable
        chunks["current"] = empty
        state.newestArchivedToken = zeroPadded(firstSequence)
        bound the total:
            delete chunks[token] for each token past the count or age limit, by key,
                after archiving it elsewhere when the business must keep it
            state.oldestRetainedToken = oldest token still kept
    state.nextSequence += 1                             // all in one commit

rpc getPage(token)             // invocation option loads only chunks[token]
    if token is older than state.oldestRetainedToken: return page not found
    return records newest first, plus the next older token
           (no older token at state.oldestRetainedToken)
```

- For an append-only history, archive bounded chunks and load only the requested page.
- Bound the total as well, or storage keeps growing. Choose a limit by chunk count or age. Archive the full chunk, delete the chunks past the limit by key, after archiving them outside the Flow when the business must keep them, and record the oldest retained token in one commit. Size limits per value and per Flow are in the [Server configuration reference](https://docs.superdurable.io/reference/server/configuration#limits). Retained pages are immutable. At the oldest retained chunk, return no older token. A request for a pruned token returns a page-not-found result.
- The `current` token is always valid; an empty `current` returns an empty page.
- Archive tokens are fixed-width digits at a chunk boundary. A malformed or missing token is an explicit error.
- Retry only the rejected invocation after a lock conflict. A few writers share this lock on `current`.
- Updating old records needs another pattern or an index.
- Route frequent concurrent appenders through an RPC that publishes to a Channel consumed by one writing Step, as in the [Idle Timer with Resolution](#idle-timer-with-resolution), instead of having each RPC lock and rewrite `current`.

### Hash-partitioned AttributeMap

Go folder: `hash-partitioned-attribute-map`.

```text
// Persisted layout: changing PARTITION_COUNT moves keys to other partitions.
const PARTITION_COUNT = 16

partition(key):
    canonical = trim and lowercase key; reject empty or invalid input
    return "partition-" + format(versionedHash(canonical bytes) % PARTITION_COUNT)

rpc upsertProfile(profile)     // invocation options: lock and load chunks[partition(profile.email)]
    bucket = load that instance, or an empty dictionary
    bucket[canonical email] = profile
    write the instance back

rpc getProfile(email)          // invocation options: load only chunks[partition(email)]
    return bucket[canonical email] or not found
```

- Define the partition count as one named constant with a code comment that it is persisted layout and that changing it moves keys to other partitions. Changing it needs a complete rehash or a new Flow version.
- Every SDK and service must use the identical canonicalization and hash.
- A collision only shares a bucket, because the bucket is a dictionary keyed by the full canonical key.
- Every writer uses the same instance-lock rule. Locks are cooperative.
- Retry an idempotent upsert after a lock conflict.
- For a map-wide invariant, use the singleton coordination lock in [attribute.md](attribute.md#whole-map-coordination).

## Durable AI agents

Use when Dex owns an agent's conversation, model calls, tools, approvals, and long waits. Example: `products/ai-agent` in Go, Python, and TypeScript.

The released dex-agents packages implement this shape. When you use them, check their message retention and paging defaults against the history the product must keep and show. See [message retention and paging in the agents reference](https://docs.superdurable.io/reference/agents/session-limits#message-retention).

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
- The resolution Step reloads committed messages and pending calls, keeps every committed message and result, clears approval and Timer state, and marks the turn stopped. It is idempotent.
- [ ] When a turn stops, the closing Step writes a canceled tool result for every requested call that has no result yet, including calls that never started, so the next model input has no dangling tool call.
- The Stop RPC and every Step that writes turn state declare the same turn-state lock.

### Long waits and the browser

- A durable wait tool moves to a Step whose WaitFor returns a Timer. Never keep a model call, coroutine, or Worker blocked for the delay.
- To allow interruption, race the Timer against the steered Channel. If the message wins, persist an interrupted tool result, consume the message, and replan.
- Expose one read-only snapshot RPC for the browser. It loads the conversation map and both pending Channels and returns history, description, queued, and steered messages together. Refresh it after mutations and live events, on focus or reconnect, and with a slow fallback poll. Use DescribeFlow when the response needs execution status.

### Verify

Context rebuild after Worker replacement, compaction before deletion, approval after page reload, retry exhaustion, Stream loss, Timer firing, user interruption, a configuration change that waits for the next turn, and Stop during a model call, a Timer, a non-idempotent tool, and racing a model Step commit.

- [ ] After every Stop, each requested tool call has a result, a canceled one for calls that never started or never finished, so the next model input has no dangling tool call.

## Self-check

- Does the graph expose every success, wait, retry exhaustion, cancellation, timeout, and terminal path?
- Is every runtime-sized fan-out bounded?
- Are losing or abandoned branches safe?
- Are side effects idempotent or compensated?
- Is progress classified as durable or best effort?
- Can a replacement Worker resume from durable state alone?
- Is every external wait a Polling Step, never a Timer loop or a retry loop?
