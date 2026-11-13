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
	"strconv"
	"strings"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// readEarlierMessages reads one stored page before request.BeforeSequence
// and answers with EarlierMessagesRead, also when the read fails.
func (feed *RemoteSessionFeed) readEarlierMessages(ctx context.Context, request sessionfeed.ReadEarlierMessages) {
	answer := sessionfeed.EarlierMessagesRead{SessionID: request.SessionID, BeforeSequence: request.BeforeSequence, RequestID: request.RequestID}
	readContext, cancel := context.WithTimeout(ctx, feed.timings.SnapshotTimeout)
	beforeSequence := request.BeforeSequence
	page, err := feed.sessions.ListAgentMessages(readContext, request.SessionID, &beforeSequence, historyPageSize)
	cancel()
	if err != nil {
		feed.debugf("read earlier messages", err)
		answer.FailureText = "Could not read earlier messages: " + describeError(err) + ". Try /history again."
		feed.emit(answer)
		return
	}
	if page.NextBeforeSequence != nil {
		answer.EarlierBeforeSequence = *page.NextBeforeSequence
	}
	if page.DeletedBeforeSequence != nil {
		answer.DeletedBeforeSequence = *page.DeletedBeforeSequence
	}
	for _, message := range page.Messages {
		if update, isShown := earlierMessageUpdate(message); isShown {
			answer.Messages = append(answer.Messages, update)
		}
	}
	feed.emit(answer)
}

// earlierMessageUpdate converts one stored message of an earlier page, and
// reports false for a message with nothing to show.
func earlierMessageUpdate(message dexagents.AgentMessage) (sessionfeed.SessionUpdate, bool) {
	text := ""
	if message.Text != nil {
		text = *message.Text
	}
	switch message.Role {
	case dexagents.MessageRoleUser:
		return sessionfeed.UserMessageRecorded{Text: text, TurnID: message.TurnID, Images: storedImages(message.Images), SenderName: storedSenderName(message)}, true
	case dexagents.MessageRoleAssistant:
		if strings.TrimSpace(text) == "" {
			return nil, false
		}
		stepExecutionID := "record-" + strconv.FormatInt(message.Sequence, 10)
		return sessionfeed.AssistantMessageRecorded{TurnID: message.TurnID, StepExecutionID: stepExecutionID, Text: text}, true
	case dexagents.MessageRoleTool:
		if message.ToolCallID == nil {
			return nil, false
		}
		return historyToolRow(message), true
	}
	return nil, false
}
