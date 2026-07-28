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
	"github.com/superdurable/dex/sdk-go/dex"
)

type persistenceWorkflowState1 struct {
	dex.WorkflowStateDefaults
}

func (b persistenceWorkflowState1) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	kw := persistence.GetSearchAttributeKeyword(testSearchAttributeKeyword)

	if kw != "init-keyword" {
		panic("incorrect init value: " + kw)
	}
	txt := persistence.GetSearchAttributeText(testSearchAttributeText)
	if txt != "init-text" {
		panic("incorrect init value: " + txt)
	}

	var do ExampleDataObjectModel
	persistence.GetDataAttribute(testDataObjectKey, &do)
	if do.StrValue == "" && do.IntValue == 0 {
		input.Get(&do)
		if do.StrValue == "" || do.IntValue == 0 {
			panic("this value shouldn't be empty as we got it from start request")
		}
	} else {
		panic("this value should be empty because we haven't set it before")
	}
	persistence.SetDataAttribute(testDataObjectKey, do)
	persistence.SetDataAttribute(testDataObjectKey2, "a string")
	persistence.SetSearchAttributeInt(testSearchAttributeInt, 1)

	return dex.EmptyCommandRequest(), nil
}

func (b persistenceWorkflowState1) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	iv := persistence.GetSearchAttributeInt(testSearchAttributeInt)
	if iv != 1 {
		panic("this value must be 1 because it got set by WaitUntil API")
	}

	var do ExampleDataObjectModel
	persistence.GetDataAttribute(testDataObjectKey, &do)
	var str string
	persistence.GetDataAttribute(testDataObjectKey2, &str)
	if str != "a string" {
		panic("testDataObjectKey2 value is incorrect")
	}

	persistence.SetSearchAttributeDatetime(testSearchAttributeDatetime, do.Datetime)
	persistence.SetSearchAttributeBool(testSearchAttributeBool, true)
	return dex.SingleNextState(persistenceWorkflowState2{}, nil), nil
}
