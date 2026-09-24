// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package visualizationv2start

import (
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/visualization-v2-start/model"
)

type StartSchemaFlow struct {
	dex.FlowDefaults
}

func (*StartSchemaFlow) GetFlowType() string {
	return "StartSchemaFlow"
}

func (*StartSchemaFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(startSchemaStep{})}
}

func (flow *StartSchemaFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*StartSchemaFlow) GetDexSummary(
	_ dex.Context,
	_ dex.None,
) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*StartSchemaFlow) GetDexDisplay(
	_ dex.Context,
	_ dex.None,
) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

// dex:group group-id:start group-label:"Start"
// dex:explanation text:"Start the schema test Flow."
type startSchemaStep struct {
	dex.StepDefaultsNoWaitFor[model.StartPayload]
}

func (startSchemaStep) GetStepType() string {
	return "StartSchema"
}

func (startSchemaStep) Execute(
	_ dex.Context,
	_ model.StartPayload,
) (*dex.StepDecision, error) {
	return dex.ForceComplete(nil), nil
}
