// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package interpreter

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
	"github.com/superdurable/dex/service/interpreter/config"
	"github.com/superdurable/dex/service/interpreter/cont"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

type stepExecutionCounterWorkflowProvider struct {
	s2WorkflowProvider
}

func (p *stepExecutionCounterWorkflowProvider) GetSearchAttributeKeywordArray(
	_ interfaces.UnifiedContext,
	key string,
) ([]string, error) {
	for index := len(p.upserts) - 1; index >= 0; index-- {
		value, ok := p.upserts[index][key]
		if !ok {
			continue
		}
		return value.([]string), nil
	}
	return nil, nil
}

func TestStepExecutionCounterTracksWaitForSteps(t *testing.T) {
	ctx := interfaces.NewUnifiedContext(context.Background())
	provider := &stepExecutionCounterWorkflowProvider{}
	configer := config.NewFlowConfiger(&dexpb.FlowConfig{})
	counter := NewStepExecutionCounter(
		provider,
		configer,
		cont.NewContinueAsCounter(configer),
	)
	waitStep := &dexpb.StepMovement{StepType: "wait"}
	skipStep := &dexpb.StepMovement{
		StepType:    "skip",
		StepOptions: &dexpb.StepOptions{SkipWaitFor: true},
	}

	require.NoError(t, counter.MarkStepTypeActiveIfNotYet(ctx, []StepRequest{
		NewStepStartRequest(waitStep),
		NewStepStartRequest(skipStep),
	}))
	require.Equal(t, int32(2), counter.GetTotalCurrentlyExecutingCount())
	require.Equal(t, []string{"wait"}, provider.upserts[0][service.SearchAttributeActiveStepTypes])

	require.Equal(t, "wait-1", counter.CreateNextExecutionId("wait"))
	require.Equal(t, "skip-1", counter.CreateNextExecutionId("skip"))
	require.False(t, counter.IsStepExecutionCompleted("wait", 1))
	require.False(t, counter.IsStepExecutionCompleted("wait", 2))

	require.NoError(t, counter.MarkStepExecutionCompleted(ctx, skipStep, "skip-1", nil))
	require.Len(t, provider.upserts, 1)
	require.NoError(t, counter.MarkStepExecutionCompleted(ctx, waitStep, "wait-1", nil))
	require.True(t, counter.IsStepExecutionCompleted("wait", 1))
	require.Equal(t, int32(0), counter.GetTotalCurrentlyExecutingCount())
	require.Empty(t, provider.upserts[1][service.SearchAttributeActiveStepTypes])
}

func TestStepExecutionCounterBackendFailureRetainsUpdatedCounts(t *testing.T) {
	ctx := interfaces.NewUnifiedContext(context.Background())
	provider := &stepExecutionCounterWorkflowProvider{
		s2WorkflowProvider: s2WorkflowProvider{
			upsertErr: errors.New("backend unavailable"),
		},
	}
	configer := config.NewFlowConfiger(&dexpb.FlowConfig{
		ActiveStepSearchMode: ptr.Any(dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL),
	})
	counter := NewStepExecutionCounter(
		provider,
		configer,
		cont.NewContinueAsCounter(configer),
	)

	err := counter.MarkStepTypeActiveIfNotYet(ctx, []StepRequest{
		NewStepStartRequest(&dexpb.StepMovement{StepType: "step"}),
	})
	require.ErrorContains(t, err, "backend unavailable")
	require.Equal(t, int32(1), counter.GetTotalCurrentlyExecutingCount())
	require.Equal(t, int32(1), counter.Dump().GetStepTypeCurrentlyExecutingCount()["step"])
}

func TestStepExecutionCounterCompletionBackendFailureIsInternal(t *testing.T) {
	ctx := interfaces.NewUnifiedContext(context.Background())
	provider := &stepExecutionCounterWorkflowProvider{}
	configer := config.NewFlowConfiger(&dexpb.FlowConfig{
		ActiveStepSearchMode: ptr.Any(dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL),
	})
	counter := NewStepExecutionCounter(
		provider,
		configer,
		cont.NewContinueAsCounter(configer),
	)
	step := &dexpb.StepMovement{StepType: "step"}
	require.NoError(t, counter.MarkStepTypeActiveIfNotYet(ctx, []StepRequest{
		NewStepStartRequest(step),
	}))
	stepExecutionID := counter.CreateNextExecutionId(step.GetStepType())
	provider.upsertErr = errors.New("backend unavailable")

	err := counter.MarkStepExecutionCompleted(ctx, step, stepExecutionID, nil)
	require.ErrorContains(t, err, "backend unavailable")
}

func TestStepExecutionCounterDisabledModeAndSharedType(t *testing.T) {
	ctx := interfaces.NewUnifiedContext(context.Background())
	disabledProvider := &stepExecutionCounterWorkflowProvider{}
	disabledConfiger := config.NewFlowConfiger(&dexpb.FlowConfig{
		ActiveStepSearchMode: ptr.Any(dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED),
	})
	disabledCounter := NewStepExecutionCounter(
		disabledProvider,
		disabledConfiger,
		cont.NewContinueAsCounter(disabledConfiger),
	)
	disabledStep := &dexpb.StepMovement{StepType: "disabled"}
	require.NoError(t, disabledCounter.MarkStepTypeActiveIfNotYet(ctx, []StepRequest{
		NewStepStartRequest(disabledStep),
	}))
	disabledStepExecutionId := disabledCounter.CreateNextExecutionId("disabled")
	require.NoError(t, disabledCounter.MarkStepExecutionCompleted(
		ctx,
		disabledStep,
		disabledStepExecutionId,
		nil,
	))
	require.Empty(t, disabledProvider.upserts)

	sharedProvider := &stepExecutionCounterWorkflowProvider{}
	sharedConfiger := config.NewFlowConfiger(&dexpb.FlowConfig{
		ActiveStepSearchMode: ptr.Any(dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL),
	})
	sharedCounter := NewStepExecutionCounter(
		sharedProvider,
		sharedConfiger,
		cont.NewContinueAsCounter(sharedConfiger),
	)
	first := &dexpb.StepMovement{StepType: "shared"}
	second := &dexpb.StepMovement{StepType: "shared"}
	require.NoError(t, sharedCounter.MarkStepTypeActiveIfNotYet(ctx, []StepRequest{
		NewStepStartRequest(first),
		NewStepStartRequest(second),
	}))
	firstStepExecutionId := sharedCounter.CreateNextExecutionId("shared")
	secondStepExecutionId := sharedCounter.CreateNextExecutionId("shared")
	require.Len(t, sharedProvider.upserts, 1)
	require.NoError(t, sharedCounter.MarkStepExecutionCompleted(
		ctx,
		first,
		firstStepExecutionId,
		nil,
	))
	require.Len(t, sharedProvider.upserts, 1)
	require.NoError(t, sharedCounter.MarkStepExecutionCompleted(
		ctx,
		second,
		secondStepExecutionId,
		nil,
	))
	require.Len(t, sharedProvider.upserts, 2)
}

func TestRebuildStepExecutionCounterRetainsProtoMaps(t *testing.T) {
	ctx := interfaces.NewUnifiedContext(context.Background())
	provider := &stepExecutionCounterWorkflowProvider{}
	configer := config.NewFlowConfiger(&dexpb.FlowConfig{
		ActiveStepSearchMode: ptr.Any(dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL),
	})
	info := &dexpb.StepExecutionCounterInfo{
		StepTypeStartedCount:            map[string]int32{"step": 2},
		StepTypeCurrentlyExecutingCount: map[string]int32{"step": 1},
		TotalCurrentlyExecutingCount:    1,
		StepActiveExecutionNums: map[string]*dexpb.StepExecutionNumbers{
			"step": {Numbers: []int32{2}},
		},
	}
	counter := RebuildStepExecutionCounter(
		provider,
		configer,
		cont.NewContinueAsCounter(configer),
		info,
	)

	info.StepTypeStartedCount["owned"] = 4
	require.Equal(t, int32(4), counter.Dump().StepTypeStartedCount["owned"])
	require.True(t, counter.IsStepExecutionCompleted("step", 1))
	require.False(t, counter.IsStepExecutionCompleted("step", 2))
	require.NoError(t, counter.MarkStepTypeActiveIfNotYet(ctx, []StepRequest{
		NewStepStartRequest(&dexpb.StepMovement{StepType: "step"}),
	}))
	require.Equal(t, "step-3", counter.CreateNextExecutionId("step"))
	require.NoError(t, counter.MarkStepExecutionCompleted(
		ctx,
		&dexpb.StepMovement{StepType: "step"},
		"step-2",
		nil,
	))
	require.NoError(t, counter.MarkStepExecutionCompleted(
		ctx,
		&dexpb.StepMovement{StepType: "step"},
		"step-3",
		nil,
	))
	require.Equal(t, int32(0), counter.GetTotalCurrentlyExecutingCount())
}
