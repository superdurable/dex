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

- Use stable domain keys or monotonic chunk IDs as instance keys. Do not put volatile or unbounded user text in keys.
- Instance keys cannot contain `/`.
- Delete instances that are no longer needed.
- Group very small records into bounded chunks when per-instance overhead would dominate.
- Map size and key enumeration inside a handler include its own buffered writes. They are not server-side pagination. An unbounded map is expensive to enumerate, transfer, and decode.
- For growing collections, use the [sequentially chunked](patterns.md#sequentially-chunked-attributemap) or [hash-partitioned](patterns.md#hash-partitioned-attributemap) layouts.

## Loading

- Steps, timeout handlers, and RPCs receive ordinary Attribute values automatically.
- AttributeMap values must be selected explicitly: exact instances for known keys, the whole map only to enumerate it.
- Map size and the list of instance keys need a whole-map load. With only exact instances loaded, they raise the not-loaded error. To test whether one known key exists, load that instance and read it.
- Reading an unselected instance raises the SDK's not-loaded error.
- A load controls data transfer only. It does not lock, and it does not make an RPC transactional or isolated.

## Locks

Attribute locks serialize Steps and RPCs that declare the same lock within one Flow run. Dex acquires and releases them around the invocation.

- Lock the exact AttributeMap instance when parallel Steps or RPCs read-modify-write the same value. Unrelated instances stay concurrent.
- Without a lock, a read-modify-write can lose an update. Dex commits a write without checking that the values the handler read are still current.
- Locks are cooperative. Every writer that must be serialized declares the same lock. A writer that skips it bypasses the protection.
- Keep locked RPCs short, so they do not delay the Steps that wait for the lock. Lock order is in the [Attribute lock reference](https://docs.superdurable.io/reference/server/runtime-data#lock-order).
- A lock does not load data. A read-modify-write of a dynamic instance selects that instance both as a lock and as a load.
- An RPC that needs a held lock fails at once with the RPC lock-conflict error. It is not queued. Retry it with bounded backoff and keep it idempotent. The holder can be a Step in retry backoff, because a method keeps its locks until the method call ends.
- A Step takes its locks when Dex hands its attempt to a Worker. See [step.md](step.md#retry-snapshots-and-locks) for queued and retrying attempts.
- When an Execute declares a lock that an RPC also takes, do not call an external system in that Execute. Decide and commit under the lock in that Step, then make the call in the next, unlocked Step. Otherwise every RPC that needs the lock fails with a lock conflict while the call and its retries run. The same applies to a WaitFor or a Flow timeout handler that declares such a lock, and starting or calling another Flow counts as an external call.
- A lock that only Steps share may cover an external call; those Steps run one at a time. A lock on long parallel branches therefore serializes them, and a failing Step keeps the lock through its retries. A polling Step never holds a lock that its deadline watchdog also needs, because the watchdog would wait until the loop ends.
- When the business accepts that an already-decided action may still happen once, no lock is needed: the RPC publishes its request, such as a stop, and answers "received". The decision point is the deciding Execute's first state snapshot, which its retries reuse. Check the stop in that snapshot, including in the branch where a Timer was selected first. A stop committed after the snapshot may let that already-decided occurrence happen once; every later interruptible wait still includes the stop Condition. One message reaches one consumer, so parallel consumers each get a stop message on their own Channel, such as one per branch, which wakes a waiting branch. A durable stop Attribute records the fact but wakes no waiting branch, because WaitFor has no Attribute Condition; reading it when another event resumes the branch fits only when the business accepts that the wait is not interrupted at once. The grace is per decided action or branch, and when the business allows only one such action overall, decide actions one at a time. The decided action carries a stable occurrence ID that the receiver recognizes and deduplicates.
- When the business deadline is the decision commit itself, the stop RPC and the deciding Step share a lock, and an unlocked Step sends after the commit.

### Whole-map coordination

When a write invariant spans several instances or the whole map, register a separate singleton bool Attribute, not indexed, as the coordination lock. For example, register `currentMessages` (an AttributeMap) and `currentMessagesLock` (a singleton bool Attribute).

- Every Step, timeout handler, and RPC that changes the protected map declares that singleton lock in its options.
- A writer that takes only an instance lock does not conflict with the singleton lock and can break the invariant.
- The bool value is not the lock state. Never read, toggle, or release it by hand. It needs no stored value.
- The lock serializes writers within one Flow run only.
- Use instance locks for independent updates and the singleton lock for a map-wide invariant.

## Indexed Attributes and search

An Indexed Attribute is copied to a Flow search field so SearchFlows, `dexcli flow search`, and Dex Web can find Flows by it. Search is eventually consistent. See [client.md](client.md#read-after-write).

Assign every application index an explicit generic key from a typed slot pool:

```text
keyword1, keyword2, ...    keywordList1, keywordList2, ...
text1, text2, ...          int1, int2, ...
bool1, bool2, ...          double1, double2, ...
datetime1, datetime2, ...
```

- Keep the Attribute name meaningful to the business. Only its physical search key is generic.
- Reuse each slot across Flow types for the same index type, even when the business meaning differs.
- Two indexed fields in one Flow need distinct slots.
- Do not use business names or the default Attribute-derived key for application indexes. Dex system indexes keep their reserved names.
- Keep one index type per key.
- A search that uses a generic slot must constrain FlowType. Combine that with the caller's filter in parentheses so an OR cannot bypass it.
- Never index secrets or personal data.
- Index registrations are permanent on a Server. A test or abandoned registration uses a slot for good. Some search backends cap slots per type and the length of an indexed Keyword value; see the [Server search reference](https://docs.superdurable.io/reference/server/search). Index long text as Text, or index a short derived key.
- Renaming an index key does not backfill existing Flows. Plan a migration for open Flows.
- Keep the SearchFlows query identical across pages when you pass a page token.

An indexed AttributeMap does not create one search entry per instance. All instances share one search field, a later write replaces it, and instance keys are not searchable. For a bounded searchable collection, index a regular Attribute as a keyword list. For per-instance queries, use an external projection.

## Hard-delete Attributes

Use a hard-delete Attribute or AttributeMap for a short-lived secret that the Flow itself must hold, such as an API key it uses a few times and then discards. Keep long-lived credentials in the deployment's secret management.

- Dex keeps the value out of Flow history. Deleting it, or writing null, permanently deletes the stored value.
- Delete the secret as soon as the Flow no longer needs it.
- The value is written before the attempt commits, so a failed attempt's write can be visible. Make handlers safe to repeat, and never read-modify-write a hard-delete Attribute.
- It cannot be indexed, synced to an Attribute Store, or used in an Attribute match or an Action condition. Keep a revision in a regular Attribute when something must wait on a change.
- When the value can be absent, such as after time travel, design the Flow to request a fresh secret.

Value types, write paths, and the other edge cases are in the [hard-delete Attributes reference](https://docs.superdurable.io/reference/server/runtime-data#hard-delete-attributes).

## Attribute Store sync

Attribute Store sync projects selected Attribute writes into PostgreSQL tables that the application owns. Each destination is an Attribute Store. It is a Dex Server feature, not a Worker feature. The application and its reports query the tables with SQL.

- The Flow stays the source of truth. The table is an eventual, latest-state projection. Never read it to make a Flow decision.
- A Flow opts in per Attribute or AttributeMap, and names the stores in its Flow configuration at start.
- Dex never creates, migrates, or backfills a destination; the application owns the tables and their retention.
- Delivery is asynchronous and at least once. A write can be dropped after its retries, and a projection failure never rolls back Flow state, so make important projections reconcilable from the Flow.
- A row key template makes the Flow ID format a storage contract. Settle both before the first write.
- Update or delete projected data only through the Flow.

Store types, row keys, column mapping, AttributeMap tables, schema changes, Flow close, the fencing table, and metrics are in the [Server Attribute Store reference](https://docs.superdurable.io/reference/server/attribute-store-sync). For local development, `dexcli dev` accepts an Attribute Store configuration file; see the [CLI reference](https://docs.superdurable.io/reference/cli/#start-locally).

## Reading and writing from outside a Flow

- Write only through typed RPCs or Channels. The Client has no direct Attribute write.
- Read stored values with the Client Attribute read, or with a typed read-only RPC when Flow code, pending messages, or a whole map are needed. See [client.md](client.md#read-attributes-from-outside-a-flow).

## Self-check

- Is every Attribute in the persistence schema, with a stable name?
- Is each growing collection a bounded AttributeMap layout, not one ever-growing value?
- Does every racing read-modify-write lock the exact instance, and every map-wide invariant use the shared singleton lock?
- Does every dynamic read-modify-write select the instance as both lock and load?
- Does every application index use a generic typed slot, and does every slot search constrain FlowType?
- Are secrets kept out of indexes, regular Attributes, logs, and Streams?
- Is the Flow the only writer of each projected fact?
- Can the projection be reconciled after a dropped write?
- Does no Flow decision read the projection?
