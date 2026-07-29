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

package interfaces

import (
	"context"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

type ActivityProvider interface {
	GetLogger(ctx context.Context) UnifiedLogger
	NewFlowError(errType dexpb.FlowErrorType, errorResponse *dexpb.ErrorResponse) error
	GetActivityInfo(ctx context.Context) ActivityInfo
	RecordHeartbeat(ctx context.Context, details ...interface{})
}

type ActivityInfo struct {
	ScheduledTime     time.Time // Time of activity scheduled by a workflow
	Attempt           int32     // Attempt starts from 1, and increased by 1 for every retry if retry policy is specified.
	IsLocalActivity   bool      // Whether the activity is at local activity
	WorkflowExecution WorkflowExecution
}

type UnifiedLogger interface {
	Debug(msg string, keyvals ...interface{})
	Info(msg string, keyvals ...interface{})
	Warn(msg string, keyvals ...interface{})
	Error(msg string, keyvals ...interface{})
}

// WorkflowExecution details.
type WorkflowExecution struct {
	ID    string
	RunID string
}

// WorkflowInfo information about currently executing workflow
type WorkflowInfo struct {
	WorkflowExecution        WorkflowExecution
	WorkflowStartTime        time.Time
	WorkflowExecutionTimeout time.Duration
	FirstRunID               string
	CurrentRunID             string
}

type ActivityOptions struct {
	StartToCloseTimeout                 time.Duration
	LocalActivityScheduleToCloseTimeout time.Duration
	HeartbeatTimeout                    time.Duration
	RetryPolicy                         *dexpb.RetryPolicy
}

type UnifiedContext interface {
	GetContext() interface{}
}

type contextHolder struct {
	ctx interface{}
}

func (c *contextHolder) GetContext() interface{} {
	return c.ctx
}

func NewUnifiedContext(ctx interface{}) UnifiedContext {
	return &contextHolder{
		ctx: ctx,
	}
}

type TimerProcessor interface {
	// Callback avoids importing StepExecutionCounter (timers↔interpreter cycle).
	Dump(isStepExecutionActive func(stepExeId string) bool) []*dexpb.StaleSkipTimer
	SkipTimer(stepExeId string, timerConditionId string, timerIdx int) bool
	ReapplyStaleSkipTimer() bool
	WaitForTimerFiredOrSkipped(ctx UnifiedContext, stepExeId string, timerIdx int, cancelWaiting *bool) dexpb.InternalTimerStatus
	RemovePendingTimersOfStep(stepExeId string)
	AddTimers(stepExeId string, timerConditions []*dexpb.TimerCondition, completedTimerConditions map[int32]dexpb.InternalTimerStatus)
	GetTimerInfos() map[string][]*dexpb.TimerInfo
	GetTimerStartedUnixTimestamps() []int64
}

type WorkflowProvider interface {
	NewFlowError(errType dexpb.FlowErrorType, resp *dexpb.ErrorResponse) error
	NewUpdateError(errType dexpb.UpdateErrorType, detail string) error
	IsApplicationError(err error) bool
	GetWorkflowInfo(ctx UnifiedContext) WorkflowInfo
	GetSearchAttributeKeywordArray(ctx UnifiedContext, key string) ([]string, error)
	UpsertSearchAttributes(ctx UnifiedContext, attributes map[string]interface{}) error
	SetQueryHandler(ctx UnifiedContext, queryType string, handler interface{}) error
	ExtendContextWithValue(parent UnifiedContext, key string, val interface{}) UnifiedContext
	GoNamed(ctx UnifiedContext, name string, f func(ctx UnifiedContext))
	GetThreadCount() int
	GetPendingThreadNames() map[string]int
	Await(ctx UnifiedContext, condition func() bool) error
	WithActivityOptions(ctx UnifiedContext, options ActivityOptions) UnifiedContext
	ExecuteActivity(
		valuePtr interface{}, durability dexpb.StepDurability, ctx UnifiedContext, activity interface{},
		args ...interface{},
	) (err error)
	ExecuteLocalActivity(
		valuePtr interface{}, ctx UnifiedContext, activity interface{}, args ...interface{},
	) (err error)
	Now(ctx UnifiedContext) time.Time
	IsReplaying(ctx UnifiedContext) bool
	Sleep(ctx UnifiedContext, d time.Duration) (err error)
	NewTimer(ctx UnifiedContext, d time.Duration) Future
	GetSignalChannel(ctx UnifiedContext, signalName string) (receiveChannel ReceiveChannel)
	GetContextValue(ctx UnifiedContext, key string) interface{}
	GetVersion(ctx UnifiedContext, changeID string, minSupported, maxSupported int) int
	GetLogger(ctx UnifiedContext) UnifiedLogger
	NewInterpreterContinueAsNewError(ctx UnifiedContext, input *dexpb.InterpreterWorkflowInput) error
	SetInvokeRPCUpdateHandler(ctx UnifiedContext, validator InvokeRPCUpdateValidator, handler InvokeRPCUpdateHandler) error
	SetWaitForStepCompletionUpdateHandler(ctx UnifiedContext, validator WaitForStepCompletionUpdateValidator, handler WaitForStepCompletionUpdateHandler) error
	SetWaitForAttributeUpdateHandler(ctx UnifiedContext, validator WaitForAttributeUpdateValidator, handler WaitForAttributeUpdateHandler) error
}

type (
	InvokeRPCUpdateValidator func(ctx UnifiedContext, req *dexpb.InvokeRPCRequest) error
	InvokeRPCUpdateHandler   func(ctx UnifiedContext, req *dexpb.InvokeRPCRequest) (*dexpb.InvokeRpcUpdateResult, error)

	WaitForStepCompletionUpdateValidator func(ctx UnifiedContext, req *dexpb.WaitForStepCompletionRequest) error
	WaitForStepCompletionUpdateHandler   func(ctx UnifiedContext, req *dexpb.WaitForStepCompletionRequest) (*dexpb.WaitForStepCompletionResponse, error)

	WaitForAttributeUpdateValidator func(ctx UnifiedContext, req *dexpb.WaitForAttributeRequest) error
	WaitForAttributeUpdateHandler   func(ctx UnifiedContext, req *dexpb.WaitForAttributeRequest) (*emptypb.Empty, error)
)

type ReceiveChannel interface {
	ReceiveAsync(valuePtr interface{}) (ok bool)
	ReceiveBlocking(ctx UnifiedContext, valuePtr interface{}) (ok bool)
}

type Future interface {
	Get(ctx UnifiedContext, valuePtr interface{}) error
	IsReady() bool
}
