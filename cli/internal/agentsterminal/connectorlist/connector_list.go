// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package connectorlist is the agents terminal list of connector
// connections, refreshed through list_connectors.
package connectorlist

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launcherchannel"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// connectorsWebPath is the Dex Web Connectors page under web_url.
const connectorsWebPath = "/v1/connectors"

// LocalRequests are the launcher requests the list calls.
type LocalRequests interface {
	HasMethod(method string) bool
	DisableMethod(method string)
	Call(ctx context.Context, method string, key string, params any) launcherchannel.RequestOutcome
}

// ConnectorListConfig configures NewConnectorList.
type ConnectorListConfig struct {
	// Catalog is the launch document's connectors.
	Catalog ConnectorCatalog
	// Requests serves list_connectors and open_url. Required.
	Requests LocalRequests
	// WebURL is the Dex Web base URL, or "".
	WebURL string
	// Theme holds the styles and symbols. Required.
	Theme terminaltheme.Theme
}

// ListClosedMsg reports that the user closed the list.
type ListClosedMsg struct{}

// ConnectorList is the connectors list model.
type ConnectorList struct {
	ctx      context.Context
	config   ConnectorListConfig
	width    int
	height   int
	catalog  ConnectorCatalog
	rows     []connectionRow
	cursor   int
	topLine  int
	expanded map[int]bool

	isRefreshing   bool
	isOpening      bool
	statusText     string
	statusSeverity statusSeverity
	isClosed       bool
}

type statusSeverity int

const (
	statusInfo statusSeverity = iota
	statusOk
	statusWarning
	statusError
)

// refreshedMsg carries the outcome of one list_connectors request.
type refreshedMsg struct {
	outcome launcherchannel.RequestOutcome
}

// openedMsg carries the outcome of one open_url request.
type openedMsg struct {
	target  string
	outcome launcherchannel.RequestOutcome
}

// NewConnectorList returns a list of the catalog's connections.
func NewConnectorList(ctx context.Context, config ConnectorListConfig) *ConnectorList {
	if config.Requests == nil {
		panic("connectorlist: ConnectorListConfig.Requests is required")
	}
	return &ConnectorList{
		ctx: ctx, config: config, width: 80, height: 24,
		catalog: config.Catalog, rows: buildConnectionRows(config.Catalog), expanded: map[int]bool{},
	}
}

// Init does nothing: the launch document already holds the rows.
func (list *ConnectorList) Init() tea.Cmd {
	return nil
}

// Update handles keys and request outcomes. It returns a command that may
// deliver ListClosedMsg.
func (list *ConnectorList) Update(message tea.Msg) tea.Cmd {
	if list.isClosed {
		return nil
	}
	switch typed := message.(type) {
	case tea.WindowSizeMsg:
		list.width, list.height = max(typed.Width, 1), max(typed.Height, 1)
	case refreshedMsg:
		list.handleRefresh(typed.outcome)
	case openedMsg:
		list.handleOpened(typed)
	case tea.KeyPressMsg:
		return list.handleKey(typed)
	}
	return nil
}

func (list *ConnectorList) handleKey(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case "esc", "q", "ctrl+c", "ctrl+d":
		list.isClosed = true
		return func() tea.Msg { return ListClosedMsg{} }
	case "ctrl+l":
		return tea.ClearScreen
	case "up", "k":
		list.cursor = max(list.cursor-1, 0)
	case "down", "j":
		list.cursor = min(list.cursor+1, max(len(list.rows)-1, 0))
	case "pgup":
		list.cursor = max(list.cursor-max(list.height/3, 1), 0)
	case "pgdown":
		list.cursor = min(list.cursor+max(list.height/3, 1), max(len(list.rows)-1, 0))
	case "enter":
		return list.openOrExpand()
	case "a":
		return list.openConnectorsPage()
	case "r":
		return list.refresh()
	}
	return nil
}

// openOrExpand shows the tools of an authorized row, and opens Dex Web for
// a connection that still needs authorizing.
func (list *ConnectorList) openOrExpand() tea.Cmd {
	if list.cursor >= len(list.rows) {
		return nil
	}
	row := list.rows[list.cursor]
	if row.hasIdentity() && row.status != "" && row.status != "authorized" && list.canOpenWeb() {
		return list.openConnectorsPage()
	}
	list.expanded[list.cursor] = !list.expanded[list.cursor]
	return nil
}

func (list *ConnectorList) canRefresh() bool {
	return list.config.Requests.HasMethod(launchcontract.MethodListConnectors)
}

func (list *ConnectorList) canOpenWeb() bool {
	return list.config.WebURL != "" && list.config.Requests.HasMethod(launchcontract.MethodOpenURL)
}

func (list *ConnectorList) refresh() tea.Cmd {
	if !list.canRefresh() || list.isRefreshing {
		return nil
	}
	key, err := launchcontract.NewIdempotencyKey()
	if err != nil {
		list.setStatus(statusError, "Could not refresh: "+err.Error())
		return nil
	}
	list.isRefreshing = true
	list.setStatus(statusInfo, "refreshing…")
	requests, ctx := list.config.Requests, list.ctx
	return func() tea.Msg {
		return refreshedMsg{outcome: requests.Call(ctx, launchcontract.MethodListConnectors, key, map[string]any{})}
	}
}

func (list *ConnectorList) handleRefresh(outcome launcherchannel.RequestOutcome) {
	list.isRefreshing = false
	switch outcome.Kind {
	case launcherchannel.OutcomeDone:
		catalog, err := DecodeConnectorCatalog(outcome.Result)
		if err != nil {
			list.setStatus(statusError, "Could not refresh: the command returned an invalid list.")
			return
		}
		if catalog.Connections == nil {
			catalog.Connections = list.catalog.Connections
		}
		list.catalog, list.rows, list.expanded = catalog, buildConnectionRows(catalog), map[int]bool{}
		list.cursor = min(list.cursor, max(len(list.rows)-1, 0))
		list.setStatus(statusOk, fmt.Sprintf("refreshed: %d connector tools", len(catalog.Tools)))
	case launcherchannel.OutcomeFailed:
		switch outcome.Error.Code {
		case "mcp_unavailable":
			list.setStatus(statusWarning, "Connectors are off: the MCP server did not answer. Tools from connectors return after a restart.")
		case launchcontract.ErrorCodeUnknownMethod:
			list.config.Requests.DisableMethod(launchcontract.MethodListConnectors)
			list.setStatus(statusWarning, "Refresh is not available with this command version.")
		default:
			list.setStatus(statusError, "Could not refresh: "+outcome.Error.Message)
		}
	case launcherchannel.OutcomeChannelEnded:
		list.setStatus(statusError, "Could not refresh: the command closed the channel.")
	default:
		list.setStatus(statusWarning, "Could not refresh: the command did not answer. Press r to try again.")
	}
}

func (list *ConnectorList) openConnectorsPage() tea.Cmd {
	if !list.canOpenWeb() {
		list.setStatus(statusWarning, "Dex Web is not available with this command version.")
		return nil
	}
	if list.isOpening {
		return nil
	}
	target := strings.TrimRight(list.config.WebURL, "/") + connectorsWebPath
	if !launchcontract.IsOpenURLAllowed(list.config.WebURL, target) {
		list.setStatus(statusError, "Cannot open the Dex Web Connectors page.")
		return nil
	}
	key, err := launchcontract.NewIdempotencyKey()
	if err != nil {
		list.setStatus(statusError, "Dex Web did not open: "+err.Error())
		return nil
	}
	list.isOpening = true
	requests, ctx := list.config.Requests, list.ctx
	return func() tea.Msg {
		return openedMsg{target: target, outcome: requests.Call(ctx, launchcontract.MethodOpenURL, key, map[string]any{"url": target})}
	}
}

func (list *ConnectorList) handleOpened(message openedMsg) {
	list.isOpening = false
	switch message.outcome.Kind {
	case launcherchannel.OutcomeDone:
		hint := "Opened Dex Web."
		if list.canRefresh() {
			hint += " Press r after you add it."
		}
		list.setStatus(statusOk, hint)
	case launcherchannel.OutcomeFailed:
		if message.outcome.Error.Code == launchcontract.ErrorCodeUnknownMethod {
			list.config.Requests.DisableMethod(launchcontract.MethodOpenURL)
		}
		list.setStatus(statusError, "Dex Web did not open: "+message.outcome.Error.Message+" Open "+message.target+" yourself.")
	default:
		list.setStatus(statusWarning, "Dex Web may not have opened. Open "+message.target+" yourself.")
	}
}

func (list *ConnectorList) setStatus(severity statusSeverity, text string) {
	list.statusSeverity, list.statusText = severity, text
}
