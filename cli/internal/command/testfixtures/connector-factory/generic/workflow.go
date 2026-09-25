// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorfactorygeneric

import (
	openai "github.com/superdurable/dex-connectors-library/connectors/openai"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex/sdk-go/dex"
)

type factoryOutput = sdkgo.QueryStepOutput[string, openai.Response]

var result = dex.DefineAttribute[sdkgo.QueryResult[openai.Response]]("generic-result")

type genericFactoryFlow struct {
	dex.FlowDefaults
	client     *openai.Client
	connection sdkgo.ConnectionRef
}

func (flow *genericFactoryFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(sdkgo.MustNewQueryStep(sdkgo.QueryStepConfig[string, openai.RetrieveRequest, openai.Response]{
			StepType: "GenericRetrieveResponse",
			Presentation: sdkgo.StepPresentation{
				GroupID: "factory", GroupLabel: "Factory", Explanation: "Use the generic Connector SDK escape hatch.",
			},
			Operation: flow.client.RetrieveResponse(), Connection: flow.connection, BuildInput: buildInput,
			Branches: []sdkgo.BranchTarget[factoryOutput]{
				sdkgo.GoToBranch(openai.RetrieveResponseBranchFound, finishStep{}),
				sdkgo.GoToBranch(openai.RetrieveResponseBranchFailed, finishStep{}),
				sdkgo.GoToBranch(openai.RetrieveResponseBranchDefect, finishStep{}),
			},
			ResultAttribute: &result,
		})),
		dex.DefineStep(finishStep{}),
	}
}

func (*genericFactoryFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{result}}
}

func (flow *genericFactoryFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

// dex:field attribute-key:generic-result value-type:json editable:false description:"Generic query result"
func (*genericFactoryFlow) GetDexSummary(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"generic-result": nil}}, nil
}

// dex:field attribute-key:generic-result value-type:json editable:false description:"Generic query result"
func (*genericFactoryFlow) GetDexDisplay(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"generic-result": nil}}, nil
}

func buildInput(responseID string) (openai.RetrieveRequest, error) {
	return openai.RetrieveRequest{ResponseID: responseID}, nil
}

// dex:group group-id:finish group-label:"Finish"
// dex:explanation text:"Complete the generic Connector SDK fixture."
type finishStep struct {
	dex.StepDefaultsNoWaitFor[factoryOutput]
}

func (finishStep) Execute(_ dex.Context, _ factoryOutput) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}
