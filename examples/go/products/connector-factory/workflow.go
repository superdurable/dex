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

	openai "github.com/superdurable/dex-connectors-library/connectors/openai"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	initializeCustomerSummaryStepType = "InitializeCustomerSummary"
	generateCustomerSummaryStepType   = "GenerateCustomerSummary"
	validateReconciliationStepType    = "ValidateCustomerSummaryReconciliation"
	reconcileCustomerSummaryStepType  = "ReconcileCustomerSummary"
)

var generatedCustomerSummary = dex.DefineAttribute[sdkgo.MutationResult[openai.Response]]("generated-customer-summary")

var reconciledCustomerSummary = dex.DefineAttribute[sdkgo.QueryResult[openai.Response]]("reconciled-customer-summary")

var customerSummaryContext = dex.DefineAttribute[Input]("customer-summary-context")

var customerSummaryProgress = dex.DefineStream[sdkgo.ProgressUpdate]("customer-summary-progress", 1<<20)

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

type generateCustomerSummaryResult = openai.CreateResponseResult

type reconcileCustomerSummaryResult = openai.RetrieveResponseResult

type CustomerSummaryConnectorFlow struct {
	dex.FlowDefaults
	connection openai.Connection
}

func NewCustomerSummaryConnectorFlow(openAI *openai.Client, connection sdkgo.ConnectionRef) *CustomerSummaryConnectorFlow {
	typedConnection, err := openai.NewConnection(openAI, connection)
	if err != nil {
		panic("customer summary Flow requires a valid connection")
	}
	return &CustomerSummaryConnectorFlow{connection: typedConnection}
}

func (flow *CustomerSummaryConnectorFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(InitializeCustomerSummary{}),
		dex.DefineStep(openai.NewCreateResponseStep(openai.CreateResponseStepConfig[Input]{
			StepType: generateCustomerSummaryStepType,
			Annotations: sdkgo.StepAnnotations{
				GroupID:     "generation",
				GroupLabel:  "Generation",
				Explanation: "Generate a customer summary with streamed model progress.",
			},
			Connection:          flow.connection,
			MapToOperationInput: mapToGenerateCustomerSummaryInput,
			Completed:           sdkgo.GoTo(CustomerSummaryCompleted{}),
			Failed:              sdkgo.GoTo(CustomerSummaryFailed{}),
			Uncertain:           sdkgo.GoTo(ValidateCustomerSummaryReconciliation{}),
			Defect:              sdkgo.GoTo(CustomerSummaryFailed{}),
			ResultAttribute:     &generatedCustomerSummary,
			ProgressStream:      &customerSummaryProgress,
			TextStream:          &customerSummaryText,
			StepOptionsOverride: &dex.StepOptions{
				ExecuteFailure: dex.ProceedToOnExecuteFailure(CustomerSummaryExecuteFailed{}, nil),
			},
		})),
		dex.DefineStep(ValidateCustomerSummaryReconciliation{}),
		dex.DefineStep(openai.NewRetrieveResponseStep(openai.RetrieveResponseStepConfig[generateCustomerSummaryResult]{
			StepType: reconcileCustomerSummaryStepType,
			Annotations: sdkgo.StepAnnotations{
				GroupID:     "recovery",
				GroupLabel:  "Recovery",
				Explanation: "Retrieve an uncertain model response without repeating the mutation.",
			},
			Connection:          flow.connection,
			MapToOperationInput: mapToReconcileCustomerSummaryInput,
			Found:               sdkgo.GoTo(CustomerSummaryReconciled{}),
			Failed:              sdkgo.GoTo(CustomerSummaryReconcileFailed{}),
			Defect:              sdkgo.GoTo(CustomerSummaryReconcileFailed{}),
			ResultAttribute:     &reconciledCustomerSummary,
		})),
		dex.DefineStep(CustomerSummaryCompleted{}),
		dex.DefineStep(CustomerSummaryFailed{}),
		dex.DefineStep(CustomerSummaryReconciled{}),
		dex.DefineStep(CustomerSummaryReconcileFailed{}),
		dex.DefineStep(CustomerSummaryExecuteFailed{}),
	}
}

func (*CustomerSummaryConnectorFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{customerSummaryContext, generatedCustomerSummary, reconciledCustomerSummary},
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

func mapToGenerateCustomerSummaryInput(input Input) openai.CreateRequest {
	return openai.CreateRequest{
		Model:        "gpt-5-mini",
		Instructions: "Summarize the customer notes for an account manager.",
		Input:        input.Notes,
	}
}

func mapToReconcileCustomerSummaryInput(result generateCustomerSummaryResult) openai.RetrieveRequest {
	return openai.RetrieveRequest{ResponseID: result.Receipt.ProviderObjectID}
}

// dex:group group-id:generation group-label:"Generation"
// dex:explanation text:"Persist customer context before invoking the Connector operation."
type InitializeCustomerSummary struct {
	dex.StepDefaultsNoWaitFor[Input]
}

func (InitializeCustomerSummary) GetStepType() string {
	return initializeCustomerSummaryStepType
}

func (InitializeCustomerSummary) Execute(ctx dex.Context, input Input) (*dex.StepDecision, error) {
	if input.CustomerID == "" || input.Notes == "" {
		return dex.ForceFail("customer ID and notes are required"), nil
	}
	if err := customerSummaryContext.Set(ctx, input); err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[Input](generateCustomerSummaryStepType), input), nil
}

// dex:group group-id:recovery group-label:"Recovery"
// dex:explanation text:"Validate the provider response identity before starting reconciliation."
type ValidateCustomerSummaryReconciliation struct {
	dex.StepDefaultsNoWaitFor[generateCustomerSummaryResult]
}

func (ValidateCustomerSummaryReconciliation) GetStepType() string {
	return validateReconciliationStepType
}

func (ValidateCustomerSummaryReconciliation) Execute(
	_ dex.Context,
	result generateCustomerSummaryResult,
) (*dex.StepDecision, error) {
	if result.Receipt.ProviderObjectID == "" {
		return dex.ForceFail("provider response ID is required for reconciliation"), nil
	}
	return dex.GoTo(
		sdkgo.StepRef[generateCustomerSummaryResult](reconcileCustomerSummaryStepType),
		result,
	), nil
}

func optionalGeneratedCustomerSummary(ctx dex.Context) (sdkgo.MutationResult[openai.Response], error) {
	result, err := generatedCustomerSummary.Get(ctx)
	var missing *dex.AttributeNotFoundError
	if errors.As(err, &missing) {
		return sdkgo.MutationResult[openai.Response]{}, nil
	}
	return result, err
}

func optionalReconciledCustomerSummary(ctx dex.Context) (sdkgo.QueryResult[openai.Response], error) {
	result, err := reconciledCustomerSummary.Get(ctx)
	var missing *dex.AttributeNotFoundError
	if errors.As(err, &missing) {
		return sdkgo.QueryResult[openai.Response]{}, nil
	}
	return result, err
}

// dex:group group-id:generation group-label:"Generation"
// dex:explanation text:"Complete after OpenAI confirms the generated customer summary."
type CustomerSummaryCompleted struct {
	dex.StepDefaultsNoWaitFor[generateCustomerSummaryResult]
}

func (CustomerSummaryCompleted) Execute(ctx dex.Context, result generateCustomerSummaryResult) (*dex.StepDecision, error) {
	input, err := customerSummaryContext.Get(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(Output{
		CustomerID: input.CustomerID,
		ResponseID: result.Value.ID,
		Summary:    result.Value.OutputText,
	}), nil
}

// dex:group group-id:failure group-label:"Failure"
// dex:explanation text:"Fail after OpenAI rejects the generation request or local input is invalid."
type CustomerSummaryFailed struct {
	dex.StepDefaultsNoWaitFor[generateCustomerSummaryResult]
}

func (CustomerSummaryFailed) Execute(_ dex.Context, result generateCustomerSummaryResult) (*dex.StepDecision, error) {
	return dex.ForceFail(failureMessage("customer summary generation", result.Failure)), nil
}

// dex:group group-id:recovery group-label:"Recovery"
// dex:explanation text:"Complete after retrieval confirms the uncertain model response."
type CustomerSummaryReconciled struct {
	dex.StepDefaultsNoWaitFor[reconcileCustomerSummaryResult]
}

func (CustomerSummaryReconciled) Execute(ctx dex.Context, result reconcileCustomerSummaryResult) (*dex.StepDecision, error) {
	input, err := customerSummaryContext.Get(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(Output{
		CustomerID: input.CustomerID,
		ResponseID: result.Value.ID,
		Summary:    result.Value.OutputText,
	}), nil
}

// dex:group group-id:failure group-label:"Failure"
// dex:explanation text:"Fail when retrieval cannot confirm the uncertain model response."
type CustomerSummaryReconcileFailed struct {
	dex.StepDefaultsNoWaitFor[reconcileCustomerSummaryResult]
}

func (CustomerSummaryReconcileFailed) Execute(_ dex.Context, result reconcileCustomerSummaryResult) (*dex.StepDecision, error) {
	return dex.ForceFail(failureMessage("customer summary reconciliation", result.Failure)), nil
}

// dex:group group-id:execute-failure group-label:"Execute Failure"
// dex:explanation text:"Fail after the generation Step exhausts its retry policy."
type CustomerSummaryExecuteFailed struct {
	dex.StepDefaultsNoWaitFor[Input]
}

func (CustomerSummaryExecuteFailed) Execute(_ dex.Context, _ Input) (*dex.StepDecision, error) {
	return dex.ForceFail("customer summary generation exhausted its retry policy"), nil
}

func failureMessage(operation string, failure *sdkgo.Failure) string {
	if failure == nil {
		return operation + " failed"
	}
	return operation + ": " + failure.Message
}
