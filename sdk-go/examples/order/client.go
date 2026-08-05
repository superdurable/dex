// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

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
	return dex.RPCResult[UpdateOrderOutput]{
		Output: UpdateOrderOutput{Status: input.Status},
	}, nil
}

var _ dex.RPC[UpdateOrderInput, UpdateOrderOutput] = Orders.UpdateOrder

func startOrder(
	ctx context.Context,
	client *dex.Client,
	flowID string,
) (string, error) {
	initialStatus, err := dex.InitialAttribute(OrderStatus, "created")
	if err != nil {
		return "", err
	}
	initialQuantity, err := dex.InitialAttributeMapValue(
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
			Attributes: []dex.InitialAttributeDef{
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
			RequestID: ptr.Any("start-order/" + flowID),
		},
	)
}

func publishCommand(
	ctx context.Context,
	client *dex.Client,
	flowID string,
) error {
	return client.PublishToChannel(
		ctx,
		flowID,
		Commands,
		Command{Name: "approve"},
	)
}

func publishOrderCommand(
	ctx context.Context,
	client *dex.Client,
	flowID string,
) error {
	return client.PublishToChannelMap(
		ctx,
		flowID,
		CommandsByOrder,
		flowID,
		Command{Name: "ship"},
	)
}

func invokeUpdateOrder(
	ctx context.Context,
	client *dex.Client,
	flowID string,
) (UpdateOrderOutput, error) {
	var output UpdateOrderOutput
	err := client.InvokeRPC(
		ctx,
		flowID,
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
) (string, bool, error) {
	var status string
	found, err := client.GetAttribute(
		ctx,
		flowID,
		OrderStatus,
		&status,
	)
	return status, found, err
}

func getItemQuantity(
	ctx context.Context,
	client *dex.Client,
	flowID string,
) (int, bool, error) {
	var quantity int
	found, err := client.GetAttributeMap(
		ctx,
		flowID,
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
) error {
	return client.SetAttribute(
		ctx,
		flowID,
		OrderStatus,
		"shipped",
	)
}

func setItemQuantity(
	ctx context.Context,
	client *dex.Client,
	flowID string,
) error {
	return client.SetAttributeMap(
		ctx,
		flowID,
		ItemQuantities,
		"sku-1",
		3,
	)
}

func waitForOrderStatus(
	ctx context.Context,
	client *dex.Client,
	flowID string,
) error {
	return client.WaitForAttributeEqual(
		ctx,
		flowID,
		OrderStatus,
		"shipped",
		dex.WaitOptions{Timeout: time.Minute},
	)
}

func waitForItemQuantity(
	ctx context.Context,
	client *dex.Client,
	flowID string,
) error {
	return client.WaitForAttributeMapEqual(
		ctx,
		flowID,
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
) (map[string]dex.Value, error) {
	return client.GetAttributes(
		ctx,
		flowID,
		OrderStatus,
	)
}

func setOrderAttributes(
	ctx context.Context,
	client *dex.Client,
	flowID string,
) error {
	return client.SetAttributes(
		ctx,
		flowID,
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
) error {
	return client.StopFlow(
		ctx,
		flowID,
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
) (dex.WaitForFlowResult, error) {
	return client.WaitForFlow(
		ctx,
		flowID,
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
		"order-status = 'shipped'",
		100,
		"",
	)
}

func resetOrder(
	ctx context.Context,
	client *dex.Client,
	flowID string,
) (string, error) {
	return client.ResetFlow(
		ctx,
		flowID,
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
) error {
	return client.SkipTimer(
		ctx,
		flowID,
		dex.StepExecutionID{
			StepType: dex.GetFinalStepType(WaitForCommand),
		},
		dex.TimerID{ConditionID: "timeout"},
	)
}

func updateOrderConfig(
	ctx context.Context,
	client *dex.Client,
	flowID string,
) error {
	return client.UpdateFlowConfig(
		ctx,
		flowID,
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
		dex.StepExecutionID{
			StepType: dex.GetFinalStepType(WaitForCommand),
		},
		dex.WaitOptions{Timeout: time.Minute},
	)
}

func continueOrderAsNew(
	ctx context.Context,
	client *dex.Client,
	flowID string,
) error {
	return client.TriggerContinueAsNew(ctx, flowID)
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
