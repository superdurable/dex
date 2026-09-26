// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package main

import "github.com/superdurable/dex/sdk-go/dex"

var Decisions = dex.DefineChannel[string]("decisions")

type orderInput struct {
	OrderID string `json:"orderId"`
}

type OrderFlow struct {
	dex.FlowDefaults
}

func (*OrderFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(awaitOrder{}),
		dex.DefineStep(shipOrder[orderInput]{}),
	}
}

func (*OrderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{Decisions}}
}

func (flow *OrderFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*OrderFlow) GetDexSummary(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*OrderFlow) GetDexDisplay(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

// dex:group group-id:order group-label:"Order"
// dex:explanation text:"Wait for the order decision."
type awaitOrder struct {
	dex.StepDefaults
}

func (awaitOrder) WaitFor(_ dex.Context, _ orderInput) (*dex.Wait, error) {
	return dex.AllOf(Decisions.ForOne()), nil
}

func (awaitOrder) Execute(_ dex.Context, input orderInput) (*dex.StepDecision, error) {
	return dex.GoTo(shipOrder[orderInput]{}, input), nil
}

// dex:group group-id:order group-label:"Order"
// dex:explanation text:"Ship the order."
type shipOrder[T any] struct {
	dex.StepDefaultsNoWaitFor[orderInput]
}

func (shipOrder[T]) Execute(_ dex.Context, _ orderInput) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}
