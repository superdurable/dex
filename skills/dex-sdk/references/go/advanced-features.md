# Advanced Go features

Use these only after Flow boundaries and recovery are explicit.

## Selective loading, locks, and transactions

Ordinary Attributes and Channel size metadata load automatically; large AttributeMaps and pending Channel payloads are opt-in per handler. Load exact instances for entity work. A not-loaded error is modeling feedback, not absence.

Declare Attribute or map-instance locks when RPC/Step mutations conflict. Lock the smallest stable key. Keep remote I/O outside transaction/lock windows. Treat `RPCLockConflictError` as contention.

`RPCOptions` fixes registration-time timeout, locks, transactionality, and loads. `RPCInvokeOptions` is additive and accepts only `LockAttributeMapInstances`, `LoadAttributeMapInstances`, and `LoadChannelMapInstances`. Call `Client.InvokeRPCWithOptions` when request data chooses an exact instance. Use `dex.LockAttributeMap(definition, instance)` for the lock and `definition.Load(instance)` for the load; include both for read-modify-write. The Client unions, sorts, and deduplicates these selections with registration options and validates the Flow definition and instance name before network I/O. The pinned [hash-partitioned controller](https://github.com/superdurable/dex/blob/sdk-go/v1.32.0/examples/go/patterns/hash-partitioned-attribute-map/controller.go) shows write and read calls.

## Buffered Stream and heartbeat

`dex.NewBufferedTextStream` batches by interval/bytes. Flush at semantic boundaries and return errors. Stream writes are liveness frames but preserve the explicit heartbeat checkpoint.

[Pinned SDK contract](https://github.com/superdurable/dex/blob/sdk-go/v1.32.0/sdk-go/dex/contracts_test.go)
<!-- dex-source: sdk-go/dex/contracts_test.go -->
```go
progress, err := dex.NewBufferedTextStream(
	context,
	progressStream,
	dex.BufferedTextStreamFlushInterval(500*time.Millisecond),
	dex.BufferedTextStreamMaxBytes(8<<10),
)
```

Set heartbeat timeout for long Execute work. On retry, test whether a prior value exists before decoding; resume from an externally committed checkpoint. Heartbeat is progress, not authoritative business state.

## StepOptions and Flow durability

Set an application-wide default through `StartFlowOptions.ConfigOverride` and `FlowConfig.StepDurability`. For a short-operation Flow, use `StepDurabilityAsync`. Leave ordinary methods at `StepDurabilityDefault`, then set `WaitForDurability` or `ExecuteDurability` to `StepDurabilitySync` for a known long method. These two method overrides are independent.

`StepOptions` also owns `WaitForMethodTimeout`, `ExecuteMethodTimeout`, `HeartbeatTimeout`, per-method retry, selected loads, locks, and failure routes. Do not infer durability from `ExecuteMethodTimeout`. Read [core StepOptions guidance](../core/step-options.md) for the replay-risk versus latency trade-off.

## Cancellation, timeout, BlobCache

A Step decision such as `dex.GoTo(...)` takes `CancelSiblingSteps(...)` for branches from the same predecessor and `CancelSteps(...)` for every current execution of a registered Step type. An RPC selects Flow-wide only, because an RPC has no Step lineage: set `RPCResult.CancelingSteps` or call `CancelSteps` on the result, for example `return (&dex.RPCResult[Out]{Output: out, NextSteps: next}).CancelSteps(modelCallStep{}), nil`. Dex resolves the selection after the decision or RPC commits and before its next Steps are queued. Steps created by the same result are excluded, and completed, already-canceled, and absent executions are no-ops. A canceled attempt's late decision, writes, retries, and recovery Steps are discarded. Cancellation is cooperative; external calls need context cancellation and idempotency. For a user's Stop in an Agent, follow [stop a turn](../core/ai-agents.md#stop-a-turn-with-canceling-steps). [Pinned `RPCResult`](https://github.com/superdurable/dex/blob/sdk-go/v1.32.0/sdk-go/dex/rpc.go) · [Go SDK README](https://github.com/superdurable/dex/blob/sdk-go/v1.32.0/sdk-go/README.md) · [Cancellation integration test](https://github.com/superdurable/dex/blob/sdk-go/v1.32.0/sdk-go/integ/step_cancellation_test.go)

Sync durability waits for persistence acknowledgement. Async improves latency but can replay a recently acknowledged attempt after failure; restrict it to idempotent work and test replay. See [durability runnable](https://github.com/superdurable/dex/blob/sdk-go/v1.32.0/examples/go/primitives/durability/workflow.go).

A timeout handler has its own timeout, retry, durability, locks, and state loads. It follows normal commit rules. Its recovery Step reads **ctx.RecoveryError()**; with Go SDK v1.17.0, use the [time-limit predicates](error-handling.md#commit-and-recovery) to tell a method or heartbeat timeout from another failure.

Open one BlobCache at bootstrap and share it with Client/Worker. Align path/capacity across replacements needing large values. It provides payload locality, not arbitrary durable files.
