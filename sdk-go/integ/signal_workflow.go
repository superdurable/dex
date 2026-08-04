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

type signalWorkflow struct {
	dex.DefaultWorkflowType
	dex.EmptyPersistenceSchema
}

const testChannelName1 = "test-channel-name-1"
const testChannelName2 = "test-channel-name-2"

func (b signalWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(&signalWorkflowState1{}),
		dex.NonStartingStateDef(&signalWorkflowState2{}),
	}
}

func (b signalWorkflow) GetCommunicationSchema() []dex.CommunicationMethodDef {
	return []dex.CommunicationMethodDef{
		dex.SignalChannelDef(testChannelName1),
		dex.SignalChannelDef(testChannelName2),
	}
}
