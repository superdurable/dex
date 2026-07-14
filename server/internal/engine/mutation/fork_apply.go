package mutation

import (
	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/common/utils/ptr"
	p "github.com/superdurable/dex/server/internal/persistence"
)

const forkWorkerRequestCounterBump = int64(1000)

// ApplyForkRestore replaces run execution state and prepares re-dispatch.
func (mutation *runMutation) ApplyForkRestore(
	stateMap map[string]p.Value,
	channels map[string][]p.ChannelMessage,
	counters map[string]int32,
	activeSteps map[string]p.ActiveStepExecution,
	externalCounter int64,
) {
	emptyWorker := ""
	mutation.update.ReplaceStateMap = &stateMap
	mutation.update.ReplaceAllUnconsumedChannels = &channels
	mutation.update.ReplaceStepExeIDCounters = &counters
	mutation.update.ReplaceActiveStepExecutions = &activeSteps
	mutation.update.ExternalChannelMessageCounter = ptr.Any(externalCounter)
	mutation.update.WorkerID = &emptyWorker
	mutation.update.LastHeartbeatTime = &mutation.now
	mutation.update.HeartbeatTimerID = ptr.Any(ids.TaskID{})
	mutation.update.ActiveDurableTimerID = ptr.Any(ids.TaskID{})
	mutation.update.DurableTimerFireAt = ptr.Any(int64(0))
	mutation.update.DurableTimerFired = ptr.Any(false)

	newCounter := mutation.run.WorkerRequestCounter + forkWorkerRequestCounterBump
	mutation.update.WorkerRequestCounter = &newCounter

	mutation.applyForkStatusAndTimers(activeSteps)
}

// ForkRestoreFromRunStart builds empty state plus starting active steps.
func ForkRestoreFromRunStart(payload *pb.HistoryRunStartPayload) (
	stateMap map[string]p.Value,
	channels map[string][]p.ChannelMessage,
	counters map[string]int32,
	activeSteps map[string]p.ActiveStepExecution,
	externalCounter int64,
) {
	steps := startingStepsFromHistoryRunStart(payload)
	activeSteps, counters = activeStepsFromStartingSteps(steps)
	return map[string]p.Value{}, map[string][]p.ChannelMessage{}, counters, activeSteps, 0
}

func (mutation *runMutation) applyForkStatusAndTimers(activeSteps map[string]p.ActiveStepExecution) {
	if mutation.hasInvokingSteps(activeSteps) {
		pending := p.RunStatusPending
		mutation.update.Status = &pending
		mutation.appendResumeDispatchTask()
		mutation.transitionReason = TransitionReasonForkRun
		return
	}
	if len(activeSteps) == 0 || !mutation.allStepsWaiting(activeSteps) {
		pending := p.RunStatusPending
		mutation.update.Status = &pending
		mutation.appendResumeDispatchTask()
		mutation.transitionReason = TransitionReasonForkRun
		return
	}

	effectiveNow := mutation.now.UnixMilli()
	minFireAt := earliestTimerFireAt(activeSteps)
	if minFireAt > 0 && minFireAt <= effectiveNow {
		mutation.markCurrentDurableTimerFired()
		if dispatched, err := mutation.promoteByServerIfAny(effectiveNow); err == nil && dispatched {
			mutation.transitionReason = TransitionReasonForkRun
			return
		}
	}
	allWaiting := p.RunStatusAllStepsWaitingForConditions
	mutation.update.Status = &allWaiting
	mutation.armDurableTimerIfNeeded()
	mutation.transitionReason = TransitionReasonForkRun
}

func (mutation *runMutation) hasInvokingSteps(activeSteps map[string]p.ActiveStepExecution) bool {
	for _, step := range activeSteps {
		if step.Status == p.StepExeStatusInvokingWaitFor || step.Status == p.StepExeStatusInvokingExecute {
			return true
		}
	}
	return false
}

func (mutation *runMutation) allStepsWaiting(activeSteps map[string]p.ActiveStepExecution) bool {
	for _, step := range activeSteps {
		if step.Status != p.StepExeStatusWaitingForCondition {
			return false
		}
	}
	return true
}

func earliestTimerFireAt(activeSteps map[string]p.ActiveStepExecution) int64 {
	var minFireAt int64
	for _, step := range activeSteps {
		if step.WaitForCondition == nil {
			continue
		}
		for _, condition := range step.WaitForCondition.Conditions {
			if condition.Timer == nil {
				continue
			}
			if minFireAt == 0 || condition.Timer.FireAtUnixMs < minFireAt {
				minFireAt = condition.Timer.FireAtUnixMs
			}
		}
	}
	return minFireAt
}

// startingStepsFromHistoryRunStart converts a RunStart history payload to spawn inputs.
func startingStepsFromHistoryRunStart(payload *pb.HistoryRunStartPayload) []StartingStep {
	if payload == nil {
		return nil
	}
	steps := make([]StartingStep, 0, len(payload.StartingSteps))
	for _, startingStep := range payload.StartingSteps {
		input := p.Value{Type: p.ValueTypeNull}
		if startingStep.Input != nil {
			input = historyPbValueToPersistence(startingStep.Input)
		}
		steps = append(steps, StartingStep{
			StepID:      startingStep.StepId,
			Input:       input,
			SkipWaitFor: startingStep.SkipWaitFor,
		})
	}
	return steps
}

// activeStepsFromStartingSteps builds replace maps for fork-to-start.
func activeStepsFromStartingSteps(steps []StartingStep) (
	activeSteps map[string]p.ActiveStepExecution,
	counters map[string]int32,
) {
	activeSteps = make(map[string]p.ActiveStepExecution)
	counters = make(map[string]int32)
	stepMethodCounter := int64(0)
	for _, startingStep := range steps {
		counter := counters[startingStep.StepID] + 1
		counters[startingStep.StepID] = counter
		stepExeID := stepExeIDFromCounter(startingStep.StepID, counter)
		step := p.ActiveStepExecution{
			Input:  startingStep.Input,
			Status: p.StepExeStatusInvokingWaitFor,
		}
		if startingStep.SkipWaitFor {
			step.Status = p.StepExeStatusInvokingExecute
			stepMethodCounter++
			step.ExecuteMethodExeID = stepMethodCounter
		} else {
			stepMethodCounter++
			step.WaitForMethodExeID = stepMethodCounter
		}
		activeSteps[stepExeID] = step
	}
	return activeSteps, counters
}
