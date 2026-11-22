// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexeino

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"

	dexagents "github.com/superdurable/dex/agents/go/core"
)

// einoSkillPackagePath is the package of Eino's skill middleware, whose skill tool may read the agent state.
const einoSkillPackagePath = "github.com/cloudwego/eino/adk/middlewares/skill"

// einoStateMissingText is part of the error Eino's skill tool returns when its call has no agent state.
const einoStateMissingText = "failed to get messages from state"

// einoNoAgentHubText is part of the error Eino's skill tool returns for a fork skill when it has no AgentHub.
const einoNoAgentHubText = "AgentHub is not configured"

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

// agentStateFailure returns the definite failure of a skill call that needed the agent's history or an AgentHub,
// or nil.
//
// Eino's skill tool reads the history first, so such a call failed before doing anything.
func agentStateFailure(toolName string, candidate tool.BaseTool, err error) *dexagents.ToolExecutionError {
	skillTool, isSkillTool := einoToolStruct(candidate, einoSkillPackagePath, "typedSkillTool")
	switch {
	case !isSkillTool:
		return nil
	case strings.Contains(err.Error(), einoStateMissingText):
		return &dexagents.ToolExecutionError{Message: "tool " + toolName + " runs a skill with context fork_with_context, " +
			"which reads the Eino agent's history, and a Dex tool Step does not have it; use context fork instead"}
	case hasNoAgentHub(skillTool) && strings.Contains(err.Error(), einoNoAgentHubText):
		return &dexagents.ToolExecutionError{Message: fmt.Sprintf("tool %s did not run: %s; fork skills need "+
			"skill.NewMiddleware with Config.AgentHub", toolName, innermostError(err).Error())}
	default:
		return nil
	}
}

// hasNoAgentHub reports whether a skill tool has no AgentHub, so it never runs a sub-agent whose tools could fail.
func hasNoAgentHub(skillTool reflect.Value) bool {
	field := skillTool.FieldByName("agentHub")
	return field.IsValid() && field.Kind() == reflect.Interface && field.IsNil()
}

// innermostError returns the error that err wraps at the end of its chain.
func innermostError(err error) error {
	for unwrapped := errors.Unwrap(err); unwrapped != nil; unwrapped = errors.Unwrap(err) {
		err = unwrapped
	}
	return err
}

// runsAnEinoAgent reports whether a tool runs a whole Eino agent in its call: an adk.NewAgentTool tool, or the skill
// middleware's tool, whose fork skills run a sub-agent.
func runsAnEinoAgent(candidate tool.BaseTool) bool {
	_, isAgentTool := einoToolStruct(candidate, einoADKPackagePath, "typedAgentTool")
	_, isSkillTool := einoToolStruct(candidate, einoSkillPackagePath, "typedSkillTool")
	return isAgentTool || isSkillTool
}

// subAgentFailures is the innermost tool middleware of a call, whose effect is not read, of a tool that runs an Eino agent.
//
// Eino returns the error of a sub-agent's tool from the agent tool unchanged
// or wrapped, so a *dexagents.ToolExecutionError there is the inner tool's
// answer, given after the sub-agent's earlier tools may have taken effect. The
// middleware turns it into an error without that type, so the session decides
// the call's outcome from its effect, as for any other tool error. It wraps
// only the tool, so a refusal before the tool runs stays definite.
func subAgentFailures(toolName string) compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return subAgentFailureEndpoint(toolName, next)
		},
		Streamable: func(next compose.StreamableToolEndpoint) compose.StreamableToolEndpoint {
			return subAgentFailureEndpoint(toolName, next)
		},
		EnhancedInvokable: func(next compose.EnhancedInvokableToolEndpoint) compose.EnhancedInvokableToolEndpoint {
			return subAgentFailureEndpoint(toolName, next)
		},
		EnhancedStreamable: func(next compose.EnhancedStreamableToolEndpoint) compose.EnhancedStreamableToolEndpoint {
			return subAgentFailureEndpoint(toolName, next)
		},
	}
}

// subAgentFailureEndpoint returns next's error without its *dexagents.ToolExecutionError type.
func subAgentFailureEndpoint[Output any](
	toolName string,
	next func(ctx context.Context, input *compose.ToolInput) (Output, error),
) func(ctx context.Context, input *compose.ToolInput) (Output, error) {
	return func(ctx context.Context, input *compose.ToolInput) (Output, error) {
		output, err := next(ctx, input)
		var toolError *dexagents.ToolExecutionError
		if err == nil || !errors.As(err, &toolError) {
			return output, err
		}
		var failed Output
		return failed, fmt.Errorf("tool %s runs an Eino agent, and a tool of that agent failed, "+
			"so the agent's earlier tools may have taken effect: %s", toolName, err.Error())
	}
}
