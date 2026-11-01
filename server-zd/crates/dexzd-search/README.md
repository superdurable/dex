# dexzd-search

## Purpose

SearchFlows backends for server-zd: a builtin evaluator over local search documents and SQL visibility projections.

## SQL visibility projection

`SqlVisibilitySearchBackend::connect` creates the visibility schema and opens exactly
`pool_size` database connections. Construct `SqlVisibilityProjector` from an `Arc` of
that backend. Searches and projection batches share those connections; a busy
pool queues work, including with pool sizes one and two. The caller's request
deadline should cancel the search future if it remains queued too long.
A projection transaction holds one connection until it commits; use
`pool_size >= 2` when searches must continue during a large initial load.

Every query and projection write uses unnamed, explicitly typed statements.
This is required for PgBouncer transaction pooling, which can route consecutive
transactions to different Postgres sessions. Schema setup uses simple SQL
inside a transaction and refuses a schema version newer than this binary.
ParadeDB setup needs a role that can create `pg_search`, or the extension must
already be installed. PostgreSQL setup needs no extensions. One database schema belongs to one Dex cluster because
projection progress is partition scoped. Upgrading from the version-one schema
clears derived rows and progress. Upgrading version two adds skipped-document
records without clearing runs. Every version gains the `applied_epoch` and
`outbox_through_seq` progress columns without a version change.
The caller should retry startup after a schema migration lock timeout.

The server projects the `SearchOutbox` partition index. `read_outbox_entries`
pages a partition's entries in seq order from a committed view, and
`read_run_change` returns the run's current document, or a delete for a purged
or purging run. `read_initial_load_page` pages every searchable document of a
partition for a one-time load into a partition without progress.
`apply_outbox_batch` writes the changes of several partitions and raises their
progress in one transaction: the outbox seq never decreases, and a batch that
finishes a committed view also records the view's position. Every touched
progress row records the writer's node epoch, and a lower epoch fails with
`ProjectionError::Fenced`. An initial-load batch first deletes the partition's
rows. Versioned upserts make a repeated batch harmless. Only the current
partition owner may project. Projection failures must not affect command
commits.

An unindexable committed document is recorded in
`dexzd_visibility_skipped_documents`, omitted from search, and logged as an
error. When the database rejects a row's content, the transaction is retried
with each row in its own savepoint, and only the rejected rows are skipped.
Construct the projector with `new_with_metric_recorder` to count these
events. A later valid upsert or delete removes its skipped-document record.
The progress still advances after the skip.

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
assignments and requires a new database or schema. A partition without
progress is loaded once by the server's outbox consumer.
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
