# OpsService Design

`OpsService` is dex's read-only operational/visibility gRPC surface.
It's served on its own port (default `:7235`) and can be deployed as an
independent Kubernetes Deployment + Service so the read traffic doesn't
contend with worker-facing run dispatch / heartbeat traffic on the
RunsService port.

## 1. API Surface

```proto
service OpsService {
  rpc ListRuns(ListRunsRequest)     returns (ListRunsResponse);
  rpc GetHistoryEvents(GetHistoryEventsRequest) returns (GetHistoryEventsResponse);
}
```

That's it for MVP. The long-poll `WaitForHistoryEventId` lives on
`RunsService` (it must run on the shard owner where history is written);
see [wait-for-history-design.md](wait-for-history-design.md).

### 1.1 `ListRuns`

Backed by the visibility store (see [visibility-store-design.md](visibility-store-design.md)).
Required filters: `namespace`, `flow_type`, `status` (every supported
index is namespace-prefixed and includes flow_type+status).

`order_by` selects between `start_time` DESC and `updated_at` DESC.
`page_token` is opaque; `limit` defaults to and is clamped at 1000.

### 1.2 `GetHistoryEvents`

Backed by the history store (see [history-store-design.md](history-store-design.md)).
Filters by `run_id` (required) + `namespace` (defense-in-depth) + cursor
`after_id`. Returns events ordered ASC by `id`.

`HistoryEvent.event_data` is the proto-marshaled event-type-specific
payload — clients decode using the schema implied by `event_type`. The
handler does NO blob hydration: history payloads keep `pb.Value` fields
inline as `EncodedObject` for read-path simplicity.

## 2. Deployment Model

Every dex instance runs Run + Matching + Ops in one process, each on its
own listener (`:7233` / `:7234` / `:7235` respectively). The OpsService is
a read-only surface over the visibility + history stores and is always
co-wired. There is no separate ops-only service mode; read traffic is
served by the same instances as the run/matching path.

## 3. Configuration

| Field                  | Env                                  | Default | Purpose                                       |
|------------------------|--------------------------------------|---------|-----------------------------------------------|
| `OpsGRPCListenAddress` | `DEX_OPS_GRPC_LISTEN_ADDRESS`   | `:7235` | OpsService listener bind address.             |
| `Persistence.Mongo.Visibility.URI` | (inherits `DEX_MONGO_URI`) | — | Visibility cluster URI override. |
| `Persistence.Mongo.History.URI`    | (inherits `DEX_MONGO_URI`) | — | History cluster URI override.    |

See [mongo-persistence-design.md §1.1](mongo-persistence-design.md#11-per-store-mongo-configuration)
for the per-store override / inheritance model.

## 4. Wiring

[`server/cmd/server.go`](../server/cmd/server.go):

- `NewServerApp` opens `VisibilityStore` and `HistoryStore` clients
  unconditionally (so the OpsService handler can be wired in any mode).
- `wireOpsService` constructs an [`OpsServiceHandler`](../server/internal/api/ops_service.go)
  with the visibility + history stores + logger, registers it on a
  fresh `grpc.NewServer`, and stashes it on `app.OpsGRPC`.
- `start()` launches `app.OpsGRPC.Serve(app.opsListener)` alongside the
  run and matching servers.
- `Stop()` calls `app.OpsGRPC.GracefulStop()`.

## 5. Test Coverage

- [`server/internal/api/ops_service.go`](../server/internal/api/ops_service.go)
  is a thin pass-through, so most of the testing happens at the layers
  it delegates to.
- [`server/internal/integration/ops_service_test.go`](../server/internal/integration/ops_service_test.go):
  - `TestOpsService_StartRunProducesVisibilityAndHistory` — full chain
    (engine commits → OpsFIFO drains → ListRuns + GetHistoryEvents
    return the right rows).
  - `TestOpsService_ListRuns_AnyFilters` — the WebUI "(any) flow type
    / Any status" read path against the real OpsService.

## 6. What's Out of Scope (For This MVP)

- **Auth / authz**: the OpsService inherits whatever the gRPC server
  options provide. Tenant-scoped enforcement of `namespace` is a
  future concern.
- **Server-side filtering by date range / step retries / waitFor**:
  add new compound indexes + extend `ListRunsQuery` when needed.
- **Streaming reads (server-side cursor)**: today every page is a
  separate `Find` round trip. Acceptable up to the 1000-row clamp.
- **Blob hydration in `GetHistoryEvents`**: history payloads currently
  carry inline `EncodedObject` bytes; large payloads risk hitting
  Mongo's 16 MB doc limit. When that becomes a concern we'll add a
  blob-ref + hydration path.
- **WaitForHistoryEventId**: implemented on `RunsService` as a long-poll
  that blocks until a run's history advances or it closes. See
  [wait-for-history-design.md](wait-for-history-design.md).
