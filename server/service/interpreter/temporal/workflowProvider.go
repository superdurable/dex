// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package temporal

import (
	"errors"
	"fmt"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/retry"
	"github.com/superdurable/dex/service/interpreter/interfaces"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type workflowProvider struct {
	threadCount        int
	pendingThreadNames map[string]int
	it                 InterpreterWorker
}

var _ interfaces.WorkflowProvider = (*workflowProvider)(nil)

func newTemporalWorkflowProvider() interfaces.WorkflowProvider {
	return &workflowProvider{
		pendingThreadNames: map[string]int{},
	}
}

func (w *workflowProvider) NewFlowError(
	errType dexpb.FlowErrorType,
	resp *dexpb.ErrorResponse,
) error {
	if resp == nil {
		panic("resp required")
	}
	return temporal.NewApplicationError("", errType.String(), resp)
}

func (w *workflowProvider) NewCanceledError(reason string) error {
	if reason == "" {
		return temporal.NewCanceledError()
	}
	return temporal.NewCanceledError(reason)
}

func (w *workflowProvider) NewUpdateError(
	errType dexpb.UpdateErrorType,
	detail string,
) error {
	return temporal.NewApplicationError("", errType.String(), detail)
}

func (w *workflowProvider) IsApplicationError(err error) bool {
	var applicationError *temporal.ApplicationError
	return errors.As(err, &applicationError)
}

func (w *workflowProvider) MapToWorkerError(err error) (*dexpb.WorkerErrorResponse, error) {
	var timeoutError *temporal.TimeoutError
	if errors.As(err, &timeoutError) {
		return &dexpb.WorkerErrorResponse{
			Detail:    timeoutError.Message(),
			ErrorType: timeoutError.TimeoutType().String(),
		}, nil
	}

	var applicationError *temporal.ApplicationError
	if errors.As(err, &applicationError) {
		errorResponse := &dexpb.ErrorResponse{}
		if applicationError.HasDetails() {
			if detailsErr := applicationError.Details(errorResponse); detailsErr != nil {
				return nil, fmt.Errorf("decode Temporal Step failure details: %w", detailsErr)
			}
		}
		return temporalWorkerError(errorResponse, applicationError.Message(), applicationError.Type()), nil
	}

	return &dexpb.WorkerErrorResponse{Detail: err.Error(), ErrorType: err.Error()}, nil
}

func temporalWorkerError(
	errorResponse *dexpb.ErrorResponse,
	backendDetail string,
	backendType string,
) *dexpb.WorkerErrorResponse {
	if errorResponse.GetOriginalWorkerErrorStatus() != 0 ||
		errorResponse.GetOriginalWorkerErrorDetail() != "" ||
		errorResponse.GetOriginalWorkerErrorType() != "" ||
		errorResponse.GetOriginalWorkerErrorStackTrace() != "" ||
		errorResponse.GetOriginalWorkerRetryAfterSeconds() != 0 {
		detail := errorResponse.GetOriginalWorkerErrorDetail()
		if detail == "" {
			detail = backendDetail
		}
		errorType := errorResponse.GetOriginalWorkerErrorType()
		if errorType == "" {
			errorType = backendType
		}
		return &dexpb.WorkerErrorResponse{
			Detail:            detail,
			ErrorType:         errorType,
			StackTrace:        errorResponse.GetOriginalWorkerErrorStackTrace(),
			RetryAfterSeconds: errorResponse.GetOriginalWorkerRetryAfterSeconds(),
		}
	}
	detail := errorResponse.GetDetail()
	if detail == "" {
		detail = backendDetail
	}
	return &dexpb.WorkerErrorResponse{Detail: detail, ErrorType: backendType}
}

func (w *workflowProvider) IsContinueAsNewError(err error) bool {
	return workflow.IsContinueAsNewError(err)
}

func (w *workflowProvider) NewInterpreterContinueAsNewError(
	ctx interfaces.UnifiedContext, input *dexpb.InterpreterWorkflowInput,
) error {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	return workflow.NewContinueAsNewError(wfCtx, w.it.Engine, input)
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

	wfCtx2 := workflow.WithActivityOptions(wfCtx, temporalActivityOptions(options))

	wfCtx3 := workflow.WithLocalActivityOptions(wfCtx2, temporalLocalActivityOptions(options))
	return interfaces.NewUnifiedActivityContext(wfCtx3, options)
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
	valuePtr interface{},
	durability dexpb.StepDurability,
	ctx interfaces.UnifiedContext,
	activity interface{},
	regularInput interface{},
	localActivityOnlyInput interface{},
) (err error) {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to temporal workflow context")
	}
	regularArgs := []interface{}{regularInput}
	localArgs := []interface{}{regularInput}
	if localActivityOnlyInput != nil {
		regularArgs = append(regularArgs, nil)
		localArgs = append(localArgs, localActivityOnlyInput)
	}
	switch durability {
	case dexpb.StepDurability_STEP_DURABILITY_SYNC:
		return workflow.ExecuteActivity(wfCtx, activity, regularArgs...).Get(wfCtx, valuePtr)
	case dexpb.StepDurability_STEP_DURABILITY_ASYNC:
		options, optionsFound := interfaces.ActivityOptionsFromContext(ctx)
		localCtx := workflow.WithLocalActivityOptions(wfCtx, temporalLocalActivityOptions(options))
		firstAttemptTime := workflow.Now(wfCtx)
		retryContext, isStepMethodActivity := interfaces.InitializeStepActivityRetryContext(
			regularInput,
			options,
			firstAttemptTime,
		)
		err = workflow.ExecuteLocalActivity(localCtx, activity, localArgs...).Get(localCtx, valuePtr)
		if err == nil {
			return nil
		}
		if !isStepMethodActivity {
			return workflow.ExecuteActivity(wfCtx, activity, regularArgs...).Get(wfCtx, valuePtr)
		}
		if !optionsFound {
			return err
		}
		previousAttempts, ok := temporalLocalActivityAttempt(err)
		if !ok {
			return err
		}
		remainingPolicy, canFallback := retry.RemainingActivityRetryPolicy(
			options.RetryPolicy,
			previousAttempts,
			workflow.Now(wfCtx).Sub(firstAttemptTime),
		)
		if !canFallback {
			return err
		}
		regularInput = interfaces.StepActivityInputForFallback(regularInput, retryContext, previousAttempts)
		options.RetryPolicy = remainingPolicy
		regularCtx := workflow.WithActivityOptions(wfCtx, temporalActivityOptions(options))
		regularArgs = []interface{}{regularInput}
		if localActivityOnlyInput != nil {
			regularArgs = append(regularArgs, nil)
		}
		return workflow.ExecuteActivity(regularCtx, activity, regularArgs...).Get(regularCtx, valuePtr)
	default:
		return fmt.Errorf("unsupported step durability %s", durability)
	}
}

func temporalActivityOptions(options interfaces.ActivityOptions) workflow.ActivityOptions {
	// in Temporal, scheduled to close timeout is the timeout include all retries
	scheduleToCloseTimeout := time.Duration(0)
	if options.RetryPolicy.GetTotalDurationSeconds() > 0 {
		scheduleToCloseTimeout = time.Second * time.Duration(options.RetryPolicy.GetTotalDurationSeconds())
	}
	return workflow.ActivityOptions{
		ActivityID:             options.ActivityID,
		ScheduleToCloseTimeout: scheduleToCloseTimeout,
		StartToCloseTimeout:    options.StartToCloseTimeout,
		RetryPolicy:            retry.ConvertTemporalActivityRetryPolicy(options.RetryPolicy),
		HeartbeatTimeout:       options.HeartbeatTimeout,
	}
}

func temporalLocalActivityOptions(options interfaces.ActivityOptions) workflow.LocalActivityOptions {
	// Local activity optimization defaults to 7s so the workflow does not need a heartbeat.
	localActivityTimeout := 7 * time.Second
	if options.LocalActivityScheduleToCloseTimeout > 0 {
		localActivityTimeout = options.LocalActivityScheduleToCloseTimeout
	}
	if totalDuration := options.RetryPolicy.GetTotalDurationSeconds(); totalDuration > 0 && time.Duration(totalDuration)*time.Second < localActivityTimeout {
		localActivityTimeout = time.Duration(totalDuration) * time.Second
	}
	localRetryPolicy := retry.LocalActivityRetryPolicy(options.RetryPolicy, localActivityTimeout)
	return workflow.LocalActivityOptions{
		ScheduleToCloseTimeout: localActivityTimeout,
		RetryPolicy:            retry.ConvertTemporalActivityRetryPolicy(localRetryPolicy),
	}
}

func temporalLocalActivityAttempt(err error) (int32, bool) {
	var applicationError *temporal.ApplicationError
	if !errors.As(err, &applicationError) || !applicationError.HasDetails() {
		return 0, false
	}
	var response dexpb.ErrorResponse
	if detailErr := applicationError.Details(&response); detailErr != nil || response.GetAttempt() <= 0 {
		return 0, false
	}
	return response.GetAttempt(), true
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
