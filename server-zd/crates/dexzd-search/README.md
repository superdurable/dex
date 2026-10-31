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
version-one schema clears derived rows and progress; startup version comparison repopulates missing rows.
Upgrading version two adds skipped-document records without clearing runs.
The caller should retry startup after a schema migration lock timeout.

Forward only committed apply outcomes to the projector. Concatenate every
outcome of one partition frame in apply order with
`extract_batch_search_document_changes`, then call `apply_batch` once for the
frame with the partition's previous and new full `LogPosition` values. The
method writes data and progress in one transaction. Sparse log sequences are
normal. A stored position that differs from the caller's previous position requires
operator attention. Confirm ambiguous commits by retrying the same operation before
processing a later frame. Only the active partition owner may project or rebuild.
Projection failures never prevent command commits.

The queue coalesces complete documents per tenant and run, transferring visibility
wait dependencies to their replacements. Above soft limits it sheds intermediate
Running updates; above hard limits it also sheds closes, then starts, including
Running replacements that inherited start priority. Retention-purge Deletes are
never shed and may exceed hard limits after queued Upserts are reclaimed.
Defaults are 4,096 frames / 64 MiB soft and 8,192 frames / 128 MiB hard.

Shed run identities persist in a local RocksDB sidecar and versioned per-partition
object-store lists. `maxRepairEntries` (default 1,000,000) bounds staging, not the
persistent list. Overflow records a partition version-comparison request. Pending
objects sync independently of SQL retries (default 1s, 64 serialized objects per
batch). SQL progress never passes an unsynced shed intent. At takeover, load pending
objects before projecting. Startup compares committed local identities and versions
with bounded SQL pages, queues differences and SQL-only purged Deletes, syncs those
intents, and then advances progress. An empty schema uses the same path. Unsynced
crash gaps remain discoverable because their SQL checkpoint is held back.

Idle repair reads current committed documents, or emits Deletes for missing/purging
runs. Starts have priority over closes, then intermediate updates. `repairBatchRuns`
defaults to 256. The normal versioned upsert checks each partition's last SQL progress
without advancing it. Pending revisions preserve drops recorded during an in-flight
repair. Local entries are removed after SQL commit; the next sync removes remote
entries. Live frames take priority between repair batches.

Read-your-writes waits cover retained frames rather than pending repairs. A dropped
close can leave a run shown as Running; a dropped start can leave it absent until a
later write or repair. Every retained row remains a complete committed version.
Purges remain protected. Exact visibility returns after pending and live work drain.

No automatic partition rebuild path remains. Unreadable state and unsafe progress
divergence quarantine only the affected partition and persist rebuild-needed markers.
Use the leader-only `AdminService` or `dexcli search-projection status` and explicit
`rebuild --partitions`, `--marked`, or `--all` requests. Rebuilding is serialized,
returns scheduled IDs immediately, and clears markers after success. Configured
authentication requires an existing JWT issuer and `auth.adminAudiences`; static
tokens cannot administer. Unauthenticated development servers allow admin calls.

Monitor dropped and repaired counters by `reason` (`intermediate`, `close`, `start`),
pending runs, pending-object sync failures, and partitions needing rebuilding.
Coalesced changes and explicit rebuild requests remain separate counters. See the
[operations guide](../../../docs/content/production/server-zd-operations.mdx#sql-search-projection-overload)
and the [local pending layout](../dexzd-keyspace/README.md#local-search-pending-sidecar).
Builtin search is unaffected.

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
progress table causes the server projection worker to compare versions and queue missing documents.
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
