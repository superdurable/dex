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

package cadence

import (
	"errors"
	"fmt"
	"time"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service/common/retry"
	"github.com/superdurable/iwf/service/interpreter/interfaces"
	"go.uber.org/cadence"
	"go.uber.org/cadence/client"
	"go.uber.org/cadence/workflow"
)

type workflowProvider struct {
	interpreterWorkflow interface{}
	threadCount         int
	pendingThreadNames  map[string]int
}

var _ interfaces.WorkflowProvider = (*workflowProvider)(nil)

func newCadenceWorkflowProvider(interpreterWorkflow interface{}) interfaces.WorkflowProvider {
	if interpreterWorkflow == nil {
		panic("workflowProvider requires an interpreter workflow")
	}
	return &workflowProvider{
		interpreterWorkflow: interpreterWorkflow,
		pendingThreadNames:  map[string]int{},
	}
}

func (w *workflowProvider) NewApplicationError(errType string, details interface{}) error {
	return cadence.NewCustomError(errType, details)
}

func (w *workflowProvider) IsApplicationError(err error) bool {
	var applicationError *cadence.CustomError
	return errors.As(err, &applicationError)
}

func (w *workflowProvider) NewInterpreterContinueAsNewError(
	ctx interfaces.UnifiedContext, input *iwfpb.InterpreterWorkflowInput,
) error {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}
	return workflow.NewContinueAsNewError(wfCtx, w.interpreterWorkflow, input)
}

func (w *workflowProvider) UpsertSearchAttributes(
	ctx interfaces.UnifiedContext, attributes map[string]interface{},
) error {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}
	return workflow.UpsertSearchAttributes(wfCtx, attributes)
}

func (w *workflowProvider) NewTimer(ctx interfaces.UnifiedContext, d time.Duration) interfaces.Future {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}
	f := workflow.NewTimer(wfCtx, d)
	return &futureImpl{
		future: f,
	}
}

func (w *workflowProvider) GetWorkflowInfo(ctx interfaces.UnifiedContext) interfaces.WorkflowInfo {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}
	info := workflow.GetInfo(wfCtx)
	return interfaces.WorkflowInfo{
		WorkflowExecution: interfaces.WorkflowExecution{
			ID:    info.WorkflowExecution.ID,
			RunID: info.WorkflowExecution.RunID,
		},
		WorkflowStartTime:        time.UnixMilli(0), // TODO need support from Cadence client: https://github.com/uber-go/cadence-client/issues/1204
		WorkflowExecutionTimeout: time.Duration(info.ExecutionStartToCloseTimeoutSeconds) * time.Second,
		FirstRunID:               info.WorkflowExecution.RunID, // Cadence does not provide FirstRunID TODO https://github.com/uber-go/cadence-client/issues/1371 use firstRunID when available
		CurrentRunID:             info.WorkflowExecution.RunID,
	}
}

func (w *workflowProvider) GetSearchAttributeKeywordArray(
	ctx interfaces.UnifiedContext,
	key string,
) ([]string, error) {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}
	field, ok := workflow.GetInfo(wfCtx).SearchAttributes.GetIndexedFields()[key]
	if !ok {
		return nil, nil
	}
	var values []string
	if err := client.NewValue(field).Get(&values); err != nil {
		return nil, err
	}
	return values, nil
}

func (w *workflowProvider) SetQueryHandler(
	ctx interfaces.UnifiedContext, queryType string, handler interface{},
) error {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}
	return workflow.SetQueryHandler(wfCtx, queryType, handler)
}

func (w *workflowProvider) SetInvokeRPCUpdateHandler(
	interfaces.UnifiedContext,
	interfaces.InvokeRPCUpdateValidator,
	interfaces.InvokeRPCUpdateHandler,
) error {
	return nil
}

func (w *workflowProvider) SetWaitForStepCompletionUpdateHandler(
	interfaces.UnifiedContext,
	interfaces.WaitForStepCompletionUpdateValidator,
	interfaces.WaitForStepCompletionUpdateHandler,
) error {
	return nil
}

func (w *workflowProvider) SetWaitForAttributeUpdateHandler(
	interfaces.UnifiedContext,
	interfaces.WaitForAttributeUpdateValidator,
	interfaces.WaitForAttributeUpdateHandler,
) error {
	return nil
}

func (w *workflowProvider) ExtendContextWithValue(
	parent interfaces.UnifiedContext, key string, val interface{},
) interfaces.UnifiedContext {
	wfCtx, ok := parent.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}
	return interfaces.NewUnifiedContext(workflow.WithValue(wfCtx, key, val))
}

func (w *workflowProvider) GoNamed(
	ctx interfaces.UnifiedContext, name string, f func(ctx interfaces.UnifiedContext),
) {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
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
		panic("cannot convert to cadence workflow context")
	}
	return workflow.Await(wfCtx, condition)
}

func (w *workflowProvider) WithActivityOptions(
	ctx interfaces.UnifiedContext, options interfaces.ActivityOptions,
) interfaces.UnifiedContext {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}

	unlimited := time.Hour * 24 * 365
	startToCloseTimeout := options.StartToCloseTimeout
	if startToCloseTimeout == 0 {
		// unlimited to match Temporal for default
		startToCloseTimeout = unlimited
	}

	wfCtx2 := workflow.WithActivityOptions(wfCtx, workflow.ActivityOptions{
		StartToCloseTimeout:    startToCloseTimeout,
		ScheduleToStartTimeout: time.Second * 10,
		HeartbeatTimeout:       options.HeartbeatTimeout,
		RetryPolicy:            retry.ConvertCadenceActivityRetryPolicy(options.RetryPolicy),
	})

	// Local activity optimization defaults to 7s so the workflow does not need a heartbeat.
	localActivityTimeout := 7 * time.Second
	if options.LocalActivityScheduleToCloseTimeout > 0 {
		localActivityTimeout = options.LocalActivityScheduleToCloseTimeout
	}
	wfCtx3 := workflow.WithLocalActivityOptions(wfCtx2, workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: localActivityTimeout,
		RetryPolicy:            retry.ConvertCadenceActivityRetryPolicy(options.RetryPolicy),
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
		panic("cannot convert to cadence workflow context")
	}

	return t.future.Get(wfCtx, valuePtr)
}

func (w *workflowProvider) ExecuteActivity(
	valuePtr interface{}, durability iwfpb.StepDurability,
	ctx interfaces.UnifiedContext, activity interface{}, args ...interface{},
) (err error) {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
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
		panic("cannot convert to cadence workflow context")
	}
	return workflow.ExecuteLocalActivity(wfCtx, activity, args...).Get(wfCtx, valuePtr)
}

func (w *workflowProvider) Now(ctx interfaces.UnifiedContext) time.Time {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}
	return workflow.Now(wfCtx)
}

func (w *workflowProvider) IsReplaying(ctx interfaces.UnifiedContext) bool {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}
	return workflow.IsReplaying(wfCtx)
}

func (w *workflowProvider) Sleep(ctx interfaces.UnifiedContext, d time.Duration) (err error) {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}
	return workflow.Sleep(wfCtx, d)
}

func (w *workflowProvider) GetVersion(
	ctx interfaces.UnifiedContext, changeID string, minSupported, maxSupported int,
) int {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}

	version := workflow.GetVersion(wfCtx, changeID, workflow.Version(minSupported), workflow.Version(maxSupported))
	return int(version)
}

type cadenceReceiveChannel struct {
	channel workflow.Channel
}

func (t *cadenceReceiveChannel) ReceiveAsync(valuePtr interface{}) (ok bool) {
	return t.channel.ReceiveAsync(valuePtr)
}

func (t *cadenceReceiveChannel) ReceiveBlocking(ctx interfaces.UnifiedContext, valuePtr interface{}) (ok bool) {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}

	return t.channel.Receive(wfCtx, valuePtr)
}

func (w *workflowProvider) GetSignalChannel(
	ctx interfaces.UnifiedContext, signalName string,
) interfaces.ReceiveChannel {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}
	wfChan := workflow.GetSignalChannel(wfCtx, signalName)
	return &cadenceReceiveChannel{
		channel: wfChan,
	}
}

func (w *workflowProvider) GetContextValue(ctx interfaces.UnifiedContext, key string) interface{} {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}
	return wfCtx.Value(key)
}

func (w *workflowProvider) GetLogger(ctx interfaces.UnifiedContext) interfaces.UnifiedLogger {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}

	zLogger := workflow.GetLogger(wfCtx)
	return &loggerImpl{
		zlogger: zLogger,
	}
}
