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

type skipWaitUntilWorkflow struct {
	dex.DefaultWorkflowType
	dex.EmptyPersistenceSchema
	dex.EmptyCommunicationSchema
}

func (b skipWaitUntilWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(&skipWaitUntilState1{}),
		dex.NonStartingStateDef(&skipWaitUntilState2{}),
	}
}

type skipWaitUntilWorkflow2 struct {
	dex.DefaultWorkflowType
	dex.EmptyPersistenceSchema
	dex.EmptyCommunicationSchema
}

func (b skipWaitUntilWorkflow2) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(skipWaitUntilState1{}),
		dex.NonStartingStateDef(skipWaitUntilState2{}),
	}
}
