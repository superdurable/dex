# Python primitives

Python shapes for each Primitive; semantics are in the core files. Skeletons
use the async surface; see [Choose async or sync](python.md#choose-async-or-sync).

## Flow, Step, and decisions

- Subclass `Flow[StartInput]` and `Step[Input]`. `get_steps` returns
  `StepList.start_step(first).other_steps(...)` with every reachable Step.
- Create each Step instance once in the Flow constructor and return those same
  instances. A Flow without a start Step uses `StepList.without_start_step(...)`.
- A Step without `wait_for` executes immediately. Override `wait_for` only when
  the Step waits.
- `get_step_options` returns the Step's static `StepOptions`.
- Decisions: `go_to(target, input)`, `go_to_many(StepMovement.of(...), ...)`,
  `dead_end()`, `graceful_complete(output)`, `force_complete(output)`,
  `force_fail(reason)`, and `force_complete_if_channels_empty(output, fallback)`.
- A target is a registered instance, a class with exactly one registered
  instance, or `StepRef("StepType")` for a Step defined later or elsewhere.
  An unknown or ambiguous target raises `FlowDefinitionError`.

```python
from dataclasses import dataclass
from datetime import timedelta

from dex import (
    AsyncContext, Attribute, Channel, Context, Flow, PersistenceSchema, RPCResult,
    Step, StepDecision, StepList, StepOptions, Timer, Wait, go_to,
    graceful_complete, rpc,
)


@dataclass(frozen=True)
class OrderRequest:
    order_id: str
    amount_cents: int


ORDER_STATUS = Attribute("order-status", str)
APPROVALS = Channel("approvals", bool)


class ChargeOrder(Step[OrderRequest]):
    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: OrderRequest
    ) -> StepDecision:
        ORDER_STATUS.set(context, "charged")
        return graceful_complete(input.order_id)


class AwaitApproval(Step[OrderRequest]):
    def wait_for(self, context: Context, input: OrderRequest) -> Wait:
        return Wait.any_of(
            APPROVALS.for_one(),
            Timer.by_duration(timedelta(days=2)),
        )

    def execute(self, context: Context, input: OrderRequest) -> StepDecision:
        if context.has_timer_fired():
            ORDER_STATUS.set(context, "expired")
            return graceful_complete(input.order_id)
        if APPROVALS.results(context)[0]:
            return go_to(ChargeOrder, input)
        ORDER_STATUS.set(context, "rejected")
        return graceful_complete(input.order_id)


class OrderApprovalFlow(Flow[OrderRequest]):
    def __init__(self) -> None:
        self.await_approval = AwaitApproval()
        self.charge_order = ChargeOrder()

    def get_steps(self) -> StepList[OrderRequest]:
        return StepList.start_step(self.await_approval).other_steps(self.charge_order)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(ORDER_STATUS, APPROVALS)

    @rpc(timeout=timedelta(seconds=10))
    def approve_order(self, context: Context, is_approved: bool) -> RPCResult[str]:
        APPROVALS.publish(context, is_approved)
        return RPCResult(ORDER_STATUS.get(context))
```

## Wait and Timer

- `Wait.until(condition)`, `Wait.all_of(...)`, `Wait.any_of(...)`, and
  `Wait.any_combination_of(ConditionCombination.of(...), ...)` compose
  conditions. `Wait.skip_immediately()` proceeds without waiting.
- `Timer.by_duration(timedelta)` is a durable Timer. It holds no Worker slot.
  Use whole seconds.
- In `execute`, `context.has_timer_fired()` tells whether a Timer won, and
  `channel.results(context)` returns the messages the winning Channel
  condition consumed. Pass `condition_id=` when several conditions of one kind
  must be told apart.

## Attribute and AttributeMap

- `Attribute(name, value_type)` and `AttributeMap(name, value_type)`. Read and
  write through the handler Context: `ORDER_STATUS.get(context)`,
  `.set(context, value)`, `.delete(context)`; maps add the instance argument.
- Ordinary Attributes load into every handler. AttributeMap instances load only
  when selected: `StepOptions(execute_load_attribute_map_instances=(notes.load(key),))`,
  `@rpc(load_attribute_map_instances=...)`, or a whole map with
  `load_attribute_maps`. Reading an unselected instance raises
  `AttributeMapNotLoadedError`; that is not "absent".
- Locks: `attribute.lock()` or `attribute_map.lock(instance)` in
  `StepOptions(execute_lock_attributes=...)` or `@rpc(lock_attributes=...)`.
- Search index: `index=AttributeIndex(IndexType.KEYWORD, "keyword1")`. An
  AttributeMap index needs an explicit `index_key`.
- Attribute Store projection: `sync_to_attribute_store=True`, plus
  `FlowConfig(attribute_store_names=[...])`. A dataclass map instance becomes
  one row whose columns are its field names.
- A short-lived secret uses `hard_delete_on_null=True`. It rejects `int`,
  `bool`, and `float` value types, `index`, and `sync_to_attribute_store`;
  `wait_for_attribute_match` rejects it. `delete(context)` or `set(context, None)`
  removes it permanently.
- Initial values at start: `StartFlowOptions().with_attribute(ORDER_STATUS, "new")`
  or `.with_attribute(notes, "n1", note)`.

## Channel and ChannelMap

- `Channel(name, value_type)` and `ChannelMap(name, value_type)`. Wait with
  `for_one()`, `for_n(count)`, `at_least(count)`, `at_most(count)`, or
  `for_range(...)`.
- Publish from an RPC or Step: `APPROVALS.publish(context, value)`. The Client
  has no publish call; expose a typed `@rpc` that publishes.
- Pending messages load only on request: `StepOptions(execute_load_channels=(queue,))`
  or `@rpc(load_channels=...)`; ChannelMap instances use
  `channel_map.load_messages(instance)`. Then use
  `pending_messages(context)`, `find_pending_message(context, message_id)`, and
  `delete(context, message_id)`.

## RPC

- Decorate a Flow method with `@rpc` or `@rpc(...)`. The handler takes
  `(self, context: Context)` plus at most one typed input, and returns
  `RPCResult[T]` or `None`.
- Registration-time options: `name` (protocol RPC name; defaults to the method
  name), `timeout`, `lock_attributes`, `is_transactional`, and the `load_*`
  selections. Name RPCs with a leading verb, such as `approve_order` or
  `get_order_status`. Keep the name stable for running Flows.
- Per-call additions for instances chosen by request data:
  `RPCInvokeOptions(lock_attribute_map_instances=(notes.lock(key),), load_attribute_map_instances=(notes.load(key),))`
  passed as `options=`. A read-modify-write includes both the lock and the load.
- `RPCResult(output, next_steps=(StepMovement.of(target, input),))` starts
  Steps; `.with_canceling_steps(target)` cancels every current execution of a
  Step type after the RPC commits.
- Call it with the bound method of the registered Flow instance:
  `await client.invoke_rpc(flow.approve_order, flow_id, True)`.

## Client Attribute reads

`get_attributes` reads committed Attributes and AttributeMap instances in one
request, without a Worker, from an open or retained closed run.

```python
from dex import AsyncClient, AttributeMap


@dataclass(frozen=True)
class OrderNote:
    text: str


ORDER_NOTES = AttributeMap("order-notes", OrderNote)


async def get_order_view(client: AsyncClient, flow_id: str) -> tuple[str, OrderNote | None]:
    values = await client.get_attributes(flow_id, ORDER_STATUS, ORDER_NOTES.instance("n1"))
    status = values.get(ORDER_STATUS, "new")
    note = values.get(ORDER_NOTES.instance("n1"))
    return status, note
```

- Pass singleton Attributes, or map instances selected with
  `attribute_map.instance(key)` (not `load(key)`, which is for handler loads).
- An unset value is absent from the read-only `AttributeValues`:
  `values.get(key)` returns `None` or the default, and `values[key]` raises
  `KeyError`. Errors are listed in [error handling](error-handling.md).

## Stream

- `Stream(name, value_type, stream_capacity_bytes)`, registered in the
  persistence schema.
- Async Step: `PROGRESS.write(context, value)`, not awaited.
  `PROGRESS.buffered_text(context)` batches `str` chunks and flushes when the
  handler returns.
- Sync generator Step: `yield PROGRESS.write(context, value)`; a sync buffered
  writer returns outputs to `yield from` and needs a final `flush()`.
- RPC and timeout-handler Contexts reject Stream writes.
- Readers: `read_stream(flow_id, stream, resume_token, timeout)` reads forward
  one message at a time; persist the resume token. `list_stream_messages(flow_id,
  stream, page_size, before_page_token)` pages newest-first; pass
  `next_page_token` unchanged until it is empty. The Server rejects an
  out-of-range `page_size` with `INVALID_ARGUMENT`; see the
  [Server reference](https://docs.superdurable.io/reference/server/).

## SubFlow

- `Wait.until(SubFlow.run(child_flow, input))` starts or attaches the child
  after the Wait commits. Register both Flows in the Registry.
- In `execute`, `SubFlow.get_condition_results(context, index)` returns the
  child's `FlowResult`, and `SubFlow.get_flow_id(context, index)` returns its
  Flow ID. The index counts only SubFlow conditions.
- Stop a losing `any_of` child with `client.stop_flow(SubFlow.get_flow_id(...))`.

## Client

| Need | Call |
| --- | --- |
| Start | `start_flow(flow, flow_id, input, StartFlowOptions(...))` returns the run ID |
| Wait for the terminal result | `wait_for_flow(flow_id, timeout)`; `LongPollTimeoutError` means call again |
| Read committed state | `get_attributes(flow_id, *reads)` |
| Mutate or compute | `invoke_rpc(flow.method, flow_id, input, options=...)` |
| One-off status | `describe_flow(flow_id)` returns `FlowInfo`; never poll it |
| Wait for state | `wait_for_attribute_match(flow_id, attribute, AttributeMatch.greater_than(n), WaitForAttributeOptions(request_timeout=...))` |
| Wait for one Step | `wait_for_step_completion(flow_id, StepExecutionId("ChargeOrder"), WaitForStepCompletionOptions(request_timeout=...))` |
| Find Flows | `search_flows(query, page_size, next_page_token)` |
| Stop | `stop_flow(flow_id, StopFlowOptions(...))` |

`AsyncClient` methods are awaited; `Client` has the same methods, blocking.
Pass a timeout to every wait.

## StepOptions, durability, heartbeat, cancellation

```python
import asyncio

from dex import RetryPolicy, StepDurability, force_fail


class RecordChargeFailure(Step[OrderRequest]):
    def execute(self, context: Context, input: OrderRequest) -> StepDecision:
        failure = context.recovery_error
        return force_fail(failure.detail if failure is not None else "charge failed")


class ChargeInBatches(Step[OrderRequest]):
    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_method_timeout=timedelta(minutes=10),
            heartbeat_timeout=timedelta(minutes=1),
            execute_retry=RetryPolicy(maximum_attempts=5),
            execute_durability=StepDurability.SYNC,
        ).on_execute_failure_proceed_to(RecordChargeFailure)

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: OrderRequest
    ) -> StepDecision:
        completed_batches = context.get_last_heartbeat_value(int) or 0
        for batch in range(completed_batches, 10):
            if context.is_cancellation_requested():
                return graceful_complete(input.order_id)
            await asyncio.sleep(1)
            await context.heartbeat(batch + 1)
        return graceful_complete(input.order_id)
```

- `StepOptions` holds per-method timeouts, `RetryPolicy`, heartbeat timeout,
  durability, locks, loads, `wait_for_failure=WaitForFailurePolicy.PROCEED`,
  and the execute failure route. `on_execute_failure_proceed_to` returns a
  copy; Registry rejects a fallback Step with a different input type.
- Durability: `StepDurability.DEFAULT`, `SYNC`, or `ASYNC` per method; a Flow
  default goes in `StartFlowOptions(config_override=FlowConfig(step_durability=...))`.
- `get_last_heartbeat_value(T)` returns `None` when no checkpoint exists.
- Check `context.is_cancellation_requested()` between bounded units of work.
- A Flow timeout handler overrides `handle_timeout(self, context)` and returns
  a decision; configure it with `StartFlowOptions(timeout=..., timeout_policy=FlowTimeoutPolicy.HANDLER)`.

## Context values

- `context.flow_id`, `run_id`, `step_execution_id`, and `attempt` identify the
  invocation.
- `context.flow_started_at` is the run's start time, a timezone-aware UTC
  `datetime`, identical across Steps and retries. Use it, not `datetime.now()`,
  for time-based identities.
- `context.deadline` is the current attempt's deadline as a UTC `datetime`, or
  `None` (RPCs and tasks without one).
- `context.recovery_error` is set only on a recovery path.
- `set_step_execution_local` passes a value from `wait_for` to `execute` of
  the same Step execution. Dex encodes it with the payload codec, commits it
  with the method's result, and stores it in the Step record, so `execute`
  reads it even on another Worker. `get_step_execution_local` returns `None`
  when the key is absent. Only that Step execution can read it; use an
  Attribute for state another Step or a Client needs. See
  [Step execution local values](../core/step.md#step-execution-local-values).
