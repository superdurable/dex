# Metered Store Wrappers

This directory contains metered decorator wrappers for all persistence store interfaces.
Each wrapper records latency (on success) and error counters (on failure) for every store method,
tagged by `persistence_method_name` and `error_category`.

## Metrics Emitted

| Metric | Type | Tier | Description |
|--------|------|------|-------------|
| `store_method_latency` | Latency | Info | Per-method latency on success |
| `store_method_error_counter` | Counter | Info | Per-method error count, tagged by error category |

Tags: `persistence_method_name` (e.g. `RunStore.GetRun`), `error_category` (e.g. `internal`, `not_found`).

## Wrapped Stores

- `RunStoreWithMetrics` — wraps `persistence.RunStore`
- `ShardStoreWithMetrics` — wraps `persistence.ShardStore`
- `BlobStoreWithMetrics` — wraps `persistence.BlobStore`
- `TasklistStoreWithMetrics` — wraps `persistence.TasklistStore`
- `VisibilityStoreWithMetrics` — wraps `persistence.VisibilityStore`
- `HistoryStoreWithMetrics` — wraps `persistence.HistoryStore`

## Wiring

Metered wrappers are applied in `server/cmd/server.go` at bootstrap:

```go
app := &ServerApp{
    RunStore:      wrappers.NewRunStoreWithMetrics(runStore, logger),
    BlobStore:     wrappers.NewBlobStoreWithMetrics(blobStore, logger),
    ShardStore:    wrappers.NewShardStoreWithMetrics(shardStore, logger),
    TasklistStore: wrappers.NewTasklistStoreWithMetrics(tasklistStore, logger),
}
```

## Regeneration

The wrappers are generated from `metered.tmpl` using [gowrap](https://github.com/hexdigest/gowrap).
If store interfaces change, regenerate:

```bash
go install github.com/hexdigest/gowrap/cmd/gowrap@latest
./generate.sh
```

**DO NOT EDIT** generated `*_with_metrics.go` files directly. Modify `metered.tmpl` instead.
