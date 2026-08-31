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

package dealdsl

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
	sdk "github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

//go:embed ui/deal-dsl/*
var dealDSLUI embed.FS

const (
	allExecutionsQuery = "ProcessID IS NOT NULL"
	initializeStepType = "InitializeDeal"
	searchPageSize     = int32(1000)
)

type controller struct {
	client     *sdk.Client
	flow       *DealDSLFlow
	repository Repository
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

func RegisterRoutes(
	router gin.IRouter,
	client *sdk.Client,
	flow *DealDSLFlow,
	repository Repository,
) {
	if client == nil {
		panic("RegisterRoutes requires Client")
	}
	if flow == nil {
		panic("RegisterRoutes requires DealDSLFlow")
	}
	if repository == nil {
		panic("RegisterRoutes requires Repository")
	}
	controller := &controller{
		client:     client,
		flow:       flow,
		repository: repository,
	}
	group := router.Group("/products/deal-dsl")
	group.GET("", controller.index)
	group.GET("/processes/:processID", controller.index)
	group.GET("/executions/:flowID", controller.index)
	group.GET("/app.js", controller.script)
	group.GET("/styles.css", controller.styles)
	group.GET("/comprehensive-process.json", controller.processTemplate)
	group.GET("/api/actions", controller.listActions)
	group.POST("/api/processes", controller.createDealProcess)
	group.GET("/api/processes", controller.listDealProcesses)
	group.GET("/api/processes/:processID", controller.getDealProcess)
	group.PUT("/api/processes/:processID", controller.updateDealProcess)
	group.POST("/api/executions", controller.startDealExecution)
	group.GET("/api/executions", controller.listDealExecutions)
	group.GET("/api/executions/:flowID", controller.getDealExecution)
	group.POST(
		"/api/executions/:flowID/channels/:conditionName",
		controller.sendChannelMessage,
	)
}

func (controller *controller) index(request *gin.Context) {
	controller.serveUIFile(request, "index.html", "text/html; charset=utf-8")
}

func (controller *controller) script(request *gin.Context) {
	controller.serveUIFile(request, "app.js", "application/javascript; charset=utf-8")
}

func (controller *controller) styles(request *gin.Context) {
	controller.serveUIFile(request, "styles.css", "text/css; charset=utf-8")
}

func (controller *controller) processTemplate(request *gin.Context) {
	controller.serveUIFile(request, "comprehensive-process.json", "application/json; charset=utf-8")
}

func (*controller) listActions(request *gin.Context) {
	request.JSON(http.StatusOK, gin.H{"actions": AvailableActionNames()})
}

func (controller *controller) listDealProcesses(request *gin.Context) {
	processes, err := controller.repository.ListProcesses(request.Request.Context())
	if err != nil {
		controller.respondError(request, err)
		return
	}
	request.JSON(http.StatusOK, gin.H{"processes": processes})
}

func (controller *controller) createDealProcess(request *gin.Context) {
	var process DealProcess
	if err := request.ShouldBindJSON(&process); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := ValidateProcess(process); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := controller.repository.CreateProcess(request.Request.Context(), process); err != nil {
		controller.respondError(request, err)
		return
	}
	request.JSON(http.StatusCreated, process)
}

func (controller *controller) getDealProcess(request *gin.Context) {
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

func (controller *controller) updateDealProcess(request *gin.Context) {
	processID := strings.TrimSpace(request.Param("processID"))
	var process DealProcess
	if err := request.ShouldBindJSON(&process); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if process.ProcessID != processID {
		request.JSON(http.StatusBadRequest, gin.H{"error": "processID cannot be changed"})
		return
	}
	if err := ValidateProcess(process); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := controller.repository.UpdateProcess(request.Request.Context(), process); err != nil {
		controller.respondError(request, err)
		return
	}
	request.JSON(http.StatusOK, process)
}

func (controller *controller) startDealExecution(request *gin.Context) {
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
	process, err := controller.repository.GetProcess(requestContext, input.ProcessID)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	flowID := input.ProcessID + "-" + uuid.NewString()
	runID, err := controller.client.StartFlow(
		requestContext,
		controller.flow,
		flowID,
		DealStart{Process: process, BuyerID: input.BuyerID},
		sdk.StartFlowOptions{
			RequestID: ptr.Any(flowID),
		},
	)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	waitContext, cancelWait := context.WithTimeout(requestContext, 30*time.Second)
	defer cancelWait()
	if err := controller.client.WaitForStepCompletion(
		waitContext,
		flowID,
		sdk.StepExecutionID{StepType: initializeStepType},
	); err != nil {
		controller.respondError(request, err)
		return
	}
	request.JSON(http.StatusCreated, startDealExecutionResponse{FlowID: flowID, RunID: runID})
}

func (controller *controller) listDealExecutions(request *gin.Context) {
	executions, err := controller.findDealExecutions(
		request.Request.Context(),
		executionSearchQuery(
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

func (controller *controller) getDealExecution(request *gin.Context) {
	flowID := request.Param("flowID")
	executions, err := controller.findDealExecutions(
		request.Request.Context(),
		fmt.Sprintf(
			"WorkflowId='%s' AND %s",
			searchString(flowID),
			allExecutionsQuery,
		),
	)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	if len(executions) == 0 {
		controller.respondError(request, ErrExecutionNotFound)
		return
	}
	request.JSON(http.StatusOK, executions[0])
}

func (controller *controller) findDealExecutions(
	ctx context.Context,
	query string,
) ([]DealExecution, error) {
	entries, err := controller.searchLatestExecutionRuns(ctx, query)
	if err != nil {
		return nil, err
	}
	executions := make([]DealExecution, 0, len(entries))
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

func (controller *controller) searchLatestExecutionRuns(
	ctx context.Context,
	query string,
) ([]sdk.SearchFlowEntry, error) {
	latestByFlowID := make(map[string]sdk.SearchFlowEntry)
	nextPageToken := ""
	for {
		page, err := controller.client.SearchFlows(
			ctx,
			query,
			searchPageSize,
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

func (controller *controller) projectDealExecution(
	ctx context.Context,
	entry sdk.SearchFlowEntry,
) (DealExecution, error) {
	values, err := controller.client.GetAttributes(
		ctx,
		entry.FlowID,
		ProcessID,
		ItemID,
		ProcessDefinition,
		BuyerID,
		CurrentState,
		CurrentActionIndexToExecute,
		PendingPreConditionState,
		PendingPreConditionName,
		StateData,
	)
	if err != nil {
		return DealExecution{}, err
	}
	processID, err := decodeRequiredAttribute(values, ProcessID)
	if err != nil {
		return DealExecution{}, err
	}
	processDefinition, err := decodeRequiredAttribute(values, ProcessDefinition)
	if err != nil {
		return DealExecution{}, err
	}
	buyerID, err := decodeRequiredAttribute(values, BuyerID)
	if err != nil {
		return DealExecution{}, err
	}
	stateData, err := decodeRequiredAttribute(values, StateData)
	if err != nil {
		return DealExecution{}, err
	}
	currentState, err := decodeOptionalAttribute(values, CurrentState)
	if err != nil {
		return DealExecution{}, err
	}
	currentActionIndex, err := decodeOptionalAttribute(
		values,
		CurrentActionIndexToExecute,
	)
	if err != nil {
		return DealExecution{}, err
	}
	pendingState, err := decodeOptionalAttribute(values, PendingPreConditionState)
	if err != nil {
		return DealExecution{}, err
	}
	pendingName, err := decodeOptionalAttribute(values, PendingPreConditionName)
	if err != nil {
		return DealExecution{}, err
	}
	status, err := flowStatus(entry.Status)
	if err != nil {
		return DealExecution{}, err
	}
	var closedAt *time.Time
	if !entry.ClosedAt.IsZero() {
		closedAt = ptr.Any(entry.ClosedAt)
	}
	return DealExecution{
		FlowID:                   entry.FlowID,
		RunID:                    entry.RunID,
		ProcessID:                processID,
		ItemID:                   processDefinition.ItemID,
		ItemName:                 processDefinition.ItemName,
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

func executionSearchQuery(processID string, buyerID string) string {
	filters := make([]string, 0, 2)
	if buyerID != "" {
		filters = append(filters, "BuyerID='"+searchString(buyerID)+"'")
	}
	if processID != "" {
		filters = append(filters, "ProcessID='"+searchString(processID)+"'")
	}
	if len(filters) == 0 {
		return allExecutionsQuery
	}
	return strings.Join(filters, " AND ")
}

func searchString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func (controller *controller) sendChannelMessage(request *gin.Context) {
	var message channelMessageRequest
	if err := request.ShouldBindJSON(&message); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	flowID := request.Param("flowID")
	conditionName := request.Param("conditionName")
	requestContext := request.Request.Context()
	var process DealProcess
	found, err := controller.client.GetAttribute(
		requestContext,
		flowID,
		ProcessDefinition,
		&process,
	)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	if !found {
		controller.respondError(request, ErrExecutionNotFound)
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
		ConditionMessages,
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

func flowStatus(status sdk.FlowStatus) (string, error) {
	switch status {
	case sdk.FlowRunning:
		return "RUNNING", nil
	case sdk.FlowCompleted:
		return "COMPLETED", nil
	case sdk.FlowFailed:
		return "FAILED", nil
	case sdk.FlowServerSideTimeoutInternalOnly:
		return "TIMED_OUT", nil
	case sdk.FlowTerminated:
		return "TERMINATED", nil
	case sdk.FlowCanceled:
		return "CANCELED", nil
	case sdk.FlowContinuedAsNew:
		return "CONTINUED_AS_NEW", nil
	default:
		return "", fmt.Errorf("unknown deal DSL flow status %d", status)
	}
}

func (*controller) serveUIFile(
	request *gin.Context,
	name string,
	contentType string,
) {
	contents, err := dealDSLUI.ReadFile("ui/deal-dsl/" + name)
	if err != nil {
		request.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	request.Data(http.StatusOK, contentType, contents)
}

func (*controller) respondError(request *gin.Context, err error) {
	var missing *sdk.FlowNotFoundError
	var inactive *sdk.FlowNotActiveError
	switch {
	case errors.Is(err, ErrProcessExists):
		request.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ErrProcessNotFound),
		errors.Is(err, ErrExecutionNotFound):
		request.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.As(err, &missing), errors.As(err, &inactive):
		request.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		request.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
