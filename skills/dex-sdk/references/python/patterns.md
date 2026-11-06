# Python patterns

Choose the pattern and keep its invariant with [patterns](../core/patterns.md).
This page maps each pattern to its runnable Python example and adds the Python
idioms that matter. Examples live under
[examples/python/dex_examples/patterns](https://github.com/superdurable/dex/tree/main/examples/python/dex_examples/patterns).

## Pattern map

| Pattern | Python idiom | Example |
| --- | --- | --- |
| Static parallel | `go_to_many(StepMovement.of(WorkA, x), StepMovement.of(WorkB, x))` | [static](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/parallel/static_parallel_steps_flow.py) |
| Dynamic parallel | build `list[StepMovement[Any]]` from input, then `go_to_many(*movements)` | [dynamic](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/parallel/dynamic_parallel_steps_flow.py) |
| Await all | branches publish once and `dead_end()`; a coordinator waits on `for_n(count)` | [await](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/parallel/await_parallel_steps_flow.py) |
| First win | the winner returns `graceful_complete(x).with_canceling_sibling_steps(Loser)` | [first win](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/parallel/first_win_parallel_steps_flow.py) |
| Parallel SubFlows | one `SubFlow.run(child, request)` per request in `Wait.all_of` | [basic](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/parallel-subflows/basic_parent_flow.py), [long-lived parent](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/parallel-subflows/advanced_long_live_parent_flow.py), [short-lived parent](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/parallel-subflows/advanced_short_live_parent_flow.py), [wait for half](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/parallel-subflows/wait_for_half_parent_flow.py), [partitioning and back pressure](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/parallel-subflows/submit_request_flow.py) |
| Polling | one long async `execute` loop; see below | [polling](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/polling/polling_flow.py) |
| Cron | race `Timer.by_duration` with trigger and skip Channels | [cron](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/cron/cron_schedule_flow.py) |
| Reminder | race a Timer with an opt-out Channel; send once per iteration | [reminder](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/reminders/reminder_flow.py) |
| Inactivity timer | activity restarts the wait; the Timer moves to processing | [inactivity](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/inactiveness-tracker-timer/inactiveness_tracker_flow.py) |
| Execute failure recovery | `StepOptions(...).on_execute_failure_proceed_to(Compensate)` | [recovery](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/recovery/failure_recovery_flow.py) |
| WaitFor failure recovery | `StepOptions(wait_for_failure=WaitForFailurePolicy.PROCEED)`, then `context.wait_for_method_failed()` | [wait failure](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/primitives/proceed_on_wait_failure/proceed_on_wait_failure_flow.py) |
| Manual recovery | after exhaustion, wait on operator Channels fed by verb-named RPCs | [intervention](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/intervention/manual_recovery_flow.py) |
| Graceful timeout | `FlowTimeoutPolicy.HANDLER` and `handle_timeout` returning a decision | [timeout](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/timeout/flow_graceful_timeout.py) |
| Internal Channel drain | finalize only after accepted messages drain | [internal drain](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/drain-channels/internal/drain_internal_channels_flow.py) |
| External publishing drain | accept by RPC; close with `force_complete_if_channels_empty` | [external drain](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/drain-channels/external_publishing/draining_channel_flow.py) |
| Interruptible execution | an RPC records the interrupt; work checks it between bounded slices | [interruptible](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/interruptible/interruptible_execution_flow.py) |
| Responsive Step completion | `wait_for_step_completion` for one named Step execution | [step completion](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/wait-for-step-completion/wait_for_step_completion_flow.py) |
| Responsive Attribute match | `wait_for_attribute_match` on a revision Attribute | [job post Flow](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/products/job-post/job_post_flow.py), [controller](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/products/job-post/controller.py) |
| Responsive Stream | `read_stream` long poll; resume with the token | [stream controller](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/primitives/stream/controller.py) |
| Entity store | AttributeMap instances plus Attribute Store; lock the mutated instance | [entity store](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/entity-store/user_profile_flow.py) |
| Sequentially chunked map | bounded `current` instance; archive full chunks; load one page per call | [chunked](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/sequentially-chunked-attribute-map/chunked_subscriber_flow.py) |
| Hash-partitioned map | stable hash of the canonical key selects a bucket; lock and load that bucket | [hash partition](https://github.com/superdurable/dex/blob/main/examples/python/dex_examples/patterns/hash-partitioned-attribute-map/customer_directory_flow.py) |

## Polling

One long-running Step polls the external system inside `execute`. Its
`StepOptions` alone bound the total wait.

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

- "Not ready" stays in the loop. A transient provider error is raised, so Step
  retry owns it. A definite provider failure returns a business decision.
- Keep the poll interval plus the call timeout well inside the heartbeat
  timeout.
- The heartbeat checkpoint holds resume state, such as the last reported
  status. It never holds a deadline. Never compute a deadline in the loop;
  `execute_method_timeout` and the retry policy own it.
- On expiry, the failure route records the business failure. Without it the
  Flow fails.
- A sync Worker writes the same loop as a generator: `yield heartbeat(state)`,
  `yield JOB_PROGRESS.write(context, status)`, and `time.sleep(...)`.
- Forbidden: a `wait_for` Timer plus `go_to` loop, `retry_after` or the retry
  policy as the loop, and passing a page token to the next execution.

## Await all

Each branch publishes to one completion Channel exactly once, in the same
handler result as its work, then returns `dead_end()`. The coordinator Step,
started in the same `go_to_many`, waits on `Wait.until(done.for_n(count))`.
A retried branch cannot count twice because its publish commits with its
result.

## Python notes for the other patterns

- Revision Attribute match: initialize an `int` revision to zero, increment it
  under the same Attribute lock in every state-changing handler, then call
  `wait_for_attribute_match(flow_id, REVISION, AttributeMatch.greater_than_or_equal(n), WaitForAttributeOptions(request_timeout=...))`.
  It returns the matched value; reload canonical state with `get_attributes`
  afterwards. The revision coalesces, so a wait may skip values.
- Hash partitioning: canonicalize the key, hash it with a stable algorithm
  (never Python's `hash()`, which is salted per process), and keep the
  algorithm and partition count stable for persisted state.
- Manual recovery and drains: RPC names start with a verb, such as
  `retry_failed_step` or `submit_message`. Add durable command-ID deduplication
  when a repeated operator command must be idempotent.
