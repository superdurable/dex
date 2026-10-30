# Python primitives

Read core semantics first; this page gives Python shapes at the pinned baseline.

## Flow, Step, Wait, decisions

Subclass `Flow[InputT]` and `Step[InputT]`. `get_steps` returns `StepList.start_step(instance).other_steps(...)`. A Step without `wait_for` executes immediately. `Wait.until`, all/any condition APIs, and `Wait.skip_immediately` control waiting. Return `go_to`, `go_to_many`, `dead_end`, graceful, or force decisions.

[Pinned runnable source](https://github.com/superdurable/dex/blob/sdk-python/v1.28.0/examples/python/dex_examples/primitives/flow/example_flow.py)
<!-- dex-source: examples/python/dex_examples/primitives/flow/example_flow.py -->
```python
class Example(Step[int]):
    def __init__(self, finish: Finish) -> None:
        self.finish = finish

    def wait_for(self, context: Context, input: int) -> Wait:
        status.set(context, "running")
        return Wait.skip_immediately()

    def execute(self, context: Context, input: int) -> StepDecision:
        return go_to(Finish, input + 1)
```

Among ready `Wait.any_of` candidates, Dex uses canonical Timer, Channel, then SubFlow order and preserves argument order within each kind. An earlier unready Condition does not block a later ready one. Only the winning Channel consumes messages. For strict priority, return only the current higher-priority Condition until it resolves.

From SDK v1.28.0, register several instances of one Step class by overriding `get_step_type()` to return a distinct Step type for each. Pass registered instances in `StepList`. `go_to`, `StepMovement.of`, `StepOptions.on_execute_failure_proceed_to`, `FlowTimeoutHandlerOptions.on_failure_proceed_to`, and cancellation selections accept a registered instance, `StepRef("StepType")`, or a class. A class target is valid only when exactly one instance of that class is registered. Use `StepRef` when the target is defined later or in another module; it holds only a Step type, so the destination codec checks its input at run time. A new instance of a registered class is rejected because instances match by identity. For a generic Step whose input annotation is a `TypeVar`, override `get_input_type()` to return the concrete input type, or `type(None)` for no input. Unknown or ambiguous targets raise `FlowDefinitionError` at registration, at start-option mapping, or when the Worker applies a returned decision.

`Context.flow_started_at` (SDK v1.28.0) is the run's server start time as a timezone-aware UTC `datetime` with one-second precision. It is identical across Steps, retries, timeout handlers, and RPC handlers for both `Worker` and `AsyncWorker`. Derive deterministic identities from the Flow ID and this value, not `datetime.now`.

`Context.deadline` (SDK v1.28.0) is the absolute UTC `datetime` deadline of the current `wait_for`, `execute`, or timeout-handler attempt, or `None` for RPC handlers and tasks without one. Compute the remaining budget with `(context.deadline - datetime.now(timezone.utc)).total_seconds()` after checking for `None`, and use it to size external call timeouts. A shorter remaining `RetryPolicy.total_duration` shortens it, and each retry has its own. See the Core [attempt deadline guidance](../core/step-options.md#budget-work-against-the-attempt-deadline).

## Attribute/AttributeMap and Channel/ChannelMap

Create `Attribute(name, value_type)` and `AttributeMap(name, value_type)` at module scope and include them in `PersistenceSchema`. Access via invocation Context. Maps partition by validated instance. Indexed definitions support search; Attribute Store sync supports external entity access. For a short-lived secret, such as an API key, use a [hard-delete Attribute](data-handling.md#hard-delete-attributes); it supports only string and object values and cannot be indexed or synced.

Channels are durable queues. Conditions wait for one/N messages; after firing, inspect condition messages and delete/move them deliberately. ChannelMap gives one logical definition with independent instance queues. External callers invoke a typed Flow RPC; its handler publishes through Context.

Pending-message reads inside a Step or RPC are invocation snapshots. Other handlers may consume, delete, or publish concurrently. Transactional execution validates selected deletions and commits writes atomically, but does not lock the whole snapshot. Read and write pending messages directly only when the operation explicitly tolerates that race. When a decision requires the queue to remain unchanged, every cooperating Step and RPC writer must use the same Attribute lock.

## Timer, RPC, Stream, SubFlow

Timer belongs in `wait_for` as a durable condition, never `asyncio.sleep` for durable scheduling. Decorate Flow methods with `@rpc`; type the input/output with `RPCResult[T]`, keep handlers short, and lock conflicting state. An RPC without locks or `is_transactional` can query a retained terminal run when it returns no durable effects. Locks, transactions, returned effects, or Server policy require an active execution.

Streams are typed feeds registered in persistence schema. In sync generator handlers, `yield` every Stream output. In async handlers, Stream writes are synchronous API calls at the pinned surface while heartbeat is awaited. Consumers resume from tokens.

[Pinned runnable source](https://github.com/superdurable/dex/blob/sdk-python/v1.28.0/examples/python/dex_examples/primitives/stream/stream_flow.py)
<!-- dex-source: examples/python/dex_examples/primitives/stream/stream_flow.py -->
```python
class RenderPreview(Step[str]):
    def __init__(self, progress: Stream[str]) -> None:
        self.progress = progress

    async def execute(self, context: AsyncContext, input: str) -> StepDecision:
        progress = self.progress.buffered_text(context)
        progress.write(f"Rendering preview for {input}")
        progress.write(f"Preview ready for {input}")
        return graceful_complete(f"Rendered {input}")
```

Use `read_stream` for forward, one-at-a-time, optionally long-polling consumption. Use `list_stream_messages` on `Client` or `AsyncClient` for non-blocking newest-first pages. Pass the typed Stream directly, and pass `next_page_token` unchanged until it is empty.

[Pinned runnable listing](https://github.com/superdurable/dex/blob/sdk-python/v1.28.0/examples/python/dex_examples/primitives/stream/controller.py)
<!-- dex-source: examples/python/dex_examples/primitives/stream/controller.py -->
```python
page = await app_state.client.list_stream_messages(
    required_query("workflowId"),
    app_state.stream.progress,
    required_int_query("pageSize"),
    optional_query("beforePageToken", ""),
)
```

The before-page token is exclusive and scope-bound. The first page uses an empty token. Listing is a best-effort retained snapshot: concurrent newer writes stay outside the older-page chain, while trimming may remove messages. A trimmed anchor returns an empty page. The server requires a positive page size and caps it at 1000 by default.

`SubFlow(child, input)` is a parent Wait condition. Register both definitions and model unfinished-child behavior explicitly.

## Client

`Client` and `AsyncClient` own Flow lifecycle, typed RPC invocation, Attribute-match waits, Stream reads/listing, history, search, config, timers, and reset. Read and write Flow-owned Attribute and Channel state through typed RPCs, not removed direct Client methods. Async calls must be awaited; sync calls must not run on an event loop thread. Use typed exceptions and explicit deadlines.

## Selection

Use `Client.describe_flow(flow_id)` or await `AsyncClient.describe_flow(flow_id)` for a one-off status read of a known Flow, such as after a read-only RPC returns non-terminal business state. `FlowInfo` contains Flow ID, run ID, Flow type, status, and start time. A missing Flow raises `FlowNotFoundError`. The call does not wait for terminal status: use `wait_for_flow` when the caller must wait, and do not loop on `describe_flow`. `FlowInfo` has no error type or message; for a closed Flow that is still retained, `wait_for_flow` returns immediately, and a failed result carries them in `error_type` and `error_message`. Do not use `describe_flow` as a `start_flow` preflight. Use `search_flows` for indexed discovery. [Released Python Client API](https://github.com/superdurable/dex/blob/sdk-python/v1.28.0/sdk-python/dex/client.py)

Attribute = current state; Channel = queued intent; RPC = synchronous mutation/snapshot; Stream = incremental output; Timer = durable time; SubFlow = independently identified durable child.
