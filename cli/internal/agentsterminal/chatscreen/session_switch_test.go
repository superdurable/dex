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

	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// A switched-to session's history prints even when its message sequences
// and turn IDs equal the previous session's.
func TestSwitchedSessionHistoryPrints(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	harness.feed(
		sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-1"}, sessionfeed.SessionLoaded{},
		sessionfeed.UserMessageRecorded{Text: "first session question", TurnID: "turn-1", Sequence: 1},
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-1", Text: "first session answer", Sequence: 2},
		sessionfeed.TurnEnded{TurnID: "turn-1"},
		sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-2"}, sessionfeed.SessionLoaded{},
		sessionfeed.UserMessageRecorded{Text: "second session question", TurnID: "turn-1", Sequence: 1},
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-1", Text: "second session answer", Sequence: 2},
	)
	scrollback := harness.scrollbackText()
	require.Contains(t, scrollback, "second session question")
	require.Contains(t, scrollback, "second session answer")
}
