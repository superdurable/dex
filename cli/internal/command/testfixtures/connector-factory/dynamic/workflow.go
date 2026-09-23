// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorfactorydynamic

import (
	openaifactory "github.com/superdurable/dex-connectors-library/connectors/openai"
	connector "github.com/superdurable/dex-connectors-library/sdk/go"
	"github.com/superdurable/dex/sdk-go/dex"
)

var result = dex.DefineAttribute[connector.MutationResult[openaifactory.Response]]("result")

type factoryOutput = openaifactory.CreateResponseStepOutput[string]

type dynamicFactoryFlow struct {
	dex.FlowDefaults
	connection openaifactory.Connection
}

func (flow *dynamicFactoryFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(openaifactory.NewCreateResponseStep(flow.dynamicConfig())),
		dex.DefineStep(openaifactory.NewCreateResponseStep(openaifactory.CreateResponseStepConfig[string]{
			StepType: flow.dynamicStepType(),
			Presentation: connector.StepPresentation{
				GroupID: "factory", GroupLabel: "Factory", Explanation: "Use a dynamic Step type.",
			},
			Connection: flow.connection, BuildInput: buildInput,
			Completed: connector.GoTo(finishStep{}), Failed: connector.GoTo(finishStep{}),
			Uncertain: connector.GoTo(finishStep{}), Defect: connector.GoTo(finishStep{}),
			ResultAttribute: &result,
		})),
		dex.DefineStep(openaifactory.NewCreateResponseStep(openaifactory.CreateResponseStepConfig[string]{
			StepType: "DynamicTarget",
			Presentation: connector.StepPresentation{
				GroupID: "factory", GroupLabel: "Factory", Explanation: "Use a dynamic branch target.",
			},
			Connection: flow.connection, BuildInput: buildInput,
			Completed: connector.GoTo(flow.dynamicTarget()), Failed: connector.GoTo(finishStep{}),
			Uncertain: connector.GoTo(finishStep{}), Defect: connector.GoTo(finishStep{}),
			ResultAttribute: &result,
		})),
		dex.DefineStep(openaifactory.NewCreateResponseStep(openaifactory.CreateResponseStepConfig[string]{
			StepType: "MissingBranch",
			Presentation: connector.StepPresentation{
				GroupID: "factory", GroupLabel: "Factory", Explanation: "Omit a required branch target.",
			},
			Connection: flow.connection, BuildInput: buildInput,
			Completed: connector.GoTo(finishStep{}), Failed: connector.GoTo(finishStep{}),
			Uncertain:       connector.GoTo(finishStep{}),
			ResultAttribute: &result,
		})),
		dex.DefineStep(finishStep{}),
	}
}

func (*dynamicFactoryFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{result}}
}

func (*dynamicFactoryFlow) dynamicStepType() string { return "DynamicStepType" }

func (*dynamicFactoryFlow) dynamicTarget() dex.Step[factoryOutput] { return finishStep{} }

func (flow *dynamicFactoryFlow) dynamicConfig() openaifactory.CreateResponseStepConfig[string] {
	return openaifactory.CreateResponseStepConfig[string]{
		StepType: "DynamicConfig",
		Presentation: connector.StepPresentation{
			GroupID: "factory", GroupLabel: "Factory", Explanation: "Use a dynamic config.",
		},
		Connection: flow.connection, BuildInput: buildInput,
		Completed: connector.GoTo(finishStep{}), Failed: connector.GoTo(finishStep{}),
		Uncertain: connector.GoTo(finishStep{}), Defect: connector.GoTo(finishStep{}),
		ResultAttribute: &result,
	}
}

func buildInput(value string) (openaifactory.CreateRequest, error) {
	return openaifactory.CreateRequest{Model: "gpt-5-mini", Input: value}, nil
}

// dex:group group-id:finish group-label:"Finish"
// dex:explanation text:"Complete the dynamic factory fixture."
type finishStep struct {
	dex.StepDefaultsNoWaitFor[factoryOutput]
}

func (finishStep) Execute(_ dex.Context, _ factoryOutput) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}
