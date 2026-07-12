# PostgreSQL Persistence Backend

DEX supports two interchangeable persistence backends behind the seven store
interfaces in [`server/internal/persistence/interfaces.go`](../server/internal/persistence/interfaces.go):
**PostgreSQL** (default) and **MongoDB**. This document covers the Postgres
backend; see [mongo-persistence-design.md](mongo-persistence-design.md) for the
Mongo counterpart.

## Backend selection

`config.PersistenceConfig.Backend` selects the implementation (`"postgres"`
default, or `"mongo"`). The bootstrap factory
[`server/internal/persistence/factory`](../server/internal/persistence/factory/factory.go)
is the single choke point: `BuildStoreSet` / `BuildDLQStore` switch on the
backend and construct the concrete stores, which the server then wraps with the
backend-agnostic metrics decorators and `ShardedRunStore`. The engine, handlers,
and task processor depend only on the interfaces, so they are unaware of the
backend.

```
cfg.Persistence.Backend ──▶ factory.BuildStoreSet ──┬─ postgres.New*Store (pgxpool/store)
                                                     └─ mongo.New*StoreWithDatabase
                                          ──▶ wrappers.New*WithMetrics ──▶ ServerApp
```

## Logical-database layout

Like Mongo, Postgres uses **one database per logical store**, each with its own
`pgxpool` connection pool:

| Store | Database | Tables |
|---|---|---|
| Shards | `dex_shards` | `shards` |
| Runs (+ DLQ) | `dex_runs` | `runs`, `immediate_tasks`, `timer_tasks`, `opsfifo_tasks`, `task_dlq` |
| Blobs | `dex_blobs` | `blobs` |
| Tasklists | `dex_tasklists` | `tasklist_metadata`, `tasklist_tasks` |
| Visibility | `dex_visibility` | `visibility` |
| History | `dex_history` | `history` |

`config.PostgresPersistenceConfig.For(store)` resolves the per-store URI /
database / pool size / timeouts (DLQ aliases to runs), mirroring the Mongo
config. Per-store URI overrides allow spreading stores across separate Postgres
servers. There are no cross-store transactions (the same as Mongo).

The base URI is a libpq/pgx DSN **without a dbname**; the resolved database is
applied to the pool at creation time (`pool.go`).

## Relational schema

The document model maps to relational tables by storing query/CAS keys as
indexed columns and nested document fields as `JSONB`:

- **`runs`**: scalar columns for the keys and CAS fields (`shard_id, namespace,
  id` PK; `version`, `status`, worker/heartbeat/durable-timer fields,
  counters). The four nested maps — `state_map`, `unconsumed_channel_messages`,
  `step_exe_id_counters`, `active_step_executions` — and `input` are `JSONB`,
  marshaled with `encoding/json`. (The persistence structs are plain Go — no
  proto — so JSON round-trips losslessly.)
- **Task tables** (`immediate_tasks`, `timer_tasks`, `opsfifo_tasks`): PK
  `(shard_id, sort_key, id)` directly serves the ordered range reads
  (`ORDER BY sort_key, id`) and range deletes the Mongo backend does over the
  unified `runs` collection.
- **History payloads** are proto messages with oneofs, which JSON cannot
  round-trip. They are stored as `proto.Marshal` bytes in a `BYTEA` column plus
  a `payload_type` discriminator — identical encoding to the Mongo backend.
  OpsFIFO history tasks wrap the same proto bytes (base64) inside their `JSONB`
  payload envelope.
- **`visibility`** has the two compound list indexes
  `(namespace, flow_type, status, start_time DESC, run_id)` and the
  `updated_at` variant, matching the Mongo list indexes; keyset pagination uses
  the same `<unix_millis>:<run_id>` page token.

Full DDL: [`schema/v0.sql`](../server/internal/persistence/postgres/schema/v0.sql)
(production / docker) and the Go constants in
[`schema.go`](../server/internal/persistence/postgres/schema.go) (tests).

## Concurrency: CAS and transactions

- **Optimistic concurrency (CAS)** uses `version`. `UpdateRunWithNewTasks`
  opens a transaction, `SELECT ... WHERE version=$expected FOR UPDATE` to lock
  and read the row, applies the partial-update delta in Go (merging the map
  fields exactly as Mongo's `$set`/`$push`/`$unset` do), then writes every
  column back with `version=version+1`. A missing/locked-out row maps to a
  `CASError` (`version mismatch`), conflating not-found and stale-version the
  same way Mongo's `MatchedCount==0` does.
- **Multi-row writes** (`CreateRunWithTasks`, `UpdateRunWithNewTasks`,
  `CreateTasks`) run in a single `BEGIN/COMMIT`. Tasklist `CreateTasks` fences
  on `range_id` inside the transaction.
- **Idempotent inserts** (history, blobs, DLQ) use
  `ON CONFLICT ... DO NOTHING`, mirroring Mongo's duplicate-key-swallow contract
  for OpsFIFO replays and lease-handoff races.
- All SQL uses parameterized placeholders (`$1, $2, …`); the only non-parameter
  interpolation is for validated, non-user-controlled identifiers (table /
  database names in schema setup), which are regex-validated and quoted.

Error mapping (`pool.go`): SQLSTATE `23505` (unique violation) → conflict;
`40001`/`40P01` (serialization/deadlock) → retryable CAS error; context
deadline → timeout; everything else → internal.

## Testing

Tests select the backend via `DEX_TEST_PERSISTENCE_BACKEND` (`postgres`
default). The integration/e2e suites under
[`server/internal/integration`](../server/internal/integration) run unchanged
against either backend via `testhelpers` dispatch (`ApplyPersistence`,
`EnsureSchemaForPrefix`, `NewStoreSetForTest`); each package keeps its own
per-prefix databases for parallel isolation. The Postgres backend additionally
has a per-store unit suite in
[`server/internal/persistence/postgres`](../server/internal/persistence/postgres).
CI runs the whole suite once per backend (matrix in
`.github/workflows/server-tests.yml`); the WebUI e2e + dev-stack run against
Postgres (the default).

## Metrics & logging

The per-store metrics decorators are backend-agnostic and cover per-operation
latency/error for both backends. Pool/connection lifecycle and schema
provisioning log at INFO; query/CAS failures at ERROR/DEBUG per the standard
logging guidance.
