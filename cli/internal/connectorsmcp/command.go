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
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultListenAddress is where dexcli connectors mcp listens without --listen.
	DefaultListenAddress = "127.0.0.1:8803"
	// StateDirectoryName is the Connectors MCP state directory inside the Dex state directory.
	StateDirectoryName = "connectors-mcp"
	shutdownTimeout    = 10 * time.Second
)

// commandConfig is the configuration of dexcli connectors mcp.
type commandConfig struct {
	// listenAddress defaults to 127.0.0.1:8803 and must be a loopback address, because tools carry no auth.
	listenAddress string
	server        Config
}

// ExecuteConnectors runs a dexcli connectors subcommand.
func ExecuteConnectors(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, version string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printConnectorsUsage(stdout)
		return nil
	}
	if args[0] != "mcp" {
		printConnectorsUsage(stderr)
		return fmt.Errorf("unknown dexcli connectors command %q", args[0])
	}
	cfg, err := parseCommandConfig(args[1:], stderr, version)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	return serve(ctx, cfg, stdout, stderr)
}

func printConnectorsUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: dexcli connectors <command>")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Commands:")
	fmt.Fprintln(output, "  mcp   Serve the agent tools of authorized Connector connections over MCP")
}

func parseCommandConfig(args []string, output io.Writer, version string) (*commandConfig, error) {
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory for Dex state: %w", err)
	}
	stateDirectory := filepath.Join(homeDirectory, ".dex")
	cfg := &commandConfig{server: Config{Version: version}}
	var allowedHosts string
	var connections ConnectionSelectorsFlag
	flags := flag.NewFlagSet("dexcli connectors mcp", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&cfg.listenAddress, "listen", DefaultListenAddress, "loopback host:port to serve MCP Streamable HTTP on, at "+MountPath)
	flags.StringVar(
		&cfg.server.ConnectorConfigDirectory, "connector-config-dir", filepath.Join(stateDirectory, "connectors"),
		"directory holding the connections.json that Dex Web writes",
	)
	flags.StringVar(
		&cfg.server.StateDirectory, "state-dir", filepath.Join(stateDirectory, StateDirectoryName),
		"directory keeping the single-dispatch state of keyed connector writes",
	)
	flags.Var(&connections, "connection", "connection to offer as <connectorId>/<connectionName> (repeatable; default each Connector's only connection)")
	flags.StringVar(&allowedHosts, "allowed-hosts", "", "comma-separated extra host names that MCP requests may use")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: dexcli connectors mcp [flags]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	cfg.server.Connections = connections.Selectors
	cfg.server.AllowedHosts = SplitAllowedHosts(allowedHosts)
	if err := validateListenAddress(cfg.listenAddress); err != nil {
		return nil, err
	}
	for _, directory := range []*string{&cfg.server.ConnectorConfigDirectory, &cfg.server.StateDirectory} {
		absolute, err := filepath.Abs(strings.TrimSpace(*directory))
		if err != nil {
			return nil, fmt.Errorf("resolve directory %q: %w", *directory, err)
		}
		*directory = absolute
	}
	return cfg, nil
}

// validateListenAddress refuses a non-loopback address: anyone who can reach the endpoint can act as the user.
func validateListenAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return fmt.Errorf("--listen must be host:port: %q", address)
	}
	if !IsLoopbackAddress(host) {
		return fmt.Errorf("--listen must be a loopback address, such as 127.0.0.1:8803, because MCP tool calls carry no credentials: %q", address)
	}
	return nil
}

func serve(ctx context.Context, cfg *commandConfig, stdout io.Writer, stderr io.Writer) error {
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	server, err := NewServer(&cfg.server, logger)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.listenAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.listenAddress, err)
	}
	mux := http.NewServeMux()
	mux.Handle(MountPath, server.Handler())
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	served := make(chan error, 1)
	go func() { served <- httpServer.Serve(listener) }()
	fmt.Fprintln(stdout, "Dex Connectors MCP server is ready")
	fmt.Fprintf(stdout, "URL:   http://%s%s\n", listener.Addr().String(), MountPath)
	fmt.Fprintf(stdout, "Tools: %s\n", FormatToolNames(server.ToolNames(ctx)))
	fmt.Fprintln(stdout, "Press Ctrl+C to stop.")
	select {
	case err := <-served:
		return fmt.Errorf("Connectors MCP server stopped: %w", err)
	case <-ctx.Done():
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("stop Connectors MCP server: %w", err)
	}
	if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("Connectors MCP server stopped: %w", err)
	}
	return nil
}

// FormatToolNames returns the names joined for a startup line, or a hint when there are none.
func FormatToolNames(names []string) string {
	if len(names) == 0 {
		return "none yet; authorize a connection on the Dex Web Connectors page"
	}
	return strings.Join(names, ", ")
}

// SplitAllowedHosts turns a comma-separated --allowed-hosts value into Config.AllowedHosts entries.
func SplitAllowedHosts(value string) []string {
	var hosts []string
	for _, host := range strings.Split(value, ",") {
		if host = strings.TrimSpace(host); host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// ConnectionSelectorsFlag collects repeated --connection values.
type ConnectionSelectorsFlag struct {
	Selectors []ConnectionSelector
}

func (value *ConnectionSelectorsFlag) String() string {
	return ""
}

func (value *ConnectionSelectorsFlag) Set(raw string) error {
	selector, err := ParseConnectionSelector(raw)
	if err != nil {
		return err
	}
	value.Selectors = append(value.Selectors, selector)
	return nil
}
