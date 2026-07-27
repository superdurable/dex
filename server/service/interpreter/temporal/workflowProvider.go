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

package temporal

import (
	"errors"
	"fmt"
	"time"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/retry"
	"github.com/superdurable/iwf/service/interpreter/interfaces"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type workflowProvider struct {
	interpreterWorkflow interface{}
	threadCount         int
	pendingThreadNames  map[string]int
}

var _ interfaces.WorkflowProvider = (*workflowProvider)(nil)

func newTemporalWorkflowProvider(interpreterWorkflow interface{}) interfaces.WorkflowProvider {
	if interpreterWorkflow == nil {
		panic("workflowProvider requires an interpreter workflow")
	}
	return &workflowProvider{
		interpreterWorkflow: interpreterWorkflow,
		pendingThreadNames:  map[string]int{},
	}
}

func (w *workflowProvider) NewApplicationError(errType string, details interface{}) error {
	return temporal.NewApplicationError("", errType, details)
}

func (w *workflowProvider) IsApplicationError(err error) bool {
	var applicationError *temporal.ApplicationError
	return errors.As(err, &applicationError)
}

func (w *workflowProvider) NewInterpreterContinueAsNewError(
	ctx interfaces.UnifiedContext, input *iwfpb.InterpreterWorkflowInput,
) error {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	return workflow.NewContinueAsNewError(wfCtx, w.interpreterWorkflow, input)
}

func (w *workflowProvider) UpsertSearchAttributes(
	ctx interfaces.UnifiedContext, attributes map[string]interface{},
) error {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	return workflow.UpsertSearchAttributes(wfCtx, attributes)
}

func (w *workflowProvider) NewTimer(ctx interfaces.UnifiedContext, d time.Duration) interfaces.Future {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	f := workflow.NewTimer(wfCtx, d)
	return &futureImpl{
		future: f,
	}
}

func (w *workflowProvider) GetWorkflowInfo(ctx interfaces.UnifiedContext) interfaces.WorkflowInfo {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	info := workflow.GetInfo(wfCtx)
	return interfaces.WorkflowInfo{
		WorkflowExecution: interfaces.WorkflowExecution{
			ID:    info.WorkflowExecution.ID,
			RunID: info.WorkflowExecution.RunID,
		},
		WorkflowStartTime:        info.WorkflowStartTime,
		WorkflowExecutionTimeout: info.WorkflowExecutionTimeout,
		FirstRunID:               info.FirstRunID,
		CurrentRunID:             info.WorkflowExecution.RunID,
	}
}

func (w *workflowProvider) GetSearchAttributeKeywordArray(
	ctx interfaces.UnifiedContext,
	key string,
) ([]string, error) {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	field, ok := workflow.GetInfo(wfCtx).SearchAttributes.GetIndexedFields()[key]
	if !ok {
		return nil, nil
	}
	var values []string
	if err := converter.GetDefaultDataConverter().FromPayload(field, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func (w *workflowProvider) SetQueryHandler(
	ctx interfaces.UnifiedContext, queryType string, handler interface{},
) error {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	return workflow.SetQueryHandler(wfCtx, queryType, handler)
}

func (w *workflowProvider) SetInvokeRPCUpdateHandler(
	ctx interfaces.UnifiedContext,
	validator interfaces.InvokeRPCUpdateValidator,
	handler interfaces.InvokeRPCUpdateHandler,
) error {
	return setUpdateHandler(ctx, service.ExecuteOptimisticLockingRpcUpdateType, validator, handler)
}

func (w *workflowProvider) SetWaitForStepCompletionUpdateHandler(
	ctx interfaces.UnifiedContext,
	validator interfaces.WaitForStepCompletionUpdateValidator,
	handler interfaces.WaitForStepCompletionUpdateHandler,
) error {
	return setUpdateHandler(ctx, service.WaitForStepCompletionUpdateType, validator, handler)
}

func (w *workflowProvider) SetWaitForAttributeUpdateHandler(
	ctx interfaces.UnifiedContext,
	validator interfaces.WaitForAttributeUpdateValidator,
	handler interfaces.WaitForAttributeUpdateHandler,
) error {
	return setUpdateHandler(ctx, service.WaitForAttributeUpdateType, validator, handler)
}

func (w *workflowProvider) ExtendContextWithValue(
	parent interfaces.UnifiedContext, key string, val interface{},
) interfaces.UnifiedContext {
	wfCtx, ok := parent.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	return interfaces.NewUnifiedContext(workflow.WithValue(wfCtx, key, val))
}

func (w *workflowProvider) GoNamed(
	ctx interfaces.UnifiedContext, name string, f func(ctx interfaces.UnifiedContext),
) {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	f2 := func(ctx workflow.Context) {
		ctx2 := interfaces.NewUnifiedContext(ctx)
		w.pendingThreadNames[name]++
		w.threadCount++
		f(ctx2)
		w.pendingThreadNames[name]--
		if w.pendingThreadNames[name] == 0 {
			delete(w.pendingThreadNames, name)
		}
		w.threadCount--
	}
	workflow.GoNamed(wfCtx, name, f2)
}

func (w *workflowProvider) GetPendingThreadNames() map[string]int {
	return w.pendingThreadNames
}

func (w *workflowProvider) GetThreadCount() int {
	return w.threadCount
}

func (w *workflowProvider) Await(ctx interfaces.UnifiedContext, condition func() bool) error {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	return workflow.Await(wfCtx, condition)
}

func (w *workflowProvider) WithActivityOptions(
	ctx interfaces.UnifiedContext, options interfaces.ActivityOptions,
) interfaces.UnifiedContext {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}

	// in Temporal, scheduled to close timeout is the timeout include all retries
	scheduledToCloseTimeout := time.Duration(0)
	if options.RetryPolicy.GetTotalDurationSeconds() > 0 {
		scheduledToCloseTimeout = time.Second * time.Duration(options.RetryPolicy.GetTotalDurationSeconds())
	}

	wfCtx2 := workflow.WithActivityOptions(wfCtx, workflow.ActivityOptions{
		ScheduleToCloseTimeout: scheduledToCloseTimeout,
		StartToCloseTimeout:    options.StartToCloseTimeout,
		RetryPolicy:            retry.ConvertTemporalActivityRetryPolicy(options.RetryPolicy),
		HeartbeatTimeout:       options.HeartbeatTimeout,
	})

	// Local activity optimization defaults to 7s so the workflow does not need a heartbeat.
	localActivityTimeout := 7 * time.Second
	if options.LocalActivityScheduleToCloseTimeout > 0 {
		localActivityTimeout = options.LocalActivityScheduleToCloseTimeout
	}
	wfCtx3 := workflow.WithLocalActivityOptions(wfCtx2, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: localActivityTimeout,
		RetryPolicy:            retry.ConvertTemporalActivityRetryPolicy(options.RetryPolicy),
	})
	return interfaces.NewUnifiedContext(wfCtx3)
}

type futureImpl struct {
	future workflow.Future
}

func (t *futureImpl) IsReady() bool {
	return t.future.IsReady()
}

func (t *futureImpl) Get(ctx interfaces.UnifiedContext, valuePtr interface{}) error {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}

	return t.future.Get(wfCtx, valuePtr)
}

func (w *workflowProvider) ExecuteActivity(
	valuePtr interface{}, durability iwfpb.StepDurability,
	ctx interfaces.UnifiedContext, activity interface{}, args ...interface{},
) (err error) {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	switch durability {
	case iwfpb.StepDurability_STEP_DURABILITY_SYNC:
		return workflow.ExecuteActivity(wfCtx, activity, args...).Get(wfCtx, valuePtr)
	case iwfpb.StepDurability_STEP_DURABILITY_ASYNC:
		err = workflow.ExecuteLocalActivity(wfCtx, activity, args...).Get(wfCtx, valuePtr)
		if err == nil {
			return nil
		}
		return workflow.ExecuteActivity(wfCtx, activity, args...).Get(wfCtx, valuePtr)
	default:
		return fmt.Errorf("unsupported step durability %s", durability)
	}
}

func (w *workflowProvider) ExecuteLocalActivity(
	valuePtr interface{}, ctx interfaces.UnifiedContext, activity interface{}, args ...interface{},
) error {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	return workflow.ExecuteLocalActivity(wfCtx, activity, args...).Get(wfCtx, valuePtr)
}

func (w *workflowProvider) Now(ctx interfaces.UnifiedContext) time.Time {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	return workflow.Now(wfCtx)
}

func (w *workflowProvider) Sleep(ctx interfaces.UnifiedContext, d time.Duration) (err error) {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	return workflow.Sleep(wfCtx, d)
}

func (w *workflowProvider) IsReplaying(ctx interfaces.UnifiedContext) bool {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	return workflow.IsReplaying(wfCtx)
}

func (w *workflowProvider) GetVersion(
	ctx interfaces.UnifiedContext, changeID string, minSupported, maxSupported int,
) int {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}

	version := workflow.GetVersion(wfCtx, changeID, workflow.Version(minSupported), workflow.Version(maxSupported))
	return int(version)
}

type temporalReceiveChannel struct {
	channel workflow.ReceiveChannel
}

func (t *temporalReceiveChannel) ReceiveAsync(valuePtr interface{}) (ok bool) {
	return t.channel.ReceiveAsync(valuePtr)
}

func (t *temporalReceiveChannel) ReceiveBlocking(ctx interfaces.UnifiedContext, valuePtr interface{}) (ok bool) {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}

	return t.channel.Receive(wfCtx, valuePtr)
}

func (w *workflowProvider) GetSignalChannel(
	ctx interfaces.UnifiedContext, signalName string,
) interfaces.ReceiveChannel {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	wfChan := workflow.GetSignalChannel(wfCtx, signalName)
	return &temporalReceiveChannel{
		channel: wfChan,
	}
}

func (w *workflowProvider) GetContextValue(ctx interfaces.UnifiedContext, key string) interface{} {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	return wfCtx.Value(key)
}

func (w *workflowProvider) GetLogger(ctx interfaces.UnifiedContext) interfaces.UnifiedLogger {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	return workflow.GetLogger(wfCtx)
}

func setUpdateHandler[Request any, Response any](
	ctx interfaces.UnifiedContext,
	updateType string,
	validator func(interfaces.UnifiedContext, *Request) error,
	handler func(interfaces.UnifiedContext, *Request) (*Response, error),
) error {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	temporalValidator := func(ctx workflow.Context, request *Request) error {
		return validator(interfaces.NewUnifiedContext(ctx), request)
	}
	temporalHandler := func(ctx workflow.Context, request *Request) (*Response, error) {
		return handler(interfaces.NewUnifiedContext(ctx), request)
	}
	return workflow.SetUpdateHandlerWithOptions(
		wfCtx,
		updateType,
		temporalHandler,
		workflow.UpdateHandlerOptions{Validator: temporalValidator},
	)
}
