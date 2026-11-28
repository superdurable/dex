# Data handling

Read this when a Flow stores large documents, conversation history, model context, API results, or sensitive data, or when you are tempted to add a database.

## Dex-first state ownership

Usually start with Dex as the durable system of record for every fact the Flow model can own; see [Dex-first state](../../SKILL.md#suggestions).

| Data | Home |
| --- | --- |
| A cohesive process snapshot | typed Attributes |
| Dynamically keyed or independently loaded records | AttributeMap with stable instances, bounded chunks, or fixed partitions |
| Supported lookup paths | Indexed Attributes and Dex search |
| Direct reads of stored values | Client Attribute read |
| Application mutations, and reads that need Flow code | typed RPCs |
| Queued intent | Channels |
| Large values | the built-in blob path |

- Data shared by several processes does not by itself need a database. Usually give shared facts one stable domain or entity Flow owner and expose typed operations on it, rather than turning an arbitrary process Flow into a global database or copying one authoritative fact into several process Flows.
- Add an external store when Dex's model does not fit the access pattern, such as complex or ad-hoc secondary indexes, full-text or vector search, sustained high-contention writes to one hot record that cannot accept Attribute-lock serialization, relational joins or transactions across independently owned records, or large analytical scans.
- When Dex can stay authoritative and only the query shape is missing, prefer an [Attribute Store projection](attribute.md#attribute-store-sync) over application dual writes.
- When an external store must own a fact, define that boundary explicitly. Keep the Flow's Attributes to process state, durable references, and recovery facts.
- A large value within the per-value and active-state [limits](../../SKILL.md#default-limits) needs no separate store; the blob path holds it.

## The built-in large-value path

Dex durably owns an Attribute even when its serialized value is stored as a blob. Handler requests then carry an opaque blob reference instead of the full value. The SDK hydrates each value before application code sees it:

1. Check the local BlobCache by owning Flow ID and blob reference.
2. Deduplicate the references the invocation needs.
3. Fetch the misses together through Dex.
4. Store fetched values in the bounded disk cache.
5. Decode them for the handler.

The Client Attribute read uses the same path in the Client. Hard-delete Attributes skip it: their values arrive inline and are never cached.

Rules:

- The BlobCache is disposable acceleration. Dex stays the source of truth, and correctness never depends on cached entries. Directory, sharing, sizing, and shutdown are in the language's Workers reference, such as [Go](https://docs.superdurable.io/reference/go/workers).
- Never parse, construct, persist, or forward blob references. A reference omits its owning Flow ID; Dex and the SDK supply it. When a value crosses to another Flow through a start, SubFlow, or RPC, Dex copies or inlines it, so the destination stays recoverable.
- Do not build application-level blob references, download logic, deduplication, or cache eviction unless requirements exceed this path.
- The inline threshold and value size limits are Server settings. See the [Server configuration reference](https://docs.superdurable.io/reference/server/configuration#limits).

## Null values

Null is an ordinary application value in inputs, Channel messages, and RPC values. Two places give it control meaning:

- An Attribute write with null deletes the Attribute. For a hard-delete Attribute, it permanently deletes the stored value.
- Flow completion with null output stores no output.

Return an explicit result object when "completed with null" must differ from "completed without output".

## Sensitive data and payload encryption

Payload encryption encrypts values in your Workers and Clients, with your codec and keys, before they reach Dex Server. Dex Server and its storage receive eligible values as ciphertext and never hold the keys.

Rules:

- Enable payload encryption when the application handles data that Dex Server, its storage, or its operators must not read.
- Give every Worker and Client of the application the same codec, the same keys, and the same Attribute definitions, including plaintext declarations. Every one of them must use an SDK that supports payload encryption.
- Make every reader able to decrypt before any writer starts writing ciphertext. A Worker that cannot decrypt a Step value fails that attempt. Rotate keys in the same order: every reader gets the new key before any writer uses it.
- With a codec, the SDK encrypts eligible application payloads when it writes them, such as Step, Channel, Stream, and RPC values. Indexed and explicitly plaintext Attributes stay plaintext. Values stored before the codec was enabled stay plaintext.
- Names and IDs are never encrypted. Examples: Flow IDs, Flow types, Step types, RPC names, Attribute keys, AttributeMap keys and instances, index keys, Channel and Stream names, Channel message IDs, Stream sources, request IDs, task queues, and the key ID. Never put secrets into them. Put personal data, such as email addresses or ID numbers, into them only when the application accepts it stored in plaintext on the Server; otherwise derive Flow IDs and keys from opaque business identifiers.
- Keep Attributes plaintext when Dex must compare or index their values, or project them into Attribute Store columns that require the decoded value. Attribute-match waits, Action conditions, and search compare values. Indexed writes are plaintext automatically; declare every other such Attribute plaintext. Keep sensitive values out of them and store those values in separate, encrypted Attributes.
- With a codec, the Client rejects an Attribute-match wait on an Attribute it would encrypt, before it sends the wait request. Always declare the Attributes that Action conditions use plaintext; an SDK may detect an encrypted one only after the Flow has started.

The [Payload encryption reference](https://docs.superdurable.io/reference/server/payload-encryption) has the setup for each SDK, the options, the key file, the envelope limits, rollout and key rotation, binary Attribute Store columns for encrypted Attributes, when each SDK checks Action conditions, and the full list of what is encrypted and what stays plaintext.

## Sticky routing and cache locality

Dex first offers a Flow's work to the Worker process that last served it, then to any Worker of the Flow type. Repeated Steps and RPCs of one Flow therefore tend to find a warm BlobCache.

- Sticky routing is a performance hint, not a placement guarantee. After a restart, a disconnect, or a busy Worker, another Worker serves the Flow and warms its own cache.
- So correctness state never lives only in Worker memory or its cache; see [Must 2](../../SKILL.md#must).

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

Old chunks keep their references and stay warm. A new chunk causes only its own first miss. Use the [sequentially chunked](patterns/flow-shapes-and-state.md#sequentially-chunked-attributemap) or [hash-partitioned](patterns/flow-shapes-and-state.md#hash-partitioned-attributemap) layouts.

## External queries

When data needs relational queries, independent pagination, cross-Flow access, analytics, or a different retention lifecycle, first consider Attribute Store sync. See [attribute.md](attribute.md#attribute-store-sync).

Attribute Store sync targets PostgreSQL only. For Elasticsearch or a vector database, choose one:

- sync a SQL projection, then index it downstream with CDC or another indexer;
- run an explicit durable projection Step when chunking, embedding, deletion, or version checks are required.

Sync does not generate embeddings or transform documents. Do not add a projection only to avoid re-reading unchanged large Attributes; the BlobCache already covers that.
