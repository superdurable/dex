// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package interpreter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

type subFlowStarterTestProvider struct {
	interfaces.WorkflowProvider
	activityOptions          interfaces.ActivityOptions
	localActivityCallCount   int
	regularActivityCallCount int
	startErr                 error
}

func (p *subFlowStarterTestProvider) WithActivityOptions(
	ctx interfaces.UnifiedContext,
	activityOptions interfaces.ActivityOptions,
) interfaces.UnifiedContext {
	p.activityOptions = activityOptions
	return ctx
}

func (p *subFlowStarterTestProvider) ExtendContextWithValue(
	parent interfaces.UnifiedContext,
	key string,
	value interface{},
) interfaces.UnifiedContext {
	return interfaces.NewUnifiedContext(context.WithValue(parent.GetContext().(context.Context), key, value))
}

func (p *subFlowStarterTestProvider) GoNamed(
	ctx interfaces.UnifiedContext,
	_ string,
	run func(interfaces.UnifiedContext),
) {
	run(ctx)
}

func (p *subFlowStarterTestProvider) Await(
	_ interfaces.UnifiedContext,
	condition func() bool,
) error {
	if !condition() {
		return errors.New("condition is not ready")
	}
	return nil
}

func (p *subFlowStarterTestProvider) ExecuteLocalActivity(
	_ interface{},
	_ interfaces.UnifiedContext,
	_ interface{},
	_ ...interface{},
) error {
	p.localActivityCallCount++
	return p.startErr
}

func (p *subFlowStarterTestProvider) ExecuteActivity(
	_ interface{},
	_ dexpb.StepDurability,
	_ interfaces.UnifiedContext,
	_ interface{},
	_ interface{},
	_ interface{},
) error {
	p.regularActivityCallCount++
	return p.startErr
}

func (p *subFlowStarterTestProvider) GetContextValue(
	ctx interfaces.UnifiedContext,
	key string,
) interface{} {
	return ctx.GetContext().(context.Context).Value(key)
}

func TestSubFlowStarterUsesDurableActivityAndConfiguredRetryPolicy(t *testing.T) {
	provider := &subFlowStarterTestProvider{}
	completedResults := map[int32]*dexpb.FlowResult{}
	conditionState := &dexpb.WaitingConditionState{
		SubFlowConditions: []*dexpb.SubFlowConditionState{{}},
	}
	tracker := NewSubFlowTracker("parent-flow", nil)
	tracker.Register("parent-step-1", conditionState, completedResults)
	starter := NewSubFlowStarter(
		provider,
		&Activities{},
		tracker,
		"parent-step-1",
		&dexpb.FlowConfig{},
		&dexpb.WaitingCondition{SubFlowConditions: []*dexpb.SubFlowCondition{{}}},
		&config.InterpreterActivityConfig{
			SubFlowStartActivityConfig: &config.SubFlowStartActivityConfig{
				StartToCloseTimeout: 13 * time.Second,
				RetryPolicy: &config.RetryPolicy{
					InitialInterval:    200 * time.Millisecond,
					BackoffCoefficient: 3,
					MaximumInterval:    5 * time.Second,
					MaximumAttempts:    7,
					TotalDuration:      2 * time.Minute,
				},
			},
		},
		&GlobalVersioner{version: DurableSubFlowStartActivityVersion},
	)

	err := starter.StartAll(interfaces.NewUnifiedContext(context.Background()))

	require.NoError(t, err)
	require.Equal(t, 1, provider.regularActivityCallCount)
	require.Zero(t, provider.localActivityCallCount)
	require.Equal(t, interfaces.ActivityOptions{
		StartToCloseTimeout: 13 * time.Second,
		RetryPolicy: &config.RetryPolicy{
			InitialInterval:    200 * time.Millisecond,
			BackoffCoefficient: 3,
			MaximumInterval:    5 * time.Second,
			MaximumAttempts:    7,
			TotalDuration:      2 * time.Minute,
		},
	}, provider.activityOptions)
}

func TestSubFlowStarterPreservesLocalActivityForExistingFlows(t *testing.T) {
	provider := &subFlowStarterTestProvider{startErr: errors.New("local start failed")}
	tracker := NewSubFlowTracker("parent-flow", nil)
	tracker.Register(
		"parent-step-1",
		&dexpb.WaitingConditionState{SubFlowConditions: []*dexpb.SubFlowConditionState{{}}},
		map[int32]*dexpb.FlowResult{},
	)
	starter := NewSubFlowStarter(
		provider,
		&Activities{},
		tracker,
		"parent-step-1",
		&dexpb.FlowConfig{},
		&dexpb.WaitingCondition{SubFlowConditions: []*dexpb.SubFlowCondition{{}}},
		&config.InterpreterActivityConfig{},
		&GlobalVersioner{version: SplitWaitForAttributeTimeoutSemanticsVersion},
	)

	err := starter.StartAll(interfaces.NewUnifiedContext(context.Background()))

	require.ErrorContains(t, err, "local start failed")
	require.Equal(t, 1, provider.localActivityCallCount)
	require.Zero(t, provider.regularActivityCallCount)
	require.Equal(t, interfaces.ActivityOptions{
		StartToCloseTimeout:                 30 * time.Second,
		LocalActivityScheduleToCloseTimeout: 2 * time.Minute,
	}, provider.activityOptions)
}
