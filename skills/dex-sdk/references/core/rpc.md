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

Name every RPC with a concrete action verb and its subject, such as `getQueuedMessages` or `moveQueuedMessageToPrioritizedMessages`. Never use a noun-only or generic name. The RPC name is durable for open Flows.

## Commit boundary

- Each RPC invocation is a commit boundary. If it fails, none of its staged Attribute writes or publications commit.
- External API calls in a handler are outside that boundary.
- An RPC result can return output, update or delete Attributes, publish, delete, or move pending messages, start new Steps, and cancel running Steps.
- An RPC has no WaitFor. It never waits for a Condition.

## Execution modes

| Mode | Selected by | Runs on | Needs an active Flow |
| --- | --- | --- | --- |
| Snapshot (query-only) | no locks, not transactional, no durable effects returned | a snapshot of the current or retained run | No. A retained closed run can serve it. |
| Non-transactional with effects | returns durable effects, no locks | a snapshot; effects applied after the handler | Yes for the effects. If the Flow closes first, the effects fail as not active after the handler ran. |
| Transactional | Attribute locks, or the explicit transactional option, or a Server policy | the handler and its effects commit together | Yes. Dex rejects it before the handler runs. |

- Every mode runs the handler on a snapshot. Steps and other RPCs can commit between the snapshot and the RPC's commit, and transactional mode does not prevent that. Only a shared Attribute lock excludes a Step or RPC that declares it.
- Success of a snapshot RPC does not prove the Flow is active. Use DescribeFlow when the response needs status.
- A read-only RPC returns application state. It does not add a terminal status that the application did not persist.
- A non-transactional handler can run and then lose its effects. Keep its external calls idempotent. A not-active error does not prove the handler never ran.
- Select transactional execution when an RPC must validate a pending message ID and commit its deletion with other writes.
- Lock conflicts are explicit contention, not queuing. Retry the rejected invocation with bounded backoff.

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

Do not search for a Run ID before an ordinary RPC. Omit the optional Run ID so the Server resolves and pins the current run for that call. Pass one only when the application must target an exact run across Flow ID reuse.

## Read models

- One page that needs conversation Attributes, a description, and pending queues gets one read-only snapshot RPC that loads all of them. Do not issue several separately timed requests.
- When a view needs only stored Attributes and exact map instances, one Client Attribute read call serves it.
- Never issue sequential Client reads to build one response.
- Keep separate views as separate RPCs. Do not combine unrelated read models only to save calls.

## Cancel running Steps from an RPC

An RPC result can cancel active Steps of its Flow, selected by Step type across the whole Flow. An RPC has no parent Step, so it cannot select siblings.

- Dex commits Attribute writes and publications, then applies cancellation, then queues the RPC's next Steps. The RPC's own next Steps are never canceled.
- A canceled Step is not retried and its failure route does not run. Dex discards its later decision and writes.
- The cancel set is chosen at commit time, not from the handler's snapshot. A selected Step may already have finished, and a new Step of that type may have started and gets canceled. Dex does not reject the RPC in either case.
- Attribute locks do not close this race. They serialize only with methods that declare the same lock.
- Do not decide the outcome in the handler. Start a follow-up Step that reads committed state and decides.
- Step code must honor cancellation. A handler that ignores it keeps running and its external calls still take effect. Keep external effects idempotent.

```text
rpc stopTurn(turnId)                   // transactional, locks turnState
    turnState.stopRequestedFor = turnId
    return result(ok)
        .cancelSteps(CallModelStep, WaitForApprovalStep)
        .goTo(ResolveStoppedTurnStep, turnId)

ResolveStoppedTurnStep.execute         // locks turnState
    reload committed messages and pending calls
    write a canceled result for every call without one
    mark the turn stopped; idempotent
```

## Retries and request identity

- Every SDK generates a new request ID for each RPC call and has no override. A retry through the SDK is a new request and can run the handler again.
- Make handlers safe to repeat, or reconcile with a typed read before you retry a write.
- A call that times out or returns UNAVAILABLE has an unknown outcome. The first call may still commit while a retry runs the handler again.
- An RPC that no Worker takes within the dispatch wait fails with UNAVAILABLE. An RPC name that no connected Worker registers fails with FAILED_PRECONDITION naming the RPC. RPCs never wait in the Step task queue.

## Self-check

- Is every external write of Flow state an RPC, and every RPC name a verb phrase?
- Does every RPC that races with Steps on shared state declare the same Attribute lock?
- Does every dynamic read-modify-write add the instance as both lock and load?
- Is every snapshot RPC that must work after closure free of locks, transactions, and durable effects?
- Are RPC handlers safe to repeat?
- Does any cancellation decision happen in a follow-up Step, not in the RPC handler?
