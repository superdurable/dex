# Advanced Python features

## Selective loading and locking

Ordinary Attributes and Channel sizes load automatically. AttributeMap entries and pending Channel/ChannelMap messages are opt-in per handler. Load exact entity instances. Not-loaded exceptions mean omitted snapshot, not missing data.

Declare Attribute/map-instance locks for conflicting handlers. Lock narrowly; keep network I/O outside transaction windows. Treat `RpcLockConflictError` as contention.

Decorator `RPCOptions` fixes timeout, locks, transactionality, and loads at registration. Frozen `RPCInvokeOptions` adds only `lock_attribute_map_instances`, `load_attribute_map_instances`, and `load_channel_map_instances` for one call. Pass it through the keyword-only `options` argument on both `Client.invoke_rpc` and `AsyncClient.invoke_rpc`. A read-modify-write includes the same `attribute_map.lock(instance)` and `attribute_map.load(instance)` selection. The Client unions, sorts, and deduplicates both layers and validates the registered Flow definition and slash-free nonempty instance before network I/O. See the pinned [hash-partitioned controller](https://github.com/superdurable/dex/blob/sdk-python/v1.35.0/examples/python/dex_examples/patterns/hash-partitioned-attribute-map/controller.py).

## Sync generator versus async coroutine

A synchronous streaming Execute yields all `StepOutput` values and returns the decision. An async Execute returns a decision and awaits heartbeat. Buffered Stream `write` is synchronous in async code.

[Pinned SDK contract](https://github.com/superdurable/dex/blob/sdk-python/v1.35.0/sdk-python/tests/typecheck_contracts.py)
<!-- dex-source: sdk-python/tests/typecheck_contracts.py -->
```python
class StreamingStep(Step[Input]):
    def execute(
        self,
        context: Context,
        input: Input,
    ) -> Generator[StepOutput, None, StepDecision]:
        buffered = progress.buffered_text(context)
        yield from buffered.write("thinking ")
        yield from buffered.flush()
        yield heartbeat({"input": input.value})
        yield progress.write(context, "working")
        return graceful_complete()
```

## Heartbeat, cancellation, durability

Set heartbeat timeout for long work. Decode a prior checkpoint only when present, resume from externally committed progress, and keep emitting. Cancellation is cooperative; check `context.is_cancellation_requested()` in bounded loops and cancel external awaits safely.

Sync durability waits for persistence acknowledgement. Async can replay recently acknowledged attempts; use only for idempotent work and test replay. See [durability runnable](https://github.com/superdurable/dex/blob/sdk-python/v1.35.0/examples/python/dex_examples/primitives/durability/durability_flow.py).

Set a short-operation Flow default with `StartFlowOptions(config_override=FlowConfig(step_durability=StepDurability.ASYNC))`. Leave ordinary methods at `StepDurability.DEFAULT`. Override known long methods with `StepOptions(wait_for_durability=StepDurability.SYNC)` or `execute_durability=StepDurability.SYNC`; the two phases are independent.

`StepOptions` also owns method timeouts, heartbeat timeout, retries, selected loads, locks, and failure routes. Do not infer durability from a long method timeout. Read [core StepOptions guidance](../core/step-options.md) for the replay-risk versus latency trade-off and the child-deadline requirement.

## Cancellation selectors

A Step decision such as `go_to(...)` takes `.with_canceling_sibling_steps(...)` for branches from the same predecessor and `.with_canceling_steps(...)` for every current execution of a Step type. An RPC returns `RPCResult(output, next_steps=...).with_canceling_steps(...)`; it selects Flow-wide only, because an RPC has no Step lineage. Each target is a registered instance, a `StepRef`, or an unambiguous class. Dex resolves the selection after the decision or RPC commits and before its next Steps are queued. Steps created by the same result are excluded, and completed, already-canceled, and absent executions are no-ops. A canceled attempt's late decision, writes, retries, and recovery Steps are discarded, so check `context.is_cancellation_requested()` in long work and keep external effects idempotent. For a user's Stop in an Agent, follow [stop a turn](../core/ai-agents.md#stop-a-turn-with-canceling-steps). [Pinned `RPCResult`](https://github.com/superdurable/dex/blob/sdk-python/v1.35.0/sdk-python/dex/flow.py) · [Pinned `StepDecision`](https://github.com/superdurable/dex/blob/sdk-python/v1.35.0/sdk-python/dex/step.py) · [Python SDK README](https://github.com/superdurable/dex/blob/sdk-python/v1.35.0/sdk-python/README.md)

## Timeout handler and BlobCache

Timeout handler options separately control timeout, retry, durability, locks, and selected loads. It obeys normal commit rules.

Open one BlobCache and share it with Client/Worker. Replacement processes needing large values need compatible path/capacity. BlobCache does not make arbitrary local objects durable.
