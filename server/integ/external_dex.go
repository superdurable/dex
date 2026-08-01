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

package integ

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	temporalapi "github.com/superdurable/dex/service/client/temporal"
	dexconverter "github.com/superdurable/dex/service/common/converter"
	"github.com/superdurable/dex/service/common/ptr"
	enumspb "go.temporal.io/api/enums/v1"
	operatorservicepb "go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

type temporalSearchAttribute struct {
	name          string
	attributeType enumspb.IndexedValueType
}

type externalDexFlowClient struct {
	dexpb.FlowServiceClient
	maxWaitSeconds int32
}

var localDexDevTestSearchAttributes = []temporalSearchAttribute{
	{"CustomKeywordField", enumspb.INDEXED_VALUE_TYPE_KEYWORD},
	{"CustomKeywordArrayField", enumspb.INDEXED_VALUE_TYPE_KEYWORD_LIST},
	{"CustomIntField", enumspb.INDEXED_VALUE_TYPE_INT},
	{"CustomBoolField", enumspb.INDEXED_VALUE_TYPE_BOOL},
	{"CustomDoubleField", enumspb.INDEXED_VALUE_TYPE_DOUBLE},
	{"CustomDatetimeField", enumspb.INDEXED_VALUE_TYPE_DATETIME},
	{"CustomStringField", enumspb.INDEXED_VALUE_TYPE_TEXT},
}

func prepareExternalDex(
	ctx context.Context,
	temporalClient client.Client,
) error {
	if err := ensureTemporalSearchAttributes(ctx, temporalClient); err != nil {
		return err
	}
	return waitForExternalDex(ctx)
}

func ensureTemporalSearchAttributes(ctx context.Context, temporalClient client.Client) error {
	missingAttributes, err := getMissingTemporalSearchAttributes(ctx, temporalClient)
	if err != nil {
		return err
	}
	if len(missingAttributes) == 0 {
		return nil
	}
	_, err = temporalClient.OperatorService().AddSearchAttributes(
		ctx,
		&operatorservicepb.AddSearchAttributesRequest{
			Namespace:        testNamespace,
			SearchAttributes: missingAttributes,
		},
	)
	if err != nil {
		return fmt.Errorf("register Temporal integration search attributes: %w", err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		missingAttributes, err = getMissingTemporalSearchAttributes(ctx, temporalClient)
		if err != nil {
			return err
		}
		if len(missingAttributes) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for Temporal integration search attributes: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func getMissingTemporalSearchAttributes(
	ctx context.Context,
	temporalClient client.Client,
) (map[string]enumspb.IndexedValueType, error) {
	response, err := temporalClient.OperatorService().ListSearchAttributes(
		ctx,
		&operatorservicepb.ListSearchAttributesRequest{Namespace: testNamespace},
	)
	if err != nil {
		return nil, fmt.Errorf("list Temporal search attributes: %w", err)
	}
	missingAttributes := make(map[string]enumspb.IndexedValueType)
	for _, requiredAttribute := range localDexDevTestSearchAttributes {
		attributeType, exists := response.GetCustomAttributes()[requiredAttribute.name]
		if !exists {
			missingAttributes[requiredAttribute.name] = requiredAttribute.attributeType
			continue
		}
		if attributeType != requiredAttribute.attributeType {
			return nil, fmt.Errorf(
				"Temporal search attribute %s must be %s, got %s",
				requiredAttribute.name,
				requiredAttribute.attributeType,
				attributeType,
			)
		}
	}
	return missingAttributes, nil
}

func waitForExternalDex(ctx context.Context) (waitErr error) {
	connection, err := newDexClientConnection(*dexServerAddress, config.DefaultGrpcMaxMessageBytes)
	if err != nil {
		return err
	}
	defer func() {
		waitErr = errors.Join(waitErr, connection.Close())
	}()
	healthClient := healthpb.NewHealthClient(connection)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		checkCtx, cancel := context.WithTimeout(ctx, time.Second)
		response, checkErr := healthClient.Check(checkCtx, &healthpb.HealthCheckRequest{
			Service: dexpb.FlowService_ServiceDesc.ServiceName,
		})
		cancel()
		if checkErr == nil && response.GetStatus() == healthpb.HealthCheckResponse_SERVING {
			return nil
		}
		if checkErr != nil {
			lastErr = checkErr
		} else {
			lastErr = fmt.Errorf("Dex FlowService health status is %s", response.GetStatus())
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for Dex at %s: %w", *dexServerAddress, errors.Join(ctx.Err(), lastErr))
		case <-ticker.C:
		}
	}
}

func connectToExternalDexService(t *testing.T, testConfig DexServiceTestConfig) *integRuntime {
	t.Helper()
	require.Equal(t, service.BackendTypeTemporal, testConfig.BackendType)
	if unsupportedReason := externalDexUnsupportedReason(testConfig); unsupportedReason != "" {
		t.Skipf("requires per-test in-process Dex configuration: %s", unsupportedReason)
	}
	cfg := createTestConfig(testConfig)
	dataConverter := dexconverter.NewTemporalDataConverter()
	temporalClient := createTemporalClient(t, dataConverter)
	unifiedClient := temporalapi.NewTemporalClient(
		temporalClient,
		testNamespace,
		dataConverter,
		false,
		&cfg.Api.QueryWorkflowFailedRetryPolicy,
	)
	t.Cleanup(unifiedClient.Close)
	connection, err := newDexClientConnection(
		*dexServerAddress,
		cfg.Api.EffectiveGrpcMaxMessageBytes(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, connection.Close())
	})
	return &integRuntime{
		FlowClient: &externalDexFlowClient{
			FlowServiceClient: dexpb.NewFlowServiceClient(connection),
			maxWaitSeconds:    int32(cfg.Api.EffectiveMaxWaitSeconds()),
		},
		UnifiedClient: unifiedClient,
		defaultFlowConfig: &dexpb.FlowConfig{
			ContinueAsNewThreshold: ptr.Any(int32(100)),
			StepDurability: ptr.Any(
				dexpb.StepDurability_STEP_DURABILITY_SYNC,
			),
		},
		internalDumpCapture: &internalDumpHeaderCapture{},
	}
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
) (*dexpb.WaitForFlowResponse, error) {
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
	request.WaitTimeSeconds = client.capPositiveWaitSeconds(request.WaitTimeSeconds)
	return client.FlowServiceClient.WaitForStepCompletion(ctx, request, options...)
}

func (client *externalDexFlowClient) WaitForAttribute(
	ctx context.Context,
	request *dexpb.WaitForAttributeRequest,
	options ...grpc.CallOption,
) (*emptypb.Empty, error) {
	request.WaitTimeSeconds = client.capPositiveWaitSeconds(request.WaitTimeSeconds)
	return client.FlowServiceClient.WaitForAttribute(ctx, request, options...)
}

func (client *externalDexFlowClient) capPositiveWaitSeconds(waitSeconds int32) int32 {
	if waitSeconds > client.maxWaitSeconds {
		return client.maxWaitSeconds
	}
	return waitSeconds
}

func externalDexUnsupportedReason(testConfig DexServiceTestConfig) string {
	if testConfig.MemoEncryption {
		return "memo encryption"
	}
	if len(testConfig.DefaultHeaders) > 0 {
		return "default headers"
	}
	if testConfig.S3TestThreshold > 0 || testConfig.LazyLoading != nil {
		return "external storage"
	}
	if *disableStickyCache {
		return "disabled sticky cache"
	}
	return ""
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
