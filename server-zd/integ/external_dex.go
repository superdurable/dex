// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"

	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type externalDexFlowClient struct {
	dexpb.FlowServiceClient
	maxWaitSeconds int32
}

var localDexDevTestAttributeIndexes = map[string]dexpb.IndexType{
	"CustomKeywordField":      dexpb.IndexType_INDEX_TYPE_KEYWORD,
	"CustomKeywordArrayField": dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY,
	"CustomIntField":          dexpb.IndexType_INDEX_TYPE_INT,
	"CustomBoolField":         dexpb.IndexType_INDEX_TYPE_BOOL,
	"CustomDoubleField":       dexpb.IndexType_INDEX_TYPE_DOUBLE,
	"CustomDatetimeField":     dexpb.IndexType_INDEX_TYPE_DATETIME,
	"CustomTextField":         dexpb.IndexType_INDEX_TYPE_TEXT,
}

func (client *externalDexFlowClient) StartFlow(
	ctx context.Context,
	request *dexpb.StartFlowRequest,
	options ...grpc.CallOption,
) (*dexpb.StartFlowResponse, error) {
	if request.FlowStartOptions == nil {
		request.FlowStartOptions = &dexpb.FlowStartOptions{}
	}
	if request.FlowStartOptions.FlowConfigOverride == nil {
		request.FlowStartOptions.FlowConfigOverride = syncDurabilityConfig()
	} else if request.FlowStartOptions.FlowConfigOverride.StepDurability == nil {
		request.FlowStartOptions.FlowConfigOverride.StepDurability = ptr.Any(
			dexpb.StepDurability_STEP_DURABILITY_SYNC,
		)
	}
	return client.FlowServiceClient.StartFlow(ctx, request, options...)
}

func (client *externalDexFlowClient) WaitForFlow(
	ctx context.Context,
	request *dexpb.WaitForFlowRequest,
	options ...grpc.CallOption,
) (*dexpb.FlowResult, error) {
	if request.WaitTimeSeconds == 0 || request.WaitTimeSeconds > client.maxWaitSeconds {
		request.WaitTimeSeconds = client.maxWaitSeconds
	}
	return client.FlowServiceClient.WaitForFlow(ctx, request, options...)
}

func (client *externalDexFlowClient) WaitForStepCompletion(
	ctx context.Context,
	request *dexpb.WaitForStepCompletionRequest,
	options ...grpc.CallOption,
) (*dexpb.WaitForStepCompletionResponse, error) {
	return client.FlowServiceClient.WaitForStepCompletion(ctx, request, options...)
}

func (client *externalDexFlowClient) WaitForAttribute(
	ctx context.Context,
	request *dexpb.WaitForAttributeRequest,
	options ...grpc.CallOption,
) (*dexpb.WaitForAttributeResponse, error) {
	return client.FlowServiceClient.WaitForAttribute(ctx, request, options...)
}

func newDexClientConnection(address string, maxMessageBytes int) (*grpc.ClientConn, error) {
	return grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxMessageBytes),
			grpc.MaxCallSendMsgSize(maxMessageBytes),
		),
	)
}
