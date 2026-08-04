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
