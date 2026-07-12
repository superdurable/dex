// Package ordertracking demonstrates dynamic channels and dynamic state fields.
//
// A merchant has multiple orders. Each order has its own channel for status
// updates (dynamic channel), and order details are stored as individual
// checkpointed entries in a dynamic map field.
package ordertracking

import (
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// --- State ---

type OrderDetail struct {
	ItemName string
	Status   string
}

var (
	keyMerchantID = dex.NewStateKey[string]("MerchantID")
	keyOrders     = dex.NewDynamicStateKey[OrderDetail]("orders/")
)

// --- Dynamic channel: one per order ---

type OrderStatusEvent struct {
	OrderID   string
	NewStatus string
}

// OrderUpdates is a dynamic channel family. Each order gets its own channel
// named "order-update-{orderID}" via OrderUpdates.Condition(orderID).
var OrderUpdates = dex.NewDynamicChannel[OrderStatusEvent]("order-update-")

// --- Flow ---

type TrackingFlow struct {
	dex.DefaultFlowType
}

func (f *TrackingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(keyMerchantID),
		},
		DynamicStateKeys: []dex.StateKeyDef{
			dex.DefineDynamicStateKey(keyOrders),
		},
		Channels: []dex.ChannelDef{
			dex.DefineDynamicChannel(OrderUpdates),
		},
	}
}

func (f *TrackingFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep(&AddOrderStep{}),
		dex.NonStartingStep(&WaitForUpdateStep{}),
	}
}

// --- Step 1: Add order and wait for its first update ---

type StartInput struct {
	MerchantID string
	OrderID    string
	ItemName   string
}

type AddOrderStep struct {
	dex.StepDefaults[StartInput]
}

func (s *AddOrderStep) Execute(ctx dex.Context, input StartInput) (dex.StepDecision, error) {
	if err := keyMerchantID.SetValue(ctx, input.MerchantID); err != nil {
		return nil, err
	}
	if err := keyOrders.SetValue(ctx, input.OrderID, OrderDetail{
		ItemName: input.ItemName,
		Status:   "pending",
	}); err != nil {
		return nil, err
	}
	return dex.GoTo(&WaitForUpdateStep{}, WaitInput{OrderID: input.OrderID}), nil
}

// --- Step 2: Wait for a status update on a specific order's channel ---

type WaitInput struct {
	OrderID string
}

type WaitForUpdateStep struct {
	dex.DefaultStepId
}

func (s *WaitForUpdateStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodStateLockingKeys: dex.LockDynamicStateKey(keyOrders),
	}
}

func (s *WaitForUpdateStep) WaitFor(_ dex.Context, input WaitInput) (dex.WaitForCondition, error) {
	return dex.AnyOf(
		OrderUpdates.Condition(input.OrderID),
		dex.Timer(48*time.Hour),
	), nil
}

func (s *WaitForUpdateStep) Execute(ctx dex.Context, input WaitInput) (dex.StepDecision, error) {
	msgs, err := OrderUpdates.GetConsumedMessages(ctx, input.OrderID)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return dex.Fail("order update timeout"), nil
	}

	newStatus := msgs[0].NewStatus

	existing, err := keyOrders.GetValue(ctx, input.OrderID)
	if err != nil {
		return nil, err
	}
	if err := keyOrders.SetValue(ctx, input.OrderID, OrderDetail{
		ItemName: existing.ItemName,
		Status:   newStatus,
	}); err != nil {
		return nil, err
	}

	if newStatus == "delivered" {
		merchantID, err := keyMerchantID.GetValue(ctx)
		if err != nil {
			return nil, err
		}
		return dex.Complete(map[string]any{
			"merchant_id": merchantID,
			"order_id":    input.OrderID,
			"status":      newStatus,
		}), nil
	}

	return dex.GoTo(&WaitForUpdateStep{}, input), nil
}
