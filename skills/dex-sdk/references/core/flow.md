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
- One Step type can run many times, including in parallel. Its input distinguishes the executions.
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

| Policy | Existing running Flow | Existing closed Flow |
| --- | --- | --- |
| Default | rejects | rejects |
| Allow if previous failed | rejects | starts a new run after a failed, canceled, or terminated run (a Flow timeout under the fail or cancel timeout policy counts; under the handler policy, the handler's outcome decides); rejects after a completed run |
| Allow if not running | rejects | starts a new run |
| Disallow | rejects | rejects |
| Terminate if running | terminates it and starts a replacement | starts a new run |

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
- Is every Flow type named after its domain and unique in the registry?
- Is every Attribute, Channel, and Stream in the persistence schema?
- Can an operator identify the current business state from Dex Web or dexcli?
