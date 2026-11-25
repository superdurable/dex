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

package failfast

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client *sdk.Client
	flow   *ExpenseReimbursementFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *ExpenseReimbursementFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/patterns/fail-fast")
	group.GET("/start", controller.startExpenseReimbursement)
	group.GET("/get-outcome", controller.getPayoutOutcome)
}

func (controller *controller) startExpenseReimbursement(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	amountText, found := httputil.RequiredQuery(request, "amountCents")
	if !found {
		return
	}
	amountCents, err := strconv.ParseInt(amountText, 10, 64)
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": "amountCents must be an integer"})
		return
	}
	bankAccount, found := httputil.RequiredQuery(request, "bankAccount")
	if !found {
		return
	}
	timeout := time.Hour
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.flow,
		flowID,
		ExpenseClaim{AmountCents: amountCents, BankAccount: bankAccount},
		sdk.StartFlowOptions{Timeout: &timeout},
	)
	httputil.RespondString(request, runID, err)
}

func (controller *controller) getPayoutOutcome(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	var outcome PayoutOutcome
	values, err := controller.client.GetAttributes(
		request.Request.Context(), flowID, sdk.ReadAttribute(PayoutOutcomeAttribute, &outcome),
	)
	if err == nil && !values.IsSet(PayoutOutcomeAttribute) {
		request.JSON(http.StatusNotFound, gin.H{"error": "the claim has no outcome yet"})
		return
	}
	httputil.Respond(request, outcome, err)
}
