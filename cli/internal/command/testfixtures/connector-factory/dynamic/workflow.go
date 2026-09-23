// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorfactorydynamic

import (
	openai "github.com/superdurable/dex-connectors-library/connectors/openai"
	connector "github.com/superdurable/dex-connectors-library/sdk/go"
	"github.com/superdurable/dex/sdk-go/dex"
)

var result = dex.DefineAttribute[connector.MutationResult[openai.Response]]("result")

type factoryOutput = connector.MutationStepOutput[string, openai.Response]

var dynamicBranches = []connector.BranchTarget[factoryOutput]{
	connector.GoToBranch[factoryOutput](openai.CreateResponseBranchCompleted, finishStep{}),
	connector.GoToBranch[factoryOutput](openai.CreateResponseBranchFailed, finishStep{}),
	connector.GoToBranch[factoryOutput](openai.CreateResponseBranchUncertain, finishStep{}),
	connector.GoToBranch[factoryOutput](openai.CreateResponseBranchDefect, finishStep{}),
}

type dynamicFactoryFlow struct {
	dex.FlowDefaults
	client     *openai.Client
	connection connector.ConnectionRef
}

func (flow *dynamicFactoryFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(connector.MustNewMutationStep(connector.MutationStepConfig[string, openai.CreateRequest, openai.Response]{
			StepType: "DynamicBranches",
			Presentation: connector.StepPresentation{
				GroupID: "factory", GroupLabel: "Factory", Explanation: "Use a dynamic branch slice.",
			},
			Operation: flow.client.CreateResponse(), Connection: flow.connection,
			BuildInput: buildInput, Branches: dynamicBranches, ResultAttribute: &result,
		})),
		dex.DefineStep(connector.MustNewMutationStep(connector.MutationStepConfig[string, openai.CreateRequest, openai.Response]{
			StepType: flow.dynamicStepType(),
			Presentation: connector.StepPresentation{
				GroupID: "factory", GroupLabel: "Factory", Explanation: "Use a dynamic Step type.",
			},
			Operation: flow.client.CreateResponse(), Connection: flow.connection, BuildInput: buildInput,
			Branches: []connector.BranchTarget[factoryOutput]{
				connector.GoToBranch(openai.CreateResponseBranchCompleted, finishStep{}),
				connector.GoToBranch(openai.CreateResponseBranchFailed, finishStep{}),
				connector.GoToBranch(openai.CreateResponseBranchUncertain, finishStep{}),
				connector.GoToBranch(openai.CreateResponseBranchDefect, finishStep{}),
			},
			ResultAttribute: &result,
		})),
		dex.DefineStep(connector.MustNewMutationStep(connector.MutationStepConfig[string, openai.CreateRequest, openai.Response]{
			StepType: "DynamicTarget",
			Presentation: connector.StepPresentation{
				GroupID: "factory", GroupLabel: "Factory", Explanation: "Use a dynamic branch target.",
			},
			Operation: flow.client.CreateResponse(), Connection: flow.connection, BuildInput: buildInput,
			Branches: []connector.BranchTarget[factoryOutput]{
				connector.GoToBranch(openai.CreateResponseBranchCompleted, flow.dynamicTarget()),
				connector.GoToBranch(openai.CreateResponseBranchFailed, finishStep{}),
				connector.GoToBranch(openai.CreateResponseBranchUncertain, finishStep{}),
				connector.GoToBranch(openai.CreateResponseBranchDefect, finishStep{}),
			},
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

func buildInput(value string) (openai.CreateRequest, error) {
	return openai.CreateRequest{Model: "gpt-5-mini", Input: value}, nil
}

// dex:group group-id:finish group-label:"Finish"
// dex:explanation text:"Complete the dynamic factory fixture."
type finishStep struct {
	dex.StepDefaultsNoWaitFor[factoryOutput]
}

func (finishStep) Execute(_ dex.Context, _ factoryOutput) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}
