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

	"github.com/superdurable/iwf/gen/iwfpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

type ApiService interface {
	StartFlow(context.Context, *iwfpb.StartFlowRequest) (*iwfpb.StartFlowResponse, error)
	PublishToChannel(context.Context, *iwfpb.PublishToChannelRequest) (*emptypb.Empty, error)
	StopFlow(context.Context, *iwfpb.StopFlowRequest) (*emptypb.Empty, error)
	GetAttributes(context.Context, *iwfpb.GetAttributesRequest) (*iwfpb.GetAttributesResponse, error)
	SetAttributes(context.Context, *iwfpb.SetAttributesRequest) (*emptypb.Empty, error)
	LoadBlobs(context.Context, *iwfpb.LoadBlobsRequest) (*iwfpb.LoadBlobsResponse, error)
	WaitForFlow(context.Context, *iwfpb.WaitForFlowRequest) (*iwfpb.WaitForFlowResponse, error)
	SearchFlows(context.Context, *iwfpb.SearchFlowsRequest) (*iwfpb.SearchFlowsResponse, error)
	ResetFlow(context.Context, *iwfpb.ResetFlowRequest) (*iwfpb.ResetFlowResponse, error)
	InvokeRPC(context.Context, *iwfpb.InvokeRPCRequest) (*iwfpb.InvokeRPCResponse, error)
	SkipTimer(context.Context, *iwfpb.SkipTimerRequest) (*emptypb.Empty, error)
	UpdateFlowConfig(context.Context, *iwfpb.UpdateFlowConfigRequest) (*emptypb.Empty, error)
	WaitForStepCompletion(
		context.Context,
		*iwfpb.WaitForStepCompletionRequest,
	) (*iwfpb.WaitForStepCompletionResponse, error)
	WaitForAttribute(context.Context, *iwfpb.WaitForAttributeRequest) (*emptypb.Empty, error)
	TriggerContinueAsNew(context.Context, *iwfpb.TriggerContinueAsNewRequest) (*emptypb.Empty, error)
	HealthCheck(context.Context, *emptypb.Empty) (*iwfpb.HealthInfo, error)
	DumpFlowForContinueAsNew(
		context.Context,
		*iwfpb.ContinueAsNewDumpRequest,
	) (*iwfpb.ContinueAsNewDumpResponse, error)
	Close()
}
