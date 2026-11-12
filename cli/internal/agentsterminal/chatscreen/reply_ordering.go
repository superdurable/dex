// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import "github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"

// noteReplyGroup marks a reply group that starts while a tool row runs.
// The session often reports the finished row after the next Step's text.
func (screen *ChatScreen) noteReplyGroup(key replyGroupKey) {
	if !screen.replies.HasGroup(key) && screen.hasRunningToolRow() {
		screen.repliesAfterTools[key] = true
	}
}

func (screen *ChatScreen) hasRunningToolRow() bool {
	for _, call := range screen.liveToolCalls {
		if call.Status == sessionfeed.ToolCallRunning {
			return true
		}
	}
	return false
}

// releaseHeldRepliesAfterTools prints the held replies once no tool row runs.
func (screen *ChatScreen) releaseHeldRepliesAfterTools() {
	if screen.hasRunningToolRow() {
		return
	}
	screen.releaseHeldReplies()
	screen.repliesAfterTools = map[replyGroupKey]bool{}
}

func (screen *ChatScreen) releaseHeldReplies() {
	held := screen.heldReplies
	screen.heldReplies = nil
	for _, output := range held {
		screen.printReplyOutput(output)
	}
}

// flushReplies prints the unfinished reply block, unless it waits for a
// running tool row.
func (screen *ChatScreen) flushReplies() {
	screen.printReplyOutputs(screen.replies.FlushPending())
}

// flushAllReplies prints every held and unfinished reply block now, before
// a user message or a final notice.
func (screen *ChatScreen) flushAllReplies() {
	screen.releaseHeldReplies()
	for _, output := range screen.replies.FlushPending() {
		screen.printReplyOutput(output)
	}
}

// forgetSessionTurns resets what counts turns, usage, and replies for the
// session the chat leaves; turn IDs and sequences repeat across sessions.
func (screen *ChatScreen) forgetSessionTurns() {
	screen.flushAllReplies()
	screen.replies = newReplyStream()
	screen.printedSequences = map[int64]bool{}
	screen.repliesAfterTools = map[replyGroupKey]bool{}
	screen.lastTurnNumber, screen.totalTokens = 0, 0
	screen.isWritingReply = false
	screen.isPopupDeferredByTyping = false
}

// recordOpenedSession keeps the first session for the welcome area, with the
// newest details of that same session.
func (screen *ChatScreen) recordOpenedSession(opened sessionfeed.SessionOpened) {
	switch {
	case !screen.isSessionKnown:
		screen.openedSession, screen.isSessionKnown = opened, true
	case opened.SessionID == screen.openedSession.SessionID:
		opened.IsNewSession = screen.openedSession.IsNewSession
		screen.openedSession = opened
	}
}
