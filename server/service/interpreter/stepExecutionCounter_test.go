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

package interpreter

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/ptr"
	"github.com/superdurable/iwf/service/interpreter/config"
	"github.com/superdurable/iwf/service/interpreter/cont"
)

func TestStepExecutionCounterTracksWaitForSteps(t *testing.T) {
	provider := &s2WorkflowProvider{}
	configer := config.NewFlowConfiger(&iwfpb.FlowConfig{})
	counter := NewStepExecutionCounter(
		nil,
		provider,
		configer,
		cont.NewContinueAsCounter(configer, nil, provider),
	)
	waitStep := &iwfpb.StepMovement{StepType: "wait"}
	skipStep := &iwfpb.StepMovement{
		StepType:    "skip",
		StepOptions: &iwfpb.StepOptions{SkipWaitFor: true},
	}

	require.NoError(t, counter.MarkStepTypeExecutingIfNotYet([]StepRequest{
		NewStepStartRequest(waitStep),
		NewStepStartRequest(skipStep),
	}))
	require.Equal(t, int32(2), counter.GetTotalCurrentlyExecutingCount())
	require.Equal(t, []string{"wait"}, provider.upserts[0][service.SearchAttributeExecutingStateIds])

	require.Equal(t, "wait-1", counter.CreateNextExecutionId("wait"))
	require.Equal(t, "wait-2", counter.CreateNextExecutionId("wait"))
	require.Equal(t, "skip-1", counter.CreateNextExecutionId("skip"))

	require.NoError(t, counter.MarkStepExecutionCompleted(skipStep, nil))
	require.Len(t, provider.upserts, 1)
	require.NoError(t, counter.MarkStepExecutionCompleted(waitStep, nil))
	require.Equal(t, int32(0), counter.GetTotalCurrentlyExecutingCount())
	require.Equal(t, []string{}, provider.upserts[1][service.SearchAttributeExecutingStateIds])
}

func TestStepExecutionCounterBackendFailureIsAtomic(t *testing.T) {
	provider := &s2WorkflowProvider{upsertErr: errors.New("backend unavailable")}
	configer := config.NewFlowConfiger(&iwfpb.FlowConfig{
		ActiveStepSearchMode: ptr.Any(iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL),
	})
	counter := NewStepExecutionCounter(
		nil,
		provider,
		configer,
		cont.NewContinueAsCounter(configer, nil, provider),
	)

	err := counter.MarkStepTypeExecutingIfNotYet([]StepRequest{
		NewStepStartRequest(&iwfpb.StepMovement{StepType: "step"}),
	})
	require.ErrorContains(t, err, "backend unavailable")
	require.Equal(t, int32(0), counter.GetTotalCurrentlyExecutingCount())
	require.Empty(t, counter.Dump().GetStepTypeCurrentlyExecutingCount())
}

func TestStepExecutionCounterDisabledModeAndSharedType(t *testing.T) {
	disabledProvider := &s2WorkflowProvider{}
	disabledConfiger := config.NewFlowConfiger(&iwfpb.FlowConfig{
		ActiveStepSearchMode: ptr.Any(iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED),
	})
	disabledCounter := NewStepExecutionCounter(
		nil,
		disabledProvider,
		disabledConfiger,
		cont.NewContinueAsCounter(disabledConfiger, nil, disabledProvider),
	)
	disabledStep := &iwfpb.StepMovement{StepType: "disabled"}
	require.NoError(t, disabledCounter.MarkStepTypeExecutingIfNotYet([]StepRequest{
		NewStepStartRequest(disabledStep),
	}))
	require.NoError(t, disabledCounter.MarkStepExecutionCompleted(disabledStep, nil))
	require.Empty(t, disabledProvider.upserts)

	sharedProvider := &s2WorkflowProvider{}
	sharedConfiger := config.NewFlowConfiger(&iwfpb.FlowConfig{
		ActiveStepSearchMode: ptr.Any(iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL),
	})
	sharedCounter := NewStepExecutionCounter(
		nil,
		sharedProvider,
		sharedConfiger,
		cont.NewContinueAsCounter(sharedConfiger, nil, sharedProvider),
	)
	first := &iwfpb.StepMovement{StepType: "shared"}
	second := &iwfpb.StepMovement{StepType: "shared"}
	require.NoError(t, sharedCounter.MarkStepTypeExecutingIfNotYet([]StepRequest{
		NewStepStartRequest(first),
		NewStepStartRequest(second),
	}))
	require.Len(t, sharedProvider.upserts, 1)
	require.NoError(t, sharedCounter.MarkStepExecutionCompleted(first, nil))
	require.Len(t, sharedProvider.upserts, 1)
	require.NoError(t, sharedCounter.MarkStepExecutionCompleted(second, nil))
	require.Len(t, sharedProvider.upserts, 2)
}

func TestRebuildStepExecutionCounterRetainsProtoMaps(t *testing.T) {
	provider := &s2WorkflowProvider{}
	configer := config.NewFlowConfiger(&iwfpb.FlowConfig{
		ActiveStepSearchMode: ptr.Any(iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL),
	})
	info := &iwfpb.StepExecutionCounterInfo{
		StepTypeStartedCount:            map[string]int32{"step": 2},
		StepTypeCurrentlyExecutingCount: map[string]int32{"step": 1},
		TotalCurrentlyExecutingCount:    1,
	}
	counter := RebuildStepExecutionCounter(
		nil,
		provider,
		configer,
		cont.NewContinueAsCounter(configer, nil, provider),
		info,
	)

	info.StepTypeStartedCount["owned"] = 4
	require.Equal(t, int32(4), counter.Dump().StepTypeStartedCount["owned"])
	require.Equal(t, "step-3", counter.CreateNextExecutionId("step"))
	require.NoError(t, counter.MarkStepExecutionCompleted(
		&iwfpb.StepMovement{StepType: "step"},
		nil,
	))
	require.Equal(t, int32(0), counter.GetTotalCurrentlyExecutingCount())
}
