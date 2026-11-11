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

type recordingWarnLogger struct {
	testLogger
	warnings []string
}

func (logger *recordingWarnLogger) Warn(message string, _ ...any) {
	logger.warnings = append(logger.warnings, message)
}

func TestWorkerSendsSubFlowFromDashedStepTypeToServer(t *testing.T) {
	registered, err := NewRegistry([]Flow{subFlowStepTypeParentFlow{}, subFlowStepTypeChildFlow{}})
	require.NoError(t, err)
	serverProtocol := &negotiatedServerProtocol{}
	serverProtocol.version.Store(maximumSupportedServerProtocolVersion)
	logger := &recordingWarnLogger{}
	client := &workerDispatchTestClient{
		service: newWorkerService(registered, concreteValueHydrator{}, logger, serverProtocol),
	}

	waitFor := func(stepType string) ([]*dexpb.InvokeWaitForMethodOutput, error) {
		return collectWaitForOutputs(client, &dexpb.InvokeWaitForMethodRequest{
			Context:   workerStepContext(),
			FlowType:  GetFinalFlowType(subFlowStepTypeParentFlow{}),
			StepType:  stepType,
			StepInput: mustEncodeWorkerTestValue(t, nil),
		})
	}

	outputs, err := waitFor("charge-order")
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	waiting := outputs[0].GetResult().GetWaitingCondition()
	require.Len(t, waiting.GetSubFlowConditions(), 1)
	require.Len(t, waiting.GetTimerConditions(), 1)
	require.Len(t, logger.warnings, 1)
	require.Contains(t, logger.warnings[0], "cannot start SubFlows")

	outputs, err = waitFor(GetFinalStepType(subFlowStartingStep{}))
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Len(t, outputs[0].GetResult().GetWaitingCondition().GetSubFlowConditions(), 1)

	outputs, err = waitFor("wait-briefly")
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Len(t, outputs[0].GetResult().GetWaitingCondition().GetTimerConditions(), 1)
	require.Len(t, logger.warnings, 1)
}
