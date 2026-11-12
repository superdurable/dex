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

	tea "charm.land/bubbletea/v2"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionpicker"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// overlayHeightPercent is the most of the screen height /sessions takes.
const overlayHeightPercent = 60

// minOverlayHeight keeps the picker usable on a short terminal.
const minOverlayHeight = 8

// openSessionPickerMsg opens the picker; /sessions asks for it.
type openSessionPickerMsg struct{}

// sessionSwitchSubmittedMsg reports that SubmitUserAction returned.
type sessionSwitchSubmittedMsg struct{}

// chatSessionPickerConfig configures newChatSessionPicker.
type chatSessionPickerConfig struct {
	// Sessions lists the sessions and checks the chosen one. Required.
	Sessions sessionpicker.SessionSource
	// Source receives the SwitchSession action. Required.
	Source sessionfeed.ChatSessionSource
	// CurrentSessionID returns the session the chat shows. Required.
	CurrentSessionID func() string
	// Theme holds the styles and symbols.
	Theme terminaltheme.Theme
	// Heading names the command and the Flow type.
	Heading string
	// NewSessionHint tells how to start a session, or "".
	NewSessionHint string
	// PickerRequests delivers one value per /sessions. Required.
	PickerRequests <-chan struct{}
}

// chatSessionPicker shows /sessions over the chat. While it is open, keys
// go to the picker.
type chatSessionPicker struct {
	ctx    context.Context
	chat   tea.Model
	config chatSessionPickerConfig
	width  int
	height int
	picker *sessionpicker.SessionPicker
}

func newChatSessionPicker(ctx context.Context, chat tea.Model, config chatSessionPickerConfig) *chatSessionPicker {
	if config.Sessions == nil || config.Source == nil || config.CurrentSessionID == nil || config.PickerRequests == nil {
		panic("agentsterminal: chatSessionPickerConfig needs Sessions, Source, CurrentSessionID, and PickerRequests")
	}
	return &chatSessionPicker{ctx: ctx, chat: chat, config: config, width: 80, height: 24}
}

// Init starts the chat screen and the wait for /sessions.
func (overlay *chatSessionPicker) Init() tea.Cmd {
	return tea.Batch(overlay.chat.Init(), overlay.waitForPickerRequest())
}

func (overlay *chatSessionPicker) waitForPickerRequest() tea.Cmd {
	requests, ctx := overlay.config.PickerRequests, overlay.ctx
	return func() tea.Msg {
		select {
		case <-requests:
			return openSessionPickerMsg{}
		case <-ctx.Done():
			return nil
		}
	}
}

// Update routes keys to an open picker and everything else to the chat.
func (overlay *chatSessionPicker) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := message.(type) {
	case openSessionPickerMsg:
		return overlay, tea.Batch(overlay.openPicker(), overlay.waitForPickerRequest())
	case sessionpicker.SessionChosenMsg:
		overlay.picker = nil
		return overlay, overlay.switchSession(typed.SessionID)
	case sessionpicker.PickerClosedMsg:
		overlay.picker = nil
		return overlay, nil
	case sessionSwitchSubmittedMsg:
		return overlay, nil
	case tea.KeyPressMsg, tea.PasteMsg:
		if overlay.picker != nil {
			return overlay, overlay.picker.Update(message)
		}
	case tea.WindowSizeMsg:
		overlay.width, overlay.height = typed.Width, typed.Height
		if overlay.picker != nil {
			overlay.picker.SetSize(typed.Width, overlay.pickerHeight())
		}
	default:
		if overlay.picker != nil {
			if command := overlay.picker.Update(message); command != nil {
				return overlay, command
			}
		}
	}
	chat, command := overlay.chat.Update(message)
	overlay.chat = chat
	return overlay, command
}

func (overlay *chatSessionPicker) openPicker() tea.Cmd {
	if overlay.picker != nil {
		return nil
	}
	overlay.picker = sessionpicker.NewSessionPicker(overlay.ctx, sessionpicker.SessionPickerConfig{
		Sessions:         overlay.config.Sessions,
		Theme:            overlay.config.Theme,
		Heading:          overlay.config.Heading,
		CurrentSessionID: overlay.config.CurrentSessionID(),
		NewSessionHint:   overlay.config.NewSessionHint,
		EmptyHint:        "No sessions yet.",
		CloseKeyName:     "back",
	})
	overlay.picker.SetSize(overlay.width, overlay.pickerHeight())
	return overlay.picker.Init()
}

// switchSession hands the checked session to the chat source, which loads
// its history; the draft stays in the input.
func (overlay *chatSessionPicker) switchSession(sessionID string) tea.Cmd {
	source, ctx := overlay.config.Source, overlay.ctx
	return func() tea.Msg {
		if err := source.SubmitUserAction(ctx, sessionfeed.SwitchSession{SessionID: sessionID, IsResumed: true}); err != nil {
			debugLog("switch the session", err)
		}
		return sessionSwitchSubmittedMsg{}
	}
}

func (overlay *chatSessionPicker) pickerHeight() int {
	return min(max(overlay.height*overlayHeightPercent/100, minOverlayHeight), overlay.height)
}

// View draws the picker in place of the chat's active area while it is open.
func (overlay *chatSessionPicker) View() tea.View {
	view := overlay.chat.View()
	if overlay.picker != nil {
		view.Content = overlay.picker.View()
		view.Cursor = nil
	}
	return view
}
