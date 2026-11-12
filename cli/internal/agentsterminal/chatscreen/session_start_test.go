// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

const backgroundSessionID = "openai-sdk-20261111-142507-d3fbbe"

// Esc while /new waits moves the wait to the background; it never cancels.
func TestEscMovesSessionStartToTheBackground(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	harness.feed(sessionfeed.SessionStartChanged{IsWaiting: true})
	require.Contains(t, harness.plainFrame(), "Esc keep starting in the background")
	harness.press(key(tea.KeyEscape))
	require.Equal(t, sessionfeed.MoveSessionStartToBackground{}, harness.lastAction())
	harness.press(key(tea.KeyEscape))
	require.Len(t, harness.source.recordedActions(), 1, "a second Esc sends nothing")
}

// A session started in the background is offered, never switched to
// silently; y switches and n stays.
func TestSessionStartedInTheBackgroundAsksBeforeSwitching(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		answer     rune
		isSwitched bool
	}{
		{name: "y switches", answer: 'y', isSwitched: true},
		{name: "n stays", answer: 'n'},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newScreenHarness(t, harnessOptions{})
			harness.feed(sessionfeed.SessionStartFinished{SessionID: backgroundSessionID})
			frame := harness.plainFrame()
			require.Contains(t, frame, "Started session "+backgroundSessionID+". Switch to it?")
			require.Contains(t, frame, "y switch · n stay")
			require.Empty(t, harness.source.recordedActions())
			harness.press(textKey(testCase.answer))
			if testCase.isSwitched {
				require.Equal(t, sessionfeed.SwitchSession{SessionID: backgroundSessionID}, harness.lastAction())
			} else {
				require.Empty(t, harness.source.recordedActions())
			}
			require.NotContains(t, harness.plainFrame(), "Switch to it?")
		})
	}
}

// While the user types, y and n are text and the question waits.
func TestSwitchQuestionWaitsWhileTheUserTypes(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	harness.typeText("hello")
	harness.feed(sessionfeed.SessionStartFinished{SessionID: backgroundSessionID})
	harness.typeText("y")
	require.Empty(t, harness.source.recordedActions())
	require.Equal(t, "helloy", harness.screen.input.Value())
}

// Another session holds typed messages until its first snapshot.
func TestSwitchedSessionHoldsMessagesUntilItLoads(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: backgroundSessionID, IsNewSession: true})
	require.False(t, harness.screen.isSessionLoaded)
	harness.typeText("first message")
	harness.press(key(tea.KeyEnter))
	require.Empty(t, harness.source.recordedActions(), "held until the new session loads")
	harness.feed(sessionfeed.SessionLoaded{})
	require.Equal(t, sessionfeed.SendUserMessage{Text: "first message"}, harness.lastAction())
	require.Contains(t, harness.screen.ExitSummary(), backgroundSessionID, "the exit summary names the current session")
}
