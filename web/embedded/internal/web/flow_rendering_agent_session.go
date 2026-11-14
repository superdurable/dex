// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"encoding/json"
	"fmt"

	"github.com/superdurable/dex/web/embedded/internal/web/api"
)

const agentSessionStartStepType = "StartAgentSession"

// agentSessionQuestionStepType is the Step that waits for an answer; a Go session has none.
const agentSessionQuestionStepType = "AwaitQuestionAnswer"

// agentSessionAttributes maps each required session Attribute to whether it is an AttributeMap.
var agentSessionAttributes = map[string]bool{
	"AgentState":        false,
	"AgentMessages":     true,
	"AgentToolCalls":    true,
	"PendingInterrupts": false,
}

// loadAgentSession returns the session parts of a graph, or nil when the graph is not a Dex Agents session.
func loadAgentSession(rawNodes []json.RawMessage, language string) (*api.V2AgentSession, error) {
	type graphNode struct {
		Kind     string `json:"kind"`
		Name     string `json:"name"`
		Phase    string `json:"phase"`
		Start    bool   `json:"start"`
		Resource *struct {
			Map bool `json:"map"`
		} `json:"resource"`
	}
	foundAttributes := make(map[string]bool, len(agentSessionAttributes))
	isStartStepFound := false
	session := &api.V2AgentSession{RPCNames: make(map[string]bool), Language: language}
	for _, raw := range rawNodes {
		var node graphNode
		if err := json.Unmarshal(raw, &node); err != nil {
			return nil, fmt.Errorf("malformed node: %w", err)
		}
		switch node.Kind {
		case "attribute":
			isMap, isSessionAttribute := agentSessionAttributes[node.Name]
			if isSessionAttribute && isMap == (node.Resource != nil && node.Resource.Map) {
				foundAttributes[node.Name] = true
			}
		case "step":
			if node.Name == agentSessionQuestionStepType {
				session.HasQuestions = true
			}
			if node.Name == agentSessionStartStepType && node.Start {
				isStartStepFound = true
				session.StartStepSkipWaitFor = node.Phase == "execute"
			}
		case "rpc":
			session.RPCNames[node.Name] = true
		}
	}
	if len(foundAttributes) != len(agentSessionAttributes) || !isStartStepFound {
		return nil, nil
	}
	return session, nil
}
