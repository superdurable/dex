# Parallel patterns

Part of the [design patterns](../patterns.md), which hold the pseudocode conventions and the runnable examples.

## Parallel Steps

Derived from: [Joins](../../../SKILL.md#joins), [Cancellation](../../../SKILL.md#flow-timeout-completion-and-cancellation), [Snapshots](../../../SKILL.md#repeated-execution-snapshots-and-locks).

Parallel Steps are the default when branches share one Flow identity and lifecycle. Every shape fans out with GoToMany. Go folder: `parallel`.

### Static parallel Steps

Use when the branches are a fixed set of distinct Step types, each with its own responsibility and input type.

```text
AcceptOrder.execute -> goToMany(ChargePayment(order), ReserveStock(order))
ChargePayment.execute -> deadEnd() or gracefulComplete(...)
ReserveStock.execute  -> deadEnd() or gracefulComplete(...)
```

- Add a join only when the business outcome needs one.
- Usually give each fixed kind of work its own domain-named Step type, even when the branches look alike, rather than one Step type with a kind field and runtime fan-out. The graph then shows them, and each can have its own StepOptions and failure route.

### Dynamic parallel Steps

Use when the number of branches is known only at runtime and every branch runs the same Step type.

```text
SplitItems.execute(items) -> goToMany(for each item: ProcessItem(item))
ProcessItem.execute(item) -> do the work; deadEnd()
```

- Bound the fan-out by the [active Step executions limit](../../../SKILL.md#default-limits), which counts waiting Steps too. Batch when it can grow large, and split a very large fan-out across [Parallel SubFlows](#parallel-subflows) or top-level Flows.
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
- Each branch that ends while the Flow stays open reports exactly one result per round, on every such exit: success, early exit, and the failure recovery route. endBranch reports and ends the branch; reportBranchResult only reports.
- A branch of several Steps passes the token to its last Step, and only that Step reports. A branch never waits on the join's conditions. Exactly one join Step waits for each round; two waiters split the results.
- A ForAllBranches wait on an empty branch list is satisfied at once; ForFirstBranch and ForBranches on it fail ([Joins](../../../SKILL.md#joins)). Skip the join when nothing was selected and no continuation needs it. Forking more than once per method and forking from an RPC have their own rules in the [Join reference](https://docs.superdurable.io/reference/server/runtime-data#join).
- Canceling a branch produces no result, so a join that waits for every branch never fires unless the branch reported before the cancel. Cancel the join together with the branches, or wait `AnyOf(Completed.forAllBranches(round), Timer(deadline))` and handle the deadline in Execute.
- The Join's ChannelMap accessor reads a round's pending results, for example in a read-only RPC. Never publish to it directly.
- An exhausted branch without a failure route fails the Flow. When the operation must continue, route it to a recovery Step that reports a failed result so the join still fires.

### Partial join and reset

Derived from: [Joins](../../../SKILL.md#joins), [Cancellation](../../../SKILL.md#flow-timeout-completion-and-cancellation).

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

A cancelling join's round is never reset. Its canceled branches produce no result, so a forRemainingBranches wait for them would never finish. In a loop, the next round forks a new round directly; its round key keeps it apart from the old one.

- Each branch is its own Step execution, with its own retries, failure route, and durable result. In-process work retries as one method, so it fits operations that are repeat-safe and share one recovery boundary.
- In the cancelling variant, each branch handles its Step's cancellation signal as in [First-win parallel Steps](#first-win-parallel-steps).
- In a loop, reset every round whose other branches keep running. Without it, each round leaves its late results pending until the Flow closes.
- Sibling Steps are the active Steps of the named Step types that the same Step execution started. Fork starts the branches and the join together, so a sibling cancel selects exactly this round's branches, not the later Steps of a multi-Step branch. Steps that any invocation of one RPC started are all siblings, so a round that an RPC forked needs an explicit cancel by Step type; see the [Join reference](https://docs.superdurable.io/reference/server/runtime-data#join).

### First-win parallel Steps

Derived from: [Joins](../../../SKILL.md#joins), [Cancellation](../../../SKILL.md#flow-timeout-completion-and-cancellation), Must 1 ([Must](../../../SKILL.md#must)).

Use when the first acceptable result is enough.

```text
RequestQuotes.execute -> goToMany(FetchQuote(a), FetchQuote(b), FetchQuote(c))
FetchQuote.execute -> gracefulComplete(result).cancelSiblingSteps(FetchQuote)
```

- When branches make their own external effects or need their own retries, failure routes, or durable results, race them as Steps: GoToMany with a sibling cancel, or a Join. In-process concurrency inside one Execute, such as `Promise.any`, asyncio tasks, or threads, retries as one method, so it fits operations that are repeat-safe and share one recovery boundary.
- The sibling selector cancels executions of the same Step type started by the same parent, not unrelated branches.
- Make the winning result durable. Losers must be safe to cancel or harmless to finish, because cancellation is cooperative and undoes no external effect.
- Each loser watches its Step's cancellation signal and passes it to calls that accept it. A canceled loser runs no failure route, so release a reservation it already made from a durable branch keyed by the business ID. The language reference names the signal.

## Parallel SubFlows

Derived from: [SubFlows and child Flows](../../../SKILL.md#subflows-and-child-flows), [Conditions](../../../SKILL.md#conditions-and-channel-messages), [Default limits](../../../SKILL.md#default-limits).

Use these when the children need their own lifecycle, history, or retries, or when one fan-out is very large; see [subflow.md](../subflow.md#when-to-use-a-subflow). Go folder: `parallel-subflows`.

### Basic

One child per input; the parent waits for all.

```text
RunChildren.waitFor(requests) -> AllOf(for each request: SubFlow(ChildFlow, request))
RunChildren.execute -> gracefulComplete(results)
```

### Short-lived parent

Derived from: [Conditions](../../../SKILL.md#conditions-and-channel-messages), [Flow completion](../../../SKILL.md#flow-timeout-completion-and-cancellation), [RPCs](../../../SKILL.md#rpcs), [Locks](../../../SKILL.md#repeated-execution-snapshots-and-locks).

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

- Use the atomic complete-if-Channels-empty decision rather than a size check in application code. It counts a request that another HandleRequest consumed and whose Execute has not finished, so it covers the gap before that Step raises the counter ([ForceCompleteIfChannelsEmpty](../../../SKILL.md#flow-timeout-completion-and-cancellation)).
- The Steps that change the counter declare the same `admission` lock. Keep the RPC free of locks; it only reads the gate.
- The intake opens only when the first Step has queued the start batch. An RPC before that is rejected, so an empty start batch can complete at once. Check the start batch against the limit at the start boundary.
- The buffer limit is a soft limit: concurrent RPCs can read the same size, and the Channel size counts only queued requests. A hard limit needs atomic admission, such as a locked reservation.
- When the caller must confirm each request was handled, the consuming Step records the request's business ID in the same commit and the caller reads that record; see [Confirm that a message was handled](https://docs.superdurable.io/reference/server/runtime-client#confirm-message-handled).
- The runnable example is simplified: it has no intake gate and starts the workers even for an empty batch. Follow the skeleton above.

### Long-lived parent

Derived from: [Conditions](../../../SKILL.md#conditions-and-channel-messages), [Default limits](../../../SKILL.md#default-limits), [RPCs](../../../SKILL.md#rpcs), [Locks](../../../SKILL.md#repeated-execution-snapshots-and-locks).

The parent stays available to admit and coordinate children over time.

```text
OpenIntake.execute -> goToMany(HandleRequest x N, CloseIntake)
HandleRequest.waitFor -> AnyOf(Requests.ForOne(), GracefulCompletionRequested())
HandleRequest.execute                            // locks intakeClosed
    if no request was consumed: deadEnd()        // queue empty and the Flow is closing
    if intakeClosed: goTo(ReturnRequest, request)
    goTo(HandleChild, request)
ReturnRequest.execute(request)                   // no lock: reply keyed by the request ID
    send the returned request back to its submitter idempotently; goTo(HandleRequest)
HandleChild.waitFor(request) -> Until(SubFlow(ChildFlow, request))   // no lock
HandleChild.execute -> record the result; goTo(HandleRequest)
rpc sendRequest(request)                         // locks intakeClosed
    if request.id is already admitted: return true                   // business request ID
    if intakeClosed or Requests.size >= buffer limit: return false
    Requests.publish(request); record request.id as admitted; return true
rpc closeIntake() -> intakeClosed = true; CloseRequests.publish()   // locks intakeClosed
CloseIntake.waitFor -> Until(CloseRequests.ForOne())
CloseIntake.execute -> gracefulComplete()
```

- The N workers are interchangeable and each takes one message, so they share one Requests Channel; which worker gets a request depends on timing. See [several waiters on one Channel](../../../SKILL.md#conditions-and-channel-messages).
- The admission RPC checks the Channel size, which every RPC receives. Do not keep a separate waiting counter. A waiting list held in one Channel must fit the [pending messages limit](../../../SKILL.md#default-limits); split a longer list across Channels.
- The admission RPC deduplicates by the business request ID it records, because a submitter may retry with a new RPC request ID and a run keeps only its newest RPC request IDs; see [Default limits](../../../SKILL.md#default-limits). In a long-lived parent the record grows, so keep it in a [hash-partitioned AttributeMap](flow-shapes-and-state.md#hash-partitioned-attributemap) that the RPC locks and loads per instance, and prune it after a business retention window. A pruned ID can be admitted again, so the window outlasts the submitter's retry period.
- The worker's wait takes no lock: it consumes a request, and the later locked Execute decides whether to process or return it. A close must define which queued requests it finishes; this skeleton finishes admitted children and returns requests still queued. Each worker owns the request it consumed. Under the `intakeClosed` lock it only commits its next move: to `HandleChild`, which starts the child without the lock and finishes even after the close, or, after the close, to `ReturnRequest`, which sends the returned request back without the lock through an idempotent path keyed by the request. The RPCs take that lock and fail at once on a conflict, so the locked Execute calls no other system or Flow. A queued request is never silently dropped or handed back twice.
- The runnable long-lived parent examples show the basic variant: a later Step starts each child, and a stop RPC ends the workers. They have no closable intake, shared admission lock, admitted-ID record, or returned-request reply.
- After the close, Channel Conditions still win over the graceful completion Condition, so workers keep taking and returning queued requests. Once the queue is empty, idle workers wake through the graceful completion Condition and end with DeadEnd. Running children finish first, and the Flow completes when the last branch ends.
- An admission RPC that sees the intake closed rejects the request, so the caller can resubmit to a new parent run.
- Where the graceful completion Condition is unavailable (see the [Server reference](https://docs.superdurable.io/reference/server/step-and-flow-close#graceful-completion-condition)), the close Step publishes one message per worker to a Wake Channel, and each worker waits `AnyOf(Requests.ForOne(), Wake.ForOne())` and ends when it takes a Wake message.

### Wait for half

Derived from: [SubFlows and child Flows](../../../SKILL.md#subflows-and-child-flows), [Conditions](../../../SKILL.md#conditions-and-channel-messages), [Cancellation](../../../SKILL.md#flow-timeout-completion-and-cancellation).

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
- When the parent's result or a follow-up action depends on how each child ended, such as counting results, undoing side effects, or showing every outcome, read the child's terminal status and stored result after every stop, as in the skeleton, and record every child, finished or stopped, in one record keyed by its SubFlow ID. When the parent needs only the quorum, stop the remaining children and move on without the read. A child can finish at the same moment its stop message arrives, and a Channel Condition wins over a SubFlow Condition, so this read is what counts every child that finished. The runnable examples are simplified: they skip the read after a successful stop.
- With [top-level child Flows](../flow.md#child-jobs-with-their-own-identity), the quorum Step stops each child that has not reported and, when the parent needs its outcome, reconciles it by the rules there. Normal reports and reconciled outcomes go into one record keyed by the child Flow ID.

### Partitioning

Derived from: [Starting a Flow and ID reuse](../../../SKILL.md#starting-a-flow-and-id-reuse), [RPCs](../../../SKILL.md#rpcs).

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
- Changing the parent count or order remaps keys.

### Back pressure

Derived from: [Conditions](../../../SKILL.md#conditions-and-channel-messages), [Default limits](../../../SKILL.md#default-limits), [RPCs](../../../SKILL.md#rpcs), [Locks](../../../SKILL.md#repeated-execution-snapshots-and-locks), [Timers](../../../SKILL.md#timers), [Workers and time bounds](../../../SKILL.md#workers-attempts-and-time-bounds).

Separate admission from processing and make admission durable.

```text
// The Client starts one SubmitFlow per business request; flowId and Request ID derive from request.id.
SubmitRequest.waitFor(attempt) -> if attempt.round == 0: skipImmediately()
                                  else: Until(Timer(interval))
SubmitRequest.execute(attempt)
    accepted = invokeRPC(parentId, sendRequest, request)   // new RPC request ID per call, the SDK default
    if accepted: gracefulComplete(parentId)
    goTo(SubmitRequest, attempt.round + 1)                 // buffer full: a normal loop, not a retry
```

- The Client starts a short submit Flow instead of retrying an RPC in memory. Submissions survive submitter and Worker restarts.
- When the parent's RPC returns false (buffer full), the submit Step loops with a WaitFor Timer and a normal GoTo to itself. Returning an error instead spends the retry budget, 4 hours by default, and a parent that stays full longer fails the submit Flow and loses the submission; see [Workers and time bounds](../../../SKILL.md#workers-attempts-and-time-bounds).
- `sendRequest` holds a lock, so a repeated RPC request ID returns the recorded reply without running the handler. Each round therefore sends a new RPC request ID, and the parent deduplicates by the business request ID, as in [Long-lived parent](#long-lived-parent), so a retry after an unknown outcome is not admitted twice.
- An already-started error or a lock conflict from the parent loops the same way. Bound the loop with a round limit or the submit Flow's timeout when the business must give up.
- On acceptance, the submit Flow completes with the parent ID.
