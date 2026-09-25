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
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex/sdk-go/dex"
)

var result = dex.DefineAttribute[sdkgo.MutationResult[openaifactory.Response]]("result")

type factoryOutput = openaifactory.CreateResponseResult

type dynamicFactoryFlow struct {
	dex.FlowDefaults
	connection openaifactory.Connection
}

func (flow *dynamicFactoryFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(openaifactory.NewCreateResponseStep(flow.dynamicConfig())),
		dex.DefineStep(openaifactory.NewCreateResponseStep(openaifactory.CreateResponseStepConfig[string]{
			StepType: flow.dynamicStepType(),
			Annotations: sdkgo.StepAnnotations{
				GroupID: "factory", GroupLabel: "Factory", Explanation: "Use a dynamic Step type.",
			},
			Connection: flow.connection, BuildOperationInput: buildOperationInput,
			Completed: sdkgo.GoTo(finishStep{}), Failed: sdkgo.GoTo(finishStep{}),
			Uncertain: sdkgo.GoTo(finishStep{}), Defect: sdkgo.GoTo(finishStep{}),
			ResultAttribute: &result,
		})),
		dex.DefineStep(openaifactory.NewCreateResponseStep(openaifactory.CreateResponseStepConfig[string]{
			StepType: "DynamicTarget",
			Annotations: sdkgo.StepAnnotations{
				GroupID: "factory", GroupLabel: "Factory", Explanation: "Use a dynamic branch target.",
			},
			Connection: flow.connection, BuildOperationInput: buildOperationInput,
			Completed: sdkgo.GoTo(flow.dynamicTarget()), Failed: sdkgo.GoTo(finishStep{}),
			Uncertain: sdkgo.GoTo(finishStep{}), Defect: sdkgo.GoTo(finishStep{}),
			ResultAttribute: &result,
		})),
		dex.DefineStep(openaifactory.NewCreateResponseStep(openaifactory.CreateResponseStepConfig[string]{
			StepType: "MissingBranch",
			Annotations: sdkgo.StepAnnotations{
				GroupID: "factory", GroupLabel: "Factory", Explanation: "Omit a required branch target.",
			},
			Connection: flow.connection, BuildOperationInput: buildOperationInput,
			Completed: sdkgo.GoTo(finishStep{}), Failed: sdkgo.GoTo(finishStep{}),
			Uncertain:       sdkgo.GoTo(finishStep{}),
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
		Annotations: sdkgo.StepAnnotations{
			GroupID: "factory", GroupLabel: "Factory", Explanation: "Use a dynamic config.",
		},
		Connection: flow.connection, BuildOperationInput: buildOperationInput,
		Completed: sdkgo.GoTo(finishStep{}), Failed: sdkgo.GoTo(finishStep{}),
		Uncertain: sdkgo.GoTo(finishStep{}), Defect: sdkgo.GoTo(finishStep{}),
		ResultAttribute: &result,
	}
}

func buildOperationInput(value string) (openaifactory.CreateRequest, error) {
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
