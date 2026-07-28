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

type StateDecision struct {
	NextStates []StateMovement
}

func SingleNextState(state WorkflowState, input interface{}) *StateDecision {
	return &StateDecision{
		NextStates: []StateMovement{
			{
				NextStateId:    GetFinalWorkflowStateId(state),
				NextStateInput: input,
			},
		},
	}
}

func MultiNextStates(states ...WorkflowState) *StateDecision {
	var movements []StateMovement
	for _, st := range states {
		movements = append(movements, StateMovement{
			NextStateId: GetFinalWorkflowStateId(st),
		})
	}
	return &StateDecision{
		NextStates: movements,
	}
}

func MultiNextStatesWithInput(movements ...StateMovement) *StateDecision {
	return &StateDecision{
		NextStates: movements,
	}
}

func MultiNextStatesByStateIds(nextStateIds ...string) *StateDecision {
	var movements []StateMovement
	for _, id := range nextStateIds {
		movements = append(movements, StateMovement{
			NextStateId: id,
		})
	}
	return &StateDecision{
		NextStates: movements,
	}
}

var ForceFailingWorkflow = ForceFailWorkflow(nil)

func ForceFailWorkflow(output interface{}) *StateDecision {
	return &StateDecision{
		NextStates: []StateMovement{
			{
				NextStateId:    ForceFailingWorkflowStateId,
				NextStateInput: output,
			},
		},
	}
}

var DeadEnd = &StateDecision{
	NextStates: []StateMovement{
		{
			NextStateId: DeadEndStateId,
		},
	},
}

var GracefulCompletingWorkflow = GracefulCompleteWorkflow(nil)

func GracefulCompleteWorkflow(output interface{}) *StateDecision {
	return &StateDecision{
		NextStates: []StateMovement{
			{
				NextStateId:    GracefulCompletingWorkflowStateId,
				NextStateInput: output,
			},
		},
	}
}

var ForceCompletingWorkflow = ForceCompleteWorkflow(nil)

func ForceCompleteWorkflow(output interface{}) *StateDecision {
	return &StateDecision{
		NextStates: []StateMovement{
			{
				NextStateId:    ForceCompletingWorkflowStateId,
				NextStateInput: output,
			},
		},
	}
}
