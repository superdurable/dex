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
)

func removeElement(s []*dexpb.StaleSkipTimer, i int) []*dexpb.StaleSkipTimer {
	s[i] = s[len(s)-1]
	return s[:len(s)-1]
}

// FixTimerConditionFromActivityOutput converts durationSeconds to firingUnixTimestampSeconds.
// This prevents time drift after continueAsNew.
func FixTimerConditionFromActivityOutput(
	now time.Time,
	waitingCondition *dexpb.WaitingCondition,
) *dexpb.WaitingCondition {
	var timerConditions []*dexpb.TimerCondition
	for _, timerCondition := range waitingCondition.GetTimerConditions() {
		timerConditions = append(timerConditions, &dexpb.TimerCondition{
			ConditionId: timerCondition.ConditionId,
			FiringUnixTimestampSeconds: now.Unix() +
				timerCondition.GetDurationSeconds(),
		})
	}
	waitingCondition.TimerConditions = timerConditions
	return waitingCondition
}
