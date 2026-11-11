# TypeScript design patterns

Pick a pattern and its invariants in [core patterns](../core/patterns.md).
This page maps each pattern to its runnable TypeScript example and shows the
skeletons where the TypeScript idiom matters. Browse the examples at
[examples/typescript/src](https://github.com/superdurable/dex/tree/main/examples/typescript/src). The blocks on this page form one
module that type-checks; `JobService`, `JobStatus`, and `JobState` are
application types.

| Pattern | TypeScript shape | Example under `examples/typescript/src/` |
| --- | --- | --- |
| Static parallel Steps | `goToMany(StepMovement.of(A, x), StepMovement.of(B, x))` | `patterns/parallel/parallel-step-flows.ts` |
| Dynamic parallel Steps | `goToMany(...items.map((item) => StepMovement.of(Work, item)))` | same file |
| Await all parallel Steps | `join.fork(...)` starts the branches and the waiter; each branch returns `join.endBranch(...)`; the waiter uses `join.forAllBranches(round)` | same file; [skeleton](#await-all-parallel-steps) |
| First win | `withCancelingSiblingSteps(gracefulComplete(x), Work)` | same file |
| Parallel SubFlows: basic, wait for half, long-lived and short-lived parent, partitioning, back pressure | `Wait.allOf(...SubFlow.run(...))`, quorum Channel, bounded request Channel | `patterns/parallel-subflows/parallel-subflows.ts` |
| Polling | one async `execute` loop with heartbeats | `patterns/polling/polling-flow.ts`; [skeleton](#polling) |
| Cron | Timer raced with trigger and skip Channels | `patterns/cron/cron-schedule-flow.ts` |
| Reminder | Timer raced with an opt-out Channel | `patterns/reminders/reminder-flow.ts` |
| Inactivity tracker | activity Channel restarts the Timer wait | `patterns/inactiveness-tracker-timer/inactiveness-tracker-flow.ts` |
| Inbound event loop | RPCs publish; one Step waits on `Wait.anyOf(replies.atLeast(1), resolutions.forOne(), Timer.byDuration(...))`, appends to the transcript, and loops back; see [the rules](../core/patterns.md#inbound-event-loop) | closest: the inactivity tracker |
| Execute failure recovery | `executeFailure: ExecuteFailure.proceedTo(Compensate)` | `patterns/recovery/failure-recovery-flow.ts` |
| WaitFor failure recovery | `waitForFailure: "proceed"`, then `context.waitForMethodFailed()` in `execute` | `primitives/proceed-on-wait-failure/proceed-on-wait-failure-flow.ts` |
| Manual recovery | failed Step proceeds to a Step waiting on operator Channels fed by verb-named RPCs | `patterns/intervention/manual-recovery-flow.ts` |
| Graceful timeout | `StartFlowOptions.timeoutMs` plus `Flow.handleTimeout` returning a decision | `patterns/timeout/flow-graceful-timeout.ts` |
| Drain internal Channels | side Step loops until a final marker | `patterns/drain-channels/internal/drain-internal-channels-flow.ts` |
| Drain for external publishing | `forceCompleteIfChannelsEmpty(output, fallback, channel)` | `patterns/drain-channels/external-publishing/draining-channel-flow.ts` |
| Interruptible execution | RPC records an interrupt; work runs in bounded slices and checks between them | `patterns/interruptible/interruptible-execution-flow.ts` |
| Responsive update: Step completion | `client.waitForStepCompletion(flowId, StepExecutionId.of("Step"), options)` | `patterns/wait-for-step-completion` |
| Responsive update: Attribute match | `client.waitForAttributeMatch(flowId, revision, AttributeMatch.greaterThan(seen), options)` | `products/job-post/job-post-flow.ts` |
| Responsive update: Stream | `client.readStream` with a persisted resume token | `primitives/stream/stream-flow.ts` |
| Entity store | one Flow per entity, RPC mutations with locks, `syncToAttributeStore()` | `patterns/entity-store/user-profile-flow.ts` |
| Sequentially chunked AttributeMap | bounded `current` instance plus immutable archived chunks | `patterns/sequentially-chunked-attribute-map/chunked-subscriber-flow.ts` |
| Hash-partitioned AttributeMap | stable hash of a canonical key selects a bucket instance | `patterns/hash-partitioned-attribute-map/customer-directory-flow.ts`, `patterns/hash-partitioned-attribute-map/controller.ts` |

## Polling

```typescript
import { setTimeout as sleep } from "node:timers/promises";

const POLL_INTERVAL_MS = 2_000;
const JOB_CALL_TIMEOUT_MS = 10_000;
const MAX_JOB_WAIT_MS = 10 * 60_000;

const jobProgress = new Stream("JobProgress", jsonCodec<JobStatus>(), 1 << 20);

class AwaitJob implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly jobs: JobService) {}

  public getStepType(): string {
    return "AwaitJob";
  }

  public getStepOptions(): StepOptions {
    return {
      executeMethodTimeoutMs: MAX_JOB_WAIT_MS,
      heartbeatTimeoutMs: 60_000,
      executeRetry: { maximumAttempts: 3, totalDurationMs: MAX_JOB_WAIT_MS },
      executeFailure: ExecuteFailure.proceedTo(RecordJobWaitFailure),
    };
  }

  public async execute(context: AsyncContext, jobId: string): Promise<StepDecision> {
    let reportedState = context.getLastHeartbeatValue<JobState>();
    while (true) {
      const status = await this.jobs.getJobStatus(jobId, AbortSignal.timeout(JOB_CALL_TIMEOUT_MS));
      if (status.state === "SUCCEEDED") {
        return gracefulComplete(status.result);
      }
      if (status.state === "FAILED") {
        return forceFail(`job ${jobId} failed`);
      }
      if (status.state !== reportedState) {
        jobProgress.write(context, status);
        reportedState = status.state;
      } else {
        await context.recordHeartbeat(reportedState);
      }
      await sleep(POLL_INTERVAL_MS);
    }
  }
}

class RecordJobWaitFailure implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "RecordJobWaitFailure";
  }

  public execute(context: Context, jobId: string): StepDecision {
    return forceFail(`waiting for job ${jobId} failed: ${context.recoveryError?.detail ?? "unknown"}`);
  }
}
```

- Use `AbortSignal.timeout(ms)` for each provider call and
  `setTimeout` from `node:timers/promises` between rounds.
- Write `jobProgress.write(context, status)` when the status changes;
  otherwise `await context.recordHeartbeat(state)`. Resume from
  `context.getLastHeartbeatValue()`.
- `executeMethodTimeoutMs`, `executeRetry.totalDurationMs`, and
  `heartbeatTimeoutMs` bound the wait. `ExecuteFailure.proceedTo` routes
  expiry to the recording Step.
- Keep `poll interval + call timeout <= heartbeatTimeoutMs - 10 s`. With the
  one-minute default and a 10-second call timeout, the interval is at most 40
  seconds. Keep the one-minute default unless a healthy call is silent longer.
- `maximumAttempts` stays small, such as 3 to 5. `totalDurationMs` bounds the
  time; never use a large attempt count as a time bound.

## Await all parallel Steps

```typescript
const workCompleted = new Join("parallel-complete", doubleCodec);

interface WorkItem {
  readonly index: number;
  readonly round: JoinRound;
}

class AwaitDoWork implements Step<WorkItem> {
  public getStepType(): string {
    return "DoWork";
  }
  public async execute(context: Context, item: WorkItem): Promise<StepDecision> {
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 50 + Math.floor(Math.random() * 450));
    });
    return workCompleted.endBranch(context, item.round, item.index);
  }
}

class Await implements Step<JoinRound> {
  public readonly inputCodec = joinRoundCodec;
  public getStepType(): string {
    return "Await";
  }
  public waitFor(_context: Context, round: JoinRound): Wait {
    return Wait.until(workCompleted.forAllBranches(round));
  }
  public execute(context: Context, round: JoinRound): StepDecision {
    return gracefulComplete(workCompleted.branchResults(context, round).length);
  }
}

class AwaitInit implements Step<number> {
  public readonly inputCodec = doubleCodec;
  public getStepType(): string {
    return "Init";
  }
  public execute(context: Context, count: number): StepDecision {
    return goToMany(
      ...workCompleted.fork(
        context,
        Array.from({ length: count }, (_, index) => (round: JoinRound) =>
          StepMovement.of(AwaitDoWork, { index, round }),
        ),
        (round) => StepMovement.of(Await, round),
      ),
    );
  }
}
```

- Register the Join in `getPersistenceSchema().channels` and register all three
  Steps in `getSteps()`. A Step whose input is a bare `JoinRound` uses
  `inputCodec = joinRoundCodec`.
- A partial join waits `forBranches(round, n)` or `forFirstBranch(round)`, and
  the next round's split Step resets with `forRemainingBranches`; see
  `patterns/workflow-patterns/partial-joins.ts`.

## TypeScript notes for other patterns

- First win: the winner returns `withCancelingSiblingSteps(decision, Work)`.
  Losers watch `context.cancellationSignal`.
- Partitioned AttributeMaps: an upsert passes the same bucket to both
  `lockAttributeMapInstances` and `loadAttributeMapInstances` in
  `client.invokeRPCWithOptions`. A read passes only the load.
- Revision match: every writer of the revision Attribute declares the same
  `revision.lock()`, then callers use `client.waitForAttributeMatch` with
  `AttributeMatch.greaterThan(seen)`.
- Graceful timeout: `handleTimeout(context)` may take `AsyncContext`. Load
  state through `StartFlowOptions.timeoutHandlerOptions`. Route exhausted
  handler retries with `FlowTimeoutHandlerFailure.proceedTo(step)`; that Step
  uses `inputCodec = voidCodec`.
