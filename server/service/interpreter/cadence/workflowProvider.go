// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package cadence

import (
	"errors"
	"fmt"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/retry"
	"github.com/superdurable/dex/service/interpreter/interfaces"
	"go.uber.org/cadence"
	"go.uber.org/cadence/client"
	"go.uber.org/cadence/workflow"
)

type workflowProvider struct {
	threadCount        int
	pendingThreadNames map[string]int
	it                 InterpreterWorker
}

var _ interfaces.WorkflowProvider = (*workflowProvider)(nil)

func newCadenceWorkflowProvider() interfaces.WorkflowProvider {
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
	return cadence.NewCustomError(errType.String(), resp)
}

func (w *workflowProvider) NewCanceledError(reason string) error {
	if reason == "" {
		return cadence.NewCanceledError()
	}
	return cadence.NewCanceledError(reason)
}

func (w *workflowProvider) NewUpdateError(
	errType dexpb.UpdateErrorType,
	detail string,
) error {
	return cadence.NewCustomError(errType.String(), detail)
}

func (w *workflowProvider) IsApplicationError(err error) bool {
	var applicationError *cadence.CustomError
	return errors.As(err, &applicationError)
}

func (w *workflowProvider) MapToWorkerError(err error) (*dexpb.WorkerErrorResponse, error) {
	var timeoutError *workflow.TimeoutError
	if errors.As(err, &timeoutError) {
		return &dexpb.WorkerErrorResponse{
			Detail:    timeoutError.Error(),
			ErrorType: timeoutError.TimeoutType().String(),
		}, nil
	}

	var customError *cadence.CustomError
	if errors.As(err, &customError) {
		var errorResponse *dexpb.ErrorResponse
		if customError.HasDetails() {
			var detailsErr error
			errorResponse, _, detailsErr = decodeCadenceStepErrorDetails(customError)
			if detailsErr != nil {
				return nil, fmt.Errorf("decode Cadence Step failure details: %w", detailsErr)
			}
		}
		if errorResponse == nil {
			errorResponse = &dexpb.ErrorResponse{}
		}
		return cadenceWorkerError(errorResponse, customError.Error(), customError.Reason()), nil
	}

	return &dexpb.WorkerErrorResponse{Detail: err.Error(), ErrorType: err.Error()}, nil
}

func cadenceWorkerError(
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
	var continueAsNewError *workflow.ContinueAsNewError
	return errors.As(err, &continueAsNewError)
}

func (w *workflowProvider) NewInterpreterContinueAsNewError(
	ctx interfaces.UnifiedContext, input *dexpb.InterpreterWorkflowInput,
) error {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}
	return workflow.NewContinueAsNewError(wfCtx, w.it.Engine, input)
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

	wfCtx2 := workflow.WithActivityOptions(wfCtx, cadenceActivityOptions(options))

	wfCtx3 := workflow.WithLocalActivityOptions(wfCtx2, cadenceLocalActivityOptions(options))
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
		panic("cannot convert to cadence workflow context")
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
		panic("cannot convert to cadence workflow context")
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
		localCtx := workflow.WithLocalActivityOptions(wfCtx, cadenceLocalActivityOptions(options))
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
		previousAttempts, attemptMetadataFound, attemptErr := cadenceLocalActivityAttempt(err)
		if attemptErr != nil {
			return attemptErr
		}
		if !attemptMetadataFound {
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
		regularCtx := workflow.WithActivityOptions(wfCtx, cadenceActivityOptions(options))
		regularArgs = []interface{}{regularInput}
		if localActivityOnlyInput != nil {
			regularArgs = append(regularArgs, nil)
		}
		return workflow.ExecuteActivity(regularCtx, activity, regularArgs...).Get(regularCtx, valuePtr)
	default:
		return fmt.Errorf("unsupported step durability %s", durability)
	}
}

func cadenceActivityOptions(options interfaces.ActivityOptions) workflow.ActivityOptions {
	unlimited := time.Hour * 24 * 365
	startToCloseTimeout := options.StartToCloseTimeout
	if startToCloseTimeout == 0 {
		// unlimited to match Temporal for default
		startToCloseTimeout = unlimited
	}
	return workflow.ActivityOptions{
		ActivityID:             options.ActivityID,
		StartToCloseTimeout:    startToCloseTimeout,
		ScheduleToStartTimeout: time.Second * 10,
		HeartbeatTimeout:       options.HeartbeatTimeout,
		RetryPolicy:            retry.ConvertCadenceActivityRetryPolicy(options.RetryPolicy),
	}
}

func cadenceLocalActivityOptions(options interfaces.ActivityOptions) workflow.LocalActivityOptions {
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
		RetryPolicy:            retry.ConvertCadenceLocalActivityRetryPolicy(localRetryPolicy),
	}
}

func cadenceLocalActivityAttempt(err error) (int32, bool, error) {
	var customError *cadence.CustomError
	if !errors.As(err, &customError) || !customError.HasDetails() {
		return 0, false, nil
	}
	response, localFailure, detailErr := decodeCadenceStepErrorDetails(customError)
	if detailErr != nil {
		return 0, false, fmt.Errorf("decode Cadence local Step failure details: %w", detailErr)
	}
	if localFailure == nil || response.GetAttempt() <= 0 {
		return 0, false, nil
	}
	return response.GetAttempt(), true, nil
}

func decodeCadenceStepErrorDetails(
	customError *cadence.CustomError,
) (*dexpb.ErrorResponse, *dexpb.InternalLocalStepActivityFailure, error) {
	var response *dexpb.ErrorResponse
	var localFailure *dexpb.InternalLocalStepActivityFailure
	localDetailsErr := customError.Details(&response, &localFailure)
	if localDetailsErr == nil {
		if response == nil || localFailure == nil {
			return nil, nil, fmt.Errorf("Cadence local Step failure details are nil")
		}
		return response, localFailure, nil
	}
	response = nil
	regularDetailsErr := customError.Details(&response)
	if regularDetailsErr != nil {
		return nil, nil, fmt.Errorf(
			"decode local details: %v; decode regular details: %w",
			localDetailsErr,
			regularDetailsErr,
		)
	}
	if response == nil {
		return nil, nil, fmt.Errorf("Cadence Step failure details are nil")
	}
	return response, nil, nil
}

func (w *workflowProvider) ExecuteLocalActivity(
	valuePtr interface{}, ctx interfaces.UnifiedContext, activity interface{}, args ...interface{},
) error {
	wfCtx, ok := ctx.GetContext().(workflow.Context)
	if !ok {
		panic("cannot convert to cadence workflow context")
	}
	// Cadence local activities that call back into Cadence (DumpFlow query) can
	// stall decision tasks; use a regular activity so CAN resume stays healthy.
	return workflow.ExecuteActivity(wfCtx, activity, args...).Get(wfCtx, valuePtr)
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
