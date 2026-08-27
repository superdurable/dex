// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package integ

import (
	"strings"
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
	if err := streamTestProgress.Write(ctx, "step-progress"); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(struct{}{}), nil
}

func TestStreamRoundTrip(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "stream")
	runID, err := integClient.StartFlow(ctx, streamTestFlow{}, flowID, struct{}{}, dex.StartFlowOptions{})
	require.NoError(t, err)
	waitForFlow(t, flowID, false)

	require.NoError(t, integClient.WriteStream(ctx, flowID, streamTestProgress, "client-write", "client-progress"))
	require.NoError(t, integClient.WriteStream(ctx, flowID, streamTestProgress, "client-write", "duplicate-ignored"))

	var stepValue string
	stepMessage, err := integClient.ReadStream(ctx, flowID, streamTestProgress, "", &stepValue)
	require.NoError(t, err)
	require.Equal(t, "step-progress", stepValue)
	require.NotEmpty(t, stepMessage.ResumeToken)
	require.False(t, stepMessage.CreatedTime.IsZero())
	require.True(t, strings.HasPrefix(stepMessage.IdempotencyKey, runID+"#"))

	var clientValue string
	clientMessage, err := integClient.ReadStream(
		ctx,
		flowID,
		streamTestProgress,
		stepMessage.ResumeToken,
		&clientValue,
	)
	require.NoError(t, err)
	require.Equal(t, "client-progress", clientValue)
	require.NotEqual(t, stepMessage.ResumeToken, clientMessage.ResumeToken)
	require.False(t, clientMessage.CreatedTime.IsZero())
	require.Equal(t, "client-write", clientMessage.IdempotencyKey)
}
