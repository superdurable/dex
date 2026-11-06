# Python primitives

Python shapes for each Primitive. Read the core page for the semantics first;
this page adds only how to write it in Python. Skeletons use the async surface
unless noted; the sync form differs only as described in
[Choose async or sync](python.md#choose-async-or-sync).

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
- `wait_for` decides only what to wait for. It performs no external side
  effect.

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
  Every handler that must serialize uses the same lock.
- Search index: `index=AttributeIndex(IndexType.KEYWORD, "keyword1")`. Reuse
  generic typed index keys across Flow types and filter searches by FlowType.
  An AttributeMap index needs an explicit key and holds only the last written
  instance.
- Attribute Store projection: `sync_to_attribute_store=True`, plus
  `FlowConfig(attribute_store_names=[...])`. The projection is an asynchronous
  copy; the Flow stays authoritative. A dataclass instance becomes one row
  whose columns are its field names.
- A short-lived secret uses `hard_delete_on_null=True`. It accepts only string
  and object values, cannot be indexed or synced, and is rejected by Attribute
  match waits. `delete`, or `set` to `None`, removes it permanently; afterwards
  it reads as absent. Its write is visible before the attempt commits, so keep
  the handler safe to repeat and never read-modify-write it.
- Initial values at start: `StartFlowOptions().with_attribute(ORDER_STATUS, "new")`
  or `.with_attribute(notes, "n1", note)`.

## Channel and ChannelMap

- `Channel(name, value_type)` and `ChannelMap(name, value_type)`. Wait with
  `for_one()`, `for_n(count)`, `at_least(count)`, `at_most(count)`, or
  `for_range(...)`; only the winning condition consumes messages.
- Publish from an RPC or Step: `APPROVALS.publish(context, value)`. External
  callers never publish directly; they invoke a typed RPC that publishes.
- Pending messages load only on request: `StepOptions(execute_load_channels=(queue,))`
  or `@rpc(load_channels=...)`; ChannelMap instances use
  `channel_map.load_messages(instance)`. Then use
  `pending_messages(context)`, `find_pending_message(context, message_id)`, and
  `delete(context, message_id)`.
- A pending-message read is a snapshot. When a decision needs the queue
  unchanged, every cooperating Step and RPC takes the same Attribute lock.

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
- An RPC with no locks, not transactional, and no returned effects can read a
  retained closed run. Anything else needs an active run.
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
  `attribute_map.instance(key)`. There is no single-value method.
- The result is a read-only `AttributeValues`. An unset value is absent:
  `values.get(key)` returns `None` or the default, `key in values` is `False`,
  and `values[key]` raises `KeyError`.
- Each read must equal a definition registered by a Flow in the Client's
  Registry; otherwise `FlowDefinitionError` is raised before any request.
- Use an RPC instead when the response needs Flow code or pending Channel
  messages. Errors are listed in [error handling](error-handling.md).

## Stream

- `Stream(name, value_type, stream_capacity_bytes)`, registered in the
  persistence schema. A Stream is a best-effort feed, not authoritative state.
  Retries can repeat messages.
- Async Step: `PROGRESS.write(context, value)`, not awaited.
  `PROGRESS.buffered_text(context)` batches `str` chunks and flushes when the
  handler returns.
- Sync generator Step: `yield PROGRESS.write(context, value)`; a sync buffered
  writer returns outputs to `yield from` and needs a final `flush()`.
- RPC and timeout-handler Contexts reject Stream writes.
- Readers: `read_stream(flow_id, stream, resume_token, timeout)` reads forward
  one message at a time; persist the resume token. `list_stream_messages(flow_id,
  stream, page_size, before_page_token)` pages newest-first; pass
  `next_page_token` unchanged until it is empty.

## SubFlow

- `Wait.until(SubFlow.run(child_flow, input))` starts or attaches the child
  after the Wait commits. Register both Flows in the Worker's Registry.
- In `execute`, `SubFlow.get_condition_results(context, index)` returns the
  child's `FlowResult`, and `SubFlow.get_flow_id(context, index)` returns its
  Flow ID. The index counts only SubFlow conditions.
- An `any_of` winner does not stop losing children. Stop them explicitly when
  that matters.

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
Bound every wait. The Client has no direct Attribute or Channel write.

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
  copy; the fallback Step takes the same input type.
- Durability: leave `StepDurability.DEFAULT`, set a Flow default with
  `FlowConfig(step_durability=...)` in `StartFlowOptions(config_override=...)`,
  and override a known long or non-idempotent method with `SYNC`. The
  trade-off is in [Step](../core/step.md).
- Heartbeat is a progress checkpoint for one long attempt, not business state.
  `get_last_heartbeat_value(T)` returns `None` when no checkpoint exists.
- Cancellation is cooperative. Check `context.is_cancellation_requested()`
  between bounded units of work.
- A Flow timeout handler overrides `handle_timeout(self, context)` and returns
  a decision; configure it with `StartFlowOptions(timeout=..., timeout_policy=FlowTimeoutPolicy.HANDLER)`.

## Context values

- `context.flow_id`, `run_id`, `step_execution_id`, and `attempt` identify the
  invocation. Log them; derive idempotency keys from the Flow ID and Step
  execution ID.
- `context.flow_started_at` is the run's start time, identical across Steps and
  retries. Derive time-based identities from it, never from `datetime.now()`.
- `context.deadline` is the current attempt's deadline, or `None` (RPCs and
  tasks without one). Size external call timeouts from it.
- `context.recovery_error` is set only on a recovery path.
- `set_step_execution_local` keeps process-local, in-memory data for one Step
  execution. It is lost on Worker restart; never use it as durable state.
