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

import "time"

type RetryPolicy struct {
	InitialInterval    time.Duration
	BackoffCoefficient float64
	MaximumInterval    time.Duration
	MaximumAttempts    int32
	TotalDuration      time.Duration
}

type StepDurability uint8

const (
	StepDurabilityDefault StepDurability = iota
	StepDurabilitySync
	StepDurabilityAsync
)

type WaitForFailurePolicy uint8

const (
	FailFlowOnWaitForFailure WaitForFailurePolicy = iota
	ProceedOnWaitForFailure
)

type ExecuteFailure struct {
	step    StepDef
	options *StepOptions
}

func ProceedToOnExecuteFailure[IN any](
	step Step[IN],
	options *StepOptions,
) *ExecuteFailure {
	return &ExecuteFailure{
		step:    typedStepDef[IN]{step: step},
		options: options,
	}
}

type StepOptions struct {
	WaitForTimeout        time.Duration
	ExecuteTimeout        time.Duration
	WaitForRetry          *RetryPolicy
	ExecuteRetry          *RetryPolicy
	WaitForFailure        WaitForFailurePolicy
	ExecuteFailure        *ExecuteFailure
	WaitForDurability     StepDurability
	ExecuteDurability     StepDurability
	WaitForLockAttributes []AttributeLock
	ExecuteLockAttributes []AttributeLock
}

type WorkerTarget struct {
	Address  string
	Headless bool
}

type ActiveStepSearchMode uint8

const (
	SearchActiveStepsDefault ActiveStepSearchMode = iota
	SearchAllActiveSteps
	SearchActiveStepsWithWaitFor
	DisableActiveStepSearch
)

type FlowConfig struct {
	ActiveStepSearchMode       *ActiveStepSearchMode
	ContinueAsNewThreshold     *int32
	ContinueAsNewPageSizeBytes *int32
	StepDurability             *StepDurability
	WorkerTarget               *WorkerTarget
}

type StartFlowOptions struct {
	Timeout        *time.Duration
	IDReusePolicy  IDReusePolicy
	CronSchedule   string
	StartDelay     *time.Duration
	RetryPolicy    *FlowRetryPolicy
	Attributes     []InitialAttributeDef
	ConfigOverride *FlowConfig
	AlreadyStarted *AlreadyStartedOptions
	RequestID      *string
}

type IDReusePolicy uint8

const (
	IDReuseDefault IDReusePolicy = iota
	IDReuseAllowIfPreviousFailed
	IDReuseAllowIfNotRunning
	IDReuseDisallow
	IDReuseTerminateIfRunning
)

type FlowRetryPolicy struct {
	InitialInterval    time.Duration
	BackoffCoefficient float64
	MaximumInterval    time.Duration
	MaximumAttempts    int32
}

type AlreadyStartedOptions struct {
	IgnoreError bool
}

type InvokeOptions struct {
	Timeout        time.Duration
	LockAttributes []AttributeLock
}

type WaitOptions struct {
	Timeout time.Duration
}

type WaitForFlowOptions struct {
	NeedsResults bool
	Timeout      time.Duration
}

type StopType uint8

const (
	CancelFlow StopType = iota + 1
	TerminateFlow
	FailFlow
)

type StopOptions struct {
	Type   StopType
	Reason string
}

type StepExecutionID struct {
	StepType        string
	ExecutionNumber *int32
}

type TimerID struct {
	ConditionID string
	Index       *int32
}

type ResetType uint8

const (
	ResetByHistoryEventID ResetType = iota + 1
	ResetToBeginning
	ResetByHistoryEventTime
	ResetByStepType
	ResetByStepExecutionID
)

type ResetOptions struct {
	Type                       ResetType
	HistoryEventID             int32
	Reason                     string
	HistoryEventTime           time.Time
	StepType                   string
	StepExecutionID            string
	SkipChannelMessagesReapply bool
	SkipLockingRPCReapply      bool
}
