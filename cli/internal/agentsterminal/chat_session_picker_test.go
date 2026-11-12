// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package agentsterminal

import (
	"context"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/cli/internal/agentsterminal/chatscreen"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionlisting"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
	"github.com/superdurable/dex/sdk-go/dex"
)

// recordingChat stands in for the chat screen and records its messages.
type recordingChat struct {
	messages []tea.Msg
}

func (chat *recordingChat) Init() tea.Cmd { return nil }

func (chat *recordingChat) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	chat.messages = append(chat.messages, message)
	return chat, nil
}

func (chat *recordingChat) View() tea.View { return tea.NewView("chat") }

// countingSessions lists one session named after the call number.
type countingSessions struct {
	mutex sync.Mutex
	calls int
}

func (sessions *countingSessions) ListSessions(context.Context, string, string) (sessionlisting.SessionPage, error) {
	sessions.mutex.Lock()
	defer sessions.mutex.Unlock()
	sessions.calls++
	names := map[int]string{1: "test-20261111-000000-first0", 2: "test-20261111-000000-secnd0"}
	return sessionlisting.SessionPage{Sessions: []sessionlisting.SessionSummary{{SessionID: names[sessions.calls], Status: dex.FlowRunning}}}, nil
}

func (sessions *countingSessions) CheckResume(context.Context, string) (sessionlisting.ResumeCheck, dex.FlowStatus, error) {
	return sessionlisting.ResumeAllowed, dex.FlowRunning, nil
}

func newTestOverlay(t *testing.T) (*chatSessionPicker, *recordingChat) {
	chat := &recordingChat{}
	overlay := newChatSessionPicker(context.Background(), chat, chatSessionPickerConfig{
		Sessions:         &countingSessions{},
		CurrentSessionID: func() string { return "test-current" },
		Theme:            terminaltheme.NewTheme(colorprofile.ASCII, true),
		PickerRequests:   make(chan struct{}),
	})
	overlay.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return overlay, chat
}

// openPickerCommand opens the picker and returns its first page load.
func openPickerCommand(t *testing.T, overlay *chatSessionPicker) tea.Cmd {
	_, command := overlay.Update(openSessionPickerMsg{})
	batch, isBatch := command().(tea.BatchMsg)
	require.True(t, isBatch)
	return batch[0]
}

// A page of a picker closed before it arrived never reaches the reopened one.
func TestReopenedPickerIgnoresTheClosedPickersPage(t *testing.T) {
	overlay, _ := newTestOverlay(t)
	firstLoad := openPickerCommand(t, overlay)
	_, closing := overlay.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	overlay.Update(closing())
	require.Nil(t, overlay.picker)
	secondLoad := openPickerCommand(t, overlay)
	stalePage := firstLoad()
	overlay.Update(secondLoad())
	overlay.Update(stalePage)
	view := terminaltheme.StripStyles(overlay.View().Content)
	require.Contains(t, view, "secnd0")
	require.NotContains(t, view, "first0", "the closed picker's late page is dropped")
}

// A picked session reaches the chat screen as a switch request.
func TestPickedSessionGoesToTheChatScreen(t *testing.T) {
	overlay, chat := newTestOverlay(t)
	load := openPickerCommand(t, overlay)
	overlay.Update(load())
	_, check := overlay.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, chosen := overlay.Update(check())
	overlay.Update(chosen())
	require.Nil(t, overlay.picker)
	require.Equal(t, chatscreen.SwitchSessionRequestMsg{SessionID: "test-20261111-000000-first0", IsResumed: true}, chat.messages[len(chat.messages)-1])
}
