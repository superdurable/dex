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

package dex

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/superdurable/dex/examples/go/workflows/datasetdeal"
	sdk "github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

//go:embed ui/dataset-deal/*
var datasetDealUI embed.FS

const (
	datasetDealAllExecutionsQuery = "ProcessID IS NOT NULL"
	datasetDealInitializeStepType = "datasetdeal.initializeStep"
	datasetDealSearchPageSize     = int32(1000)
)

type datasetDealController struct {
	client     *sdk.Client
	flow       *datasetdeal.DealFlow
	repository datasetdeal.Repository
}

type startDealExecutionRequest struct {
	ProcessID string `json:"processID"`
	BuyerID   string `json:"buyerID"`
}

type startDealExecutionResponse struct {
	FlowID string `json:"flowID"`
	RunID  string `json:"runID"`
}

type channelMessageRequest struct {
	Data map[string]string `json:"data"`
}

func newDatasetDealController(
	client *sdk.Client,
	flow *datasetdeal.DealFlow,
	repository datasetdeal.Repository,
) *datasetDealController {
	if client == nil {
		panic("newDatasetDealController requires Client")
	}
	if flow == nil {
		panic("newDatasetDealController requires DealFlow")
	}
	if repository == nil {
		panic("newDatasetDealController requires Repository")
	}
	return &datasetDealController{
		client:     client,
		flow:       flow,
		repository: repository,
	}
}

func (controller *datasetDealController) registerRoutes(router *gin.Engine) {
	router.GET("/dataset-deal", controller.index)
	router.GET("/dataset-deal/processes/:processID", controller.index)
	router.GET("/dataset-deal/executions/:flowID", controller.index)
	router.GET("/dataset-deal/app.js", controller.script)
	router.GET("/dataset-deal/styles.css", controller.styles)
	router.GET("/dataset-deal/comprehensive-process.json", controller.processTemplate)
	router.GET("/api/dataset-deal/actions", controller.listActions)
	router.POST("/api/dataset-deal/processes", controller.createDealProcess)
	router.GET("/api/dataset-deal/processes", controller.listDealProcesses)
	router.GET("/api/dataset-deal/processes/:processID", controller.getDealProcess)
	router.PUT("/api/dataset-deal/processes/:processID", controller.updateDealProcess)
	router.POST("/api/dataset-deal/executions", controller.startDealExecution)
	router.GET("/api/dataset-deal/executions", controller.listDealExecutions)
	router.GET("/api/dataset-deal/executions/:flowID", controller.getDealExecution)
	router.POST(
		"/api/dataset-deal/executions/:flowID/channels/:conditionName",
		controller.sendChannelMessage,
	)
}

func (controller *datasetDealController) index(request *gin.Context) {
	controller.serveUIFile(request, "index.html", "text/html; charset=utf-8")
}

func (controller *datasetDealController) script(request *gin.Context) {
	controller.serveUIFile(request, "app.js", "application/javascript; charset=utf-8")
}

func (controller *datasetDealController) styles(request *gin.Context) {
	controller.serveUIFile(request, "styles.css", "text/css; charset=utf-8")
}

func (controller *datasetDealController) processTemplate(request *gin.Context) {
	controller.serveUIFile(request, "comprehensive-process.json", "application/json; charset=utf-8")
}

func (*datasetDealController) listActions(request *gin.Context) {
	request.JSON(http.StatusOK, gin.H{"actions": datasetdeal.AvailableActionNames()})
}

func (controller *datasetDealController) listDealProcesses(request *gin.Context) {
	processes, err := controller.repository.ListProcesses(request.Request.Context())
	if err != nil {
		controller.respondError(request, err)
		return
	}
	request.JSON(http.StatusOK, gin.H{"processes": processes})
}

func (controller *datasetDealController) createDealProcess(request *gin.Context) {
	var process datasetdeal.DealProcess
	if err := request.ShouldBindJSON(&process); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := datasetdeal.ValidateProcess(process); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := controller.repository.CreateProcess(request.Request.Context(), process); err != nil {
		controller.respondError(request, err)
		return
	}
	request.JSON(http.StatusCreated, process)
}

func (controller *datasetDealController) getDealProcess(request *gin.Context) {
	process, err := controller.repository.GetProcess(
		request.Request.Context(),
		request.Param("processID"),
	)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	request.JSON(http.StatusOK, process)
}

func (controller *datasetDealController) updateDealProcess(request *gin.Context) {
	processID := strings.TrimSpace(request.Param("processID"))
	var process datasetdeal.DealProcess
	if err := request.ShouldBindJSON(&process); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if process.ProcessID != processID {
		request.JSON(http.StatusBadRequest, gin.H{"error": "processID cannot be changed"})
		return
	}
	if err := datasetdeal.ValidateProcess(process); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := controller.repository.UpdateProcess(request.Request.Context(), process); err != nil {
		controller.respondError(request, err)
		return
	}
	request.JSON(http.StatusOK, process)
}

func (controller *datasetDealController) startDealExecution(request *gin.Context) {
	var input startDealExecutionRequest
	if err := request.ShouldBindJSON(&input); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	input.ProcessID = strings.TrimSpace(input.ProcessID)
	input.BuyerID = strings.TrimSpace(input.BuyerID)
	if input.ProcessID == "" || input.BuyerID == "" {
		request.JSON(http.StatusBadRequest, gin.H{"error": "processID and buyerID are required"})
		return
	}
	requestContext := request.Request.Context()
	if _, err := controller.repository.GetProcess(requestContext, input.ProcessID); err != nil {
		controller.respondError(request, err)
		return
	}
	initialBuyerID, err := sdk.InitialAttribute(datasetdeal.BuyerID, input.BuyerID)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	flowID := input.ProcessID + "-" + uuid.NewString()
	runID, err := controller.client.StartFlow(
		requestContext,
		controller.flow,
		flowID,
		input.ProcessID,
		sdk.StartFlowOptions{
			Attributes: []sdk.InitialAttributeDef{initialBuyerID},
			RequestID:  ptr.Any(flowID),
		},
	)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	if err := controller.client.WaitForStepCompletion(
		requestContext,
		flowID,
		sdk.StepExecutionID{StepType: datasetDealInitializeStepType},
		sdk.WaitOptions{Timeout: 30 * time.Second},
	); err != nil {
		controller.respondError(request, err)
		return
	}
	request.JSON(http.StatusCreated, startDealExecutionResponse{FlowID: flowID, RunID: runID})
}

func (controller *datasetDealController) listDealExecutions(request *gin.Context) {
	executions, err := controller.findDealExecutions(
		request.Request.Context(),
		datasetDealExecutionSearchQuery(
			strings.TrimSpace(request.Query("processID")),
			strings.TrimSpace(request.Query("buyerID")),
		),
	)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	request.JSON(http.StatusOK, gin.H{"executions": executions})
}

func (controller *datasetDealController) getDealExecution(request *gin.Context) {
	flowID := request.Param("flowID")
	executions, err := controller.findDealExecutions(
		request.Request.Context(),
		fmt.Sprintf(
			"WorkflowId='%s' AND %s",
			datasetDealSearchString(flowID),
			datasetDealAllExecutionsQuery,
		),
	)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	if len(executions) == 0 {
		controller.respondError(request, datasetdeal.ErrExecutionNotFound)
		return
	}
	request.JSON(http.StatusOK, executions[0])
}

func (controller *datasetDealController) findDealExecutions(
	ctx context.Context,
	query string,
) ([]datasetdeal.DealExecution, error) {
	entries, err := controller.searchLatestExecutionRuns(ctx, query)
	if err != nil {
		return nil, err
	}
	executions := make([]datasetdeal.DealExecution, 0, len(entries))
	for _, entry := range entries {
		execution, projectErr := controller.projectDealExecution(ctx, entry)
		if projectErr != nil {
			return nil, projectErr
		}
		executions = append(executions, execution)
	}
	sort.Slice(executions, func(first int, second int) bool {
		return executions[first].StartedAt.After(executions[second].StartedAt)
	})
	return executions, nil
}

func (controller *datasetDealController) searchLatestExecutionRuns(
	ctx context.Context,
	query string,
) ([]sdk.SearchFlowEntry, error) {
	latestByFlowID := make(map[string]sdk.SearchFlowEntry)
	nextPageToken := ""
	for {
		page, err := controller.client.SearchFlows(
			ctx,
			query,
			datasetDealSearchPageSize,
			nextPageToken,
		)
		if err != nil {
			return nil, err
		}
		for _, entry := range page.Flows {
			current, found := latestByFlowID[entry.FlowID]
			if !found || entry.StartedAt.After(current.StartedAt) {
				latestByFlowID[entry.FlowID] = entry
			}
		}
		if page.NextPageToken == "" {
			break
		}
		nextPageToken = page.NextPageToken
	}
	entries := make([]sdk.SearchFlowEntry, 0, len(latestByFlowID))
	for _, entry := range latestByFlowID {
		entries = append(entries, entry)
	}
	return entries, nil
}

func (controller *datasetDealController) projectDealExecution(
	ctx context.Context,
	entry sdk.SearchFlowEntry,
) (datasetdeal.DealExecution, error) {
	values, err := controller.client.GetAttributes(
		ctx,
		entry.FlowID,
		datasetdeal.ProcessID,
		datasetdeal.ProcessDefinition,
		datasetdeal.BuyerID,
		datasetdeal.CurrentState,
		datasetdeal.CurrentActionIndexToExecute,
		datasetdeal.PendingPreConditionState,
		datasetdeal.PendingPreConditionName,
		datasetdeal.StateData,
	)
	if err != nil {
		return datasetdeal.DealExecution{}, err
	}
	processID, err := decodeRequiredAttribute(values, datasetdeal.ProcessID)
	if err != nil {
		return datasetdeal.DealExecution{}, err
	}
	processDefinition, err := decodeRequiredAttribute(values, datasetdeal.ProcessDefinition)
	if err != nil {
		return datasetdeal.DealExecution{}, err
	}
	buyerID, err := decodeRequiredAttribute(values, datasetdeal.BuyerID)
	if err != nil {
		return datasetdeal.DealExecution{}, err
	}
	stateData, err := decodeRequiredAttribute(values, datasetdeal.StateData)
	if err != nil {
		return datasetdeal.DealExecution{}, err
	}
	currentState, err := decodeOptionalAttribute(values, datasetdeal.CurrentState)
	if err != nil {
		return datasetdeal.DealExecution{}, err
	}
	currentActionIndex, err := decodeOptionalAttribute(
		values,
		datasetdeal.CurrentActionIndexToExecute,
	)
	if err != nil {
		return datasetdeal.DealExecution{}, err
	}
	pendingState, err := decodeOptionalAttribute(values, datasetdeal.PendingPreConditionState)
	if err != nil {
		return datasetdeal.DealExecution{}, err
	}
	pendingName, err := decodeOptionalAttribute(values, datasetdeal.PendingPreConditionName)
	if err != nil {
		return datasetdeal.DealExecution{}, err
	}
	status, err := datasetDealStatus(entry.Status)
	if err != nil {
		return datasetdeal.DealExecution{}, err
	}
	var closedAt *time.Time
	if !entry.ClosedAt.IsZero() {
		closedAt = ptr.Any(entry.ClosedAt)
	}
	return datasetdeal.DealExecution{
		FlowID:                   entry.FlowID,
		RunID:                    entry.RunID,
		ProcessID:                processID,
		ProcessDefinition:        processDefinition,
		BuyerID:                  buyerID,
		CurrentState:             currentState,
		CurrentActionIndex:       currentActionIndex,
		PendingPreConditionState: pendingState,
		PendingPreConditionName:  pendingName,
		StateData:                stateData,
		Status:                   status,
		StartedAt:                entry.StartedAt,
		ClosedAt:                 closedAt,
	}, nil
}

func datasetDealExecutionSearchQuery(processID string, buyerID string) string {
	filters := make([]string, 0, 2)
	if buyerID != "" {
		filters = append(filters, "BuyerID='"+datasetDealSearchString(buyerID)+"'")
	}
	if processID != "" {
		filters = append(filters, "ProcessID='"+datasetDealSearchString(processID)+"'")
	}
	if len(filters) == 0 {
		return datasetDealAllExecutionsQuery
	}
	return strings.Join(filters, " AND ")
}

func datasetDealSearchString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func (controller *datasetDealController) sendChannelMessage(request *gin.Context) {
	var message channelMessageRequest
	if err := request.ShouldBindJSON(&message); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	flowID := request.Param("flowID")
	conditionName := request.Param("conditionName")
	requestContext := request.Request.Context()
	var process datasetdeal.DealProcess
	found, err := controller.client.GetAttribute(
		requestContext,
		flowID,
		datasetdeal.ProcessDefinition,
		&process,
	)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	if !found {
		controller.respondError(request, datasetdeal.ErrExecutionNotFound)
		return
	}
	if !process.HasCondition(conditionName) {
		request.JSON(
			http.StatusBadRequest,
			gin.H{"error": "condition is not defined by this deal process"},
		)
		return
	}
	if err := controller.client.PublishToChannelMap(
		requestContext,
		flowID,
		datasetdeal.ConditionMessages,
		conditionName,
		message.Data,
	); err != nil {
		controller.respondError(request, err)
		return
	}
	request.JSON(http.StatusAccepted, gin.H{"flowID": flowID, "conditionName": conditionName})
}

func decodeRequiredAttribute[T any](
	values map[string]sdk.Value,
	attribute sdk.Attribute[T],
) (T, error) {
	value, found, err := decodeAttribute(values, attribute)
	if err != nil {
		return value, err
	}
	if !found {
		return value, fmt.Errorf("attribute %q is missing", attribute.AttributeName())
	}
	return value, nil
}

func decodeOptionalAttribute[T any](
	values map[string]sdk.Value,
	attribute sdk.Attribute[T],
) (T, error) {
	value, _, err := decodeAttribute(values, attribute)
	return value, err
}

func decodeAttribute[T any](
	values map[string]sdk.Value,
	attribute sdk.Attribute[T],
) (value T, found bool, err error) {
	encoded, found := values[attribute.AttributeName()]
	if !found {
		return value, false, nil
	}
	if err := encoded.Decode(&value); err != nil {
		return value, false, fmt.Errorf(
			"decode attribute %q: %w",
			attribute.AttributeName(),
			err,
		)
	}
	return value, true, nil
}

func datasetDealStatus(status sdk.FlowStatus) (string, error) {
	switch status {
	case sdk.FlowRunning:
		return "RUNNING", nil
	case sdk.FlowCompleted:
		return "COMPLETED", nil
	case sdk.FlowFailed:
		return "FAILED", nil
	case sdk.FlowTimedOut:
		return "TIMED_OUT", nil
	case sdk.FlowTerminated:
		return "TERMINATED", nil
	case sdk.FlowCanceled:
		return "CANCELED", nil
	case sdk.FlowContinuedAsNew:
		return "CONTINUED_AS_NEW", nil
	default:
		return "", fmt.Errorf("unknown dataset deal flow status %d", status)
	}
}

func (*datasetDealController) serveUIFile(
	request *gin.Context,
	name string,
	contentType string,
) {
	contents, err := datasetDealUI.ReadFile("ui/dataset-deal/" + name)
	if err != nil {
		request.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	request.Data(http.StatusOK, contentType, contents)
}

func (*datasetDealController) respondError(request *gin.Context, err error) {
	var sdkError *sdk.Error
	switch {
	case errors.Is(err, datasetdeal.ErrProcessExists):
		request.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, datasetdeal.ErrProcessNotFound),
		errors.Is(err, datasetdeal.ErrExecutionNotFound):
		request.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.As(err, &sdkError) && sdkError.SubStatus == sdk.ErrorFlowNotFound:
		request.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		request.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
