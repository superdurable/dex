// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package sessionfeed is the contract between the agents chat screen and one
// agent session.
package sessionfeed

import (
	"context"
	"errors"
)

// ErrSessionFeedClosed is returned by NextSessionUpdate when the source has
// no more updates, for example because the session's command stopped.
var ErrSessionFeedClosed = errors.New("session feed closed")

// ChatSessionSource connects the chat screen to one session, off the UI
// goroutine. See SessionLoaded and UserMessageRecorded.
type ChatSessionSource interface {
	// NextSessionUpdate returns the next update, ctx.Err() when ctx ends, or
	// ErrSessionFeedClosed.
	NextSessionUpdate(ctx context.Context) (SessionUpdate, error)
	// SubmitUserAction delivers one action. Session outcomes, such as an
	// accepted cancel, arrive later as updates.
	SubmitUserAction(ctx context.Context, action UserAction) error
}
