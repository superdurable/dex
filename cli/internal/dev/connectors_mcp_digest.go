// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dev

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

// connectorsMCPConnectionsConfigDigest returns the server.json connectionsConfigDigest of a connection selection,
// or nil for no selection. Each entry has the form connectorId/connectionName[=tool,...]. It returns an error when
// a connection appears twice.
//
// The digest is "sha256:" plus the lowercase hex SHA-256 of the canonical JSON
// {"connections":[{"connection":...,"tools":[...]|null},...]}: items sorted by connection, tools deduplicated and
// sorted, both by bytes.
func connectorsMCPConnectionsConfigDigest(connections []string) (*string, error) {
	if len(connections) == 0 {
		return nil, nil
	}
	type selectedConnection struct {
		connection string
		// tools is nil when the entry has no tool list.
		tools []string
	}
	selected := make([]selectedConnection, 0, len(connections))
	seenConnections := make(map[string]bool, len(connections))
	for _, entry := range connections {
		connection, toolList, hasTools := strings.Cut(entry, "=")
		if seenConnections[connection] {
			return nil, fmt.Errorf("connectors MCP connection %q is duplicated", connection)
		}
		seenConnections[connection] = true
		var tools []string
		if hasTools {
			tools = strings.Split(toolList, ",")
			slices.Sort(tools)
			tools = slices.Compact(tools)
		}
		selected = append(selected, selectedConnection{connection: connection, tools: tools})
	}
	slices.SortFunc(selected, func(left selectedConnection, right selectedConnection) int {
		return strings.Compare(left.connection, right.connection)
	})
	var canonical strings.Builder
	canonical.WriteString(`{"connections":[`)
	for position, item := range selected {
		if position > 0 {
			canonical.WriteByte(',')
		}
		canonical.WriteString(`{"connection":`)
		writeCanonicalJSONString(&canonical, item.connection)
		canonical.WriteString(`,"tools":`)
		if item.tools == nil {
			canonical.WriteString("null")
		} else {
			canonical.WriteByte('[')
			for toolPosition, tool := range item.tools {
				if toolPosition > 0 {
					canonical.WriteByte(',')
				}
				writeCanonicalJSONString(&canonical, tool)
			}
			canonical.WriteByte(']')
		}
		canonical.WriteByte('}')
	}
	canonical.WriteString("]}")
	sum := sha256.Sum256([]byte(canonical.String()))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	return &digest, nil
}

// writeCanonicalJSONString writes value as an RFC 8785 JSON string: only quote, backslash, and control characters
// are escaped, so other characters, including HTML characters and U+2028, stay raw UTF-8.
func writeCanonicalJSONString(builder *strings.Builder, value string) {
	builder.WriteByte('"')
	for _, character := range value {
		switch character {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\b':
			builder.WriteString(`\b`)
		case '\f':
			builder.WriteString(`\f`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		default:
			if character < 0x20 {
				fmt.Fprintf(builder, `\u%04x`, character)
			} else {
				builder.WriteRune(character)
			}
		}
	}
	builder.WriteByte('"')
}

// isSameConnectionsConfigDigest reports whether two optional digests are equal; two nil digests are equal.
func isSameConnectionsConfigDigest(left *string, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
