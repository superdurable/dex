// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
)

type persistenceWorkflow struct {
	dex.DefaultWorkflowType
	dex.EmptyCommunicationSchema
}

const (
	testDataObjectKey  = "test-data-object"
	testDataObjectKey2 = "test-data-object-2"

	testSearchAttributeInt      = "CustomIntField"
	testSearchAttributeDatetime = "CustomDatetimeField"
	testSearchAttributeBool     = "CustomBoolField"
	testSearchAttributeDouble   = "CustomDoubleField"
	testSearchAttributeText     = "CustomStringField"
	testSearchAttributeKeyword  = "CustomKeywordField"
)

func (b persistenceWorkflow) GetWorkflowStates() []dex.StateDef {
	return []dex.StateDef{
		dex.StartingStateDef(&persistenceWorkflowState1{}),
		dex.NonStartingStateDef(&persistenceWorkflowState2{}),
	}
}

func (b persistenceWorkflow) GetPersistenceSchema() []dex.PersistenceFieldDef {
	return []dex.PersistenceFieldDef{
		dex.DataAttributeDef(testDataObjectKey),
		dex.DataAttributeDef(testDataObjectKey2),
		dex.SearchAttributeDef(testSearchAttributeInt, dexpb.INT),
		dex.SearchAttributeDef(testSearchAttributeDatetime, dexpb.DATETIME),
		dex.SearchAttributeDef(testSearchAttributeBool, dexpb.BOOL),
		dex.SearchAttributeDef(testSearchAttributeDouble, dexpb.DOUBLE),
		dex.SearchAttributeDef(testSearchAttributeText, dexpb.TEXT),
		dex.SearchAttributeDef(testSearchAttributeKeyword, dexpb.KEYWORD),
	}
}
