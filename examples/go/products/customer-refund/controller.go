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

package customerrefund

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/products/customer-refund/agentic"
	"github.com/superdurable/dex/examples/go/products/customer-refund/deterministic"
	refundmodel "github.com/superdurable/dex/examples/go/products/customer-refund/model"
	"github.com/superdurable/dex/examples/go/server/httputil"
	"github.com/superdurable/dex/sdk-go/dex"
)

type startRefundRequest struct {
	FlowID string                 `json:"flowId"`
	Case   refundmodel.RefundCase `json:"case"`
}

type controller struct {
	client            *dex.Client
	deterministicFlow *deterministic.CustomerRefundFlow
	agenticFlow       *agentic.AgenticCustomerRefundFlow
}

func RegisterRoutes(
	router gin.IRouter,
	client *dex.Client,
	deterministicFlow *deterministic.CustomerRefundFlow,
	agenticFlow *agentic.AgenticCustomerRefundFlow,
) {
	controller := &controller{
		client: client, deterministicFlow: deterministicFlow, agenticFlow: agenticFlow,
	}
	group := router.Group("/products/customer-refund")
	group.POST("/deterministic/start", controller.startDeterministic)
	group.POST("/agentic/start", controller.startAgentic)
}

func (controller *controller) startDeterministic(request *gin.Context) {
	controller.start(request, controller.deterministicFlow)
}

func (controller *controller) startAgentic(request *gin.Context) {
	controller.start(request, controller.agenticFlow)
}

func (controller *controller) start(request *gin.Context, flow dex.Flow) {
	var body startRefundRequest
	if err := request.ShouldBindJSON(&body); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	flowID := body.FlowID
	if flowID == "" {
		flowID = httputil.NewFlowID("customer-refund")
	}
	if body.Case.CaseID == "" {
		body.Case.CaseID = flowID
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(), flow, flowID, body.Case, dex.StartFlowOptions{},
	)
	if err != nil {
		httputil.Respond(request, nil, err)
		return
	}
	httputil.Respond(request, gin.H{"flowID": flowID, "runID": runID}, nil)
}
