// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorsmcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex-connectors-library/sdkgo/projectconfig"
)

const (
	connectionsFileName       = "connections.json"
	useConfigurationsFileName = "use-configurations.json"
	// maximumToolNameLength is the longest tool name every model API accepts.
	maximumToolNameLength = 64
	mcpImplementationName = "dexcli-connectors"
)

// toolBinding is one offered tool: a connector operation bound to one authorized connection.
type toolBinding struct {
	name             string
	tool             *mcp.Tool
	connectorID      string
	connectionName   string
	operation        agentOperation
	runner           operationRunner
	reference        sdkgo.ConnectionRef
	requiredBranches map[sdkgo.BranchID]bool
}

// toolSet is the MCP server for one snapshot of the connection files.
type toolSet struct {
	server   *mcp.Server
	bindings []*toolBinding
}

// connectionFilesFingerprint changes whenever Dex Web rewrites a connection file.
type connectionFilesFingerprint struct {
	connections       fileFingerprint
	useConfigurations fileFingerprint
}

type fileFingerprint struct {
	exists  bool
	size    int64
	modTime time.Time
	mode    os.FileMode
}

// compiledConnectorCatalog is a compiled connector with its validated agent manifest.
type compiledConnectorCatalog struct {
	connector  compiledConnector
	manifest   agentManifest
	operations map[string]agentOperation
}

// toolSetSource builds the tool set from connections.json and rebuilds it when a connection file changes.
type toolSetSource struct {
	cfg      *Config
	catalogs []compiledConnectorCatalog
	calls    *toolCallExecutor
	logger   *slog.Logger

	mu          sync.Mutex
	fingerprint connectionFilesFingerprint
	current     *toolSet
}

func newToolSetSource(cfg *Config, catalogs []compiledConnectorCatalog, calls *toolCallExecutor, logger *slog.Logger) *toolSetSource {
	if cfg == nil || calls == nil || logger == nil {
		panic("tool set source dependencies must not be nil")
	}
	return &toolSetSource{cfg: cfg, catalogs: catalogs, calls: calls, logger: logger}
}

// currentToolSet returns the tool set of the connection files as they are now.
func (source *toolSetSource) currentToolSet(ctx context.Context) *toolSet {
	fingerprint := source.readFingerprint()
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.current != nil && fingerprint == source.fingerprint {
		return source.current
	}
	source.current = source.buildToolSet(ctx)
	source.fingerprint = fingerprint
	return source.current
}

func (source *toolSetSource) readFingerprint() connectionFilesFingerprint {
	directory := source.cfg.ConnectorConfigDirectory
	return connectionFilesFingerprint{
		connections:       readFileFingerprint(filepath.Join(directory, connectionsFileName)),
		useConfigurations: readFileFingerprint(filepath.Join(directory, useConfigurationsFileName)),
	}
}

func readFileFingerprint(path string) fileFingerprint {
	info, err := os.Lstat(path)
	if err != nil {
		// Any unreadable state rebuilds once and reports the load error then.
		return fileFingerprint{}
	}
	return fileFingerprint{exists: true, size: info.Size(), modTime: info.ModTime(), mode: info.Mode()}
}

func (source *toolSetSource) buildToolSet(ctx context.Context) *toolSet {
	server := mcp.NewServer(&mcp.Implementation{Name: mcpImplementationName, Version: source.cfg.Version}, nil)
	set := &toolSet{server: server}
	connectionsPath := filepath.Join(source.cfg.ConnectorConfigDirectory, connectionsFileName)
	project, err := loadLocalProject(ctx, connectionsPath)
	if errors.Is(err, os.ErrNotExist) {
		source.logger.Info("no connector connections yet; authorize one on the Dex Web Connectors page", "file", connectionsPath)
		return set
	}
	if err != nil {
		source.logger.Warn("connector connections cannot be loaded; serving no tools", "file", connectionsPath, "error", err)
		return set
	}
	for _, selected := range source.selectConnections(project) {
		set.bindings = append(set.bindings, source.bindConnection(project, selected)...)
	}
	slices.SortFunc(set.bindings, func(left, right *toolBinding) int { return strings.Compare(left.name, right.name) })
	for _, binding := range set.bindings {
		server.AddTool(binding.tool, source.calls.handler(binding))
	}
	return set
}

// selectedConnection is a connection to offer, with the catalog of its connector.
type selectedConnection struct {
	catalog        compiledConnectorCatalog
	connectionName string
}

// selectConnections returns the configured connections, or by default each compiled connector's only connection.
func (source *toolSetSource) selectConnections(project *projectconfig.LoadedProject) []selectedConnection {
	authorized := make(map[string][]string)
	for _, connection := range project.Configuration.Connections {
		authorized[connection.ConnectorID] = append(authorized[connection.ConnectorID], connection.ConnectionName)
	}
	var selected []selectedConnection
	if len(source.cfg.Connections) > 0 {
		for _, selector := range source.cfg.Connections {
			catalog, found := source.catalogOf(selector.ConnectorID)
			if !found {
				source.logger.Warn("Connector is not compiled into dexcli; skipping", "connection", selector.String())
				continue
			}
			if !slices.Contains(authorized[selector.ConnectorID], selector.ConnectionName) {
				source.logger.Warn("connection is not authorized in Dex Web; skipping", "connection", selector.String())
				continue
			}
			selected = append(selected, selectedConnection{catalog: catalog, connectionName: selector.ConnectionName})
		}
		return selected
	}
	for _, catalog := range source.catalogs {
		names := authorized[catalog.connector.connectorID]
		switch len(names) {
		case 0:
		case 1:
			selected = append(selected, selectedConnection{catalog: catalog, connectionName: names[0]})
		default:
			slices.Sort(names)
			source.logger.Warn(
				"several connections of one Connector are authorized; select one with --connection to offer its tools",
				"connector", catalog.connector.connectorID, "connections", strings.Join(names, ","))
		}
	}
	return selected
}

func (source *toolSetSource) catalogOf(connectorID string) (compiledConnectorCatalog, bool) {
	for _, catalog := range source.catalogs {
		if catalog.connector.connectorID == connectorID {
			return catalog, true
		}
	}
	return compiledConnectorCatalog{}, false
}

// bindConnection opens one connection and returns a tool for each of its agent-exposed operations.
func (source *toolSetSource) bindConnection(project *projectconfig.LoadedProject, selected selectedConnection) []*toolBinding {
	connectorID := selected.catalog.connector.connectorID
	opened, err := selected.catalog.connector.openConnection(project, selected.connectionName)
	if err != nil {
		source.logger.Warn("connection cannot be opened; skipping", "connector", connectorID, "connection", selected.connectionName, "error", err)
		return nil
	}
	var bindings []*toolBinding
	for _, runner := range opened.runners {
		operation, isExposed := selected.catalog.operations[runner.operationRef().OperationID]
		if !isExposed {
			continue
		}
		binding, err := newToolBinding(connectorID, selected.connectionName, opened.reference, operation, runner)
		if err != nil {
			source.logger.Warn("operation cannot be offered; skipping", "connector", connectorID, "operation", operation.manifest.OperationID, "error", err)
			continue
		}
		bindings = append(bindings, binding)
	}
	return bindings
}

// newToolBinding checks the manifest entry against the compiled operation and builds its tool definition.
func newToolBinding(
	connectorID string,
	connectionName string,
	reference sdkgo.ConnectionRef,
	operation agentOperation,
	runner operationRunner,
) (*toolBinding, error) {
	if err := checkManifestMatchesOperation(operation, runner); err != nil {
		return nil, err
	}
	name := toolNameOf(connectorID, operation.manifest.OperationID)
	if len(name) > maximumToolNameLength {
		return nil, fmt.Errorf("tool name %s is longer than %d characters", name, maximumToolNameLength)
	}
	requiredBranches := make(map[sdkgo.BranchID]bool)
	for _, branch := range runner.branches() {
		if !branch.Optional {
			requiredBranches[branch.ID] = true
		}
	}
	meta := mcp.Meta{
		metaKeyEffect:     string(operation.effect),
		metaKeyConnector:  connectorID,
		metaKeyOperation:  operation.manifest.OperationID,
		metaKeyConnection: connectionName,
	}
	if operation.manifest.Agent.Approval == "recommended" {
		meta[metaKeyApproval] = "recommended"
	}
	tool := &mcp.Tool{
		Meta:        meta,
		Name:        name,
		Description: operation.manifest.Description,
		InputSchema: operation.manifest.InputSchema,
		Annotations: toolAnnotationsOf(operation.effect),
	}
	return &toolBinding{
		name:             name,
		tool:             tool,
		connectorID:      connectorID,
		connectionName:   connectionName,
		operation:        operation,
		runner:           runner,
		reference:        reference,
		requiredBranches: requiredBranches,
	}, nil
}

// checkManifestMatchesOperation returns an error when the manifest's kind or agent.branches differ from the operation.
func checkManifestMatchesOperation(operation agentOperation, runner operationRunner) error {
	if operation.manifest.Kind != runner.kind() {
		return fmt.Errorf("the manifest says %s, the compiled operation is a %s", operation.manifest.Kind, runner.kind())
	}
	optional := make(map[string]bool)
	for _, branch := range runner.branches() {
		if branch.Optional {
			optional[string(branch.ID)] = true
		}
	}
	for branch := range operation.manifest.Agent.Branches {
		if !optional[branch] {
			return fmt.Errorf("agent.branches lists %s, which is not an optional branch of the compiled operation", branch)
		}
	}
	return nil
}

// toolAnnotationsOf returns hints that the agent cores' MCP sources map back to the same effect class.
func toolAnnotationsOf(effect toolEffect) *mcp.ToolAnnotations {
	isOpenWorld := true
	switch effect {
	case toolEffectRead:
		return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &isOpenWorld}
	case toolEffectIdempotentWrite:
		return &mcp.ToolAnnotations{IdempotentHint: true, OpenWorldHint: &isOpenWorld}
	default:
		return &mcp.ToolAnnotations{OpenWorldHint: &isOpenWorld}
	}
}
