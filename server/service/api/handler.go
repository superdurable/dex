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

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	uclient "github.com/superdurable/dex/service/client"
	"github.com/superdurable/dex/service/common/blobstore"
	"github.com/superdurable/dex/service/common/log"
	"google.golang.org/protobuf/types/known/emptypb"
)

// DumpFlowForContinueAsNewHeaderObserver is set by integration tests to capture
// incoming metadata on InternalService dump calls.
var DumpFlowForContinueAsNewHeaderObserver func(context.Context)

type handler struct {
	dexpb.UnimplementedFlowServiceServer
	dexpb.UnimplementedInternalServiceServer

	svc    ApiService
	logger log.Logger
}

func newHandler(
	apiCfg *config.ApiConfig,
	extStore *config.ExternalStorageConfig,
	interpreterCfg *config.Interpreter,
	client uclient.UnifiedClient,
	logger log.Logger,
	store blobstore.BlobStore,
) *handler {
	svc, err := NewApiService(
		apiCfg,
		extStore,
		interpreterCfg,
		client,
		service.TaskQueue,
		logger,
		store,
	)
	if err != nil {
		panic(err)
	}
	return &handler{
		svc:    svc,
		logger: logger,
	}
}

func (h *handler) close() {
	h.svc.Close()
}

func (h *handler) StartFlow(
	ctx context.Context,
	req *dexpb.StartFlowRequest,
) (*dexpb.StartFlowResponse, error) {
	return h.svc.StartFlow(ctx, req)
}

func (h *handler) PublishToChannel(
	ctx context.Context,
	req *dexpb.PublishToChannelRequest,
) (*emptypb.Empty, error) {
	return h.svc.PublishToChannel(ctx, req)
}

func (h *handler) StopFlow(
	ctx context.Context,
	req *dexpb.StopFlowRequest,
) (*emptypb.Empty, error) {
	return h.svc.StopFlow(ctx, req)
}

func (h *handler) GetAttributes(
	ctx context.Context,
	req *dexpb.GetAttributesRequest,
) (*dexpb.GetAttributesResponse, error) {
	return h.svc.GetAttributes(ctx, req)
}

func (h *handler) SetAttributes(
	ctx context.Context,
	req *dexpb.SetAttributesRequest,
) (*emptypb.Empty, error) {
	return h.svc.SetAttributes(ctx, req)
}

func (h *handler) LoadBlobs(
	ctx context.Context,
	req *dexpb.LoadBlobsRequest,
) (*dexpb.LoadBlobsResponse, error) {
	return h.svc.LoadBlobs(ctx, req)
}

func (h *handler) WaitForFlow(
	ctx context.Context,
	req *dexpb.WaitForFlowRequest,
) (*dexpb.WaitForFlowResponse, error) {
	return h.svc.WaitForFlow(ctx, req)
}

func (h *handler) SearchFlows(
	ctx context.Context,
	req *dexpb.SearchFlowsRequest,
) (*dexpb.SearchFlowsResponse, error) {
	return h.svc.SearchFlows(ctx, req)
}

func (h *handler) ResetFlow(
	ctx context.Context,
	req *dexpb.ResetFlowRequest,
) (*dexpb.ResetFlowResponse, error) {
	return h.svc.ResetFlow(ctx, req)
}

func (h *handler) InvokeRPC(
	ctx context.Context,
	req *dexpb.InvokeRPCRequest,
) (*dexpb.InvokeRPCResponse, error) {
	return h.svc.InvokeRPC(ctx, req)
}

func (h *handler) SkipTimer(
	ctx context.Context,
	req *dexpb.SkipTimerRequest,
) (*emptypb.Empty, error) {
	return h.svc.SkipTimer(ctx, req)
}

func (h *handler) UpdateFlowConfig(
	ctx context.Context,
	req *dexpb.UpdateFlowConfigRequest,
) (*emptypb.Empty, error) {
	return h.svc.UpdateFlowConfig(ctx, req)
}

func (h *handler) WaitForStepCompletion(
	ctx context.Context,
	req *dexpb.WaitForStepCompletionRequest,
) (*dexpb.WaitForStepCompletionResponse, error) {
	return h.svc.WaitForStepCompletion(ctx, req)
}

func (h *handler) WaitForAttribute(
	ctx context.Context,
	req *dexpb.WaitForAttributeRequest,
) (*emptypb.Empty, error) {
	return h.svc.WaitForAttribute(ctx, req)
}

func (h *handler) TriggerContinueAsNew(
	ctx context.Context,
	req *dexpb.TriggerContinueAsNewRequest,
) (*emptypb.Empty, error) {
	return h.svc.TriggerContinueAsNew(ctx, req)
}

func (h *handler) HealthCheck(
	ctx context.Context,
	req *emptypb.Empty,
) (*dexpb.HealthInfo, error) {
	return h.svc.HealthCheck(ctx, req)
}

func (h *handler) DumpFlowForContinueAsNew(
	ctx context.Context,
	req *dexpb.ContinueAsNewDumpRequest,
) (*dexpb.ContinueAsNewDumpResponse, error) {
	if DumpFlowForContinueAsNewHeaderObserver != nil {
		DumpFlowForContinueAsNewHeaderObserver(ctx)
	}
	return h.svc.DumpFlowForContinueAsNew(ctx, req)
}
