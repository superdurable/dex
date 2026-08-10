// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

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
	pool *workerclient.WorkerClientPool,
	rpcPrep *dexpb.PrepareRpcQueryResponse,
	req *dexpb.InvokeRPCRequest,
	apiMaxSeconds int64,
	blobStore blobstore.BlobStore,
	invocationId string,
	blobStoreCfg *config.BlobStoreConfig,
) (*dexpb.InvokeWorkerRPCResponse, error) {

	if !blobStoreCfg.EffectiveLazyLoading() {
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

	client, callCtx, release, err := pool.Acquire(
		rpcCtx,
		rpcPrep.GetWorkerTarget(),
		req.GetFlowId(),
	)
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
		ctx, resp.GetUpsertAttributes(), req.GetFlowId(), invocationId,
		blobStoreCfg.ThresholdInBytes, blobStore, blobStoreCfg.Enabled,
	); err != nil {
		return nil, err
	}
	if err := blobstore.OffloadLargeValue(
		ctx, resp.GetOutput(), req.GetFlowId(), invocationId,
		blobStoreCfg.ThresholdInBytes, blobStore, blobStoreCfg.Enabled,
	); err != nil {
		return nil, err
	}
	if err := offloadRPCSideEffects(
		ctx,
		resp,
		req.GetFlowId(),
		invocationId,
		blobStore,
		blobStoreCfg,
	); err != nil {
		return nil, err
	}

	return resp, nil
}

func offloadRPCSideEffects(
	ctx context.Context,
	response *dexpb.InvokeWorkerRPCResponse,
	flowID string,
	invocationID string,
	blobStore blobstore.BlobStore,
	blobStoreCfg *config.BlobStoreConfig,
) error {
	threshold := blobStoreCfg.ThresholdInBytes
	enabled := blobStoreCfg.Enabled
	if err := blobstore.OffloadLargeKVs(
		ctx, response.GetRecordEvents(), flowID, invocationID, threshold, blobStore, enabled,
	); err != nil {
		return err
	}
	if err := blobstore.OffloadLargeChannelMessages(
		ctx, response.GetPublishToChannel(), flowID, invocationID, threshold, blobStore, enabled,
	); err != nil {
		return err
	}
	for _, movement := range response.GetStepDecision().GetNextSteps() {
		if err := blobstore.OffloadLargeValue(
			ctx, movement.GetStepInput(), flowID, invocationID, threshold, blobStore, enabled,
		); err != nil {
			return err
		}
	}
	closeDecision := response.GetStepDecision().GetCloseDecision()
	if closeDecision.GetCloseDecisionType() == dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL {
		return nil
	}
	return blobstore.OffloadLargeValue(
		ctx, closeDecision.GetCloseInput(), flowID, invocationID, threshold, blobStore, enabled,
	)
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
	if decision.GetCloseDecision() != nil {
		return fmt.Errorf("closing flow in RPC is not supported yet")
	}
	return nil
}
