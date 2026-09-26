// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package webv2approval

import (
	"errors"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	StatusAwaitingDecision = "awaiting-decision"
	StatusApproved         = "approved"
	DecidePermission       = "approval.decide"
)

var ApprovalDecisions = dex.DefineChannel[string]("approval-decisions")

var (
	approvalRequester = dex.DefineAttribute[string]("approval-requester")
	approvalStatus    = dex.DefineAttribute[string]("approval-status")
	approvalNote      = dex.DefineAttribute[string]("approval-note")
)

type Input struct {
	Requester string `json:"requester"`
}

type Flow struct {
	dex.FlowDefaults
}

func StartStepType() string {
	return dex.GetFinalStepType[Input](awaitDecision{})
}

func (*Flow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(awaitDecision{}),
		dex.DefineStep(recordApproval{}),
	}
}

func (*Flow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{approvalRequester, approvalStatus, approvalNote},
		Channels:   []dex.ChannelDef{ApprovalDecisions},
	}
}

func (flow *Flow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
		dex.DefineRPC(flow.ApproveDecision, &dex.RPCOptions{
			Action: dex.DefineAction(
				"Approve",
				dex.WhenAttributeMatches(approvalStatus, dex.AttributeMatchEqual(StatusAwaitingDecision)),
				dex.ActionRequiresPermission(DecidePermission),
			),
		}),
	}
}

// dex:field attribute-key:approval-requester value-type:string editable:false description:"Requester" ui-slot:title
// dex:field attribute-key:approval-status value-type:string editable:false description:"Approval status" ui-slot:status
func (*Flow) GetDexSummary(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	requester, err := optionalAttribute(ctx, approvalRequester)
	if err != nil {
		return nil, err
	}
	status, err := optionalAttribute(ctx, approvalStatus)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"approval-requester": requester,
		"approval-status":    status,
	}}, nil
}

// dex:field attribute-key:approval-requester value-type:string editable:false description:"Requester" ui-slot:title
// dex:field attribute-key:approval-status value-type:string editable:false description:"Approval status" ui-slot:status
// dex:field attribute-key:approval-note value-type:string editable:true description:"Approval note"
func (*Flow) GetDexDisplay(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	requester, err := optionalAttribute(ctx, approvalRequester)
	if err != nil {
		return nil, err
	}
	status, err := optionalAttribute(ctx, approvalStatus)
	if err != nil {
		return nil, err
	}
	note, err := optionalAttribute(ctx, approvalNote)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"approval-requester": requester,
		"approval-status":    status,
		"approval-note":      note,
	}}, nil
}

func (*Flow) ApproveDecision(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	if err := ApprovalDecisions.Publish(ctx, StatusApproved); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

// dex:group group-id:decision group-label:"Decision"
// dex:explanation text:"Wait for an operator to approve the request."
type awaitDecision struct {
	dex.StepDefaults
}

func (awaitDecision) WaitFor(ctx dex.Context, input Input) (*dex.Wait, error) {
	if err := approvalRequester.Set(ctx, input.Requester); err != nil {
		return nil, err
	}
	if err := approvalStatus.Set(ctx, StatusAwaitingDecision); err != nil {
		return nil, err
	}
	return dex.AllOf(ApprovalDecisions.ForOne()), nil
}

func (awaitDecision) Execute(ctx dex.Context, _ Input) (*dex.StepDecision, error) {
	decisions, err := ApprovalDecisions.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GoTo(recordApproval{}, decisions[0]), nil
}

// dex:group group-id:resolution group-label:"Resolution"
// dex:explanation text:"Record the approval and complete the request."
type recordApproval struct {
	dex.StepDefaultsNoWaitFor[string]
}

func (recordApproval) Execute(ctx dex.Context, decision string) (*dex.StepDecision, error) {
	if err := approvalStatus.Set(ctx, decision); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(decision), nil
}

func optionalAttribute(ctx dex.Context, attribute dex.Attribute[string]) (any, error) {
	value, err := attribute.Get(ctx)
	if err == nil {
		return value, nil
	}
	var notFound *dex.AttributeNotFoundError
	if errors.As(err, &notFound) {
		return nil, nil
	}
	return nil, err
}
