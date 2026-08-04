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
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	uclient "github.com/superdurable/dex/service/client"
	historybuilder "github.com/superdurable/dex/service/client/history"
	"github.com/superdurable/dex/service/common/index"
	"github.com/superdurable/dex/service/common/ptr"
	"github.com/superdurable/dex/service/common/utils"
	"github.com/superdurable/dex/service/interpreter/temporal"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	history "go.temporal.io/api/history/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	realtemporal "go.temporal.io/sdk/temporal"
)

type temporalClient struct {
	tClient                        client.Client
	namespace                      string
	dataConverter                  converter.DataConverter
	memoEncryption                 bool // this is a workaround for https://github.com/temporalio/sdk-go/issues/1045
	queryWorkflowFailedRetryPolicy config.QueryWorkflowFailedRetryPolicy
	it                             *temporal.InterpreterWorker
}

type localActivityMarkerData struct {
	ActivityType string
	Attempt      int32
}

func NewTemporalClient(
	tClient client.Client, namespace string, dataConverter converter.DataConverter, memoEncryption bool, retryPolicy *config.QueryWorkflowFailedRetryPolicy,
) uclient.UnifiedClient {
	return &temporalClient{
		tClient:                        tClient,
		namespace:                      namespace,
		dataConverter:                  dataConverter,
		memoEncryption:                 memoEncryption,
		queryWorkflowFailedRetryPolicy: config.QueryWorkflowFailedRetryPolicyWithDefaults(retryPolicy),
	}
}

func (t *temporalClient) Close() {
	t.tClient.Close()
}

func (t *temporalClient) IsWorkflowAlreadyStartedError(err error) bool {
	if err.Error() == "schedule with this ID is already registered" {
		// there is no type to check, just a string
		// https://github.com/temporalio/sdk-go/blob/d10e87118a07b44fd09bf88d39a628f0e6e70c34/internal/error.go#L336
		return true
	}
	return realtemporal.IsWorkflowExecutionAlreadyStartedError(err)
}

func (t *temporalClient) GetRunIdFromWorkflowAlreadyStartedError(err error) (string, bool) {
	var workflowExecutionAlreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
	ok := errors.As(err, &workflowExecutionAlreadyStarted)

	runId := ""
	if ok {
		runId = workflowExecutionAlreadyStarted.RunId
	}

	return runId, ok
}

func (t *temporalClient) IsNotFoundError(err error) bool {
	var notFound *serviceerror.NotFound
	ok := errors.As(err, &notFound)
	return ok
}

func (t *temporalClient) isQueryFailedError(err error) bool {
	var serviceError *serviceerror.QueryFailed
	ok := errors.As(err, &serviceError)
	return ok
}

func (t *temporalClient) IsRequestTimeoutError(err error) bool {
	var deadlineExceeded *serviceerror.DeadlineExceeded
	ok := errors.As(err, &deadlineExceeded)
	if ok {
		return ok
	}
	var canceled *serviceerror.Canceled
	ok = errors.As(err, &canceled)
	return ok
}

func (t *temporalClient) IsWorkflowTimeoutError(err error) bool {
	return realtemporal.IsTimeoutError(err)
}

func (t *temporalClient) GetIfUpdateError(err error, detail *string) (dexpb.UpdateErrorType, bool) {
	typeName := t.extractAppErrType(err)
	value, ok := dexpb.UpdateErrorType_value[typeName]
	if !ok {
		return 0, false
	}
	var decoded string
	if decodeErr := t.decodeAppErrDetails(err, &decoded); decodeErr == nil && detail != nil {
		*detail = decoded
	}
	return dexpb.UpdateErrorType(value), true
}

func (t *temporalClient) GetIfFlowError(err error, resp *dexpb.ErrorResponse) (dexpb.FlowErrorType, bool) {
	typeName := t.extractAppErrType(err)
	value, ok := dexpb.FlowErrorType_value[typeName]
	if !ok {
		return 0, false
	}
	if resp == nil {
		panic("resp required")
	}
	if decodeErr := t.decodeAppErrDetails(err, resp); decodeErr != nil {
		if resp.Detail == "" {
			resp.Detail = err.Error()
		}
	}
	return dexpb.FlowErrorType(value), true
}

func (t *temporalClient) extractAppErrType(err error) string {
	var applicationError *realtemporal.ApplicationError
	if errors.As(err, &applicationError) {
		return applicationError.Type()
	}
	return ""
}

func (t *temporalClient) decodeAppErrDetails(err error, detailsPtr interface{}) error {
	var applicationError *realtemporal.ApplicationError
	if !errors.As(err, &applicationError) {
		return fmt.Errorf("not an application error")
	}
	if !applicationError.HasDetails() {
		return fmt.Errorf("application error doesn't have details")
	}
	return applicationError.Details(detailsPtr)
}

func (t *temporalClient) StartInterpreterWorkflow(
	ctx context.Context, options uclient.StartWorkflowOptions, args ...interface{},
) (runId string, err error) {
	memo, err := t.encryptMemoIfNeeded(options.Memo)
	if err != nil {
		return "", err
	}

	workflowOptions := client.StartWorkflowOptions{
		ID:                                       options.ID,
		TaskQueue:                                options.TaskQueue,
		WorkflowExecutionTimeout:                 options.WorkflowExecutionTimeout,
		SearchAttributes:                         options.SearchAttributes,
		Memo:                                     memo,
		WorkflowExecutionErrorWhenAlreadyStarted: true,
	}

	if options.IdReusePolicy != nil {
		workflowIdReusePolicy, err := mapToTemporalWorkflowIdReusePolicy(*options.IdReusePolicy)
		if err != nil {
			return "", err
		}

		workflowOptions.WorkflowIDReusePolicy = *workflowIdReusePolicy
	}

	if options.RetryPolicy != nil {
		workflowOptions.RetryPolicy = mapToTemporalRetryPolicy(options.RetryPolicy)
	}

	if options.CronSchedule != nil && *options.CronSchedule != "" {
		// use temporal schedule instead of cron
		// https://temporal.io/blog/how-do-i-convert-my-cron-into-a-schedule
		// workflowOptions.CronSchedule = *options.CronSchedule

		_, err := t.tClient.ScheduleClient().Create(ctx, client.ScheduleOptions{
			ID: "schedule for workflow: " + options.ID,
			Spec: client.ScheduleSpec{
				CronExpressions: []string{*options.CronSchedule},
			},
			Action: &client.ScheduleWorkflowAction{
				ID:                       workflowOptions.ID,
				TaskQueue:                workflowOptions.TaskQueue,
				Workflow:                 t.it.Engine,
				Args:                     args,
				WorkflowExecutionTimeout: workflowOptions.WorkflowExecutionTimeout,
				RetryPolicy:              workflowOptions.RetryPolicy,
				Memo:                     workflowOptions.Memo,
				TypedSearchAttributes:    workflowOptions.TypedSearchAttributes,
			},
		})

		return "", err
	}

	if options.WorkflowStartDelay != nil {
		workflowOptions.StartDelay = *options.WorkflowStartDelay
	}

	run, err := t.tClient.ExecuteWorkflow(ctx, workflowOptions, t.it.Engine, args...)
	if err != nil {
		return "", err
	}
	return run.GetRunID(), nil
}

func (t *temporalClient) StartBlobStoreCleanupWorkflow(
	ctx context.Context, taskQueue, workflowID, cronSchedule, storeId string,
) error {

	if cronSchedule == "" {
		_, err := t.tClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: taskQueue,
		}, t.it.BlobStoreCleanup, storeId)
		return err
	}

	_, err := t.tClient.ScheduleClient().Create(ctx, client.ScheduleOptions{
		ID: workflowID,
		Spec: client.ScheduleSpec{
			CronExpressions: []string{cronSchedule},
		},
		Action: &client.ScheduleWorkflowAction{
			ID:                       workflowID,
			TaskQueue:                taskQueue,
			Workflow:                 t.it.BlobStoreCleanup,
			Args:                     []interface{}{storeId},
			WorkflowExecutionTimeout: 0,
		},
	})
	return err
}

func (t *temporalClient) SignalWorkflow(
	ctx context.Context, workflowID string, runID string, signalName string, arg interface{},
) error {
	return t.tClient.SignalWorkflow(ctx, workflowID, runID, signalName, arg)
}

func (t *temporalClient) CancelWorkflow(ctx context.Context, workflowID string, runID string) error {
	return t.tClient.CancelWorkflow(ctx, workflowID, runID)
}

func (t *temporalClient) TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string) error {
	var reasonStr string
	if reason == "" {
		reasonStr = "Force termiantion from user"
	} else {
		reasonStr = reason
	}

	return t.tClient.TerminateWorkflow(ctx, workflowID, runID, reasonStr)
}

func (t *temporalClient) ListWorkflow(
	ctx context.Context, request *uclient.ListWorkflowExecutionsRequest,
) (*uclient.ListWorkflowExecutionsResponse, error) {
	listReq := &workflowservice.ListWorkflowExecutionsRequest{
		PageSize:      request.PageSize,
		Query:         request.Query,
		NextPageToken: request.NextPageToken,
	}
	resp, err := t.tClient.ListWorkflow(ctx, listReq)
	if err != nil {
		return nil, err
	}
	var executions []*dexpb.SearchFlowsResponseEntry
	for _, exe := range resp.GetExecutions() {
		searchAttributes, err := index.MapTemporalSearchAttributeFieldsToKVs(exe.GetSearchAttributes())
		if err != nil {
			return nil, err
		}
		status, err := mapToDexWorkflowStatus(exe.GetStatus())
		if err != nil {
			return nil, err
		}
		executions = append(executions, &dexpb.SearchFlowsResponseEntry{
			FlowId:           exe.Execution.WorkflowId,
			RunId:            exe.Execution.RunId,
			SearchAttributes: searchAttributes,
			FlowType:         stringSearchAttribute(searchAttributes, service.SearchAttributeDexWorkflowType),
			FlowStatus:       status,
			StartTime:        exe.GetStartTime(),
			CloseTime:        exe.GetCloseTime(),
		})
	}
	return &uclient.ListWorkflowExecutionsResponse{
		Executions:    executions,
		NextPageToken: resp.NextPageToken,
	}, nil
}

func (t *temporalClient) QueryWorkflow(
	ctx context.Context, valuePtr interface{}, workflowID string, runID string, queryType string, args ...interface{},
) error {
	var qres converter.EncodedValue
	var err error

	attempt := 1
	// Only QueryFailed error causes retry; all other errors make the loop to finish immediately
	for attempt <= t.queryWorkflowFailedRetryPolicy.MaximumAttempts {
		qres, err = t.tClient.QueryWorkflow(ctx, workflowID, runID, queryType, args...)
		if err == nil {
			break
		} else {
			if t.isQueryFailedError(err) {
				time.Sleep(time.Duration(t.queryWorkflowFailedRetryPolicy.InitialIntervalSeconds) * time.Second)
				attempt++
				continue
			}
			return err
		}
	}
	if err != nil {
		return err
	}
	return qres.Get(valuePtr)
}

func (t *temporalClient) DescribeWorkflowExecution(
	ctx context.Context, workflowID, runID string, indexedAttrTypes map[string]dexpb.IndexType,
) (*uclient.DescribeWorkflowExecutionResponse, error) {
	resp, err := t.tClient.DescribeWorkflowExecution(ctx, workflowID, runID)
	if err != nil {
		return nil, err
	}
	status, err := mapToDexWorkflowStatus(resp.GetWorkflowExecutionInfo().GetStatus())
	if err != nil {
		return nil, err
	}

	indexedAttributes, err := index.MapTemporalSearchAttributeFieldsToAttrValues(resp.GetWorkflowExecutionInfo().GetSearchAttributes(), indexedAttrTypes)
	if err != nil {
		return nil, err
	}

	memo, err := t.getMemoAndDecryptIfNeeded(resp.GetWorkflowExecutionInfo().GetMemo())
	if err != nil {
		return nil, err
	}
	info := resp.GetWorkflowExecutionInfo()
	startTime := info.GetStartTime().AsTime()
	var closeTime *time.Time
	if info.GetCloseTime() != nil {
		closeTime = ptr.Any(info.GetCloseTime().AsTime())
	}

	return &uclient.DescribeWorkflowExecutionResponse{
		RunId:                info.GetExecution().GetRunId(),
		FirstRunId:           info.GetFirstRunId(),
		Status:               status,
		IndexedAttributes:    indexedAttributes,
		Memos:                memo,
		FlowStartedTimestamp: utils.ToNanoSeconds(info.GetStartTime()),
		StartTime:            startTime,
		CloseTime:            closeTime,
	}, nil
}

func (t *temporalClient) GetWorkflowHistory(
	ctx context.Context,
	request *uclient.GetWorkflowHistoryRequest,
) (*uclient.WorkflowHistory, error) {
	response, err := t.getTemporalHistoryPage(ctx, request)
	if err != nil {
		return nil, err
	}
	rawEvents := response.GetHistory().GetEvents()
	startInternalEventID := request.StartInternalEventID
	if startInternalEventID == 0 {
		startInternalEventID = 1
		if len(rawEvents) > 0 {
			startInternalEventID = rawEvents[0].GetEventId()
		}
	}
	nextInternalEventID := startInternalEventID
	if len(rawEvents) > 0 {
		nextInternalEventID = rawEvents[len(rawEvents)-1].GetEventId() + 1
	}
	events, err := t.buildTemporalHistoryEvents(
		ctx,
		request.WorkflowID,
		request.RunID,
		startInternalEventID,
		nextInternalEventID,
	)
	if err != nil {
		return nil, err
	}
	return &uclient.WorkflowHistory{
		Events:              events,
		NextPageToken:       response.GetNextPageToken(),
		NextInternalEventID: nextInternalEventID,
	}, nil
}

func (t *temporalClient) getTemporalHistoryPage(
	ctx context.Context,
	request *uclient.GetWorkflowHistoryRequest,
) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
	nextPageToken := request.NextPageToken
	for {
		response, err := t.tClient.WorkflowService().GetWorkflowExecutionHistory(
			ctx,
			&workflowservice.GetWorkflowExecutionHistoryRequest{
				Namespace: t.namespace,
				Execution: &common.WorkflowExecution{
					WorkflowId: request.WorkflowID,
					RunId:      request.RunID,
				},
				MaximumPageSize:        request.EstimatePageSize,
				NextPageToken:          nextPageToken,
				HistoryEventFilterType: enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT,
			},
		)
		if err != nil {
			return nil, err
		}
		rawEvents := response.GetHistory().GetEvents()
		reachedStart := len(rawEvents) > 0 &&
			rawEvents[len(rawEvents)-1].GetEventId() >= request.StartInternalEventID
		if len(request.NextPageToken) > 0 ||
			request.StartInternalEventID <= 1 ||
			reachedStart ||
			len(response.GetNextPageToken()) == 0 {
			return response, nil
		}
		nextPageToken = response.GetNextPageToken()
	}
}

func (t *temporalClient) buildTemporalHistoryEvents(
	ctx context.Context,
	workflowID string,
	runID string,
	startInternalEventID int64,
	nextInternalEventID int64,
) ([]*dexpb.FlowHistoryEvent, error) {
	iterator := t.tClient.GetWorkflowHistory(
		ctx,
		workflowID,
		runID,
		false,
		enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT,
	)
	builder := historybuilder.NewBuilder(workflowID, runID)
	scheduledTypes := map[int64]string{}
	fallbackFailures := map[string][]*dexpb.StepMethodAttemptFailure{}
	for iterator.HasNext() {
		event, err := iterator.Next()
		if err != nil {
			return nil, err
		}
		if err := t.addTemporalHistoryEvent(
			builder,
			scheduledTypes,
			fallbackFailures,
			event,
		); err != nil {
			return nil, err
		}
	}
	return builder.EventsInRange(startInternalEventID, nextInternalEventID)
}

func (t *temporalClient) WaitForWorkflowHistoryEvent(
	ctx context.Context,
	workflowID string,
	runID string,
	nextInternalEventID int64,
) (*uclient.WorkflowHistory, error) {
	iterator := t.tClient.GetWorkflowHistory(
		ctx,
		workflowID,
		runID,
		true,
		enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT,
	)
	for iterator.HasNext() {
		event, err := iterator.Next()
		if err != nil {
			return nil, err
		}
		if event.GetEventId() >= nextInternalEventID {
			return &uclient.WorkflowHistory{
				EventAvailable:           true,
				AvailableInternalEventID: event.GetEventId(),
			}, nil
		}
	}
	return &uclient.WorkflowHistory{}, nil
}

func (t *temporalClient) addTemporalHistoryEvent(
	builder *historybuilder.Builder,
	scheduledTypes map[int64]string,
	fallbackFailures map[string][]*dexpb.StepMethodAttemptFailure,
	event *history.HistoryEvent,
) error {
	eventTime := event.GetEventTime().AsTime()
	switch event.GetEventType() {
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED:
		var input dexpb.InterpreterWorkflowInput
		if err := t.dataConverter.FromPayloads(
			event.GetWorkflowExecutionStartedEventAttributes().GetInput(),
			&input,
		); err != nil {
			return err
		}
		builder.RecordStart(event.GetEventId(), eventTime, &input)
	case enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
		return t.recordTemporalScheduledActivity(
			builder,
			scheduledTypes,
			fallbackFailures,
			event,
		)
	case enums.EVENT_TYPE_ACTIVITY_TASK_STARTED:
		attributes := event.GetActivityTaskStartedEventAttributes()
		builder.RecordActivityStarted(
			eventTime,
			attributes.GetScheduledEventId(),
			attributes.GetAttempt(),
			temporalStepFailure(
				attributes.GetLastFailure(),
				enums.RETRY_STATE_IN_PROGRESS.String(),
			),
		)
	case enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
		return t.recordTemporalCompletedActivity(builder, scheduledTypes, event)
	case enums.EVENT_TYPE_ACTIVITY_TASK_FAILED:
		attributes := event.GetActivityTaskFailedEventAttributes()
		if !isStepActivity(scheduledTypes[attributes.GetScheduledEventId()]) {
			return nil
		}
		return builder.RecordActivityFailed(
			event.GetEventId(),
			eventTime,
			attributes.GetScheduledEventId(),
			temporalStepFailure(attributes.GetFailure(), attributes.GetRetryState().String()),
		)
	case enums.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT:
		attributes := event.GetActivityTaskTimedOutEventAttributes()
		if !isStepActivity(scheduledTypes[attributes.GetScheduledEventId()]) {
			return nil
		}
		return builder.RecordActivityFailed(
			event.GetEventId(),
			eventTime,
			attributes.GetScheduledEventId(),
			&dexpb.StepMethodFailure{
				Message:    "step method activity timed out",
				RetryState: attributes.GetRetryState().String(),
			},
		)
	case enums.EVENT_TYPE_MARKER_RECORDED:
		return t.recordTemporalLocalActivity(
			builder,
			fallbackFailures,
			event,
		)
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED:
		attributes := event.GetWorkflowExecutionSignaledEventAttributes()
		if attributes.GetSignalName() != service.ExecuteRpcSignalChannelName {
			return nil
		}
		var request dexpb.ExecuteRpcSignalRequest
		if err := t.dataConverter.FromPayloads(attributes.GetInput(), &request); err != nil {
			return err
		}
		builder.RecordSignal(event.GetEventId(), eventTime, &request)
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:
		var output dexpb.InterpreterWorkflowOutput
		attributes := event.GetWorkflowExecutionCompletedEventAttributes()
		if attributes.GetResult() != nil {
			if err := t.dataConverter.FromPayloads(attributes.GetResult(), &output); err != nil {
				return err
			}
		}
		builder.RecordClose(event.GetEventId(), eventTime, &dexpb.FlowClosedHistoryEvent{
			FlowStatus: dexpb.FlowStatus_FLOW_STATUS_COMPLETED,
			Results:    output.GetStepCompletionOutputs(),
		})
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED:
		attributes := event.GetWorkflowExecutionFailedEventAttributes()
		builder.RecordClose(event.GetEventId(), eventTime, &dexpb.FlowClosedHistoryEvent{
			FlowStatus:   dexpb.FlowStatus_FLOW_STATUS_FAILED,
			ErrorType:    temporalFlowErrorType(attributes.GetFailure()),
			ErrorMessage: attributes.GetFailure().GetMessage(),
		})
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT:
		builder.RecordClose(event.GetEventId(), eventTime, &dexpb.FlowClosedHistoryEvent{
			FlowStatus: dexpb.FlowStatus_FLOW_STATUS_TIMEOUT,
		})
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED:
		attributes := event.GetWorkflowExecutionTerminatedEventAttributes()
		builder.RecordClose(event.GetEventId(), eventTime, &dexpb.FlowClosedHistoryEvent{
			FlowStatus:   dexpb.FlowStatus_FLOW_STATUS_TERMINATED,
			ErrorMessage: attributes.GetReason(),
		})
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED:
		builder.RecordClose(event.GetEventId(), eventTime, &dexpb.FlowClosedHistoryEvent{
			FlowStatus: dexpb.FlowStatus_FLOW_STATUS_CANCELED,
		})
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW:
		attributes := event.GetWorkflowExecutionContinuedAsNewEventAttributes()
		builder.RecordClose(event.GetEventId(), eventTime, &dexpb.FlowClosedHistoryEvent{
			FlowStatus:       dexpb.FlowStatus_FLOW_STATUS_CONTINUED_AS_NEW,
			ContinuedToRunId: attributes.GetNewExecutionRunId(),
		})
	}
	return nil
}

func (t *temporalClient) recordTemporalScheduledActivity(
	builder *historybuilder.Builder,
	scheduledTypes map[int64]string,
	fallbackFailures map[string][]*dexpb.StepMethodAttemptFailure,
	event *history.HistoryEvent,
) error {
	attributes := event.GetActivityTaskScheduledEventAttributes()
	activityType := attributes.GetActivityType().GetName()
	durability := dexpb.StepDurability_STEP_DURABILITY_SYNC
	method := activityMethod(activityType)
	var previousFailures []*dexpb.StepMethodAttemptFailure
	if failures := fallbackFailures[method]; len(failures) > 0 {
		durability = dexpb.StepDurability_STEP_DURABILITY_ASYNC
		previousFailures = failures
		delete(fallbackFailures, method)
	}
	switch {
	case strings.Contains(activityType, "InvokeWaitForMethod"):
		var input dexpb.InvokeWaitForMethodActivityInput
		if err := t.dataConverter.FromPayloads(attributes.GetInput(), &input); err != nil {
			return err
		}
		builder.RecordWaitScheduled(
			event.GetEventId(),
			event.GetEventTime().AsTime(),
			&input,
			durability,
			previousFailures,
		)
	case strings.Contains(activityType, "InvokeExecuteMethod"):
		var input dexpb.InvokeExecuteMethodActivityInput
		if err := t.dataConverter.FromPayloads(attributes.GetInput(), &input); err != nil {
			return err
		}
		builder.RecordExecuteScheduled(
			event.GetEventId(),
			event.GetEventTime().AsTime(),
			&input,
			durability,
			previousFailures,
		)
	case strings.Contains(activityType, "DumpFlowForContinueAsNew"):
	default:
		return nil
	}
	scheduledTypes[event.GetEventId()] = activityType
	return nil
}

func (t *temporalClient) recordTemporalCompletedActivity(
	builder *historybuilder.Builder,
	scheduledTypes map[int64]string,
	event *history.HistoryEvent,
) error {
	attributes := event.GetActivityTaskCompletedEventAttributes()
	activityType := scheduledTypes[attributes.GetScheduledEventId()]
	switch {
	case strings.Contains(activityType, "InvokeWaitForMethod"):
		var output dexpb.InvokeWaitForMethodActivityOutput
		if err := t.dataConverter.FromPayloads(attributes.GetResult(), &output); err != nil {
			return err
		}
		return builder.RecordActivityCompleted(
			event.GetEventId(),
			event.GetEventTime().AsTime(),
			attributes.GetScheduledEventId(),
			&output,
			nil,
		)
	case strings.Contains(activityType, "InvokeExecuteMethod"):
		var output dexpb.InvokeExecuteMethodActivityOutput
		if err := t.dataConverter.FromPayloads(attributes.GetResult(), &output); err != nil {
			return err
		}
		return builder.RecordActivityCompleted(
			event.GetEventId(),
			event.GetEventTime().AsTime(),
			attributes.GetScheduledEventId(),
			nil,
			&output,
		)
	case strings.Contains(activityType, "DumpFlowForContinueAsNew"):
		var output dexpb.DumpFlowForContinueAsNewActivityOutput
		if err := t.dataConverter.FromPayloads(attributes.GetResult(), &output); err != nil {
			return err
		}
		builder.RecordContinueDump(&output)
	}
	return nil
}

func (t *temporalClient) recordTemporalLocalActivity(
	builder *historybuilder.Builder,
	fallbackFailures map[string][]*dexpb.StepMethodAttemptFailure,
	event *history.HistoryEvent,
) error {
	attributes := event.GetMarkerRecordedEventAttributes()
	if attributes.GetMarkerName() != "LocalActivity" {
		return nil
	}
	var marker localActivityMarkerData
	if err := t.dataConverter.FromPayloads(attributes.GetDetails()["data"], &marker); err != nil {
		return err
	}
	result := attributes.GetDetails()["result"]
	if result == nil {
		method := activityMethod(marker.ActivityType)
		fallbackFailures[method] = append(
			fallbackFailures[method],
			&dexpb.StepMethodAttemptFailure{
				Attempt:    marker.Attempt,
				FailedTime: event.GetEventTime(),
				Failure: temporalStepFailure(
					attributes.GetFailure(),
					enums.RETRY_STATE_IN_PROGRESS.String(),
				),
			},
		)
		return nil
	}
	previousFailures := fallbackFailures[activityMethod(marker.ActivityType)]
	delete(fallbackFailures, activityMethod(marker.ActivityType))
	switch {
	case strings.Contains(marker.ActivityType, "InvokeWaitForMethod"):
		var output dexpb.InvokeWaitForMethodActivityOutput
		if err := t.dataConverter.FromPayloads(result, &output); err != nil {
			return err
		}
		builder.RecordLocalWaitCompleted(
			event.GetEventId(),
			event.GetEventTime().AsTime(),
			&output,
			marker.Attempt,
			previousFailures,
		)
	case strings.Contains(marker.ActivityType, "InvokeExecuteMethod"):
		var output dexpb.InvokeExecuteMethodActivityOutput
		if err := t.dataConverter.FromPayloads(result, &output); err != nil {
			return err
		}
		builder.RecordLocalExecuteCompleted(
			event.GetEventId(),
			event.GetEventTime().AsTime(),
			&output,
			marker.Attempt,
			previousFailures,
		)
	case strings.Contains(marker.ActivityType, "DumpFlowForContinueAsNew"):
		var output dexpb.DumpFlowForContinueAsNewActivityOutput
		if err := t.dataConverter.FromPayloads(result, &output); err != nil {
			return err
		}
		builder.RecordContinueDump(&output)
	}
	return nil
}

func temporalStepFailure(
	failure *failurepb.Failure,
	retryState string,
) *dexpb.StepMethodFailure {
	if failure == nil {
		return &dexpb.StepMethodFailure{RetryState: retryState}
	}
	errorType := ""
	if failure.GetApplicationFailureInfo() != nil {
		errorType = failure.GetApplicationFailureInfo().GetType()
	}
	return &dexpb.StepMethodFailure{
		Message:    failure.GetMessage(),
		ErrorType:  errorType,
		StackTrace: failure.GetStackTrace(),
		RetryState: retryState,
	}
}

func temporalFlowErrorType(failure *failurepb.Failure) dexpb.FlowErrorType {
	if failure == nil || failure.GetApplicationFailureInfo() == nil {
		return dexpb.FlowErrorType_FLOW_ERROR_TYPE_UNSPECIFIED
	}
	value, ok := dexpb.FlowErrorType_value[failure.GetApplicationFailureInfo().GetType()]
	if !ok {
		return dexpb.FlowErrorType_FLOW_ERROR_TYPE_UNSPECIFIED
	}
	return dexpb.FlowErrorType(value)
}

func activityMethod(activityType string) string {
	switch {
	case strings.Contains(activityType, "InvokeWaitForMethod"):
		return "wait-for"
	case strings.Contains(activityType, "InvokeExecuteMethod"):
		return "execute"
	default:
		return activityType
	}
}

func isStepActivity(activityType string) bool {
	return strings.Contains(activityType, "InvokeWaitForMethod") ||
		strings.Contains(activityType, "InvokeExecuteMethod")
}

func stringSearchAttribute(attributes []*dexpb.KV, key string) string {
	for _, attribute := range attributes {
		if attribute.GetKey() == key {
			return attribute.GetValue().GetStringValue()
		}
	}
	return ""
}

func (t *temporalClient) encryptMemoIfNeeded(rawMemo map[string]interface{}) (map[string]interface{}, error) {
	if !t.memoEncryption || rawMemo == nil {
		return rawMemo, nil
	}

	out := map[string]interface{}{}
	for k, v := range rawMemo {

		pl, err := t.dataConverter.ToPayload(v)
		if err != nil {
			return nil, err
		}
		out[k] = pl
	}
	return out, nil
}

func (t *temporalClient) getMemoAndDecryptIfNeeded(memo *common.Memo) (map[string]*dexpb.Value, error) {
	if memo == nil || len(memo.GetFields()) == 0 {
		return nil, nil
	}

	out := map[string]*dexpb.Value{}
	for k, payload := range memo.GetFields() {
		var value dexpb.EncodedObject

		if t.memoEncryption {
			// Newer Temporal SDKs apply the configured DataConverter (including its
			// PayloadCodec) to memos (sdk-go #1045), whereas older SDKs used the default
			// converter. dex also pre-encrypts the memo value, so the stored memo is
			// double-wrapped by the encrypting converter. Decode twice through that same
			// converter: first to recover the inner (pre-encrypted) payload, then the value.
			var encryptedPayload common.Payload
			err := t.dataConverter.FromPayload(payload, &encryptedPayload)
			if err != nil {
				return nil, err
			}

			err = t.dataConverter.FromPayload(&encryptedPayload, &value)
			if err != nil {
				return nil, err
			}
		} else {
			err := converter.GetDefaultDataConverter().FromPayload(payload, &value)
			if err != nil {
				return nil, err
			}
		}
		out[k] = &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &value}}
	}
	return out, nil
}

func mapToTemporalWorkflowIdReusePolicy(idReusePolicy dexpb.IdReusePolicy) (*enums.WorkflowIdReusePolicy, error) {
	var res enums.WorkflowIdReusePolicy
	switch idReusePolicy {
	case dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_IF_NO_RUNNING:
		res = enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE
		return &res, nil
	case dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_IF_PREVIOUS_EXISTS_ABNORMALLY:
		res = enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY
		return &res, nil
	case dexpb.IdReusePolicy_ID_REUSE_POLICY_DISALLOW_REUSE:
		res = enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
		return &res, nil
	case dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING:
		res = enums.WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING
		return &res, nil
	default:
		return nil, fmt.Errorf("unsupported workflow id reuse policy %s", idReusePolicy)
	}
}

// mapToTemporalRetryPolicy fills unset (zero-value) fields with the same
// defaults dex has always used for flow retries.
func mapToTemporalRetryPolicy(policy *dexpb.FlowRetryPolicy) *realtemporal.RetryPolicy {
	if policy == nil {
		return nil
	}

	initialIntervalSeconds := policy.GetInitialIntervalSeconds()
	if initialIntervalSeconds <= 0 {
		initialIntervalSeconds = 1
	}
	backoffCoefficient := policy.GetBackoffCoefficient()
	if backoffCoefficient <= 0 {
		backoffCoefficient = 2
	}
	maximumIntervalSeconds := policy.GetMaximumIntervalSeconds()
	if maximumIntervalSeconds <= 0 {
		maximumIntervalSeconds = 100
	}

	return &realtemporal.RetryPolicy{
		InitialInterval:    time.Second * time.Duration(initialIntervalSeconds),
		MaximumInterval:    time.Second * time.Duration(maximumIntervalSeconds),
		MaximumAttempts:    policy.GetMaximumAttempts(),
		BackoffCoefficient: float64(backoffCoefficient),
	}
}

func mapToDexWorkflowStatus(status enums.WorkflowExecutionStatus) (dexpb.FlowStatus, error) {
	switch status {
	case enums.WORKFLOW_EXECUTION_STATUS_CANCELED:
		return dexpb.FlowStatus_FLOW_STATUS_CANCELED, nil
	case enums.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		return dexpb.FlowStatus_FLOW_STATUS_COMPLETED, nil
	case enums.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW:
		return dexpb.FlowStatus_FLOW_STATUS_CONTINUED_AS_NEW, nil
	case enums.WORKFLOW_EXECUTION_STATUS_FAILED:
		return dexpb.FlowStatus_FLOW_STATUS_FAILED, nil
	case enums.WORKFLOW_EXECUTION_STATUS_RUNNING:
		return dexpb.FlowStatus_FLOW_STATUS_RUNNING, nil
	case enums.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return dexpb.FlowStatus_FLOW_STATUS_TIMEOUT, nil
	case enums.WORKFLOW_EXECUTION_STATUS_TERMINATED:
		return dexpb.FlowStatus_FLOW_STATUS_TERMINATED, nil
	default:
		return dexpb.FlowStatus_FLOW_STATUS_UNSPECIFIED, fmt.Errorf("not supported status %s", status)
	}
}

func (t *temporalClient) GetWorkflowResult(
	ctx context.Context, valuePtr interface{}, workflowID string, runID string,
) (resolvedRunID string, status dexpb.FlowStatus, err error) {
	workflowRun := t.tClient.GetWorkflow(ctx, workflowID, runID)
	err = workflowRun.Get(ctx, valuePtr)
	resolvedRunID = workflowRun.GetRunID()
	switch {
	case err == nil:
		status = dexpb.FlowStatus_FLOW_STATUS_COMPLETED
	case realtemporal.IsCanceledError(err):
		status = dexpb.FlowStatus_FLOW_STATUS_CANCELED
	case realtemporal.IsTimeoutError(err):
		status = dexpb.FlowStatus_FLOW_STATUS_TIMEOUT
	case realtemporal.IsTerminatedError(err):
		status = dexpb.FlowStatus_FLOW_STATUS_TERMINATED
	default:
		var workflowExecutionError *realtemporal.WorkflowExecutionError
		if errors.As(err, &workflowExecutionError) {
			status = dexpb.FlowStatus_FLOW_STATUS_FAILED
		}
	}
	return
}

func (t *temporalClient) SynchronousUpdateWorkflow(
	ctx context.Context, valuePtr interface{}, workflowID, runID, updateID, updateType string, input interface{},
) error {
	args := []interface{}{input}
	options := client.UpdateWorkflowOptions{
		UpdateID:   updateID,
		WorkflowID: workflowID,
		RunID:      runID,
		UpdateName: updateType,
		Args:       args,
		// TODO: Leaving this as Accepted that was a default value before WaitForStage became required argument, but Completed might be a better choice
		WaitForStage: client.WorkflowUpdateStageAccepted,
	}
	handle, err := t.tClient.UpdateWorkflow(ctx, options)
	if err != nil {
		return err
	}
	return handle.Get(ctx, valuePtr)
}

func (t *temporalClient) ResetWorkflow(
	ctx context.Context, request *dexpb.ResetFlowRequest,
) (runId string, err error) {
	reqRunId := request.GetRunId()
	if reqRunId == "" {
		// set default runId to current
		resp, err := t.tClient.DescribeWorkflowExecution(ctx, request.GetFlowId(), "")
		if err != nil {
			return "", err
		}
		reqRunId = resp.GetWorkflowExecutionInfo().GetExecution().GetRunId()
	}

	resetType := request.GetResetType()
	resetBaseRunID, resetEventId, err := getResetEventIDByType(ctx, resetType,
		t.namespace, request.GetFlowId(), reqRunId,
		t.tClient.WorkflowService(), t.dataConverter,
		request.GetHistoryEventId(), request.GetHistoryEventTime(), request.GetStepType(), request.GetStepExecutionId())

	if err != nil {
		return "", err
	}

	requestId := uuid.New().String()
	var resetReapplyExcludeTypes []enums.ResetReapplyExcludeType
	if request.GetSkipChannelMessagesReapply() {
		resetReapplyExcludeTypes = append(resetReapplyExcludeTypes, enums.RESET_REAPPLY_EXCLUDE_TYPE_SIGNAL)
	}
	if request.GetSkipLockingRpcReapply() {
		resetReapplyExcludeTypes = append(resetReapplyExcludeTypes, enums.RESET_REAPPLY_EXCLUDE_TYPE_UPDATE)
	}

	resp, err := t.tClient.ResetWorkflowExecution(ctx, &workflowservice.ResetWorkflowExecutionRequest{
		Namespace: t.namespace,
		WorkflowExecution: &common.WorkflowExecution{
			WorkflowId: request.FlowId,
			RunId:      resetBaseRunID,
		},
		Reason:                    request.GetReason(),
		WorkflowTaskFinishEventId: resetEventId,
		RequestId:                 requestId,
		ResetReapplyExcludeTypes:  resetReapplyExcludeTypes,
	})

	if err != nil {
		return "", err
	}
	return resp.GetRunId(), nil
}

func (t *temporalClient) GetBackendType() (backendType service.BackendType) {
	return service.BackendTypeTemporal
}

func (t *temporalClient) GetApiService() interface{} {
	return t.tClient.WorkflowService()
}
