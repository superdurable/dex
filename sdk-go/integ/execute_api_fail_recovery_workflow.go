// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import "github.com/superdurable/dex/sdk-go/dex"

type executeApiFailRecoveryWorkflow struct {
	dex.WorkflowDefaults
}

func (b executeApiFailRecoveryWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(&executeApiFailRecoveryWorkflowState1{}),
		dex.NonStartingStateDef(&executeApiFailRecoveryWorkflowState2{}),
	}
}
