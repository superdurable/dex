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

// Package connectorfactory demonstrates Connector operations registered as Dex Steps.
package connectorfactory

import (
	"errors"
	"fmt"

	openai "github.com/superdurable/dex-connectors-library/connectors/openai"
	factorysdk "github.com/superdurable/dex-connectors-library/sdk/go"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	generateCustomerSummaryStepType  = "GenerateCustomerSummary"
	reconcileCustomerSummaryStepType = "ReconcileCustomerSummary"
)

var generatedCustomerSummary = dex.DefineAttribute[factorysdk.MutationResult[openai.Response]]("generated-customer-summary")

var reconciledCustomerSummary = dex.DefineAttribute[factorysdk.QueryResult[openai.Response]]("reconciled-customer-summary")

var customerSummaryProgress = dex.DefineStream[factorysdk.ProgressUpdate]("customer-summary-progress", 1<<20)

var customerSummaryText = dex.DefineStream[string]("customer-summary-text", 1<<20)

type Input struct {
	CustomerID string `json:"customerId"`
	Notes      string `json:"notes"`
}

type Output struct {
	CustomerID string `json:"customerId"`
	ResponseID string `json:"responseId"`
	Summary    string `json:"summary"`
}

type generateCustomerSummaryOutput = factorysdk.MutationStepOutput[Input, openai.Response]

type reconcileCustomerSummaryOutput = factorysdk.QueryStepOutput[generateCustomerSummaryOutput, openai.Response]

type CustomerSummaryConnectorFlow struct {
	dex.FlowDefaults
	openAI     *openai.Client
	connection factorysdk.ConnectionRef
}

func NewCustomerSummaryConnectorFlow(openAI *openai.Client, connection factorysdk.ConnectionRef) *CustomerSummaryConnectorFlow {
	if openAI == nil {
		panic("customer summary Flow requires an OpenAI connector")
	}
	if err := connection.Validate(); err != nil {
		panic("customer summary Flow requires a valid connection")
	}
	return &CustomerSummaryConnectorFlow{openAI: openAI, connection: connection}
}

func (flow *CustomerSummaryConnectorFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(factorysdk.MustNewMutationStep(factorysdk.MutationStepConfig[Input, openai.CreateRequest, openai.Response]{
			StepType: generateCustomerSummaryStepType,
			Presentation: factorysdk.StepPresentation{
				GroupID:     "generation",
				GroupLabel:  "Generation",
				Explanation: "Generate a customer summary with streamed model progress.",
			},
			Operation:  flow.openAI.CreateResponse(),
			Connection: flow.connection,
			BuildInput: buildGenerateCustomerSummaryInput,
			Branches: []factorysdk.BranchTarget[generateCustomerSummaryOutput]{
				factorysdk.GoToBranch(openai.CreateResponseBranchCompleted, CustomerSummaryCompletedStep{}),
				factorysdk.GoToBranch(openai.CreateResponseBranchFailed, CustomerSummaryFailedStep{}),
				factorysdk.GoToBranch(openai.CreateResponseBranchUncertain, factorysdk.StepRef[generateCustomerSummaryOutput](reconcileCustomerSummaryStepType)),
				factorysdk.GoToBranch(openai.CreateResponseBranchDefect, CustomerSummaryFailedStep{}),
			},
			ResultAttribute: &generatedCustomerSummary,
			ProgressStream:  &customerSummaryProgress,
			TextStream:      &customerSummaryText,
			StepOptionsOverride: &dex.StepOptions{
				ExecuteFailure: dex.ProceedToOnExecuteFailure(CustomerSummaryExecuteFailedStep{}, nil),
			},
		})),
		dex.DefineStep(factorysdk.MustNewQueryStep(factorysdk.QueryStepConfig[generateCustomerSummaryOutput, openai.RetrieveRequest, openai.Response]{
			StepType: reconcileCustomerSummaryStepType,
			Presentation: factorysdk.StepPresentation{
				GroupID:     "recovery",
				GroupLabel:  "Recovery",
				Explanation: "Retrieve an uncertain model response without repeating the mutation.",
			},
			Operation:  flow.openAI.RetrieveResponse(),
			Connection: flow.connection,
			BuildInput: buildReconcileCustomerSummaryInput,
			Branches: []factorysdk.BranchTarget[reconcileCustomerSummaryOutput]{
				factorysdk.GoToBranch(openai.RetrieveResponseBranchFound, CustomerSummaryReconciledStep{}),
				factorysdk.GoToBranch(openai.RetrieveResponseBranchFailed, CustomerSummaryReconcileFailedStep{}),
				factorysdk.GoToBranch(openai.RetrieveResponseBranchDefect, CustomerSummaryReconcileFailedStep{}),
			},
			ResultAttribute: &reconciledCustomerSummary,
		})),
		dex.DefineStep(CustomerSummaryCompletedStep{}),
		dex.DefineStep(CustomerSummaryFailedStep{}),
		dex.DefineStep(CustomerSummaryReconciledStep{}),
		dex.DefineStep(CustomerSummaryReconcileFailedStep{}),
		dex.DefineStep(CustomerSummaryExecuteFailedStep{}),
	}
}

func (*CustomerSummaryConnectorFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{generatedCustomerSummary, reconciledCustomerSummary},
		Streams:    []dex.StreamDef{customerSummaryProgress, customerSummaryText},
	}
}

func (flow *CustomerSummaryConnectorFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

// dex:field attribute-key:generated-customer-summary value-type:json editable:false description:"Generated summary result"
func (*CustomerSummaryConnectorFlow) GetDexSummary(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	result, err := optionalGeneratedCustomerSummary(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"generated-customer-summary": result}}, nil
}

// dex:field attribute-key:generated-customer-summary value-type:json editable:false description:"Generated summary result"
// dex:field attribute-key:reconciled-customer-summary value-type:json editable:false description:"Reconciled summary result"
func (*CustomerSummaryConnectorFlow) GetDexDisplay(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	generated, err := optionalGeneratedCustomerSummary(ctx)
	if err != nil {
		return nil, err
	}
	reconciled, err := optionalReconciledCustomerSummary(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"generated-customer-summary":  generated,
		"reconciled-customer-summary": reconciled,
	}}, nil
}

func buildGenerateCustomerSummaryInput(input Input) (openai.CreateRequest, error) {
	if input.CustomerID == "" || input.Notes == "" {
		return openai.CreateRequest{}, fmt.Errorf("customer ID and notes are required")
	}
	return openai.CreateRequest{
		Model:        "gpt-5-mini",
		Instructions: "Summarize the customer notes for an account manager.",
		Input:        input.Notes,
	}, nil
}

func buildReconcileCustomerSummaryInput(output generateCustomerSummaryOutput) (openai.RetrieveRequest, error) {
	if output.Result.Receipt.ProviderObjectID == "" {
		return openai.RetrieveRequest{}, fmt.Errorf("provider response ID is required for reconciliation")
	}
	return openai.RetrieveRequest{ResponseID: output.Result.Receipt.ProviderObjectID}, nil
}

func optionalGeneratedCustomerSummary(ctx dex.Context) (factorysdk.MutationResult[openai.Response], error) {
	result, err := generatedCustomerSummary.Get(ctx)
	var missing *dex.AttributeNotFoundError
	if errors.As(err, &missing) {
		return factorysdk.MutationResult[openai.Response]{}, nil
	}
	return result, err
}

func optionalReconciledCustomerSummary(ctx dex.Context) (factorysdk.QueryResult[openai.Response], error) {
	result, err := reconciledCustomerSummary.Get(ctx)
	var missing *dex.AttributeNotFoundError
	if errors.As(err, &missing) {
		return factorysdk.QueryResult[openai.Response]{}, nil
	}
	return result, err
}

// dex:group group-id:generation group-label:"Generation"
// dex:explanation text:"Complete after OpenAI confirms the generated customer summary."
type CustomerSummaryCompletedStep struct {
	dex.StepDefaultsNoWaitFor[generateCustomerSummaryOutput]
}

func (CustomerSummaryCompletedStep) Execute(_ dex.Context, output generateCustomerSummaryOutput) (*dex.StepDecision, error) {
	return dex.GracefulComplete(Output{
		CustomerID: output.Input.CustomerID,
		ResponseID: output.Result.Value.ID,
		Summary:    output.Result.Value.OutputText,
	}), nil
}

// dex:group group-id:failure group-label:"Failure"
// dex:explanation text:"Fail after OpenAI rejects the generation request or local input is invalid."
type CustomerSummaryFailedStep struct {
	dex.StepDefaultsNoWaitFor[generateCustomerSummaryOutput]
}

func (CustomerSummaryFailedStep) Execute(_ dex.Context, output generateCustomerSummaryOutput) (*dex.StepDecision, error) {
	return dex.ForceFail(failureMessage("customer summary generation", output.Result.Failure)), nil
}

// dex:group group-id:recovery group-label:"Recovery"
// dex:explanation text:"Complete after retrieval confirms the uncertain model response."
type CustomerSummaryReconciledStep struct {
	dex.StepDefaultsNoWaitFor[reconcileCustomerSummaryOutput]
}

func (CustomerSummaryReconciledStep) Execute(_ dex.Context, output reconcileCustomerSummaryOutput) (*dex.StepDecision, error) {
	return dex.GracefulComplete(Output{
		CustomerID: output.Input.Input.CustomerID,
		ResponseID: output.Result.Value.ID,
		Summary:    output.Result.Value.OutputText,
	}), nil
}

// dex:group group-id:failure group-label:"Failure"
// dex:explanation text:"Fail when retrieval cannot confirm the uncertain model response."
type CustomerSummaryReconcileFailedStep struct {
	dex.StepDefaultsNoWaitFor[reconcileCustomerSummaryOutput]
}

func (CustomerSummaryReconcileFailedStep) Execute(_ dex.Context, output reconcileCustomerSummaryOutput) (*dex.StepDecision, error) {
	return dex.ForceFail(failureMessage("customer summary reconciliation", output.Result.Failure)), nil
}

// dex:group group-id:failure group-label:"Failure"
// dex:explanation text:"Fail after the generation Step exhausts its retry policy."
type CustomerSummaryExecuteFailedStep struct {
	dex.StepDefaultsNoWaitFor[Input]
}

func (CustomerSummaryExecuteFailedStep) Execute(_ dex.Context, _ Input) (*dex.StepDecision, error) {
	return dex.ForceFail("customer summary generation exhausted its retry policy"), nil
}

func failureMessage(operation string, failure *factorysdk.Failure) string {
	if failure == nil {
		return operation + " failed"
	}
	return operation + ": " + failure.Message
}
