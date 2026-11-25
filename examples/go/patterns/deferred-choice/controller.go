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

package deferredchoice

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/superdurable/dex/examples/go/server/httputil"
	sdk "github.com/superdurable/dex/sdk-go/dex"
)

const (
	responseLockConflictAttempts = 5
	responseLockConflictBackoff  = 200 * time.Millisecond
)

type controller struct {
	client *sdk.Client
	flow   *OfferResponseFlow
}

func RegisterRoutes(router gin.IRouter, client *sdk.Client, flow *OfferResponseFlow) {
	controller := &controller{client: client, flow: flow}
	group := router.Group("/patterns/deferred-choice")
	group.GET("/start", controller.start)
	group.GET("/accept", controller.acceptCustomerOffer)
	group.GET("/reject", controller.rejectCustomerOffer)
}

func (controller *controller) start(request *gin.Context) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	offerID, found := httputil.RequiredQuery(request, "offerId")
	if !found {
		return
	}
	runID, err := controller.client.StartFlow(
		request.Request.Context(),
		controller.flow,
		flowID,
		offerID,
		sdk.StartFlowOptions{},
	)
	httputil.RespondString(request, runID, err)
}

func (controller *controller) acceptCustomerOffer(request *gin.Context) {
	controller.sendCustomerResponse(request, controller.flow.AcceptCustomerOffer)
}

func (controller *controller) rejectCustomerOffer(request *gin.Context) {
	controller.sendCustomerResponse(request, controller.flow.RejectCustomerOffer)
}

// sendCustomerResponse answers 409 when the response window is already closed.
func (controller *controller) sendCustomerResponse(request *gin.Context, rpc any) {
	flowID, found := httputil.RequiredQuery(request, "workflowId")
	if !found {
		return
	}
	note, found := httputil.RequiredQuery(request, "note")
	if !found {
		return
	}
	ctx := request.Request.Context()
	outcome, err := invokeResponseRPC(ctx, controller.client, flowID, rpc, note)
	if (err == nil && outcome == OfferResponseWindowClosed) || isClosedOffer(ctx, controller.client, flowID, err) {
		request.String(http.StatusConflict, "offer response window closed")
		return
	}
	httputil.RespondString(request, "offer response recorded", err)
}

// isClosedOffer reports whether err rejected the reply because the offer Flow already closed.
// Dex rejects an RPC to a closed Flow before the handler runs, so the retained Flow tells closed from never started.
func isClosedOffer(ctx context.Context, client *sdk.Client, flowID string, err error) bool {
	var closing *sdk.FlowClosingError
	if errors.As(err, &closing) {
		return true
	}
	var notActive *sdk.FlowNotActiveOrNotFoundError
	var notFound *sdk.FlowNotFoundError
	if !errors.As(err, &notActive) && !errors.As(err, &notFound) {
		return false
	}
	info, describeErr := client.DescribeFlow(ctx, flowID)
	return describeErr == nil && info.Status != sdk.FlowRunning
}

// invokeResponseRPC retries a lock conflict with bounded backoff. Retrying is safe:
// a conflicting call committed nothing.
// It returns the RPC outcome, or the last error.
func invokeResponseRPC(ctx context.Context, client *sdk.Client, flowID string, rpc any, note string) (string, error) {
	var outcome string
	var err error
	for attempt := 1; attempt <= responseLockConflictAttempts; attempt++ {
		err = client.InvokeRPC(ctx, flowID, rpc, note, &outcome)
		var lockConflict *sdk.RPCLockConflictError
		if !errors.As(err, &lockConflict) || attempt == responseLockConflictAttempts {
			return outcome, err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(attempt) * responseLockConflictBackoff):
		}
	}
	return outcome, err
}
