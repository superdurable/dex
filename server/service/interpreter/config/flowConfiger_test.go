// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
)

func TestNewFlowConfiger_PanicsOnNil(t *testing.T) {
	require.Panics(t, func() { NewFlowConfiger(nil) })
}

func TestNewFlowConfiger_PanicsOnInvalid(t *testing.T) {
	require.Panics(t, func() {
		NewFlowConfiger(&dexpb.FlowConfig{ContinueAsNewThreshold: ptr.Any(int32(-1))})
	})
}

func TestFlowConfiger_RetainsOwnershipTransferredInput(t *testing.T) {
	input := &dexpb.FlowConfig{ContinueAsNewThreshold: ptr.Any(int32(5))}
	flowConfiger := NewFlowConfiger(input)

	assert.Same(t, input, flowConfiger.Get())
}

func TestFlowConfiger_ZeroDefaults(t *testing.T) {
	flowConfiger := NewFlowConfiger(&dexpb.FlowConfig{})

	assert.Equal(t, int32(0), flowConfiger.EffectiveContinueAsNewThreshold())
	assert.Equal(t, int32(service.DefaultContinueAsNewPageSizeInBytes), flowConfiger.EffectiveContinueAsNewPageSizeInBytes())
	assert.Equal(t,
		dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR,
		flowConfiger.EffectiveActiveStepSearchMode())
	assert.Equal(t, dexpb.StepDurability_STEP_DURABILITY_SYNC, flowConfiger.ResolveWaitForDurability(nil))
	assert.Equal(t, dexpb.StepDurability_STEP_DURABILITY_SYNC, flowConfiger.ResolveExecuteDurability(nil))
}

func TestFlowConfiger_DurabilityPrecedence(t *testing.T) {
	flowConfiger := NewFlowConfiger(&dexpb.FlowConfig{
		StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_ASYNC),
	})

	assert.Equal(t, dexpb.StepDurability_STEP_DURABILITY_ASYNC, flowConfiger.ResolveWaitForDurability(&dexpb.StepOptions{}))
	assert.Equal(t, dexpb.StepDurability_STEP_DURABILITY_ASYNC, flowConfiger.ResolveExecuteDurability(&dexpb.StepOptions{}))

	waitOverride := &dexpb.StepOptions{WaitForDurabilityOverride: dexpb.StepDurability_STEP_DURABILITY_SYNC}
	assert.Equal(t, dexpb.StepDurability_STEP_DURABILITY_SYNC, flowConfiger.ResolveWaitForDurability(waitOverride))
	assert.Equal(t, dexpb.StepDurability_STEP_DURABILITY_ASYNC, flowConfiger.ResolveExecuteDurability(waitOverride))

	executeOverride := &dexpb.StepOptions{ExecuteDurabilityOverride: dexpb.StepDurability_STEP_DURABILITY_SYNC}
	assert.Equal(t, dexpb.StepDurability_STEP_DURABILITY_ASYNC, flowConfiger.ResolveWaitForDurability(executeOverride))
	assert.Equal(t, dexpb.StepDurability_STEP_DURABILITY_SYNC, flowConfiger.ResolveExecuteDurability(executeOverride))
}

func TestFlowConfiger_UpdateByAPIPartialOverride(t *testing.T) {
	flowConfiger := NewFlowConfiger(&dexpb.FlowConfig{
		ContinueAsNewThreshold:  ptr.Any(int32(5)),
		StepDurability:          ptr.Any(dexpb.StepDurability_STEP_DURABILITY_ASYNC),
		AttributeSyncConfigName: ptr.Any("reporting"),
	})
	update := &dexpb.FlowConfig{
		ContinueAsNewThreshold:  ptr.Any(int32(9)),
		AttributeSyncConfigName: ptr.Any(""),
	}

	require.NoError(t, flowConfiger.UpdateByAPI(update))

	assert.Equal(t, int32(9), flowConfiger.EffectiveContinueAsNewThreshold())
	assert.Equal(t, dexpb.StepDurability_STEP_DURABILITY_ASYNC, flowConfiger.ResolveExecuteDurability(nil))
	assert.Equal(t, "", flowConfiger.Get().GetAttributeSyncConfigName())
}

func TestFlowConfiger_UpdateByAPIRejectsInvalidAndKeepsState(t *testing.T) {
	flowConfiger := NewFlowConfiger(&dexpb.FlowConfig{
		ContinueAsNewThreshold: ptr.Any(int32(7)),
	})
	assert.Error(t, flowConfiger.UpdateByAPI(&dexpb.FlowConfig{
		ContinueAsNewThreshold: ptr.Any(int32(-5)),
	}))
	assert.Error(t, flowConfiger.UpdateByAPI(nil))
	assert.Equal(t, int32(7), flowConfiger.EffectiveContinueAsNewThreshold())
}

func TestValidateFlowConfig(t *testing.T) {
	assert.NoError(t, ValidateFlowConfig(&dexpb.FlowConfig{}))
	assert.Error(t, ValidateFlowConfig(&dexpb.FlowConfig{
		ContinueAsNewThreshold: ptr.Any(int32(-1)),
	}))
	assert.Error(t, ValidateFlowConfig(&dexpb.FlowConfig{
		ContinueAsNewPageSizeInBytes: ptr.Any(int32(-1)),
	}))
	assert.Error(t, ValidateFlowConfig(&dexpb.FlowConfig{
		StepDurability: ptr.Any(dexpb.StepDurability(99)),
	}))
	assert.Error(t, ValidateFlowConfig(&dexpb.FlowConfig{
		ActiveStepSearchMode: ptr.Any(dexpb.ActiveStepSearchMode(99)),
	}))
}
