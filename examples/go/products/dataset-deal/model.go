// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package datasetdeal

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

type DealProcess struct {
	ProcessID        string            `json:"processID"`
	InitialState     string            `json:"initialState"`
	InitialStateData map[string]string `json:"initialStateData"`
	States           []StateDefinition `json:"states"`
}

type StateDefinition struct {
	Name          string             `json:"name"`
	PreCondition  *ExternalCondition `json:"preCondition,omitempty"`
	PreActions    []string           `json:"preActions,omitempty"`
	PostActions   []string           `json:"postActions,omitempty"`
	PostCondition *PostCondition     `json:"postCondition,omitempty"`
}

type ExternalCondition struct {
	Name string `json:"name"`
}

type PostCondition struct {
	WaitFor  *ExternalCondition `json:"waitFor,omitempty"`
	Decision DecisionExpression `json:"decision"`
}

type DecisionExpression struct {
	Key       string      `json:"key,omitempty"`
	Cases     []EqualCase `json:"cases,omitempty"`
	ElseState string      `json:"elseState"`
}

type EqualCase struct {
	Equals    string `json:"equals"`
	GoToState string `json:"goToState"`
}

type DealExecution struct {
	FlowID                   string            `json:"flowID"`
	RunID                    string            `json:"runID"`
	ProcessID                string            `json:"processID"`
	ProcessDefinition        DealProcess       `json:"processDefinition"`
	BuyerID                  string            `json:"buyerID"`
	CurrentState             string            `json:"currentState"`
	CurrentActionIndex       int               `json:"currentActionIndexToExecute"`
	PendingPreConditionState string            `json:"pendingPreConditionState"`
	PendingPreConditionName  string            `json:"pendingPreConditionName"`
	StateData                map[string]string `json:"stateData"`
	Status                   string            `json:"status"`
	StartedAt                time.Time         `json:"startedAt"`
	ClosedAt                 *time.Time        `json:"closedAt,omitempty"`
}

type actionPhase string

const (
	preActionPhase  actionPhase = "pre"
	postActionPhase actionPhase = "post"
)

type stateStepInput struct {
	StateName string `json:"stateName"`
}

type actionStepInput struct {
	StateName string      `json:"stateName"`
	Phase     actionPhase `json:"phase"`
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func ValidateProcess(process DealProcess) error {
	availableActions := make(map[string]struct{}, len(AvailableActionNames()))
	for _, name := range AvailableActionNames() {
		availableActions[name] = struct{}{}
	}
	return process.validate(availableActions)
}

func (process DealProcess) validate(availableActions map[string]struct{}) error {
	if err := validateIdentifier("processID", process.ProcessID); err != nil {
		return err
	}
	if len(process.States) == 0 {
		return fmt.Errorf("deal process requires at least one state")
	}
	states := make(map[string]StateDefinition, len(process.States))
	for _, state := range process.States {
		if err := validateIdentifier("state name", state.Name); err != nil {
			return err
		}
		if _, found := states[state.Name]; found {
			return fmt.Errorf("duplicate state %q", state.Name)
		}
		states[state.Name] = state
	}
	if _, found := states[process.InitialState]; !found {
		return fmt.Errorf("initial state %q is not defined", process.InitialState)
	}

	conditions := make(map[string]string)
	for _, state := range process.States {
		if err := validateState(state, states, conditions, availableActions); err != nil {
			return fmt.Errorf("state %q: %w", state.Name, err)
		}
	}
	for key := range process.InitialStateData {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("initial stateData keys must not be empty")
		}
	}
	return nil
}

func (process DealProcess) State(name string) (StateDefinition, error) {
	for _, state := range process.States {
		if state.Name == name {
			return state, nil
		}
	}
	return StateDefinition{}, fmt.Errorf("state %q is not defined", name)
}

func (process DealProcess) HasCondition(name string) bool {
	for _, state := range process.States {
		if state.PreCondition != nil && state.PreCondition.Name == name {
			return true
		}
		if state.PostCondition != nil && state.PostCondition.WaitFor != nil &&
			state.PostCondition.WaitFor.Name == name {
			return true
		}
	}
	return false
}

func validateState(
	state StateDefinition,
	states map[string]StateDefinition,
	conditions map[string]string,
	availableActions map[string]struct{},
) error {
	if state.PreCondition != nil {
		if err := validateCondition(state.Name, *state.PreCondition, conditions); err != nil {
			return err
		}
	}
	for _, actionName := range append(append([]string{}, state.PreActions...), state.PostActions...) {
		if _, found := availableActions[actionName]; !found {
			return fmt.Errorf("action %q is not available", actionName)
		}
	}
	if state.PostCondition == nil {
		return nil
	}
	if state.PostCondition.WaitFor != nil {
		if err := validateCondition(state.Name, *state.PostCondition.WaitFor, conditions); err != nil {
			return err
		}
	}
	return validateDecision(state.PostCondition.Decision, states)
}

func validateCondition(
	stateName string,
	condition ExternalCondition,
	conditions map[string]string,
) error {
	if err := validateIdentifier("condition name", condition.Name); err != nil {
		return err
	}
	if existingState, found := conditions[condition.Name]; found {
		return fmt.Errorf(
			"condition %q is already used by state %q",
			condition.Name,
			existingState,
		)
	}
	conditions[condition.Name] = stateName
	return nil
}

func validateDecision(
	decision DecisionExpression,
	states map[string]StateDefinition,
) error {
	if _, found := states[decision.ElseState]; !found {
		return fmt.Errorf("else state %q is not defined", decision.ElseState)
	}
	if len(decision.Cases) == 0 {
		return nil
	}
	if strings.TrimSpace(decision.Key) == "" {
		return fmt.Errorf("decision key is required when cases are defined")
	}
	values := make(map[string]struct{}, len(decision.Cases))
	for _, conditionCase := range decision.Cases {
		if _, found := values[conditionCase.Equals]; found {
			return fmt.Errorf("duplicate equals value %q", conditionCase.Equals)
		}
		values[conditionCase.Equals] = struct{}{}
		if _, found := states[conditionCase.GoToState]; !found {
			return fmt.Errorf("case state %q is not defined", conditionCase.GoToState)
		}
	}
	return nil
}

func validateIdentifier(field string, value string) error {
	if !identifierPattern.MatchString(value) {
		return fmt.Errorf("%s %q must match %s", field, value, identifierPattern)
	}
	return nil
}
