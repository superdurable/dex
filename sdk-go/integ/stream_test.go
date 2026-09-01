// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package integ

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

var streamTestProgress = dex.DefineStream[string]("stream-test-progress", 1<<20)

type streamTestFlow struct {
	dex.FlowDefaults
}

func (streamTestFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(streamTestStep{})}
}

func (streamTestFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Streams: []dex.StreamDef{streamTestProgress}}
}

type streamTestStep struct {
	dex.StepDefaultsNoWaitFor[struct{}]
}

func (streamTestStep) Execute(ctx dex.Context, _ struct{}) (*dex.StepDecision, error) {
	progress, err := dex.NewBufferedTextStream(
		ctx,
		streamTestProgress,
		dex.BufferedTextStreamMaxBytes(len("step-progress-1")),
	)
	if err != nil {
		return nil, err
	}
	if err := progress.Write("step-progress-"); err != nil {
		return nil, err
	}
	if err := progress.Write("1"); err != nil {
		return nil, err
	}
	if err := progress.Write("step-progress-"); err != nil {
		return nil, err
	}
	if err := progress.Write("2"); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(struct{}{}), nil
}

func TestStreamRoundTrip(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "stream")
	_, err := integClient.StartFlow(ctx, streamTestFlow{}, flowID, struct{}{}, dex.StartFlowOptions{})
	require.NoError(t, err)
	waitForFlow(t, flowID, false)

	require.NoError(t, integClient.WriteStream(ctx, flowID, streamTestProgress, "client-write", "client-progress"))
	require.NoError(t, integClient.WriteStream(ctx, flowID, streamTestProgress, "client-write", "client-progress-2"))

	var stepValue string
	stepMessage, err := integClient.ReadStream(ctx, flowID, streamTestProgress, "", &stepValue)
	require.NoError(t, err)
	require.Equal(t, "step-progress-1", stepValue)
	require.NotEmpty(t, stepMessage.ResumeToken)
	require.False(t, stepMessage.CreatedTime.IsZero())
	require.NotEmpty(t, stepMessage.Source)
	require.Equal(t, '#', rune(stepMessage.Source[0]))

	var secondStepValue string
	secondStepMessage, err := integClient.ReadStream(
		ctx,
		flowID,
		streamTestProgress,
		stepMessage.ResumeToken,
		&secondStepValue,
	)
	require.NoError(t, err)
	require.Equal(t, "step-progress-2", secondStepValue)
	require.Equal(t, stepMessage.Source, secondStepMessage.Source)

	var clientValue string
	clientMessage, err := integClient.ReadStream(
		ctx,
		flowID,
		streamTestProgress,
		secondStepMessage.ResumeToken,
		&clientValue,
	)
	require.NoError(t, err)
	require.Equal(t, "client-progress", clientValue)
	require.NotEqual(t, stepMessage.ResumeToken, clientMessage.ResumeToken)
	require.False(t, clientMessage.CreatedTime.IsZero())
	require.Equal(t, "client-write", clientMessage.Source)

	var secondClientValue string
	secondClientMessage, err := integClient.ReadStream(
		ctx,
		flowID,
		streamTestProgress,
		clientMessage.ResumeToken,
		&secondClientValue,
	)
	require.NoError(t, err)
	require.Equal(t, "client-progress-2", secondClientValue)
	require.Equal(t, "client-write", secondClientMessage.Source)
}
