# Observability for Go applications

## Correlation and signals

Log Flow ID, run ID, Step type/execution ID, attempt, and operation. Keep high-cardinality IDs out of metric labels. Persist external request IDs when needed across retries.

Heartbeat is liveness/checkpoint data for one long attempt, not business state. Resume only when a previous value exists, and return heartbeat errors.

server-zd enforces the Step HeartbeatTimeout for pull Workers and sends cancellation directly to the pull Worker. Record heartbeats within the timeout, stop promptly when the SDK reports cancellation, and keep side effects idempotent. Later attempts receive the last recorded heartbeat. See [pull Worker routing](../core/pull-workers.md).

[Pinned runnable source](https://github.com/superdurable/dex/blob/sdk-go/v1.29.0/examples/go/primitives/heartbeat/workflow.go)
<!-- dex-source: examples/go/primitives/heartbeat/workflow.go -->
```go
for batch := completedBatches; batch < batches; batch++ {
	select {
	case <-ctx.Done():
		return dex.DeadEnd(), nil
	default:
	}
	time.Sleep(2 * time.Second)
	if err := ctx.RecordHeartbeat(batch + 1); err != nil {
		return nil, err
	}
}
return dex.GracefulComplete("processed"), nil
```

Use summary/status for execution state, history for causality, `Client.GetAttributes` or a typed snapshot RPC for business state, Streams for progress, and metrics for saturation/failure rates. Track attempts, exhaustion, timeout handlers, RPC latency/conflicts, Channel backlog, Stream lag, Worker connectivity, and BlobCache errors.

For a stall: confirm IDs/status; inspect latest history and active attempt; verify registered types; verify a pull Worker is registered for the Flow type; then inspect the application error chain. Supported read surfaces appear in the pinned [client controller](https://github.com/superdurable/dex/blob/sdk-go/v1.29.0/examples/go/primitives/client-apis/controller.go).

## Describe a Flow

From Go SDK v1.21.0, `func (client *Client) DescribeFlow(ctx context.Context, flowID string) (FlowInfo, error)` reads the current or latest run of a known Flow ID without waiting for completion. `FlowInfo` has `FlowID string` (application-assigned ID), `RunID string` (server-assigned run ID), `FlowType string`, `Status FlowStatus`, `StartedAt time.Time`, and `ClosedAt time.Time`. A zero `ClosedAt` means the run is open or its close time is unavailable. server-zd keeps the run ID across time travel, so `RunID` matches the run ID `StartFlow` returned. Use this for a one-off status check after a read-only RPC reports non-terminal business state. Use the typed RPC for business state, `WaitForFlow` to wait for a terminal result or completion output, and `SearchFlows` to find Flow IDs by query. The Java, Python, TypeScript, and Rust SDKs have the same call without a close time; see the [shared status read](../core/error-handling.md#closed-flow-races).

The method rejects a nil, canceled, or expired context, a closed Client, or an empty or whitespace-only Flow ID before the RPC. A missing Flow returns `*dex.FlowNotFoundError`; validation, context, transport, and server failures also return errors. An incomplete or invalid summary returns a mapping error. The call uses `GetFlowSummary` with the Flow ID and no run ID, so it does not select a historical run. server-zd implements that RPC. [Tagged Client](https://github.com/superdurable/dex/blob/sdk-go/v1.29.0/sdk-go/dex/client.go) · [Tagged mapper](https://github.com/superdurable/dex/blob/sdk-go/v1.29.0/sdk-go/dex/proto_mapper.go) · [Go SDK README](https://github.com/superdurable/dex/blob/sdk-go/v1.29.0/sdk-go/README.md)
