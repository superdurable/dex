# Flow

A Flow is one durable run of a business process, with a stable identity and lifecycle, such as an order, a subscription, a transfer, an approval, or a processing job. It owns its Step list, its persistence schema, and its RPC handlers. Dex persists every state change, so the Flow survives Worker and Server restarts.

## Identifiers

| Identifier | Set by | Meaning | Rule |
| --- | --- | --- | --- |
| Flow type | the application, from the Flow implementation | Routes Worker calls and groups search results | Name it after the domain, such as `ApprovalFlow`. Never just `Flow`. |
| Flow ID | the caller at start | Business identity of one Flow run | Derive it from the business operation or resource. Address a Flow by Flow ID. |
| Run ID | Dex Server | One run of that Flow ID | Diagnostic only. Never a business revision or fence. Time travel keeps it. |
| Run attempt | Dex Server | Increments on each time travel of a run | Capture it before an operator mutation. |
| Step type | the application | Stable name of a Step definition in the Flow | Part of the durable contract of open runs, not just a function name. |
| Step execution ID | Dex Server | One execution of a Step type, formatted Step type plus a number | Use it, with the Flow ID, to key external effects. |
| RPC name | the application | Durable name of an RPC handler | Must start with a concrete action verb. |

Go, Java, Python, and Rust derive the default Flow type from the type or class name without its package or module. Flow types must therefore be unique within one Worker registry and on one Server. A generic Flow or Step type must override its type name. Dex Web and search show the Flow type as written.

## Definition

A Flow definition declares:

- the **Step list**, with one start Step whose input type is the Flow start input;
- the **persistence schema**: every Attribute, AttributeMap, Channel, ChannelMap, and Stream the Flow uses;
- the **RPC handlers**, declared explicitly.

Rules:

- Dex rejects a read or write of an Attribute or Channel name that the schema does not declare. Register every one before use.
- A Flow with no start Step, or an empty Step list, is valid when RPCs drive it into Steps later.
- One Step type can run many times, including in parallel. Its input distinguishes the executions. That fits the same work on different items. Different kinds of work, such as calls to different services, get distinct domain-named Step types, never one Step type with a kind field.
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
| 4 is yes | Do not split, whatever else holds. Every cross-Flow call adds history to both Flows and is a separate engine operation with its own latency and cost. Keep a deadline such as inactivity in the Flow where the operations happen. |
| 1 to 3 are all yes, 4 is no | Use an independent top-level Flow. Start it independently and coordinate with typed RPCs or Channels. It is not a SubFlow just because another Flow consumes its result. |
| Otherwise | Use Steps, Attributes, or AttributeMaps in the existing Flow. |

None of these justify a new Flow: field count, source size, number of phases, a retry policy, code reuse, provider neutrality, or ordinary parallelism.

Cross-Flow calls are fine for rare events: a start, a completion, a cancellation, or an explicit user command.

Keep long-lived authoritative facts in their stable owner. A temporary Flow stores only the validation or coordination state its own wait needs, and cleans it up by its own retention policy. Failed, expired, or temporary state must not pollute the authoritative store.

### Default to no SubFlows

Default a new design to no SubFlows. Use parallel Steps when work shares one Flow identity and lifecycle. Use ordinary language helpers or interfaces for code and provider abstraction. A SubFlow is an evolution step that must pass the gate in [subflow.md](subflow.md#evolution-gate).

### One owning Flow per business operation

For a product mutation that involves several actions, cross-service calls, durable waits, retries, reconciliation, or cleanup, start one domain-named Flow directly at the API boundary. That Flow owns admission, orchestration, recovery, completion, and the process state Dex can model.

- Do not add a database outbox and dispatcher, a polling command queue, or a generic event coordinator only to start or sequence the Flow.
- A single bounded local operation can stay synchronous.
- Independently owned external integrations may still need explicit events.

### Own downstream starts durably

The API starts one owning Flow and returns its accepted response. It never sequences dependent top-level starts, such as `StartFlow -> read status -> StartFlow`. The process can exit after the first start, and an HTTP retry does not create a durable recovery owner.

Put the follow-on work and its typed input in the first Flow's graph and state:

- Work with the same identity and lifecycle becomes more Steps.
- Work that needs its own top-level Flow is started by an Execute Step through an injected Client. It is not a SubFlow.
- Never start or mutate another Flow from WaitFor.

A downstream start is a separate acceptance boundary, not part of the Step's commit. Persist its stable Flow ID, a Request ID bound to the complete downstream request, and its start options in the owning Flow's input or state. If the Worker dies after the start is accepted but before the Step commits, the retried Execute repeats the same start safely. Never mark downstream work complete only because its start succeeded.

### Child jobs with their own identity

A SubFlow's Flow ID derives from its parent. Model a child job as a top-level child Flow when it needs its own business identity: people or systems look it up, retry it, or stop it by its own number, or something other than the parent can start it. The parent starts it from a Step through the Client and receives its result durably.

```text
StartChildJob.execute(job)                    // Client injected through the Flow constructor
    client.startFlow(ChildJobFlow, flowId = job.number, requestId = from the complete request,
                     reuse = disallow, ignore already started, Flow timeout = the job's limit)
    goTo(AwaitChildJob, job)
AwaitChildJob.waitFor(job) -> AnyOf(ChildReports[job.number].ForOne(), Timer(reconcile interval))
AwaitChildJob.execute(job)
    if a report was consumed: record its outcome under job.number; a repeat changes nothing; continue
    else: reconcile(job.number)               // see the rules below
          known: record it; continue
          unknown: goTo(AwaitChildJob, job)   // read again at the next interval
rpc recordChildOutcome(childFlowId, outcome) -> ChildReports[childFlowId].publish(outcome)

ChildJobFlow:
    RecordChildOutcome.execute -> write the outcome Attribute; goTo(ReportChildOutcome)
    ReportChildOutcome.execute -> call the parent's recordChildOutcome RPC,
                                  Request ID = "report:" + child Flow ID; a closed parent ends the attempts
                                  gracefulComplete()
```

- Start the child with a stable Flow ID and Request ID, so a retried Execute repeats the same start.
- When several entry points can start the same child, they all derive the same Flow ID and Request ID from the same complete request. Otherwise catch the typed already-started error, read the existing run, and track it only after its business identity matches. A deliberate new attempt gets a new Flow ID or an explicit reuse policy.
- Receive the result durably. The child reports through a parent RPC that publishes to a Channel, or a [Polling](patterns.md#polling) Step waits for the child's terminal state through the Client. Never keep the result only in Worker memory.
- The report Step must not run before the outcome Step commits. Under ASYNC durability the next Step can start before that commit is stored, so keep the Execute of these two Steps SYNC, the default, even when the Flow defaults to ASYNC; see [Durability](https://docs.superdurable.io/reference/go/step-options#durability).
- The report carries a stable Request ID derived from the child Flow ID and its purpose, such as `report:<child Flow ID>`. The parent keeps one record per child, keyed by its Flow ID, with the outcome and the terminal status as separate fields; a report or a reconcile read fills in what is missing, and a repeat changes nothing.
- Give each business attempt its own child Flow ID. DescribeFlow and the Client Attribute read see only the current or latest run, so a reused Flow ID mixes up attempts.
- Give every child its own Flow timeout. A closing parent never stops its children.
- StopFlow (cancel, fail, or terminate) starts no new application code in the child: no later decision, failure route, or cleanup runs. A handler already running gets a cancellation signal; if it ignores the signal, it can keep running and make more external calls, and Dex discards its uncommitted result. So a report sent around the stop can still arrive. The parent reconciles instead of waiting for a report:
  - After a successful stop, it reads the child's terminal status and its outcome Attribute, and records both.
  - When StopFlow returns not found, not active, or closing, or when the reconcile Timer fires, it reads the child with DescribeFlow and a Client Attribute read. A closed child records its terminal status, plus its outcome when one was committed. A running or closing child is still unknown: read it again later. A child that is not found has no record; the business policy decides, for example "unavailable".
  - Any other error, such as a permission or transport error, proves nothing. Return it so the Step retries.
  - Keep the terminal status and the outcome apart: a completed child without an outcome is not a stopped one.
- To let a child finish its own report when it is stopped, give the child a stop RPC that ends it through its own Steps.
- Children that finish after a quorum, or while the parent stops the rest, still count. Normal reports and reconciled outcomes go into the same record, keyed by the child Flow ID. The exact StopFlow and DescribeFlow errors are in the [Client reference](https://docs.superdurable.io/reference/server/runtime-client#stop-a-flow).

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

- Default and Disallow reject a start while a run exists, running or closed.
- Allow if not running rejects only while a run is active.
- Allow if previous failed starts a new run only after a failed, canceled, or terminated run.
- Terminate if running replaces an active run, and also starts a new run after a closed one.
- The exact outcome of each policy for each run state is in the [ID reuse policy reference](https://docs.superdurable.io/reference/python/start-flow-options#flow-id-request-id-and-already-started).

Choose "allow if not running" explicitly when a Flow ID must start again after closure. For replay of one logical start, use a stable Request ID, a rejecting policy, and ignore-already-started together, with the same input and options on every retry.

### Flow timeout

- Timeout policy applies only with a positive timeout. Choose fail, cancel, or handler.
- Cancel does not retry. Handler is invalid without a timeout handler.
- Without a Flow timeout, a Step that no Worker takes, or a SubFlow without its own timeout, waits forever.
- The timeout is an internal Dex timer. It shows in active-Step diagnostics, and SkipTimer can fire it early.
- Fix the cause before you recover a Flow that failed by timeout.

### Timeout handler

The timeout handler is the Flow's last chance to record a business outcome and clean up. It has Execute semantics:

- Configure its method timeout, heartbeat timeout, retry, failure route, durability, locks, and state loads in its timeout handler options. These need a positive Flow timeout and the handler policy.
- Its timing starts after the deadline fires, so it can run past the original deadline.
- Active Steps, including Steps that wait on a Channel or Timer, keep running while it runs. When it commits ForceComplete or ForceFail, Dex starts closing the Flow and cancels the other Steps. GracefulComplete, or a GoTo to a Step that completes gracefully, waits until every other active Step ends. The handler can commit well after the deadline, because it waits for a Worker and may retry. To start closing the Flow when the Timer fires, use the fail or cancel timeout policy.
- It runs once per run. There is no Flow-level retry.
- It may stage Attribute writes, Channel deletions, and Channel publications with its decision.
- It receives ordinary Attributes and Channel sizes. AttributeMap values and pending messages need explicit selections in its options.
- When its retries are exhausted, it can route only to a registered no-input recovery Step. The recovery Step reads the final failure from its Context. Without a route, the Flow fails.
- A handler that no Worker takes also waits in the task queue. Dex tries to dispatch it ahead of other queued Steps but does not guarantee the order.

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

Before code, write down for each Step:

- typed input;
- the Conditions WaitFor returns;
- the side effects Execute performs, and their idempotency keys;
- Attribute and Channel reads and writes, with selective loads and locks;
- durability: inherited Flow default or a method override;
- WaitFor and Execute method timeouts, heartbeat timeout, and retry budget;
- the success transition;
- the exhausted-retry or business-failure transition;
- compensation and operator recovery, as explicit Steps.

For every external side effect, also decide which failures are retryable, the total retry budget, whether it needs heartbeats, what happens after retry exhaustion, whether compensation is needed, and what deadline ends the business process.

Treat StepOptions as part of the graph, not tuning added later. See [step.md](step.md).

## Self-check

- Can the graph explain every success, wait, failure, cancellation, and timeout path?
- Is every top-level Flow justified by owner, retention, wait, and terminal-lifecycle differences?
- Does any pair of Flows exchange RPCs or messages on frequent events? Then merge them or move the decision.
- Does one owning Flow durably start dependent work, including recovery between downstream acceptance and Step commit?
- Does a parent of top-level child Flows record children that were stopped without reporting, or that finished after a quorum?
- Is every Flow type named after its domain and unique in the registry?
- Is every Attribute, Channel, and Stream in the persistence schema?
- Can an operator identify the current business state from Dex Web or dexcli?
