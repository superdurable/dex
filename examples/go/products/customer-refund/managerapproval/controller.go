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

package managerapproval

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	"github.com/superdurable/dex/sdk-go/dex"
)

const refundNotFoundMessage = "no refund with this Flow ID"

type controller struct {
	client *dex.Client
	flow   *ManagerApprovalRefundFlow
}

// RegisterRoutes serves start, decide, and describe under /products/customer-refund/manager-approval.
func RegisterRoutes(router gin.IRouter, client *dex.Client, flow *ManagerApprovalRefundFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/products/customer-refund/manager-approval")
	group.GET("/start", controller.start)
	group.GET("/decide", controller.decide)
	group.GET("/describe", controller.describe)
}

func (controller *controller) start(request *gin.Context) {
	scenario := request.Query("scenario")
	if scenario == "" {
		scenario = "normal"
	}
	input, found := ScenarioRequest(scenario)
	if !found {
		request.JSON(http.StatusBadRequest, gin.H{
			"error": "scenario must be one of " + strings.Join(ScenarioNames, ", "),
		})
		return
	}
	flowID := optionalFlowID(request)
	if flowID == "" {
		flowID = httputil.NewFlowID("customer-refund")
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(), controller.flow, flowID, input,
		dex.StartFlowOptions{IDReusePolicy: dex.IDReuseDisallow},
	)
	var alreadyStarted *dex.FlowAlreadyStartedError
	if errors.As(err, &alreadyStarted) {
		request.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		httputil.Respond(request, nil, err)
		return
	}
	waitContext, cancelWait := context.WithTimeout(request.Request.Context(), 10*time.Second)
	defer cancelWait()
	err = controller.client.WaitForStepCompletion(
		waitContext, flowID, dex.StepExecutionID{StepType: dex.GetFinalStepType(CheckPolicy{})},
		dex.WaitForStepCompletionOptions{},
	)
	respondRefundStart(request, flowID, runID, err)
}

func respondRefundStart(request *gin.Context, flowID, runID string, err error) {
	var inactive *dex.FlowNotActiveOrNotFoundError
	var closing *dex.FlowClosingError
	if errors.As(err, &inactive) || errors.As(err, &closing) {
		err = nil
	}
	httputil.Respond(request, gin.H{"flowID": flowID, "runID": runID}, err)
}

func (controller *controller) decide(request *gin.Context) {
	flowID, found := requiredFlowID(request)
	if !found {
		return
	}
	var rpc func(dex.Context, DecisionReason) (*dex.RPCResult[dex.None], error)
	decision := request.Query("decision")
	switch decision {
	case "approve":
		rpc = controller.flow.ApproveRefund
	case "reject":
		rpc = controller.flow.RejectRefund
	default:
		request.JSON(http.StatusBadRequest, gin.H{"error": "decision must be approve or reject"})
		return
	}
	reason := request.Query("reason")
	if strings.TrimSpace(reason) == "" {
		request.JSON(http.StatusBadRequest, gin.H{"error": reasonRequiredMessage})
		return
	}
	if _, found := controller.describeRefund(request, flowID); !found {
		return
	}
	err := controller.client.InvokeRPC(
		request.Request.Context(), flowID, rpc, DecisionReason{Reason: reason}, nil,
	)
	if err != nil {
		respondDecisionError(request, err)
		return
	}
	httputil.Respond(request, gin.H{"flowID": flowID, "decision": decision}, nil)
}

// respondDecisionError maps a refused decision to 4xx and leaves other failures 500.
func respondDecisionError(request *gin.Context, err error) {
	var refused *dex.WorkerInvocationError
	var lockConflict *dex.RPCLockConflictError
	var closing *dex.FlowClosingError
	var unavailable *dex.FlowNotActiveOrNotFoundError
	var notFound *dex.FlowNotFoundError
	switch {
	case errors.As(err, &refused) && IsRefundNotAwaitingManager(refused.Worker):
		request.JSON(http.StatusConflict, gin.H{"error": refused.Worker.Detail})
	case errors.As(err, &lockConflict):
		request.JSON(http.StatusConflict, gin.H{"error": "another decision on this refund is in progress"})
	case errors.As(err, &closing):
		request.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.As(err, &unavailable), errors.As(err, &notFound):
		request.JSON(http.StatusNotFound, gin.H{"error": refundNotFoundMessage})
	default:
		httputil.Respond(request, nil, err)
	}
}

func IsRefundNotAwaitingManager(worker *dex.WorkerError) bool {
	// Go SDK reports *errors.joinError as the type (#1054).
	return worker != nil && worker.Detail == notAwaitingManagerMessage &&
		(worker.Type == notAwaitingManagerErrorType || worker.Type == "*errors.joinError")
}

func (controller *controller) describe(request *gin.Context) {
	flowID, found := requiredFlowID(request)
	if !found {
		return
	}
	info, found := controller.describeRefund(request, flowID)
	if !found {
		return
	}
	var caseStatus, refundID string
	attributes, err := controller.client.GetAttributes(
		request.Request.Context(), flowID, dex.ReadAttribute(CaseStatus, &caseStatus), dex.ReadAttribute(RefundID, &refundID),
	)
	var notFound *dex.FlowNotFoundError
	if errors.As(err, &notFound) {
		request.JSON(http.StatusNotFound, gin.H{"error": refundNotFoundMessage})
		return
	}
	httputil.Respond(request, gin.H{
		"flowID":     flowID,
		"status":     refundStatusName(info.Status),
		"caseStatus": optionalRefundAttribute(caseStatus, attributes.IsSet(CaseStatus)),
		"refundId":   optionalRefundAttribute(refundID, attributes.IsSet(RefundID)),
	}, err)
}

func (controller *controller) describeRefund(request *gin.Context, flowID string) (dex.FlowInfo, bool) {
	info, err := controller.client.DescribeFlow(request.Request.Context(), flowID)
	var notFound *dex.FlowNotFoundError
	switch {
	case errors.As(err, &notFound):
		request.JSON(http.StatusNotFound, gin.H{"error": refundNotFoundMessage})
	case err != nil:
		httputil.Respond(request, nil, err)
	case info.FlowType != dex.GetFinalFlowType(controller.flow):
		request.JSON(http.StatusNotFound, gin.H{"error": refundNotFoundMessage})
	default:
		return info, true
	}
	return dex.FlowInfo{}, false
}

func refundStatusName(status dex.FlowStatus) string {
	switch status {
	case dex.FlowRunning:
		return "running"
	case dex.FlowCompleted:
		return "completed"
	case dex.FlowFailed:
		return "failed"
	case dex.FlowCanceled:
		return "canceled"
	case dex.FlowTerminated:
		return "terminated"
	case dex.FlowServerSideTimeoutInternalOnly:
		return "timeout"
	default:
		return "unknown"
	}
}

func optionalRefundAttribute(value string, isSet bool) any {
	if !isSet {
		return nil
	}
	return value
}

// optionalFlowID reads the Flow ID from flowId, or from its alias workflowId.
func optionalFlowID(request *gin.Context) string {
	if flowID := request.Query("workflowId"); flowID != "" {
		return flowID
	}
	return request.Query("flowId")
}

// requiredFlowID reads the Flow ID from flowId, or from its alias workflowId.
func requiredFlowID(request *gin.Context) (string, bool) {
	if flowID := request.Query("workflowId"); flowID != "" {
		return flowID, true
	}
	return httputil.RequiredQuery(request, "flowId")
}
