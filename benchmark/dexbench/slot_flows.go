// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package benchmark

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// slotProbeInput carries the time the open-loop scheduler meant to start the Flow.
type slotProbeInput struct {
	ScheduledAtUnixNano int64
}

// slotProbeFlow is a one-Step Flow; per-Worker instances let the Step record which Worker ran it.
type slotProbeFlow struct {
	dex.FlowDefaults
	flowType string
	step     slotProbeStep
}

func newSlotProbeFlow(flowTypeIndex, workerIndex int, handlerDuration time.Duration, durability dex.StepDurability, recorder *slotProbeRecorder) *slotProbeFlow {
	return &slotProbeFlow{
		flowType: slotProbeFlowType(flowTypeIndex),
		step:     slotProbeStep{workerIndex: workerIndex, handlerDuration: handlerDuration, durability: durability, recorder: recorder},
	}
}

// slotProbeFlowType names the Flow type of one index; F Flow types put each stream in 1 + F queues.
func slotProbeFlowType(flowTypeIndex int) string {
	return fmt.Sprintf("SlotProbeFlow%02d", flowTypeIndex)
}

func (flow *slotProbeFlow) GetFlowType() string { return flow.flowType }

func (flow *slotProbeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(flow.step)}
}

func (*slotProbeFlow) GetPersistenceSchema() dex.PersistenceSchema { return dex.PersistenceSchema{} }

type slotProbeStep struct {
	dex.StepDefaultsNoWaitFor[slotProbeInput]
	workerIndex     int
	handlerDuration time.Duration
	durability      dex.StepDurability
	recorder        *slotProbeRecorder
}

func (step slotProbeStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{ExecuteDurability: step.durability}
}

func (step slotProbeStep) Execute(_ dex.Context, input slotProbeInput) (*dex.StepDecision, error) {
	if step.recorder == nil {
		return nil, fmt.Errorf("slot probe Step ran in a client-only registry")
	}
	step.recorder.recordStart(step.workerIndex, time.Unix(0, input.ScheduledAtUnixNano))
	defer step.recorder.recordFinish(step.workerIndex)
	time.Sleep(step.handlerDuration)
	return dex.GracefulComplete(nil), nil
}
