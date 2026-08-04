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
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/utils"
)

type OutputCollector struct {
	outputs []*dexpb.StepCompletionOutput
}

func NewOutputCollector(initOutputs []*dexpb.StepCompletionOutput) *OutputCollector {
	collector := &OutputCollector{}
	for _, output := range initOutputs {
		collector.Add(output)
	}
	return collector
}

func (o *OutputCollector) Add(
	output *dexpb.StepCompletionOutput,
) {
	if output == nil {
		return
	}
	completedStepOutput := output.GetCompletedStepOutput()
	if completedStepOutput.GetKind() == nil || utils.IsNullValue(completedStepOutput) {
		return
	}
	o.outputs = append(o.outputs, output)
}

func (o *OutputCollector) GetAll() []*dexpb.StepCompletionOutput {
	return o.outputs
}
