# TypeScript design patterns

Pick a pattern by its invariant in [core patterns](../core/patterns.md). This
page maps each pattern to its runnable TypeScript example and shows the
skeletons where the TypeScript idiom matters. All examples are under
`examples/typescript/src/patterns/` on the default branch.

| Pattern | TypeScript shape | Runnable example |
| --- | --- | --- |
| Static parallel Steps | `goToMany(StepMovement.of(A, x), StepMovement.of(B, x))` | [parallel-step-flows.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/parallel/parallel-step-flows.ts) |
| Dynamic parallel Steps | `goToMany(...items.map((item) => StepMovement.of(Work, item)))` | same file |
| Await all parallel Steps | each branch publishes once and returns `deadEnd()`; a waiter uses `channel.forN(count)` | same file; [skeleton](#await-all-parallel-steps) |
| First win | `withCancelingSiblingSteps(gracefulComplete(x), Work)` | same file |
| Parallel SubFlows: basic, wait for half, long-lived and short-lived parent, partitioning, back pressure | `Wait.allOf(...SubFlow.run(...))`, quorum Channel, bounded request Channel | [parallel-subflows.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/parallel-subflows/parallel-subflows.ts) |
| Polling | one async `execute` loop with heartbeats | [polling-flow.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/polling/polling-flow.ts); [skeleton](#polling) |
| Cron | Timer raced with trigger and skip Channels | [cron-schedule-flow.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/cron/cron-schedule-flow.ts) |
| Reminder | Timer raced with an opt-out Channel | [reminder-flow.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/reminders/reminder-flow.ts) |
| Inactivity tracker | activity Channel restarts the Timer wait | [inactiveness-tracker-flow.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/inactiveness-tracker-timer/inactiveness-tracker-flow.ts) |
| Execute failure recovery | `executeFailure: ExecuteFailure.proceedTo(Compensate)` | [failure-recovery-flow.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/recovery/failure-recovery-flow.ts) |
| WaitFor failure recovery | `waitForFailure: "proceed"`, then `context.waitForMethodFailed()` in `execute` | [proceed-on-wait-failure-flow.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/primitives/proceed-on-wait-failure/proceed-on-wait-failure-flow.ts) |
| Manual recovery | failed Step proceeds to a Step waiting on operator Channels fed by verb-named RPCs | [manual-recovery-flow.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/intervention/manual-recovery-flow.ts) |
| Graceful timeout | `StartFlowOptions.timeoutMs` plus `Flow.handleTimeout` returning a decision | [flow-graceful-timeout.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/timeout/flow-graceful-timeout.ts) |
| Drain internal Channels | side Step loops until a final marker | [drain-internal-channels-flow.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/drain-channels/internal/drain-internal-channels-flow.ts) |
| Drain for external publishing | `forceCompleteIfChannelsEmpty(output, fallback, channel)` | [draining-channel-flow.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/drain-channels/external-publishing/draining-channel-flow.ts) |
| Interruptible execution | RPC records an interrupt; work runs in bounded slices and checks between them | [interruptible-execution-flow.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/interruptible/interruptible-execution-flow.ts) |
| Responsive update: Step completion | `client.waitForStepCompletion(flowId, StepExecutionId.of("Step"), options)` | [wait-for-step-completion](https://github.com/superdurable/dex/tree/main/examples/typescript/src/patterns/wait-for-step-completion) |
| Responsive update: Attribute match | `client.waitForAttributeMatch(flowId, revision, AttributeMatch.greaterThan(seen), options)` | [job-post-flow.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/products/job-post/job-post-flow.ts) |
| Responsive update: Stream | `client.readStream` with a persisted resume token | [stream-flow.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/primitives/stream/stream-flow.ts) |
| Entity store | one Flow per entity, RPC mutations with locks, `syncToAttributeStore()` | [user-profile-flow.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/entity-store/user-profile-flow.ts) |
| Sequentially chunked AttributeMap | bounded `current` instance plus immutable archived chunks | [chunked-subscriber-flow.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/sequentially-chunked-attribute-map/chunked-subscriber-flow.ts) |
| Hash-partitioned AttributeMap | stable hash of a canonical key selects a bucket instance | [customer-directory-flow.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/hash-partitioned-attribute-map/customer-directory-flow.ts), [controller.ts](https://github.com/superdurable/dex/blob/main/examples/typescript/src/patterns/hash-partitioned-attribute-map/controller.ts) |

## Polling

Wait on an external system with one long-running async `execute`. Its
StepOptions alone bound the wait.

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
```

- "Not ready" stays in the loop. A transient provider error is thrown so Step
  retry handles it. A definite provider failure returns a business decision.
- Each provider call has its own timeout (`AbortSignal.timeout`). Keep the
  poll interval plus the call timeout well below `heartbeatTimeoutMs`.
- Write a Stream frame when the status changes; otherwise record a heartbeat.
  The checkpoint holds resume state such as the last reported status. It never
  holds a deadline.
- `executeMethodTimeoutMs` is the maximum wait. `totalDurationMs` caps the
  retry budget. `executeFailure` routes expiry to a Step that records the
  business failure; without it the Flow fails.
- Forbidden: a `waitFor` Timer plus `goTo` loop, `retryAfter` or retry policy
  as the loop, a hand-written deadline that duplicates the StepOptions, and
  passing a page token to the next execution.

## Await all parallel Steps

```typescript
const branchDone = new Channel("branch-done", voidCodec);

class ProcessShard implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "ProcessShard";
  }

  public async execute(context: Context, _shard: number): Promise<StepDecision> {
    branchDone.publish(context, undefined);
    return deadEnd();
  }
}

class AwaitShards implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "AwaitShards";
  }

  public waitFor(_context: Context, count: number): Wait {
    return Wait.until(branchDone.forN(count));
  }

  public execute(_context: Context, count: number): StepDecision {
    return gracefulComplete(count);
  }
}

class SplitShards implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "SplitShards";
  }

  public execute(_context: Context, count: number): StepDecision {
    return goToMany(
      StepMovement.of(AwaitShards, count),
      ...Array.from({ length: count }, (_, shard) => StepMovement.of(ProcessShard, shard)),
    );
  }
}
```

- Each branch publishes exactly once, in the same result that ends it.
- Register the completion Channel in `getPersistenceSchema().channels`.

## TypeScript notes for other patterns

- First win: the winner returns `withCancelingSiblingSteps(decision, Work)`.
  Losers check `context.cancellationSignal` and must not leave uncompensated
  external effects.
- Partitioned AttributeMaps: an upsert passes the same bucket to both
  `lockAttributeMapInstances` and `loadAttributeMapInstances` in
  `client.invokeRPCWithOptions`. A read passes only the load. Keep the hash
  function and bucket count stable with persisted data; changing either needs
  a rehash or a new Flow type.
- Revision match: every writer of the revision Attribute takes the same
  Attribute lock and increments it in the locked handler. A wait may skip
  intermediate revisions; read canonical state after it returns.
- Graceful timeout: `handleTimeout(context)` may take `AsyncContext` and load
  state through `StartFlowOptions.timeoutHandlerOptions`. Route exhausted
  handler retries with `FlowTimeoutHandlerFailure.proceedTo(step)`; that Step's
  input codec is `voidCodec`.
- Before coding, write down the terminal states, queue bounds, cancellation
  targets, idempotency keys, and recovery path.
