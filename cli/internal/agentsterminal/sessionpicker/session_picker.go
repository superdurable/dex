// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package sessionpicker is the agents terminal session picker: a paged
// list of sessions to resume.
package sessionpicker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionlisting"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
	"github.com/superdurable/dex/sdk-go/dex"
)

// dexCallTimeout bounds one SearchFlows or DescribeFlow call.
const dexCallTimeout = 15 * time.Second

// SessionSource lists sessions and checks one before it resumes.
type SessionSource interface {
	ListSessions(ctx context.Context, nextPageToken string, titleSearch string) (sessionlisting.SessionPage, error)
	CheckResume(ctx context.Context, sessionID string) (sessionlisting.ResumeCheck, dex.FlowStatus, error)
}

// SessionPickerConfig configures NewSessionPicker.
type SessionPickerConfig struct {
	// Sessions lists the sessions. Required.
	Sessions SessionSource
	// Theme holds the styles and symbols. Required.
	Theme terminaltheme.Theme
	// Heading names the list, such as "openai-sdk · DexAgentsOpenAISdkCliSession-ab12cd".
	Heading string
	// CurrentSessionID is the session the chat shows, or "" outside a chat.
	CurrentSessionID string
	// NewSessionHint ends the message for an ended session. Default: none.
	NewSessionHint string
	// EmptyHint is shown when the Flow type has no sessions.
	EmptyHint string
	// CloseKeyName is the hint for Esc, such as "back" or "exit".
	CloseKeyName string
	// Now returns the current time. Default: time.Now.
	Now func() time.Time
}

// SessionChosenMsg reports the session the user picked; DescribeFlow found
// it running with the picker's Flow type.
type SessionChosenMsg struct {
	// SessionID is the chosen session.
	SessionID string
}

// PickerClosedMsg reports a close without a choice. IsExitRequested
// marks Ctrl-C and Ctrl-D.
type PickerClosedMsg struct {
	// IsExitRequested asks to exit rather than go back.
	IsExitRequested bool
}

// SessionPicker is the picker model. Its host forwards messages and sets
// its size.
type SessionPicker struct {
	// instanceID tells this picker's messages from a closed picker's.
	instanceID     uint64
	ctx            context.Context
	cancelRequests context.CancelFunc
	config         SessionPickerConfig

	width  int
	height int

	sessions      []sessionlisting.SessionSummary
	nextPageToken string
	isLoading     bool
	loadFailure   string
	hasLoaded     bool
	generation    int

	cursor  int
	topItem int

	isTyping            bool
	query               string
	committedQuery      string
	isTitleSearch       bool
	isTitleSearchOff    bool
	statusText          string
	statusSeverity      statusSeverity
	checkingSessionID   string
	isClosed            bool
	unfilteredSessions  []sessionlisting.SessionSummary
	unfilteredPageToken string
	isUnfilteredLoaded  bool
}

type statusSeverity int

const (
	statusInfo statusSeverity = iota
	statusWarning
	statusError
)

// pageLoadedMsg carries one page, or why it failed.
type pageLoadedMsg struct {
	instanceID uint64
	generation int
	page       sessionlisting.SessionPage
	err        error
}

// resumeCheckedMsg carries the DescribeFlow check of one session.
type resumeCheckedMsg struct {
	instanceID uint64
	sessionID  string
	check      sessionlisting.ResumeCheck
	status     dex.FlowStatus
	err        error
}

// NewSessionPicker returns a picker that loads its first page in Init.
func NewSessionPicker(ctx context.Context, config SessionPickerConfig) *SessionPicker {
	if config.Sessions == nil {
		panic("sessionpicker: SessionPickerConfig.Sessions is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.CloseKeyName == "" {
		config.CloseKeyName = "back"
	}
	requestContext, cancelRequests := context.WithCancel(ctx)
	return &SessionPicker{
		instanceID: nextInstanceID.Add(1), ctx: requestContext, cancelRequests: cancelRequests,
		config: config, width: 80, height: 24,
	}
}

// nextInstanceID numbers the pickers of one process.
var nextInstanceID atomic.Uint64

// Init loads the first page.
func (picker *SessionPicker) Init() tea.Cmd {
	return picker.loadPage("")
}

// SetSize sets the room the picker draws in.
func (picker *SessionPicker) SetSize(width int, height int) {
	picker.width, picker.height = max(width, 1), max(height, 1)
}

// Update handles keys and loaded pages. It returns a command that may
// deliver SessionChosenMsg or PickerClosedMsg.
func (picker *SessionPicker) Update(message tea.Msg) tea.Cmd {
	if picker.isClosed {
		return nil
	}
	switch typed := message.(type) {
	case tea.WindowSizeMsg:
		picker.SetSize(typed.Width, typed.Height)
	case pageLoadedMsg:
		if typed.instanceID != picker.instanceID {
			return nil
		}
		return picker.handlePage(typed)
	case resumeCheckedMsg:
		if typed.instanceID != picker.instanceID {
			return nil
		}
		return picker.handleResumeCheck(typed)
	case tea.PasteMsg:
		if picker.isTyping {
			picker.setQuery(picker.query + strings.Join(strings.Fields(typed.Content), " "))
		}
	case tea.KeyPressMsg:
		return picker.handleKey(typed)
	}
	return nil
}

func (picker *SessionPicker) handleKey(key tea.KeyPressMsg) tea.Cmd {
	keyName := key.String()
	switch keyName {
	case "ctrl+c", "ctrl+d":
		return picker.close(true)
	case "ctrl+l":
		return tea.ClearScreen
	}
	if picker.isTyping {
		return picker.handleSearchKey(key)
	}
	switch keyName {
	case "esc":
		if picker.committedQuery != "" {
			return picker.clearSearch()
		}
		return picker.close(false)
	case "q":
		return picker.close(false)
	case "up", "k":
		return picker.moveCursor(-1)
	case "down", "j":
		return picker.moveCursor(1)
	case "pgup":
		return picker.moveCursor(-picker.pageStep())
	case "pgdown":
		return picker.moveCursor(picker.pageStep())
	case "home":
		return picker.moveCursor(-len(picker.sessions))
	case "end":
		return picker.moveCursor(len(picker.sessions) + 1)
	case "/":
		picker.isTyping = true
		picker.query = picker.committedQuery
		picker.statusText = ""
	case "r":
		return picker.reload()
	case "enter":
		return picker.choose()
	}
	return nil
}

func (picker *SessionPicker) handleSearchKey(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case "esc":
		picker.isTyping = false
		return picker.clearSearch()
	case "enter":
		picker.isTyping = false
		return picker.commitSearch()
	case "backspace":
		runes := []rune(picker.query)
		if len(runes) > 0 {
			picker.setQuery(string(runes[:len(runes)-1]))
		}
	case "up":
		return picker.moveCursor(-1)
	case "down":
		return picker.moveCursor(1)
	default:
		if key.Text != "" && key.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
			picker.setQuery(picker.query + key.Text)
		}
	}
	return nil
}

// setQuery filters the loaded sessions as the user types.
func (picker *SessionPicker) setQuery(query string) {
	picker.query = query
	picker.cursor, picker.topItem = 0, 0
}

// commitSearch searches titles in Dex, or filters loaded IDs without the
// title index.
func (picker *SessionPicker) commitSearch() tea.Cmd {
	query := strings.TrimSpace(picker.query)
	picker.query = query
	if query == "" {
		return picker.clearSearch()
	}
	picker.committedQuery = query
	if picker.isTitleSearchOff {
		picker.setStatus(statusInfo, fmt.Sprintf("Title search is off here; filtering %d loaded sessions by ID.", len(picker.allSessions())))
		return nil
	}
	if !picker.isTitleSearch {
		picker.unfilteredSessions, picker.unfilteredPageToken, picker.isUnfilteredLoaded = picker.sessions, picker.nextPageToken, picker.hasLoaded
	}
	picker.isTitleSearch = true
	picker.sessions, picker.nextPageToken, picker.hasLoaded = nil, "", false
	picker.cursor, picker.topItem = 0, 0
	return picker.loadPage("")
}

// clearSearch drops the query and shows every loaded session again.
func (picker *SessionPicker) clearSearch() tea.Cmd {
	picker.query, picker.committedQuery = "", ""
	picker.cursor, picker.topItem = 0, 0
	picker.statusText = ""
	if picker.isTitleSearch {
		picker.isTitleSearch = false
		picker.generation++
		picker.isLoading = false
		picker.sessions, picker.nextPageToken, picker.hasLoaded = picker.unfilteredSessions, picker.unfilteredPageToken, picker.isUnfilteredLoaded
		picker.unfilteredSessions, picker.unfilteredPageToken = nil, ""
		if !picker.hasLoaded {
			// The first page was still loading when the title search began.
			return picker.loadPage("")
		}
	}
	return nil
}

func (picker *SessionPicker) reload() tea.Cmd {
	picker.sessions, picker.nextPageToken, picker.hasLoaded = nil, "", false
	picker.cursor, picker.topItem = 0, 0
	picker.statusText = ""
	return picker.loadPage("")
}

func (picker *SessionPicker) loadPage(pageToken string) tea.Cmd {
	picker.generation++
	picker.isLoading, picker.loadFailure = true, ""
	generation := picker.generation
	search := ""
	if picker.isTitleSearch {
		search = picker.committedQuery
	}
	sessions, ctx, instanceID := picker.config.Sessions, picker.ctx, picker.instanceID
	return func() tea.Msg {
		callContext, cancel := context.WithTimeout(ctx, dexCallTimeout)
		defer cancel()
		page, err := sessions.ListSessions(callContext, pageToken, search)
		return pageLoadedMsg{instanceID: instanceID, generation: generation, page: page, err: err}
	}
}

func (picker *SessionPicker) handlePage(message pageLoadedMsg) tea.Cmd {
	if message.generation != picker.generation {
		return nil
	}
	picker.isLoading = false
	if errors.Is(message.err, sessionlisting.ErrTitleSearchUnavailable) {
		picker.isTitleSearchOff = true
		query := picker.committedQuery
		command := picker.clearSearch()
		picker.query, picker.committedQuery = query, query
		picker.setStatus(statusInfo, fmt.Sprintf("Title search is off here; filtering %d loaded sessions by ID.", len(picker.sessions)))
		return command
	}
	if message.err != nil {
		picker.loadFailure = message.err.Error()
		return nil
	}
	picker.hasLoaded = true
	picker.sessions = append(picker.sessions, message.page.Sessions...)
	picker.nextPageToken = message.page.NextPageToken
	return nil
}

// moveCursor moves over the shown sessions and the Load more row; reaching
// that row loads the next page.
func (picker *SessionPicker) moveCursor(delta int) tea.Cmd {
	itemCount := len(picker.shownSessions())
	if picker.hasMoreRow() {
		itemCount++
	}
	if itemCount == 0 {
		return nil
	}
	picker.cursor = min(max(picker.cursor+delta, 0), itemCount-1)
	if picker.isCursorOnMoreRow() && !picker.isLoading {
		return picker.loadPage(picker.nextPageToken)
	}
	return nil
}

func (picker *SessionPicker) choose() tea.Cmd {
	if picker.isCursorOnMoreRow() {
		if picker.isLoading {
			return nil
		}
		return picker.loadPage(picker.nextPageToken)
	}
	shown := picker.shownSessions()
	if picker.cursor >= len(shown) || picker.checkingSessionID != "" {
		return nil
	}
	session := shown[picker.cursor]
	if session.SessionID == picker.config.CurrentSessionID {
		return picker.close(false)
	}
	if session.Status != dex.FlowRunning {
		picker.setStatus(statusWarning, picker.notRunningText(session.Status))
		return nil
	}
	picker.checkingSessionID = session.SessionID
	picker.setStatus(statusInfo, "checking "+session.SessionID+"…")
	sessions, ctx, sessionID, instanceID := picker.config.Sessions, picker.ctx, session.SessionID, picker.instanceID
	return func() tea.Msg {
		callContext, cancel := context.WithTimeout(ctx, dexCallTimeout)
		defer cancel()
		check, status, err := sessions.CheckResume(callContext, sessionID)
		return resumeCheckedMsg{instanceID: instanceID, sessionID: sessionID, check: check, status: status, err: err}
	}
}

func (picker *SessionPicker) handleResumeCheck(message resumeCheckedMsg) tea.Cmd {
	if message.sessionID != picker.checkingSessionID {
		return nil
	}
	picker.checkingSessionID = ""
	switch {
	case message.err != nil:
		picker.setStatus(statusError, "Could not check the session: "+message.err.Error())
		return nil
	case message.check == sessionlisting.ResumeOtherFlowType:
		picker.setStatus(statusError, "This session belongs to another command; it cannot resume here.")
		return nil
	case message.check == sessionlisting.ResumeNotRunning:
		picker.updateStatus(message.sessionID, message.status)
		picker.setStatus(statusWarning, picker.notRunningText(message.status))
		return nil
	}
	picker.isClosed = true
	picker.cancelRequests()
	sessionID := message.sessionID
	return func() tea.Msg { return SessionChosenMsg{SessionID: sessionID} }
}

func (picker *SessionPicker) updateStatus(sessionID string, status dex.FlowStatus) {
	for _, sessions := range [][]sessionlisting.SessionSummary{picker.sessions, picker.unfilteredSessions} {
		for index := range sessions {
			if sessions[index].SessionID == sessionID {
				sessions[index].Status = status
			}
		}
	}
}

func (picker *SessionPicker) notRunningText(status dex.FlowStatus) string {
	text := "This session is " + statusName(status)
	if picker.config.NewSessionHint != "" {
		return text + "; " + picker.config.NewSessionHint + "."
	}
	return text + "; only running sessions resume."
}

func (picker *SessionPicker) close(isExitRequested bool) tea.Cmd {
	picker.isClosed = true
	picker.cancelRequests()
	return func() tea.Msg { return PickerClosedMsg{IsExitRequested: isExitRequested} }
}

func (picker *SessionPicker) setStatus(severity statusSeverity, text string) {
	picker.statusSeverity, picker.statusText = severity, text
}

// shownSessions are the loaded sessions that match the query. A title
// search shows all.
func (picker *SessionPicker) shownSessions() []sessionlisting.SessionSummary {
	query := picker.committedQuery
	if picker.isTyping {
		query = picker.query
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" || (picker.isTitleSearch && !picker.isTyping) {
		return picker.sessions
	}
	var matches []sessionlisting.SessionSummary
	for _, session := range picker.allSessions() {
		if matchesLocalQuery(session, query) {
			matches = append(matches, session)
		}
	}
	return matches
}

// allSessions are the sessions loaded without a title search.
func (picker *SessionPicker) allSessions() []sessionlisting.SessionSummary {
	if picker.isTitleSearch {
		return picker.unfilteredSessions
	}
	return picker.sessions
}

// matchesLocalQuery matches the ID, and the title when the session has one.
func matchesLocalQuery(session sessionlisting.SessionSummary, lowerQuery string) bool {
	if strings.Contains(strings.ToLower(session.SessionID), lowerQuery) {
		return true
	}
	return session.Title != nil && strings.Contains(strings.ToLower(*session.Title), lowerQuery)
}

func (picker *SessionPicker) hasMoreRow() bool {
	if picker.isTyping || (picker.committedQuery != "" && !picker.isTitleSearch) {
		return false
	}
	return picker.nextPageToken != ""
}

func (picker *SessionPicker) isCursorOnMoreRow() bool {
	return picker.hasMoreRow() && picker.cursor == len(picker.shownSessions())
}

func (picker *SessionPicker) pageStep() int {
	return max(picker.height/3, 1)
}

// FocusedSessionID returns the session under the cursor, or "".
func (picker *SessionPicker) FocusedSessionID() string {
	shown := picker.shownSessions()
	if picker.cursor < len(shown) {
		return shown[picker.cursor].SessionID
	}
	return ""
}
