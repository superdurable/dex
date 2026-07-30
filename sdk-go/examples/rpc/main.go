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

func (BillingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{}
}

func (BillingFlow) Refund(
	ctx dex.Context,
	input RefundInput,
) (dex.RPCResult[RefundOutput], error) {
	return dex.Reply(RefundOutput{Accepted: true}), nil
}

var Billing = BillingFlow{}
var _ dex.Flow = Billing
var _ dex.RPC[RefundInput, RefundOutput] = Billing.Refund

func main() {}
