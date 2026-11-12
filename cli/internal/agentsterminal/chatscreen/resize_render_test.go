// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// channelSource feeds updates from a channel to a running program.
type channelSource struct {
	updates chan sessionfeed.SessionUpdate
}

func (source *channelSource) NextSessionUpdate(ctx context.Context) (sessionfeed.SessionUpdate, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case update := <-source.updates:
		return update, nil
	}
}

func (source *channelSource) SubmitUserAction(context.Context, sessionfeed.UserAction) error {
	return nil
}

// TestResizeLeavesNoStaleFrames drives the real Bubble Tea renderer into a
// reflowing terminal at 140, then 80, then 40 columns.
func TestResizeLeavesNoStaleFrames(t *testing.T) {
	for _, isReflowCorrected := range []bool{true, false} {
		lines := renderThroughResizes(t, isReflowCorrected)
		liveRowCount := countLinesContaining(lines, "=== RUN")
		if !isReflowCorrected {
			require.Greater(t, liveRowCount, 1, "without the correction the test terminal shows stale frames, so the test can see them")
			continue
		}
		screenText := strings.Join(lines, "\n")
		require.Equal(t, 1, liveRowCount, "stale frames remain:\n%s", screenText)
		require.Equal(t, 1, countLinesContaining(lines, "Enter queue"), screenText)
		require.Equal(t, 1, countLinesContaining(lines, "type to queue a message"), screenText)
		require.Equal(t, 1, countLinesContaining(lines, "Find open issues"), "the scrollback keeps one copy:\n%s", screenText)
	}
}

func renderThroughResizes(t *testing.T, isReflowCorrected bool) []string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	terminal := newReflowTerminal(140, 30)
	source := &channelSource{updates: make(chan sessionfeed.SessionUpdate, 32)}
	screen := NewChatScreen(ctx, ChatScreenConfig{
		Source:              source,
		Theme:               terminaltheme.NewTheme(colorprofile.ANSI, true),
		PrintScrollback:     NewScrollbackPrinter(colorprofile.ANSI),
		IsTerminalReflowing: isReflowCorrected,
		// A fixed clock stops the spinner, so finished redraws leave a stable screen.
		Now: func() time.Time { return testClockStart },
	})
	program := tea.NewProgram(screen,
		tea.WithContext(ctx),
		tea.WithInput(nil),
		tea.WithOutput(terminal),
		tea.WithoutSignals(),
		tea.WithWindowSize(140, 30),
		tea.WithEnvironment([]string{"TERM=xterm-256color"}),
		tea.WithColorProfile(colorprofile.ANSI),
		tea.WithFilter(NewResizeFilter(screen, terminal)),
	)
	finished := make(chan error, 1)
	go func() {
		_, err := program.Run()
		finished <- err
	}()
	now := testClockStart
	for _, update := range []sessionfeed.SessionUpdate{
		sessionfeed.SessionOpened{Command: "openai-sdk", Model: "gpt-5.2", SessionID: "openai-sdk-1", AcceptsTurnControl: true},
		sessionfeed.SessionLoaded{},
		sessionfeed.UserMessageRecorded{Text: "Find open issues about flaky tests and post a summary to #eng."},
		sessionfeed.TurnStarted{TurnID: "turn-1", TurnNumber: 1, StartedAt: now},
		finishedTool("call-1", "search_files", `"flaky" in tests/`, sessionfeed.ToolCallSucceeded, "12 matches", 400*time.Millisecond),
		sessionfeed.ToolCallChanged{ToolCallID: "call-2", ToolName: "run_shell_command", KeyArgument: "go test ./integ/... -run Retry", Status: sessionfeed.ToolCallRunning, StartedAt: now, OutputTail: []string{"=== RUN   TestRetryBackoff with a long name that fills the row"}},
		sessionfeed.UsageRecorded{TotalTokens: 3900},
	} {
		source.updates <- update
	}
	waitForStableScreen(t, terminal, "TestRetryBackoff", "Enter queue")
	debugScreen(t, terminal, "140")
	for _, width := range []int{80, 40} {
		terminal.Resize(width)
		debugScreen(t, terminal, "after reflow")
		program.Send(tea.WindowSizeMsg{Width: width, Height: 30})
		waitForStableScreen(t, terminal, "Enter queue")
		debugScreen(t, terminal, "redrawn")
	}
	lines := terminal.ScreenLines()
	require.Empty(t, terminal.UnknownSequences(), "the renderer wrote sequences the test terminal does not know")
	program.Quit()
	require.NoError(t, <-finished)
	return lines
}

// waitForStableScreen waits for parts and three equal polls, so the redraw
// finished.
func waitForStableScreen(t *testing.T, terminal *reflowTerminal, parts ...string) {
	t.Helper()
	previous, stablePolls := "", 0
	require.Eventually(t, func() bool {
		text := strings.Join(terminal.ScreenLines(), "\n")
		for _, part := range parts {
			if !strings.Contains(text, part) {
				previous, stablePolls = text, 0
				return false
			}
		}
		if text == previous {
			stablePolls++
		} else {
			previous, stablePolls = text, 0
		}
		return stablePolls >= 3
	}, 5*time.Second, 50*time.Millisecond, "screen:\n%s", strings.Join(terminal.ScreenLines(), "\n"))
}

func countLinesContaining(lines []string, part string) int {
	count := 0
	for _, line := range lines {
		if strings.Contains(line, part) {
			count++
		}
	}
	return count
}

func debugScreen(t *testing.T, terminal *reflowTerminal, label string) {
	if os.Getenv("DEBUG_REFLOW") == "" {
		return
	}
	terminal.mutex.Lock()
	x, y := terminal.cursorX, terminal.cursorY
	terminal.mutex.Unlock()
	t.Logf("---- %s (cursor %d,%d) ----\n%s", label, x, y, strings.Join(terminal.ScreenLines(), "|\n"))
}
