# Agent self-check

Run the design review before you implement a new Dex application, library, or design change. Run the code check before you hand off. Answer each item in one line in the design document or the handoff.

## Design review

1. **Prior art.** Search the runnable [examples](https://github.com/superdurable/dex/tree/main/examples), [patterns.md](patterns.md), and any Dex-native reference the user has. Name the nearest one and say what the design keeps and changes.
2. **Premises.** Every "Dex can X" or "Dex can't X" claim cites a source file or test in the installed SDK version, or the matching Developer reference. An unverified premise is a question for the owner, not a constraint.
3. **Primitive check.** Before building a workaround, check whether a Dex primitive covers the need: canceling Steps, chunked or hash-partitioned AttributeMaps, Attribute locks, durable Timers, Channels with atomic complete-if-empty, and SubFlows.
4. **Persisted layout.** Settle Flow, Step, RPC, Attribute, AttributeMap, Channel, and Stream names, instance keys, chunk size, partition count, hash algorithm, value schemas, and RPC types before the first release. Changing them later means reading both forms while old Flows or values exist.
5. **Existing features.** List the features of the nearest example or the version being replaced. Drop one only with an explicit, recorded decision.
6. **Owner review.** Get the owner's review of the premises and decisions before implementation starts, and record the outcome.

## Model before implementation

State, for every non-trivial Flow:

- [ ] Flow type, Flow ID derivation, and lifecycle
- [ ] typed start input and completion output
- [ ] Steps, transitions, and each Step's recovery boundary
- [ ] Flow durability default and method overrides
- [ ] method timeouts, heartbeat timeouts, retry budgets, and failure routes
- [ ] durable state: Attributes and AttributeMap layouts
- [ ] messages: Channels and their producers and consumers
- [ ] synchronous RPCs, their locks and loads
- [ ] best-effort Streams
- [ ] Timers, deadlines, and the Flow timeout
- [ ] SubFlows, only after the [evolution gate](subflow.md#evolution-gate)

## Code check

Modeling:

- [ ] Each Flow type is named after its domain and unique in the registry.
- [ ] Every RPC name starts with a concrete action verb. Names are complete and precise, not generic.
- [ ] Every top-level Flow boundary is justified; no pair of Flows calls each other on frequent events.
- [ ] One owning Flow starts dependent work; the API handler never chains starts.
- [ ] No external polling in WaitFor or through a Timer loop; external waits use the Polling pattern.
- [ ] No other Flow is started or mutated from WaitFor.

State:

- [ ] Every Attribute, AttributeMap, Channel, ChannelMap, and Stream is in the persistence schema.
- [ ] Durable state lives in Dex unless a confirmed requirement needs another store; each fact has one authority.
- [ ] Growing collections use bounded AttributeMap layouts.
- [ ] Racing read-modify-writes lock the exact instance; map-wide invariants use one shared singleton lock.
- [ ] Every dynamic read-modify-write selects the instance as lock and load.
- [ ] Application indexes use generic typed slots, and slot searches constrain FlowType.
- [ ] No secrets in regular Attributes, indexes, Streams, or logs.

Execution:

- [ ] Every external effect is idempotent, keyed by a stable ID, or compensated.
- [ ] Long-running Execute heartbeats or streams within its heartbeat timeout and honors cancellation.
- [ ] SYNC is used where a replay would be costly or unsafe, and wherever recovery relies on NOT_STARTED.
- [ ] Retry budgets are bounded; a method timeout bounds each attempt.
- [ ] No correctness state lives only in Worker memory.

Boundary:

- [ ] StartFlow is called first with a stable Flow ID, Request ID, and reuse policy; no preflight or verification read.
- [ ] Milestone-confirming APIs wait for a condition published after the milestone commits, with a bounded wait.
- [ ] Each read after a write is classified; strong pairs are read once without polling.
- [ ] Terminal snapshot reads use a query-only RPC or a Client Attribute read, with no history fallback.
- [ ] A query-only Get maps missing or not-active directly to not found.
- [ ] Only concrete SDK errors are caught; local defects stay visible.
- [ ] Streams are treated as best effort.

Wiring and tests:

- [ ] Worker and Client share one registry and payload configuration; the Worker waits for registration and stops gracefully.
- [ ] Build or type-check passes.
- [ ] A real-Server integration test covers the main path, Worker replacement at a wait, retry and recovery, duplicate requests, terminal behavior, and heartbeat cadence. See [testing.md](testing.md).
- [ ] Stable names stay compatible with open Flows, or a migration is explicit.
