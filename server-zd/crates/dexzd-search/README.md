# dexzd-search

## Purpose

SearchFlows backends for server-zd: a builtin evaluator over local search documents and SQL visibility projections.

## SQL visibility projection

`SqlVisibilitySearchBackend::connect` creates the visibility schema and opens exactly
`pool_size` database connections. Construct `SqlVisibilityProjector` from an `Arc` of
that backend. Searches and projection batches share those connections; a busy
pool queues work, including with pool sizes one and two. The caller's request
deadline should cancel the search future if it remains queued too long.
An entire partition rebuild holds one connection until its transaction commits;
use `pool_size >= 2` when searches must continue during a large rebuild.

Every query and projection write uses unnamed, explicitly typed statements.
This is required for PgBouncer transaction pooling, which can route consecutive
transactions to different Postgres sessions. Schema setup uses simple SQL
inside a transaction and refuses a schema version newer than this binary.
ParadeDB setup needs a role that can create `pg_search`, or the extension must
already be installed. PostgreSQL setup needs no extensions. One database schema belongs to one Dex cluster because
projection progress and rebuilds are partition scoped. Upgrading from the
version-one schema clears derived rows and progress, so rebuild every partition.
Upgrading version two adds skipped-document records without clearing runs.
The caller should retry startup after a schema migration lock timeout.

Forward only committed apply outcomes to the projector. Concatenate every
outcome of one partition frame in apply order with
`extract_batch_search_document_changes`, then call `apply_batch` once for the
frame with the partition's previous and new full `LogPosition` values. The
method writes data and progress in one transaction. Sparse log sequences are
normal; a stored position that differs from the caller's previous position
requires a rebuild. On a mismatch or hard queue overflow, obtain a consistent
committed local view and call `rebuild_partition` with that view's applied
position. Only the current partition owner may project or rebuild a partition.
Projection failures must not affect command commits.

The server queue coalesces complete queued upserts per tenant and run. Deletes
supersede queued upserts. Empty frames are removed, transferring their visibility
dependency to the replacement. Version 1 identifies starts, including child
starts; terminal documents are closes, including later writes to closed runs.
A replacement Running document inherits its superseded start's priority and
can still be shed at the hard limit.

`search.projectionQueue` defaults to 4,096 frames / 64 MiB soft and 8,192 frames /
128 MiB hard. Above a soft limit, shed incoming intermediate Running updates.
Above a hard limit, shed closes, then starts. Retention-purge Deletes are never
shed: reclaim queued intermediates, closes, then starts, oldest first within
each class. Delete admission excludes in-flight Upsert bytes, allowing total
live bytes to exceed the hard limit by at most that worker batch. Delete-only
overflow retains the partition rebuild fallback.

Every shed Upsert records its tenant, run, Flow hash, partition, and priority in
a deduplicated in-memory repair set. `maxRepairEntries` defaults to 1,000,000
runs, including in-flight repairs; `repairBatchRuns` defaults to 256. Both must
be positive. This identity set uses memory separately from the document queue.
Duplicates keep the highest repair priority: start, close, intermediate. A full
set requests a partition rebuild for the new overflowing run. Rebuild completion
clears only repairs covered by its committed view.

When live frames drain, the worker reads current committed documents for repair
and skips purged or purging runs. One transaction batches rows across partitions
using the normal versioned upsert and locks each partition's progress, checking
its last live position without advancing it. Live frames have priority between
repair batches. New drops during an in-flight repair remain pending. Repair
never blocks commits. Every retained row is some complete committed version.

During shedding and repair, SearchFlows visibility waits may succeed with stale
or missing rows. Exact visibility returns when repairs and live projection drain.
A dropped close may appear Running; a dropped start may be absent. A later
projected write or repair refreshes it; retention purge removes it (default seven
days after close, subject to SQL recovery). Restart or leader loss discards the
repair set. Startup progress comparisons cannot detect every lost repair, so a
restart during shedding may leave stale or absent rows until a later write or
purge. A zero pending gauge after restart does not prove past drops were repaired.

Monitor `dexzd_search_projection_dropped_updates_total` and
`dexzd_search_projection_repaired_runs_total` by `reason` (`intermediate`,
`close`, `start`), `dexzd_search_projection_repair_pending` including the active
batch, and `dexzd_search_projection_repair_overflow_total`. Coalesced and newly
pending rebuild counters remain separate and label-free; rebuild warnings are
limited to once per second per node. Unreadable documents and stored-position
divergence keep their existing rebuild paths. Builtin search is unaffected.

An unindexable committed document is recorded in
`dexzd_visibility_skipped_documents`, omitted from search, and logged as an
error. Construct the projector with `new_with_metric_recorder` to count these
events in `dexzd_search_skipped_documents_total`, separately from shedding. A later valid upsert or delete removes its skipped-document record.
The progress position still advances after the skip.

## SQL dialects and Attribute slots

`sql_visibility` shares translation, rows, batched upserts, schema management,
connection pooling, projection, and visibility waits between ParadeDB and plain
PostgreSQL. `VisibilityDialect` is injected at connection construction.
`migration.sql` owns shared tables and B-trees; `paradedb.sql` owns the extension
and BM25 index; `postgres.sql` owns typed slots and partial B-tree/GIN indexes.
PostgreSQL requires version 15 or newer and no extensions.

The registry reader is constructor-injected and shared with the API registry
cache. Slots follow per-tenant, per-type registration order and exclude system
fields, including `DexWorkQueuePermissions`. JSONB buckets remain authoritative.
The API caches registry snapshots, and a background refresh discovers external
registrations without holding the cache mutex during object-store I/O.
Projection writers take a shared, schema-scoped advisory lock and use cached
assignments by registry version. Registration briefly takes the exclusive lock
to persist new assignments. An independent background task backfills 256 rows
per transaction without that lock; durable keyset checkpoints survive crashes.
Queries read the cached ready snapshot without registry or metadata I/O, and use JSONB
until the assignment is ready. Text slots contain truncated SHA-256 term hashes
and every Text match retains its exact JSONB recheck. The database tokenizer is
shared by slot writes and the recheck, preserving database-locale behavior.

The schema records its dialect. A mismatch refuses startup. Version-three
ParadeDB schemas gain metadata without truncation, rebuilding, or a version
change, so older version-three binaries remain compatible. PostgreSQL uses
version four. Startup rejects a registry that disagrees with persisted slot
assignments and requires a new database or schema. An empty
progress table causes the server projection worker to rebuild committed data.
The PostgreSQL backend enables registry capacity and 1024-byte Keyword ingress
limits. Projection skips older oversized values only when the stored Keyword
or KeywordArray type matches the registered type. Unregistered keys remain
searchable. NULL and negative predicates use key presence and JSONB semantics.
Int and Double remain distinct; KeywordArray accepts a stored scalar string
as one element. Positive Keyword predicates deliberately do not match stored
arrays; negatives preserve builtin semantics.

Run database integration coverage through `DEX_ZD_PARADEDB_URL` or
`DEX_ZD_POSTGRES_URL`. CI uses separate ParadeDB and vanilla `postgres:17`
services, including differential predicates, paging, projection, and backfill.
SearchFlows must keep `query_typed` unnamed so parameter-specific plans can use
partial slot indexes.
