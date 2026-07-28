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
	"fmt"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
)

type interStateWorkflowState1 struct {
	dex.WorkflowStateDefaults
}

func (b interStateWorkflowState1) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	return dex.AnyCommandCompletedRequest(
			dex.NewInternalChannelCommand("id1", interStateChannel1),
			dex.NewInternalChannelCommand("id2", interStateChannel2)),
		nil
}

func (b interStateWorkflowState1) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	var i int
	cmd1 := commandResults.GetInternalChannelCommandResultById("id1")
	cmd2 := commandResults.GetInternalChannelCommandResultById("id2")
	cmd2.Value.Get(&i)

	if cmd1.Status == dexpb.WAITING && i == 2 {
		return dex.GracefulCompletingWorkflow, nil
	}
	return nil, fmt.Errorf("error in executing %s", ctx.GetStateExecutionId())
}
