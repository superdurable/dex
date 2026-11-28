# Attribute and AttributeMap

An Attribute is typed durable state inside one Flow run. Later Steps, RPCs, Client reads, search, and recovery read it. Dex persists every committed write.

Use Attributes for current state. Use Channels for ordered durable messages. Do not use a Channel as storage.

## Lifetime and schema

- Register every Attribute and AttributeMap in the Flow's persistence schema before reading or writing it.
- An Attribute lives as long as its Flow run. Dex deletes it when retention removes the Flow. Data that must outlive the Flow needs [Attribute Store sync](#attribute-store-sync) or an application database.
- Writing null deletes the Attribute. A deleted or never-written Attribute reads as unset.
- Initial values passed at StartFlow commit with the start. The first Step sees them.
- Attribute names are durable data for open Flows. Do not rename them as a refactor. See [application.md](application.md#versioning-flow-code).

## Attribute or AttributeMap

| Use | When |
| --- | --- |
| One Attribute | The value is cohesive and is read and replaced as a unit. |
| AttributeMap | Keys are known only at runtime, and instances are read, written, or deleted independently. Each instance is stored separately, so changing one does not rewrite the others. |

- Instance keys are never encrypted and each instance has its own overhead, so usually use stable domain keys or monotonic chunk IDs, not volatile or unbounded user text.
- Instance keys cannot contain `/`.
- Usually delete instances the Flow no longer needs, so its active state stays under the [limit](../../SKILL.md#default-limits).
- Group very small records into bounded chunks when per-instance overhead would dominate.
- Map size and key enumeration inside a handler include its own buffered writes. They are not server-side pagination. An unbounded map is expensive to enumerate, transfer, and decode.
- For growing collections, use the [sequentially chunked](patterns/flow-shapes-and-state.md#sequentially-chunked-attributemap) or [hash-partitioned](patterns/flow-shapes-and-state.md#hash-partitioned-attributemap) layouts.

## Loading

- Steps, timeout handlers, and RPCs receive ordinary Attribute values automatically.
- AttributeMap values must be selected explicitly: exact instances for known keys, the whole map only to enumerate it.
- Map size and the list of instance keys need a whole-map load. With only exact instances loaded, they raise the not-loaded error. To test whether one known key exists, load that instance and read it.
- Reading an unselected instance raises the SDK's not-loaded error.
- A load controls data transfer only. It does not lock, and it does not make an RPC transactional or isolated.

## Locks

Attribute locks serialize Steps and RPCs that declare the same lock within one Flow run. Dex acquires and releases them around the invocation.

- Lock the exact AttributeMap instance when parallel Steps or RPCs read-modify-write the same value. Unrelated instances stay concurrent.
- Without a lock, a read-modify-write can lose an update, and a writer that skips the lock bypasses it; see [Commits and isolation](../../SKILL.md#commits-and-isolation) and [Locks](../../SKILL.md#repeated-execution-snapshots-and-locks).
- A free lock goes to an RPC ahead of queued Steps, so a long locked RPC delays them; usually keep locked RPCs short. Lock order is in the [Attribute lock reference](https://docs.superdurable.io/reference/server/runtime-data#lock-order).
- A lock does not load data. A read-modify-write of a dynamic instance selects that instance both as a lock and as a load.
- An RPC that needs a held lock fails at once with the RPC lock-conflict error; Dex does not queue it. The holder can be a Step in retry backoff, because a method keeps its locks until the method call ends. Retry with bounded backoff, or return a busy result to the caller; either way keep the RPC idempotent.
- A Step takes its locks when Dex hands its attempt to a Worker. See [step.md](step.md#retry-snapshots-and-locks) for queued and retrying attempts.
- A Step keeps its locks through external calls and retries, about 4 hours under the default retry policy, and every RPC that needs one fails with a lock conflict meanwhile. So when an Execute declares a lock that an RPC also takes, usually decide and commit under the lock in that Step, then make the external call in the next, unlocked Step. The same applies to a WaitFor or a Flow timeout handler that declares such a lock, and starting or calling another Flow counts as an external call.
- A lock that only Steps share may cover an external call; those Steps run one at a time. A lock on long parallel branches therefore serializes them, and a failing Step keeps the lock through its retries.
- When an RPC stops an action that a Step decides, such as a reminder, choose the design by what the business promises:

  | The business accepts | Design |
  | --- | --- |
  | The decided action may still happen once after the stop | No lock. The stop RPC publishes the stop and answers "received". The deciding Execute checks the stop Channel, also when a Timer was selected first, and every later interruptible wait includes the stop Condition. The action carries a stable occurrence ID that the receiver deduplicates. |
  | No action may be decided after the stop RPC returns | The stop RPC and the deciding Step share a lock. The deciding Step commits the decision under the lock only when no stop is committed, and an unlocked Step acts after the commit. |

  Parallel waiters and the per-branch grace follow the [Reminder](patterns/polling-and-timers.md#reminder) rules.

### Whole-map coordination

When a write invariant spans several instances or the whole map, register a separate singleton bool Attribute, not indexed, as the coordination lock. For example, register `currentMessages` (an AttributeMap) and `currentMessagesLock` (a singleton bool Attribute).

- Every Step, timeout handler, and RPC that changes the protected map declares that singleton lock in its options.
- A writer that takes only an instance lock does not conflict with the singleton lock and can break the invariant.
- The bool value is not the lock state. Never read, toggle, or release it by hand. It needs no stored value.
- The lock serializes writers within one Flow run only.
- Use instance locks for independent updates and the singleton lock for a map-wide invariant.

## Indexed Attributes and search

An Indexed Attribute is copied to a Flow search field so SearchFlows, `dexcli flow search`, and Dex Web can find Flows by it. Search is eventually consistent. See [client.md](client.md#read-after-write).

Index keys share the Server's finite search capacity, the slot registry has no delete, some search backends cap slots per type, and renaming an index key does not backfill existing Flows ([Attributes](../../SKILL.md#attributes-loading-and-secrets)). So usually assign each application index an explicit generic key from a typed slot pool and reuse slots across Flow types, instead of a business name or the default Attribute-derived key:

```text
keyword1, keyword2, ...    keywordList1, keywordList2, ...
text1, text2, ...          int1, int2, ...
bool1, bool2, ...          double1, double2, ...
datetime1, datetime2, ...
```

- Keep the Attribute name meaningful to the business. Only its physical search key is generic.
- A reused slot may carry a different business meaning in each Flow type of the same index type.
- Two indexed fields in one Flow need distinct slots.
- Dex system indexes keep their reserved names.
- Keep one index type per key.
- When a query on a generic slot is for one Flow type, constrain FlowType, or it mixes Flow types that reuse the slot; an intentional cross-type query must account for each type's slot meaning. Combine that with the caller's filter in parentheses so an OR cannot bypass it.
- Indexed values are never encrypted; see [payload encryption](../../SKILL.md#attributes-loading-and-secrets). Never index secrets. Index personal data only when the application accepts it stored in plaintext on the Server; otherwise index an opaque derived key.
- Index registrations are permanent on a Server. A test or abandoned registration uses a slot for good. Some search backends cap slots per type and the length of an indexed Keyword value; see the [Server search reference](https://docs.superdurable.io/reference/server/search). Index long text as Text, or index a short derived key.
- Renaming an index key does not backfill existing Flows. Plan a migration for open Flows.
- Keep the SearchFlows query identical across pages when you pass a page token.

An indexed AttributeMap does not create one search entry per instance. All instances share one search field, a later write replaces it, and instance keys are not searchable. For a bounded searchable collection, index a regular Attribute as a keyword list. For per-instance queries, use an external projection.

## Hard-delete Attributes

Use a hard-delete Attribute or AttributeMap for a short-lived secret that the Flow itself must hold, such as an API key it uses a few times and then discards. Long-lived credentials stay outside Flow state; see [Attributes, loading, and secrets](../../SKILL.md#attributes-loading-and-secrets).

- Dex keeps the value out of Flow history. Deleting it, or writing null, permanently deletes the stored value.
- Delete the secret as soon as the Flow no longer needs it.
- A write becomes readable only when its method commits, like an ordinary write; a failed or uncommitted attempt leaves the stored value unchanged. Each run has its own values, so a later run under the same Flow ID does not see an earlier run's secret.
- It cannot be indexed, synced to an Attribute Store, or used in an Attribute match or an Action condition. Keep a revision in a regular Attribute when something must wait on a change.
- Time travel never changes a hard-delete Attribute: it keeps its current value or absence, so a re-run Step can read a newer value than the restored point had, or none. When the value can be absent, design the Flow to request a fresh secret.

Value types, write paths, and the other edge cases are in the [hard-delete Attributes reference](https://docs.superdurable.io/reference/server/runtime-data#hard-delete-attributes).

## Attribute Store sync

Attribute Store sync projects selected Attribute writes into PostgreSQL tables that the application owns. Each destination is an Attribute Store. It is a Dex Server feature, not a Worker feature. The application and its reports query the tables with SQL.

- The Flow stays the source of truth. The table is an eventually consistent latest-state projection that can repeat and drop writes ([Attributes](../../SKILL.md#attributes-loading-and-secrets)), so a Flow decision reads the Flow's own state, not the table.
- A Flow opts in per Attribute or AttributeMap, and names the stores in its Flow configuration at start.
- Dex never creates, migrates, or backfills a destination; the application owns the tables and their retention.
- Delivery is asynchronous and at least once. A write can be dropped after its retries, and a projection failure never rolls back Flow state, so make important projections reconcilable from the Flow.
- A row key template makes the Flow ID format a storage contract. Settle both before the first write.
- Change a projected value only through the Flow; the application may still purge rows by its own retention.

Store types, row keys, column mapping, AttributeMap tables, schema changes, Flow close, the fencing table, and metrics are in the [Server Attribute Store reference](https://docs.superdurable.io/reference/server/attribute-store-sync). For local development, `dexcli dev` accepts an Attribute Store configuration file; see the [CLI reference](https://docs.superdurable.io/reference/cli/#start-locally).

## Reading and writing from outside a Flow

- Write only through typed RPCs; an RPC may publish to a Channel. The Client has no direct Attribute write.
- Read stored values with the Client Attribute read, or with a typed read-only RPC when Flow code, pending messages, or a whole map are needed. See [client.md](client.md#read-attributes-from-outside-a-flow).

## Self-check

- Is every Attribute in the persistence schema, with a stable name?
- Is each growing collection a bounded AttributeMap layout, not one ever-growing value?
- Does every racing read-modify-write lock the exact instance, and every map-wide invariant use the shared singleton lock?
- Does every dynamic read-modify-write select the instance as both lock and load?
- Does every search over a reused generic slot constrain FlowType?
- Are secrets kept out of indexes, regular Attributes, logs, and Streams, and is personal data indexed only where plaintext on the Server is accepted?
- Is the Flow the only writer of each projected fact?
- Can the projection be reconciled after a dropped write?
- Does no Flow decision read the projection?
