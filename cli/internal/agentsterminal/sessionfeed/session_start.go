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

// SessionStartFinished reports the session /new started; it comes before
// SessionStartChanged ends the wait. While the user still waits in the
// foreground, the screen switches to it with SwitchSession. After Esc moved
// the wait to the background, it asks "Started session <id>. Switch to it? y
// switch · n stay" and never switches silently. The screen decides on its
// event loop, so an Esc it accepted always wins over a result read later.
type SessionStartFinished struct {
	// SessionID is the new session's ID.
	SessionID string
}

// MoveSessionStartToBackground tells the source that the user stopped
// waiting for /new. It sends no cancel: the session may still start, and
// SessionStartFinished offers it.
type MoveSessionStartToBackground struct{}

// SwitchSession switches the chat to another session of the same command,
// after the user agreed to it.
type SwitchSession struct {
	// SessionID is the session to show.
	SessionID string
	// IsResumed loads the session's history, for a session /sessions picked;
	// false for the new session /new started.
	IsResumed bool
}

// SessionSwitchHandedOff tells the screen that the launcher opens the session
// a SwitchSession named. The screen exits 0 without its exit summary.
type SessionSwitchHandedOff struct {
	// SessionID is the session the launcher opens next.
	SessionID string
}

func (SessionStartChanged) isSessionUpdate()       {}
func (SessionSwitchHandedOff) isSessionUpdate()    {}
func (SessionStartFinished) isSessionUpdate()      {}
func (MoveSessionStartToBackground) isUserAction() {}
func (SwitchSession) isUserAction()                {}
