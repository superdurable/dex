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

	"github.com/superdurable/iwf/config"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	uclient "github.com/superdurable/iwf/service/client"
	"github.com/superdurable/iwf/service/common/blobstore"
	"github.com/superdurable/iwf/service/common/log"
	"google.golang.org/protobuf/types/known/emptypb"
)

type handler struct {
	iwfpb.UnimplementedFlowServiceServer
	iwfpb.UnimplementedInternalServiceServer

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
	req *iwfpb.StartFlowRequest,
) (*iwfpb.StartFlowResponse, error) {
	return h.svc.StartFlow(ctx, req)
}

func (h *handler) PublishToChannel(
	ctx context.Context,
	req *iwfpb.PublishToChannelRequest,
) (*emptypb.Empty, error) {
	return h.svc.PublishToChannel(ctx, req)
}

func (h *handler) StopFlow(
	ctx context.Context,
	req *iwfpb.StopFlowRequest,
) (*emptypb.Empty, error) {
	return h.svc.StopFlow(ctx, req)
}

func (h *handler) GetAttributes(
	ctx context.Context,
	req *iwfpb.GetAttributesRequest,
) (*iwfpb.GetAttributesResponse, error) {
	return h.svc.GetAttributes(ctx, req)
}

func (h *handler) SetAttributes(
	ctx context.Context,
	req *iwfpb.SetAttributesRequest,
) (*emptypb.Empty, error) {
	return h.svc.SetAttributes(ctx, req)
}

func (h *handler) LoadBlobs(
	ctx context.Context,
	req *iwfpb.LoadBlobsRequest,
) (*iwfpb.LoadBlobsResponse, error) {
	return h.svc.LoadBlobs(ctx, req)
}

func (h *handler) WaitForFlow(
	ctx context.Context,
	req *iwfpb.WaitForFlowRequest,
) (*iwfpb.WaitForFlowResponse, error) {
	return h.svc.WaitForFlow(ctx, req)
}

func (h *handler) SearchFlows(
	ctx context.Context,
	req *iwfpb.SearchFlowsRequest,
) (*iwfpb.SearchFlowsResponse, error) {
	return h.svc.SearchFlows(ctx, req)
}

func (h *handler) ResetFlow(
	ctx context.Context,
	req *iwfpb.ResetFlowRequest,
) (*iwfpb.ResetFlowResponse, error) {
	return h.svc.ResetFlow(ctx, req)
}

func (h *handler) InvokeRPC(
	ctx context.Context,
	req *iwfpb.InvokeRPCRequest,
) (*iwfpb.InvokeRPCResponse, error) {
	return h.svc.InvokeRPC(ctx, req)
}

func (h *handler) SkipTimer(
	ctx context.Context,
	req *iwfpb.SkipTimerRequest,
) (*emptypb.Empty, error) {
	return h.svc.SkipTimer(ctx, req)
}

func (h *handler) UpdateFlowConfig(
	ctx context.Context,
	req *iwfpb.UpdateFlowConfigRequest,
) (*emptypb.Empty, error) {
	return h.svc.UpdateFlowConfig(ctx, req)
}

func (h *handler) WaitForStepCompletion(
	ctx context.Context,
	req *iwfpb.WaitForStepCompletionRequest,
) (*iwfpb.WaitForStepCompletionResponse, error) {
	return h.svc.WaitForStepCompletion(ctx, req)
}

func (h *handler) WaitForAttribute(
	ctx context.Context,
	req *iwfpb.WaitForAttributeRequest,
) (*emptypb.Empty, error) {
	return h.svc.WaitForAttribute(ctx, req)
}

func (h *handler) TriggerContinueAsNew(
	ctx context.Context,
	req *iwfpb.TriggerContinueAsNewRequest,
) (*emptypb.Empty, error) {
	return h.svc.TriggerContinueAsNew(ctx, req)
}

func (h *handler) HealthCheck(
	ctx context.Context,
	req *emptypb.Empty,
) (*iwfpb.HealthInfo, error) {
	return h.svc.HealthCheck(ctx, req)
}

func (h *handler) DumpFlowForContinueAsNew(
	ctx context.Context,
	req *iwfpb.ContinueAsNewDumpRequest,
) (*iwfpb.ContinueAsNewDumpResponse, error) {
	return h.svc.DumpFlowForContinueAsNew(ctx, req)
}
