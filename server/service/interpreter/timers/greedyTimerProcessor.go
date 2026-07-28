// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package timers

import (
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/interpreter/cont"
	"github.com/superdurable/iwf/service/interpreter/interfaces"
)

type GreedyTimerProcessor struct {
	timerManager                   *timerScheduler
	stepExecutionCurrentTimerInfos map[string][]*iwfpb.TimerInfo
	staleSkipTimers                []*iwfpb.StaleSkipTimer
	provider                       interfaces.WorkflowProvider
	logger                         interfaces.UnifiedLogger
}

func NewGreedyTimerProcessor(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	continueAsNewCounter *cont.ContinueAsNewCounter,
	staleSkipTimers []*iwfpb.StaleSkipTimer,
) *GreedyTimerProcessor {

	// start some single thread that manages pendingScheduling
	scheduler := startGreedyTimerScheduler(ctx, provider, continueAsNewCounter)

	tp := &GreedyTimerProcessor{
		provider:                       provider,
		timerManager:                   scheduler,
		stepExecutionCurrentTimerInfos: map[string][]*iwfpb.TimerInfo{},
		logger:                         provider.GetLogger(ctx),
		staleSkipTimers:                staleSkipTimers,
	}

	return tp
}

func (t *GreedyTimerProcessor) Dump(
	isStepExecutionActive func(stepExeId string) bool,
) []*iwfpb.StaleSkipTimer {
	if isStepExecutionActive == nil {
		panic("isStepExecutionActive is required")
	}
	kept := make([]*iwfpb.StaleSkipTimer, 0, len(t.staleSkipTimers))
	for _, staleSkip := range t.staleSkipTimers {
		if isStepExecutionActive(staleSkip.GetStepExecutionId()) {
			kept = append(kept, staleSkip)
		}
	}
	t.staleSkipTimers = kept
	return kept
}

func (t *GreedyTimerProcessor) GetTimerInfos() map[string][]*iwfpb.TimerInfo {
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
		t.staleSkipTimers = append(t.staleSkipTimers, &iwfpb.StaleSkipTimer{
			StepExecutionId:     stepExeId,
			TimerConditionId:    timerId,
			TimerConditionIndex: int32(timerIdx),
		})
		return false
	}
	timer.Status = iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED
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
) iwfpb.InternalTimerStatus {
	timerInfos := t.stepExecutionCurrentTimerInfos[stepExeId]
	if len(timerInfos) == 0 {
		if *cancelWaiting {
			// The step may remove timers before this waiting thread resumes.
			// Return pending because waiting was already canceled.
			return iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING
		} else {
			panic("bug: this shouldn't happen")
		}
	}
	timer := timerInfos[timerIdx]
	if timer.GetStatus() == iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED ||
		timer.GetStatus() == iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED {
		return timer.GetStatus()
	}
	// ReapplyStaleSkipTimer may skip a different timer; only return if this one changed.
	if t.ReapplyStaleSkipTimer() {
		if timer.GetStatus() == iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED ||
			timer.GetStatus() == iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED {
			t.logger.Warn("timer skipped by stale skip signal", stepExeId, timerIdx)
			return timer.GetStatus()
		}
	}

	// Await cancellation is handled by the status checks below.
	_ = t.provider.Await(ctx, func() bool {
		// Timer firing creates a workflow task that reevaluates waiting goroutines.
		return timer.GetStatus() == iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED ||
			timer.GetStatus() == iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED ||
			timer.GetFiringUnixTimestampSeconds() <= t.provider.Now(ctx).Unix() ||
			*cancelWaiting
	})

	if timer.GetStatus() == iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED {
		return iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED
	}
	if timer.GetFiringUnixTimestampSeconds() <= t.provider.Now(ctx).Unix() {
		timer.Status = iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED
		return iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_FIRED
	}

	// Otherwise cancellation occurred and the timer remains pending.
	t.timerManager.removeTimer(timer)
	return iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING
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
	timerConditions []*iwfpb.TimerCondition,
	completedTimerConditions map[int32]iwfpb.InternalTimerStatus,
) {
	timers := make([]*iwfpb.TimerInfo, len(timerConditions))
	for idx, cmd := range timerConditions {
		var timer iwfpb.TimerInfo
		if status, ok := completedTimerConditions[int32(idx)]; ok {
			timer = iwfpb.TimerInfo{
				ConditionId:                cmd.GetConditionId(),
				FiringUnixTimestampSeconds: cmd.GetFiringUnixTimestampSeconds(),
				Status:                     status,
			}
		} else {
			timer = iwfpb.TimerInfo{
				ConditionId:                cmd.GetConditionId(),
				FiringUnixTimestampSeconds: cmd.GetFiringUnixTimestampSeconds(),
				Status:                     iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING,
			}
		}
		if timer.GetStatus() == iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING {
			t.timerManager.addTimer(&timer)
		}
		timers[idx] = &timer
	}
	t.stepExecutionCurrentTimerInfos[stepExeId] = timers
	for t.ReapplyStaleSkipTimer() {
	}
}
