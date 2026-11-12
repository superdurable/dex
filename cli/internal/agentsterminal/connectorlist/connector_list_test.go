// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorlist

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launcherchannel"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// launchConnectors is the connectors of the conformance transcripts.
const launchConnectors = `{"mcp_url":"http://127.0.0.1:8803/mcp","tools":[
 {"name":"hacker_news_list_story_ids","connector":"hacker-news","connection":"default","operation":"list_story_ids","is_enabled":true},
 {"name":"slack_post_message","connector":"slack","connection":"support-slack","operation":"post_message","is_enabled":false},
 {"name":"lookup","connector":null,"connection":null,"operation":null,"is_enabled":true}],
 "connections":[{"connector":"hacker-news","connection":"default","status":"authorized"},
  {"connector":"slack","connection":"support-slack","status":"pending"},
  {"connector":"gmail","connection":"personal","status":"authorized"}]}`

// fakeRequests answers each method with a queued outcome.
type fakeRequests struct {
	methods  []string
	outcomes map[string]launcherchannel.RequestOutcome
	calls    []string
	params   []any
}

func (requests *fakeRequests) HasMethod(method string) bool {
	return slices.Contains(requests.methods, method)
}

func (requests *fakeRequests) DisableMethod(method string) {
	requests.methods = slices.DeleteFunc(requests.methods, func(listed string) bool { return listed == method })
}

func (requests *fakeRequests) Call(_ context.Context, method string, _ string, params any) launcherchannel.RequestOutcome {
	requests.calls = append(requests.calls, method)
	requests.params = append(requests.params, params)
	return requests.outcomes[method]
}

func newTestList(t *testing.T, requests *fakeRequests, webURL string) *ConnectorList {
	catalog, err := DecodeConnectorCatalog(json.RawMessage(launchConnectors))
	require.NoError(t, err)
	list := NewConnectorList(context.Background(), ConnectorListConfig{
		Catalog: catalog, Requests: requests, WebURL: webURL, Theme: terminaltheme.NewTheme(colorprofile.ASCII, true),
	})
	list.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return list
}

func press(list *ConnectorList, name string) []tea.Msg {
	key := tea.KeyPressMsg{Code: []rune(name)[0], Text: name}
	if name == "enter" {
		key = tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	command := list.Update(key)
	if command == nil {
		return nil
	}
	message := command()
	if followUp := list.Update(message); followUp != nil {
		return []tea.Msg{message, followUp()}
	}
	return []tea.Msg{message}
}

func screen(list *ConnectorList) string {
	return terminaltheme.StripStyles(list.View())
}

// Tools group by connection; tools without a Dex identity share the
// last row.
func TestConnectorRowsFromTheLaunch(t *testing.T) {
	list := newTestList(t, &fakeRequests{}, "")
	text := screen(list)
	require.Contains(t, text, "Connectors · MCP http://127.0.0.1:8803/mcp")
	require.Contains(t, text, "3 connections")
	require.Regexp(t, `gmail/personal\s+✓ authorized\s+—\s+—`, text)
	require.Regexp(t, `hacker-news/default\s+✓ authorized\s+yes\s+1`, text)
	require.Regexp(t, `slack/support-slack\s+! pending\s+no\s+1`, text)
	require.Regexp(t, `\(no connector\)\s+—\s+yes\s+1`, text)
	require.NotContains(t, text, "r refresh", "no refresh key without list_connectors")
	require.NotContains(t, text, "a add in Dex Web", "no Dex Web key without open_url")
	lines := strings.Split(text, "\n")
	require.Less(t, slices.IndexFunc(lines, func(line string) bool { return strings.Contains(line, "gmail") }),
		slices.IndexFunc(lines, func(line string) bool { return strings.Contains(line, "(no connector)") }))
}

// Rows without connections show no status.
func TestConnectorRowsWithoutConnections(t *testing.T) {
	catalog, err := DecodeConnectorCatalog(json.RawMessage(`{"mcp_url":"http://127.0.0.1:8803/mcp","tools":[
	 {"name":"a","connector":"slack","connection":"support","operation":"post","is_enabled":true},
	 {"name":"b","connector":"slack","connection":"support","operation":"read","is_enabled":false}],"connections":null}`))
	require.NoError(t, err)
	rows := buildConnectionRows(catalog)
	require.Len(t, rows, 1)
	require.Equal(t, "", rows[0].status)
	require.Equal(t, "some", rows[0].enabledState())
	_, err = DecodeConnectorCatalog(json.RawMessage(`{"tools":[]}`))
	require.Error(t, err, "mcp_url is required")
}

// r refreshes once; a result without connections keeps the known ones.
func TestConnectorRefresh(t *testing.T) {
	requests := &fakeRequests{
		methods: []string{launchcontract.MethodListConnectors},
		outcomes: map[string]launcherchannel.RequestOutcome{launchcontract.MethodListConnectors: {
			Kind: launcherchannel.OutcomeDone,
			Result: json.RawMessage(`{"mcp_url":"http://127.0.0.1:8803/mcp","tools":[
			 {"name":"slack_post_message","connector":"slack","connection":"support-slack","operation":"post_message","is_enabled":true}],"connections":null}`),
		}},
	}
	list := newTestList(t, requests, "")
	require.Contains(t, screen(list), "r refresh")
	press(list, "r")
	require.Equal(t, []string{launchcontract.MethodListConnectors}, requests.calls)
	text := screen(list)
	require.Contains(t, text, "refreshed: 1 connector tools")
	require.Regexp(t, `slack/support-slack\s+! pending\s+yes\s+1`, text)
	require.NotContains(t, text, "(no connector)")
}

// mcp_unavailable keeps the rows and says the connectors are off;
// unknown_method turns the refresh key off.
func TestConnectorRefreshFailures(t *testing.T) {
	requests := &fakeRequests{
		methods: []string{launchcontract.MethodListConnectors},
		outcomes: map[string]launcherchannel.RequestOutcome{launchcontract.MethodListConnectors: {
			Kind:  launcherchannel.OutcomeFailed,
			Error: &launchcontract.ChannelError{Code: "mcp_unavailable", Message: "The connectors MCP server is unavailable."},
		}},
	}
	list := newTestList(t, requests, "")
	press(list, "r")
	text := screen(list)
	require.Contains(t, text, "Connectors are off: the MCP server did not answer.")
	require.Contains(t, text, "hacker-news/default")

	requests.outcomes[launchcontract.MethodListConnectors] = launcherchannel.RequestOutcome{
		Kind: launcherchannel.OutcomeFailed, Error: &launchcontract.ChannelError{Code: launchcontract.ErrorCodeUnknownMethod, Message: "unknown"},
	}
	press(list, "r")
	require.Contains(t, screen(list), "Refresh is not available with this command version.")
	require.NotContains(t, screen(list), "r refresh")
	press(list, "r")
	require.Len(t, requests.calls, 2, "no request after unknown_method")

	requests.methods = []string{launchcontract.MethodListConnectors}
	requests.outcomes[launchcontract.MethodListConnectors] = launcherchannel.RequestOutcome{Kind: launcherchannel.OutcomeUnknown}
	press(list, "r")
	require.Contains(t, screen(list), "did not answer. Press r to try again.")
}

// a and Enter on a pending row open Dex Web; Enter on an authorized row
// shows tools.
func TestConnectorOpenDexWeb(t *testing.T) {
	requests := &fakeRequests{
		methods:  []string{launchcontract.MethodListConnectors, launchcontract.MethodOpenURL},
		outcomes: map[string]launcherchannel.RequestOutcome{launchcontract.MethodOpenURL: {Kind: launcherchannel.OutcomeDone, Result: json.RawMessage(`{}`)}},
	}
	list := newTestList(t, requests, "http://127.0.0.1:8802")
	require.Contains(t, screen(list), "a add in Dex Web")
	press(list, "a")
	require.Equal(t, []any{map[string]any{"url": "http://127.0.0.1:8802/v1/connectors"}}, requests.params)
	require.Contains(t, screen(list), "Opened Dex Web. Press r after you add it.")

	press(list, "j")
	press(list, "enter")
	require.Contains(t, screen(list), "list_story_ids  enabled", "Enter shows the tools of an authorized row")
	press(list, "j")
	press(list, "enter")
	require.Len(t, requests.calls, 2, "Enter on a pending connection opens Dex Web")
}

// Esc, q, and Ctrl-D close the list.
func TestConnectorListCloses(t *testing.T) {
	list := newTestList(t, &fakeRequests{}, "")
	messages := press(list, "q")
	require.Equal(t, []tea.Msg{ListClosedMsg{}}, messages)
}
