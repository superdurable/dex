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

type stateApiTimeoutWorkflow struct {
	dex.DefaultWorkflowType
	dex.EmptyCommunicationSchema
	dex.EmptyPersistenceSchema
}

func (b stateApiTimeoutWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(&stateApiTimeoutWorkflowState1{}),
	}
}
