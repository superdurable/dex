# Attribute and AttributeMap

An Attribute is typed durable state inside one Flow execution. Later Steps, RPCs, Client reads, search, and recovery read it. Dex persists every committed write.

Use Attributes for current state. Use Channels for ordered durable messages. Do not use a Channel as storage.

## Lifetime and schema

- Register every Attribute and AttributeMap in the Flow's persistence schema before reading or writing it.
- An Attribute lives as long as its Flow execution. Dex deletes it when retention removes the Flow. Data that must outlive the Flow needs [Attribute Store sync](external-attribute-sync.md) or an application database.
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

Attribute locks serialize Steps and RPCs that declare the same lock within one Flow execution. Dex acquires and releases them around the invocation.

- Lock the exact AttributeMap instance when parallel Steps or RPCs read-modify-write the same value. Unrelated instances stay concurrent.
- Locks are cooperative. Every writer that must be serialized declares the same lock. A writer that skips it bypasses the protection.
- A lock does not load data. A read-modify-write of a dynamic instance selects that instance both as a lock and as a load.
- An RPC that needs a held lock fails at once with the RPC lock-conflict error. It is not queued. Retry it with bounded backoff and keep it idempotent. The holder can be a Step in retry backoff, because a method keeps its locks until the method call ends.
- A Step takes its locks when Dex hands its attempt to a Worker. See [step.md](step.md#retry-snapshots-and-locks) for queued and retrying attempts.
- Keep lock scope narrow. Do not hold a lock across a long wait; commit the locked change in a short following Step.

### Whole-map coordination

When a write invariant spans several instances or the whole map, register a separate singleton bool Attribute, not indexed, as the coordination lock. For example, register `currentMessages` (an AttributeMap) and `currentMessagesLock` (a singleton bool Attribute).

- Every Step, timeout handler, and RPC that changes the protected map declares that singleton lock in its options.
- A writer that takes only an instance lock does not conflict with the singleton lock and can break the invariant.
- The bool value is not the lock state. Never read, toggle, or release it by hand. It needs no stored value.
- The lock serializes writers within one Flow execution only.
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

Behavior:

- Dex stores the value only in object storage, never in Flow history. History keeps a reference.
- Deleting it, or writing null, permanently deletes the stored value. Neither Dex APIs nor history can recover it.
- Every write through the definition carries the setting: Step, RPC, and timeout-handler writes, and StartFlow and SubFlow initial values.
- Handlers receive the value inline. The SDK never stores it in the BlobCache.
- The Client Attribute read returns the currently stored value. The SDK never caches it.

Constraints:

- Only string and object values, including bytes. Wrap a numeric secret in an object with one field. Null is always allowed.
- It cannot be indexed or synced to an Attribute Store.
- WaitForAttributeMatch rejects it, and Action conditions cannot use it. Keep a revision in a regular Attribute and read the secret through a Client Attribute read or an RPC. Return it only to callers authorized to hold it.
- The SDK refuses to write it to a Server that does not support it.
- Build StartFlow and SubFlow initial values from the same definition the target Flow registers.

The stored value is written in place, before the attempt commits:

- A retried attempt, other Steps and RPCs, and any Attribute read can see a write or deletion from an attempt that later failed. Make handlers safe to repeat. Never read-modify-write a hard-delete Attribute.
- Concurrent writers to one key may not leave the last committed write. Use a single writer when order matters.
- A start that fails because the Flow ID is running still overwrites, or with null deletes, the running Flow's value.
- Runs that reuse a Flow ID share the stored value.
- Time travel does not restore a deleted value, and a restarted SubFlow may read it as absent. Design the Flow to request a fresh secret when the value is absent.
- Removing the option from the definition leaves the stored value until Dex removes the Flow. Delete the value through the hard-delete definition first.
- Delete the secret as soon as the Flow no longer needs it. Object storage backups and bucket versioning outside Dex can keep copies; configure them separately.

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
