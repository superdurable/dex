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
	"errors"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
)

type proceedOnStateStartFailWorkflowState1 struct {
	output string
}

func (b *proceedOnStateStartFailWorkflowState1) GetStateId() string {
	return "proceed_on_state_start_fail_workflow_state1"
}

func (b *proceedOnStateStartFailWorkflowState1) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	var i string
	input.Get(&i)
	b.output = i + "_state1_start"
	return nil, errors.New("")
}

func (b *proceedOnStateStartFailWorkflowState1) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	b.output += "_state1_decide"
	return dex.SingleNextState(&proceedOnStateStartFailWorkflowState2{}, b.output), nil
}

func (b *proceedOnStateStartFailWorkflowState1) GetStateOptions() *dex.StateOptions {
	return &dex.StateOptions{
		WaitUntilApiRetryPolicy: &dexpb.RetryPolicy{
			InitialIntervalSeconds: dexpb.PtrInt32(1),
			MaximumAttempts:        dexpb.PtrInt32(2),
		},
		WaitUntilApiFailurePolicy: dexpb.PROCEED_ON_FAILURE.Ptr(),
	}
}
