# RPC

An RPC is a typed, synchronous handler that a Flow declares. A Client invokes it on a Flow ID, and a Worker runs it against that Flow's state.

## When to use an RPC

| Need | Use |
| --- | --- |
| Write Flow-owned Attributes, AttributeMaps, Channels, or ChannelMaps from outside | RPC. It is the only application write path. |
| Read that needs Flow code, pending Channel messages, or a whole AttributeMap | Read-only RPC |
| Read stored Attributes or exact AttributeMap instances | [Client Attribute read](client.md#read-attributes-from-outside-a-flow); no Worker needed |
| Enqueue work without synchronous handling | Publish to a Channel through an RPC, and let a Step consume it |
| Wait for state to change | Client Attribute-match or Step-completion wait, not a polling RPC |

Usually name an RPC with a concrete action verb and its subject, such as `getQueuedMessages` or `moveQueuedMessageToPrioritizedMessages`; see [Names](../../SKILL.md#suggestions). The RPC name is durable for open Flows.

## Commit boundary

- Each RPC invocation is a commit boundary. If it fails, none of its staged Attribute writes or publications commit.
- External API calls in a handler are outside that boundary.
- An RPC result can return output, update or delete Attributes, publish, delete, or move pending messages, start new Steps, and cancel running Steps.
- An RPC has no WaitFor. It never waits for a Condition.
- When the business ranks events against each other, each event's RPC validates, records its candidate, and answers "received"; an arbitration Step decides. Lock order is commit order, not business priority. See [Arbitrate by business priority](patterns/control-structures.md#arbitrate-by-business-priority).

## Execution modes

| Mode | Selected by | Runs on | Needs an active Flow |
| --- | --- | --- | --- |
| Snapshot (query-only) | no locks, not transactional, no durable effects returned | a snapshot of the current or retained run | No. A retained closed run can serve it. |
| Non-transactional with effects | returns durable effects, no locks | a snapshot; effects applied after the handler | Yes for the effects. If the Flow closes first, the effects fail as not active after the handler ran. |
| Transactional | Attribute locks, or the explicit transactional option | the handler and its effects commit together | Yes for a new invocation. Dex rejects it before the handler runs. A retained recorded request ID replays its reply even after close; see [RPCs](../../SKILL.md#rpcs). |

- Every mode runs the handler on a snapshot. Steps and other RPCs can commit between the snapshot and the RPC's commit, and transactional mode does not prevent that. Only a shared Attribute lock excludes a Step or RPC that declares it.
- Success of a snapshot RPC does not prove the Flow is active. Use DescribeFlow when the response needs status.
- A read-only RPC returns application state. It does not add a terminal status that the application did not persist.
- A non-transactional handler can run and then lose its effects. Keep its external calls idempotent. A not-active error does not prove the handler never ran.
- Select transactional execution when an RPC must validate a pending message ID and commit its deletion with other writes.
- A lock conflict fails the RPC at once; Dex does not queue it. See [locks](attribute.md#locks) for retrying or returning a busy result.

## Options

Keep registration options and invocation options separate.

| | Registration options | Invocation options |
| --- | --- | --- |
| Set | once, when the Flow declares the RPC | per call, from request data |
| Own | timeout, singleton Attribute locks, transactional mode, whole-map loads, singleton Channel loads | only additions: exact AttributeMap instance locks, exact AttributeMap instance loads, exact ChannelMap instance loads |
| Can remove registered settings | n/a | never |

- The SDK merges, sorts, and deduplicates the two sets.
- The Client validates invocation selections before any network call. Every definition must belong to the RPC's Flow. Every dynamic lock targets an AttributeMap instance. Every instance name is nonempty and has no `/`.
- A dynamic read-modify-write adds the same instance as a lock and as a load. A read needs only the load.
- The RPC timeout bounds one Worker handler invocation. It is fixed at registration. The caller's context can only stop waiting sooner.
- Reading unselected AttributeMap or pending-message state raises a not-loaded error. A loaded empty collection is not an error.

## Run ID

Without a Run ID, the Server resolves the current run and pins it for the whole call, so an ordinary RPC usually omits it and needs no search for one first. Pass one when the application must target an exact run across Flow ID reuse.

## Read models

- Separate calls can read different commits. So a page that needs conversation Attributes, a description, and pending queues from one state usually gets one read-only snapshot RPC that loads all of them. Split it when parts refresh at different rates or a large queue pages on its own.
- When a view needs only stored Attributes and exact map instances, one Client Attribute read call serves it.

## Cancel running Steps from an RPC

An RPC result can cancel active Steps of its Flow, selected by Step type across the whole Flow. An RPC has no parent Step, so it cannot select siblings.

- Dex commits Attribute writes and publications, then applies cancellation, then queues the RPC's next Steps. The RPC's own next Steps are never canceled.
- A canceled Step is not retried and its failure route does not run. Dex discards its later decision and writes.
- The cancel set is chosen at commit time, not from the handler's snapshot. A selected Step may already have finished, and a new Step of that type may have started and gets canceled. Dex does not reject the RPC in either case.
- Attribute locks do not close this race. They serialize only with methods that declare the same lock.
- The handler cannot know which Steps the cancel hit. When the outcome depends on whether a canceled Step finished, decide in a follow-up Step that reads committed state.
- Step code must honor cancellation. A handler that ignores it keeps running and its external calls still take effect. Keep external effects idempotent.

```text
rpc stopTurn(turnId)                   // transactional, locks turnState
    turnState.stopRequestedFor = turnId
    return result(ok)
        .cancelSteps(CallModelStep, WaitForApprovalStep)
        .goTo(ResolveStoppedTurnStep, turnId)

ResolveStoppedTurnStep.execute         // locks turnState
    reload committed messages and pending calls
    record planned calls that were never admitted as canceled; leave admitted calls to their commit Step
    mark the turn stopped once no call is in flight; idempotent
```

A commit Step that schedules a tool checks the stop request and marks the call admitted in the same locked commit, so no tool call is admitted after Stop commits; calls admitted earlier may still start and must be resolved. See [Durable AI agents](patterns/ai-agents.md#stop-a-turn-with-canceling-steps).

## Retries and request identity

- Request ID behavior is in [RPCs](../../SKILL.md#rpcs): by default each call gets a new one, so a retry is a new request. When one logical call must apply at most once, pass the same stable ID, such as an event ID, on every attempt where the SDK accepts a caller request ID. A transactional or locking RPC answers a repeated request ID with its recorded reply, including a refusal it returned, such as `false`, and even after the run closes while it is retained. Another RPC runs its handler again; when it returns durable effects (Flow changes or recorded events), the commit returns the recorded reply, and when it returns no Flow changes and records no event, it returns a fresh output and nothing is recorded. A business retry after a refusal, such as offering again later, is a new logical call with a new request ID. See [RPC request IDs](https://docs.superdurable.io/reference/server/runtime-data#rpc-request-ids).
- Make handlers safe to repeat, or reconcile with a typed read before you retry a write.
- A failed handler that committed nothing records nothing, so a retry runs it again. A caller timeout, a cancellation, or UNAVAILABLE leaves the outcome unknown: the first call may still commit while a retry runs the handler again.
- An RPC that no Worker takes within the dispatch wait fails with UNAVAILABLE. An RPC name that no connected Worker registers fails with FAILED_PRECONDITION naming the RPC. RPCs never wait in the Step task queue.

## Self-check

- Is every external write of Flow state an RPC, and every RPC name a verb phrase?
- Does every RPC that read-modify-writes a value a concurrent Step also read-modify-writes share that Step's lock, or publish to the Step instead?
- When the caller must get a definite answer about a race with a deciding Step, does the RPC take the same lock as that Step?
- Does every dynamic read-modify-write add the instance as both lock and load?
- Is every snapshot RPC that must work after closure free of locks, transactions, and durable effects?
- Are RPC handlers safe to repeat?
- When an outcome depends on whether a canceled Step finished, is it decided in a follow-up Step, not in the RPC handler?
