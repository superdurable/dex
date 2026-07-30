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

package api

import (
	"context"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

type ApiService interface {
	StartFlow(context.Context, *dexpb.StartFlowRequest) (*dexpb.StartFlowResponse, error)
	PublishToChannel(context.Context, *dexpb.PublishToChannelRequest) (*emptypb.Empty, error)
	StopFlow(context.Context, *dexpb.StopFlowRequest) (*emptypb.Empty, error)
	GetAttributes(context.Context, *dexpb.GetAttributesRequest) (*dexpb.GetAttributesResponse, error)
	SetAttributes(context.Context, *dexpb.SetAttributesRequest) (*emptypb.Empty, error)
	LoadBlobs(context.Context, *dexpb.LoadBlobsRequest) (*dexpb.LoadBlobsResponse, error)
	WaitForFlow(context.Context, *dexpb.WaitForFlowRequest) (*dexpb.WaitForFlowResponse, error)
	SearchFlows(context.Context, *dexpb.SearchFlowsRequest) (*dexpb.SearchFlowsResponse, error)
	GetFlowSummary(context.Context, *dexpb.GetFlowSummaryRequest) (*dexpb.GetFlowSummaryResponse, error)
	GetHistoryEvents(context.Context, *dexpb.GetHistoryEventsRequest) (*dexpb.GetHistoryEventsResponse, error)
	WaitForHistoryEvent(context.Context, *dexpb.WaitForHistoryEventRequest) (*dexpb.WaitForHistoryEventResponse, error)
	GetFlowState(context.Context, *dexpb.GetFlowStateRequest) (*dexpb.GetFlowStateResponse, error)
	ResetFlow(context.Context, *dexpb.ResetFlowRequest) (*dexpb.ResetFlowResponse, error)
	InvokeRPC(context.Context, *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error)
	SkipTimer(context.Context, *dexpb.SkipTimerRequest) (*emptypb.Empty, error)
	UpdateFlowConfig(context.Context, *dexpb.UpdateFlowConfigRequest) (*emptypb.Empty, error)
	WaitForStepCompletion(
		context.Context,
		*dexpb.WaitForStepCompletionRequest,
	) (*dexpb.WaitForStepCompletionResponse, error)
	WaitForAttribute(context.Context, *dexpb.WaitForAttributeRequest) (*emptypb.Empty, error)
	TriggerContinueAsNew(context.Context, *dexpb.TriggerContinueAsNewRequest) (*emptypb.Empty, error)
	HealthCheck(context.Context, *emptypb.Empty) (*dexpb.HealthInfo, error)
	DumpFlowForContinueAsNew(
		context.Context,
		*dexpb.ContinueAsNewDumpRequest,
	) (*dexpb.ContinueAsNewDumpResponse, error)
	Close()
}
