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

type executeApiFailRecoveryWorkflowState1 struct {
	dex.WorkflowStateDefaultsNoWaitUntil
}

func (b executeApiFailRecoveryWorkflowState1) GetStateId() string {
	return "execute_api_fail_recovery_workflow_state1"
}

func (b executeApiFailRecoveryWorkflowState1) GetStateOptions() *dex.StateOptions {
	options := &dex.StateOptions{
		ExecuteApiRetryPolicy: &dexpb.RetryPolicy{
			InitialIntervalSeconds: dexpb.PtrInt32(1),
			MaximumAttempts:        dexpb.PtrInt32(1),
		},
		ExecuteApiFailureProceedState: &executeApiFailRecoveryWorkflowState2{},
	}

	return options
}

func (b executeApiFailRecoveryWorkflowState1) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	return nil, errors.New("error")
}
