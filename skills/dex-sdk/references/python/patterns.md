# Python patterns

Each pattern's steps and invariants are in [patterns](../core/patterns.md).
This page maps each pattern to its Python API shape and runnable example.

## Pattern map

| Pattern | Python idiom | Example |
| --- | --- | --- |
| Static parallel | `go_to_many(StepMovement.of(WorkA, x), StepMovement.of(WorkB, x))` | [static](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/parallel/static_parallel_steps_flow.py) |
| Dynamic parallel | build `list[StepMovement[Any]]` from input, then `go_to_many(*movements)` | [dynamic](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/parallel/dynamic_parallel_steps_flow.py) |
| Await all | `go_to_many(*join.fork(context, builders, join_builder))`; each branch returns `join.end_branch(context, round, result)`; the join Step waits on `join.for_all_branches(round)` | [await](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/parallel/await_parallel_steps_flow.py) |
| First win | the winner returns `graceful_complete(x).with_canceling_sibling_steps(Loser)` | [first win](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/parallel/first_win_parallel_steps_flow.py) |
| Parallel SubFlows | one `SubFlow.run(child, request)` per request in `Wait.all_of` | [parallel-subflows](https://github.com/superdurable/dex/tree/main/examples/python/dex_examples/patterns/parallel-subflows) (basic, long- and short-lived parent, wait for half, partitioning, back pressure) |
| Polling | one long async `execute` loop; see below | [polling](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/polling/polling_flow.py) |
| Cron | `Wait.any_of(Timer.by_duration(...), trigger.for_one(), skip.for_one())` | [cron](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/cron/cron_schedule_flow.py) |
| Reminder | `Wait.any_of(Timer.by_duration(...), opt_out.for_one())` | [reminder](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/reminders/reminder_flow.py) |
| Inactivity timer | `Wait.any_of(activity.for_one(), Timer.by_duration(...))` | [inactivity](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/inactiveness-tracker-timer/inactiveness_tracker_flow.py) |
| Inbound event loop | RPCs publish; one Step waits on `Wait.any_of(replies.at_least(1), resolutions.for_one(), Timer.by_duration(...))` and loops back to itself; see below | closest: the inactivity example |
| Execute failure recovery | `StepOptions(...).on_execute_failure_proceed_to(Compensate)` | [recovery](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/recovery/failure_recovery_flow.py) |
| WaitFor failure recovery | `StepOptions(wait_for_failure=WaitForFailurePolicy.PROCEED)`, then `context.wait_for_method_failed()` | [wait failure](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/primitives/proceed_on_wait_failure/proceed_on_wait_failure_flow.py) |
| Manual recovery | failure route to a Step waiting on operator Channels; verb-named `@rpc` methods publish | [intervention](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/intervention/manual_recovery_flow.py) |
| Graceful timeout | `FlowTimeoutPolicy.HANDLER` and `handle_timeout` returning a decision | [timeout](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/timeout/flow_graceful_timeout.py) |
| Internal Channel drain | `go_to_many` main and side Steps; the side Step waits on `channel.for_one()` | [internal drain](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/drain-channels/internal/drain_internal_channels_flow.py) |
| External publishing drain | accept by RPC; close with `force_complete_if_channels_empty(output, fallback, channel)` | [external drain](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/drain-channels/external_publishing/draining_channel_flow.py) |
| Interruptible execution | an `@rpc` sets an Attribute; `execute` reads it between slices | [interruptible](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/interruptible/interruptible_execution_flow.py) |
| Responsive Step completion | `wait_for_step_completion` for one named Step execution | [step completion](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/wait-for-step-completion/wait_for_step_completion_flow.py) |
| Responsive Attribute match | `wait_for_attribute_match` on a revision Attribute | [job post controller](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/products/job-post/controller.py) |
| Responsive Stream | `read_stream` long poll; resume with the token | [stream controller](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/primitives/stream/controller.py) |
| Entity store | `AttributeMap(..., sync_to_attribute_store=True)`; `@rpc` locks `map.lock(key)` | [entity store](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/entity-store/user_profile_flow.py) |
| Sequentially chunked map | `RPCInvokeOptions(load_attribute_map_instances=(chunks.load(page),))` | [chunked](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/sequentially-chunked-attribute-map/chunked_subscriber_flow.py) |
| Hash-partitioned map | `RPCInvokeOptions` with `buckets.lock(bucket)` and `buckets.load(bucket)` | [hash partition](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/hash-partitioned-attribute-map/customer_directory_flow.py) |

## Polling

An async Step loops in `execute`. The invariants are in
[patterns](../core/patterns.md).

```python
import asyncio
from dataclasses import dataclass
from datetime import timedelta
from enum import Enum

from dex import (
    AsyncContext, Context, RetryPolicy, Step, StepDecision, StepOptions, Stream,
    force_fail, graceful_complete,
)

POLL_INTERVAL = timedelta(seconds=2)
JOB_CALL_TIMEOUT = timedelta(seconds=10)
MAX_JOB_WAIT = timedelta(minutes=10)


class JobState(Enum):
    RUNNING = "running"
    SUCCEEDED = "succeeded"
    FAILED = "failed"


@dataclass(frozen=True)
class JobStatus:
    state: JobState
    result: str = ""


JOB_PROGRESS = Stream("JobProgress", JobStatus, 1 << 20)


class JobService:
    async def get_job_status(self, job_id: str) -> JobStatus:
        return JobStatus(JobState.RUNNING)


class RecordJobWaitFailure(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        failure = context.recovery_error
        detail = failure.detail if failure is not None else "unknown failure"
        return force_fail(f"waiting for job {input} failed: {detail}")


class AwaitJob(Step[str]):
    def __init__(self, jobs: JobService) -> None:
        self.jobs = jobs

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_method_timeout=MAX_JOB_WAIT,
            heartbeat_timeout=timedelta(minutes=1),
            execute_retry=RetryPolicy(maximum_attempts=3, total_duration=MAX_JOB_WAIT),
        ).on_execute_failure_proceed_to(RecordJobWaitFailure)

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: str
    ) -> StepDecision:
        reported_state = context.get_last_heartbeat_value(JobState)
        while True:
            status = await asyncio.wait_for(
                self.jobs.get_job_status(input), JOB_CALL_TIMEOUT.total_seconds()
            )
            if status.state is JobState.SUCCEEDED:
                return graceful_complete(status.result)
            if status.state is JobState.FAILED:
                return force_fail(f"job {input} failed")
            if status.state != reported_state:
                JOB_PROGRESS.write(context, status)
                reported_state = status.state
            else:
                await context.heartbeat(reported_state)
            await asyncio.sleep(POLL_INTERVAL.total_seconds())
```

- Bound each provider call with `asyncio.wait_for`; never block the event
  loop with a sync client.
- Keep `POLL_INTERVAL + JOB_CALL_TIMEOUT <= heartbeat_timeout - 10 s`. With
  the one-minute default and a 10-second call timeout, the interval is at most
  40 seconds. Keep the one-minute default unless a healthy call is silent
  longer.
- `maximum_attempts` stays small, such as 3 to 5. `total_duration` bounds the
  time; never use a large attempt count as a time bound.
- Read the checkpoint with `context.get_last_heartbeat_value(T)` and write it
  with `await context.heartbeat(value)`.
- A sync Worker writes the same loop as a generator: `yield heartbeat(state)`,
  `yield JOB_PROGRESS.write(context, status)`, and `time.sleep(...)`.

## Inbound event loop

The RPCs only validate and publish. The waiting Step consumes the messages,
appends them to the transcript, and loops back to itself, which restarts the
inactivity Timer. See [the rules](../core/patterns.md#inbound-event-loop).

```python
from datetime import timedelta

from dex import (
    Attribute, Channel, Context, Flow, PersistenceSchema, RPCResult, Step,
    StepDecision, StepList, Timer, Wait, go_to, graceful_complete, rpc,
)

INACTIVITY_WINDOW = timedelta(hours=24)

REPLIES = Channel("replies", str)
RESOLUTIONS = Channel("resolutions", str)
TRANSCRIPT = Attribute("transcript", list[str])


class ResolveConversation(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete(f"resolved: {input}")


class CloseInactiveConversation(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("inactive")


class AwaitInbound(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        # Replies are declared before resolutions, so queued replies win first.
        return Wait.any_of(
            REPLIES.at_least(1),
            RESOLUTIONS.for_one(),
            Timer.by_duration(INACTIVITY_WINDOW),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        if context.has_timer_fired():
            return go_to(CloseInactiveConversation, None)
        replies = REPLIES.results(context)
        if not replies:
            return go_to(ResolveConversation, RESOLUTIONS.results(context)[0])
        # An unset list Attribute reads as None.
        transcript = TRANSCRIPT.get(context) or []
        TRANSCRIPT.set(context, [*transcript, *replies])
        return go_to(AwaitInbound, None)


class ConversationFlow(Flow[None]):
    def __init__(self) -> None:
        self.await_inbound = AwaitInbound()
        self.resolve_conversation = ResolveConversation()
        self.close_inactive_conversation = CloseInactiveConversation()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.await_inbound).other_steps(
            self.resolve_conversation, self.close_inactive_conversation
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(TRANSCRIPT, REPLIES, RESOLUTIONS)

    @rpc
    def send_reply(self, context: Context, text: str) -> RPCResult[bool]:
        if not text.strip():
            return RPCResult(False)
        REPLIES.publish(context, text)
        return RPCResult(True)

    @rpc
    def resolve(self, context: Context, reason: str) -> None:
        RESOLUTIONS.publish(context, reason)
```

- The Step locks nothing, because it is the only writer of `TRANSCRIPT`. If an
  RPC wrote `TRANSCRIPT`, both would need its lock, and replies would fail
  with `RpcLockConflictError` while the Step runs.
- `REPLIES.at_least(1)` takes every queued reply in one execution.

## Python notes for the other patterns

- Await all: declare `Join("name", ResultType)` on the Flow and pass it to
  `PersistenceSchema.of`. Each branch builder is a `Callable[[JoinRound],
  StepMovement]`; a branch returns `join.end_branch(context, round, result)`,
  and the join Step reads `join.branch_results(context, round)`. A partial join
  waits on `join.for_branches(round, n)`, and the next round resets with
  `join.for_remaining_branches(previous_round, n)`.
- Revision Attribute match: `wait_for_attribute_match(flow_id, REVISION,
  AttributeMatch.greater_than_or_equal(n), WaitForAttributeOptions(request_timeout=...))`
  returns the matched value; reload state with `get_attributes` afterwards.
- Hash partitioning: never use Python's built-in `hash()`; it is salted per
  process. Use a stable algorithm such as FNV-1a over the canonical key.
