// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

type subFlowStepTypeChildFlow struct {
	FlowDefaults
}

func (subFlowStepTypeChildFlow) GetSteps() []StepDef {
	return []StepDef{DefineStartStep(subFlowStepTypeChildStep{})}
}

func (subFlowStepTypeChildFlow) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{}
}

type subFlowStepTypeChildStep struct {
	StepDefaultsNoWaitFor[None]
}

func (subFlowStepTypeChildStep) Execute(Context, None) (*StepDecision, error) {
	return GracefulComplete(nil), nil
}

type subFlowStepTypeParentFlow struct {
	FlowDefaults
}

func (subFlowStepTypeParentFlow) GetSteps() []StepDef {
	return []StepDef{
		DefineStartStep(subFlowStartingStep{}),
		DefineStep(dashedSubFlowStartingStep{}),
		DefineStep(dashedTimerStep{}),
	}
}

func (subFlowStepTypeParentFlow) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{}
}

type subFlowStartingStep struct {
	StepDefaults
}

func (subFlowStartingStep) WaitFor(Context, None) (*Wait, error) {
	return Until(SubFlow(subFlowStepTypeChildFlow{}, nil)), nil
}

func (subFlowStartingStep) Execute(Context, None) (*StepDecision, error) {
	return DeadEnd(), nil
}

type dashedSubFlowStartingStep struct {
	StepDefaults
}

func (dashedSubFlowStartingStep) GetStepType() string {
	return "charge-order"
}

func (dashedSubFlowStartingStep) WaitFor(Context, None) (*Wait, error) {
	return AllOf(Timer(0), SubFlow(subFlowStepTypeChildFlow{}, nil)), nil
}

func (dashedSubFlowStartingStep) Execute(Context, None) (*StepDecision, error) {
	return DeadEnd(), nil
}

type dashedTimerStep struct {
	StepDefaults
}

func (dashedTimerStep) GetStepType() string {
	return "wait-briefly"
}

func (dashedTimerStep) WaitFor(Context, None) (*Wait, error) {
	return Until(Timer(0)), nil
}

func (dashedTimerStep) Execute(Context, None) (*StepDecision, error) {
	return DeadEnd(), nil
}

func TestWorkerRejectsSubFlowFromDashedStepType(t *testing.T) {
	client, closeService := newWorkerClientForFlows(
		t,
		[]Flow{subFlowStepTypeParentFlow{}, subFlowStepTypeChildFlow{}},
		nil,
	)
	defer closeService()

	waitFor := func(stepType string) ([]*dexpb.InvokeWaitForMethodOutput, error) {
		return collectWaitForOutputs(client, &dexpb.InvokeWaitForMethodRequest{
			Context:   workerStepContext(),
			FlowType:  GetFinalFlowType(subFlowStepTypeParentFlow{}),
			StepType:  stepType,
			StepInput: mustEncodeWorkerTestValue(t, nil),
		})
	}

	outputs, err := waitFor("charge-order")
	require.Empty(t, outputs)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.ErrorContains(t, err,
		`Step type "charge-order" contains "-" and cannot start SubFlows: the child Flow ID joins `+
			`the parent Flow ID, Step execution ID, and SubFlow index with "-", so a dash makes it `+
			`ambiguous. Rename the Step type`)

	outputs, err = waitFor(GetFinalStepType(subFlowStartingStep{}))
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Len(t, outputs[0].GetResult().GetWaitingCondition().GetSubFlowConditions(), 1)

	outputs, err = waitFor("wait-briefly")
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Len(t, outputs[0].GetResult().GetWaitingCondition().GetTimerConditions(), 1)
}

func TestValidateSubFlowStartingStepTypeChecksDashAndSubFlows(t *testing.T) {
	err := validateSubFlowStartingStepType("b-C", &dexpb.WaitingCondition{
		SubFlowConditions: []*dexpb.SubFlowCondition{{}},
	})
	require.ErrorContains(t, err, `Step type "b-C" contains "-"`)
	require.NoError(t, validateSubFlowStartingStepType("bC", &dexpb.WaitingCondition{
		SubFlowConditions: []*dexpb.SubFlowCondition{{}},
	}))
	require.NoError(t, validateSubFlowStartingStepType("b-C", &dexpb.WaitingCondition{}))
}
