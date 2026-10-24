# dexzd-attribute-store

## Purpose

Writes synchronized Attribute batches into Postgres tables with the Go Server's Attribute Store contract and a per-cluster fencing token.

## Contract

`AttributeStore::connect_and_validate` connects, pings, and validates the
store table, every AttributeMap table, and the fencing table, exactly like the
Go Server's Postgres Attribute Store (`server/service/common/attributestore`).
Any failure fails construction. It never creates or alters a table.

`write_batch` writes one batch from one Flow in one transaction: the store row
upsert, then one upsert or delete per AttributeMap instance, ordered by map name
and instance key. Filtering, `rowKey`, `columnNaming: snake_case`, the SQL type
contract, and unknown-column refreshes follow the Go Server. An item whose value
is `None` is a null write.

Go runs a batch with one statement outside a transaction. This crate always
uses a transaction, because the fencing check must commit atomically with the
writes.

## Fencing

Every write transaction first upserts its `(store_name, cluster_id, scope)` row
in the fencing table and aborts with `AttributeStoreError::Fenced` when a newer
token is recorded. One fencing table serves every store and cluster in a
database. When it is missing, construction fails with an error that contains
the `CREATE TABLE` statement for the table owner to run. The Dex role needs only
SELECT, INSERT, and UPDATE on it.

Each transaction sets `statement_timeout` and
`idle_in_transaction_session_timeout` to `transaction_timeout`. The write phase
starts after any unknown-column schema refresh, which takes at most 30 seconds.
It covers waiting for the store's write connection, connecting, and the
transaction. The client abandons the connection when the write phase exceeds
`transaction_timeout`. A transaction therefore commits within twice
`transaction_timeout` after the write phase starts, or not at all.

## Connections

Writes use one connection and schema refreshes another, so writes on one store
are serialized. Statements are unnamed, as PgBouncer transaction pooling
requires. The DSN accepts tokio-postgres parameters plus libpq's `sslmode`
values `disable`, `allow`, `prefer`, `require`, `verify-ca`, and `verify-full`,
and `sslrootcert` as a CA bundle path or `system`, with the pgx semantics the Go
Server uses. Without `sslrootcert`, `verify-ca` and `verify-full` trust the
system roots. Channel binding uses `tls-server-end-point`.

## Failures

`AttributeStoreError::is_retryable` is true for every database and timeout
failure, because the Go Server retries them all. `failure_scope` tells the
caller what a retry should hold back. SQLSTATE classes 21, 22, 23, 42, 44, 54,
and P0 blame the batch's values or columns, so they return `Batch`. The
exception is 42501, a missing grant. Every other failure returns `Store`,
including connection errors without a SQLSTATE and timeouts.

## Tests

Unit tests run with `cargo test -p dexzd-attribute-store`. The Postgres
integration tests in `postgres_integration_tests` run only when
`DEX_ZD_PARADEDB_URL` names a database whose user can create schemas and roles;
CI runs them in the ParadeDB job. Each test uses its own schema and drops it.
