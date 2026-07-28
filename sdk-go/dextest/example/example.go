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

package example

import "github.com/superdurable/dex/sdk-go/dex"

func NewInitState() dex.WorkflowState {
	return initState{}
}

type initState struct {
	dex.WorkflowStateDefaults
}

const keyCustomer = "customer"

func (b initState) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	var customer string
	input.Get(&customer)
	persistence.SetDataAttribute(keyCustomer, customer)
	return dex.EmptyCommandRequest(), nil
}

func (b initState) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	return dex.GracefulCompletingWorkflow, nil
}
