// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorlist

import (
	"encoding/json"
	"errors"
	"sort"

	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
)

// ConnectorCatalog is a list_connectors result, or the launch document's
// connectors.
type ConnectorCatalog struct {
	// MCPURL is the connectors MCP server URL.
	MCPURL string `json:"mcp_url"`
	// Tools has one row per connector tool.
	Tools []ConnectorTool `json:"tools"`
	// Connections are the Dex Web connections, or nil when the launcher did
	// not read them.
	Connections []ConnectorConnection `json:"connections"`
}

// ConnectorTool is one connector tool.
type ConnectorTool struct {
	// Name is the tool name the agent sees.
	Name string `json:"name"`
	// Connector is the connector name, or nil without a Dex identity.
	Connector *string `json:"connector"`
	// Connection is the connection name, or nil without a Dex identity.
	Connection *string `json:"connection"`
	// Operation is the connector operation, or nil.
	Operation *string `json:"operation"`
	// IsEnabled reports the tool enabled by connectors.enabled.
	IsEnabled bool `json:"is_enabled"`
}

// ConnectorConnection is one Dex Web connection.
type ConnectorConnection struct {
	// Connector is the connector name.
	Connector string `json:"connector"`
	// Connection is the connection name.
	Connection string `json:"connection"`
	// Status is the connection status, such as "authorized" or "pending".
	Status string `json:"status"`
}

var errInvalidCatalog = errors.New("the connector list does not match the contract")

// DecodeConnectorCatalog checks raw against the list_connectors result schema
// and decodes it.
func DecodeConnectorCatalog(raw json.RawMessage) (ConnectorCatalog, error) {
	if len(raw) == 0 || launchcontract.CheckMethodResultJSON(launchcontract.MethodListConnectors, raw) != nil {
		return ConnectorCatalog{}, errInvalidCatalog
	}
	var catalog ConnectorCatalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return ConnectorCatalog{}, errInvalidCatalog
	}
	return catalog, nil
}

// connectionRow is one connection with its tools.
type connectionRow struct {
	// connector and connection are "" for tools without a Dex identity.
	connector  string
	connection string
	// status is the Dex Web status, or "" when unknown.
	status string
	tools  []ConnectorTool
}

func (row connectionRow) hasIdentity() bool {
	return row.connector != ""
}

// enabledState is "yes", "no", "some", or "—" for a row without tools.
func (row connectionRow) enabledState() string {
	enabledCount := 0
	for _, tool := range row.tools {
		if tool.IsEnabled {
			enabledCount++
		}
	}
	switch {
	case len(row.tools) == 0:
		return "—"
	case enabledCount == len(row.tools):
		return "yes"
	case enabledCount == 0:
		return "no"
	}
	return "some"
}

// buildConnectionRows groups the tools by connection, in name order.
// Tools without a Dex identity come last.
func buildConnectionRows(catalog ConnectorCatalog) []connectionRow {
	type rowKey struct{ connector, connection string }
	rowsByKey := map[rowKey]*connectionRow{}
	var keys []rowKey
	rowOf := func(key rowKey) *connectionRow {
		if row, exists := rowsByKey[key]; exists {
			return row
		}
		row := &connectionRow{connector: key.connector, connection: key.connection}
		rowsByKey[key] = row
		keys = append(keys, key)
		return row
	}
	for _, connection := range catalog.Connections {
		rowOf(rowKey{connection.Connector, connection.Connection}).status = connection.Status
	}
	var otherTools []ConnectorTool
	for _, tool := range catalog.Tools {
		if tool.Connector == nil || tool.Connection == nil {
			otherTools = append(otherTools, tool)
			continue
		}
		row := rowOf(rowKey{*tool.Connector, *tool.Connection})
		row.tools = append(row.tools, tool)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].connector != keys[right].connector {
			return keys[left].connector < keys[right].connector
		}
		return keys[left].connection < keys[right].connection
	})
	rows := make([]connectionRow, 0, len(keys)+1)
	for _, key := range keys {
		rows = append(rows, *rowsByKey[key])
	}
	if len(otherTools) > 0 {
		rows = append(rows, connectionRow{tools: otherTools})
	}
	return rows
}
