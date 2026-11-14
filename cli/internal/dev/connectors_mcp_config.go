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
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	connectorsMCPBinaryName              = "dex-connectors-mcp"
	connectorsMCPBinaryEnvironment       = "DEX_CONNECTORS_MCP_BINARY"
	defaultConnectorsMCPIndexURL         = "https://connectors.superdurable.io/connectors-mcp/index.json"
	defaultConnectorsMCPIndexCheckPeriod = 24 * time.Hour
	defaultConnectorsMCPReadyTimeout     = 15 * time.Second
	defaultConnectorsMCPBackoffBase      = time.Second
	defaultConnectorsMCPBackoffMax       = 30 * time.Second
	defaultConnectorsMCPStableRun        = time.Minute
	defaultConnectorsMCPStartAttempts    = 5
	defaultConnectorsMCPHealthInterval   = 2 * time.Second
	connectorsMCPMountPath               = "/mcp"
	connectorConnectionsFileName         = "connections.json"
)

// connectorsMCPReleaseVersionPattern accepts X.Y.Z only, because a version names a cache directory.
var connectorsMCPReleaseVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

// connectorsMCPConnectorIDPattern is the Connector ID syntax the Connectors MCP server accepts.
var connectorsMCPConnectorIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

// ConnectorsMCPConfig configures how dexcli dev finds, starts, and proxies the Dex Connectors MCP server.
type ConnectorsMCPConfig struct {
	// IsEnabled defaults true. When false, dexcli dev neither resolves nor starts the server and does not serve /mcp.
	IsEnabled bool
	// Version defaults empty, which selects the newest release whose launch contract dexcli supports. A value such
	// as 0.1.3 pins that release; it must use a supported launch contract.
	Version string
	// BinaryPath defaults to DEX_CONNECTORS_MCP_BINARY. When set, dexcli runs this executable and never reads the
	// release index or the release cache.
	BinaryPath string
	// Connections defaults empty, which lets the server offer every authorized connection. Each entry has the form
	// connectorId/connectionName[=tool,tool...] and is passed to the server as --connection.
	Connections []string
	// IndexURL defaults to https://connectors.superdurable.io/connectors-mcp/index.json, the release index that the
	// Connectors library publishes. It must be an https URL.
	IndexURL string
	// IndexCheckInterval defaults to 24 hours. A cached index younger than this is used without fetching again.
	IndexCheckInterval time.Duration
	// StateDirectory defaults to <Dex state directory>/connectors-mcp for the default connections file, and to
	// <Dex state directory>/connectors-mcp-<digest of ConnectionsFile> for another one. It is passed to the server
	// as --state-dir; the server keeps server.json, its instance lock, and call state there.
	StateDirectory string
	// ToolsDirectory defaults to <Dex state directory>/tools/dex-connectors-mcp and caches the cached release index
	// and one directory per downloaded release version.
	ToolsDirectory string
	// ConnectionsFile defaults to <connector config directory>/connections.json, the file Dex Web writes when a
	// connection is authorized. It is passed to the server as --connections-file.
	ConnectionsFile string
	// ReadyTimeout defaults to 15 seconds and bounds the wait for the server's ready line.
	ReadyTimeout time.Duration
	// RestartBackoffBase defaults to 1 second. The delay before a restart doubles after each consecutive failure.
	RestartBackoffBase time.Duration
	// RestartBackoffMax defaults to 30 seconds and caps the restart delay.
	RestartBackoffMax time.Duration
	// StableRunDuration defaults to 1 minute. A server that ran at least this long resets the restart delay.
	StableRunDuration time.Duration
	// MaxConsecutiveStartFailures defaults to 5. After that many starts in a row fail before readiness, dexcli dev
	// stops trying and prints a hint; Dex Server and Dex Web keep running.
	MaxConsecutiveStartFailures int
	// HealthCheckInterval defaults to 2 seconds and paces the /healthz checks of a server that dexcli reused but
	// did not start.
	HealthCheckInterval time.Duration
	// ShutdownTimeout defaults to 10 seconds and bounds the wait for a started server to exit.
	ShutdownTimeout time.Duration
}

func defaultConnectorsMCPConfig() ConnectorsMCPConfig {
	return ConnectorsMCPConfig{
		IsEnabled:                   true,
		BinaryPath:                  os.Getenv(connectorsMCPBinaryEnvironment),
		IndexURL:                    defaultConnectorsMCPIndexURL,
		IndexCheckInterval:          defaultConnectorsMCPIndexCheckPeriod,
		ReadyTimeout:                defaultConnectorsMCPReadyTimeout,
		RestartBackoffBase:          defaultConnectorsMCPBackoffBase,
		RestartBackoffMax:           defaultConnectorsMCPBackoffMax,
		StableRunDuration:           defaultConnectorsMCPStableRun,
		MaxConsecutiveStartFailures: defaultConnectorsMCPStartAttempts,
		HealthCheckInterval:         defaultConnectorsMCPHealthInterval,
		ShutdownTimeout:             10 * time.Second,
	}
}

// resolvePaths fills the directories and files derived from the Dex state and connector config directories.
func (c *ConnectorsMCPConfig) resolvePaths(stateDirectory string, connectorConfigDirectory string) error {
	absoluteStateDirectory, err := filepath.Abs(stateDirectory)
	if err != nil {
		return fmt.Errorf("resolve Dex state directory: %w", err)
	}
	absoluteConnectorConfigDirectory, err := filepath.Abs(connectorConfigDirectory)
	if err != nil {
		return fmt.Errorf("resolve connector config directory: %w", err)
	}
	if c.ConnectionsFile == "" {
		c.ConnectionsFile = filepath.Join(absoluteConnectorConfigDirectory, connectorConnectionsFileName)
	}
	absoluteConnectionsFile, err := filepath.Abs(c.ConnectionsFile)
	if err != nil {
		return fmt.Errorf("resolve connections file: %w", err)
	}
	// Aliases of one physical file must share one state directory and instance lock.
	c.ConnectionsFile = resolveExistingSymlinks(absoluteConnectionsFile)
	if c.StateDirectory == "" {
		c.StateDirectory = connectorsMCPStateDirectory(absoluteStateDirectory, c.ConnectionsFile)
	}
	if c.ToolsDirectory == "" {
		c.ToolsDirectory = filepath.Join(absoluteStateDirectory, "tools", connectorsMCPBinaryName)
	}
	return nil
}

// connectorsMCPStateDirectory returns <state>/connectors-mcp for the default connections file, and a directory
// suffixed with a digest of any other file, so each connections file gets its own server and call state.
// connectionsFile must have its symlinks resolved.
func connectorsMCPStateDirectory(stateDirectory string, connectionsFile string) string {
	defaultConnectionsFile := resolveExistingSymlinks(
		filepath.Join(stateDirectory, "connectors", connectorConnectionsFileName))
	if filepath.Clean(connectionsFile) == defaultConnectionsFile {
		return filepath.Join(stateDirectory, "connectors-mcp")
	}
	digest := sha256.Sum256([]byte(filepath.Clean(connectionsFile)))
	return filepath.Join(stateDirectory, "connectors-mcp-"+hex.EncodeToString(digest[:])[:12])
}

// resolveExistingSymlinks returns the absolute path with symlinks resolved in its longest existing prefix. The
// missing rest, such as a connections file Dex Web has not written yet, is appended unchanged.
func resolveExistingSymlinks(absolutePath string) string {
	existingPrefix := filepath.Clean(absolutePath)
	var missingParts []string
	for {
		resolved, err := filepath.EvalSymlinks(existingPrefix)
		if err == nil {
			return filepath.Join(append([]string{resolved}, missingParts...)...)
		}
		parent := filepath.Dir(existingPrefix)
		if parent == existingPrefix {
			// Nothing resolves, not even the root; the cleaned path is the best identity left.
			return filepath.Clean(absolutePath)
		}
		missingParts = append([]string{filepath.Base(existingPrefix)}, missingParts...)
		existingPrefix = parent
	}
}

func (c *ConnectorsMCPConfig) validate() error {
	c.Version = strings.TrimSpace(c.Version)
	if c.Version != "" && !connectorsMCPReleaseVersionPattern.MatchString(c.Version) {
		return fmt.Errorf("connectors MCP version must have the form X.Y.Z: %q", c.Version)
	}
	c.BinaryPath = strings.TrimSpace(c.BinaryPath)
	if c.BinaryPath != "" {
		absoluteBinaryPath, err := filepath.Abs(c.BinaryPath)
		if err != nil {
			return fmt.Errorf("resolve connectors MCP binary: %w", err)
		}
		c.BinaryPath = absoluteBinaryPath
	}
	if c.BinaryPath != "" && c.Version != "" {
		return fmt.Errorf("--connectors-mcp-binary and --connectors-mcp-version cannot be combined")
	}
	if !strings.HasPrefix(c.IndexURL, "https://") {
		return fmt.Errorf("connectors MCP release index must be an https URL: %q", c.IndexURL)
	}
	return nil
}

// addConnectorsMCPServerFlags defines the Connectors MCP server flags that dexcli dev and dexcli connectors mcp share.
func addConnectorsMCPServerFlags(flags *flag.FlagSet, cfg *ConnectorsMCPConfig, connections *connectorsMCPConnectionFlag) {
	flags.StringVar(
		&cfg.Version,
		"connectors-mcp-version",
		"",
		"Connectors MCP server release X.Y.Z to run (default the newest compatible release)",
	)
	flags.StringVar(
		&cfg.BinaryPath,
		"connectors-mcp-binary",
		cfg.BinaryPath,
		"local dex-connectors-mcp executable to run instead of a downloaded release (default "+connectorsMCPBinaryEnvironment+")",
	)
	flags.Var(
		connections,
		"connectors-mcp-connection",
		"connection the Connectors MCP server offers, as connectorId/connectionName[=tool,...] (repeatable; default every authorized connection)",
	)
}

// connectorsMCPConnectionFlag collects repeatable --connectors-mcp-connection values.
type connectorsMCPConnectionFlag struct {
	values []string
}

func (value *connectorsMCPConnectionFlag) String() string {
	return ""
}

func (value *connectorsMCPConnectionFlag) Set(raw string) error {
	connection, err := parseConnectorsMCPConnection(raw)
	if err != nil {
		return err
	}
	for _, existing := range value.values {
		existingReference, _, _ := strings.Cut(existing, "=")
		reference, _, _ := strings.Cut(connection, "=")
		if existingReference == reference {
			return fmt.Errorf("connectors MCP connection %q is duplicated", reference)
		}
	}
	value.values = append(value.values, connection)
	return nil
}

// parseConnectorsMCPConnection returns raw without surrounding spaces when it has the form
// connectorId/connectionName[=tool,tool...], or an error naming the malformed part.
func parseConnectorsMCPConnection(raw string) (string, error) {
	connection := strings.TrimSpace(raw)
	reference, toolList, hasTools := strings.Cut(connection, "=")
	connectorID, connectionName, found := strings.Cut(reference, "/")
	if !found || !connectorsMCPConnectorIDPattern.MatchString(connectorID) || strings.TrimSpace(connectionName) == "" ||
		strings.Contains(connectionName, ",") {
		return "", fmt.Errorf("connectors MCP connection must be connectorId/connectionName[=tool,...]: %q", raw)
	}
	if !hasTools {
		return connection, nil
	}
	seenTools := make(map[string]bool)
	for _, tool := range strings.Split(toolList, ",") {
		if strings.TrimSpace(tool) == "" || tool != strings.TrimSpace(tool) {
			return "", fmt.Errorf("connectors MCP connection %q has an empty or padded tool name", raw)
		}
		if seenTools[tool] {
			return "", fmt.Errorf("connectors MCP connection %q lists tool %q twice", raw, tool)
		}
		seenTools[tool] = true
	}
	return connection, nil
}
