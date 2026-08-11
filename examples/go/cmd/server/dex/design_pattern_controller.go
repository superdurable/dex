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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/workflows"
	drainsignal "github.com/superdurable/dex/examples/go/workflows/patterns/drainchannels/signal"
	"github.com/superdurable/dex/examples/go/workflows/patterns/entitystore"
	"github.com/superdurable/dex/examples/go/workflows/patterns/parallel"
	"github.com/superdurable/dex/examples/go/workflows/patterns/recovery"
	"github.com/superdurable/dex/examples/go/workflows/patterns/reminders"
	"github.com/superdurable/dex/examples/go/workflows/patterns/waitforstatecompletion"
	sdk "github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

type designPatternController struct {
	client *sdk.Client
}

func newDesignPatternController(client *sdk.Client) *designPatternController {
	return &designPatternController{client: client}
}

func (controller *designPatternController) registerRoutes(router *gin.Engine) {
	router.GET("/design-pattern/polling/start/simple", controller.startSimplePolling)
	router.GET("/design-pattern/polling/start/backoff", controller.startBackoffPolling)
	router.GET("/design-pattern/interruptible/start", controller.startInterruptible)
	router.GET("/design-pattern/interruptible/cancel", controller.cancelInterruptible)
	router.GET("/design-pattern/workflow-with-reminder/start", controller.startReminder)
	router.GET("/design-pattern/workflow-with-reminder/accept", controller.acceptReminder)
	router.GET("/design-pattern/workflow-with-reminder/optout", controller.optOutReminder)
	router.POST("/design-pattern/entity-store/profile", controller.createUserProfile)
	router.POST("/design-pattern/entity-store/profile/update", controller.updateUserProfile)
	router.GET("/design-pattern/entity-store/profile", controller.getUserProfile)
	router.POST("/design-pattern/entity-store/profile/clear", controller.clearUserProfile)
	router.GET("/design-pattern/intervention/start", controller.startIntervention)
	router.GET("/design-pattern/resettabletimer/start", controller.startResettableTimer)
	router.GET("/design-pattern/resettabletimer/reset", controller.resetResettableTimer)
	router.GET("/design-pattern/parallel/start/simple", controller.startParallelSimple)
	router.GET("/design-pattern/parallel/start/withAwait", controller.startParallelWithAwait)
	router.GET("/design-pattern/recovery/start", controller.startRecovery)
	router.GET("/design-pattern/scalableparallel/start", controller.startScalableParallel)
	router.GET("/design-pattern/parentchild/start", controller.startParentChild)
	router.GET("/design-pattern/drainchannels/internal/start", controller.startDrainInternal)
	router.GET("/design-pattern/drainchannels/signal/startorsignal", controller.startOrSignalDrain)
	router.GET("/design-pattern/waitforstatecompletion/start", controller.startWaitForStateCompletion)
	router.GET("/design-pattern/timeout/start", controller.startTimeout)
}

func patternStartOptions() sdk.StartFlowOptions {
	timeout := time.Hour
	return sdk.StartFlowOptions{Timeout: &timeout}
}

func idReuseStartOptions() sdk.StartFlowOptions {
	timeout := time.Hour
	return sdk.StartFlowOptions{
		Timeout:       &timeout,
		IDReusePolicy: sdk.IDReuseAllowIfPreviousFailed,
	}
}

func (controller *designPatternController) startSimplePolling(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		workflows.SimplePolling,
		flowID,
		nil,
		patternStartOptions(),
	)
	respondString(request, runID, err)
}

func (controller *designPatternController) startBackoffPolling(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		workflows.BackoffPolling,
		flowID,
		nil,
		patternStartOptions(),
	)
	respondString(request, runID, err)
}

func (controller *designPatternController) startInterruptible(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		workflows.InterruptibleExecution,
		flowID,
		nil,
		patternStartOptions(),
	)
	respondString(request, runID, err)
}

func (controller *designPatternController) cancelInterruptible(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	var none sdk.None
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		workflows.InterruptibleExecution.Interrupt,
		nil,
		&none,
		sdk.InvokeOptions{},
	)
	respondString(request, "done", err)
}

func (controller *designPatternController) startReminder(request *gin.Context) {
	flowID := fmt.Sprintf("reminder_test_id_%d", time.Now().UnixNano())
	_, err := controller.client.StartFlow(
		request.Request.Context(),
		workflows.Reminder,
		flowID,
		nil,
		patternStartOptions(),
	)
	respondString(request, fmt.Sprintf("started workflowId: %s", flowID), err)
}

func (controller *designPatternController) acceptReminder(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	var none sdk.None
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		workflows.Reminder.Accept,
		nil,
		&none,
		sdk.InvokeOptions{},
	)
	respondString(request, "accepted", err)
}

func (controller *designPatternController) optOutReminder(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	err := controller.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		reminders.OptOutReminder,
		nil,
	)
	respondString(request, "done", err)
}

func (controller *designPatternController) createUserProfile(request *gin.Context) {
	profileRequest, found := bindUserProfileRequest(request)
	if !found {
		return
	}
	options, err := entityStoreStartOptions(profileRequest.UserProfile)
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		workflows.UserProfile,
		profileRequest.UserID,
		nil,
		options,
	)
	respondString(request, runID, err)
}

func (controller *designPatternController) updateUserProfile(request *gin.Context) {
	profileRequest, found := bindUserProfileRequest(request)
	if !found {
		return
	}
	var none sdk.None
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		profileRequest.UserID,
		workflows.UserProfile.UpdateProfile,
		profileRequest.UserProfile,
		&none,
		sdk.InvokeOptions{},
	)
	respondString(request, "Updated user profile", err)
}

func (controller *designPatternController) getUserProfile(request *gin.Context) {
	userID, found := requiredQuery(request, "userId")
	if !found {
		return
	}
	var profile entitystore.UserProfile
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		userID,
		workflows.UserProfile.GetProfile,
		nil,
		&profile,
		sdk.InvokeOptions{},
	)
	if err != nil {
		respondString(request, "", err)
		return
	}
	request.JSON(http.StatusOK, profile)
}

func (controller *designPatternController) clearUserProfile(request *gin.Context) {
	userID, found := requiredQuery(request, "userId")
	if !found {
		return
	}
	var none sdk.None
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		userID,
		workflows.UserProfile.ClearProfile,
		nil,
		&none,
		sdk.InvokeOptions{},
	)
	respondString(request, "Cleared user profile", err)
}

func bindUserProfileRequest(request *gin.Context) (entitystore.UserProfileRequest, bool) {
	var profileRequest entitystore.UserProfileRequest
	if err := request.ShouldBindJSON(&profileRequest); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return entitystore.UserProfileRequest{}, false
	}
	if profileRequest.UserID == "" {
		request.JSON(http.StatusBadRequest, gin.H{"error": "userId is required"})
		return entitystore.UserProfileRequest{}, false
	}
	return profileRequest, true
}

func entityStoreStartOptions(profile entitystore.UserProfile) (sdk.StartFlowOptions, error) {
	attributes, err := entitystore.InitialAttributes(profile)
	if err != nil {
		return sdk.StartFlowOptions{}, err
	}
	options := patternStartOptions()
	options.Attributes = attributes
	options.ConfigOverride = &sdk.FlowConfig{
		AttributeStoreName: ptr.Any(entitystore.StoreName),
	}
	return options, nil
}

func (controller *designPatternController) startIntervention(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		workflows.ManualIntervention,
		flowID,
		nil,
		patternStartOptions(),
	)
	respondString(request, runID, err)
}

func (controller *designPatternController) startResettableTimer(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		workflows.ResettableTimer,
		flowID,
		nil,
		patternStartOptions(),
	)
	respondString(request, runID, err)
}

func (controller *designPatternController) resetResettableTimer(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	var none sdk.None
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		workflows.ResettableTimer.SendResetMessage,
		nil,
		&none,
		sdk.InvokeOptions{},
	)
	respondString(request, "reset", err)
}

func (controller *designPatternController) startParallelSimple(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	jobSeeker := parallel.JobSeeker{
		ID:          "123",
		Email:       "jobseeker@indeed.com",
		PhoneNumber: "0987654321",
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		workflows.SimpleParallel,
		flowID,
		jobSeeker,
		patternStartOptions(),
	)
	respondString(request, runID, err)
}

func (controller *designPatternController) startParallelWithAwait(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		workflows.ParallelWithAwait,
		flowID,
		50,
		patternStartOptions(),
	)
	respondString(request, runID, err)
}

func (controller *designPatternController) startRecovery(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	itemName, found := requiredQuery(request, "itemName")
	if !found {
		return
	}
	quantityText, found := requiredQuery(request, "quantity")
	if !found {
		return
	}
	quantity, err := strconv.Atoi(quantityText)
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": "quantity must be an integer"})
		return
	}
	_, err = controller.client.StartFlow(
		request.Request.Context(),
		workflows.FailureRecovery,
		flowID,
		recovery.FailureRecoveryWorkflowInput{
			ItemName:          itemName,
			RequestedQuantity: quantity,
		},
		patternStartOptions(),
	)
	respondString(request, "recovery workflow started", err)
}

func (controller *designPatternController) startScalableParallel(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	numOfChildWorkflows, found := requiredQuery(request, "numOfChildWfs")
	if !found {
		return
	}
	childCount, err := strconv.Atoi(numOfChildWorkflows)
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": "numOfChildWfs must be an integer"})
		return
	}
	_, err = controller.client.StartFlow(
		request.Request.Context(),
		workflows.RequestReceiver,
		flowID,
		childCount,
		idReuseStartOptions(),
	)
	respondString(request, "success", err)
}

func (controller *designPatternController) startParentChild(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	numOfChildWorkflows, found := requiredQuery(request, "numOfChildWfs")
	if !found {
		return
	}
	childCount, err := strconv.Atoi(numOfChildWorkflows)
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": "numOfChildWfs must be an integer"})
		return
	}
	_, err = controller.client.StartFlow(
		request.Request.Context(),
		workflows.ParentChild,
		flowID,
		childCount,
		idReuseStartOptions(),
	)
	respondString(request, "success", err)
}

func (controller *designPatternController) startDrainInternal(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		workflows.DrainInternal,
		flowID,
		"",
		patternStartOptions(),
	)
	respondString(request, runID, err)
}

func (controller *designPatternController) startOrSignalDrain(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	err := controller.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		drainsignal.QueueSignalChannel,
		"signal from startorsignal endpoint",
	)
	if err == nil {
		respondString(request, "Signaled the workflow", nil)
		return
	}
	var inactive *sdk.FlowNotActiveError
	if !errors.As(err, &inactive) {
		respondString(request, "", err)
		return
	}
	runID, startErr := controller.client.StartFlow(
		request.Request.Context(),
		workflows.DrainSignal,
		flowID,
		"first message from start",
		patternStartOptions(),
	)
	respondString(
		request,
		fmt.Sprintf("Started the workflow with runId %s", runID),
		startErr,
	)
}

func (controller *designPatternController) startWaitForStateCompletion(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	input := waitforstatecompletion.JobSeekerData{ID: 1}
	_, err := controller.client.StartFlow(
		request.Request.Context(),
		workflows.WaitForStateCompletion,
		flowID,
		input,
		patternStartOptions(),
	)
	if err != nil {
		respondString(request, "", err)
		return
	}
	err = controller.client.WaitForStepCompletion(
		request.Request.Context(),
		flowID,
		sdk.StepExecutionID{StepType: "PersistData"},
		sdk.WaitOptions{Timeout: 5 * time.Minute},
	)
	if err != nil {
		respondString(request, "", err)
		return
	}
	var persistedData waitforstatecompletion.JobSeekerData
	err = controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		workflows.WaitForStateCompletion.GetJobSeekerData,
		nil,
		&persistedData,
		sdk.InvokeOptions{},
	)
	if err != nil {
		respondString(request, "", err)
		return
	}
	serialized, err := json.Marshal(persistedData)
	if err != nil {
		respondString(request, "", err)
		return
	}
	respondString(
		request,
		fmt.Sprintf("success for workflow %s with data %s", flowID, serialized),
		nil,
	)
}

func (controller *designPatternController) startTimeout(request *gin.Context) {
	flowID, found := requiredQuery(request, "workflowId")
	if !found {
		return
	}
	successfulWorkflow, err := strconv.ParseBool(
		request.DefaultQuery("successfulWorkflow", "true"),
	)
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": "successfulWorkflow must be a boolean"})
		return
	}
	_, err = controller.client.StartFlow(
		request.Request.Context(),
		workflows.GracefulTimeout,
		flowID,
		successfulWorkflow,
		patternStartOptions(),
	)
	respondString(request, fmt.Sprintf("success for workflow %s", flowID), err)
}

func respondString(request *gin.Context, value string, err error) {
	if err != nil {
		request.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	request.String(http.StatusOK, value)
}
