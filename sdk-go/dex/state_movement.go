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

package dex

type StateMovement struct {
	// NextStateId is required
	NextStateId string
	// NextStateInput is optional, it's also used as workflow result for GracefulCompletingWorkflowStateId and ForceCompletingWorkflowStateId
	NextStateInput interface{}
}

const (
	ReservedStateIdPrefix             = "_SYS_"
	GracefulCompletingWorkflowStateId = "_SYS_GRACEFUL_COMPLETING_WORKFLOW"
	ForceCompletingWorkflowStateId    = "_SYS_FORCE_COMPLETING_WORKFLOW"
	ForceFailingWorkflowStateId       = "_SYS_FORCE_FAILING_WORKFLOW"
	DeadEndStateId                    = "_SYS_DEAD_END"
)

func NewStateMovement(st WorkflowState, in interface{}) StateMovement {
	return StateMovement{
		NextStateId:    GetFinalWorkflowStateId(st),
		NextStateInput: in,
	}
}
