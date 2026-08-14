// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package timers

import (
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/interpreter/cont"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

type GreedyTimerProcessor struct {
	timerManager                   *timerScheduler
	stepExecutionCurrentTimerInfos map[string][]*dexpb.TimerInfo
	staleSkipTimers                []*dexpb.StaleSkipTimer
	provider                       interfaces.WorkflowProvider
	logger                         interfaces.UnifiedLogger
}

func NewGreedyTimerProcessor(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	continueAsNewCounter *cont.ContinueAsNewCounter,
	staleSkipTimers []*dexpb.StaleSkipTimer,
) *GreedyTimerProcessor {

	// start some single thread that manages pendingScheduling
	scheduler := startGreedyTimerScheduler(ctx, provider, continueAsNewCounter)

	tp := &GreedyTimerProcessor{
		provider:                       provider,
		timerManager:                   scheduler,
		stepExecutionCurrentTimerInfos: map[string][]*dexpb.TimerInfo{},
		logger:                         provider.GetLogger(ctx),
		staleSkipTimers:                staleSkipTimers,
	}

	return tp
}

func (t *GreedyTimerProcessor) Dump(
	isStepExecutionActive func(stepExeId string) bool,
) []*dexpb.StaleSkipTimer {
	if isStepExecutionActive == nil {
		panic("isStepExecutionActive is required")
	}
	kept := make([]*dexpb.StaleSkipTimer, 0, len(t.staleSkipTimers))
	for _, staleSkip := range t.staleSkipTimers {
		if isStepExecutionActive(staleSkip.GetStepExecutionId()) {
			kept = append(kept, staleSkip)
		}
	}
	t.staleSkipTimers = kept
	return kept
}

func (t *GreedyTimerProcessor) GetTimerInfos() map[string][]*dexpb.TimerInfo {
	return t.stepExecutionCurrentTimerInfos
}

func (t *GreedyTimerProcessor) GetTimerStartedUnixTimestamps() []int64 {
	return t.timerManager.providerScheduledTimerUnixTs
}

// SkipTimer will attempt to skip a timer, return false if no valid timer found
func (t *GreedyTimerProcessor) SkipTimer(
	stepExeId string,
	timerId string,
	timerIdx int,
) bool {
	timer, valid := service.ValidateTimerSkipRequest(
		t.stepExecutionCurrentTimerInfos[stepExeId],
		timerId,
		timerIdx,
	)
	if !valid {
		// API validation makes this a rare race, usually after the step closes.
		t.logger.Warn(
			"cannot process timer skip request, maybe step is already closed...putting into a stale skip timer queue",
			stepExeId,
			timerId,
			timerIdx,
		)
		t.staleSkipTimers = append(t.staleSkipTimers, &dexpb.StaleSkipTimer{
			StepExecutionId:     stepExeId,
			TimerConditionId:    timerId,
			TimerConditionIndex: int32(timerIdx),
		})
		return false
	}
	timer.Status = dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED
	return true
}

func (t *GreedyTimerProcessor) ReapplyStaleSkipTimer() bool {
	for i, staleSkip := range t.staleSkipTimers {
		found := t.SkipTimer(
			staleSkip.GetStepExecutionId(),
			staleSkip.GetTimerConditionId(),
			int(staleSkip.GetTimerConditionIndex()),
		)
		if found {
			newList := removeElement(t.staleSkipTimers, i)
			t.staleSkipTimers = newList
			return true
		}
	}
	return false
}

// WaitForTimerFiredOrSkipped waits until a timer completes.
// It returns pending when step completion or continue-as-new cancels waiting.
func (t *GreedyTimerProcessor) WaitForTimerFiredOrSkipped(
	ctx interfaces.UnifiedContext, stepExeId string, timerIdx int, cancelWaiting *bool,
) dexpb.InternalTimerStatus {
	timerInfos := t.stepExecutionCurrentTimerInfos[stepExeId]
	if len(timerInfos) == 0 {
		if *cancelWaiting {
			// The step may remove timers before this waiting thread resumes.
			// Return pending because waiting was already canceled.
			return dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING
		} else {
			panic("bug: this shouldn't happen")
		}
	}
	timer := timerInfos[timerIdx]
	if timer.GetStatus() == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED ||
		timer.GetStatus() == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED {
		return timer.GetStatus()
	}
	// ReapplyStaleSkipTimer may skip a different timer; only return if this one changed.
	if t.ReapplyStaleSkipTimer() {
		if timer.GetStatus() == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED ||
			timer.GetStatus() == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED {
			t.logger.Warn("timer skipped by stale skip signal", stepExeId, timerIdx)
			return timer.GetStatus()
		}
	}

	if err := t.provider.Await(ctx, func() bool {
		// Timer firing creates a workflow task that reevaluates waiting goroutines.
		return timer.GetStatus() == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED ||
			timer.GetStatus() == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED ||
			timer.GetFiringUnixTimestampSeconds() <= t.provider.Now(ctx).Unix() ||
			*cancelWaiting
	}); err != nil {
		t.timerManager.removeTimer(timer)
		return dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING
	}

	if timer.GetStatus() == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED {
		return dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED
	}
	if timer.GetFiringUnixTimestampSeconds() <= t.provider.Now(ctx).Unix() {
		timer.Status = dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED
		return dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED
	}

	// Otherwise cancellation occurred and the timer remains pending.
	t.timerManager.removeTimer(timer)
	return dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING
}

// RemovePendingTimersOfStep removes pending scheduling when a step completes.
func (t *GreedyTimerProcessor) RemovePendingTimersOfStep(stepExeId string) {
	timers := t.stepExecutionCurrentTimerInfos[stepExeId]

	for _, timer := range timers {
		t.timerManager.removeTimer(timer)
	}

	delete(t.stepExecutionCurrentTimerInfos, stepExeId)
}

func (t *GreedyTimerProcessor) AddTimers(
	stepExeId string,
	timerConditions []*dexpb.TimerCondition,
	completedTimerConditions map[int32]dexpb.InternalTimerStatus,
) {
	timers := make([]*dexpb.TimerInfo, len(timerConditions))
	for idx, cmd := range timerConditions {
		var timer dexpb.TimerInfo
		if status, ok := completedTimerConditions[int32(idx)]; ok {
			timer = dexpb.TimerInfo{
				ConditionId:                cmd.GetConditionId(),
				FiringUnixTimestampSeconds: cmd.GetFiringUnixTimestampSeconds(),
				Status:                     status,
			}
		} else {
			timer = dexpb.TimerInfo{
				ConditionId:                cmd.GetConditionId(),
				FiringUnixTimestampSeconds: cmd.GetFiringUnixTimestampSeconds(),
				Status:                     dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING,
			}
		}
		if timer.GetStatus() == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING {
			t.timerManager.addTimer(&timer)
		}
		timers[idx] = &timer
	}
	t.stepExecutionCurrentTimerInfos[stepExeId] = timers
	for t.ReapplyStaleSkipTimer() {
	}
}
