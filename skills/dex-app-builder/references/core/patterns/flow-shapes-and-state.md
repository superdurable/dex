# Flow shape and state patterns

Part of the [design patterns](../patterns.md), which hold the pseudocode conventions and the runnable examples.

## Workflow DSL

Derived from: [Conditions](../../../SKILL.md#conditions-and-channel-messages), [Repeated execution](../../../SKILL.md#repeated-execution-snapshots-and-locks), [Joins](../../../SKILL.md#joins), [Workers](../../../SKILL.md#workers-attempts-and-time-bounds), the [Must](../../../SKILL.md#must).

Use when operators, customers, partners, or another system must define workflows at runtime from the same set of actions and external conditions. Give definitions a serializable schema, usually JSON, and version them in the application's own definition store. Users can edit, copy, import, and export definitions. A new JSON document adds a workflow without a code change or release. One interpreter Flow runs them all. Go folder: `products/deal-dsl`.

```text
SaveDefinition(definition) -> validate and save a version in the application store
StartWorkflow(version) -> load and validate the definition, then start the interpreter
Start.execute(definition) -> save the snapshot and initial state data; goTo(WaitForState(initialState))
ReceiveCondition.rpc(name, message) -> validate name against the definition; publish to Conditions[name]
WaitForState.waitFor(state) -> wait for one message on Conditions[precondition], if declared
WaitForState.execute(state) -> merge the message; goTo(RunAction(state, 0) or EvaluateTransition(state))
RunAction.execute(state, index) -> run one action; merge the result; goTo(next action or transition)
EvaluateTransition.waitFor(state) -> wait for one message on Conditions[transitionCondition], if declared
EvaluateTransition.execute(state) -> merge the message; choose the next state from shared data;
                                     goTo(WaitForState(nextState)) or complete
```

- Reject an invalid definition at the application boundary, before StartFlow.
- Keep one active position per branch; a run without parallel branches has exactly one. Use a fixed set of Step types for startup, waiting before state entry, running one action, and evaluating a transition. Advance with `GoTo`. After startup, pass only the position in Step input, such as state name and action index.
- Give each action its own Step execution, checkpoint, and retries. A retry leaves earlier completed actions intact. Run the registered handler or script in `Execute`. Treat scripts as external calls with timeouts, retries, and idempotency.
- Declare one `ChannelMap` in the Flow's persistence schema in code. Use the condition names supplied at runtime as keys. The receiving RPC validates the name against the definition and publishes to that key. Each wait takes one message.
- Keep one shared state data map. Merge condition messages and action results into it and read it when evaluating transitions.
- Let the DSL define progress, such as the current state, current step, or a custom stage. Write progress to Attributes for the Client to read. Use Attribute Sync when progress needs to be searchable. The Flow graph shows the interpreter Steps, and the Attributes show a run's business position.

Choose how a running Flow reads the definition:

- **Snapshot at start.** Use this basic approach when edits apply to new runs. The start Step stores the full definition in an Attribute. Later Steps read only that copy, so running Flows keep their original definition. The example uses this approach.
- **Read the latest definition.** If edits must reach running Flows, read the store in `Execute` at every state entry or transition evaluation. Treat the read as an external call that can fail and retry. A newer version can rename or remove the current state or remove an awaited condition. Define compatibility rules or migrate the saved position between versions.

For parallel branches, use `GoToMany` to start them with a separate position in each branch's Step input. Bring branches together at a Join. Protect concurrent writes to shared state with an Attribute lock, or let each branch write its own data and merge it after the Join.

Anti-patterns:

- **One Channel per condition.** Channel declarations live in code, and the workflow provides condition names at runtime. Use keys on the shared `ChannelMap`.
- **Actions or state changes in an RPC, in this interpreter pattern.** Keep the RPC to validation and publishing. Perform actions and update state in the interpreter Steps.
- **Flow ID and action name as the key for a repeated action.** In a request-documents loop, the provider receives `claim-77/send-letter` for both letters and drops the second as a repeat. Use the Flow ID and `StepExecutionID`, as in `claim-77/ExecuteClaimAction-5`. Each round gets a new execution ID, and retries of that round keep it.

## Root Flow admission

Derived from: [Starting a Flow and ID reuse](../../../SKILL.md#starting-a-flow-and-id-reuse).

Start one domain-named root Flow directly from the application boundary.

- Use a stable Flow ID for the logical operation and a Request ID derived from the complete start request. Set a reuse policy from the [ID reuse table](../../../SKILL.md#starting-a-flow-and-id-reuse) when the default does not fit the lifecycle.
- A retry of the same request reuses both identities. Under a policy that rejects the existing run, the same Flow ID with a different Request ID is another logical request, unless the domain intentionally routes commands to one coordinator. A deliberate reopen after close is a new request with a new Request ID.
- When Dex owns the triggering fact and retained Flow identity covers the required retry window, start directly; an extra admission store, such as a table, outbox, lease, or cache, adds no guarantee. An external authoritative transaction or a deduplication lifetime longer than the Flow's [retention](../../../SKILL.md#starting-a-flow-and-id-reuse) may need its own durable handoff. When the uniqueness key is not the Flow ID, route starts through a Flow whose ID is that key. Store request fingerprints only when they are real fields of the owning domain record.
- When the caller needs durable acceptance before completion, wait for the named admission Step, then read the domain record.

Full rules: [error-handling.md](../error-handling.md#start-identity-and-duplicate-starts).

## Entity Flow with Attribute Sync

Derived from: [Starting a Flow](../../../SKILL.md#starting-a-flow-and-id-reuse), [Locks](../../../SKILL.md#repeated-execution-snapshots-and-locks), [Runs and reads](../../../SKILL.md#runs-reads-and-outputs), [Attributes](../../../SKILL.md#attributes-loading-and-secrets).

One Flow per entity, whose Attributes the Attribute Store syncs into a SQL table for queries. Go folder: `entity-store`.

The plain Entity Flow is the same shape without the SQL table: one long-lived Flow per entity, with a Flow ID from the entity ID, state in Attributes, changes through locked RPCs, and reads through the Client. Runnable examples: [Go](https://github.com/superdurable/dex/tree/main/examples/go/patterns/entity-flow), [Python](https://github.com/superdurable/dex/tree/main/examples/python/dex_examples/patterns/entity-flow), [Java](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/superdurable/dex/patterns/entityflow), [TypeScript](https://github.com/superdurable/dex/tree/main/examples/typescript/src/patterns/entity-flow), [Rust](https://github.com/superdurable/dex/tree/main/examples/rust/src/data_storage/entity_flow).

- Use when an entity needs durable state and RPC updates, and the application needs database queries over latest state.
- The Flow ID is the entity ID. It becomes the table's primary key, or its key columns with a row key template.
- Mark the projected Attributes for sync, and select the Attribute Store in the Flow configuration at start.
- Update through a typed RPC that validates, then writes every projected Attribute. Read authoritative state from the Flow.
- The SQL table is an asynchronous latest-state projection: it lags commits, a write can repeat, and entries still pending after the retry budget are dropped ([Attributes](../../../SKILL.md#attributes-loading-and-secrets)). So a Flow decision reads the Flow's own state, not the table. Deleting an Attribute keeps the row and projects NULL.
- Define optimistic concurrency, deletion, retention, and open-Flow compatibility. See [attribute.md](../attribute.md#attribute-store-sync).

## Partitioned AttributeMap state

Derived from: [Default limits](../../../SKILL.md#default-limits), [Loading](../../../SKILL.md#attributes-loading-and-secrets), [Locks](../../../SKILL.md#repeated-execution-snapshots-and-locks).

A single Attribute that grows rewrites its whole value on every write. Split it into bounded AttributeMap instances.

| | Sequentially chunked | Hash-partitioned |
| --- | --- | --- |
| Fits | append-only ordered history: messages, audit events, subscriber history | keyed records with direct lookup and update: profiles, account metadata |
| Contention | every append contends on the active chunk | independent per-partition locks; hot keys make hot partitions |
| Old data | archived chunks are immutable | any record can be updated |
| Fixed layout | chunk size | partition count and hash algorithm |

Neither creates server-side map pagination or a secondary index. The layout is persisted schema: changing it needs a migration or a new Flow version.

### Sequentially chunked AttributeMap

Derived from: [Default limits](../../../SKILL.md#default-limits), [Loading](../../../SKILL.md#attributes-loading-and-secrets), [Locks](../../../SKILL.md#repeated-execution-snapshots-and-locks).

Go folder: `sequentially-chunked-attribute-map`.

```text
state: Attribute { nextSequence, newestArchivedToken, oldestRetainedToken }
chunks: AttributeMap; instance "current" is the only mutable chunk

rpc appendRecord(record)       // locks state and chunks["current"]; loads chunks["current"]
    current.append(record)
    if current is full:
        chunks[zeroPadded(firstSequence)] = current     // archive, immutable
        chunks["current"] = empty
        state.newestArchivedToken = zeroPadded(firstSequence)
        bound the total:
            delete chunks[token] for each token past the count or age limit, by key,
                after archiving it elsewhere when the business must keep it
            state.oldestRetainedToken = oldest token still kept
    state.nextSequence += 1                             // all in one commit

rpc getPage(token)             // invocation option loads only chunks[token]
    if token is older than state.oldestRetainedToken: return page not found
    return records newest first, plus the next older token
           (no older token at state.oldestRetainedToken)
```

- Use it for a collection that can grow without a small business bound; a collection with a small bound fits in one Attribute.
- For an append-only history, archive bounded chunks and load only the requested page.
- For a long-lived Flow, also bound the total, because one value and a run's active state each have a [size limit](../../../SKILL.md#default-limits). Choose a limit by chunk count or age. Archive the full chunk, delete the chunks past the limit by key, after archiving them outside the Flow when the business must keep them, and record the oldest retained token in one commit. Retained pages are immutable. At the oldest retained chunk, return no older token. A request for a pruned token returns a page-not-found result.
- The `current` token is always valid; an empty `current` returns an empty page.
- Retry only the rejected invocation after a lock conflict. A few writers share this lock on `current`.
- Updating old records needs another pattern or an index.
- Route frequent concurrent appenders through an RPC that publishes to a Channel consumed by one writing Step, as in the [Idle Timer with Resolution](polling-and-timers.md#idle-timer-with-resolution), instead of having each RPC lock and rewrite `current`.

### Hash-partitioned AttributeMap

Derived from: [Default limits](../../../SKILL.md#default-limits), [Loading](../../../SKILL.md#attributes-loading-and-secrets), [Locks](../../../SKILL.md#repeated-execution-snapshots-and-locks).

Go folder: `hash-partitioned-attribute-map`.

```text
// Persisted layout: changing PARTITION_COUNT moves keys to other partitions.
const PARTITION_COUNT = 16

partition(key):
    canonical = trim and lowercase key; reject empty or invalid input
    return "partition-" + format(versionedHash(canonical bytes) % PARTITION_COUNT)

rpc upsertProfile(profile)     // invocation options: lock and load chunks[partition(profile.email)]
    bucket = load that instance, or an empty dictionary
    bucket[canonical email] = profile
    write the instance back

rpc getProfile(email)          // invocation options: load only chunks[partition(email)]
    return bucket[canonical email] or not found
```

- The partition count and hash are persisted layout. Changing them needs a complete rehash or a new Flow version.
- Every writer uses the same instance-lock rule. Locks are cooperative.
- Retry an idempotent upsert after a lock conflict.
- For a map-wide invariant, use the singleton coordination lock in [attribute.md](../attribute.md#whole-map-coordination).
