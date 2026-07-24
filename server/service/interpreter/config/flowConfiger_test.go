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

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/ptr"
)

func TestNewFlowConfiger_PanicsOnNil(t *testing.T) {
	require.Panics(t, func() { NewFlowConfiger(nil) })
}

func TestNewFlowConfiger_PanicsOnInvalid(t *testing.T) {
	require.Panics(t, func() {
		NewFlowConfiger(&iwfpb.FlowConfig{ContinueAsNewThreshold: ptr.Any(int32(-1))})
	})
}

func TestFlowConfiger_RetainsOwnershipTransferredInput(t *testing.T) {
	input := &iwfpb.FlowConfig{ContinueAsNewThreshold: ptr.Any(int32(5))}
	flowConfiger := NewFlowConfiger(input)

	assert.Same(t, input, flowConfiger.Get())
}

func TestFlowConfiger_ZeroDefaults(t *testing.T) {
	flowConfiger := NewFlowConfiger(&iwfpb.FlowConfig{})

	assert.Equal(t, int32(0), flowConfiger.EffectiveContinueAsNewThreshold())
	assert.Equal(t, int32(service.DefaultContinueAsNewPageSizeInBytes), flowConfiger.EffectiveContinueAsNewPageSizeInBytes())
	assert.Equal(t,
		iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR,
		flowConfiger.EffectiveActiveStepSearchMode())
	assert.Equal(t, iwfpb.StepDurability_STEP_DURABILITY_SYNC, flowConfiger.ResolveWaitForDurability(nil))
	assert.Equal(t, iwfpb.StepDurability_STEP_DURABILITY_SYNC, flowConfiger.ResolveExecuteDurability(nil))
}

func TestFlowConfiger_DurabilityPrecedence(t *testing.T) {
	flowConfiger := NewFlowConfiger(&iwfpb.FlowConfig{
		StepDurability: ptr.Any(iwfpb.StepDurability_STEP_DURABILITY_ASYNC),
	})

	assert.Equal(t, iwfpb.StepDurability_STEP_DURABILITY_ASYNC, flowConfiger.ResolveWaitForDurability(&iwfpb.StepOptions{}))
	assert.Equal(t, iwfpb.StepDurability_STEP_DURABILITY_ASYNC, flowConfiger.ResolveExecuteDurability(&iwfpb.StepOptions{}))

	waitOverride := &iwfpb.StepOptions{WaitForDurabilityOverride: iwfpb.StepDurability_STEP_DURABILITY_SYNC}
	assert.Equal(t, iwfpb.StepDurability_STEP_DURABILITY_SYNC, flowConfiger.ResolveWaitForDurability(waitOverride))
	assert.Equal(t, iwfpb.StepDurability_STEP_DURABILITY_ASYNC, flowConfiger.ResolveExecuteDurability(waitOverride))
}

func TestFlowConfiger_UpdateByAPIPartialOverride(t *testing.T) {
	flowConfiger := NewFlowConfiger(&iwfpb.FlowConfig{
		ContinueAsNewThreshold: ptr.Any(int32(5)),
		StepDurability:         ptr.Any(iwfpb.StepDurability_STEP_DURABILITY_ASYNC),
	})
	update := &iwfpb.FlowConfig{ContinueAsNewThreshold: ptr.Any(int32(9))}

	require.NoError(t, flowConfiger.UpdateByAPI(update))

	assert.Equal(t, int32(9), flowConfiger.EffectiveContinueAsNewThreshold())
	assert.Equal(t, iwfpb.StepDurability_STEP_DURABILITY_ASYNC, flowConfiger.ResolveExecuteDurability(nil))
}

func TestFlowConfiger_UpdateByAPIRejectsInvalidAndKeepsState(t *testing.T) {
	flowConfiger := NewFlowConfiger(&iwfpb.FlowConfig{
		ContinueAsNewThreshold: ptr.Any(int32(7)),
	})
	assert.Error(t, flowConfiger.UpdateByAPI(&iwfpb.FlowConfig{
		ContinueAsNewThreshold: ptr.Any(int32(-5)),
	}))
	assert.Error(t, flowConfiger.UpdateByAPI(nil))
	assert.Equal(t, int32(7), flowConfiger.EffectiveContinueAsNewThreshold())
}

func TestValidateFlowConfig(t *testing.T) {
	assert.NoError(t, ValidateFlowConfig(&iwfpb.FlowConfig{}))
	assert.Error(t, ValidateFlowConfig(&iwfpb.FlowConfig{
		ContinueAsNewThreshold: ptr.Any(int32(-1)),
	}))
	assert.Error(t, ValidateFlowConfig(&iwfpb.FlowConfig{
		ContinueAsNewPageSizeInBytes: ptr.Any(int32(-1)),
	}))
	assert.Error(t, ValidateFlowConfig(&iwfpb.FlowConfig{
		StepDurability: ptr.Any(iwfpb.StepDurability(99)),
	}))
	assert.Error(t, ValidateFlowConfig(&iwfpb.FlowConfig{
		ActiveStepSearchMode: ptr.Any(iwfpb.ActiveStepSearchMode(99)),
	}))
}
