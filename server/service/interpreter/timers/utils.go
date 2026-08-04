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
