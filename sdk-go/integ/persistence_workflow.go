// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
