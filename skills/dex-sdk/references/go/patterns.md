# Go patterns

Choose a pattern with [core patterns](../core/patterns.md) first. This page maps
each pattern to its runnable Go example and shows the Go skeleton where the
idiom matters. Examples live under
[examples/go/patterns](https://github.com/superdurable/dex/tree/main/examples/go/patterns).

| Pattern | Go example | Go notes |
| --- | --- | --- |
| Static parallel Steps | [parallel/static.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/parallel/static.go) | `dex.GoToMany(dex.MovementOf(a, x), dex.MovementOf(b, x))` |
| Dynamic parallel Steps | [parallel/dynamic.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/parallel/dynamic.go) | Build `[]dex.StepMovement` from input |
| Await all | [parallel/await_parallel.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/parallel/await_parallel.go) | `dex.DefineJoin`; skeleton below |
| First win | [parallel/first_win.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/parallel/first_win.go) | Winner returns a decision with `.CancelSiblingSteps(...)` |
| Parallel SubFlows | [parallel-subflows/](https://github.com/superdurable/dex/tree/main/examples/go/patterns/parallel-subflows) | `dex.AllOf` over `dex.SubFlow` conditions |
| Polling | [polling/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/polling/workflow.go) | Skeleton below |
| Cron | [cron/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/cron/workflow.go) | Timer raced with trigger and skip Channels |
| Reminder | [reminders/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/reminders/workflow.go) | Timer raced with an opt-out Channel |
| Inactivity timer | [inactiveness-tracker-timer/workflow.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/inactiveness-tracker-timer/workflow.go) | Activity restarts the wait |
| Inbound event loop | Closest: the inactivity timer example | Skeleton below |
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
var WorkCompletedJoin = dex.DefineJoin[int]("parallel-complete")

type awaitInit struct{ dex.StepDefaultsNoWaitFor[int] }

func (awaitInit) GetStepType() string { return "Init" }

func (awaitInit) Execute(ctx dex.Context, count int) (*dex.StepDecision, error) {
	branchBuilders := make([]func(dex.JoinRound) dex.StepMovement, 0, count)
	for index := range count {
		branchBuilders = append(branchBuilders, func(round dex.JoinRound) dex.StepMovement {
			return dex.MovementOf(awaitWork{}, WorkItem{Index: index, Round: round})
		})
	}
	movements, err := WorkCompletedJoin.Fork(ctx, branchBuilders, func(round dex.JoinRound) dex.StepMovement {
		return dex.MovementOf(awaitStep{}, round)
	})
	if err != nil {
		return nil, err
	}
	return dex.GoToMany(movements...), nil
}

// WorkItem is the input of one DoWork branch.
type WorkItem struct {
	Index int           `json:"index"`
	Round dex.JoinRound `json:"round"`
}

type awaitWork struct {
	dex.StepDefaultsNoWaitFor[WorkItem]
}

func (awaitWork) GetStepType() string { return "DoWork" }

func (awaitWork) Execute(ctx dex.Context, input WorkItem) (*dex.StepDecision, error) {
	time.Sleep(time.Duration(50+rand.Intn(450)) * time.Millisecond)
	return WorkCompletedJoin.EndBranch(ctx, input.Round, input.Index)
}

type awaitStep struct{ dex.StepDefaults }

func (awaitStep) GetStepType() string { return "Await" }

func (awaitStep) WaitFor(_ dex.Context, round dex.JoinRound) (*dex.Wait, error) {
	return dex.Until(WorkCompletedJoin.ForAllBranches(round)), nil
}

func (awaitStep) Execute(ctx dex.Context, round dex.JoinRound) (*dex.StepDecision, error) {
	completedIndexes, err := WorkCompletedJoin.BranchResults(ctx, round)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(len(completedIndexes)), nil
}
```

- Register the Join in `PersistenceSchema.Channels`. `Fork` returns the branch
  and join movements for one `dex.GoToMany`; each worker returns
  `EndBranch`, and the join Step reads `BranchResults`.
- A partial join waits `ForBranches(round, n)` or `ForFirstBranch(round)`, and
  the next round's split Step resets with `ForRemainingBranches`; see
  [partial_joins.go](https://github.com/superdurable/dex/blob/main/examples/go/patterns/workflow-patterns/partial_joins.go).

## Polling

One long-running Step polls the external system. Its StepOptions alone bound
the wait.

```go
const (
	pollInterval   = 5 * time.Second
	jobCallTimeout = 10 * time.Second
	maxJobWait     = 30 * time.Minute
)

var JobProgress = dex.DefineStream[JobStatus]("job-progress", 1<<20)

type awaitJob struct {
	dex.StepDefaultsNoWaitFor[string]
	jobs JobService
}

type recordJobWaitFailure struct {
	dex.StepDefaultsNoWaitFor[string]
}

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

- Bound each call with `context.WithTimeout(ctx, ...)`. Do not `select` on
  `ctx.Done()` around `time.Sleep`; Dex cancels `ctx` on expiry.
- Keep `pollInterval + jobCallTimeout <= HeartbeatTimeout - 10s`. With the
  one-minute default and a 10-second call timeout, `pollInterval` is at most
  40 seconds. Keep the one-minute default unless a healthy call is silent
  longer.
- `MaximumAttempts` stays small, such as 3 to 5. `TotalDuration` bounds the
  time; never use a large attempt count as a time bound.
- `ExecuteMethodTimeout` and `RetryPolicy.TotalDuration` hold the maximum wait.
  Never compute a deadline in the loop.

## Inbound event loop

The RPCs only validate and publish. The waiting Step consumes the messages,
appends them to the transcript, and loops back to itself, which restarts the
inactivity Timer. See [the rules](../core/patterns.md#inbound-event-loop).

```go
var (
	Replies     = dex.DefineChannel[string]("replies")
	Resolutions = dex.DefineChannel[string]("resolutions")
	Transcript  = dex.DefineAttribute[[]string]("transcript")
)

const inactivityWindow = 24 * time.Hour

type ConversationFlow struct {
	dex.FlowDefaults
}

func (*ConversationFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(awaitInbound{}),
		dex.DefineStep(resolveConversation{}),
		dex.DefineStep(closeInactiveConversation{}),
	}
}

func (*ConversationFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{Transcript},
		Channels:   []dex.ChannelDef{Replies, Resolutions},
	}
}

func (flow *ConversationFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.SendReply, nil),
		dex.DefineRPC(flow.ResolveConversation, nil),
	}
}

// SendReply returns false for a rejected reply.
func (*ConversationFlow) SendReply(ctx dex.Context, text string) (*dex.RPCResult[bool], error) {
	if strings.TrimSpace(text) == "" {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	if err := Replies.Publish(ctx, text); err != nil {
		return nil, err
	}
	return &dex.RPCResult[bool]{Output: true}, nil
}

func (*ConversationFlow) ResolveConversation(ctx dex.Context, reason string) (*dex.RPCResult[dex.None], error) {
	if err := Resolutions.Publish(ctx, reason); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type awaitInbound struct {
	dex.StepDefaults
}

func (awaitInbound) WaitFor(_ dex.Context, _ dex.None) (*dex.Wait, error) {
	// Replies are declared before Resolutions, so queued replies win first.
	return dex.AnyOf(
		Replies.AtLeast(1),
		Resolutions.ForOne(),
		dex.Timer(inactivityWindow),
	), nil
}

func (awaitInbound) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if ctx.HasTimerFired() {
		return dex.GoTo(closeInactiveConversation{}, nil), nil
	}
	replies, err := Replies.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(replies) == 0 {
		reasons, err := Resolutions.GetConditionResults(ctx)
		if err != nil {
			return nil, err
		}
		return dex.GoTo(resolveConversation{}, reasons[0]), nil
	}
	transcript, err := Transcript.Get(ctx)
	var notFound *dex.AttributeNotFoundError
	if err != nil && !errors.As(err, &notFound) {
		return nil, err
	}
	if err := Transcript.Set(ctx, append(transcript, replies...)); err != nil {
		return nil, err
	}
	return dex.GoTo(awaitInbound{}, nil), nil
}

type resolveConversation struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (resolveConversation) Execute(_ dex.Context, reason string) (*dex.StepDecision, error) {
	return dex.GracefulComplete("resolved: " + reason), nil
}

type closeInactiveConversation struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (closeInactiveConversation) Execute(_ dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete("inactive"), nil
}
```

- The Step locks nothing, because it is the only writer of `Transcript`. If
  an RPC wrote `Transcript`, both would need its lock, and replies would fail
  with lock conflicts while the Step runs.
- `Replies.AtLeast(1)` takes every queued reply in one execution.

## Durable Timer

Race `dex.Timer(...)` against Channels with `dex.AnyOf` in `WaitFor`. In
`Execute`, branch on `ctx.HasTimerFired()`, then `GoTo` the wait Step again
with the next schedule in its input.

## Partitioned AttributeMaps

An upsert passes the same bucket to `RPCInvokeOptions.LockAttributeMapInstances`
and `LoadAttributeMapInstances`. Appenders that share a lock retry after
`*dex.RPCLockConflictError`.
