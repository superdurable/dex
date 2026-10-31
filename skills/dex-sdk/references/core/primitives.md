# Primitive selection

Read only the sections relevant to the task.

## Flow

Use a Flow as the top-level durable business execution. It owns the Step list, persistence schema, and RPC handlers. Give the Flow type and execution ID stable business meanings.

Name each Flow type after its domain, such as `ApprovalFlow`, and never just `Flow`. Go, Java, Python, and Rust derive the default Flow type from the type or class name without its package or module, so Flow types must be unique within one Worker registry. Dex Web, run history, and Flow type searches show that name as written.

Docs: https://docs.superdurable.io/primitives/flow

## Step and Wait

Use a Step for retried background work and explicit state transitions. **WaitFor** returns Conditions; **Execute** performs work and returns a Step decision.

Leave Conditions unnamed in **Until**, **AnyOf**, and **AllOf**. Do not add condition IDs merely to distinguish branches: read Channel results from the Channel definition and inspect Timer outcomes through the Context. Every Condition in **AnyCombinationOf** needs a unique ID. A Timer also needs an ID when another operation selects it by ID, such as **SkipTimer**.

When multiple **AnyOf** alternatives are ready in one evaluation, Dex selects the first feasible candidate in canonical order: Timer Conditions in declaration order, then Channel Conditions in declaration order, then SubFlow Conditions in declaration order. An earlier unready Condition does not block a later ready Condition. Mixed Condition kinds do not preserve one global call argument order. Only the winning Channel Condition consumes messages; other Channel candidates remain unchanged.

**AnyOf** models a race among the currently available alternatives. When business logic requires strict priority, return only the active high-priority Condition until it is resolved. Do not put lower-priority alternatives in that Wait.

Use multiple next Steps for parallel work. Use cancellation deliberately when a first-winner branch makes siblings unnecessary.

Step durability resolves in this order: a method override, FlowConfig, then **SYNC**. WaitFor and Execute can override durability independently. Applications dominated by short idempotent operations can set a Flow-level **ASYNC** default and override long-running or replay-sensitive methods to **SYNC**. **ASYNC** advances before the commit, so a node failure can replay a recent result.

Every **WaitFor** and **Execute** call receives ordinary Attributes and Channel size metadata. AttributeMap values and pending Channel messages require method-specific selections in StepOptions. Select the whole map only when the method must enumerate instances; otherwise select exact instances. WaitFor and Execute use independent snapshots. Execute loads after Wait consumption, and retries reuse the first snapshot for that logical method call. Attribute locks do not load state.

A long-running attempt must emit an explicit heartbeat or Stream message before its heartbeat timeout. A heartbeat value is a retry checkpoint. An explicit valueless heartbeat clears the checkpoint; a Stream message preserves its current state.

For an LLM call that may remain healthy without output for more than one minute, tell the application developer to raise **HeartbeatTimeout** above its one-minute default. Size it to the longest acceptable silent interval, and use the method timeout to cap the whole attempt. Do not add periodic heartbeats solely to mask provider silence; they prove only that application code is running, not that the upstream request is progressing.

The Context exposes the Flow run's start time (Go **FlowStartedAt**, Java **getFlowStartedAt**, Python **flow_started_at** from SDK v1.28.0, TypeScript **flowStartedAt**, Rust **flow_started_at**). It is the same for every Step, retry, timeout handler, and RPC handler in one run, so derive a deterministic identity from the Flow ID plus the start time instead of reading the wall clock, which differs on each retry. Do not use it as a unique key across runs that reuse a Flow ID without including the run's other identifying values.

A Flow may register several instances of one Step class under different Step types, such as a generic Step parameterized by its input type. Go routes by the instance's Step type. Python and TypeScript v1.28.0 route a movement, an execute-failure fallback, a timeout-handler failure fallback, or a cancellation selection by registered instance, by **StepRef** (a non-executable reference holding only a Step type), or by class. A class target is valid only when exactly one instance of that class is registered. Give each instance a distinct Step type, and use **StepRef** for a target that is defined elsewhere or would create a construction cycle. See the [Python](../python/primitives.md#flow-step-wait-decisions) and [TypeScript](../typescript/primitives.md) shapes.

Read [StepOptions](step-options.md) for the durability trade-off, shared retry budget, timeout boundary, and full policy checklist.

Docs: https://docs.superdurable.io/primitives/step

## Attribute

Use an Attribute for durable state inside one Flow execution. Register every Attribute in the persistence schema before reading or writing it.

Use one Attribute when the value is cohesive and should be replaced as a unit. Use an AttributeMap when runtime-keyed instances change independently; each instance is stored separately, avoiding a rewrite of the whole collection. Use stable domain keys and delete instances that are no longer needed.

Steps, timeout handlers, and RPCs receive regular Attribute values automatically. They must explicitly load AttributeMap entries. Load the whole map for broad snapshots or exact instances for known keys. AttributeMap size does not make its entries available. An explicit load controls data transfer only; it does not enable transactional execution or isolation.

Lock the exact AttributeMap instance when Steps or RPCs can race on it. For an invariant spanning the whole map, register a separate non-indexed singleton bool Attribute as a coordination lock and require every cooperating writer to acquire it; see [whole-map coordination](data-handling.md#whole-map-coordination). Do not treat an AttributeMap index as an index over its instances: all instances share one Flow search field, later writes replace that field, and instance keys are not searchable. AttributeMap enumeration is not server-side pagination.

Assign every application Indexed Attribute an explicit generic index key from a namespace-wide typed pool: **keyword1**, **keyword2**, **keywordList1**, **keywordList2**, **text1**, **text2**, **int1**, **int2**, **bool1**, **bool2**, **double1**, **double2**, **datetime1**, **datetime2**, and later numbers as capacity permits. Keep the Attribute name meaningful to the business; only its physical search key uses a generic slot. Reuse each slot across Flow types with the same index type, even when its business meaning differs. Two logical indexed fields in one Flow need distinct slots. Do not use business names or the default Attribute-derived key for application indexes. Dex system indexes keep their reserved names.

Generic slots can mean different things for different Flow types. Application run searches using a generic slot must constrain **FlowType**. Combine that scope predicate with parenthesized caller filters so an OR cannot bypass it. Do not index sensitive data or PII.

For a short-lived secret that the Flow itself must hold, such as an API key, define a hard-delete Attribute or AttributeMap. Dex keeps its values out of Flow history and permanently deletes them on delete or a null write. It supports only string and object values. It cannot be indexed, synced to an Attribute Store, or used with WaitForAttributeMatch. Writes can become visible before the attempt commits. Read [hard-delete Attributes](data-handling.md#hard-delete-attributes-for-short-lived-secrets) for every requirement and limitation before using one.

Read [data-handling.md](data-handling.md) for large values, map chunking, BlobCache locality, hard-delete secrets, and external projections.

Docs: https://docs.superdurable.io/primitives/attribute

## Channel

Use a Channel for ordered, durable, typed messages scoped to one Flow execution. One matching wait consumes a message once.

Use a ChannelMap when the same message contract is partitioned by a dynamic key. Plan how externally published messages are drained before Flow completion.

Every pending Channel message has a server-assigned message ID. Expose a typed Flow RPC when an application needs a durable queue UI. The RPC explicitly loads and lists pending messages in FIFO order without consuming them. Only a pending message can be deleted; deletion after consumption returns the Channel-message-not-found error.

Steps, timeout handlers, and RPCs always receive ChannelInfo sizes, so Channel size and ChannelMap keys and sizes do not require a load. Reading pending message envelopes requires an explicit Channel or ChannelMap load. Load the whole ChannelMap or exact instances according to what the handler reads. A loaded empty queue is empty; reading an unloaded queue is a usage error.

Steps and timeout handlers may delete a pending message in the same response as Attribute writes, Channel publications, and a decision. A known message ID can be deleted without loading its envelope. Deletion is a response side effect, not part of the Step decision. If the message is already absent, deletion is best effort and the remaining effects still apply.

A Channel queue is not conversation history. Keep consumed user and assistant messages in Attributes when the application must display or reconstruct them.

Use a transactional RPC to move or edit a pending message atomically. The caller should send only the message ID. Explicitly load the source Channel, find the original Value in the RPC snapshot, then stage its deletion and destination publication. A missing message rejects the entire transaction, including all Attribute writes and Channel publications.

Attribute locking already selects transactional execution. Channel deletion without an Attribute lock must explicitly select the SDK's transactional RPC option. Transactional validation protects an ID-only move from concurrent consumption, but it does not isolate decisions based on the whole snapshot. For those decisions, every cooperating Step and RPC writer must use the same Attribute lock. The lock does not implicitly load map entries or Channel messages.

Treat pending-message reads inside Steps and RPCs as potentially stale snapshots. Other handlers can consume, delete, or publish while the current handler runs. Read and write pending messages directly only when the operation explicitly tolerates that race. If the result depends on the queue remaining unchanged, use one shared Attribute lock across every cooperating Step and RPC writer; transactional execution alone is insufficient.

Without transactional execution, a missing deletion may be a no-op while other RPC effects commit. When a deployment cannot provide the required atomic guarantee, reconcile from a fresh pending-message list.

Docs: https://docs.superdurable.io/primitives/channel

## RPC

Use an RPC for every application write of Flow-owned Attribute, AttributeMap, Channel, or ChannelMap state. Use a read-only RPC for a read that needs Flow code, pending Channel messages, or a whole AttributeMap. Read stored Attribute values directly with the Client's [Attribute read](#read-attributes-from-outside-a-flow) where the SDK has it. RPC handlers may return snapshots, update or delete Attributes, and publish, list, move, or delete pending Channel messages. Protect shared mutations with Attribute locks when they can race with Steps or other RPCs.

Use a Channel instead when the caller should enqueue work without synchronous application-level handling.

An RPC without Attribute locks or transactional execution runs its handler against a snapshot of the Flow's state. If the handler returns only output, Dex writes nothing to the Flow, and a retained terminal execution can serve the read. Success therefore does not prove that the Flow is active. Locks, explicit transactions, returned durable effects, or a Server policy that runs every RPC transactionally require an active Flow. A non-transactional handler that returns durable effects runs before Dex applies them; if the Flow closes in between, applying the effects fails with a not-active error after the handler ran. Keep external mutations idempotent and do not treat `FlowNotActiveOrNotFoundError` as proof that the handler never ran.

Use a lifecycle API when a response needs current execution status. A read-only RPC returns its application-state snapshot; it does not add terminal status that the application did not persist.

Do not search for a Run ID before an ordinary read-only RPC. Omit an optional Run ID so the Server resolves and pins the current execution for that invocation. Supply one only when the application must target an exact execution across Flow ID reuse.

Keep application read models cohesive. When one page needs conversation Attributes, a description, and pending queues, prefer one read-only snapshot RPC that explicitly loads those collections over several independently timed requests. When a view needs only stored Attributes and exact AttributeMap instances, one Client Attribute read call serves it.

When one response requires multiple Attributes or AttributeMap instances, read them in one Client Attribute read call or assemble them in one dedicated read-only RPC. Do not issue sequential Client reads. Select only the required map instances so the invocation has one coherent read boundary and avoids repeated round trips. Keep separate views as separate RPCs; do not combine unrelated read models merely to reduce calls.

Select transactional execution when an RPC must atomically validate a pending Channel message ID and commit its deletion with other Flow-state writes. Handle the Channel-message-not-found error as a stale queue view and refresh before retrying.

Keep registration-time and invocation-time options separate. Registration options own fixed timeout, locks, transactionality, whole-map loads, singleton Channel loads, and other state requirements. Invocation options are additive and intentionally narrow: they can add exact AttributeMap instance locks, exact AttributeMap instance loads, and exact ChannelMap instance loads selected from request data. They never replace or clear registration options.

The Client validates invocation selections before network I/O. Every definition must belong to the RPC's Flow, every dynamic lock must target an AttributeMap instance, and every instance name must be nonempty and slash-free. A dynamic read-modify-write must include the same instance as both a lock and a load. Reads need only the load. All cooperating writers must use the same instance lock, and lock conflicts are explicit contention rather than queued execution.

Docs: https://docs.superdurable.io/primitives/rpc

## Stream

Use a Stream for low-latency, best-effort, resumable updates such as progress displayed in a UI. Do not use a Stream when delivery must be durable; use a Channel or Attribute instead.

A Step may append any number of messages to the same or different Streams before its final result. A Step Stream write is fire-and-forget: local encoding or registration can fail immediately, but Dex Server does not acknowledge Stream Store persistence and a Store failure does not fail the Step.

When a Step calls a streaming LLM API, create one invocation-managed buffered text writer before starting the request and pass its bound write method as the token or delta callback. Do not call direct Stream write for each LLM token or delta. The default one-second timer and 16 KiB soft UTF-8 threshold reduce message volume, and the invocation flushes the tail before its result or error. In Go, Java, Python async, TypeScript, and Rust, call only write; the SDK owns finalization. Python sync generators are cooperative and must yield from an explicit final flush. An empty buffer does not heartbeat. Retry does not restore unsent text and may repeat batches already sent.

Use the text-specific API for the installed SDK version: **NewBufferedTextStream** in Go, **BufferedTextStream.create** in Java, **buffered_text** in Python, **bufferedText** in TypeScript, or **buffered_text** in Rust. For example, a Python async Step should create **progress = thinking.buffered_text(context)** and pass **progress.write** to the LLM helper.

Continue using direct Stream writes for semantically complete, independent messages. Do not buffer events merely because they arrive quickly; batching changes the message boundaries observed by readers.

Step messages use **#StepExecutionID** as source metadata. The source is not an idempotency key: attempts and messages may share it, and every write appends. Client Stream writes require a nonempty source, which may repeat or contain **#**.

Stream clients have two distinct read paths. **ReadStream** consumes forward one message at a time, resumes from a message token, and can long-poll for the next write. **ListStreamMessages** immediately returns a retained-message page in newest-first order. Its empty before-page token starts at the retained tail; each nonempty next-page token is an exclusive, scope-bound anchor for older messages. An empty next-page token marks the end.

Treat reverse listing as a best-effort retained-message snapshot. Writes after the first page do not enter the older-page chain, but trimming can create gaps. If trimming moves the retained head past the anchor, Dex returns an empty page instead of restarting from the tail. Page size must be positive and cannot exceed the Server **streamStore.maxReadMessages** limit, which defaults to 1000.

Docs: https://docs.superdurable.io/primitives/stream

## Timer

Use a Timer Condition for a durable delay, reminder, deadline branch, or scheduling loop. Do not use a Timer loop to poll an external system; use the [Polling pattern](patterns.md#polling). Decide what happens if a timer is skipped and whether the business deadline should complete, cancel, fail, or route to a handler.

A Flow timeout handler has Execute semantics. Configure its per-attempt timeout, heartbeat timeout, retry, failure route, durability, locks, and selective state loads through FlowTimeoutHandlerOptions on StartFlowOptions or SubFlowOptions. These options require a positive Flow timeout and the Handler policy. Handler timing starts after the soft timeout fires and may extend beyond the original deadline.

Route exhausted handler retries only to a registered no-input Step. The SDK supplies null or unit input, and the recovery Step reads the final failure from Context. Without a failure target, exhausted retries fail the Flow.

Docs: https://docs.superdurable.io/primitives/timer

## SubFlow

Use a SubFlow for child work that benefits from a separate Flow identity and lifecycle. Bound parallel SubFlows and define their reuse, cancellation, and parent-completion behavior.

Docs: https://docs.superdurable.io/primitives/subflow

## Client

Use the Client at application boundaries to start, stop, inspect, search, read committed Attributes, invoke typed Flow RPCs, wait for Attribute matches, consume Streams, and manage Flow lifecycle. The Client has no direct Attribute write, Channel publication, or pending-message method; write through typed RPCs or Channels. Handle typed SDK failures for duplicate starts, missing Flows, closed Flows, lock conflicts, missing messages, long-poll expiry, and uncompleted closure.

### Read Attributes from outside a Flow

Read committed Attribute values from outside a Flow with the Client Attribute read API. Go has **Client.GetAttributes** from Go SDK v1.25.0. Python has **Client.get_attributes** and **AsyncClient.get_attributes** from Python SDK v1.29.0. TypeScript has **Client.getAttributes** from TypeScript SDK v1.29.0. The Java and Rust SDKs do not have this API yet; read Flow state there through a typed read-only RPC. Inside a Step or RPC handler, read through the Context, never through a Client.

- **One server read.** The Client sends every requested key in one request, so all values come from the same commit.
- **No Worker.** Dex serves the read itself. It uses no Worker capacity and works while no Worker is available for the Flow type.
- **Closed runs.** The call reads the current run, or the latest run when no run is open. A closed run stays readable until Dex purges it. The call takes no run ID.
- **Committed state only.** Unlike an Attribute read inside an RPC handler, the call never sees writes that a running Step or RPC has staged but not committed. Hard-delete values are no exception: the call returns only committed values.
- **Batch only.** There is no single-Attribute method. Pass every Attribute and AttributeMap instance the response needs in one call. To read one value, pass one read. Never issue one call per Attribute.
- **Exact keys.** Each read is a singleton Attribute or one exact AttributeMap instance. The call cannot enumerate an AttributeMap and does not read Channels.
- **Registered definitions.** Each definition must be registered by some Flow in the Client's Registry. Go and Python compare by value: the same name, Attribute or AttributeMap kind, index, Attribute Store sync, hard-delete setting, and value type. TypeScript requires the exact registered definition object. Dex reads the key by name and does not check that the target Flow type declares it, so a same-named Attribute with another value type fails to decode.
- **Values.** The Client hydrates large values through LoadBlobs and its BlobCache before decoding. Hard-delete Attributes are readable; Dex resolves their values, and the SDK never caches them.
- **Not a wait or a status.** Use Attribute match to wait for a value, and DescribeFlow for lifecycle status.

Each SDK represents an unset Attribute or AttributeMap instance in its own way:

| SDK | Call shape | Unset value |
| --- | --- | --- |
| Go | `client.GetAttributes(ctx, flowID, dex.ReadAttribute(orderStatus, &status), dex.ReadAttributeMapInstance(notes, "n1", &note))` | The destination is unchanged. `AttributeReadResult.IsSet` or `IsMapInstanceSet` reports false. |
| Python | `values = client.get_attributes(flow_id, ORDER_STATUS, NOTES.instance("n1"))` | The key is absent from `AttributeValues`. `values[key]` raises `KeyError`, `values.get(key, default)` returns the default, and `key in values` is false. A stored value that decodes to `None` is present. |
| TypeScript | `const [status, note] = await client.getAttributes(flowId, orderStatus, notes.instance("n1"))` | The tuple element is `undefined`. Writing `null` or `undefined` deletes the value, so it then reads as `undefined`. |
| Java, Rust | Not available yet | Use a typed read-only RPC. |

Errors are typed and name the Flow ID:

- A missing Flow, or a latest run that is being purged, returns the SDK's Flow-not-found error, not the combined missing-or-inactive error. Closure is not an error. At a business Get boundary, return the contract's not-found result directly, as the [missing query target rule](error-handling.md#missing-query-targets) requires.
- A definition that no registered Flow declares, or one whose value type differs, returns the SDK's Flow definition error before any request.
- A value that cannot be decoded returns the SDK's value mapping error. When LoadBlobs omits a large value, Python and TypeScript also return their value mapping error, while Go returns a plain error.
- An empty Flow ID, no reads, or a duplicate read fails before any request: a plain error in Go, `ValueError` or `TypeError` in Python, and `TypeError` in TypeScript.
- A failed FlowService or LoadBlobs request is a service error: Go `*dex.ServiceError` or a concrete error wrapping it, Python `DexServiceError` (`FlowNotFoundError` when LoadBlobs reports a missing Flow), and TypeScript `DexServiceError` with operation `loadBlobs`. In Go, a failure that is not a gRPC status, such as a context error or an invalid response, is a plain error.
- On any error, the call returns no values and, in Go, writes no destination.

Choose GetAttributes when the caller needs the stored values of known Attributes or exact AttributeMap instances as they are. Typical uses are an entity snapshot API, a status page, a read after closure, and a read that must not depend on Worker availability. It has lower latency than an RPC because no Worker is dispatched.

Choose a typed read-only RPC when:

- Flow code must compute, validate, or reshape the response;
- the response needs pending Channel or ChannelMap messages, or every instance of an AttributeMap;
- the read must exclude concurrent writers through an Attribute lock, which requires an active Flow;
- the SDK is Java or Rust.

Write through typed RPCs or Channels; the Client has no direct Attribute write. Existing read-only RPCs keep working, and applications can replace pure-read RPCs at their own pace. Language details: [Go](../go/primitives.md#client-attribute-reads), [Python](../python/primitives.md#client-attribute-reads), [TypeScript](../typescript/primitives.md#client-attribute-reads), [Java](../java/primitives.md#client-interaction), and [Rust](../rust/primitives.md#client).

Docs: https://docs.superdurable.io/primitives/client
