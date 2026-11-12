// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// The terminal UI contract's notices.json cases hold for the screen's
// cleanup and repeat filter.
func TestNoticeConformanceCases(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "agents", "conformance", "terminal_ui", "notices.json"))
	require.NoError(t, err)
	var file struct {
		TextCases []struct {
			Name  string `json:"name"`
			Text  string `json:"text"`
			Shown string `json:"shown"`
		} `json:"text_cases"`
		RepeatCases []struct {
			Name    string `json:"name"`
			Notices []struct {
				AtMS    int64  `json:"at_ms"`
				Level   string `json:"level"`
				Text    string `json:"text"`
				IsShown bool   `json:"is_shown"`
			} `json:"notices"`
		} `json:"repeat_cases"`
	}
	require.NoError(t, json.Unmarshal(data, &file))
	severities := map[string]sessionfeed.NoticeSeverity{
		"info": sessionfeed.NoticeSeverityInfo, "warning": sessionfeed.NoticeSeverityWarning, "error": sessionfeed.NoticeSeverityError,
	}
	start := time.Unix(1_700_000_000, 0)
	for _, testCase := range file.TextCases {
		cleaned := replaceUpdateControlCharacters(sessionfeed.NoticeRaised{Text: testCase.Text}).(sessionfeed.NoticeRaised)
		shown, isShown := newNoticeFilter().Admit(sessionfeed.NoticeSeverityInfo, cleaned.Text, start)
		require.True(t, isShown, testCase.Name)
		require.Equal(t, testCase.Shown, shown, testCase.Name)
	}
	for _, testCase := range file.RepeatCases {
		filter := newNoticeFilter()
		for index, notice := range testCase.Notices {
			cleaned := replaceUpdateControlCharacters(sessionfeed.NoticeRaised{Text: notice.Text}).(sessionfeed.NoticeRaised)
			_, isShown := filter.Admit(severities[notice.Level], cleaned.Text, start.Add(time.Duration(notice.AtMS)*time.Millisecond))
			require.Equal(t, notice.IsShown, isShown, "%s notice %d", testCase.Name, index)
		}
	}
}
