// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

var (
	gracefulSupportMessages        = dex.DefineChannel[string]("graceful-support-messages")
	gracefulSupportIsSupported     = dex.DefineAttribute[bool]("graceful-support-is-supported")
	gracefulSupportIsGracefulEnded = dex.DefineAttribute[bool]("graceful-support-is-graceful-ended")
)

// gracefulSupportFlow waits for a message, adding the graceful completion condition only when supported.
type gracefulSupportFlow struct {
	dex.FlowDefaults
}

func (gracefulSupportFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(gracefulSupportWaitStep{})}
}

func (gracefulSupportFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{gracefulSupportIsSupported, gracefulSupportIsGracefulEnded},
		Channels:   []dex.ChannelDef{gracefulSupportMessages},
	}
}

type gracefulSupportWaitStep struct {
	dex.StepDefaults
}

func (gracefulSupportWaitStep) WaitFor(ctx dex.Context, _ dex.None) (*dex.Wait, error) {
	isSupported := dex.SupportsGracefulCompletionCondition(ctx)
	if err := gracefulSupportIsSupported.Set(ctx, isSupported); err != nil {
		return nil, err
	}
	if !isSupported {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.AnyOf(gracefulSupportMessages.ForOne(), dex.GracefulCompletionRequested()), nil
}

func (gracefulSupportWaitStep) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := gracefulSupportIsGracefulEnded.Set(ctx, dex.IsGracefulCompletionConditionCompleted(ctx)); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

func TestWaitForAddsTheGracefulCompletionConditionWhenTheServerSupportsIt(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "graceful-support")
	_, err := integClient.StartFlow(ctx, gracefulSupportFlow{}, flowID, nil, dex.StartFlowOptions{})
	require.NoError(t, err)

	// The graceful completion request stays in force, so it may arrive before the Wait registers.
	require.NoError(t, integClient.StopFlow(ctx, flowID, dex.StopOptions{Type: dex.GracefulCompleteFlow}))

	result := waitForFlow(t, flowID, false)
	require.Equal(t, dex.FlowCompleted, result.Status)
	var isSupported, isGracefulEnded bool
	_, err = integClient.GetAttributes(ctx, flowID,
		dex.ReadAttribute(gracefulSupportIsSupported, &isSupported),
		dex.ReadAttribute(gracefulSupportIsGracefulEnded, &isGracefulEnded),
	)
	require.NoError(t, err)
	require.True(t, isSupported)
	require.True(t, isGracefulEnded)
}
