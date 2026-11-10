// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package connectorsmcp serves the agent-exposed operations of compiled-in Dex Connectors as MCP tools.
package connectorsmcp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MountPath is the HTTP path of the MCP endpoint, both standalone and inside dexcli dev.
const MountPath = "/mcp"

// Config configures the Connectors MCP server.
type Config struct {
	// ConnectorConfigDirectory defaults to $HOME/.dex/connectors and holds the connections.json that Dex Web writes.
	ConnectorConfigDirectory string
	// StateDirectory defaults to $HOME/.dex/connectors-mcp and keeps each keyed write's single-dispatch state.
	StateDirectory string
	// Connections defaults empty, which offers each compiled connector's connection when it has exactly one.
	// When set, it offers exactly these connections, at most one per connector.
	Connections []ConnectionSelector
	// AllowedHosts defaults empty and lists host names that requests may use besides loopback names and IP addresses.
	AllowedHosts []string
	// Version is the dexcli version reported as the MCP server version.
	Version string
}

// ConnectionSelector names one authorized connection as <connectorId>/<connectionName>.
type ConnectionSelector struct {
	ConnectorID    string
	ConnectionName string
}

// ParseConnectionSelector parses <connectorId>/<connectionName>.
func ParseConnectionSelector(value string) (ConnectionSelector, error) {
	connectorID, connectionName, found := strings.Cut(strings.TrimSpace(value), "/")
	if !found || strings.TrimSpace(connectorID) == "" || strings.TrimSpace(connectionName) == "" {
		return ConnectionSelector{}, fmt.Errorf("connection must be <connectorId>/<connectionName>: %q", value)
	}
	return ConnectionSelector{ConnectorID: strings.TrimSpace(connectorID), ConnectionName: strings.TrimSpace(connectionName)}, nil
}

func (selector ConnectionSelector) String() string {
	return selector.ConnectorID + "/" + selector.ConnectionName
}

// Server is the Connectors MCP endpoint. Its handler serves stateless Streamable HTTP.
type Server struct {
	source  *toolSetSource
	handler http.Handler
}

// NewServer validates cfg and the compiled agent manifests and prepares the state directory.
func NewServer(cfg *Config, logger *slog.Logger) (*Server, error) {
	if cfg == nil {
		panic("Connectors MCP config must not be nil")
	}
	if logger == nil {
		panic("Connectors MCP logger must not be nil")
	}
	if strings.TrimSpace(cfg.ConnectorConfigDirectory) == "" || strings.TrimSpace(cfg.StateDirectory) == "" {
		return nil, fmt.Errorf("Connectors MCP needs a connector config directory and a state directory")
	}
	catalogs, err := loadCompiledConnectorCatalogs()
	if err != nil {
		return nil, err
	}
	if err := validateConnectionSelectors(cfg.Connections, catalogs); err != nil {
		return nil, err
	}
	store, err := newCallStateStore(cfg.StateDirectory)
	if err != nil {
		return nil, err
	}
	if err := store.pruneExpired(); err != nil {
		logger.Warn("expired connector call state was not all removed", "error", err)
	}
	source := newToolSetSource(cfg, catalogs, newToolCallExecutor(store, logger), logger)
	streamable := mcp.NewStreamableHTTPHandler(
		func(request *http.Request) *mcp.Server { return source.currentToolSet(request.Context()).server },
		// The Host policy below replaces the SDK's localhost check, which would refuse every allowed host.
		&mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true, Logger: logger},
	)
	return &Server{source: source, handler: newRequestHostPolicy(cfg.AllowedHosts).protect(streamable)}, nil
}

// Handler serves the MCP endpoint at any path; mount it at MountPath.
func (server *Server) Handler() http.Handler {
	return server.handler
}

// ToolNames returns the names of the tools offered for the current connection files.
func (server *Server) ToolNames(ctx context.Context) []string {
	set := server.source.currentToolSet(ctx)
	names := make([]string, 0, len(set.bindings))
	for _, binding := range set.bindings {
		names = append(names, binding.name)
	}
	return names
}

func loadCompiledConnectorCatalogs() ([]compiledConnectorCatalog, error) {
	connectors := compiledConnectors()
	catalogs := make([]compiledConnectorCatalog, 0, len(connectors))
	for _, connector := range connectors {
		manifest, operations, err := loadAgentManifest(connector.connectorID)
		if err != nil {
			return nil, err
		}
		catalogs = append(catalogs, compiledConnectorCatalog{connector: connector, manifest: manifest, operations: operations})
	}
	return catalogs, nil
}

func validateConnectionSelectors(selectors []ConnectionSelector, catalogs []compiledConnectorCatalog) error {
	selectedConnectors := make(map[string]string)
	for _, selector := range selectors {
		isCompiled := false
		for _, catalog := range catalogs {
			isCompiled = isCompiled || catalog.connector.connectorID == selector.ConnectorID
		}
		if !isCompiled {
			return fmt.Errorf("Connector %q is not compiled into dexcli; compiled Connectors: %s", selector.ConnectorID, strings.Join(compiledConnectorIDs(catalogs), ", "))
		}
		if previous, exists := selectedConnectors[selector.ConnectorID]; exists {
			return fmt.Errorf("connections %s/%s and %s select one Connector twice; tool names must stay unique", selector.ConnectorID, previous, selector)
		}
		selectedConnectors[selector.ConnectorID] = selector.ConnectionName
	}
	return nil
}

func compiledConnectorIDs(catalogs []compiledConnectorCatalog) []string {
	identifiers := make([]string, 0, len(catalogs))
	for _, catalog := range catalogs {
		identifiers = append(identifiers, catalog.connector.connectorID)
	}
	return identifiers
}
