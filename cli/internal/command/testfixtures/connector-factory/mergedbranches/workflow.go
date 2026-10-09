// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package mergedbranches

import (
	openaifactory "github.com/superdurable/dex-connectors-library/connectors/openai"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex/sdk-go/dex"
)

type summaryResult = openaifactory.CreateResponseResult

type retrievedSummary = openaifactory.RetrieveResponseResult

var savedSummary = dex.DefineAttribute[string]("saved-summary")

type MergedBranchesFlow struct {
	dex.FlowDefaults
	connection openaifactory.Connection
}

func (flow *MergedBranchesFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(openaifactory.NewCreateResponseStep(openaifactory.CreateResponseStepConfig[string]{
			StepType: "GenerateSummary",
			Annotations: sdkgo.StepAnnotations{
				GroupID: "generation", GroupLabel: "Generation", Explanation: "Generate a summary.",
			},
			Connection: flow.connection, ConnectionName: "summary-model", MapToOperationInput: mapToSummaryRequest,
			Completed: sdkgo.GoTo(saveSummary{}), Defect: sdkgo.GoTo(saveSummary{}),
			Failed: sdkgo.GoTo(recordSummaryFailure{}), Uncertain: sdkgo.GoTo(recordSummaryFailure{}),
		})),
		dex.DefineStep(openaifactory.NewRetrieveResponseStep(openaifactory.RetrieveResponseStepConfig[summaryResult]{
			StepType: "RetrieveSummary",
			Annotations: sdkgo.StepAnnotations{
				GroupID: "generation", GroupLabel: "Generation", Explanation: "Retrieve an uncertain summary.",
			},
			Connection: flow.connection, ConnectionName: "summary-model", MapToOperationInput: mapToRetrieveRequest,
			Found: sdkgo.GoTo(inspectRetrievedSummary{}), Failed: sdkgo.GoTo(inspectRetrievedSummary{}),
			Defect: sdkgo.GoTo(closeSummary{}),
		})),
		dex.DefineStep(saveSummary{}),
		dex.DefineStep(recordSummaryFailure{}),
		dex.DefineStep(inspectRetrievedSummary{}),
		dex.DefineStep(closeSummary{}),
	}
}

func (*MergedBranchesFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{savedSummary}}
}

func mapToSummaryRequest(text string) openaifactory.CreateRequest {
	return openaifactory.CreateRequest{Model: "summary-model", Input: text}
}

func mapToRetrieveRequest(result summaryResult) openaifactory.RetrieveRequest {
	return openaifactory.RetrieveRequest{ResponseID: result.Receipt.ProviderObjectID}
}

type saveSummary struct {
	dex.StepDefaultsNoWaitFor[summaryResult]
}

func (saveSummary) Execute(ctx dex.Context, result summaryResult) (*dex.StepDecision, error) {
	if err := savedSummary.Set(ctx, result.Value.OutputText); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(nil), nil
}

type recordSummaryFailure struct {
	dex.StepDefaultsNoWaitFor[summaryResult]
}

func (recordSummaryFailure) Execute(ctx dex.Context, result summaryResult) (*dex.StepDecision, error) {
	if err := savedSummary.Set(ctx, failureMessage(result)); err != nil {
		return nil, err
	}
	return dex.GoTo(sdkgo.StepRef[summaryResult]("RetrieveSummary"), result), nil
}

type inspectRetrievedSummary struct {
	dex.StepDefaultsNoWaitFor[retrievedSummary]
}

func (inspectRetrievedSummary) Execute(_ dex.Context, result retrievedSummary) (*dex.StepDecision, error) {
	return dex.GracefulComplete(result.Value.OutputText), nil
}

type closeSummary struct {
	dex.StepDefaultsNoWaitFor[retrievedSummary]
}

func (closeSummary) Execute(_ dex.Context, _ retrievedSummary) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

func failureMessage(result summaryResult) string {
	if result.Failure == nil {
		return "summary generation failed"
	}
	return result.Failure.Message
}
