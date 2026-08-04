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

type persistenceWorkflowState2 struct {
	dex.WorkflowStateDefaults
}

const testText = "Hail Dex!"

func (b persistenceWorkflowState2) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	iv := persistence.GetSearchAttributeInt(testSearchAttributeInt)
	if iv != 1 {
		panic("this value must be 1 because it got set by WaitUntil API")
	}

	var do ExampleDataObjectModel
	persistence.GetDataAttribute(testDataObjectKey, &do)
	dv := persistence.GetSearchAttributeDatetime(testSearchAttributeDatetime)
	bv := persistence.GetSearchAttributeBool(testSearchAttributeBool)
	persistence.SetSearchAttributeDouble(testSearchAttributeDouble, 1.0)
	if dv.Unix() == do.Datetime.Unix() && bv {
		persistence.SetSearchAttributeText(testSearchAttributeText, testText)
		return dex.EmptyCommandRequest(), nil
	}
	panic("the value of datatime or bool search attribute is incorrect")

}

func (b persistenceWorkflowState2) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	tv := persistence.GetSearchAttributeText(testSearchAttributeText)
	persistence.SetSearchAttributeKeyword(testSearchAttributeKeyword, "Dex")
	if tv == testText {
		return dex.GracefulCompletingWorkflow, nil
	}
	panic("the value of text search attribute is incorrect")
}
