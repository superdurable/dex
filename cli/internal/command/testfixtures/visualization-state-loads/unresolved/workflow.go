// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package unresolved

import (
	"github.com/superdurable/dex/cli/internal/command/testfixtures/visualization-state-loads/unresolved/sharedoptions"
	"github.com/superdurable/dex/sdk-go/dex"
)

var Subscribers = dex.DefineAttributeMap[string]("subscribers")

type UnresolvedLoadsFlow struct {
	dex.FlowDefaults
}

func (*UnresolvedLoadsFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(listSubscribers{})}
}

func (flow *UnresolvedLoadsFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{dex.DefineRPC(flow.GetSubscriber, sharedoptions.RPCWholeMaps(Subscribers))}
}

func (*UnresolvedLoadsFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{Subscribers}}
}

type listSubscribers struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (listSubscribers) GetStepOptions() *dex.StepOptions {
	return sharedoptions.ExecuteWholeMaps(Subscribers)
}

func (listSubscribers) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete(Subscribers.AllInstanceKeys(ctx)), nil
}

func (*UnresolvedLoadsFlow) HandleTimeout(ctx dex.Context) (*dex.StepDecision, error) {
	return dex.GracefulComplete(Subscribers.MapSize(ctx)), nil
}

func (*UnresolvedLoadsFlow) GetSubscriber(ctx dex.Context, recipient string) (*dex.RPCResult[string], error) {
	address, err := Subscribers.Get(ctx, recipient)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[string]{Output: address}, nil
}
