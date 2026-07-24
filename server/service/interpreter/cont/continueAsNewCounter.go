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

package cont

import (
	"github.com/superdurable/iwf/service/interpreter/config"
	"github.com/superdurable/iwf/service/interpreter/interfaces"
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
