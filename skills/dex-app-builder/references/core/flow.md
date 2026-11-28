# Flow

A Flow is one durable run of a business process, with a stable identity and lifecycle, such as an order, a subscription, a transfer, an approval, or a processing job. It owns its Step list, its persistence schema, and its RPC handlers. Dex persists every state change, so the Flow survives Worker and Server restarts.

## Identifiers

| Identifier | Set by | Meaning | Rule |
| --- | --- | --- | --- |
| Flow type | the application, from the Flow implementation | Routes Worker calls and groups search results | Usually name it after the domain, such as `ApprovalFlow`, not just `Flow`; it must be unique, see below. |
| Flow ID | the caller at start | Business identity of one Flow run | Derive it from the business operation or resource. Address a Flow by Flow ID. |
| Run ID | Dex Server | One run of that Flow ID | Time travel keeps it, so it never tells attempts within a run apart and is never a business revision. A claimed run ID can tie a child to its run; see [child jobs](#child-jobs-with-their-own-identity). |
| Run attempt | Dex Server | Increments on each time travel of a run | Capture it before an operator mutation. |
| Step type | the application | Stable name of a Step definition in the Flow | Part of the durable contract of open runs, not just a function name. |
| Step execution ID | Dex Server | One execution of a Step type, formatted Step type plus a number | With the Flow ID, it keys an effect when one Step execution is one logical effect. A later run of the same Flow ID numbers executions from the start again, so add the run's start time when the Flow ID can be reused. A later execution that can repeat the same effect, such as a manual retry or a loop, gets a new ID, so key that effect by a business ID. |
| RPC name | the application | Durable name of an RPC handler | Usually a concrete action verb first. |

Go, Java, Python, and Rust derive the default Flow type from the type or class name without its package or module. Flow types must therefore be unique within one Worker registry and on one Server. A generic Flow or Step type must override its type name. Dex Web and search show the Flow type as written.

## Definition

A Flow definition declares:

- the **Step list**, with one start Step whose input type is the Flow start input;
- the **persistence schema**: every Attribute, AttributeMap, Channel, ChannelMap, and Stream the Flow uses;
- the **RPC handlers**, declared explicitly.

Rules:

- Dex rejects a read or write of an Attribute or Channel name that the schema does not declare. Register every one before use.
- A Flow with no start Step, or an empty Step list, is valid when RPCs drive it into Steps later.
- One Step type can run many times, including in parallel. Its input distinguishes the executions. That fits the same work on different items. Prefer one Step type per kind of work, such as calls to different services, so the graph shows it and it can have its own options.
- Initial Attribute values passed at start must match an Attribute or AttributeMap in the schema. Dex creates the Flow and its initial values in one operation, so the first Step sees them.
- Flow completion with a null output stores no output. When "completed with null" must differ from "completed without output", return an explicit result object.

## Find the Flow boundary

Do not confuse three shapes: parallel Steps in one Flow, independent top-level Flows, and parent-child SubFlows.

For each candidate data set or process, ask:

1. Does it have a different authoritative owner and a different retention or cleanup lifecycle?
2. Does it have its own waits, Timers, and terminal outcomes?
3. Would keeping it in the existing Flow force that Flow to retain or coordinate unrelated state?
4. Would a split make the Flows exchange RPCs or Channel messages on frequent events, such as every user operation, every external callback, or every poll?

| Answers | Decision |
| --- | --- |
| 4 is yes | Usually keep one Flow. Cross-Flow calls add network and coordination cost and repeat when the calling method retries. Mutating calls add durable work and history to the target; a read-only call need not add target history. Split anyway when the parts have different owners or lifecycles, such as a shared quota or admission parent that many Flows use, and the per-call cost is acceptable. Keep a deadline such as inactivity in the Flow where the operations happen. |
| 1 to 3 are all yes, 4 is no | Use an independent top-level Flow. Start it independently and coordinate with typed RPCs or Channels. It is not a SubFlow just because another Flow consumes its result. |
| Otherwise | Use Steps, Attributes, or AttributeMaps in the existing Flow. |

None of these justify a new Flow: field count, source size, number of phases, a retry policy, code reuse, provider neutrality, or ordinary parallelism.

Cross-Flow calls are fine for rare events: a start, a completion, a cancellation, or an explicit user command.

Keep long-lived authoritative facts in their stable owner. A temporary Flow stores only the validation or coordination state its own wait needs, and cleans it up by its own retention policy.

### Prefer parallel Steps to SubFlows

Prefer parallel Steps when work shares one Flow identity and lifecycle. Use ordinary language helpers or interfaces for code and provider abstraction. Use a SubFlow when the child needs its own lifecycle, history, or retries, or when one fan-out is very large; see [subflow.md](subflow.md#when-to-use-a-subflow).

### One owning Flow per business operation

For a product mutation that involves several actions, cross-service calls, durable waits, retries, reconciliation, or cleanup, start one domain-named Flow directly at the API boundary. That Flow owns admission, orchestration, recovery, completion, and the process state Dex can model.

- When the triggering fact lives in Dex, or the caller retries the start until it succeeds, a replayed StartFlow is the durable handoff; then a database outbox and dispatcher, a polling command queue, or a generic event coordinator added only to start or sequence the Flow adds nothing.
- A single bounded local operation can stay synchronous.
- Independently owned external integrations may still need explicit events.

### Own downstream starts durably

The API process can exit after its first start, and an HTTP retry does not create a durable recovery owner. So the API usually starts one owning Flow and returns its accepted response, rather than sequencing dependent top-level starts, such as `StartFlow -> read status -> StartFlow`.

Put the follow-on work and its typed input in the first Flow's graph and state:

- Work with the same identity and lifecycle becomes more Steps.
- Work that needs its own top-level Flow is started by an Execute Step through an injected Client. It is not a SubFlow.
- Usually start or mutate another Flow from Execute, not WaitFor; see [WaitFor versus Execute](step.md#waitfor-versus-execute).

A downstream start is a separate acceptance boundary, not part of the Step's commit. Persist its stable Flow ID, a Request ID bound to the complete downstream request, and its start options in the owning Flow's input or state. If the Worker dies after the start is accepted but before the Step commits, the retried Execute repeats the same start safely. Never mark downstream work complete only because its start succeeded.

### Child jobs with their own identity

A SubFlow's Flow ID derives from its parent. Model a child job as a top-level child Flow when it needs its own business identity: people or systems look it up, retry it, or stop it by its own number, or something other than the parent can start it. The parent starts it from a Step through the Client and receives its result durably.

The pseudocode below shows the Channel variant: the report RPC publishes to a Channel and the parent consumes it. The linked examples use the direct-record variant: the report RPC writes the outcome into the parent's record, and a Timer keeps reconciling. Both are valid.

Runnable examples: [Go](https://github.com/superdurable/dex/tree/main/examples/go/patterns/child-flows-business-identity), [Python](https://github.com/superdurable/dex/tree/main/examples/python/dex_examples/patterns/child-flows-business-identity), [Java](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/superdurable/dex/patterns/childflowsbusinessidentity), [TypeScript](https://github.com/superdurable/dex/tree/main/examples/typescript/src/patterns/child-flows-business-identity), [Rust](https://github.com/superdurable/dex/tree/main/examples/rust/src/control_patterns/child_flows_business_identity).

```text
StartChildJob.execute(job)                    // Client injected through the Flow constructor
    result = client.startFlow(ChildJobFlow, flowId = job.number, requestId = from the complete request,
                     reuse = disallow, ignore already started, Flow timeout = the job's limit)
    if result is started: claim result.runId
    if result is already started:             // another logical request, even from this entry point
        if a stored business identity read from one run matches job: claim that run ID
        else:                                 // not this parent's child: never wait for, count, or stop it
            apply the business policy for a rejected child (record it and continue, or stop the admitted children and fail)
    goTo(AwaitChildJob, job)
AwaitChildJob.waitFor(job) -> AnyOf(ChildReports[job.number].ForOne(), Timer(reconcile interval))
AwaitChildJob.execute(job)
    if a report from the claimed run was consumed: record its outcome under job.number; a repeat changes nothing
    else: reconcile(job.number, claimed run ID)   // see the rules below; fills what it read
    every needed field recorded: continue
    otherwise: goTo(AwaitChildJob, job)       // read again at the next interval
rpc recordChildOutcome(childFlowId, runId, outcome) -> ChildReports[childFlowId].publish(runId, outcome)

ChildJobFlow:
    RecordChildOutcome.execute -> write the outcome Attribute; goTo(ReportChildOutcome)
    ReportChildOutcome.execute -> call the parent's recordChildOutcome RPC with its own run ID,
                                  Request ID = "report:" + child Flow ID + run ID; a closed parent ends the attempts
                                  gracefulComplete()
```

- Start the child with a stable Flow ID and Request ID, so a retried Execute repeats the same start.
- Skip the identity check only when the mapping from business identity to child Flow ID guarantees that a Flow ID is only ever started again by a replay of the same logical request; people who retry or start the child by its own number break that guarantee. Otherwise another logical request can start a run with the same Flow ID with a different Request ID, even from the same entry point or the same parent run. Catch the typed already-started error, read the existing run, and claim that run only when its stored business identity matches the request; otherwise the run is not this parent's child, so never wait for, count, or stop it.
- Receive the result durably ([Must 2](../../SKILL.md#must)). The child reports through a parent RPC that publishes to a Channel, or a [Polling](patterns/polling-and-timers.md#polling) Step waits for the child's terminal state through the Client.
- SYNC durability, the default, stores a method result before downstream work starts. Under ASYNC, the report Step can start before the outcome Step's result is stored; a Server node crash can lose that result and rerun the outcome Step after the report was sent. See [Workers, attempts, and time bounds](../../SKILL.md#workers-attempts-and-time-bounds). So when a replay could compute a different outcome, such as from a model or an external read, keep the Execute of the outcome Step and the report Step SYNC, the default, even when the Flow defaults to ASYNC.
- The report carries a stable Request ID derived from the child Flow ID, its run ID, and its purpose, such as `report:<child Flow ID>:<run ID>`. The parent keeps one record per child, keyed by its Flow ID, with the outcome and the terminal status as separate fields; a report or a reconcile read fills in what is missing, and a repeat changes nothing.
- When people reopen a child by its own business number, that number stays the child Flow ID:
  - A resend of the same start request keeps its Request ID. A deliberate reopen is a new start request with a new Request ID; resends of that request keep the new one. The Request ID of the latest run's start replays that start, so a reopen with it starts nothing.
  - Every entry point uses the same ID reuse policy, chosen from the [ID reuse table](../../SKILL.md#starting-a-flow-and-id-reuse) by the run states the business reopens from. A policy reads only how the latest run closed: a child that records a business failure and then completes is closed as completed. To reopen it after that failure, close it with ForceFail or allow a completed run.
  - Tie reads, reports, and stops to the claimed run ID when a reopen can happen while the parent still waits and the parent must not mix runs. When a child number has only one run, the Flow ID is enough. A parent that ties them records the run ID that its start returned or that it claimed. It accepts a report only when the report carries that run ID.
  - DescribeFlow and the Client Attribute read address the latest run of the Flow ID, so a parent that ties runs records a read only while the latest run is the claimed one. A query that returns the status and the outcome by run ID can be used directly.
  - StopFlow stops the active run of the Flow ID, which may be a newer run. To stop only the claimed run, call a child stop RPC that carries the claimed run ID; the child compares it with its own run ID before it commits the stop.
- When a child must end by a fixed limit known at start, each starter sets its Flow timeout, because the Flow timeout is a start option; when the child must report its timeout, use a timeout handler that reports. A limit that can move after the start uses the [deadline watchdog](patterns/polling-and-timers.md#deadline-watchdog) inside the child. When a Step's failure route must run before the Flow timeout, set its retry total duration shorter than the timeout.
- Closing the parent does not stop its children; see [SubFlows and child Flows](../../SKILL.md#subflows-and-child-flows). Stop them explicitly when the business needs it.
- When the parent must know the result: a child that fails or times out on its own does not report either, because its report Step never runs. Route the child's terminal failure to a recovery Step, or its timeout to a timeout handler, that reports the failure to the parent; or keep the parent's reconcile Timer so it reads the child's terminal state. A timeout handler that holds a lock an RPC takes records the outcome and moves to an unlocked Step that reports idempotently and then force-closes. When a replay could compute a different outcome, keep that handler SYNC, even when the Flow defaults to ASYNC, so the outcome is stored before the report. A handler that force-closes at once only records the outcome, and the parent reconciles it, unless the handler holds no such lock and reports itself.
- When the parent does not need to know how a stopped child ended, stop the child and move on; a parent that needs only a quorum stops the remaining children this way. StopFlow with cancel, fail, or terminate starts no new handlers in the child, so it may never report; a running handler can ignore cancellation, and a report already sent can still arrive.
- When the parent needs to know how a stopped child ended, reconcile it: for example, to count its result, undo or bill its side effects, or show its outcome. A parent that follows the Flow ID reconciles its latest run instead. The steps below apply when the parent tracks the claimed run:
  - Run `reconcile` after any StopFlow result and when the reconcile Timer fires:
    1. Describe the claimed run by its run ID. A typed not-found result means the run is gone, for example deleted after its retention period; it goes to the business policy, for example record "unavailable".
    2. The claimed run is RUNNING: read again at the next interval.
    3. The claimed run is terminal: record its status and its outcome. An absent Attribute is an absent outcome.
    4. Return any other error so the Step retries.
  - A report that arrives after the stop, or from a child that finished after a quorum, fills the same record; a repeat changes nothing.
  - The errors are in the Client reference: [Stop a Flow](https://docs.superdurable.io/reference/server/runtime-client#stop-a-flow), [Describe a Flow](https://docs.superdurable.io/reference/server/runtime-client#describe-a-flow), and [Target one run](https://docs.superdurable.io/reference/server/runtime-client#target-one-run).
- When a stopped child must still send its own report, give it a stop RPC that ends it through its own Steps instead of StopFlow.

## Flow options

Start options are immutable after the start. Flow configuration can change while the Flow runs.

| Option group | Rule |
| --- | --- |
| Flow ID, Request ID, ID reuse policy, ignore-already-started | Start identity. See [error-handling.md](error-handling.md#start-identity-and-duplicate-starts). |
| Flow timeout and timeout policy | A durable absolute deadline set at start. Zero means none. |
| Timeout handler options | Execute semantics for the timeout handler. |
| Initial Attributes | Written atomically with the start. |
| Flow configuration (config override) | Durability default, Attribute Store names, active-Step search indexing. Supplied fields merge over the Server defaults. |

### ID reuse policy

The outcome of each policy for each latest-run state, and how the Request ID interacts with it, are in [Starting a Flow and ID reuse](../../SKILL.md#starting-a-flow-and-id-reuse). Pick the policy by which earlier runs the business may start again under the same Flow ID.

For replay of one logical start, use a stable Request ID with ignore-already-started; the retry skips the reuse policy.

### Flow timeout

- Timeout policy applies only with a positive timeout. Choose fail, cancel, or handler.
- Cancel does not retry. Handler is invalid without a timeout handler.
- Without a Flow timeout, a Step that no Worker takes, or a SubFlow without its own timeout, waits forever; see [Flow timeout](../../SKILL.md#flow-timeout-completion-and-cancellation).
- The timeout is an internal Dex timer. It shows in active-Step diagnostics, and SkipTimer can fire it early.
- Fix the cause before you recover a Flow that failed by timeout.

### Timeout handler

The timeout handler is the Flow's last chance to record a business outcome and clean up. It has Execute semantics:

- Configure its method timeout, heartbeat timeout, retry, failure route, durability, locks, and state loads in its timeout handler options. These need a positive Flow timeout and the handler policy.
- Its timing starts after the deadline fires.
- Active Steps, including Steps that wait on a Channel or Timer, keep running until the handler commits. ForceComplete or ForceFail then starts closing the Flow and cancels them, so those Steps need no deadline check of their own. GracefulComplete, or a GoTo to a Step that completes gracefully, waits until every other active Step ends. To start closing the Flow when the Timer fires, use the fail or cancel timeout policy.
- When no work may start after the deadline, use the fail or cancel timeout policy, or give the handler a short retry budget and let it withdraw work by its stable reference.
- It runs once per run. There is no Flow-level retry.
- It may stage Attribute writes, Channel deletions, and Channel publications with its decision.
- It receives ordinary Attributes and Channel sizes. AttributeMap values and pending messages need explicit selections in its options.
- When its retries are exhausted, it can route only to a registered no-input recovery Step. The recovery Step reads the final failure from its Context. Without a route, the Flow fails.

### Behaviors Dex does not offer

- **No start delay.** To delay the first work, start at once and have the start Step wait on a Timer.
- **No Flow-level retry.** Retries are per Step method. Model a whole-process retry as explicit Steps.

### Flow configuration

- Flow configuration is persisted at start. Changing the application default affects new Flows only.
- A configuration update merges only the supplied fields. It does not recall work already dispatched.
- A method's durability is fixed when the method call becomes pending. A changed default affects later method calls only.
- Attribute Store sync requires the Flow to name its stores at start. See [attribute.md](attribute.md#attribute-store-sync).
- Indexing active Step types for search is an explicit opt-in in Flow configuration.

The current option names, defaults, and limits are in the language's Developer reference. See [SKILL.md](../../SKILL.md#find-the-right-reference-version).

## Turn the business process into a Step graph

Before code, sketch for each Step:

- typed input;
- the Conditions WaitFor returns;
- the side effects Execute performs, and their idempotency keys;
- Attribute and Channel reads and writes, with selective loads and locks;
- the success transition;
- the exhausted-retry or business-failure transition;
- compensation and operator recovery, as explicit Steps.

For every external side effect, also decide which failures are retryable, what happens after retry exhaustion, whether compensation is needed, and what deadline ends the business process.

The StepOptions defaults fit short, idempotent methods. Usually decide StepOptions with the graph only for a method that runs long or stays silent, holds a lock, makes a non-idempotent effect, needs a failure route, or loads AttributeMaps or pending messages. See [step.md](step.md#configure-the-method-policy-where-defaults-do-not-fit).

## Self-check

- Can the graph explain every success, wait, failure, cancellation, and timeout path?
- Is every top-level Flow justified by owner, retention, wait, and terminal-lifecycle differences?
- Does any pair of Flows exchange RPCs or messages on frequent events? Then merge them or move the decision, unless different owners or lifecycles justify the per-call cost.
- Does one owning Flow durably start dependent work, including recovery between downstream acceptance and Step commit?
- When the business needs a child's ending, does the parent of top-level child Flows record children that were stopped without reporting, or that finished after a quorum?
- Is every Flow type named after its domain and unique in the registry?
- Is every Attribute, Channel, and Stream in the persistence schema?
- Can an operator identify the current business state from Dex Web or dexcli?
