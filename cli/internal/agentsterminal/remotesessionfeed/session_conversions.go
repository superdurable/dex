// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesessionfeed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// failureTailLines is how many error lines a failed tool row keeps.
const failureTailLines = 3

// keyArgumentNames are the arguments that identify a call, most specific first.
var keyArgumentNames = []string{"path", "file_path", "command", "url", "query", "pattern", "message", "question"}

// liveToolRow converts a snapshot tool call record of the running turn.
func liveToolRow(call dexagents.AgentToolCall) sessionfeed.ToolCallChanged {
	row := sessionfeed.ToolCallChanged{
		ToolCallID:        call.ToolCallID,
		TurnID:            call.TurnID,
		ToolName:          call.ToolName,
		KeyArgument:       keyArgument(call.Arguments),
		Status:            toolCallStatus(call),
		IsOutputTruncated: call.TruncatedOutputBytes != nil && *call.TruncatedOutputBytes > 0,
		Arguments:         approvalArguments(call.Arguments),
		ImageCount:        len(call.Images),
	}
	switch row.Status {
	case sessionfeed.ToolCallSucceeded:
		if call.Output != nil {
			row.Summary = firstLine(*call.Output)
			row.OutputText = *call.Output
		}
	case sessionfeed.ToolCallFailed, sessionfeed.ToolCallOutcomeUnknown:
		if call.Error != nil {
			row.OutputTail, row.HiddenOutputLineCount = headLines(*call.Error, failureTailLines)
			row.OutputText = *call.Error
		}
	case sessionfeed.ToolCallRejected:
		if call.RejectionMessage != nil {
			row.Reason = *call.RejectionMessage
		}
	case sessionfeed.ToolCallCancelled:
		row.Reason = "the turn was cancelled"
		if call.CancellationReason != nil && *call.CancellationReason == dexagents.ToolCallCancellationReasonSteered {
			row.Reason = "a steered message replaced the turn"
		}
	}
	describeBuiltInToolRow(&row, call.Arguments, false)
	return row
}

// The built-in tools whose rows show their meaning instead of raw JSON.
const (
	askUserToolName    = "ask_user"
	writeTodosToolName = "write_todos"
)

// describeBuiltInToolRow replaces the JSON of ask_user and write_todos rows.
// A live answer shows in its own notice, so only a history row carries it.
func describeBuiltInToolRow(row *sessionfeed.ToolCallChanged, arguments string, isHistory bool) {
	switch row.ToolName {
	case askUserToolName:
		if !row.Status.IsFinished() || row.Status == sessionfeed.ToolCallFailed {
			return
		}
		row.Summary = ""
		var result struct {
			Status string `json:"status"`
			Answer string `json:"answer"`
		}
		if json.Unmarshal([]byte(row.OutputText), &result) != nil || !isHistory {
			return
		}
		switch result.Status {
		case "answered":
			row.Summary = "answered: " + firstLine(result.Answer)
		case "declined":
			row.Summary = "declined"
		}
	case writeTodosToolName:
		var written struct {
			Todos []struct {
				Content string                   `json:"content"`
				Status  dexagents.PlanTaskStatus `json:"status"`
			} `json:"todos"`
		}
		if json.Unmarshal([]byte(arguments), &written) != nil {
			return
		}
		row.PlanItems = make([]sessionfeed.PlanItem, 0, len(written.Todos))
		for _, todo := range written.Todos {
			row.PlanItems = append(row.PlanItems, sessionfeed.PlanItem{Title: firstLine(todo.Content), Status: planItemStatus(todo.Status)})
		}
		row.KeyArgument = stepCountText(len(written.Todos))
		if row.Status.IsFinished() && row.Status != sessionfeed.ToolCallFailed {
			row.Summary = "plan updated"
			if len(written.Todos) == 0 {
				row.Summary = "plan cleared"
			}
		}
	}
}

func stepCountText(count int) string {
	if count == 1 {
		return "1 step"
	}
	return fmt.Sprintf("%d steps", count)
}

// toolCallStatus maps a record's status to the screen's display state.
func toolCallStatus(call dexagents.AgentToolCall) sessionfeed.ToolCallStatus {
	if call.Skipped {
		return sessionfeed.ToolCallSkipped
	}
	if call.CancellationReason != nil && call.Status == dexagents.ToolCallStatusRejected {
		return sessionfeed.ToolCallCancelled
	}
	switch call.Status {
	case dexagents.ToolCallStatusAwaitingApproval:
		return sessionfeed.ToolCallAwaitingApproval
	case dexagents.ToolCallStatusAwaitingAnswer:
		return sessionfeed.ToolCallAwaitingAnswer
	case dexagents.ToolCallStatusWaitingForTimer, dexagents.ToolCallStatusWaitingForTask:
		return sessionfeed.ToolCallQueued
	case dexagents.ToolCallStatusRejected:
		return sessionfeed.ToolCallRejected
	case dexagents.ToolCallStatusSucceeded:
		return sessionfeed.ToolCallSucceeded
	case dexagents.ToolCallStatusFailed:
		return sessionfeed.ToolCallFailed
	case dexagents.ToolCallStatusOutcomeUnknown:
		return sessionfeed.ToolCallOutcomeUnknown
	}
	if call.WriteQueued {
		return sessionfeed.ToolCallQueued
	}
	return sessionfeed.ToolCallRunning
}

// historyToolRow converts a stored tool message, which keeps no reliable
// status.
func historyToolRow(message dexagents.AgentMessage) sessionfeed.ToolCallChanged {
	row := sessionfeed.ToolCallChanged{Status: sessionfeed.ToolCallFromHistory, TurnID: message.TurnID, ImageCount: len(message.Images)}
	if message.ToolCallID != nil {
		row.ToolCallID = *message.ToolCallID
	}
	if message.ToolName != nil {
		row.ToolName = *message.ToolName
	}
	if message.ToolArguments != nil {
		row.KeyArgument = keyArgument(*message.ToolArguments)
		row.Arguments = approvalArguments(*message.ToolArguments)
	}
	if message.ToolOutput != nil {
		row.Summary = firstLine(*message.ToolOutput)
		row.OutputText = *message.ToolOutput
	}
	arguments := ""
	if message.ToolArguments != nil {
		arguments = *message.ToolArguments
	}
	describeBuiltInToolRow(&row, arguments, true)
	return row
}

// interruptUpdate converts a pending interrupt into an approval or a question.
func interruptUpdate(interrupt dexagents.AgentInterrupt, calls []dexagents.AgentToolCall) sessionfeed.SessionUpdate {
	if interrupt.Kind == dexagents.InterruptKindQuestion {
		question := sessionfeed.QuestionAsked{InterruptID: interrupt.InterruptID, Choices: interrupt.Choices, AllowsFreeText: interrupt.AllowsFreeText}
		if interrupt.Question != nil {
			question.Question = *interrupt.Question
		}
		return question
	}
	approval := sessionfeed.ApprovalRequested{
		InterruptID: interrupt.InterruptID,
		ToolCallID:  interrupt.ToolCallID,
		ToolName:    interrupt.ToolName,
		Arguments:   approvalArguments(interrupt.Arguments),
	}
	for _, call := range calls {
		if call.ToolCallID == interrupt.ToolCallID {
			approval.EffectLabel = effectLabel(call.Effect)
		}
	}
	if interrupt.Deadline != nil {
		if deadline, err := time.Parse(time.RFC3339Nano, *interrupt.Deadline); err == nil {
			approval.Deadline = deadline
		}
	}
	if display := interrupt.Display; display != nil {
		approval.ScopeLabel, approval.EffectSummary = display.Target, display.Summary
		if display.Preview != nil {
			if preview, isDiff := fileChangePreview(*display.Preview); isDiff {
				approval.Preview = &preview
			} else {
				approval.Arguments = append(approval.Arguments, sessionfeed.ApprovalArgument{Name: "preview", Value: *display.Preview})
			}
		}
	}
	return approval
}

func effectLabel(effect dexagents.ToolEffect) string {
	switch effect {
	case dexagents.ToolEffectRead:
		return "read"
	case dexagents.ToolEffectIdempotentWrite, dexagents.ToolEffectNonIdempotentWrite:
		return "write"
	case "":
		return ""
	}
	return "unclassified"
}

// approvalArguments returns a JSON object's members in their written order.
// Text that is not an object becomes one "arguments" row.
func approvalArguments(arguments string) []sessionfeed.ApprovalArgument {
	decoder := json.NewDecoder(strings.NewReader(arguments))
	token, err := decoder.Token()
	if delimiter, isDelimiter := token.(json.Delim); err != nil || !isDelimiter || delimiter != '{' {
		if strings.TrimSpace(arguments) == "" {
			return nil
		}
		return []sessionfeed.ApprovalArgument{{Name: "arguments", Value: arguments}}
	}
	var rows []sessionfeed.ApprovalArgument
	for decoder.More() {
		nameToken, err := decoder.Token()
		if err != nil {
			break
		}
		name, _ := nameToken.(string)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			break
		}
		rows = append(rows, sessionfeed.ApprovalArgument{Name: name, Value: argumentText(value)})
	}
	return rows
}

func argumentText(value json.RawMessage) string {
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		return text
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, value); err != nil {
		return string(value)
	}
	return compact.String()
}

// keyArgument picks the argument that identifies a call.
func keyArgument(arguments string) string {
	var object map[string]any
	if err := json.Unmarshal([]byte(arguments), &object); err != nil {
		return ""
	}
	for _, name := range keyArgumentNames {
		if text, isString := object[name].(string); isString && text != "" {
			return firstLine(text)
		}
	}
	for _, row := range approvalArguments(arguments) {
		if row.Value != "" {
			return firstLine(row.Value)
		}
	}
	return ""
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(line)
}

func headLines(text string, count int) ([]string, int) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) <= count {
		return lines, 0
	}
	return lines[:count], len(lines) - count
}

func planStatus(status dexagents.PlanStatus) sessionfeed.PlanStatus {
	switch status {
	case dexagents.PlanStatusActive:
		return sessionfeed.PlanStatusActive
	case dexagents.PlanStatusCompleted:
		return sessionfeed.PlanStatusCompleted
	case dexagents.PlanStatusCleared:
		return sessionfeed.PlanStatusCleared
	default:
		return sessionfeed.PlanStatusDraft
	}
}

func planItemStatus(status dexagents.PlanTaskStatus) sessionfeed.PlanItemStatus {
	switch status {
	case dexagents.PlanTaskStatusInProgress:
		return sessionfeed.PlanItemInProgress
	case dexagents.PlanTaskStatusCompleted:
		return sessionfeed.PlanItemDone
	default:
		return sessionfeed.PlanItemPending
	}
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%d KB", bytes>>10)
	}
	return fmt.Sprintf("%d B", bytes)
}
