// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dex

import (
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"time"
)

type ResetWorkflowOptions struct {
	// ResetType is required
	ResetType dexpb.WorkflowResetType
	// Reason is required
	Reason string
	// HistoryEventId is required for dexpb.HISTORY_EVENT_ID resetType
	HistoryEventId *int32
	// HistoryEventTime is required for dexpb.HISTORY_EVENT_TIME resetType
	HistoryEventTime *time.Time
	//  StateId is required for dexpb.STATE_ID resetType
	StateId *string
	// StateExecutionId is required for dexpb.STATE_EXECUTION_ID resetType
	StateExecutionId *string
	// SkipSignalReapply is optional, default to false on server, which will re-apply all signals
	SkipSignalReapply *bool
}

func ResetToBeginning(reason string) ResetWorkflowOptions {
	return ResetWorkflowOptions{
		ResetType: dexpb.BEGINNING,
		Reason:    reason,
	}
}

func ResetToHistoryEventId(historyEventId int32, reason string) ResetWorkflowOptions {
	return ResetWorkflowOptions{
		ResetType:      dexpb.HISTORY_EVENT_ID,
		Reason:         reason,
		HistoryEventId: &historyEventId,
	}
}

func ResetToHistoryEventTime(historyEventTime time.Time, reason string) ResetWorkflowOptions {
	return ResetWorkflowOptions{
		ResetType:        dexpb.HISTORY_EVENT_TIME,
		Reason:           reason,
		HistoryEventTime: &historyEventTime,
	}
}

func ResetToStateId(stateId, reason string) ResetWorkflowOptions {
	return ResetWorkflowOptions{
		ResetType: dexpb.STATE_ID,
		Reason:    reason,
		StateId:   &stateId,
	}
}

func ResetToStateExecutionId(stateExecutionId, reason string) ResetWorkflowOptions {
	return ResetWorkflowOptions{
		ResetType:        dexpb.STATE_EXECUTION_ID,
		Reason:           reason,
		StateExecutionId: &stateExecutionId,
	}
}
