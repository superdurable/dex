package mutation

import (
	"sort"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/internal/engine/evaluate"
	"github.com/superdurable/dex/server/internal/engine/pbconv"
	p "github.com/superdurable/dex/server/internal/persistence"
)

type serverPromotedStep struct {
	stepExeID string
	result    evaluate.EvaluationResult
}

func (mutation *runMutation) promoteByServerIfAny(effectiveNow int64) (bool, errors.CategorizedError) {
	activeSteps := mutation.getCurrentMergedActiveStepsView()
	unconsumed := mutation.mergedUnconsumedChannels()
	_, promoted := promoteAllSatisfiedWaitingSteps(mutation.run, mutation.update, activeSteps, unconsumed, effectiveNow)
	if !promoted {
		return false, nil
	}
	pending := p.RunStatusPending
	mutation.update.Status = &pending
	mutation.appendResumeDispatchTask()
	return true, nil
}

func promoteAllSatisfiedWaitingSteps(
	run *p.RunRow,
	update *p.RunRowUpdate,
	activeSteps map[string]p.ActiveStepExecution,
	unconsumed map[string][]p.ChannelMessage,
	effectiveNow int64,
) ([]serverPromotedStep, bool) {
	evaluator := evaluate.NewConditionEvaluator(activeSteps, effectiveNow, unconsumed)

	// Sort so exeID allocation (and thus channel-reservation ordering) is
	// deterministic, mirroring the SDK's reEvaluateWaitingSteps.
	var waitingIDs []string
	for stepExeID, step := range activeSteps {
		if step.Status != p.StepExeStatusWaitingForCondition || step.WaitForCondition == nil {
			continue
		}
		waitingIDs = append(waitingIDs, stepExeID)
	}
	sort.Strings(waitingIDs)

	var promoted []serverPromotedStep
	for _, stepExeID := range waitingIDs {
		result, evalErr := evaluator.EvaluateWaitForCondition(stepExeID)
		if evalErr != nil {
			return nil, false
		}
		if !result.Satisfied {
			continue
		}
		// Apply the reservation back into activeSteps (the evaluator's map)
		// BEFORE evaluating the next step, so competing same-channel siblings
		// account for messages this step just reserved — otherwise N steps
		// waiting on one message would all promote (available=queue-reserved
		// would be violated).
		step := activeSteps[stepExeID]
		step.Status = p.StepExeStatusInvokingExecute
		step.ConditionResults = append([]p.ConditionResult(nil), result.ConditionResults...)
		step.ExecuteMethodExeID = update.AllocateStepMethodExeCounter(run.StepMethodExeCounter)
		activeSteps[stepExeID] = step
		update.ActiveStepExecutions[stepExeID] = &step

		promoted = append(promoted, serverPromotedStep{stepExeID: stepExeID, result: result})
	}
	if len(promoted) == 0 {
		return nil, false
	}
	return promoted, true
}

func applyWorkerReportedUnblocks(
	run *p.RunRow,
	update *p.RunRowUpdate,
	unblocks []*pb.StepUnblocked,
) {
	if len(unblocks) == 0 {
		return
	}
	for _, unblocked := range unblocks {
		existing, ok := run.ActiveStepExecutions[unblocked.StepExeId]
		if !ok {
			if updated, okUpdate := lookupMergedStep(run, update, unblocked.StepExeId); okUpdate {
				existing = updated
			} else {
				continue
			}
		}
		step := existing
		step.Status = p.StepExeStatusInvokingExecute
		step.ConditionResults = pbconv.PbConditionResultsToPersistence(unblocked.ConditionResults)
		step.ExecuteMethodExeID = unblocked.ExecuteMethodExeId
		update.ActiveStepExecutions[unblocked.StepExeId] = &step
		if unblocked.ExecuteMethodExeId > 0 {
			update.SetStepMethodCounterIfGreater(run.StepMethodExeCounter, unblocked.ExecuteMethodExeId)
		}
	}
}

func lookupMergedStep(run *p.RunRow, update *p.RunRowUpdate, stepExeID string) (p.ActiveStepExecution, bool) {
	if update.ActiveStepExecutions != nil {
		if updated, ok := update.ActiveStepExecutions[stepExeID]; ok && updated != nil {
			return *updated, true
		}
	}
	step, ok := run.ActiveStepExecutions[stepExeID]
	return step, ok
}
