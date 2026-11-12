// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"time"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// These limits come from the terminal UI contract's Notices section.
const (
	maximumNoticeCodePoints = 500
	noticeRepeatWindow      = 10 * time.Second
)

// noticeFilter cuts session notices and hides repeats.
type noticeFilter struct {
	lastShownAt map[noticeKey]time.Time
}

type noticeKey struct {
	severity sessionfeed.NoticeSeverity
	text     string
}

func newNoticeFilter() *noticeFilter {
	return &noticeFilter{lastShownAt: map[noticeKey]time.Time{}}
}

// Admit returns the text cut to 500 code points and whether to show it.
func (filter *noticeFilter) Admit(severity sessionfeed.NoticeSeverity, text string, now time.Time) (string, bool) {
	shown := terminaltheme.TruncateCodePoints(text, maximumNoticeCodePoints, "…")
	key := noticeKey{severity: severity, text: shown}
	if lastShownAt, exists := filter.lastShownAt[key]; exists && now.Sub(lastShownAt) < noticeRepeatWindow {
		return shown, false
	}
	for oldKey, shownAt := range filter.lastShownAt {
		if now.Sub(shownAt) >= noticeRepeatWindow {
			delete(filter.lastShownAt, oldKey)
		}
	}
	filter.lastShownAt[key] = now
	return shown, true
}
