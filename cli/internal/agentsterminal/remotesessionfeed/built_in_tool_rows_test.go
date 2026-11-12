// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesessionfeed

import (
	"testing"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// An ask_user row never shows its JSON result. Live, the answer notice
// shows the answer; a history row has no notice, so it carries the answer.
func TestAskUserRowsHideTheJSONResult(t *testing.T) {
	output := `{"status":"answered","answer":"green"}`
	live := liveToolRow(dexagents.AgentToolCall{
		ToolCallID: "call-1", TurnID: "turn-1", ToolName: "ask_user",
		Arguments: `{"question":"Which color?","choices":["red","green"]}`,
		Status:    dexagents.ToolCallStatusSucceeded, Output: &output,
	})
	require.Equal(t, "Which color?", live.KeyArgument)
	require.Empty(t, live.Summary, "the answer notice shows the answer once")
	require.Equal(t, output, live.OutputText, "the pager still shows the stored result")

	waiting := liveToolRow(dexagents.AgentToolCall{ToolCallID: "call-1", ToolName: "ask_user", Arguments: `{"question":"Which color?"}`, Status: dexagents.ToolCallStatusAwaitingAnswer})
	require.Equal(t, sessionfeed.ToolCallAwaitingAnswer, waiting.Status, "a question waits for an answer, not an approval")

	toolCallID, toolName, arguments := "call-1", "ask_user", `{"question":"Which color?"}`
	history := historyToolRow(dexagents.AgentMessage{Role: dexagents.MessageRoleTool, TurnID: "turn-1", ToolCallID: &toolCallID, ToolName: &toolName, ToolArguments: &arguments, ToolOutput: &output})
	require.Equal(t, "answered: green", history.Summary)
	declined := `{"status":"declined"}`
	history = historyToolRow(dexagents.AgentMessage{Role: dexagents.MessageRoleTool, TurnID: "turn-1", ToolCallID: &toolCallID, ToolName: &toolName, ToolArguments: &arguments, ToolOutput: &declined})
	require.Equal(t, "declined", history.Summary)
}

// A write_todos row lists its steps instead of the raw todos JSON.
func TestWriteTodosRowsCarryTheirSteps(t *testing.T) {
	output := `{"status":"updated","revision":1,"task_count":3}`
	arguments := `{"todos":[{"content":"Read the file","status":"completed"},{"content":"Fix the test","status":"in_progress"},{"content":"Ship it","status":"pending"}]}`
	row := liveToolRow(dexagents.AgentToolCall{ToolCallID: "call-2", TurnID: "turn-1", ToolName: "write_todos", Arguments: arguments, Status: dexagents.ToolCallStatusSucceeded, Output: &output})
	require.Equal(t, "3 steps", row.KeyArgument)
	require.Equal(t, "plan updated", row.Summary)
	require.Equal(t, []sessionfeed.PlanItem{
		{Title: "Read the file", Status: sessionfeed.PlanItemDone},
		{Title: "Fix the test", Status: sessionfeed.PlanItemInProgress},
		{Title: "Ship it", Status: sessionfeed.PlanItemPending},
	}, row.PlanItems)

	toolCallID, toolName := "call-2", "write_todos"
	history := historyToolRow(dexagents.AgentMessage{Role: dexagents.MessageRoleTool, TurnID: "turn-1", ToolCallID: &toolCallID, ToolName: &toolName, ToolArguments: &arguments, ToolOutput: &output})
	require.Len(t, history.PlanItems, 3)
	require.Equal(t, "plan updated", history.Summary)
}
