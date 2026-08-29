# Primitive selection

Read only the sections relevant to the task.

## Flow

Use a Flow as the top-level durable business execution. It owns the Step list, persistence schema, and RPC handlers. Give the Flow type and execution ID stable business meanings.

Docs: https://docs.superdurable.io/primitives/flow

## Step and Wait

Use a Step for retried background work and explicit state transitions. **WaitFor** returns Conditions; **Execute** performs work and returns a Step decision.

Use multiple next Steps for parallel work. Use cancellation deliberately when a first-winner branch makes siblings unnecessary.

Docs: https://docs.superdurable.io/primitives/step

## Attribute

Use an Attribute for durable state inside one Flow execution. Register every Attribute in the persistence schema before reading or writing it.

Use an AttributeMap for a dynamic set of keyed values. Use indexes for search and locks when concurrent handlers update shared state.

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
