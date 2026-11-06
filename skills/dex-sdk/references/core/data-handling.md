# Data handling

Read this when a Flow stores large documents, conversation history, model context, or API results, or when you are tempted to add a database.

## Dex-first state ownership

Start with Dex as the durable system of record for every fact the Flow model can own.

| Data | Home |
| --- | --- |
| A cohesive process snapshot | typed Attributes |
| Dynamically keyed or independently loaded records | AttributeMap with stable instances, bounded chunks, or fixed partitions |
| Supported lookup paths | Indexed Attributes and Dex search |
| Direct reads of stored values | Client Attribute read |
| Application mutations, and reads that need Flow code | typed RPCs |
| Queued intent | Channels |
| Large values | the built-in blob path |

- Data shared by several processes does not by itself need a database. Give shared facts one stable domain or entity Flow owner and expose typed operations on it. Do not turn an arbitrary process Flow into a global database, and do not copy one authoritative fact into several process Flows.
- Do not add a database, cache, ORM, outbox, or shadow read model for hypothetical flexibility.
- An external store needs a confirmed requirement Dex cannot reasonably meet: complex or ad-hoc secondary indexes, full-text or vector search, sustained high-contention writes to one hot record that cannot accept Attribute-lock serialization, relational joins or transactions across independently owned records, or large analytical scans. Record the operation, scale, consistency need, and the Dex alternative you evaluated.
- When Dex can stay authoritative and only the query shape is missing, prefer an [Attribute Store projection](external-attribute-sync.md) over application dual writes.
- When an external store must own a fact, define that boundary explicitly. Keep the Flow's Attributes to process state, durable references, and recovery facts.
- One fact never has two ambiguous authorities. Name the source of truth for each fact and define synchronization, failure, and reconciliation.
- Large size alone does not require a separate store.

## The built-in large-value path

Dex durably owns an Attribute even when its serialized value is stored as a blob. Handler requests then carry an opaque blob reference instead of the full value. The SDK hydrates each value before application code sees it:

1. Check the local BlobCache by owning Flow ID and blob reference.
2. Deduplicate the references the invocation needs.
3. Fetch the misses together through Dex.
4. Store fetched values in the bounded disk cache.
5. Decode them for the handler.

The Client Attribute read uses the same path in the Client. Hard-delete Attributes skip it: their values arrive inline and are never cached.

Rules:

- The BlobCache is disposable acceleration. Dex stays the source of truth.
- Create one cache-owned, writable directory per Worker replica. Share the opened cache between that process's Worker and Client when the SDK allows. Size it for the hot set, and close it after both stop.
- A persistent volume can keep entries across restarts, but correctness must not depend on it.
- Never parse, construct, persist, or forward blob references. A reference omits its owning Flow ID; Dex and the SDK supply it. When a value crosses to another Flow through a start, SubFlow, or RPC, Dex copies or inlines it, so the destination stays recoverable.
- Do not build application-level blob references, download logic, deduplication, or cache eviction unless requirements exceed this path.
- The inline threshold and value size limits are Server settings. See the [Server configuration reference](https://docs.superdurable.io/reference/server/configuration#limits).

## Null values

Null is an ordinary application value in inputs, Channel messages, and RPC values. Two places give it control meaning:

- An Attribute write with null deletes the Attribute. For a hard-delete Attribute, it permanently deletes the stored value.
- Flow completion with null output stores no output.

Return an explicit result object when "completed with null" must differ from "completed without output".

## Sticky routing and cache locality

Dex first offers a Flow's work to the Worker process that last served it, then to any Worker of the Flow type. Repeated Steps and RPCs of one Flow therefore tend to find a warm BlobCache.

- Sticky routing is a performance hint, not a placement guarantee. After a restart, a disconnect, or a busy Worker, another Worker serves the Flow and warms its own cache.
- Never keep correctness state only in Worker memory or its cache.

For API or MCP agents that need no code sandbox, this path is usually enough:

```text
Flow Attributes -> Dex blob storage -> sticky Worker -> local BlobCache -> handler
```

## Shape data for cache reuse

Cache entries are keyed by owning Flow ID and immutable blob reference. Updating a large Attribute creates a new value and a new reference. One ever-growing array, such as a single `messages` Attribute, rewrites the whole array and creates a cold blob on every append. Sticky routing cannot remove that.

Prefer immutable or rarely changed values:

- store messages, tool results, or document parts in an AttributeMap;
- group small records into immutable bounded chunks;
- keep only the active window or a summary in a small mutable Attribute;
- edit old chunks only when the product really edits history.

Old chunks keep their references and stay warm. A new chunk causes only its own first miss. Use the [sequentially chunked](patterns.md#sequentially-chunked-attributemap) or [hash-partitioned](patterns.md#hash-partitioned-attributemap) layouts.

## External queries

When data needs relational queries, independent pagination, cross-Flow access, analytics, or a different retention lifecycle, first consider Attribute Store sync. See [external-attribute-sync.md](external-attribute-sync.md).

Attribute Store sync targets PostgreSQL only. For Elasticsearch or a vector database, choose one:

- sync a SQL projection, then index it downstream with CDC or another indexer;
- run an explicit durable projection Step when chunking, embedding, deletion, or version checks are required.

Sync does not generate embeddings or transform documents. Do not add a projection only to avoid re-reading unchanged large Attributes; the BlobCache already covers that.

## Verify

- Invoke the same Flow repeatedly and confirm unchanged values hydrate correctly.
- Replace the serving Worker and confirm the Flow continues after a cold-cache load.
- Update one chunk and confirm the old chunks stay readable.
- Confirm the cache directory is writable, bounded, and never treated as durable state.
- When diagnosing latency, separate remote blob loads from local cache reads and decoding.
