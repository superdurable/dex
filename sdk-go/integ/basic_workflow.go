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

type basicWorkflow struct {
	dex.DefaultWorkflowType
	dex.EmptyPersistenceSchema
	dex.EmptyCommunicationSchema
}

func (b basicWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(&basicWorkflowState1{}),
		dex.NonStartingStateDef(&basicWorkflowState2{}),
	}
}
