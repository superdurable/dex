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
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	"github.com/superdurable/dex/sdk-go/dex"
)

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
	scenario := request.DefaultQuery("scenario", "normal")
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
		request.Request.Context(), controller.flow, flowID, input, dex.StartFlowOptions{},
	)
	httputil.Respond(request, gin.H{"flowID": flowID, "runID": runID}, err)
}

func (controller *controller) decide(request *gin.Context) {
	flowID, found := requiredFlowID(request)
	if !found {
		return
	}
	var rpc func(dex.Context, DecisionReason) (*dex.RPCResult[dex.None], error)
	switch decision := request.Query("decision"); decision {
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
	err := controller.client.InvokeRPC(
		request.Request.Context(), flowID, rpc, DecisionReason{Reason: reason}, nil,
	)
	if err != nil {
		respondDecisionError(request, err)
		return
	}
	httputil.Respond(request, gin.H{"flowID": flowID, "decision": request.Query("decision")}, nil)
}

// respondDecisionError maps a refused decision to 4xx and leaves other failures 500.
func respondDecisionError(request *gin.Context, err error) {
	var refused *dex.WorkerInvocationError
	var lockConflict *dex.RPCLockConflictError
	var unavailable *dex.FlowNotActiveOrNotFoundError
	switch {
	case errors.As(err, &refused) && refused.Worker != nil:
		request.JSON(http.StatusConflict, gin.H{"error": refused.Worker.Detail})
	case errors.As(err, &lockConflict):
		request.JSON(http.StatusConflict, gin.H{"error": "another decision on this refund is in progress"})
	case errors.As(err, &unavailable):
		request.JSON(http.StatusNotFound, gin.H{"error": "no running refund with this Flow ID"})
	default:
		httputil.Respond(request, nil, err)
	}
}

func (controller *controller) describe(request *gin.Context) {
	flowID, found := requiredFlowID(request)
	if !found {
		return
	}
	ctx := request.Request.Context()
	info, err := controller.client.DescribeFlow(ctx, flowID)
	var notFound *dex.FlowNotFoundError
	if errors.As(err, &notFound) {
		request.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		httputil.Respond(request, nil, err)
		return
	}
	var caseStatus, refundID string
	_, err = controller.client.GetAttributes(
		ctx, flowID, dex.ReadAttribute(CaseStatus, &caseStatus), dex.ReadAttribute(RefundID, &refundID),
	)
	httputil.Respond(request, gin.H{
		"flowID":     flowID,
		"status":     info.Status.String(),
		"caseStatus": caseStatus,
		"refundId":   refundID,
	}, err)
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
