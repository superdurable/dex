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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	hardDeleteAttributes "github.com/superdurable/dex/server-zd/integ/workflow/hard-delete-attributes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/prototext"
)

const (
	hardDeleteTransactionalRPCSelector = "transactional"
	hardDeleteSignalRPCSelector        = "signal"
	hardDeleteTransactionalRPCSecret   = hardDeleteAttributes.RPCSecretPrefix + hardDeleteTransactionalRPCSelector
	hardDeleteSignalRPCSecret          = hardDeleteAttributes.RPCSecretPrefix + hardDeleteSignalRPCSelector
	hardDeleteAPISecret                = "api-secret-a71e42"
	hardDeleteAPISecretAfterDelete     = "api-secret-after-delete-b93f05"
	hardDeleteContinuedSecret          = "continued-secret-c08d16"
)

var hardDeleteSecrets = []string{
	hardDeleteAttributes.InitialSecret,
	hardDeleteAttributes.InitialToken,
	hardDeleteAttributes.StepSecret,
	hardDeleteTransactionalRPCSecret,
	hardDeleteSignalRPCSecret,
	hardDeleteAPISecret,
	hardDeleteAPISecretAfterDelete,
	hardDeleteContinuedSecret,
}

func TestHardDeleteAttributesTemporal(t *testing.T) {
	runHardDeleteAttributeMatrix(t)
}

func runHardDeleteAttributeMatrix(t *testing.T) {
	durabilities := []dexpb.StepDurability{
		dexpb.StepDurability_STEP_DURABILITY_SYNC,
		dexpb.StepDurability_STEP_DURABILITY_ASYNC,
	}
	for _, lazyLoading := range []bool{true, false} {
		for _, durability := range durabilities {
			for i := 0; i < *repeatIntegTest; i++ {
				t.Run(fmt.Sprintf("lazy=%v-%v-%d", lazyLoading, durability, i), func(t *testing.T) {
					doTestHardDeleteAttributes(t, lazyLoading, durability)
					smallWaitForFastTest()
				})
			}
		}
	}
}

// doTestHardDeleteAttributes covers every write path, reads in Steps and RPCs, deletion, and history contents.
func doTestHardDeleteAttributes(
	t *testing.T,
	lazyLoading bool,
	durability dexpb.StepDurability,
) {
	runtime := startDexService(t, serverZDTestConfig{

		LazyLoading: ptr.Any(lazyLoading),
	})
	workerHandler := hardDeleteAttributes.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	flowID := hardDeleteAttributes.WorkflowType + "-" + uuid.NewString()
	startOptions := withWorkerTarget(&dexpb.FlowStartOptions{
		Attributes: []*dexpb.AttributeWrite{
			hardDeleteAttributes.HardDeleteStringWrite(hardDeleteAttributes.SecretKey, hardDeleteAttributes.InitialSecret),
			hardDeleteAttributes.HardDeleteJSONWrite(hardDeleteAttributes.TokenKey, `"`+hardDeleteAttributes.InitialToken+`"`),
			{Key: hardDeleteAttributes.PlainKey, Value: stringValue(hardDeleteAttributes.PlainAttribute)},
		},
	}, workerTarget)
	startOptions.FlowConfigOverride.StepDurability = ptr.Any(durability)
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           hardDeleteAttributes.WorkflowType,
		FlowTimeoutSeconds: 100,
		StartStepType:      hardDeleteAttributes.State1,
		StepOptions:        &dexpb.StepOptions{SkipWaitFor: true, ExecuteLoadAttributeMapInstances: []string{hardDeleteAttributes.TokenMapName + "/"}},
		FlowStartOptions:   startOptions,
	})
	require.NoError(t, err)

	requireWorkerSawAttributes(t, workerHandler, "S1_execute", map[string]string{
		hardDeleteAttributes.SecretKey: hardDeleteAttributes.InitialSecret,
		hardDeleteAttributes.TokenKey:  `"` + hardDeleteAttributes.InitialToken + `"`,
		hardDeleteAttributes.PlainKey:  hardDeleteAttributes.PlainAttribute,
	})
	requireWorkerSawAttributes(t, workerHandler, "S2_waitFor", map[string]string{
		hardDeleteAttributes.SecretKey: hardDeleteAttributes.StepSecret,
	})
	requireHardDeleteAttribute(t, ctx, flowClient, flowID, hardDeleteAttributes.SecretKey, hardDeleteAttributes.StepSecret)
	// server-zd keeps hard-delete objects in its own object store, not the Go local Blob directory (C4, C5).

	const hardDeleteBlobID = "local-store-id|hd/0123456789"
	_, err = flowClient.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{
		Entries: []*dexpb.LoadBlobRequestEntry{{
			FlowId: flowID,
			BlobValue: &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForStringValue{
				InternalBlobIdForStringValue: hardDeleteBlobID,
			}},
		}},
	})

	// server-zd rejects a Go Blob ID; neither Server lets LoadBlobs read a hard-delete object.
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	for _, isTransactional := range []bool{true, false} {
		selector, nextSecret := hardDeleteSignalRPCSelector, hardDeleteSignalRPCSecret
		if isTransactional {
			selector, nextSecret = hardDeleteTransactionalRPCSelector, hardDeleteTransactionalRPCSecret
		}
		_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
			FlowId:          flowID,
			RpcName:         hardDeleteAttributes.RPCNameRotateSecret,
			Input:           stringValue(selector),
			RequestId:       newRequestID(),
			IsTransactional: isTransactional,
		})
		require.NoError(t, err)
		requireHardDeleteAttribute(t, ctx, flowClient, flowID, hardDeleteAttributes.SecretKey, nextSecret)
	}
	requireWorkerSawAttributes(t, workerHandler, hardDeleteAttributes.RPCNameRotateSecret+"_"+hardDeleteSignalRPCSelector,
		map[string]string{hardDeleteAttributes.SecretKey: hardDeleteTransactionalRPCSecret})

	setHardDeleteAttribute(t, ctx, flowClient, flowID,
		hardDeleteAttributes.HardDeleteStringWrite(hardDeleteAttributes.SecretKey, hardDeleteAPISecret))
	requireHardDeleteAttribute(t, ctx, flowClient, flowID, hardDeleteAttributes.SecretKey, hardDeleteAPISecret)

	publishToHardDeleteChannel(t, ctx, flowClient, flowID, hardDeleteAttributes.ProceedChannelName)
	requireWorkerSawAttributes(t, workerHandler, "S2_execute", map[string]string{
		hardDeleteAttributes.SecretKey: hardDeleteAPISecret,
		hardDeleteAttributes.TokenKey:  `"` + hardDeleteAttributes.InitialToken + `"`,
	})
	requireWorkerSawAttributes(t, workerHandler, "S3_waitFor", map[string]string{
		hardDeleteAttributes.SecretKey: hardDeleteAttributes.AbsentAttribute,
		hardDeleteAttributes.TokenKey:  hardDeleteAttributes.AbsentAttribute,
		hardDeleteAttributes.PlainKey:  hardDeleteAttributes.PlainAttribute,
	})
	requireHardDeleteAttributeAbsent(t, ctx, flowClient, flowID, hardDeleteAttributes.SecretKey)

	setHardDeleteAttribute(t, ctx, flowClient, flowID,
		hardDeleteAttributes.HardDeleteStringWrite(hardDeleteAttributes.SecretKey, hardDeleteAPISecretAfterDelete))
	requireHardDeleteAttribute(t, ctx, flowClient, flowID, hardDeleteAttributes.SecretKey, hardDeleteAPISecretAfterDelete)
	setHardDeleteAttribute(t, ctx, flowClient, flowID, hardDeleteAttributes.HardDeleteNullWrite(hardDeleteAttributes.SecretKey))
	requireHardDeleteAttributeAbsent(t, ctx, flowClient, flowID, hardDeleteAttributes.SecretKey)

	setHardDeleteAttribute(t, ctx, flowClient, flowID,
		hardDeleteAttributes.HardDeleteStringWrite(hardDeleteAttributes.SecretKey, hardDeleteContinuedSecret))
	requireHardDeleteAttribute(t, ctx, flowClient, flowID, hardDeleteAttributes.SecretKey, hardDeleteContinuedSecret)
	// server-zd never continues a run as new (C3).

	requireHardDeleteAttribute(t, ctx, flowClient, flowID, hardDeleteAttributes.SecretKey, hardDeleteContinuedSecret)
	setHardDeleteAttribute(t, ctx, flowClient, flowID, hardDeleteAttributes.HardDeleteNullWrite(hardDeleteAttributes.SecretKey))
	requireHardDeleteAttributeAbsent(t, ctx, flowClient, flowID, hardDeleteAttributes.SecretKey)

	publishToHardDeleteChannel(t, ctx, flowClient, flowID, hardDeleteAttributes.CompletionChannelName)
	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	requireWorkerSawAttributes(t, workerHandler, "S3_execute", map[string]string{
		hardDeleteAttributes.SecretKey: hardDeleteAttributes.AbsentAttribute,
		hardDeleteAttributes.PlainKey:  hardDeleteAttributes.PlainAttribute,
	})

	historyText := flowHistoryText(t, ctx, runtime, flowID)
	require.Contains(t, historyText, hardDeleteAttributes.PlainAttribute, "history text must expose inline payloads")
	for _, secret := range hardDeleteSecrets {
		require.NotContains(t, historyText, secret, "history must not store hard-delete Attribute values")
	}
}

func TestHardDeleteAttributeResetTemporal(t *testing.T) {
	doTestHardDeleteAttributeReset(t)
}

// doTestHardDeleteAttributeReset verifies a reset before the deletion reads the Attribute as absent.
func doTestHardDeleteAttributeReset(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	workerHandler := hardDeleteAttributes.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	flowClient := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	flowID := hardDeleteAttributes.WorkflowType + "-" + uuid.NewString()
	startResponse, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           hardDeleteAttributes.WorkflowType,
		FlowTimeoutSeconds: 100,
		StartStepType:      hardDeleteAttributes.State2,
		StepOptions:        hardDeleteAttributes.StepOptions,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{Attributes: []*dexpb.AttributeWrite{
			hardDeleteAttributes.HardDeleteStringWrite(hardDeleteAttributes.SecretKey, hardDeleteAttributes.InitialSecret),
		}}, workerTarget),
	})
	require.NoError(t, err)
	requireWorkerSawAttributes(t, workerHandler, "S2_waitFor", map[string]string{
		hardDeleteAttributes.SecretKey: hardDeleteAttributes.InitialSecret,
	})
	publishToHardDeleteChannel(t, ctx, flowClient, flowID, hardDeleteAttributes.ProceedChannelName)
	requireWorkerSawAttributes(t, workerHandler, "S3_waitFor", map[string]string{
		hardDeleteAttributes.SecretKey: hardDeleteAttributes.AbsentAttribute,
	})

	_, err = flowClient.ResetFlow(ctx, &dexpb.ResetFlowRequest{
		RunId:     startResponse.GetRunId(),
		FlowId:    flowID,
		ResetType: dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_TYPE,
		StepType:  hardDeleteAttributes.State2,
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		seen, isFound := workerHandler.SeenAttributes("S2_waitFor")
		return isFound && seen[hardDeleteAttributes.SecretKey] == hardDeleteAttributes.AbsentAttribute
	}, 30*time.Second, 100*time.Millisecond, "reset Step must read the deleted Attribute as absent")
	requireHardDeleteAttributeAbsent(t, ctx, flowClient, flowID, hardDeleteAttributes.SecretKey)
}

func TestHardDeleteAttributeSubFlowTemporal(t *testing.T) {
	doTestHardDeleteAttributeSubFlow(t)
}

// doTestHardDeleteAttributeSubFlow verifies SubFlow initial hard-delete Attributes stay out of both histories.
func doTestHardDeleteAttributeSubFlow(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	workerHandler := hardDeleteAttributes.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	flowClient := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	parentFlowID := hardDeleteAttributes.WorkflowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             parentFlowID,
		FlowType:           hardDeleteAttributes.WorkflowType,
		FlowTimeoutSeconds: 100,
		StartStepType:      hardDeleteAttributes.State0,
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	childFlowID := "SubFlow:" + parentFlowID + "-" + hardDeleteAttributes.State0 + "-1-0"
	requireHardDeleteAttribute(t, ctx, flowClient, childFlowID, hardDeleteAttributes.SecretKey, hardDeleteAttributes.SubFlowSecret)

	publishToHardDeleteChannel(t, ctx, flowClient, childFlowID, hardDeleteAttributes.ProceedChannelName)
	requireHardDeleteAttributeAbsent(t, ctx, flowClient, childFlowID, hardDeleteAttributes.SecretKey)
	publishToHardDeleteChannel(t, ctx, flowClient, childFlowID, hardDeleteAttributes.CompletionChannelName)
	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: parentFlowID})
	require.NoError(t, err)

	for _, flowID := range []string{parentFlowID, childFlowID} {
		summary, err := flowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
		require.NoError(t, err)
		events, _ := getAllWebHistoryEvents(t, ctx, flowClient, flowID, summary.GetFlowExecutionId().GetRunId())
		require.NotEmpty(t, events)
		for _, event := range events {
			require.NotContains(t, event.String(), hardDeleteAttributes.SubFlowSecret)
		}

	}
}

func TestHardDeleteAttributeValidationTemporal(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	invalidWrites := map[string]*dexpb.AttributeWrite{
		"int value": {
			Key:                hardDeleteAttributes.SecretKey,
			Value:              &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: 1}},
			IsHardDeleteOnNull: true,
		},
		"indexed": {
			Key:                hardDeleteAttributes.SecretKey,
			Value:              stringValue(hardDeleteAttributes.InitialSecret),
			IndexConfig:        &dexpb.IndexConfig{Enable: true, Type: dexpb.IndexType_INDEX_TYPE_KEYWORD},
			IsHardDeleteOnNull: true,
		},
		"synced": {
			Key:                hardDeleteAttributes.SecretKey,
			Value:              stringValue(hardDeleteAttributes.InitialSecret),
			SyncConfig:         &dexpb.AttributeSyncConfig{Enabled: true},
			IsHardDeleteOnNull: true,
		},
	}
	for name, write := range invalidWrites {
		t.Run(name, func(t *testing.T) {
			_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
				RequestId:        newRequestID(),
				FlowId:           hardDeleteAttributes.WorkflowType + "-" + uuid.NewString(),
				FlowType:         hardDeleteAttributes.WorkflowType,
				StartStepType:    hardDeleteAttributes.State1,
				FlowStartOptions: &dexpb.FlowStartOptions{Attributes: []*dexpb.AttributeWrite{write}},
			})
			require.Equal(t, codes.InvalidArgument, status.Code(err), err)
		})
	}
}

func requireWorkerSawAttributes(
	t *testing.T,
	workerHandler interface {
		SeenAttributes(string) (map[string]string, bool)
	},
	invocation string,
	expected map[string]string,
) {
	t.Helper()
	var seen map[string]string
	require.Eventually(t, func() bool {
		var isFound bool
		seen, isFound = workerHandler.SeenAttributes(invocation)
		return isFound
	}, 30*time.Second, 100*time.Millisecond, "Worker never received %s", invocation)
	for key, value := range expected {
		require.Equal(t, value, seen[key], "%s Attribute %q", invocation, key)
	}
}

func requireHardDeleteAttribute(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	key string,
	expected string,
) {
	t.Helper()
	var lastValue *dexpb.Value
	require.Eventually(t, func() bool {
		response, err := flowClient.GetAttributes(ctx, &dexpb.GetAttributesRequest{FlowId: flowID, Keys: []string{key}})
		if err != nil {
			return false
		}
		lastValue = attributeMap(response.GetAttributes())[key]
		return lastValue.GetStringValue() == expected
	}, 30*time.Second, 100*time.Millisecond, "Attribute %q never became %q; last value %v", key, expected, lastValue)
}

func requireHardDeleteAttributeAbsent(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	key string,
) {
	t.Helper()
	require.Eventually(t, func() bool {
		response, err := flowClient.GetAttributes(ctx, &dexpb.GetAttributesRequest{FlowId: flowID, Keys: []string{key}})
		if err != nil {
			return false
		}
		_, isPresent := attributeMap(response.GetAttributes())[key]
		return !isPresent
	}, 30*time.Second, 100*time.Millisecond, "Attribute %q was never deleted", key)
}

func setHardDeleteAttribute(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	write *dexpb.AttributeWrite,
) {
	t.Helper()
	_, err := flowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		RequestId:  newRequestID(),
		FlowId:     flowID,
		Attributes: []*dexpb.AttributeWrite{write},
	})
	require.NoError(t, err)
}

func publishToHardDeleteChannel(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	channelName string,
) {
	t.Helper()
	_, err := flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId:   flowID,
		Messages: []*dexpb.ChannelMessage{{ChannelName: channelName, Value: stringValue("go")}},
	})
	require.NoError(t, err)
}

// flowHistoryText renders every run of a Flow so tests can search for leaked payload bytes.
func flowHistoryText(t *testing.T, ctx context.Context, runtime *integRuntime, flowID string) string {
	t.Helper()

	return dexHistoryText(t, ctx, runtime.FlowClient, flowID)

}

// dexHistoryText renders the Dex history events of the current run, which is the only run on server-zd.
func dexHistoryText(t *testing.T, ctx context.Context, flowClient dexpb.FlowServiceClient, flowID string) string {
	t.Helper()
	summary, err := flowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
	require.NoError(t, err)
	var builder strings.Builder
	var pageToken []byte
	for {
		response, err := flowClient.GetHistoryEvents(ctx, &dexpb.GetHistoryEventsRequest{
			FlowId: flowID, RunId: summary.GetFlowExecutionId().GetRunId(), NextPageToken: pageToken,
		})
		require.NoError(t, err)
		for _, event := range response.GetEvents() {
			builder.WriteString(prototext.Format(event))
		}
		pageToken = response.GetNextPageToken()
		if len(pageToken) == 0 {
			return builder.String()
		}
	}
}
