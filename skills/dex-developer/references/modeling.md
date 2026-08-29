# Flow modeling

Use this guide before implementing a non-trivial Flow or when a Flow has become difficult to reason about.

## Find the Flow boundary

A Flow should represent one durable business execution with a stable identity and lifecycle, such as an order, subscription, transfer, approval, or processing job.

Prefer a SubFlow when work needs its own identity, lifecycle, retry boundary, independent scaling, or bounded fan-out. Prefer another Step when the work is only the next state in the same business execution.

## Turn the business process into a Step graph

For each Step, record:

- typed input
- Conditions returned by **WaitFor**
- side effects performed by **Execute**
- Attribute and Channel reads or writes
- retry and timeout policy
- success transition
- exhausted-retry or business-failure transition

Use stable domain names. A Step type is part of the durable contract of open executions, not merely a function name.

## Separate waiting from work

**WaitFor** declares when a Step may execute. It may prepare durable state needed to establish that wait. Do not call third-party services or perform irreversible side effects there.

**Execute** performs application work and returns the next movement or terminal decision. Make external operations idempotent when Dex may retry the Step.

## Persist only durable coordination state

Use Attributes for state that later Steps, RPCs, Clients, search, or recovery need. Keep authoritative long-lived business records in the application's database when their lifetime exceeds the Flow or they need relational querying.

Use Indexed Attributes for bounded lookup and operational search. Use Attribute Store sync when an application-owned database needs a durable projection.

Do not use Channels as generic storage. A Channel message is consumed by a matching wait. Use Attributes for current state and Channels for ordered durable messages.

## Make failure behavior part of the graph

For every external side effect, decide:

- which failures are retryable
- total retry budget and backoff
- whether the operation needs heartbeat/progress reporting
- what happens after retry exhaustion
- whether compensation is required
- whether an operator can recover the Flow
- what deadline ends the business process

Represent compensation and operator recovery as explicit Steps. Do not catch every error and silently mark the Flow successful.

## Design for open executions

Running Flows can outlive a deployment. Prefer additive changes:

- add new Step types instead of changing incompatible behavior in an existing type
- add optional Attributes and RPCs
- retain old Step implementations while open executions can reference them
- version request schemas or RPC names when compatibility cannot be preserved

Use a new routing flag or Attribute so only new executions enter an incompatible path.

## Review checklist

- Can the graph explain every success, wait, failure, cancellation, and timeout path?
- Are Step inputs and transitions typed?
- Are side effects idempotent under retry?
- Does each Channel have one clear producer/consumer contract?
- Are shared state changes protected when concurrent Steps or RPCs can race?
- Is fan-out bounded?
- Can an operator identify the current business state from Dex Web or dexcli?
