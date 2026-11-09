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

package workflowpatterns

import (
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	IsAwaitingMotionEventAttribute = dex.DefineAttribute[bool]("isAwaitingMotionEvent")
	MotionSensorArmedChannel       = dex.DefineChannel[string]("motionSensorArmed")
	MotionEventChannel             = dex.DefineChannel[int]("motionEvent")
	MotionMonitoringStoppedChannel = dex.DefineChannel[string]("motionMonitoringStopped")
)

// TransientTriggerFlow implements WCP-23: the RPC drops motion events nobody waits for.
type TransientTriggerFlow struct{ dex.FlowDefaults }

func (*TransientTriggerFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(AwaitMotionSensorArmed{}),
		dex.DefineStep(AwaitMotionEvent{}),
		dex.DefineStep(AwaitMonitoringStop{}),
	}
}

func (flow *TransientTriggerFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.ArmMotionSensor, nil),
		dex.DefineRPC(flow.StopMotionMonitoring, nil),
		dex.DefineRPC(flow.SendMotionEvent, &dex.RPCOptions{
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(IsAwaitingMotionEventAttribute)},
		}),
	}
}

func (*TransientTriggerFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{IsAwaitingMotionEventAttribute},
		Channels:   []dex.ChannelDef{MotionSensorArmedChannel, MotionEventChannel, MotionMonitoringStoppedChannel},
	}
}

// ArmMotionSensor arms the sensor so the Flow starts waiting for motion.
func (*TransientTriggerFlow) ArmMotionSensor(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := MotionSensorArmedChannel.Publish(ctx, "armed"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// StopMotionMonitoring ends monitoring, which completes the Flow.
func (*TransientTriggerFlow) StopMotionMonitoring(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := MotionMonitoringStoppedChannel.Publish(ctx, "stopped"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// SendMotionEvent returns true when a Step waits for motion, and false after dropping the event.
func (*TransientTriggerFlow) SendMotionEvent(ctx dex.Context, value int) (*dex.RPCResult[bool], error) {
	isAwaiting, err := getAttributeOrZero(ctx, IsAwaitingMotionEventAttribute)
	if err != nil {
		return nil, err
	}
	if !isAwaiting {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	if err := IsAwaitingMotionEventAttribute.Set(ctx, false); err != nil {
		return nil, err
	}
	if err := MotionEventChannel.Publish(ctx, value); err != nil {
		return nil, err
	}
	return &dex.RPCResult[bool]{Output: true}, nil
}

type AwaitMotionSensorArmed struct{ dex.StepDefaults }

func (AwaitMotionSensorArmed) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(MotionSensorArmedChannel.ForOne()), nil
}

func (AwaitMotionSensorArmed) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GoTo(AwaitMotionEvent{}, nil), nil
}

type AwaitMotionEvent struct{ dex.StepDefaults }

func (AwaitMotionEvent) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForLockAttributes: []dex.AttributeLock{dex.LockAttribute(IsAwaitingMotionEventAttribute)},
	}
}

func (AwaitMotionEvent) WaitFor(ctx dex.Context, _ dex.None) (*dex.Wait, error) {
	if err := IsAwaitingMotionEventAttribute.Set(ctx, true); err != nil {
		return nil, err
	}
	return dex.Until(MotionEventChannel.ForOne()), nil
}

func (AwaitMotionEvent) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	values, err := MotionEventChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(AwaitMonitoringStop{}, values[0]), nil
}

// AwaitMonitoringStop keeps the Flow open so later triggers reach the RPC guard.
type AwaitMonitoringStop struct{ dex.StepDefaults }

func (AwaitMonitoringStop) WaitFor(dex.Context, int) (*dex.Wait, error) {
	return dex.Until(MotionMonitoringStoppedChannel.ForOne()), nil
}

func (AwaitMonitoringStop) Execute(_ dex.Context, value int) (*dex.StepDecision, error) {
	return dex.GracefulComplete(value), nil
}

var (
	CarrierReadyChannel  = dex.DefineChannel[string]("carrierReady")
	ShipmentLabelChannel = dex.DefineChannel[string]("shipmentLabel")
)

// PersistentTriggerFlow implements WCP-24 Persistent Trigger: a label sent early
// stays queued until AwaitShipmentLabel consumes it.
type PersistentTriggerFlow struct{ dex.FlowDefaults }

func (*PersistentTriggerFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(AwaitCarrierReady{}),
		dex.DefineStep(AwaitShipmentLabel{}),
	}
}

func (flow *PersistentTriggerFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.MarkCarrierReady, nil),
		dex.DefineRPC(flow.SendShipmentLabel, nil),
	}
}

func (*PersistentTriggerFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{CarrierReadyChannel, ShipmentLabelChannel}}
}

// MarkCarrierReady records that the carrier is ready.
func (*PersistentTriggerFlow) MarkCarrierReady(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := CarrierReadyChannel.Publish(ctx, "ready"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// SendShipmentLabel queues labelID, even before the Flow waits for it.
func (*PersistentTriggerFlow) SendShipmentLabel(ctx dex.Context, labelID string) (*dex.RPCResult[dex.None], error) {
	if err := ShipmentLabelChannel.Publish(ctx, labelID); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

type AwaitCarrierReady struct{ dex.StepDefaults }

func (AwaitCarrierReady) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(CarrierReadyChannel.ForOne()), nil
}

func (AwaitCarrierReady) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GoTo(AwaitShipmentLabel{}, nil), nil
}

type AwaitShipmentLabel struct{ dex.StepDefaults }

func (AwaitShipmentLabel) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.Until(ShipmentLabelChannel.ForOne()), nil
}

func (AwaitShipmentLabel) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	labels, err := ShipmentLabelChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(labels[0]), nil
}

var (
	_ dex.Flow = (*TransientTriggerFlow)(nil)
	_ dex.Flow = (*PersistentTriggerFlow)(nil)
)
