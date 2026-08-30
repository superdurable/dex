# Large Attributes and Worker locality

Read this guide when a Flow stores large documents, conversation history, model context, or API/MCP results, especially with replicated Workers.

## Use the built-in large-value path

Dex durably owns an Attribute even when its serialized value is stored as a blob. With server-side blob offload and lazy loading enabled, handler requests carry an opaque blob ID instead of repeatedly transferring the full value.

The SDK hydrates each blob-backed value before application code uses it:

1. Check the local BlobCache by immutable blob ID.
2. Deduplicate IDs needed by the invocation.
3. Fetch cache misses together through Dex.
4. Store fetched values in the bounded disk cache.
5. Decode the hydrated values for the handler.

A cache hit avoids another remote blob fetch. It still incurs local cache reading and decoding. Treat the BlobCache as disposable acceleration; Dex remains the durable source of truth.

Create one cache-owned, writable directory per Worker replica. Share the opened BlobCache between that process's Worker and Client when the SDK supports it, size it for the hot set, and close it after both stop. A persistent volume can preserve entries across process restarts, but correctness must not depend on that volume or on cache admission.

Use the installed SDK and its version-matched examples for exact BlobCache constructors and lifecycle APIs.

## Combine caching with headless Worker locality

For a headless Worker target, Dex resolves individual Worker endpoints and prefers the endpoint that most recently handled the same Flow ID. Repeated Steps and RPCs for one Flow therefore tend to reach a Worker whose BlobCache is already warm.

Configure a target as headless only when its **host:port** resolves to individual Worker replicas, such as a Kubernetes headless Service. A normal load-balanced target does not give Dex direct control over replica selection.

Sticky routing is a performance hint, not a placement guarantee. Dex may select another Worker after a transport failure, endpoint removal, routing-entry eviction, or server restart. The replacement Worker loads each missing blob once and warms its own cache. Never keep Flow correctness state only in Worker memory or its cache.

For API/MCP agents that do not need a code sandbox, this combination is often sufficient:

```text
Flow Attributes -> Dex blob storage -> sticky Worker -> local BlobCache -> handler
```

Do not build application-level blob references, download logic, deduplication, or cache eviction unless product requirements exceed this path.

## Model updates for cache reuse

BlobCache entries are keyed by immutable blob ID. Updating a large Attribute produces a new value and therefore a new blob ID. A single ever-growing array, such as one **messages** Attribute, rewrites the full array and creates a cold blob version on each append. Stickiness cannot remove that write amplification.

Prefer immutable or infrequently changed values:

- store messages, tool results, or document parts in an AttributeMap
- group small records into immutable bounded chunks
- keep only the active window or summary in a small mutable Attribute
- update old chunks only when the product operation truly edits history

Old chunks retain their blob IDs and stay reusable in the local cache; one newly added chunk causes only its own first hydration miss.

### Use AttributeMap deliberately

Choose one Attribute when the value is a cohesive snapshot that handlers normally read and replace together. Choose an AttributeMap when keys are known only at runtime and instances are read, written, or deleted independently. Each instance is a separate blob, so changing one message chunk, tool result, item, or document part does not rewrite the others.

Use stable domain identifiers or monotonic chunk IDs as instance keys. Do not place volatile or unbounded user text in keys. Group very small records into bounded chunks when per-instance overhead would dominate, and define when old instances are deleted.

Inside a handler, map size and instance-key enumeration include buffered writes and deletes. They do not provide server-side pagination. An unbounded map can therefore avoid blob rewrite amplification while still becoming expensive to enumerate, transfer, and decode.

Locks are scoped to one AttributeMap instance. Lock the exact instance when parallel Steps or RPCs perform a read-modify-write on the same value; unrelated instances can remain concurrent.

An indexed AttributeMap does not create one searchable entry per instance. Every instance writes the same fixed Flow search field, a later instance can replace the prior indexed value, and the instance key is not searchable. Use a regular Attribute with a keyword-array index for a bounded searchable collection, or an external projection for per-instance queries.

## Project Attributes for external queries

When data needs relational queries, independent pagination, cross-Flow access, analytics, or a different retention lifecycle, first consider Dex Attribute Store synchronization instead of application-managed dual writes.

Attribute Store synchronization is opt-in for each Attribute or AttributeMap, and a Flow selects one or more Server-configured stores. Dex asynchronously projects latest-state writes and retries transient failures. A projection failure does not roll back the Flow Attribute, and retry exhaustion does not automatically replay or backfill the missed batch. Keep Dex or the application's authoritative database as the source of truth and make important projections reconcilable.

The built-in Attribute Store targets are currently PostgreSQL and MySQL. They project into columns of an existing table keyed by Flow ID; Dex does not create the table. They are not direct Elasticsearch or vector-store connectors, and synchronization does not generate embeddings or transform documents.

For Elasticsearch or a vector database, choose among:

- synchronize a suitable SQL projection, then use CDC or another downstream indexer
- run an explicit durable projection Step when chunking, embedding, deletion, or version checks are required
- add a Dex Attribute Store backend when direct projection is a product requirement

An AttributeMap does not automatically become child rows or vector records in the built-in SQL stores. Verify how every physical instance name maps to the destination schema before enabling synchronization for a dynamic map.

Do not introduce an external projection merely to avoid repeat reads of unchanged large Attributes; BlobCache and Worker locality already address that path.

## Verify the deployment

Exercise the actual topology with an integration test:

- invoke the same Flow repeatedly and confirm unchanged values hydrate correctly
- replace the serving Worker and confirm the Flow continues after a cold-cache load
- update one chunk and confirm old state remains readable
- verify the cache directory is writable, bounded, and not treated as durable state

When diagnosing latency, distinguish remote blob loads from local cache reads and value decoding. Worker affinity can improve the first; data shape determines how much work remains on every invocation.
