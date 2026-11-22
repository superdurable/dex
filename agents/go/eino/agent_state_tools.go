// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexeino

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/cloudwego/eino/components/tool"

	dexagents "github.com/superdurable/dex/agents/go/core"
)

// einoSkillPackagePath is the package of Eino's skill middleware, whose skill tool may read the agent state.
const einoSkillPackagePath = "github.com/cloudwego/eino/adk/middlewares/skill"

// einoStateMissingText is part of the error Eino's skill tool returns when its call has no agent state.
const einoStateMissingText = "failed to get messages from state"

// einoToolStruct returns the struct behind an Eino tool of package packagePath and generic type typeName, if it is one.
func einoToolStruct(candidate tool.BaseTool, packagePath, typeName string) (reflect.Value, bool) {
	value := reflect.ValueOf(candidate)
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return reflect.Value{}, false
		}
		value = value.Elem()
	}
	isEinoTool := value.Kind() == reflect.Struct && value.Type().PkgPath() == packagePath &&
		strings.HasPrefix(value.Type().Name(), typeName+"[")
	return value, isEinoTool
}

// isHistoryReadingAgentTool reports whether a tool is an Eino agent tool made with adk.WithFullChatHistoryAsInput.
func isHistoryReadingAgentTool(candidate tool.BaseTool) bool {
	value, isAgentTool := einoToolStruct(candidate, einoADKPackagePath, "typedAgentTool")
	if !isAgentTool {
		return false
	}
	field := value.FieldByName("fullChatHistoryAsInput")
	return field.IsValid() && field.Kind() == reflect.Bool && field.Bool()
}

// historyReadingAgentToolReason says why a tool Step cannot run an agent tool that reads the agent's history.
func historyReadingAgentToolReason(toolName string) string {
	return fmt.Sprintf("tool %q is an Eino agent tool with WithFullChatHistoryAsInput, which reads the agent's "+
		"history in its tool Step, and a Dex tool Step does not have it", toolName)
}

// refuseHistoryReadingAgentTools returns an error naming the first agent tool that reads the agent's history.
func refuseHistoryReadingAgentTools(tools agentTools) error {
	for _, entry := range tools.ordered {
		if isHistoryReadingAgentTool(entry.tool) {
			return fmt.Errorf("%s; remove the option and pass what the sub-agent needs in the call's arguments",
				historyReadingAgentToolReason(entry.name))
		}
	}
	return nil
}

// agentStateFailure returns the definite failure of a skill call that needed the agent's history, or nil.
//
// Eino's skill tool reads the history first, so such a call failed before doing anything.
func agentStateFailure(toolName string, candidate tool.BaseTool, err error) *dexagents.ToolExecutionError {
	if _, isSkillTool := einoToolStruct(candidate, einoSkillPackagePath, "typedSkillTool"); !isSkillTool ||
		!strings.Contains(err.Error(), einoStateMissingText) {
		return nil
	}
	return &dexagents.ToolExecutionError{Message: "tool " + toolName + " runs a skill with context fork_with_context, " +
		"which reads the Eino agent's history, and a Dex tool Step does not have it; use context fork instead"}
}
