// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package main

import "github.com/superdurable/dex/sdk-go/dex"

type RefundInput struct {
	PaymentID string
}

type RefundOutput struct {
	Accepted bool
}

type BillingFlow struct{}

func (BillingFlow) GetFlowType() string {
	return "billing"
}

func (BillingFlow) GetSteps() []dex.StepDef {
	return nil
}

func (BillingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

func (BillingFlow) Refund(
	ctx dex.Context,
	input RefundInput,
) (dex.RPCResult[RefundOutput], error) {
	return dex.RPCResult[RefundOutput]{Output: RefundOutput{Accepted: true}}, nil
}

var Billing = BillingFlow{}
var _ dex.Flow = Billing
var _ dex.RPC[RefundInput, RefundOutput] = Billing.Refund

func main() {}
