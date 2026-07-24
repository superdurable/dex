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
	"fmt"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
)

// FlowConfiger holds one execution's effective configuration.
type FlowConfiger struct {
	config *iwfpb.FlowConfig
}

// NewFlowConfiger validates the ownership-transferred configuration.
func NewFlowConfiger(config *iwfpb.FlowConfig) *FlowConfiger {
	if config == nil {
		panic("FlowConfiger requires a non-nil FlowConfig")
	}
	if err := ValidateFlowConfig(config); err != nil {
		panic(fmt.Sprintf("interpreter received an invalid FlowConfig: %v", err))
	}
	return &FlowConfiger{config: config}
}

// UpdateByAPI applies fields present in the validated request.
func (fc *FlowConfiger) UpdateByAPI(config *iwfpb.FlowConfig) error {
	if config == nil {
		return fmt.Errorf("UpdateFlowConfig requires a non-nil FlowConfig")
	}
	if err := ValidateFlowConfig(config); err != nil {
		return err
	}
	if config.ActiveStepSearchMode != nil {
		fc.config.ActiveStepSearchMode = config.ActiveStepSearchMode
	}
	if config.ContinueAsNewPageSizeInBytes != nil {
		fc.config.ContinueAsNewPageSizeInBytes = config.ContinueAsNewPageSizeInBytes
	}
	if config.ContinueAsNewThreshold != nil {
		fc.config.ContinueAsNewThreshold = config.ContinueAsNewThreshold
	}
	if config.StepDurability != nil {
		fc.config.StepDurability = config.StepDurability
	}
	return nil
}

// Get returns the immutable configuration.
func (fc *FlowConfiger) Get() *iwfpb.FlowConfig {
	return fc.config
}

// EffectiveContinueAsNewThreshold returns the raw threshold. Zero disables
// automatic continue-as-new; a negative value is rejected at validation.
func (fc *FlowConfiger) EffectiveContinueAsNewThreshold() int32 {
	return fc.config.GetContinueAsNewThreshold()
}

// EffectiveContinueAsNewPageSizeInBytes resolves a zero page size to the default.
func (fc *FlowConfiger) EffectiveContinueAsNewPageSizeInBytes() int32 {
	if fc.config.GetContinueAsNewPageSizeInBytes() == 0 {
		return int32(service.DefaultContinueAsNewPageSizeInBytes)
	}
	return fc.config.GetContinueAsNewPageSizeInBytes()
}

// EffectiveActiveStepSearchMode resolves UNSPECIFIED to the wait-for default.
func (fc *FlowConfiger) EffectiveActiveStepSearchMode() iwfpb.ActiveStepSearchMode {
	if fc.config.GetActiveStepSearchMode() == iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_UNSPECIFIED {
		return iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR
	}
	return fc.config.GetActiveStepSearchMode()
}

// ResolveWaitForDurability resolves the durability for a step's WaitFor activity.
func (fc *FlowConfiger) ResolveWaitForDurability(opts *iwfpb.StepOptions) iwfpb.StepDurability {
	return resolveDurability(opts.GetWaitForDurabilityOverride(), fc.config.GetStepDurability())
}

// ResolveExecuteDurability resolves the durability for a step's Execute activity.
func (fc *FlowConfiger) ResolveExecuteDurability(opts *iwfpb.StepOptions) iwfpb.StepDurability {
	return resolveDurability(opts.GetExecuteDurabilityOverride(), fc.config.GetStepDurability())
}

// resolveDurability applies step, flow, then synchronous precedence.
func resolveDurability(override, flowLevel iwfpb.StepDurability) iwfpb.StepDurability {
	if override != iwfpb.StepDurability_STEP_DURABILITY_UNSPECIFIED {
		return override
	}
	if flowLevel != iwfpb.StepDurability_STEP_DURABILITY_UNSPECIFIED {
		return flowLevel
	}
	return iwfpb.StepDurability_STEP_DURABILITY_SYNC
}

// ValidateFlowConfig rejects negative sizes and unknown enum numbers.
func ValidateFlowConfig(c *iwfpb.FlowConfig) error {
	if c.GetContinueAsNewThreshold() < 0 {
		return fmt.Errorf("continue_as_new_threshold must be >= 0, got %d", c.GetContinueAsNewThreshold())
	}
	if c.GetContinueAsNewPageSizeInBytes() < 0 {
		return fmt.Errorf("continue_as_new_page_size_in_bytes must be >= 0, got %d", c.GetContinueAsNewPageSizeInBytes())
	}
	if _, ok := iwfpb.StepDurability_name[int32(c.GetStepDurability())]; !ok {
		return fmt.Errorf("unknown step_durability enum value %d", c.GetStepDurability())
	}
	if _, ok := iwfpb.ActiveStepSearchMode_name[int32(c.GetActiveStepSearchMode())]; !ok {
		return fmt.Errorf("unknown active_step_search_mode enum value %d", c.GetActiveStepSearchMode())
	}
	return nil
}
