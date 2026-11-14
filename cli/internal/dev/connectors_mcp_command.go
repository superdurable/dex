// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dev

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const connectorsMCPCommandHint = "dexcli connectors mcp exits. Use --connectors-mcp-binary to run a local " +
	"dex-connectors-mcp."

var connectorsMCPCommandFlagOrder = []string{
	"connector-config-dir",
	"connectors-mcp-binary",
	"connectors-mcp-connection",
	"connectors-mcp-version",
	"exit-on-stdin-close",
}

// ConnectorsMCPCommandConfig configures dexcli connectors mcp, which serves only the Connectors MCP server.
type ConnectorsMCPCommandConfig struct {
	// StateDirectory defaults to $HOME/.dex, the state directory of dexcli dev, so both commands share one server
	// per connections file.
	StateDirectory string
	// ConnectorConfigDirectory defaults to $HOME/.dex/connectors, whose connections.json Dex Web writes.
	ConnectorConfigDirectory string
	// ConnectorsMCP configures how the command finds or starts the server; IsEnabled is ignored.
	ConnectorsMCP ConnectorsMCPConfig
	// IsExitingOnStdinClose defaults false. When true, the command also stops when its standard input closes.
	IsExitingOnStdinClose bool
	// ShutdownTimeout defaults to 10 seconds and bounds the wait for in-flight proxied requests at exit.
	ShutdownTimeout time.Duration
}

// connectorsMCPCommandReadyLine is the one stdout line dexcli connectors mcp prints when its URL answers.
type connectorsMCPCommandReadyLine struct {
	Type            string `json:"type"`
	URL             string `json:"url"`
	PID             int    `json:"pid"`
	ConnectionsFile string `json:"connectionsFile"`
}

// connectorsMCPCommandErrorLine is the one stdout line dexcli connectors mcp prints when no server becomes available.
type connectorsMCPCommandErrorLine struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ExecuteConnectors runs dexcli connectors and its subcommands.
func ExecuteConnectors(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printConnectorsUsage(stdout)
		return nil
	}
	if args[0] != "mcp" {
		printConnectorsUsage(stderr)
		return fmt.Errorf("unknown connectors command %q", args[0])
	}
	output := &connectorsMCPCommandOutput{stdout: stdout}
	err := executeConnectorsMCP(ctx, args[1:], stdin, output, stderr)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return nil
	}
	// Every startup failure reaches the caller as the one error line, unless a line was already attempted.
	if writeErr := output.writeLine(connectorsMCPCommandErrorLine{Type: "error", Message: err.Error()}); writeErr != nil {
		return errors.Join(err, writeErr)
	}
	return err
}

// executeConnectorsMCP parses the flags and runs dexcli connectors mcp. It returns flag.ErrHelp after printing help.
func executeConnectorsMCP(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	output *connectorsMCPCommandOutput,
	stderr io.Writer,
) error {
	cfg, err := parseConnectorsMCPCommandConfig(args, stderr)
	if err != nil {
		return err
	}
	var stdinClosed <-chan struct{}
	if cfg.IsExitingOnStdinClose {
		stdinClosed = observeStdinClose(stdin)
	}
	return runConnectorsMCPCommand(ctx, cfg, stdinClosed, output, stderr)
}

func printConnectorsUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: dexcli connectors <command>")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Commands:")
	fmt.Fprintln(output, "  mcp       Serve the Dex Connectors MCP server alone, without Dex Server or Dex Web")
}

func parseConnectorsMCPCommandConfig(args []string, output io.Writer) (*ConnectorsMCPCommandConfig, error) {
	stateDirectory, err := defaultStateDirectory()
	if err != nil {
		return nil, err
	}
	cfg := &ConnectorsMCPCommandConfig{
		StateDirectory:           stateDirectory,
		ConnectorConfigDirectory: filepath.Join(stateDirectory, "connectors"),
		ConnectorsMCP:            defaultConnectorsMCPConfig(),
		ShutdownTimeout:          10 * time.Second,
	}
	flags := flag.NewFlagSet("dexcli connectors mcp", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(
		&cfg.ConnectorConfigDirectory,
		"connector-config-dir",
		cfg.ConnectorConfigDirectory,
		"directory containing local Connector configuration",
	)
	connections := connectorsMCPConnectionFlag{}
	addConnectorsMCPServerFlags(flags, &cfg.ConnectorsMCP, &connections)
	flags.BoolVar(&cfg.IsExitingOnStdinClose, "exit-on-stdin-close", false, "also stop when standard input closes")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: dexcli connectors mcp [flags]")
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Prints one JSON line on stdout, then serves until SIGINT or SIGTERM:")
		fmt.Fprintln(output, `  {"type":"ready","url":"http://127.0.0.1:<port>/mcp","pid":<dexcli pid>,"connectionsFile":"<path>"}`)
		fmt.Fprintln(output, `  or {"type":"error","message":"<reason>"} followed by exit code 1.`)
		printFlags(output, flags, connectorsMCPCommandFlagOrder)
	}
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	cfg.ConnectorsMCP.Connections = connections.values
	cfg.ConnectorConfigDirectory = strings.TrimSpace(cfg.ConnectorConfigDirectory)
	if cfg.ConnectorConfigDirectory == "" {
		return nil, fmt.Errorf("connector config directory is required")
	}
	if err := cfg.ConnectorsMCP.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// runConnectorsMCPCommand serves a loopback /mcp proxy and prints its ready line. It stops only servers it started.
func runConnectorsMCPCommand(
	ctx context.Context,
	cfg *ConnectorsMCPCommandConfig,
	stdinClosed <-chan struct{},
	output *connectorsMCPCommandOutput,
	stderr io.Writer,
) (runErr error) {
	if err := cfg.ConnectorsMCP.resolvePaths(cfg.StateDirectory, cfg.ConnectorConfigDirectory); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for the Connectors MCP proxy: %w", err)
	}
	proxyURL := "http://" + listener.Addr().String() + connectorsMCPMountPath
	proxy := newConnectorsMCPProxy()
	mux := http.NewServeMux()
	mux.Handle(connectorsMCPMountPath, proxy)
	requestBaseContext, cancelRequestContexts := context.WithCancel(context.Background())
	proxyServer := &http.Server{
		Handler:           mux,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return requestBaseContext },
	}
	serveDone := make(chan struct{})
	var serveErr error
	go func() {
		serveErr = proxyServer.Serve(listener)
		close(serveDone)
	}()
	defer func() {
		// Cancelling request contexts first ends open event streams, so shutdown does not wait for them.
		cancelRequestContexts()
		runErr = errors.Join(runErr, shutdownConnectorsMCPProxy(proxyServer, serveDone, cfg.ShutdownTimeout))
	}()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// A nil stdinClosed never fires; this goroutine ends with runCtx when the function returns.
	go func() {
		select {
		case <-stdinClosed:
			cancel()
		case <-runCtx.Done():
		}
	}()
	manager := newConnectorsMCPManagerForThisPlatform(
		&cfg.ConnectorsMCP, proxy, stderr, proxyURL, connectorsMCPCommandHint)
	manager.Start(runCtx)
	defer func() { runErr = errors.Join(runErr, manager.Stop()) }()

	if err := manager.WaitForFirstOutcome(runCtx); err != nil {
		if runCtx.Err() != nil {
			return nil
		}
		return fmt.Errorf("Connectors MCP server is unavailable: %w", err)
	}
	ready := connectorsMCPCommandReadyLine{
		Type: "ready", URL: proxyURL, PID: os.Getpid(), ConnectionsFile: cfg.ConnectorsMCP.ConnectionsFile,
	}
	if err := output.writeLine(ready); err != nil {
		return err
	}
	select {
	case <-runCtx.Done():
		return nil
	case <-serveDone:
		return fmt.Errorf("Connectors MCP proxy stopped: %w", serveErr)
	}
}

// connectorsMCPCommandOutput writes the one stdout line of dexcli connectors mcp, at most once.
type connectorsMCPCommandOutput struct {
	stdout      io.Writer
	isAttempted bool
}

// writeLine writes line unless a line was already attempted, and returns the write error.
func (output *connectorsMCPCommandOutput) writeLine(line any) error {
	if output.isAttempted {
		return nil
	}
	output.isAttempted = true
	content, err := json.Marshal(line)
	if err != nil {
		panic("encode a Connectors MCP command line: " + err.Error())
	}
	if _, err := output.stdout.Write(append(content, '\n')); err != nil {
		return fmt.Errorf("write the Connectors MCP command line: %w", err)
	}
	return nil
}

// observeStdinClose returns a channel that closes when stdin reaches EOF or fails.
// Only the process entry calls it; the reader goroutine lives as long as the process.
func observeStdinClose(stdin io.Reader) <-chan struct{} {
	closed := make(chan struct{})
	go func() {
		// Any read error means the caller is gone, the same as EOF.
		_, _ = io.Copy(io.Discard, stdin)
		close(closed)
	}()
	return closed
}

// shutdownConnectorsMCPProxy stops the proxy server, waits for Serve to return, and returns the shutdown error.
func shutdownConnectorsMCPProxy(proxyServer *http.Server, serveDone <-chan struct{}, timeout time.Duration) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	shutdownErr := proxyServer.Shutdown(shutdownCtx)
	<-serveDone
	if shutdownErr != nil {
		return fmt.Errorf("stop the Connectors MCP proxy: %w", shutdownErr)
	}
	return nil
}
