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

package rpc

import (
	"context"
	"fmt"

	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/blobstore"
	"github.com/superdurable/dex/service/common/utils"
	"github.com/superdurable/dex/service/common/workerclient"
)

// InvokeWorkerRpc calls WorkerService.InvokeWorkerRPC using the shared worker pool.
func InvokeWorkerRpc(
	ctx context.Context,
	pool *workerclient.Pool,
	rpcPrep *dexpb.PrepareRpcQueryResponse,
	req *dexpb.InvokeRPCRequest,
	apiMaxSeconds int64,
	blobStore blobstore.BlobStore,
	externalStorageConfig *config.ExternalStorageConfig,
) (*dexpb.InvokeWorkerRPCResponse, error) {

	if !externalStorageConfig.EffectiveLazyLoading() {
		if err := blobstore.HydrateKVs(ctx, rpcPrep.GetAttributes(), blobStore); err != nil {
			return nil, err
		}
		if err := blobstore.HydrateValue(ctx, req.GetInput(), blobStore); err != nil {
			return nil, err
		}
	}

	timeoutSeconds := req.GetTimeoutSeconds()
	var timeoutPtr *int32
	if timeoutSeconds > 0 {
		timeoutPtr = &timeoutSeconds
	}
	rpcCtx, cancel := utils.TrimContextByTimeoutWithCappedDDL(ctx, timeoutPtr, apiMaxSeconds)
	defer cancel()

	client, callCtx, release, err := pool.Acquire(rpcCtx, rpcPrep.GetWorkerTarget())
	if err != nil {
		return nil, err
	}
	defer release()

	channelInfos := rpcPrep.GetChannelInfos()
	if channelInfos == nil {
		channelInfos = map[string]*dexpb.ChannelInfo{}
	}

	workerReq := &dexpb.InvokeWorkerRPCRequest{
		Context: &dexpb.Context{
			FlowId:               req.GetFlowId(),
			RunId:                rpcPrep.GetRunId(),
			FlowStartedTimestamp: rpcPrep.GetFlowStartedTimestamp(),
		},
		FlowType:     rpcPrep.GetFlowType(),
		RpcName:      req.GetRpcName(),
		Input:        req.GetInput(),
		Attributes:   rpcPrep.GetAttributes(),
		ChannelInfos: channelInfos,
	}

	resp, err := client.InvokeWorkerRPC(callCtx, workerReq)
	if err != nil {
		return nil, err
	}

	if err := validateWorkerRpcResponse(resp); err != nil {
		return nil, err
	}
	service.SetFromStepExecutionID(
		resp.GetStepDecision(),
		service.GetFromStepExecutionIdForRPC(req.GetRpcName()),
	)

	if err := blobstore.OffloadLargeAttributeWrites(
		ctx, resp.GetUpsertAttributes(), req.GetFlowId(),
		externalStorageConfig.ThresholdInBytes, blobStore, externalStorageConfig.Enabled,
	); err != nil {
		return nil, err
	}
	if err := blobstore.OffloadLargeValue(
		ctx, resp.GetOutput(), req.GetFlowId(),
		externalStorageConfig.ThresholdInBytes, blobStore, externalStorageConfig.Enabled,
	); err != nil {
		return nil, err
	}

	return resp, nil
}

func validateWorkerRpcResponse(resp *dexpb.InvokeWorkerRPCResponse) error {
	if resp == nil {
		return fmt.Errorf("nil InvokeWorkerRPCResponse")
	}
	if err := workerclient.RejectWorkerBlobIDs(resp.GetOutput()); err != nil {
		return err
	}
	if err := workerclient.RejectWorkerAttributeWriteBlobIDs(resp.GetUpsertAttributes()); err != nil {
		return err
	}
	if err := workerclient.RejectWorkerKVBlobIDs(resp.GetRecordEvents()); err != nil {
		return err
	}
	decision := resp.GetStepDecision()
	if decision == nil {
		return nil
	}
	if decision.GetConditionalClose() != nil {
		return fmt.Errorf("closing flow in RPC is not supported yet")
	}
	for _, step := range decision.GetNextSteps() {
		if step != nil && service.ValidClosingFlowStepType[step.GetStepType()] {
			return fmt.Errorf("closing flow in RPC is not supported yet")
		}
	}
	return nil
}
