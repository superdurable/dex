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
	"github.com/superdurable/dex/sdk-go/dex"
	"time"
)

type timerWorkflowState1 struct {
	dex.DefaultStateId
	dex.DefaultStateOptions
}

func (b timerWorkflowState1) WaitUntil(ctx dex.WorkflowContext, input dex.Object, persistence dex.Persistence, communication dex.Communication) (*dex.CommandRequest, error) {
	var i int
	input.Get(&i)
	return dex.AllCommandsCompletedRequest(
		dex.NewTimerCommandByDuration("", time.Duration(i)*time.Second),
	), nil
}

func (b timerWorkflowState1) Execute(ctx dex.WorkflowContext, input dex.Object, commandResults dex.CommandResults, persistence dex.Persistence, communication dex.Communication) (*dex.StateDecision, error) {
	var i int
	input.Get(&i)
	return dex.GracefulCompleteWorkflow(i + 1), nil
}
