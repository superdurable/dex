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

package entityflow

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	"github.com/superdurable/dex/sdk-go/dex"
)

type AccountRequest struct {
	AccountID string `json:"accountId"`
	ContactDetails
}

type CreditAdjustmentRequest struct {
	AccountID string `json:"accountId"`
	CreditAdjustment
}

type AccountView struct {
	AccountID     string `json:"accountId"`
	DisplayName   string `json:"displayName"`
	Email         string `json:"email"`
	CreditBalance int64  `json:"creditBalance"`
}

type controller struct {
	client *dex.Client
	flow   *CustomerAccountFlow
}

func RegisterRoutes(router gin.IRouter, client *dex.Client, flow *CustomerAccountFlow) {
	patternController := &controller{client: client, flow: flow}
	group := router.Group("/patterns/entity-flow")
	group.POST("/account", patternController.openAccount)
	group.POST("/account/contact", patternController.updateContactDetails)
	group.POST("/account/credits", patternController.adjustCreditBalance)
	group.GET("/account", patternController.getAccount)
	group.POST("/account/retire", patternController.retireAccount)
}

func OpenAccountOptions(contact ContactDetails) (dex.StartFlowOptions, error) {
	displayName, err := dex.InitialAttribute(DisplayName, contact.DisplayName)
	if err != nil {
		return dex.StartFlowOptions{}, err
	}
	email, err := dex.InitialAttribute(Email, contact.Email)
	if err != nil {
		return dex.StartFlowOptions{}, err
	}
	creditBalance, err := dex.InitialAttribute(CreditBalance, int64(0))
	if err != nil {
		return dex.StartFlowOptions{}, err
	}
	retired, err := dex.InitialAttribute(Retired, false)
	if err != nil {
		return dex.StartFlowOptions{}, err
	}
	return dex.StartFlowOptions{
		IDReusePolicy: dex.IDReuseDisallow,
		Attributes:    []dex.InitialAttributeDef{displayName, email, creditBalance, retired},
	}, nil
}

func (controller *controller) openAccount(request *gin.Context) {
	accountRequest, found := bindAccountRequest(request)
	if !found {
		return
	}
	options, err := OpenAccountOptions(accountRequest.ContactDetails)
	if err != nil {
		httputil.RespondString(request, "", err)
		return
	}
	flowID := CustomerAccountFlowID(accountRequest.AccountID)
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.flow,
		flowID,
		nil,
		options,
	)
	var alreadyStarted *dex.FlowAlreadyStartedError
	if errors.As(err, &alreadyStarted) {
		request.JSON(http.StatusConflict, gin.H{"error": "account already exists"})
		return
	}
	httputil.Respond(request, gin.H{"flowID": flowID, "runID": runID}, err)
}

func (controller *controller) updateContactDetails(request *gin.Context) {
	accountRequest, found := bindAccountRequest(request)
	if !found {
		return
	}
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		CustomerAccountFlowID(accountRequest.AccountID),
		controller.flow.UpdateContactDetails,
		accountRequest.ContactDetails,
		nil,
	)
	httputil.RespondString(request, "Updated contact details", err)
}

func (controller *controller) adjustCreditBalance(request *gin.Context) {
	var adjustmentRequest CreditAdjustmentRequest
	if err := request.ShouldBindJSON(&adjustmentRequest); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if adjustmentRequest.AccountID == "" {
		request.JSON(http.StatusBadRequest, gin.H{"error": "accountId is required"})
		return
	}
	var balance int64
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		CustomerAccountFlowID(adjustmentRequest.AccountID),
		controller.flow.AdjustCreditBalance,
		adjustmentRequest.CreditAdjustment,
		&balance,
	)
	httputil.Respond(request, gin.H{"creditBalance": balance}, err)
}

func (controller *controller) getAccount(request *gin.Context) {
	accountID, found := httputil.RequiredQuery(request, "accountId")
	if !found {
		return
	}
	account := AccountView{AccountID: accountID}
	_, err := controller.client.GetAttributes(
		request.Request.Context(),
		CustomerAccountFlowID(accountID),
		dex.ReadAttribute(DisplayName, &account.DisplayName),
		dex.ReadAttribute(Email, &account.Email),
		dex.ReadAttribute(CreditBalance, &account.CreditBalance),
	)
	httputil.Respond(request, account, err)
}

func (controller *controller) retireAccount(request *gin.Context) {
	accountID, found := httputil.RequiredQuery(request, "accountId")
	if !found {
		return
	}
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		CustomerAccountFlowID(accountID),
		controller.flow.RetireAccount,
		nil,
		nil,
	)
	httputil.RespondString(request, "Retired account", err)
}

func bindAccountRequest(request *gin.Context) (AccountRequest, bool) {
	var accountRequest AccountRequest
	if err := request.ShouldBindJSON(&accountRequest); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return AccountRequest{}, false
	}
	if accountRequest.AccountID == "" {
		request.JSON(http.StatusBadRequest, gin.H{"error": "accountId is required"})
		return AccountRequest{}, false
	}
	if err := accountRequest.Validate(); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return AccountRequest{}, false
	}
	return accountRequest, true
}
