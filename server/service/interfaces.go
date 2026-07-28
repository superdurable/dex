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

package service

import (
	"github.com/superdurable/dex/gen/dexpb"
)

// BasicInfo contains non-serialized flow identity.
type BasicInfo struct {
	FlowType     string
	WorkerTarget string
}

// StepExecutionStatus is the interpreter's internal per-step-execution outcome.
// It is not serialized to history and has no proto equivalent.
type StepExecutionStatus string

const StepExecutionStatusCompleted StepExecutionStatus = "Completed"
const StepExecutionStatusFailedNoProceed StepExecutionStatus = "Failure"
const StepExecutionStatusWaitingAborted StepExecutionStatus = "WaitingConditions"
const StepExecutionStatusFailedAndProceed StepExecutionStatus = "ExecuteApiFailedAndProceed"

// ValidateTimerSkipRequest validates a pending timer by condition ID or index.
func ValidateTimerSkipRequest(
	timerInfos []*dexpb.TimerInfo,
	timerConditionId string,
	timerConditionIndex int,
) (*dexpb.TimerInfo, bool) {
	if timerConditionId != "" {
		for _, timerInfo := range timerInfos {
			if timerInfo.GetConditionId() == timerConditionId &&
				timerInfo.GetStatus() == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING {
				return timerInfo, true
			}
		}
		return nil, false
	}
	if timerConditionIndex >= 0 && timerConditionIndex < len(timerInfos) {
		timerInfo := timerInfos[timerConditionIndex]
		if timerInfo.GetStatus() == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING {
			return timerInfo, true
		}
	}
	return nil, false
}
