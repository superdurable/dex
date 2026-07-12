package main

import (
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// ============================================================================
// Wait/channel/timer benchmark flows
//
// These flows are driven by the /trigger HTTP endpoint via mode= and exercise
// the SDK's WaitFor execution path (Channel Min/Max, AnyOf, AllOf, Timer,
// PublishToChannel from both WaitFor and Execute, SetStateKey from both
// methods, ProcessStepsUnblocked checkpoint). See
// docs/wait-for-conditions-design.md for the protocol.
// ============================================================================

var (
	keyNumDelivered = dex.NewStateKey[int]("num_delivered")
	keyLastValue    = dex.NewStateKey[string]("last_value")
	keyNotes        = dex.NewStateKey[[]string]("notes")
	keyOrderRecords = dex.NewDynamicStateKey[orderRecord]("orders/")
)

// notifyChannel is a static channel reused across all four flows. Tests can
// publish to it via /publish?runId=...&channel=notify&value=...
var notifyChannel = dex.NewChannel[map[string]any]("notify")

// downstreamChannel is published from a step's WaitFor (scenario 1.5) so the
// dev-stack stagger script can observe both the wait satisfaction AND the
// downstream publish in the timeline.
var downstreamChannel = dex.NewChannel[map[string]any]("downstream")

func waitFlowPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(keyNumDelivered),
			dex.DefineStateKey(keyLastValue),
			dex.DefineStateKey(keyNotes),
		},
		Channels: []dex.ChannelDef{
			dex.DefineChannel(notifyChannel),
			dex.DefineChannel(downstreamChannel),
		},
	}
}

// --- Flow A: ChannelMinMaxFlow (covers 1.1 publish-from-Execute, 1.6 channel-Min/Max) ---

type channelMinMaxFlow struct {
	dex.FlowDefaults
}

func (f *channelMinMaxFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return waitFlowPersistenceSchema()
}

func (f *channelMinMaxFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep(&channelMinMaxStep{}),
	}
}

type waitInput struct {
	Token string `json:"token"`
}

type channelMinMaxStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *channelMinMaxStep) WaitFor(_ dex.Context, _ waitInput) (dex.WaitForCondition, error) {
	return dex.AnyOf(
		notifyChannel.ConditionWithMinMax(2, 5),
		dex.Timer(2*time.Minute),
	), nil
}

func (s *channelMinMaxStep) Execute(ctx dex.Context, _ waitInput) (dex.StepDecision, error) {
	msgs, err := notifyChannel.GetConsumedMessages(ctx)
	if err != nil {
		return nil, err
	}
	notes, err := keyNotes.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	notes = append(append([]string{}, notes...), "delivered")

	numDelivered, err := keyNumDelivered.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	numDelivered += len(msgs)
	if err := keyNumDelivered.SetValue(ctx, numDelivered); err != nil {
		return nil, err
	}
	if err := keyNotes.SetValue(ctx, notes); err != nil {
		return nil, err
	}
	if len(msgs) > 0 {
		if value, ok := msgs[0]["value"].(string); ok {
			if err := keyLastValue.SetValue(ctx, value); err != nil {
				return nil, err
			}
		}
	}

	if err := downstreamChannel.Publish(ctx, map[string]any{"event": "ack", "delivered": numDelivered}); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

// --- Flow B: AllOfTimerChannelFlow (covers 1.4 AllOf, 1.5 publish-from-WaitFor, 1.7 state-upsert-from-WaitFor) ---

type allOfTimerChannelFlow struct {
	dex.FlowDefaults
}

func (f *allOfTimerChannelFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return waitFlowPersistenceSchema()
}

func (f *allOfTimerChannelFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep(&allOfTimerChannelStep{}),
	}
}

type allOfTimerChannelStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *allOfTimerChannelStep) WaitFor(ctx dex.Context, _ waitInput) (dex.WaitForCondition, error) {
	if err := keyNotes.SetValue(ctx, []string{"armed"}); err != nil {
		return nil, err
	}
	if err := downstreamChannel.Publish(ctx, map[string]any{"event": "armed"}); err != nil {
		return nil, err
	}
	return dex.AllOf(
		dex.Timer(15*time.Second),
		notifyChannel.ConditionWithMinMax(1, 0),
	), nil
}

func (s *allOfTimerChannelStep) Execute(ctx dex.Context, _ waitInput) (dex.StepDecision, error) {
	msgs, err := notifyChannel.GetConsumedMessages(ctx)
	if err != nil {
		return nil, err
	}
	notes, err := keyNotes.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	notes = append(append([]string{}, notes...), "fired")

	numDelivered, err := keyNumDelivered.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	if err := keyNumDelivered.SetValue(ctx, numDelivered+len(msgs)); err != nil {
		return nil, err
	}
	if err := keyNotes.SetValue(ctx, notes); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

// --- Flow C: AnyOfTimerOnlyFlow (covers 1.2 AnyOf with single timer; pure durable-timer path) ---

type anyOfTimerOnlyFlow struct {
	dex.FlowDefaults
}

func (f *anyOfTimerOnlyFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return waitFlowPersistenceSchema()
}

func (f *anyOfTimerOnlyFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep(&anyOfTimerOnlyStep{}),
	}
}

type anyOfTimerOnlyStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *anyOfTimerOnlyStep) WaitFor(_ dex.Context, _ waitInput) (dex.WaitForCondition, error) {
	return dex.AnyOf(dex.Timer(20 * time.Second)), nil
}

func (s *anyOfTimerOnlyStep) Execute(ctx dex.Context, _ waitInput) (dex.StepDecision, error) {
	notes, err := keyNotes.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	notes = append(append([]string{}, notes...), "timer-fired")
	if err := keyNotes.SetValue(ctx, notes); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

// --- Flow D: AnyOfRaceFlow (covers 1.3 AnyOf timer-vs-channel; 1.7+1.8 state-upsert from both methods) ---

type anyOfRaceFlow struct {
	dex.FlowDefaults
}

func (f *anyOfRaceFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return waitFlowPersistenceSchema()
}

func (f *anyOfRaceFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep(&anyOfRaceStep{}),
	}
}

type anyOfRaceStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *anyOfRaceStep) WaitFor(ctx dex.Context, _ waitInput) (dex.WaitForCondition, error) {
	if err := keyNotes.SetValue(ctx, []string{"wait-started"}); err != nil {
		return nil, err
	}
	return dex.AnyOf(
		dex.Timer(30*time.Second),
		notifyChannel.Condition(),
	), nil
}

func (s *anyOfRaceStep) Execute(ctx dex.Context, _ waitInput) (dex.StepDecision, error) {
	msgs, err := notifyChannel.GetConsumedMessages(ctx)
	if err != nil {
		return nil, err
	}
	notes, err := keyNotes.GetValue(ctx)
	if err != nil {
		return nil, err
	}

	branch := "timer"
	if len(msgs) > 0 {
		branch = "channel"
	} else if ctx.TimerFired() {
		branch = "timer"
	}
	notes = append(append([]string{}, notes...), branch)

	numDelivered, err := keyNumDelivered.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	if err := keyNumDelivered.SetValue(ctx, numDelivered+len(msgs)); err != nil {
		return nil, err
	}
	if err := keyLastValue.SetValue(ctx, branch); err != nil {
		return nil, err
	}
	if err := keyNotes.SetValue(ctx, notes); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

// ============================================================================
// Flow E: dynamicChannelFlow (covers dynamic channel: external + internal
// publish, per-key isolation, decoupled producer/consumer pub/sub via
// internally-published acks, AND sibling cancellation when an external
// "cancel" publish arrives).
// ============================================================================

// OrderUpdates: external publishes target this family.
var OrderUpdates = dex.NewDynamicChannel[map[string]any]("order-update-")

// OrderAcks: internal step publishes target this family.
var OrderAcks = dex.NewDynamicChannel[map[string]any]("order-ack-")

// OrderCancellations: external publishes target this family to cancel siblings.
var OrderCancellations = dex.NewDynamicChannel[map[string]any]("order-cancel-")

type dynamicChannelFlow struct {
	dex.FlowDefaults
}

func (f *dynamicChannelFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		DynamicStateKeys: []dex.StateKeyDef{
			dex.DefineDynamicStateKey(keyOrderRecords),
		},
		Channels: []dex.ChannelDef{
			dex.DefineDynamicChannel(OrderUpdates),
			dex.DefineDynamicChannel(OrderAcks),
			dex.DefineDynamicChannel(OrderCancellations),
		},
	}
}

func (f *dynamicChannelFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep(&dispatchOrdersStep{}),
		dex.NonStartingStep(&orderWaitStep{}),
		dex.NonStartingStep(&orderAckStep{}),
	}
}

type dynamicTriggerInput struct {
	OrderIDs []string `json:"order_ids"`
}

type orderInput struct {
	OrderID string `json:"order_id"`
}

type orderRecord struct {
	UpdatePayload string `json:"update_payload,omitempty"`
	AckPayload    string `json:"ack_payload,omitempty"`
	Cancelled     bool   `json:"cancelled,omitempty"`
}

type dispatchOrdersStep struct {
	dex.StepDefaults[dynamicTriggerInput]
}

func (s *dispatchOrdersStep) Execute(ctx dex.Context, input dynamicTriggerInput) (dex.StepDecision, error) {
	if len(input.OrderIDs) == 0 {
		return dex.Fail("dynamicChannelFlow: input.OrderIDs is empty"), nil
	}
	movements := make([]dex.StepMovement, 0, 2*len(input.OrderIDs))
	for _, orderID := range input.OrderIDs {
		branchInput := orderInput{OrderID: orderID}
		movements = append(movements,
			dex.MovementOf(&orderWaitStep{}, branchInput),
			dex.MovementOf(&orderAckStep{}, branchInput),
		)
	}
	return dex.GoToMany(movements...), nil
}

type orderWaitStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *orderWaitStep) WaitFor(_ dex.Context, input orderInput) (dex.WaitForCondition, error) {
	return dex.AnyOf(
		OrderUpdates.Condition(input.OrderID),
		OrderCancellations.Condition(input.OrderID),
		dex.Timer(2*time.Minute),
	), nil
}

func (s *orderWaitStep) Execute(ctx dex.Context, input orderInput) (dex.StepDecision, error) {
	cancelMsgs, err := OrderCancellations.GetConsumedMessages(ctx, input.OrderID)
	if err != nil {
		return nil, err
	}
	if len(cancelMsgs) > 0 {
		reason := ""
		if value, ok := cancelMsgs[0]["value"].(string); ok {
			reason = value
		}
		if err := keyOrderRecords.SetValue(ctx, input.OrderID, orderRecord{
			UpdatePayload: reason,
			Cancelled:     true,
		}); err != nil {
			return nil, err
		}
		return dex.DeadEnd().
			WithCancelingSiblingStepExecution(dex.CancelOf(&orderAckStep{})), nil
	}

	msgs, err := OrderUpdates.GetConsumedMessages(ctx, input.OrderID)
	if err != nil {
		return nil, err
	}
	updatePayload := ""
	if len(msgs) > 0 {
		if value, ok := msgs[0]["value"].(string); ok {
			updatePayload = value
		}
	}
	if err := keyOrderRecords.SetValue(ctx, input.OrderID, orderRecord{UpdatePayload: updatePayload}); err != nil {
		return nil, err
	}
	if err := OrderAcks.Publish(ctx, input.OrderID, map[string]any{"acked": input.OrderID, "from": "orderWaitStep"}); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type orderAckStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *orderAckStep) WaitFor(_ dex.Context, input orderInput) (dex.WaitForCondition, error) {
	return dex.AnyOf(
		OrderAcks.Condition(input.OrderID),
		dex.Timer(2*time.Minute),
	), nil
}

func (s *orderAckStep) Execute(ctx dex.Context, input orderInput) (dex.StepDecision, error) {
	msgs, err := OrderAcks.GetConsumedMessages(ctx, input.OrderID)
	if err != nil {
		return nil, err
	}
	ackPayload := ""
	if len(msgs) > 0 {
		if value, ok := msgs[0]["acked"].(string); ok {
			ackPayload = value
		}
	}

	existing, err := keyOrderRecords.GetValue(ctx, input.OrderID)
	if err != nil {
		return nil, err
	}
	existing.AckPayload = ackPayload
	if err := keyOrderRecords.SetValue(ctx, input.OrderID, existing); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}
