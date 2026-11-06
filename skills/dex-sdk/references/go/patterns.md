# Go patterns

Choose a pattern with [core patterns](../core/patterns.md) first. This page maps
each pattern to its runnable Go example and shows the Go skeleton where the
idiom matters. Examples live under
[examples/go/patterns](https://github.com/superdurable/dex/tree/main/examples/go/patterns).

| Pattern | Go example | Go notes |
| --- | --- | --- |
| Static parallel Steps | [parallel/static.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/parallel/static.go) | `dex.GoToMany(dex.MovementOf(a, x), dex.MovementOf(b, x))` |
| Dynamic parallel Steps | [parallel/dynamic.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/parallel/dynamic.go) | Build `[]dex.StepMovement` from input |
| Await all | [parallel/await_parallel.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/parallel/await_parallel.go) | Skeleton below |
| First win | [parallel/first_win.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/parallel/first_win.go) | Winner returns a decision with `.CancelSiblingSteps(...)` |
| Parallel SubFlows | [parallel-subflows/](https://github.com/superdurable/dex/tree/main/examples/go/patterns/parallel-subflows) | `dex.AllOf` over `dex.SubFlow` conditions |
| Polling | [polling/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/polling/workflow.go) | Skeleton below |
| Cron | [cron/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/cron/workflow.go) | Timer raced with trigger and skip Channels |
| Reminder | [reminders/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/reminders/workflow.go) | Timer raced with an opt-out Channel |
| Inactivity timer | [inactiveness-tracker-timer/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/inactiveness-tracker-timer/workflow.go) | Activity restarts the wait |
| Execute failure recovery | [recovery/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/recovery/workflow.go) | `ProceedToOnExecuteFailure` |
| WaitFor failure recovery | [proceed-on-wait-failure/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/primitives/proceed-on-wait-failure/workflow.go) | `ProceedOnWaitForFailure` plus `ctx.WaitForMethodFailed()` |
| Manual recovery | [intervention/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/intervention/workflow.go) | Operator decision Channels behind verb-named RPCs |
| Graceful timeout | [timeout/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/timeout/workflow.go) | Flow implements `HandleTimeout(ctx)` |
| Internal Channel drain | [drain-channels/internal-drain/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/drain-channels/internal-drain/workflow.go) | Finalize after accepted messages drain |
| External publishing drain | [drain-channels/external-publishing/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/drain-channels/external-publishing/workflow.go) | `dex.ForceCompleteIfChannelsEmpty` |
| Interruptible work | [interruptible/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/interruptible/workflow.go) | Bounded slices; check the signal between them |
| Responsive Step completion | [wait-for-step-completion/](https://github.com/superdurable/dex/tree/main/examples/go/patterns/wait-for-step-completion) | `client.WaitForStepCompletion` |
| Responsive Attribute match | [products/job-post/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/products/job-post/workflow.go) | Revision Attribute under one lock |
| Responsive Stream | [primitives/stream/controller.go](https://github.com/superdurable/dex/blob/main/examples/go/primitives/stream/controller.go) | `ReadStream` with resume tokens |
| Entity store | [entity-store/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/entity-store/workflow.go) | `SyncToAttributeStore`; RPC-only Flow |
| Sequentially chunked AttributeMap | [sequentially-chunked-attribute-map/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/sequentially-chunked-attribute-map/workflow.go) | Load one chunk per call with `RPCInvokeOptions` |
| Hash-partitioned AttributeMap | [hash-partitioned-attribute-map/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/hash-partitioned-attribute-map/workflow.go) | Same bucket in the lock and the load |

## Await all

```go
var ShardDone = dex.DefineChannel[int]("shard-done")

func (shardFanOut) Execute(_ dex.Context, shardCount int) (*dex.StepDecision, error) {
	movements := []dex.StepMovement{dex.MovementOf(awaitShards{}, shardCount)}
	for shard := 0; shard < shardCount; shard++ {
		movements = append(movements, dex.MovementOf(processShard{}, shard))
	}
	return dex.GoToMany(movements...), nil
}

func (processShard) Execute(ctx dex.Context, shard int) (*dex.StepDecision, error) {
	// Process the shard idempotently, then report exactly once.
	if err := ShardDone.Publish(ctx, shard); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

func (awaitShards) WaitFor(_ dex.Context, shardCount int) (*dex.Wait, error) {
	return dex.Until(ShardDone.ForN(shardCount)), nil
}

func (awaitShards) Execute(_ dex.Context, shardCount int) (*dex.StepDecision, error) {
	return dex.GracefulComplete(shardCount), nil
}
```

- Each worker branch publishes once in the same commit as its result, then
  ends with `DeadEnd`.
- The coordinator is moved in the same decision as the workers, so it waits
  from the start.

## Polling

One long-running Step polls the external system. Its StepOptions alone bound
the wait.

```go
const (
	pollInterval   = 5 * time.Second
	jobCallTimeout = 10 * time.Second
	maxJobWait     = 30 * time.Minute
)

func (awaitJob) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodTimeout: maxJobWait,
		HeartbeatTimeout:     time.Minute,
		ExecuteRetry: &dex.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumAttempts:    3,
			TotalDuration:      maxJobWait,
		},
		ExecuteFailure: dex.ProceedToOnExecuteFailure(recordJobWaitFailure{}, nil),
	}
}

func (step awaitJob) Execute(ctx dex.Context, jobID string) (*dex.StepDecision, error) {
	var reportedState JobState
	if _, err := ctx.GetLastHeartbeatValue(&reportedState); err != nil {
		return nil, err
	}
	for {
		callCtx, cancel := context.WithTimeout(ctx, jobCallTimeout)
		status, err := step.jobs.GetJobStatus(callCtx, jobID)
		cancel()
		if err != nil {
			return nil, dex.ErrorWithStack(err)
		}
		switch status.State {
		case JobSucceeded:
			return dex.GracefulComplete(status.Result), nil
		case JobFailed:
			return dex.ForceFail("job " + jobID + " failed"), nil
		}
		if status.State != reportedState {
			if err := JobProgress.Write(ctx, status); err != nil {
				return nil, err
			}
			reportedState = status.State
		} else if err := ctx.RecordHeartbeat(reportedState); err != nil {
			return nil, err
		}
		time.Sleep(pollInterval)
	}
}

func (recordJobWaitFailure) Execute(ctx dex.Context, jobID string) (*dex.StepDecision, error) {
	failure := ctx.RecoveryError()
	if failure == nil {
		return dex.ForceFail("waiting for job " + jobID + " failed"), nil
	}
	return dex.ForceFail("waiting for job " + jobID + " failed: " + failure.Detail), nil
}
```

- Each round bounds the provider call with `context.WithTimeout(ctx, ...)`.
- "Not ready yet" stays in the loop. A transient call error returns, wrapped
  with `dex.ErrorWithStack`, so Step retry owns it.
- Each round writes a Stream frame on a status change, else records a
  heartbeat. Keep `pollInterval + jobCallTimeout` well under
  `HeartbeatTimeout`.
- The checkpoint holds resume state, never a deadline.
- `ExecuteMethodTimeout` and `RetryPolicy.TotalDuration` set the maximum wait.
  `ExecuteFailure` turns expiry into a business result; without it, the Flow
  fails.
- Forbidden: a WaitFor Timer plus `GoTo` loop, `dex.RetryAfter` or retry policy
  as the loop, and a hand-written deadline that duplicates the StepOptions.

## Durable Timer

- Cron, reminders, and inactivity tracking race `dex.Timer(...)` against
  Channels with `dex.AnyOf` in `WaitFor`.
- Carry the schedule in the Step input, not in process memory. Validate the
  interval and count before the first wait.
- In `Execute`, branch on `ctx.HasTimerFired()` first. Send a reminder at most
  once per iteration, then `GoTo` the wait Step again with the next schedule.
- Activity restarts the wait: the activity branch moves back to the wait Step.
  Drain or coalesce bursts of activity messages.

## Partitioned AttributeMaps

- Keep the hash function and partition count stable with persisted state.
  Changing either needs a rehash of every record or a new Flow type.
- An upsert passes the same bucket to `LockAttributeMapInstances` and
  `LoadAttributeMapInstances`. A read loads only the bucket.
- Append-only history keeps a bounded `current` instance and archives full
  chunks under zero-padded start sequences. Appenders lock only `current` and
  retry after `*dex.RPCLockConflictError`.
