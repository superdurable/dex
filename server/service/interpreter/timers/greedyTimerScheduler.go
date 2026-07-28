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
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/interpreter/cont"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

type timerScheduler struct {
	// Timers requested by the workflow in desc order
	pendingScheduling []*dexpb.TimerInfo
	// timers created through the workflow provider that are going to fire in desc order
	providerScheduledTimerUnixTs []int64
}

func (t *timerScheduler) addTimer(toAdd *dexpb.TimerInfo) {
	if toAdd == nil ||
		toAdd.GetStatus() != dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING {
		panic("invalid timer added")
	}

	insertIndex := 0
	for i, timer := range t.pendingScheduling {
		if toAdd.GetFiringUnixTimestampSeconds() >= timer.GetFiringUnixTimestampSeconds() {
			// don't want dupes. Makes remove simpler
			if toAdd == timer {
				return
			}
			insertIndex = i
			break
		}
		insertIndex = i + 1
	}

	front := t.pendingScheduling[:insertIndex]
	var back []*dexpb.TimerInfo
	if insertIndex >= len(t.pendingScheduling) {
		back = []*dexpb.TimerInfo{toAdd}
	} else {
		back = append([]*dexpb.TimerInfo{toAdd}, t.pendingScheduling[insertIndex:]...)
	}
	t.pendingScheduling = append(front, back...)
}

func (t *timerScheduler) removeTimer(toRemove *dexpb.TimerInfo) {
	for i, timer := range t.pendingScheduling {
		if toRemove == timer {
			t.pendingScheduling = append(t.pendingScheduling[:i], t.pendingScheduling[i+1:]...)
			return
		}
	}
}

func (t *timerScheduler) pruneToNextTimer(pruneTo int64) *dexpb.TimerInfo {
	index := len(t.providerScheduledTimerUnixTs)
	for i := len(t.providerScheduledTimerUnixTs) - 1; i >= 0; i-- {
		timerTime := t.providerScheduledTimerUnixTs[i]
		if timerTime > pruneTo {
			break
		}
		index = i
	}
	// If index is 0, it means all times are in the past
	if index == 0 {
		t.providerScheduledTimerUnixTs = nil
	} else {
		t.providerScheduledTimerUnixTs = t.providerScheduledTimerUnixTs[:index]
	}

	if len(t.pendingScheduling) == 0 {
		return nil
	}

	index = len(t.pendingScheduling)

	for i := len(t.pendingScheduling) - 1; i >= 0; i-- {
		timer := t.pendingScheduling[i]
		if timer.GetFiringUnixTimestampSeconds() > pruneTo &&
			timer.GetStatus() == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING {
			break
		}
		index = i
	}

	// If index is 0, it means all timers are pruned
	if index == 0 {
		t.pendingScheduling = nil
		return nil
	}

	prunedTimer := t.pendingScheduling[index-1]
	t.pendingScheduling = t.pendingScheduling[:index]
	return prunedTimer
}

func startGreedyTimerScheduler(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	continueAsNewCounter *cont.ContinueAsNewCounter,
) *timerScheduler {
	t := timerScheduler{}
	provider.GoNamed(ctx, "greedy-timer-scheduler", func(ctx interfaces.UnifiedContext) {
		for {
			err := provider.Await(ctx, func() bool {
				now := provider.Now(ctx).Unix()
				next := t.pruneToNextTimer(now)
				return next != nil &&
					(len(t.providerScheduledTimerUnixTs) == 0 ||
						next.GetFiringUnixTimestampSeconds() <
							t.providerScheduledTimerUnixTs[len(t.providerScheduledTimerUnixTs)-1]) ||
					continueAsNewCounter.IsThresholdMet()
			})

			// Expecting CanceledError if the ctx is canceled
			if err != nil {
				break
			}

			if continueAsNewCounter.IsThresholdMet() {
				break
			}

			now := provider.Now(ctx).Unix()
			next := t.pruneToNextTimer(now)
			fireAt := next.GetFiringUnixTimestampSeconds()
			duration := time.Duration(fireAt-now) * time.Second
			// This will create a new timer but not yield the goroutines awaiting the timer firing.
			// This works since when a timer fires, a new workflow task is created with the expectation that
			//   there is a goroutines awaiting some condition(some time has past) to continue,
			//   see WaitForTimerFiredOrSkipped.
			provider.NewTimer(ctx, duration)
			t.providerScheduledTimerUnixTs = append(t.providerScheduledTimerUnixTs, fireAt)
		}
	})

	return &t
}
