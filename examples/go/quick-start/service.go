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

package quickstart

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// StartOrder starts the order's Flow once, however often the request is retried.
// It returns after Charge completes.
func StartOrder(ctx context.Context, client *dex.Client, flow *OrderFlow, orderID string) error {
	requestID := "start-" + orderID
	options := dex.StartFlowOptions{
		RequestID:      &requestID,
		AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true},
	}
	if _, err := client.StartFlow(ctx, flow, orderID, orderID, options); err != nil {
		return err
	}
	return client.WaitForStepCompletion(ctx, orderID, dex.StepExecutionID{StepType: "Charge"},
		dex.WaitForStepCompletionOptions{RequestTimeout: 30 * time.Second})
}

// NewOrderHandler serves POST /orders with a JSON body such as {"orderId": "order-1"}.
func NewOrderHandler(client *dex.Client, flow *OrderFlow) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /orders", func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			OrderID string `json:"orderId"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.OrderID == "" {
			http.Error(writer, "orderId is required", http.StatusBadRequest)
			return
		}
		err := StartOrder(request.Context(), client, flow, body.OrderID)
		var alreadyStarted *dex.FlowAlreadyStartedError
		switch {
		case errors.As(err, &alreadyStarted):
			http.Error(writer, "order "+body.OrderID+" was already started", http.StatusConflict)
		case err != nil:
			http.Error(writer, err.Error(), http.StatusInternalServerError)
		default:
			writer.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(writer).Encode(map[string]any{"flowId": body.OrderID, "charged": true}); err != nil {
				http.Error(writer, err.Error(), http.StatusInternalServerError)
			}
		}
	})
	return mux
}
