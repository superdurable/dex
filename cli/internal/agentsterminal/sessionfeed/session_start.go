// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package sessionfeed

// SessionStartChanged reports whether /new waits in the foreground for its
// new session. While it waits, Esc moves the wait to the background with
// MoveSessionStartToBackground.
type SessionStartChanged struct {
	// IsWaiting is true from the start of /new until its result arrives or
	// its wait moves to the background.
	IsWaiting bool
}

// SessionStartFinished offers a session that /new started after its wait
// moved to the background. The screen asks "Started session <id>. Switch to
// it? y switch · n stay" and switches only with SwitchSession.
type SessionStartFinished struct {
	// SessionID is the new session's ID.
	SessionID string
}

// MoveSessionStartToBackground lets /new go on without the user waiting. It
// sends no cancel: the session may still start, and SessionStartFinished
// offers it.
type MoveSessionStartToBackground struct{}

// SwitchSession switches the chat to another session of the same command,
// after the user agreed to it.
type SwitchSession struct {
	// SessionID is the session to show.
	SessionID string
}

func (SessionStartChanged) isSessionUpdate()       {}
func (SessionStartFinished) isSessionUpdate()      {}
func (MoveSessionStartToBackground) isUserAction() {}
func (SwitchSession) isUserAction()                {}
