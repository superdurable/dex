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
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/workflows/moneytransfer"
	"net/http"
	"strconv"
	"time"
)

func startMoneyTransferWorkflow(c *gin.Context) {
	fromAccount := c.Query("fromAccount")
	toAccount := c.Query("toAccount")
	amount := c.Query("amount")
	notes := c.Query("notes")

	amountInt, err := strconv.Atoi(amount)
	if err != nil {
		c.JSON(http.StatusBadRequest, "must provide correct amount via URL parameter")
		return
	}

	req := moneytransfer.TransferRequest{
		FromAccount: fromAccount,
		ToAccount:   toAccount,
		Notes:       notes,
		Amount:      amountInt,
	}
	wfId := fmt.Sprintf("money_transfer-%d", time.Now().Unix())

	_, err = client.StartWorkflow(c.Request.Context(), moneytransfer.MoneyTransferWorkflow{}, wfId, 3600, req, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, fmt.Sprintf("workflowId: %v", wfId))
	return
}
