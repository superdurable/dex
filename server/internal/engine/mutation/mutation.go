package mutation

import (
	"context"
	"fmt"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/log/tag"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/common/utils/ptr"
	"github.com/superdurable/dex/server/internal/engine/blobs"
	"github.com/superdurable/dex/server/internal/engine/mutation/ops"
	"github.com/superdurable/dex/server/internal/metrics"
	p "github.com/superdurable/dex/server/internal/persistence"
)

type runMutation struct {
	deps             Deps
	shardID          int32
	mode             commitMode
	run              *p.RunRow
	update           *p.RunRowUpdate
	newTasks         []p.TaskRow
	ops              *ops.OpsTasksBuilder
	now              time.Time
	transitionReason TransitionReason
}

type commitMode int

const (
	commitModeCreate commitMode = iota
	commitModeUpdate
)

func (mutation *runMutation) GetRun() *p.RunRow { return mutation.run }

func (mutation *runMutation) Commit(ctx context.Context, uploader blobs.Uploader) errors.CategorizedError {
	if uploader != nil {
		if err := uploader.SubmitOnce(ctx); err != nil {
			return err
		}
	}
	mutation.finalizeOpsTasks()
	previousStatus := mutation.run.Status
	switch mutation.mode {
	case commitModeCreate:
		if err := mutation.deps.RunStore.CreateRunWithTasks(ctx, mutation.run, mutation.newTasks); err != nil {
			return err
		}
		logRunStatusTransition(
			mutation.deps.Logger,
			mutation.shardID, mutation.run.Namespace, mutation.run.ID, mutation.run.TaskListName,
			"none", mutation.run.Status, mutation.transitionReason)
		metrics.CounterRunAttemptStarted.Inc()
		return nil
	case commitModeUpdate:
		if err := mutation.deps.RunStore.UpdateRunWithNewTasks(
			ctx, mutation.shardID, mutation.run.Namespace, mutation.run.ID,
			mutation.run.Version, mutation.update, mutation.newTasks,
		); err != nil {
			return err
		}
		if mutation.update.Status != nil && *mutation.update.Status != previousStatus {
			reason := mutation.transitionReason
			if reason == TransitionReasonNone {
				reason = TransitionReasonRunMutation
			}
			logRunStatusTransition(
				mutation.deps.Logger,
				mutation.shardID, mutation.run.Namespace, mutation.run.ID, mutation.run.TaskListName,
				previousStatus.Name(), *mutation.update.Status, reason)
			if *mutation.update.Status == p.RunStatusCompleted {
				metrics.CounterRunSuccess.Inc()
				metrics.LatencyRunExecution.Record(time.Since(mutation.run.CreatedAt))
			}
			if *mutation.update.Status == p.RunStatusAllStepsWaitingForConditions {
				metrics.CounterProcessReleaseRunAllStepsWaitingParked.Inc(metrics.TagNamespace(mutation.run.Namespace))
			}
		}
		return nil
	default:
		return errors.NewInternalError("unknown mutation commit mode", nil)
	}
}

func (mutation *runMutation) IsChannelCatchUpComplete(lastReceivedExternalMessageID int64) bool {
	for _, messages := range mutation.run.UnconsumedChannelMessages {
		for _, message := range messages {
			if message.ID > lastReceivedExternalMessageID {
				return false
			}
		}
	}
	return true
}

func (mutation *runMutation) RecordWorkerContext(workerCtx *pb.WorkerCallContext) errors.CategorizedError {
	newCounter := workerCtx.GetWorkerRequestCounter()
	mutation.update.WorkerRequestCounter = &newCounter
	return applyWorkerRetryUpdates(
		mutation.deps.Logger,
		mutation.run,
		workerCtx.GetActiveStepRetryUpdates(),
		mutation.update.ActiveStepExecutions,
		mutation.deps.StepRetryLastErrorMaxBytes,
	)
}

func (mutation *runMutation) SetStateMap(stateMap map[string]p.Value) {
	mutation.update.StateMap = stateMap
}

func (mutation *runMutation) SpawnStartingSteps(steps []StartingStep) int64 {
	stepMethodCounter := mutation.run.StepMethodExeCounter
	for _, startingStep := range steps {
		counter := mutation.run.StepExeIDCounters[startingStep.StepID] + 1
		mutation.run.StepExeIDCounters[startingStep.StepID] = counter
		stepExeID := stepExeIDFromCounter(startingStep.StepID, counter)
		status := p.StepExeStatusInvokingWaitFor
		step := p.ActiveStepExecution{
			Input:  startingStep.Input,
			Status: status,
		}
		if startingStep.SkipWaitFor {
			step.Status = p.StepExeStatusInvokingExecute
			stepMethodCounter++
			step.ExecuteMethodExeID = stepMethodCounter
		} else {
			stepMethodCounter++
			step.WaitForMethodExeID = stepMethodCounter
		}
		mutation.run.ActiveStepExecutions[stepExeID] = step
	}
	mutation.run.StepMethodExeCounter = stepMethodCounter
	return stepMethodCounter
}

func (mutation *runMutation) SpawnNextSteps(parentStepExeID string, nextSteps []NextStep) {
	if len(nextSteps) == 0 {
		return
	}
	if mutation.update.StepExeIDCounters == nil {
		mutation.update.StepExeIDCounters = make(map[string]int32)
	}
	nextCounters := make(map[string]int32, len(mutation.run.StepExeIDCounters))
	for stepID, counter := range mutation.run.StepExeIDCounters {
		nextCounters[stepID] = counter
	}
	for _, nextStep := range nextSteps {
		counter := nextCounters[nextStep.StepID] + 1
		nextCounters[nextStep.StepID] = counter
		mutation.update.StepExeIDCounters[nextStep.StepID] = counter
		stepExeID := stepExeIDFromCounter(nextStep.StepID, counter)
		status := p.StepExeStatusInvokingWaitFor
		if nextStep.SkipWaitFor {
			status = p.StepExeStatusInvokingExecute
		}
		step := &p.ActiveStepExecution{
			Input:         nextStep.Input,
			Status:        status,
			FromStepExeID: parentStepExeID,
		}
		if nextStep.SkipWaitFor {
			step.ExecuteMethodExeID = nextStep.ExecuteMethodExeID
			if nextStep.ExecuteMethodExeID > 0 {
				mutation.update.SetStepMethodCounterIfGreater(mutation.run.StepMethodExeCounter, nextStep.ExecuteMethodExeID)
			}
		} else {
			step.WaitForMethodExeID = nextStep.WaitForMethodExeID
			if nextStep.WaitForMethodExeID > 0 {
				mutation.update.SetStepMethodCounterIfGreater(mutation.run.StepMethodExeCounter, nextStep.WaitForMethodExeID)
			}
		}
		mutation.update.ActiveStepExecutions[stepExeID] = step
	}
}

func (mutation *runMutation) RemoveSteps(stepExeIDs ...string) {
	for _, stepExeID := range stepExeIDs {
		mutation.update.ActiveStepExecutions[stepExeID] = nil
	}
}

func (mutation *runMutation) TransitionStepToWaitingForCondition(stepExeID string, waitCond p.WaitForCondition) {
	existing := mutation.run.ActiveStepExecutions[stepExeID]
	step := existing
	step.Status = p.StepExeStatusWaitingForCondition
	step.WaitForCondition = &waitCond
	step.WaitForRetryState = nil
	mutation.update.ActiveStepExecutions[stepExeID] = &step
}

func (mutation *runMutation) PromoteReportedUnblocks(unblocks []*pb.StepUnblocked) {
	applyWorkerReportedUnblocks(mutation.run, mutation.update, unblocks)
}

func (mutation *runMutation) SpliceChannelsOnExecuteAndPublishInternalChannels(
	req *pb.StepExecuteCompletedRequest,
	channelPubs map[string][]p.ChannelMessage,
) {
	splicedChannels := spliceConsumedMessages(mutation.run.UnconsumedChannelMessages, mutation.run.ActiveStepExecutions, req)
	applyChannelPublishes(mutation.update, mutation.run.UnconsumedChannelMessages, channelPubs, splicedChannels)
}

func (mutation *runMutation) PublishInternalChannels(channelPubs map[string][]p.ChannelMessage) {
	applyChannelPublishes(mutation.update, mutation.run.UnconsumedChannelMessages, channelPubs, nil)
}

func (mutation *runMutation) PublishExternalChannels(channelName string, messages []p.ChannelMessage) {
	baseID := mutation.run.ExternalChannelMessageCounter
	newExtCounter := baseID + int64(len(messages))
	existing := mutation.run.UnconsumedChannelMessages[channelName]
	full := make([]p.ChannelMessage, 0, len(existing)+len(messages))
	full = append(full, existing...)
	full = append(full, messages...)
	if mutation.update.ReplaceUnconsumedChannels == nil {
		mutation.update.ReplaceUnconsumedChannels = make(map[string][]p.ChannelMessage)
	}
	mutation.update.ReplaceUnconsumedChannels[channelName] = full
	mutation.update.ExternalChannelMessageCounter = &newExtCounter
	metrics.CounterChannelExternalPublish.Inc(metrics.TagNamespace(mutation.run.Namespace))
}

func (mutation *runMutation) TransitionToTerminal(status p.RunStatus, reason TransitionReason) {
	mutation.update.Status = &status
	mutation.transitionReason = reason
	if status.IsTerminal() {
		mutation.update.HeartbeatTimerID = ptr.Any(ids.TaskID{})
	}
}

func (mutation *runMutation) TransitionToStatusFromStopDecision(stopDecision pb.StopDecision, reason TransitionReason) {
	newStatus := computeNewStatus(mutation.run, mutation.update, stopDecision)
	mutation.update.Status = &newStatus
	mutation.transitionReason = reason
}

func computeNewStatus(run *p.RunRow, update *p.RunRowUpdate, stopDecision pb.StopDecision) p.RunStatus {
	switch stopDecision {
	case pb.StopDecision_STOP_DECISION_COMPLETE:
		return p.RunStatusCompleted
	case pb.StopDecision_STOP_DECISION_FAIL:
		return p.RunStatusFailed
	}
	activeSteps := make(map[string]p.ActiveStepExecution)
	for key, value := range run.ActiveStepExecutions {
		activeSteps[key] = value
	}
	for key, value := range update.ActiveStepExecutions {
		if value == nil {
			delete(activeSteps, key)
		} else {
			activeSteps[key] = *value
		}
	}
	if len(activeSteps) == 0 {
		return p.RunStatusCompleted
	}
	return p.RunStatusRunning
}

func (mutation *runMutation) appendResumeDispatchTask() {
	mutation.newTasks = append(mutation.newTasks, newImmediateTask(mutation.shardID, p.ImmediateTaskRunResumeDispatch, p.ImmediateTaskInfo{
		RunID: mutation.run.ID, Namespace: mutation.run.Namespace, TaskListName: mutation.run.TaskListName,
	}))
}

func (mutation *runMutation) EnqueueInitialDispatchTask() {
	mutation.newTasks = append(mutation.newTasks, newImmediateTask(mutation.shardID, p.ImmediateTaskRunInitialDispatch, p.ImmediateTaskInfo{
		RunID: mutation.run.ID, Namespace: mutation.run.Namespace, TaskListName: mutation.run.TaskListName,
	}))
}

func (mutation *runMutation) TransitionToRunning(workerID string, heartbeatDuration time.Duration, reason TransitionReason) {
	running := p.RunStatusRunning
	nowTime := mutation.now
	newTimerID := ids.NewTaskID()
	mutation.update.Status = &running
	mutation.update.LastHeartbeatTime = &nowTime
	mutation.update.HeartbeatTimerID = &newTimerID
	mutation.update.WorkerID = &workerID
	mutation.newTasks = append(mutation.newTasks, p.TaskRow{Timer: &p.TimerTaskRow{
		ShardID:  mutation.shardID,
		ID:       newTimerID,
		SortKey:  mutation.now.Add(heartbeatDuration).UnixMilli(),
		TaskType: p.TimerTaskRunHeartbeat,
		TaskInfo: p.TimerTaskInfo{RunID: mutation.run.ID, Namespace: mutation.run.Namespace},
	}})
	mutation.ops.AddVisibility(running)
	mutation.transitionReason = reason
}

func (mutation *runMutation) TransitionToAllStepsWaitingForConditions(reason TransitionReason) {
	allWaiting := p.RunStatusAllStepsWaitingForConditions
	emptyWorker := ""
	mutation.update.Status = &allWaiting
	mutation.update.WorkerID = &emptyWorker
	mutation.update.HeartbeatTimerID = ptr.Any(ids.TaskID{})
	mutation.armDurableTimerIfNeeded()
	mutation.ops.AddVisibility(allWaiting)
	mutation.transitionReason = reason
}

func (mutation *runMutation) MaybeTransitionToPendingIfPromoteWaitingSteps(effectiveNow int64, reason TransitionReason) (bool, errors.CategorizedError) {
	dispatched, err := mutation.promoteByServerIfAny(effectiveNow)
	if dispatched {
		mutation.transitionReason = reason
	}
	return dispatched, err
}

func (mutation *runMutation) MaybeTransitionToPendingOnDurableTimerFired(effectiveNow int64, reason TransitionReason) errors.CategorizedError {
	mutation.update.ActiveDurableTimerID = ptr.Any(ids.TaskID{})
	// increase the DurableTimerFireAt to prevent the time skew issues
	// no check here cuz timer task queue has guaranteed the firing time increases
	mutation.update.DurableTimerFireAt = ptr.Any(effectiveNow)

	dispatched, err := mutation.promoteByServerIfAny(effectiveNow)
	if err != nil {
		return err
	}
	if dispatched {
		// this means the timer has promoted at least a step to execute
		mutation.transitionReason = reason
		return nil
	}
	// otherwise, check if there is another timer to set up
	mutation.armDurableTimerIfNeeded()
	return nil
}

func (mutation *runMutation) RenewHeartbeatTimer() {
	newTimerID := ids.NewTaskID()
	mutation.update.LastHeartbeatTime = &mutation.now
	mutation.update.HeartbeatTimerID = &newTimerID
	mutation.newTasks = append(mutation.newTasks, p.TaskRow{Timer: &p.TimerTaskRow{
		ShardID:  mutation.shardID,
		ID:       newTimerID,
		SortKey:  mutation.now.Add(mutation.deps.HeartbeatTimerDuration).UnixMilli(),
		TaskType: p.TimerTaskRunHeartbeat,
		TaskInfo: p.TimerTaskInfo{RunID: mutation.run.ID, Namespace: mutation.run.Namespace},
	}})
}

func (mutation *runMutation) AddHistoryRunStart(req *pb.StartRunRequest) {
	mutation.ops.AddHistoryRunStart(req)
}

func (mutation *runMutation) AddHistoryRunStop(status p.RunStatus, reason string) {
	mutation.ops.AddHistoryRunStop(status, reason)
}

func (mutation *runMutation) AddHistoryStepExecuteCompleted(
	req *pb.StepExecuteCompletedRequest,
	fromStepExeID string,
	conditionResults []*pb.ConditionResult,
	workerID string,
) {
	mutation.ops.AddHistoryStepExecuteCompleted(req, fromStepExeID, conditionResults, workerID)
}

func (mutation *runMutation) AddHistoryStepWaitForCompleted(req *pb.StepWaitForCompletedRequest, fromStepExeID string, workerID string) {
	mutation.ops.AddHistoryStepWaitForCompleted(req, fromStepExeID, workerID)
}

func (mutation *runMutation) AddHistoryStepsUnblocked(req *pb.StepsUnblockedRequest, workerID string) {
	mutation.ops.AddHistoryStepsUnblocked(req, workerID)
}

func (mutation *runMutation) AddHistoryChannelPublish(req *pb.PublishToChannelRequest) {
	mutation.ops.AddHistoryChannelPublish(req)
}

func (mutation *runMutation) UpdateVisibility(status p.RunStatus) {
	mutation.ops.AddVisibility(status)
}

func (mutation *runMutation) UpdateVisibilityIfStatusChanged() {
	if mutation.update.Status != nil && *mutation.update.Status != mutation.run.Status {
		mutation.ops.AddVisibility(*mutation.update.Status)
	}
}

func (mutation *runMutation) AddHistoryRunStopIfTerminal() {
	if mutation.update.Status != nil && mutation.update.Status.IsTerminal() {
		mutation.ops.AddHistoryRunStop(*mutation.update.Status, "")
	}
}

func (mutation *runMutation) finalizeOpsTasks() {
	mutation.newTasks = append(mutation.newTasks, mutation.ops.Tasks()...)
	if historyHighWater := mutation.ops.LastHistoryEventIDPtr(); historyHighWater != nil {
		if mutation.mode == commitModeCreate {
			mutation.run.LastHistoryEventID = *historyHighWater
		} else {
			mutation.update.LastHistoryEventID = historyHighWater
		}
	}
}

func (mutation *runMutation) getCurrentMergedActiveStepsView() map[string]p.ActiveStepExecution {
	activeSteps := make(map[string]p.ActiveStepExecution, len(mutation.run.ActiveStepExecutions))
	for key, value := range mutation.run.ActiveStepExecutions {
		activeSteps[key] = value
	}
	if mutation.update.ActiveStepExecutions != nil {
		for key, value := range mutation.update.ActiveStepExecutions {
			if value == nil {
				delete(activeSteps, key)
			} else {
				activeSteps[key] = *value
			}
		}
	}
	return activeSteps
}

func (mutation *runMutation) mergedUnconsumedChannels() map[string][]p.ChannelMessage {
	channels := make(map[string][]p.ChannelMessage, len(mutation.run.UnconsumedChannelMessages))
	for key, value := range mutation.run.UnconsumedChannelMessages {
		channels[key] = value
	}
	if mutation.update.ReplaceUnconsumedChannels != nil {
		for key, value := range mutation.update.ReplaceUnconsumedChannels {
			channels[key] = value
		}
	}
	return channels
}

func stepExeIDFromCounter(stepID string, counter int32) string {
	return fmt.Sprintf("%v-%v", stepID, counter)
}

func logRunStatusTransition(
	logger log.Logger,
	shardID int32,
	namespace, runID, taskListName, fromStatus string,
	toStatus p.RunStatus,
	reason TransitionReason,
) {
	tags := []tag.Tag{
		tag.Shard(shardID),
		tag.Namespace(namespace),
		tag.RunID(runID),
		tag.FromStatus(fromStatus),
		tag.ToStatus(toStatus.Name()),
		tag.Reason(string(reason)),
	}
	if taskListName != "" {
		tags = append(tags, tag.TaskListName(taskListName))
	}
	logger.Info("Run status transitioned", tags...)
}
