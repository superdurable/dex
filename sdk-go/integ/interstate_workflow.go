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

type interStateWorkflow struct {
	dex.DefaultWorkflowType
	dex.EmptyPersistenceSchema
}

const interStateChannel1 = "test-inter-state-channel-1"
const interStateChannel2 = "test-inter-state-channel-2"

func (b interStateWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(&interStateWorkflowState0{}),
		dex.NonStartingStateDef(&interStateWorkflowState1{}),
		dex.NonStartingStateDef(&interStateWorkflowState2{}),
	}
}

func (b interStateWorkflow) GetCommunicationSchema() []dex.CommunicationMethodDef {
	return []dex.CommunicationMethodDef{
		dex.InternalChannelDef(interStateChannel1),
		dex.InternalChannelDef(interStateChannel2),
	}
}
