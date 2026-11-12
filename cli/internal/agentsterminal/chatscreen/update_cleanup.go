// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// replaceUpdateControlCharacters cleans every text field, so session text
// cannot write escape sequences.
func replaceUpdateControlCharacters(update sessionfeed.SessionUpdate) sessionfeed.SessionUpdate {
	switch typed := update.(type) {
	case sessionfeed.SessionStartFinished:
		typed.SessionID = oneLine(typed.SessionID)
		return typed
	case sessionfeed.SessionOpened:
		typed.Command, typed.Model, typed.SessionID = oneLine(typed.Command), oneLine(typed.Model), oneLine(typed.SessionID)
		typed.Workspace, typed.WebURL = oneLine(typed.Workspace), oneLine(typed.WebURL)
		return typed
	case sessionfeed.UserMessageRecorded:
		typed.Text, typed.Origin = manyLines(typed.Text), oneLine(typed.Origin)
		typed.Images = cleanAttachments(typed.Images)
		return typed
	case sessionfeed.UserMessageRejected:
		typed.Text, typed.Reason = manyLines(typed.Text), oneLine(typed.Reason)
		return typed
	case sessionfeed.ImageAttached:
		typed.Attachment = cleanAttachments([]sessionfeed.ImageAttachment{typed.Attachment})[0]
		return typed
	case sessionfeed.TurnEnded:
		typed.FailureText = oneLine(typed.FailureText)
		return typed
	case sessionfeed.AssistantTextDelta:
		typed.Text = manyLines(typed.Text)
		return typed
	case sessionfeed.AssistantMessageRecorded:
		typed.Text = manyLines(typed.Text)
		return typed
	case sessionfeed.ReasoningDelta:
		typed.Text = manyLines(typed.Text)
		return typed
	case sessionfeed.NoticeRaised:
		// The contract maps each control character, tabs included, to one space.
		typed.Text = terminaltheme.ReplaceControlCharacters(typed.Text)
		return typed
	case sessionfeed.CancelRequestAnswered:
		typed.Reason = oneLine(typed.Reason)
		return typed
	case sessionfeed.WorkerStateChanged:
		typed.ResumeCommand = oneLine(typed.ResumeCommand)
		return typed
	case sessionfeed.QueuedMessagesChanged:
		messages := make([]sessionfeed.QueuedMessage, len(typed.Messages))
		for index, message := range typed.Messages {
			messages[index] = sessionfeed.QueuedMessage{MessageID: message.MessageID, Text: oneLine(message.Text)}
		}
		typed.Messages = messages
		return typed
	case sessionfeed.PlanChanged:
		items := make([]sessionfeed.PlanItem, len(typed.Items))
		for index, item := range typed.Items {
			items[index] = sessionfeed.PlanItem{Title: oneLine(item.Title), Status: item.Status}
		}
		typed.Items = items
		return typed
	case sessionfeed.ToolCallChanged:
		return cleanToolCall(typed)
	case sessionfeed.ApprovalRequested:
		return cleanApproval(typed)
	case sessionfeed.QuestionAsked:
		typed.Question = manyLines(typed.Question)
		choices := make([]string, len(typed.Choices))
		for index, choice := range typed.Choices {
			choices[index] = oneLine(choice)
		}
		typed.Choices = choices
		return typed
	case sessionfeed.InterruptResolved:
		typed.ResolvedBy = oneLine(typed.ResolvedBy)
		return typed
	default:
		return update
	}
}

func cleanToolCall(call sessionfeed.ToolCallChanged) sessionfeed.ToolCallChanged {
	call.ToolName, call.KeyArgument = oneLine(call.ToolName), oneLine(call.KeyArgument)
	call.Summary, call.Reason = oneLine(call.Summary), oneLine(call.Reason)
	outputTail := make([]string, len(call.OutputTail))
	for index, line := range call.OutputTail {
		outputTail[index] = oneLine(line)
	}
	call.OutputTail = outputTail
	return call
}

func cleanApproval(approval sessionfeed.ApprovalRequested) sessionfeed.ApprovalRequested {
	approval.ToolName, approval.SourceLabel = oneLine(approval.ToolName), oneLine(approval.SourceLabel)
	approval.ScopeLabel, approval.EffectLabel = oneLine(approval.ScopeLabel), oneLine(approval.EffectLabel)
	approval.EffectSummary = oneLine(approval.EffectSummary)
	arguments := make([]sessionfeed.ApprovalArgument, len(approval.Arguments))
	for index, argument := range approval.Arguments {
		arguments[index] = sessionfeed.ApprovalArgument{Name: oneLine(argument.Name), Value: manyLines(argument.Value)}
	}
	approval.Arguments = arguments
	if approval.Preview != nil {
		preview := *approval.Preview
		preview.Path = oneLine(preview.Path)
		lines := make([]sessionfeed.DiffLine, len(preview.Lines))
		for index, line := range preview.Lines {
			line.Text = oneLine(terminaltheme.ExpandTabs(line.Text))
			lines[index] = line
		}
		preview.Lines = lines
		approval.Preview = &preview
	}
	return approval
}

func cleanAttachments(attachments []sessionfeed.ImageAttachment) []sessionfeed.ImageAttachment {
	cleaned := make([]sessionfeed.ImageAttachment, len(attachments))
	for index, attachment := range attachments {
		attachment.Name, attachment.MimeType = oneLine(attachment.Name), oneLine(attachment.MimeType)
		cleaned[index] = attachment
	}
	return cleaned
}

func oneLine(text string) string {
	return terminaltheme.ReplaceControlCharacters(terminaltheme.ExpandTabs(text))
}

func manyLines(text string) string {
	return terminaltheme.ReplaceControlCharactersKeepingLines(text)
}
