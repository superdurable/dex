// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package main

import "github.com/superdurable/dex/sdk-go/dex"

var (
	refundStatus = dex.DefineAttribute[string]("refund-status")
	refundRegion = dex.DefineAttribute[string]("refund-region")
	permissionProjectionLocks = []dex.AttributeLock{
		dex.LockAttribute(refundStatus),
		dex.LockAttribute(refundRegion),
	}
)

type RefundInput struct {
	PaymentID string
}

type RefundOutput struct {
	Accepted bool
}

type BillingFlow struct {
	dex.FlowDefaults
}

func (BillingFlow) GetSteps() []dex.StepDef {
	return nil
}

func (flow BillingFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.ApproveRefund, &dex.RPCOptions{
			LockAttributes: permissionProjectionLocks,
			Action: dex.DefineAction(
				"Approve refund",
				dex.WhenAttributeMatches(
					refundStatus,
					dex.AttributeMatchEqual("awaiting-manager"),
					dex.AttributeMatchEqual("awaiting-agent"),
				),
				dex.ActionRequiresPermission("refund.approve"),
			),
		}),
		dex.DefineRPC(flow.EscalateRefund, &dex.RPCOptions{
			LockAttributes: permissionProjectionLocks,
			Action: dex.DefineAction(
				"Escalate refund",
				dex.WhenAttributeMatches(
					refundRegion,
					dex.AttributeMatchEqual("regulated"),
				),
				dex.ActionRequiresPermission("refund.escalate"),
			),
		}),
	}
}

func (BillingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{
		refundStatus,
		refundRegion,
	}}
}

func (BillingFlow) ApproveRefund(
	ctx dex.Context,
	input RefundInput,
) (*dex.RPCResult[RefundOutput], error) {
	if err := refundStatus.Set(ctx, "approved"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[RefundOutput]{Output: RefundOutput{Accepted: true}}, nil
}

func (BillingFlow) EscalateRefund(
	ctx dex.Context,
	_ RefundInput,
) (*dex.RPCResult[RefundOutput], error) {
	if err := refundRegion.Set(ctx, "escalated"); err != nil {
		return nil, err
	}
	return &dex.RPCResult[RefundOutput]{Output: RefundOutput{Accepted: false}}, nil
}

var Billing = BillingFlow{}
var _ dex.Flow = Billing
var _ dex.RPC[RefundInput, RefundOutput] = Billing.ApproveRefund
var _ dex.RPC[RefundInput, RefundOutput] = Billing.EscalateRefund

func main() {}
