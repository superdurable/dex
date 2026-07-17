// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package persistence

import (
	"common-go/ids"
	"fmt"
	"time"
)

// ============================================================================
// Run Row
// ============================================================================

type RowType int32

const (
	RowTypeRun           RowType = 1
	RowTypeImmediateTask RowType = 2
	RowTypeTimerTask     RowType = 3
)

type RunStatus int32

const (
	RunStatusInvalid                      RunStatus = 0 // reserved -- not a valid status
	RunStatusPending                      RunStatus = 1 // just created, will be dispatched
	RunStatusWaitingForWorker             RunStatus = 2 // waiting for worker to pick up
	RunStatusRunning                      RunStatus = 3 // a worker is owning and running it
	RunStatusAllStepsWaitingForConditions RunStatus = 4 // all steps are waiting for certain conditions
	RunStatusCompleted                    RunStatus = 5 // run completed successfully (by itself or by stopRun API)
	RunStatusFailed                       RunStatus = 6 // run failed (by itself or by stopRun API)
)

func (s RunStatus) IsStopped() bool {
	return s == RunStatusCompleted || s == RunStatusFailed
}

func (s RunStatus) Name() string {
	switch s {
	case RunStatusInvalid:
		return "invalid"
	case RunStatusPending:
		return "pending"
	case RunStatusWaitingForWorker:
		return "waiting_for_worker"
	case RunStatusRunning:
		return "running"
	case RunStatusAllStepsWaitingForConditions:
		return "all_steps_waiting_for_conditions"
	case RunStatusCompleted:
		return "completed"
	case RunStatusFailed:
		return "failed"
	default:
		return fmt.Sprintf("unknown_%d", s)
	}
}

type StepExecutionStatus int32

const (
	StepExeStatusInvalid                                 = 0
	StepExeStatusInvokingWaitFor     StepExecutionStatus = 1
	StepExeStatusWaitingForCondition StepExecutionStatus = 2
	StepExeStatusInvokingExecute     StepExecutionStatus = 3
)

type RunRow struct {
	ShardID   int32   `bson:"shard_id"`
	RowType   RowType `bson:"row_type"`
	Namespace string  `bson:"namespace"`
	SortKey   int64   `bson:"sort_key"`
	ID        string  `bson:"id"`

	FlowType                string    `bson:"flow_type"`
	TaskListName            string    `bson:"task_list_name"`
	HeartbeatTimeoutSeconds int32     `bson:"heartbeat_timeout_seconds"`
	Status                  RunStatus `bson:"status"`
	Version                 int64     `bson:"version"`
	WorkerID                string    `bson:"worker_id"`

	DataAttributes            map[string]Value               `bson:"data_attributes"`
	UnconsumedChannelMessages map[string][]ChannelMessage    `bson:"unconsumed_channel_messages"`
	StepExeIDCounters         map[string]int32               `bson:"step_exe_id_counters"`
	ActiveStepExecutions      map[string]ActiveStepExecution `bson:"active_step_executions"`

	WorkerRequestCounter          int64 `bson:"worker_request_counter"`
	ExternalChannelMessageCounter int64 `bson:"external_channel_message_counter"`
	// StepMethodExeCounter is a run-global monotonic counter for waitForMethodExeID
	// and executeMethodExeID allocation.
	StepMethodExeCounter int64 `bson:"step_method_exe_counter"`

	LastHeartbeatTime time.Time `bson:"last_heartbeat_time"`
	HeartbeatTimerID  ids.UID   `bson:"heartbeat_timer_id"`

	ActiveDurableTimerID ids.UID `bson:"active_durable_timer_id"`
	// record last durable timer fired at to avoid time skew
	DurableTimerFiredAt int64 `bson:"durable_timer_fired_at"`

	// LastHistoryEventID is the highest event_id allocated for this run
	LastHistoryEventID int64 `bson:"last_history_event_id"`

	CreatedAt time.Time `bson:"created_at"`
	UpdatedAt time.Time `bson:"updated_at"`
}

// RunRowUpdate is a partial update applied via UpdateRunWithNewTasks.
// nil/zero fields are not modified.
type RunRowUpdate struct {
	Status                        *RunStatus
	WorkerID                      *string
	DataAttributes                map[string]Value                // fields to upsert (delta)
	ReplaceUnconsumedChannels     map[string][]ChannelMessage     // channels to replace entirely ($set)
	StepExeIDCounters             map[string]int32                // counters to set
	ActiveStepExecutions          map[string]*ActiveStepExecution // nil value = delete key
	WorkerRequestCounter          *int64
	ExternalChannelMessageCounter *int64
	StepMethodExeCounter          *int64
	LastHeartbeatTime             *time.Time
	HeartbeatTimerID              *ids.UID
	ActiveDurableTimerID          *ids.UID
	DurableTimerFiredAt           *int64
	LastHistoryEventID            *int64
	// Full-map replace fields (used by ForkRun; nil = not modified).
	ReplaceDataAttributes        *map[string]Value
	ReplaceActiveStepExecutions  *map[string]ActiveStepExecution
	ReplaceStepExeIDCounters     *map[string]int32
	ReplaceAllUnconsumedChannels *map[string][]ChannelMessage
}

func (ru *RunRowUpdate) AllocateStepMethodExeCounter(fromRunRow int64) int64 {
	if ru.StepMethodExeCounter == nil {
		ru.StepMethodExeCounter = new(fromRunRow)
	}
	ru.StepMethodExeCounter = new(*ru.StepMethodExeCounter + 1)
	return *ru.StepMethodExeCounter
}
func (ru *RunRowUpdate) SetStepMethodCounterIfGreater(fromRunRow, input int64) bool {
	if ru.StepMethodExeCounter == nil {
		ru.StepMethodExeCounter = new(fromRunRow)
	}
	if input > *ru.StepMethodExeCounter {
		ru.StepMethodExeCounter = new(input)
		return true
	}
	return false
}

type ActiveStepExecution struct {
	Input             Value               `bson:"input"`
	Status            StepExecutionStatus `bson:"status"`
	WaitForRetryState *RetryState         `bson:"wait_for_retry_state,omitempty"`
	ExecuteRetryState *RetryState         `bson:"execute_retry_state,omitempty"`
	// FromStepExeID is the parent step_exe_id that spawned this step via
	// NextSteps. Empty for starting steps (those created by StartRun).
	FromStepExeID string `bson:"from_step_exe_id,omitempty"`
	// WaitForMethodExeID is allocated once when the step enters WaitFor phase.
	WaitForMethodExeID int64 `bson:"wait_for_method_exe_id,omitempty"`
	// ExecuteMethodExeID is allocated on WAITING -> INVOKING_EXECUTE promote.
	ExecuteMethodExeID int64 `bson:"execute_method_exe_id,omitempty"`
}

// TaskRow is a union type for CreateRunWithTasks / UpdateRunWithNewTasks.
// Exactly one of Immediate / Timer is non-nil.
type TaskRow struct {
	Immediate *ImmediateTaskRow
	Timer     *TimerTaskRow
}

// ============================================================================
// Immediate Task Row
// ============================================================================

type ImmediateTaskInfo struct {
	RunID              string `bson:"run_id"`
	Namespace          string `bson:"namespace"`
	TaskListName       string `bson:"task_list_name"`
	DurableTimerFireAt int64  `bson:"durable_timer_fire_at,omitempty"`
}

type ImmediateTaskRow struct {
	ShardID   int32             `bson:"shard_id"`
	RowType   RowType           `bson:"row_type"`
	Namespace string            `bson:"namespace"`
	SortKey   int64             `bson:"sort_key"`
	ID        ids.UID           `bson:"id"`
	TaskType  ImmediateTaskType `bson:"task_type"`
	TaskInfo  ImmediateTaskInfo `bson:"task_info"`
	CreatedAt time.Time         `bson:"created_at"`
}

// ============================================================================
// Timer Task Row
// ============================================================================

type TimerTaskInfo struct {
	RunID     string `bson:"run_id"`
	Namespace string `bson:"namespace"`

	// For TimerTaskStepWaitForTimer: debug-only. Records which step triggered
	// timer creation, but the timer may fire and be used by a different step
	// due to lazy timer reuse.
	CreatedByStepExeID string `bson:"created_by_step_exe_id,omitempty"`
}

type TimerTaskRow struct {
	ShardID   int32         `bson:"shard_id"`
	RowType   RowType       `bson:"row_type"`
	Namespace string        `bson:"namespace"`
	SortKey   int64         `bson:"sort_key"`
	ID        ids.UID       `bson:"id"`
	TaskType  TimerTaskType `bson:"task_type"`
	TaskInfo  TimerTaskInfo `bson:"task_info"`
	CreatedAt time.Time     `bson:"created_at"`
}

type ImmediateTaskType int32

const (
	ImmediateTaskTypeInvalid                              = 0
	ImmediateTaskTypeRunInitialDispatch ImmediateTaskType = 1
	ImmediateTaskTypeRunResumeDispatch  ImmediateTaskType = 2
)

type TimerTaskType int32

const (
	TimerTaskTypeInvalid                        = 0
	TimerTaskTypeRunHeartbeat     TimerTaskType = 1
	TimerTaskTypeStepWaitForTimer TimerTaskType = 2
)

// RetryState captures the worker's retry progress for a step method.
// Persisted as part of ActiveStepExecution; never cleared individually — removed
// only when the entire ActiveStepExecution is deleted on step completion.
type RetryState struct {
	FirstAttemptTime    time.Time `bson:"first_attempt_time"`
	CurrentAttempts     int32     `bson:"current_attempts"`
	LastError           string    `bson:"last_error"`
	LastErrorStackTrace string    `bson:"last_error_stack_trace,omitempty"`
}
