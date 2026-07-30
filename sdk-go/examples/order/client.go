// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
)

type UpdateOrderInput struct {
	Status string
}

type UpdateOrderOutput struct {
	Status string
}

func (OrderFlow) UpdateOrder(
	ctx dex.Context,
	input UpdateOrderInput,
) (dex.RPCResult[UpdateOrderOutput], error) {
	return dex.Reply(UpdateOrderOutput{Status: input.Status}), nil
}

var _ dex.RPC[UpdateOrderInput, UpdateOrderOutput] = Orders.UpdateOrder

func startOrder(
	ctx context.Context,
	client *dex.Client,
	flowID string,
) (string, error) {
	initialStatus, err := dex.Initial(OrderStatus, "created")
	if err != nil {
		return "", err
	}
	initialQuantity, err := dex.InitialMapValue(
		ItemQuantities,
		"sku-1",
		2,
	)
	if err != nil {
		return "", err
	}

	return client.StartFlow(
		ctx,
		Orders,
		flowID,
		OrderInput{OrderID: flowID},
		dex.StartFlowOptions{
			Timeout:       ptr.Any(24 * time.Hour),
			IDReusePolicy: dex.IDReuseAllowIfPreviousFailed,
			CronSchedule:  "",
			StartDelay:    ptr.Any(5 * time.Second),
			RetryPolicy: &dex.FlowRetryPolicy{
				InitialInterval:    time.Second,
				BackoffCoefficient: 2,
				MaximumInterval:    time.Minute,
				MaximumAttempts:    3,
			},
			Attributes: []dex.InitialAttribute{
				initialStatus,
				initialQuantity,
			},
			ConfigOverride: &dex.FlowConfig{
				ActiveStepSearchMode: ptr.Any(dex.SearchAllActiveSteps),
				StepDurability:       ptr.Any(dex.StepDurabilitySync),
			},
			AlreadyStarted: &dex.AlreadyStartedOptions{
				IgnoreError: true,
			},
		},
	)
}

func publishCommand(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) error {
	return client.PublishToChannel(
		ctx,
		flowID,
		runID,
		Commands,
		Command{Name: "approve"},
	)
}

func publishOrderCommand(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) error {
	return client.PublishToChannelMap(
		ctx,
		flowID,
		runID,
		CommandsByOrder,
		flowID,
		Command{Name: "ship"},
	)
}

func invokeUpdateOrder(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) (UpdateOrderOutput, error) {
	var output UpdateOrderOutput
	err := client.InvokeRPC(
		ctx,
		flowID,
		runID,
		Orders.UpdateOrder,
		UpdateOrderInput{Status: "processing"},
		&output,
		dex.InvokeOptions{
			Timeout: time.Minute,
			LockAttributes: []dex.AttributeLock{
				dex.LockAttribute(OrderStatus),
			},
		},
	)
	return output, err
}

func getOrderStatus(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) (string, bool, error) {
	var status string
	found, err := client.GetAttribute(
		ctx,
		flowID,
		runID,
		OrderStatus,
		&status,
	)
	return status, found, err
}

func getItemQuantity(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) (int, bool, error) {
	var quantity int
	found, err := client.GetAttributeMap(
		ctx,
		flowID,
		runID,
		ItemQuantities,
		"sku-1",
		&quantity,
	)
	return quantity, found, err
}

func setOrderStatus(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) error {
	return client.SetAttribute(
		ctx,
		flowID,
		runID,
		OrderStatus,
		"shipped",
	)
}

func setItemQuantity(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) error {
	return client.SetAttributeMap(
		ctx,
		flowID,
		runID,
		ItemQuantities,
		"sku-1",
		3,
	)
}

func deleteOrderStatus(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) error {
	return client.DeleteAttribute(
		ctx,
		flowID,
		runID,
		OrderStatus,
	)
}

func deleteItemQuantity(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) error {
	return client.DeleteAttributeMap(
		ctx,
		flowID,
		runID,
		ItemQuantities,
		"sku-1",
	)
}

func waitForOrderStatus(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) error {
	return client.WaitForAttributeEqual(
		ctx,
		flowID,
		runID,
		OrderStatus,
		"shipped",
		dex.WaitOptions{Timeout: time.Minute},
	)
}

func waitForItemQuantity(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) error {
	return client.WaitForAttributeMapEqual(
		ctx,
		flowID,
		runID,
		ItemQuantities,
		"sku-1",
		3,
		dex.WaitOptions{Timeout: time.Minute},
	)
}

func getOrderAttributes(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) (map[string]dex.Value, error) {
	return client.GetAttributes(
		ctx,
		flowID,
		runID,
		OrderStatus,
		ItemQuantities,
	)
}

func setOrderAttributes(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) error {
	return client.SetAttributes(
		ctx,
		flowID,
		runID,
		dex.AttributeWrite{
			Name:  OrderStatus.AttributeName(),
			Value: "processing",
			Index: ptr.Any(dex.AttributeIndex{Type: dex.IndexKeyword}),
		},
	)
}

func stopOrder(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) error {
	return client.StopFlow(
		ctx,
		flowID,
		runID,
		dex.StopOptions{
			Type:   dex.CancelFlow,
			Reason: "customer requested cancellation",
		},
	)
}

func waitForOrder(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) (dex.WaitForFlowResult, error) {
	return client.WaitForFlow(
		ctx,
		flowID,
		runID,
		dex.WaitForFlowOptions{
			NeedsResults: true,
			Timeout:      time.Minute,
		},
	)
}

func searchOrders(
	ctx context.Context,
	client *dex.Client,
) (dex.SearchFlowsPage, error) {
	return client.SearchFlows(
		ctx,
		dex.SearchFlowsOptions{
			Query:    "order-status = 'shipped'",
			PageSize: 100,
		},
	)
}

func resetOrder(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) (string, error) {
	return client.ResetFlow(
		ctx,
		flowID,
		runID,
		dex.ResetOptions{
			Type:   dex.ResetToBeginning,
			Reason: "reprocess order",
		},
	)
}

func skipOrderTimer(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) error {
	return client.SkipTimer(
		ctx,
		flowID,
		runID,
		dex.StepExecutionRef{
			StepType:        WaitForCommand.GetStepType(),
			ExecutionNumber: 1,
		},
		dex.TimerRef{ConditionID: "timeout"},
	)
}

func updateOrderConfig(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) error {
	return client.UpdateFlowConfig(
		ctx,
		flowID,
		runID,
		dex.FlowConfig{
			ContinueAsNewThreshold: ptr.Any(int32(100)),
			StepDurability:         ptr.Any(dex.StepDurabilityAsync),
			WorkerTarget: &dex.WorkerTarget{
				Address:  "worker:7234",
				Headless: false,
			},
		},
	)
}

func waitForOrderStep(
	ctx context.Context,
	client *dex.Client,
	flowID string,
) error {
	return client.WaitForStepCompletion(
		ctx,
		flowID,
		dex.StepExecutionRef{
			StepType:        WaitForCommand.GetStepType(),
			ExecutionNumber: 1,
		},
		dex.WaitOptions{Timeout: time.Minute},
	)
}

func continueOrderAsNew(
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) error {
	return client.TriggerContinueAsNew(ctx, flowID, runID)
}

func checkHealth(
	ctx context.Context,
	client *dex.Client,
) (dex.HealthInfo, error) {
	return client.HealthCheck(ctx)
}

func decodeOrderStatus(values map[string]dex.Value) (string, error) {
	value, found := values[OrderStatus.AttributeName()]
	if !found {
		return "", fmt.Errorf("order status is missing")
	}
	var status string
	if err := value.Decode(&status); err != nil {
		return "", err
	}
	return status, nil
}
