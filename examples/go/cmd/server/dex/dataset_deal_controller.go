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
	"embed"
	"errors"
	"fmt"
	"net/http"
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
	datasetDealTriggerTimeout = 30 * time.Second
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

type conditionMessageRequest struct {
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
		"/api/dataset-deal/executions/:flowID/conditions/:conditionName",
		controller.triggerCondition,
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
	flowID := input.ProcessID + "-" + uuid.NewString()
	runID, err := controller.client.StartFlow(
		requestContext,
		controller.flow,
		flowID,
		datasetdeal.StartTrigger(input.ProcessID, input.BuyerID),
		sdk.StartFlowOptions{
			IDReusePolicy: sdk.IDReuseAllowIfNotRunning,
			RequestID:     ptr.Any(uuid.NewString()),
		},
	)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	if err := controller.client.WaitForStepCompletion(
		requestContext,
		flowID,
		datasetdeal.TriggerStepExecutionID(),
		sdk.WaitOptions{Timeout: datasetDealTriggerTimeout},
	); err != nil {
		controller.respondError(request, err)
		return
	}
	request.JSON(http.StatusCreated, startDealExecutionResponse{FlowID: flowID, RunID: runID})
}

func (controller *datasetDealController) listDealExecutions(request *gin.Context) {
	status := datasetdeal.ExecutionStatus(strings.ToUpper(strings.TrimSpace(request.Query("status"))))
	if status != "" && !status.Valid() {
		request.JSON(http.StatusBadRequest, gin.H{"error": "invalid execution status"})
		return
	}
	executions, err := controller.repository.ListExecutions(
		request.Request.Context(),
		datasetdeal.ExecutionFilter{
			BuyerID:              strings.TrimSpace(request.Query("buyerID")),
			ProcessID:            strings.TrimSpace(request.Query("processID")),
			Status:               status,
			CurrentState:         strings.TrimSpace(request.Query("currentState")),
			PendingConditionName: strings.TrimSpace(request.Query("pendingConditionName")),
		},
	)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	request.JSON(http.StatusOK, gin.H{"executions": executions})
}

func (controller *datasetDealController) getDealExecution(request *gin.Context) {
	execution, err := controller.repository.GetExecution(
		request.Request.Context(),
		request.Param("flowID"),
	)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	request.JSON(http.StatusOK, execution)
}

func (controller *datasetDealController) triggerCondition(request *gin.Context) {
	var message conditionMessageRequest
	if err := request.ShouldBindJSON(&message); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	flowID := request.Param("flowID")
	conditionName := request.Param("conditionName")
	requestContext := request.Request.Context()
	execution, err := controller.repository.GetExecution(
		requestContext,
		flowID,
	)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	if execution.Status != datasetdeal.ExecutionWaiting {
		controller.respondError(request, datasetdeal.ErrExecutionNotWaiting)
		return
	}
	if execution.PendingConditionName != conditionName {
		controller.respondError(request, datasetdeal.ErrConditionNotPending)
		return
	}
	runID, err := controller.client.StartFlow(
		requestContext,
		controller.flow,
		flowID,
		datasetdeal.ConditionTrigger(conditionName, message.Data),
		sdk.StartFlowOptions{
			IDReusePolicy: sdk.IDReuseAllowIfNotRunning,
			RequestID:     ptr.Any(uuid.NewString()),
		},
	)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	result, err := controller.client.WaitForFlow(
		requestContext,
		flowID,
		sdk.WaitForFlowOptions{Timeout: datasetDealTriggerTimeout},
	)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	if result.Status != sdk.FlowCompleted {
		latest, getErr := controller.repository.GetExecution(requestContext, flowID)
		if getErr != nil {
			controller.respondError(request, getErr)
			return
		}
		if latest.LatestRunID != runID &&
			(latest.Status != datasetdeal.ExecutionWaiting ||
				latest.PendingConditionName != conditionName) {
			controller.respondError(request, datasetdeal.ErrExecutionConflict)
			return
		}
		controller.respondError(request, fmt.Errorf(
			"trigger run ended with status %d: %s",
			result.Status,
			result.ErrorMessage,
		))
		return
	}
	execution, err = controller.repository.GetExecution(requestContext, flowID)
	if err != nil {
		controller.respondError(request, err)
		return
	}
	if execution.LatestRunID != runID {
		controller.respondError(request, errors.New("latest trigger run ID does not match response"))
		return
	}
	request.JSON(http.StatusOK, execution)
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
	case errors.Is(err, datasetdeal.ErrExecutionExists),
		errors.Is(err, datasetdeal.ErrExecutionConflict),
		errors.Is(err, datasetdeal.ErrExecutionNotWaiting),
		errors.Is(err, datasetdeal.ErrConditionNotPending):
		request.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.As(err, &sdkError) && sdkError.SubStatus == sdk.ErrorFlowAlreadyStarted:
		request.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.As(err, &sdkError) && sdkError.SubStatus == sdk.ErrorFlowNotFound:
		request.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		request.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
