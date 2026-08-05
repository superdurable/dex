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
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/workflows"
	"github.com/superdurable/dex/examples/go/workflows/moneytransfer"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type moneyTransferController struct {
	client *sdk.Client
}

func newMoneyTransferController(client *sdk.Client) *moneyTransferController {
	return &moneyTransferController{client: client}
}

func (controller *moneyTransferController) registerRoutes(router *gin.Engine) {
	router.GET("/moneytransfer/start", controller.start)
}

func (controller *moneyTransferController) start(request *gin.Context) {
	amount, err := strconv.Atoi(request.Query("amount"))
	if err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": "amount must be an integer"})
		return
	}
	flowID := newFlowID("money-transfer")
	input := moneytransfer.TransferRequest{
		FromAccount: request.Query("fromAccount"),
		ToAccount:   request.Query("toAccount"),
		Amount:      amount,
		Notes:       request.Query("notes"),
	}
	startFlow(request, controller.client, workflows.MoneyTransfer, flowID, input)
}
