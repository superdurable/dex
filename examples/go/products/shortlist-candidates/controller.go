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

package shortlistcandidates

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

type controller struct {
	client        *sdk.Client
	employerOptIn *EmployerOptInFlow
	shortlistFlow *ShortlistFlow
}

func RegisterRoutes(
	router gin.IRouter,
	client *sdk.Client,
	employerOptIn *EmployerOptInFlow,
	shortlistFlow *ShortlistFlow,
) {
	controller := &controller{
		client:        client,
		employerOptIn: employerOptIn,
		shortlistFlow: shortlistFlow,
	}
	group := router.Group("/products/shortlist-candidates")
	group.POST("/opt_in", controller.optIn)
	group.POST("/opt_out", controller.optOut)
	group.GET("/is_opted_in", controller.isOptedIn)
	group.POST("/shortlist", controller.shortlist)
	group.POST("/revoke_shortlist", controller.revokeShortlist)
	group.GET("/email_sent_timestamp", controller.emailSentTimestamp)
}

func (controller *controller) optIn(request *gin.Context) {
	var body map[string]string
	if err := request.ShouldBindJSON(&body); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	employerID := body["employerId"]
	flowID := EmployerOptInFlowID(employerID)
	timeout := time.Hour
	_, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.employerOptIn,
		flowID,
		EmployerOptInInput{EmployerID: employerID},
		sdk.StartFlowOptions{Timeout: &timeout},
	)
	if err != nil {
		var duplicate *sdk.FlowAlreadyStartedError
		if errors.As(err, &duplicate) {
			request.String(
				http.StatusOK,
				fmt.Sprintf("Employer %s has already opted in", employerID),
			)
			return
		}
		httputil.Respond(request, nil, err)
		return
	}
	request.String(http.StatusOK, fmt.Sprintf("Started workflowId: %s", flowID))
}

func (controller *controller) optOut(request *gin.Context) {
	var body map[string]string
	if err := request.ShouldBindJSON(&body); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	employerID := body["employerId"]
	flowID := EmployerOptInFlowID(employerID)
	var none sdk.None
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		controller.employerOptIn.OptOut,
		nil,
		&none,
		sdk.InvokeOptions{},
	)
	if err != nil {
		var inactive *sdk.FlowNotActiveError
		if errors.As(err, &inactive) {
			request.String(
				http.StatusOK,
				fmt.Sprintf("Employer %s is not in the opt-in status", employerID),
			)
			return
		}
		httputil.Respond(request, nil, err)
		return
	}
	request.String(http.StatusOK, fmt.Sprintf("Employer %s has opted out", employerID))
}

func (controller *controller) isOptedIn(request *gin.Context) {
	employerID := request.DefaultQuery("employerId", "test-employer")
	optedIn, err := IsOptedIn(
		request.Request.Context(),
		controller.client,
		controller.employerOptIn,
		employerID,
	)
	httputil.Respond(request, optedIn, err)
}

func (controller *controller) shortlist(request *gin.Context) {
	var body map[string]string
	if err := request.ShouldBindJSON(&body); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	employerID := body["employerId"]
	candidateID := body["candidateId"]
	optedIn, err := IsOptedIn(
		request.Request.Context(),
		controller.client,
		controller.employerOptIn,
		employerID,
	)
	if err != nil {
		httputil.Respond(request, nil, err)
		return
	}
	if !optedIn {
		request.String(
			http.StatusOK,
			fmt.Sprintf("Do nothing for %s because of no opt-in", employerID+"-"+candidateID),
		)
		return
	}
	flowID := ShortlistFlowID(employerID, candidateID)
	timeout := time.Hour
	_, err = controller.client.StartFlow(
		request.Request.Context(),
		controller.shortlistFlow,
		flowID,
		ShortlistInput{
			EmployerID:  employerID,
			CandidateID: candidateID,
		},
		sdk.StartFlowOptions{Timeout: &timeout},
	)
	if err != nil {
		var duplicate *sdk.FlowAlreadyStartedError
		if errors.As(err, &duplicate) {
			request.String(
				http.StatusOK,
				fmt.Sprintf("Already running workflowId: %s", flowID),
			)
			return
		}
		httputil.Respond(request, nil, err)
		return
	}
	request.String(http.StatusOK, fmt.Sprintf("Started workflowId: %s", flowID))
}

func (controller *controller) revokeShortlist(request *gin.Context) {
	var body map[string]string
	if err := request.ShouldBindJSON(&body); err != nil {
		request.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	employerID := body["employerId"]
	candidateID := body["candidateId"]
	flowID := ShortlistFlowID(employerID, candidateID)
	err := controller.client.PublishToChannel(
		request.Request.Context(),
		flowID,
		RevokeShortlist,
		nil,
	)
	if err != nil {
		var inactive *sdk.FlowNotActiveError
		if errors.As(err, &inactive) {
			request.String(
				http.StatusOK,
				fmt.Sprintf("No running workflow to revoke for %s", employerID+"-"+candidateID),
			)
			return
		}
		httputil.Respond(request, nil, err)
		return
	}
	request.String(
		http.StatusOK,
		fmt.Sprintf("Revoked shortlist for %s", employerID+"-"+candidateID),
	)
}

func (controller *controller) emailSentTimestamp(request *gin.Context) {
	employerID := request.DefaultQuery("employerId", "test-employer")
	candidateID := request.DefaultQuery("candidateId", "test-candidate")
	flowID := ShortlistFlowID(employerID, candidateID)
	var timestamp int64
	err := controller.client.InvokeRPC(
		request.Request.Context(),
		flowID,
		controller.shortlistFlow.GetEmailSentTimestamp,
		nil,
		&timestamp,
		sdk.InvokeOptions{},
	)
	if err != nil {
		var inactive *sdk.FlowNotActiveError
		if errors.As(err, &inactive) {
			request.Status(http.StatusNotFound)
			return
		}
		httputil.Respond(request, nil, err)
		return
	}
	httputil.Respond(request, timestamp, nil)
}
