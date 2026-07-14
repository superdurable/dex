package mutation

import (
	"context"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/internal/engine/blobs"
	p "github.com/superdurable/dex/server/internal/persistence"
)

// RunMutation is one CAS attempt's workspace for run row + task writes.
type RunMutation interface {
	Commit(ctx context.Context, uploader blobs.Uploader) errors.CategorizedError
	GetRun() *p.RunRow

	// worker general
	RecordWorkerContext(workerCtx *pb.WorkerCallContext) errors.CategorizedError
	RecordWorkerCounter(counter int64)
	RenewHeartbeatTimer()
	SetStateMap(stateMap map[string]p.Value)

	// steps
	SpawnStartingSteps(steps []StartingStep) int64
	SpawnNextSteps(parentStepExeID string, nextSteps []NextStep)
	RemoveSteps(stepExeIDs ...string)
	TransitionStepToWaitingForCondition(stepExeID string, waitCond p.WaitForCondition)
	PromoteReportedUnblocks(unblocks []*pb.StepUnblocked)

	// channel messages
	IsChannelCatchUpComplete(lastReceivedExternalMessageID int64) bool
	SpliceChannelsOnExecuteAndPublishInternalChannels(req *pb.StepExecuteCompletedRequest, channelPubs map[string][]p.ChannelMessage)
	PublishInternalChannels(channelPubs map[string][]p.ChannelMessage)
	PublishExternalChannels(channelName string, messages []p.ChannelMessage)

	// transitions
	TransitionToTerminal(status p.RunStatus, reason TransitionReason)
	TransitionToStatusFromStopDecision(stopDecision pb.StopDecision, reason TransitionReason)
	TransitionToRunning(workerID string, heartbeatDuration time.Duration, reason TransitionReason)
	TransitionToWaitingForWorker(reason TransitionReason)
	TransitionToAllStepsWaitingForConditions(reason TransitionReason)
	MaybeTransitionToPendingIfPromoteWaitingSteps(effectiveNow int64, reason TransitionReason) (bool, errors.CategorizedError)
	MaybeTransitionToPendingIfDurableTimerFired(effectiveNow int64, reason TransitionReason) errors.CategorizedError

	// dispatch tasks
	EnqueueInitialDispatchTask()

	// history
	AddHistoryRunStart(req *pb.StartRunRequest)
	AddHistoryRunStop(status p.RunStatus, reason string)
	AddHistoryStepExecuteCompleted(req *pb.StepExecuteCompletedRequest, fromStepExeID string, conditionResults []*pb.ConditionResult, workerID string)
	AddHistoryStepWaitForCompleted(req *pb.StepWaitForCompletedRequest, fromStepExeID string, workerID string)
	AddHistoryStepsUnblocked(req *pb.StepsUnblockedRequest, workerID string)
	AddHistoryChannelPublish(req *pb.PublishToChannelRequest)
	AddHistoryRunStopIfTerminal()

	// visibility
	UpdateVisibility(status p.RunStatus)
	UpdateVisibilityIfStatusChanged()
}

// NextStep holds a converted next-step spawn request.
type NextStep struct {
	StepID             string
	Input              p.Value
	SkipWaitFor        bool
	WaitForMethodExeID int64
	ExecuteMethodExeID int64
}

// StartingStep holds a converted starting step for StartRun.
type StartingStep struct {
	StepID      string
	Input       p.Value
	SkipWaitFor bool
}

// TransitionReason labels a run status change for logging/metrics.
type TransitionReason string

const (
	TransitionReasonNone                      TransitionReason = ""
	TransitionReasonRunMutation               TransitionReason = "run_mutation"
	TransitionReasonStartRun                  TransitionReason = "start_run"
	TransitionReasonStopRun                   TransitionReason = "stop_run"
	TransitionReasonStepExecuteCompleted      TransitionReason = "step_execute_completed"
	TransitionReasonStepWaitForMethodFailed   TransitionReason = "step_waitfor_method_failed"
	TransitionReasonBatchAsyncMatch           TransitionReason = "batch_async_match"
	TransitionReasonHandleRunDispatchResult   TransitionReason = "handle_run_dispatch_result"
	TransitionReasonReleaseRunAllStepsWaiting TransitionReason = "release_run_all_steps_waiting"
	TransitionReasonExternalChannelReceived   TransitionReason = "external_channel_received"
	TransitionReasonStepWaitForTimerFired     TransitionReason = "step_wait_for_timer_fired"
	TransitionReasonHeartbeatTimeout          TransitionReason = "heartbeat_timeout"
	TransitionReasonReleaseRunYield           TransitionReason = "release_run_yield"
)
