# ParadeDB Attribute Index

Dex can index flow attributes with either the Temporal/Cadence visibility API
or ParadeDB. The backend is selected once per Dex Server deployment. Dex does
not dual-write, backfill, migrate, or search across both backends.

## Configure the backend

Visibility remains the default. To enable ParadeDB, configure a reachable
PostgreSQL-compatible ParadeDB endpoint:

```yaml
flowIndex:
  backend: paradedb
  paradeDB:
    dsn: postgres://dex:dex@paradedb:5432/dex?sslmode=disable
    schema: dex
    table: flow_index
    maxConnections: 10
```

Dex Server fails startup if it cannot connect. Schema and table default to
`dex` and `flow_index`; the pool defaults to 10 connections. The table stores
one latest-state row per `FlowID`.

The compose stack exposes its ParadeDB service on `localhost:5433` for local
development.

## Apply a schema

ParadeDB mode requires an applied schema before `StartFlow` or `SearchFlows`.
Create a schema file:

```yaml
definitionVersion: 1
attributes:
  - name: status
    type: keyword
  - name: description
    type: text
  - name: tags
    type: keyword_array
  - name: priority
    type: int
  - name: confidence
    type: double
  - name: ready
    type: bool
  - name: deadline
    type: datetime
  - name: embedding
    type: vector
    vectorDimensions: 1536
    vectorMetric: cosine
```

Apply it synchronously through Dex Server:

```bash
dexcli index apply --file flow-index.yaml --address localhost:8801
```

The command calls `AdminService.ApplyFlowIndexSchema`; it never connects to
ParadeDB directly. Apply is advisory-lock protected and supports initial,
idempotent, and additive changes. Removing a field or changing its type,
vector dimensions, or distance metric returns a schema diff error.

Keyword and text values map to `TEXT`, keyword arrays to `TEXT[]`, integers to
`BIGINT`, doubles to `DOUBLE PRECISION`, booleans to `BOOLEAN`, datetimes to
`TIMESTAMPTZ`, and vectors to `VECTOR(dimensions)`. Keyword fields use the
literal tokenizer. Text fields use ParadeDB's default Unicode tokenizer. All
filter, text, and vector fields share one covering ParadeDB index. Index
replacement uses a concurrent candidate rebuild under the schema lock.

## Workflow write model

Each workflow has one deterministic `IndexSynchronizer` actor. A step, RPC, or
signal applies attribute state and appends an immutable mutation to the actor's
durable FIFO queue in the same workflow task. The producer then returns without
waiting for SQL. The actor is the only ParadeDB writer, so concurrent workflow
producers cannot race each other.

The actor writes one mutation through a local activity and removes it only
after SQL commits. Mutation sequence, run epoch, and terminal fencing make
activity retries idempotent and prevent old Runs or late terminal writes from
overwriting newer state.

Every writer failure is a system error, including connection, SQL, catalog,
schema, type, and vector-dimension errors. Dex logs and retries it; Dex neither
drops the mutation nor converts it into an application Flow failure.

Temporal uses a seven-second local-activity scheduling window and reschedules
the same logical operation with unlimited attempts. Retry starts at one second,
backs off by 2, and caps at 30 seconds. Cadence cannot express native unlimited
retry, so each scheduled activity retains the existing 365-day expiration
workaround. If that expiration is reached, the mutation stays queued and Dex
schedules it again.

Explicit finite retry settings on other activities keep their existing
semantics.

## Continue-as-New and Reset

When the Continue-as-New threshold is reached, the synchronizer completes only
its current in-flight mutation and stops taking new queue entries. Dex drains
already-started step threads, synchronous updates, and received signals. Any
mutations produced after the actor stops remain in the durable queue.

The CAN dump carries the indexed projection, pending queue, and next mutation
sequence. The new Run resumes the actor and enqueues only new Run metadata. CAN
does not replace the full snapshot.

Reset is different. After the backend creates the new Run, Dex sends an
internal reconcile signal. Replay reconstructs the indexed projection, then
the synchronizer writes a replacement snapshot. Replacement clears user
columns missing from the reset point, the previous close time, and stale active
step types before reopening the row as Running.

Direct backend reset operations that bypass Dex are outside this guarantee.

## Terminal behavior

Completion, CancelFlow, and FailFlow use a cooperative terminal finalizer:

1. Stop starting queued or newly produced steps.
2. Let already-running steps and locking RPC updates finish naturally.
3. Preserve their attribute writes, but do not start their next-step decisions.
4. Append the final status mutation.
5. Flush the synchronizer queue before closing the backend workflow.

Terminal cause is first-wins. Repeated Cancel/Fail signals are idempotent.
CancelFlow signals Dex instead of invoking backend cancellation, then returns
to the caller as soon as the signal is accepted. After cleanup, the workflow
returns the provider's canceled error, so Temporal or Cadence records a real
Canceled execution.

If an already-running business activity retries forever, cooperative Cancel
waits for it. Use TerminateFlow to force a hard close and accept index loss.

Non-locking RPC work executes outside the workflow and is not part of terminal
drain. Results consumed before the terminal signal are applied. Results ordered
after it are ignored. Concurrent results follow the workflow's deterministic
signal order.

TerminateFlow and engine execution timeout are hard stops. They do not run the
terminal finalizer, so an in-flight activity may have committed without a
recorded marker and pending mutations may be lost. After a successful backend
terminate, Dex best-effort writes `TERMINATED`; if that write fails, the API
returns an explicit partial-success system error.

Direct Temporal/Cadence cancel, terminate, or reset operations are outside the
consistency contract.

## Search

Visibility mode keeps the existing backend query syntax and rejects vector
queries. ParadeDB mode passes non-empty text to strict `pdb.parse`; an empty
query uses `pdb.all`.

Scalar/text searches sort by raw BM25 score descending with `FlowID` as the
tie-break. Vector searches use the metric declared by the schema, sort distance
ascending, and may include the same strict query as a filter. Dex does not
combine BM25 and vector scores. Results expose either `BM25Score` or
`VectorDistance`.

Go callers use a structured query:

```go
page, err := client.SearchFlows(ctx, dex.SearchQuery{
    Query: `FlowType:"Checkout" AND ready:true`,
    Vector: &dex.SearchVectorQuery{
        IndexKey: "embedding",
        Vector:   []float32{0.1, 0.2, 0.3},
    },
}, 100, "")
```

`dex.IndexVector` enables vector attribute encoding. Invalid vectors in a
search request return `InvalidArgument`; invalid workflow writes remain
retryable system errors.

Dex Web reads `GetFlowIndexInfo` to select the correct basic/advanced editor.
ParadeDB deployments also expose a vector editor with schema field, dimensions,
metric, vector input, optional filter, and raw score/distance result columns.

## Operations and tests

Monitor writer failure logs and pending terminal cleanup. A Flow that remains
in cleanup while ParadeDB is unavailable is preserving its queue, not failing
application code.

Run the storage integration suite against the compose ParadeDB:

```bash
make -C server paradeDBIntegTests
```

The suite covers schema apply, every value type, text and vector search,
idempotent sequence fencing, terminal fencing, Reset replacement, and
pagination.

ParadeDB references:

- [Create and rebuild indexes](https://docs.paradedb.com/documentation/indexing/reindexing)
- [Vector indexing](https://docs.paradedb.com/documentation/indexing/indexing-vectors)
- [Vector querying](https://docs.paradedb.com/documentation/vector/querying)
- [Query parser](https://docs.paradedb.com/documentation/query-builder/compound/query-parser)
