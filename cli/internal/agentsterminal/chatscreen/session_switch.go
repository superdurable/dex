// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// SwitchSessionRequestMsg asks the screen to switch sessions, as /sessions
// does. Attached images are dropped only after the user agrees.
type SwitchSessionRequestMsg struct {
	// SessionID is the session to show.
	SessionID string
	// IsResumed loads the session's history.
	IsResumed bool
}

// requestSessionSwitch switches at once without attached images, and asks
// "Drop N attached images?" first with them.
func (screen *ChatScreen) requestSessionSwitch(request sessionfeed.SwitchSession) tea.Cmd {
	if len(screen.attachments) == 0 {
		return screen.submitAction(request)
	}
	screen.pendingImageDrop = &request
	return nil
}

// handleImageDropKey answers the drop question: y drops the images and
// switches; any other key stays in this session.
func (screen *ChatScreen) handleImageDropKey(keyName string) tea.Cmd {
	request := *screen.pendingImageDrop
	screen.pendingImageDrop = nil
	if keyName != "y" {
		screen.printNotice(sessionfeed.NoticeSeverityInfo, "staying in this session; resume "+request.SessionID+" later with /sessions")
		return nil
	}
	screen.printNotice(sessionfeed.NoticeSeverityInfo, "dropped "+attachedImagesText(len(screen.attachments)))
	screen.attachments = nil
	return screen.submitAction(request)
}

// imageDropQuestion is the pending row of the drop question.
func (screen *ChatScreen) imageDropQuestion() string {
	return fmt.Sprintf(" Drop %s to switch to %s?", attachedImagesText(len(screen.attachments)), screen.pendingImageDrop.SessionID)
}

func attachedImagesText(count int) string {
	if count == 1 {
		return "1 attached image"
	}
	return fmt.Sprintf("%d attached images", count)
}
