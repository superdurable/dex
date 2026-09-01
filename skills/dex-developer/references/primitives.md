# Primitive selection

Read only the sections relevant to the task.

## Flow

Use a Flow as the top-level durable business execution. It owns the Step list, persistence schema, and RPC handlers. Give the Flow type and execution ID stable business meanings.

Docs: https://docs.superdurable.io/primitives/flow

## Step and Wait

Use a Step for retried background work and explicit state transitions. **WaitFor** returns Conditions; **Execute** performs work and returns a Step decision.

Use multiple next Steps for parallel work. Use cancellation deliberately when a first-winner branch makes siblings unnecessary.

Step durability resolves in this order: a method override, FlowConfig, then **SYNC**. The default retry total duration is four hours. Regular attempts default to a two-hour method timeout and one-minute heartbeat timeout.

A long-running regular attempt must emit an explicit heartbeat or Stream message before its heartbeat timeout. A heartbeat value is a retry checkpoint. An explicit valueless heartbeat clears the checkpoint; a Stream message preserves its current state. The local phase of **ASYNC** durability ignores heartbeats but still emits Stream messages.

For an LLM call that may remain healthy without output for more than one minute, tell the application developer to raise **HeartbeatTimeout** above its one-minute default. Size it to the longest acceptable silent interval, and use the method timeout to cap the whole attempt. Do not add periodic heartbeats solely to mask provider silence; they prove only that application code is running, not that the upstream request is progressing.

Docs: https://docs.superdurable.io/primitives/step

## Attribute

Use an Attribute for durable state inside one Flow execution. Register every Attribute in the persistence schema before reading or writing it.

Use one Attribute when the value is cohesive and should be replaced as a unit. Use an AttributeMap when runtime-keyed instances change independently; each instance is stored separately, avoiding a rewrite of the whole collection. Use stable domain keys and delete instances that are no longer needed.

Lock the exact AttributeMap instance when Steps or RPCs can race on it. Do not treat an AttributeMap index as an index over its instances: all instances share one Flow search field, later writes replace that field, and instance keys are not searchable. AttributeMap enumeration is not server-side pagination.

Read [large-attributes-and-locality.md](large-attributes-and-locality.md) for large values, map chunking, BlobCache locality, and external projections.

Docs: https://docs.superdurable.io/primitives/attribute

## Channel

Use a Channel for ordered, durable, typed messages scoped to one Flow execution. One matching wait consumes a message once.

Use a ChannelMap when the same message contract is partitioned by a dynamic key. Plan how externally published messages are drained before Flow completion.

Docs: https://docs.superdurable.io/primitives/channel

## RPC

Use an RPC for a typed request/response interaction with an active Flow. RPC handlers may read or update Attributes and publish Channels. Protect shared mutations with Attribute locks when they can race with Steps or other RPCs.

Use a Channel instead when the caller should enqueue work without synchronous application-level handling.

Docs: https://docs.superdurable.io/primitives/rpc

## Stream

Use a Stream for low-latency, best-effort, resumable updates such as progress displayed in a UI. Do not use a Stream when delivery must be durable; use a Channel or Attribute instead.

A Step may append any number of messages to the same or different Streams before its final result. A Step Stream write is fire-and-forget: local encoding or registration can fail immediately, but Dex Server does not acknowledge Stream Store persistence and a Store failure does not fail the Step.

Step messages use **#StepExecutionID** as source metadata. The source is not an idempotency key: attempts and messages may share it, and every write appends. Client Stream writes require a nonempty source, which may repeat or contain **#**.

Docs: https://docs.superdurable.io/primitives/stream

## Timer

Use a Timer Condition for a durable delay, reminder, deadline branch, or scheduling loop. Decide what happens if a timer is skipped and whether the business deadline should complete, cancel, fail, or route to a handler.

Docs: https://docs.superdurable.io/primitives/timer

## SubFlow

Use a SubFlow for child work that benefits from a separate Flow identity and lifecycle. Bound parallel SubFlows and define their reuse, cancellation, and parent-completion behavior.

Docs: https://docs.superdurable.io/primitives/subflow

## Client

Use the Client at application boundaries to start, stop, inspect, search, and interact with Flows. Handle typed SDK failures for duplicate starts, missing Flows, closed Flows, long-poll expiry, and uncompleted closure.

Docs: https://docs.superdurable.io/primitives/client
