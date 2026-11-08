// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package actionparity

import "github.com/superdurable/dex/sdk-go/dex"

var (
	CaseStatus     = dex.DefineAttribute[string]("case-status")
	GateRequestKey = dex.DefineAttribute[string]("gate-request-key")
	Priority       = dex.DefineAttribute[int64]("priority")
)

type RefundReviewFlow struct{ dex.FlowDefaults }

type RejectRefundInput struct {
	Reason         string  `json:"reason"`
	GateRequestKey string  `json:"gate_request_key"`
	Note           *string `json:"note"`
}

func (*RefundReviewFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(HoldStep{})}
}

func (flow *RefundReviewFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.RejectRefund, &dex.RPCOptions{
			Name:           "reject_refund",
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(CaseStatus)},
			Action: dex.DefineAction(
				"Reject refund",
				dex.WhenAttributeMatches(
					CaseStatus,
					dex.AttributeMatchEqual("awaiting-manager"),
					dex.AttributeMatchEqual("escalated"),
				),
				dex.ActionRequiresPermission("refund.approve"),
			),
		}),
		dex.DefineRPC(flow.EscalateCase, &dex.RPCOptions{
			Name: "escalate_case",
			Action: dex.DefineAction(
				"Escalate",
				dex.WhenAttributeMatches(Priority, dex.AttributeMatchEqual[int64](1), dex.AttributeMatchEqual[int64](2)),
				dex.ActionRequiresPermission("refund.escalate"),
			),
		}),
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*RefundReviewFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{CaseStatus, GateRequestKey, Priority}}
}

// dex:input field-name:reason value-type:string source:user required:true description:"Rejection reason"
// dex:input field-name:gate_request_key value-type:string source:attribute attribute-key:gate-request-key required:true description:"Approval gate"
// dex:input field-name:note value-type:string source:user required:false description:"Note"
func (*RefundReviewFlow) RejectRefund(ctx dex.Context, input RejectRefundInput) (*dex.RPCResult[dex.None], error) {
	status, err := CaseStatus.Get(ctx)
	if err != nil || status != "awaiting-manager" {
		return &dex.RPCResult[dex.None]{}, err
	}
	if err := CaseStatus.Set(ctx, "rejected: "+input.Reason); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*RefundReviewFlow) EscalateCase(ctx dex.Context, _ dex.None) (*dex.RPCResult[dex.None], error) {
	priority, err := Priority.Get(ctx)
	if err != nil || priority == 0 {
		return &dex.RPCResult[dex.None]{}, err
	}
	if err := CaseStatus.Set(ctx, "escalated"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*RefundReviewFlow) GetDexSummary(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

func (*RefundReviewFlow) GetDexDisplay(_ dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	return &dex.RPCResult[map[string]any]{Output: map[string]any{}}, nil
}

// dex:group group-id:hold group-label:"Hold"
// dex:explanation text:"Wait for the review to close."
type HoldStep struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (HoldStep) Execute(_ dex.Context, _ dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}
