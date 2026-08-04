// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package cont

import (
	"github.com/superdurable/dex/service/interpreter/config"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

type ContinueAsNewCounter struct {
	executedStepApis   int32
	signalsReceived    int32
	syncUpdateReceived int32
	triggeredByAPI     bool

	configer *config.FlowConfiger
	rootCtx  interfaces.UnifiedContext
	provider interfaces.WorkflowProvider
}

func NewContinueAsCounter(
	configer *config.FlowConfiger,
	rootCtx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
) *ContinueAsNewCounter {
	return &ContinueAsNewCounter{
		configer: configer,
		rootCtx:  rootCtx,
		provider: provider,
	}
}

func (c *ContinueAsNewCounter) IncExecutedStepExecution(skipWaitFor bool) {
	if skipWaitFor {
		c.executedStepApis++
	} else {
		c.executedStepApis += 2
	}
}

func (c *ContinueAsNewCounter) IncSignalsReceived() {
	c.signalsReceived++
}

func (c *ContinueAsNewCounter) IncSyncUpdateReceived() {
	c.syncUpdateReceived++
}

func (c *ContinueAsNewCounter) IsThresholdMet() bool {
	if c.triggeredByAPI {
		return true
	}
	// A zero threshold disables automatic continue-as-new.
	if c.configer.EffectiveContinueAsNewThreshold() == 0 {
		return false
	}
	totalOperations := c.signalsReceived + c.executedStepApis + c.syncUpdateReceived
	return totalOperations >= c.configer.EffectiveContinueAsNewThreshold()
}

func (c *ContinueAsNewCounter) TriggerByAPI() {
	c.triggeredByAPI = true
}
