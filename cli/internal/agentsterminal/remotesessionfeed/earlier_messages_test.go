// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesessionfeed

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

func toolMessage(sequence int64, toolCallID *string, name string, arguments string, output string) dexagents.AgentMessage {
	return dexagents.AgentMessage{
		Sequence: sequence, TurnID: "turn-1", Role: dexagents.MessageRoleTool,
		ToolCallID: toolCallID, ToolName: &name, ToolArguments: &arguments, ToolOutput: &output,
		Images: []dexagents.AgentImageReference{{MimeType: "image/png", SizeBytes: 10}},
	}
}

// /history reads the named session's page before the named sequence, and
// drops empty items.
func TestReadEarlierMessagesAnswersWithOnePage(t *testing.T) {
	harness := newModelHarness(t)
	callID := "call-1"
	next := int64(3)
	var askedSession string
	var askedBefore, askedLimit int64
	harness.sessions.listMessages = func(sessionID string, beforeSequence *int64, limit int64) (dexagents.AgentMessagesPage, error) {
		askedSession, askedBefore, askedLimit = sessionID, *beforeSequence, limit
		return dexagents.AgentMessagesPage{NextBeforeSequence: &next, Messages: []dexagents.AgentMessage{
			message(3, "turn-1", dexagents.MessageRoleUser, "read go.mod"),
			message(4, "turn-1", dexagents.MessageRoleAssistant, "  "),
			toolMessage(5, &callID, "read_file", `{"path":"go.mod"}`, "module example.com/app\ngo 1.26"),
			toolMessage(6, nil, "read_file", `{}`, "no call ID"),
			message(7, "turn-1", dexagents.MessageRoleAssistant, "It is **example.com/app**."),
		}}, nil
	}
	require.NoError(t, harness.feed.SubmitUserAction(context.Background(), sessionfeed.ReadEarlierMessages{SessionID: "session-0", BeforeSequence: 8}))
	answers := updatesOfType[sessionfeed.EarlierMessagesRead](harness.drain())
	require.Len(t, answers, 1)
	require.Equal(t, "session-0", askedSession, "the page comes from the session the screen showed")
	require.Equal(t, int64(8), askedBefore)
	require.Equal(t, int64(historyPageSize), askedLimit)
	answer := answers[0]
	require.Equal(t, "session-0", answer.SessionID)
	require.Equal(t, int64(8), answer.BeforeSequence)
	require.Equal(t, int64(3), answer.EarlierBeforeSequence)
	require.Empty(t, answer.FailureText)
	require.Len(t, answer.Messages, 3)
	require.Equal(t, "read go.mod", answer.Messages[0].(sessionfeed.UserMessageRecorded).Text)
	tool := answer.Messages[1].(sessionfeed.ToolCallChanged)
	require.Equal(t, sessionfeed.ToolCallFromHistory, tool.Status)
	require.Equal(t, "go.mod", tool.KeyArgument)
	require.Equal(t, "module example.com/app", tool.Summary)
	require.Equal(t, "module example.com/app\ngo 1.26", tool.OutputText)
	require.Equal(t, []sessionfeed.ApprovalArgument{{Name: "path", Value: "go.mod"}}, tool.Arguments)
	require.Equal(t, 1, tool.ImageCount)
	require.Equal(t, "It is **example.com/app**.", answer.Messages[2].(sessionfeed.AssistantMessageRecorded).Text)
}

func TestReadEarlierMessagesReportsAFailedRead(t *testing.T) {
	harness := newModelHarness(t)
	harness.sessions.listMessages = func(string, *int64, int64) (dexagents.AgentMessagesPage, error) {
		return dexagents.AgentMessagesPage{}, errors.New("connection refused")
	}
	require.NoError(t, harness.feed.SubmitUserAction(context.Background(), sessionfeed.ReadEarlierMessages{SessionID: "session-1", BeforeSequence: 8}))
	answers := updatesOfType[sessionfeed.EarlierMessagesRead](harness.drain())
	require.Len(t, answers, 1)
	require.Contains(t, answers[0].FailureText, "Could not read earlier messages")
	require.Contains(t, answers[0].FailureText, "Try /history again.")
	require.Empty(t, answers[0].Messages)
}

// Ctrl-O shows a finished call's whole output, or a failed call's error.
func TestLiveToolRowsCarryTheirOutput(t *testing.T) {
	output := "line 1\nline 2"
	row := liveToolRow(dexagents.AgentToolCall{
		ToolCallID: "call-1", TurnID: "turn-2", ToolName: "run_shell_command", Arguments: `{"command":"go test","cwd":"."}`,
		Status: dexagents.ToolCallStatusSucceeded, Output: &output,
		Images: []dexagents.AgentImageReference{{MimeType: "image/png"}, {MimeType: "image/png"}},
	})
	require.Equal(t, "turn-2", row.TurnID)
	require.Equal(t, output, row.OutputText)
	require.Equal(t, 2, row.ImageCount)
	require.Equal(t, []sessionfeed.ApprovalArgument{{Name: "command", Value: "go test"}, {Name: "cwd", Value: "."}}, row.Arguments)
	failure := "exit 1\nFAIL TestRetry"
	failed := liveToolRow(dexagents.AgentToolCall{ToolCallID: "call-2", ToolName: "run_shell_command", Arguments: `{}`, Status: dexagents.ToolCallStatusFailed, Error: &failure})
	require.Equal(t, failure, failed.OutputText)
}

// A removed chip no longer counts against the per-message image limit.
func TestDetachImageFreesItsPlace(t *testing.T) {
	harness := newModelHarness(t)
	harness.feed.attachedImages = 2
	require.NoError(t, harness.feed.SubmitUserAction(context.Background(), sessionfeed.DetachImage{}))
	require.Equal(t, 1, harness.feed.attachedImages)
	harness.feed.attachedImages = 0
	require.NoError(t, harness.feed.SubmitUserAction(context.Background(), sessionfeed.DetachImage{}))
	require.Equal(t, 0, harness.feed.attachedImages)
}
