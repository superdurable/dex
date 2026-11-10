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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const connectorsMCPHint = "dexcli dev keeps running without /mcp. Use --connectors-mcp-binary to run a local " +
	"dex-connectors-mcp, or --connectors-mcp=false to turn this off."

// connectorsMCPUnavailableError reports a failure that retrying cannot fix, such as no release for this platform.
type connectorsMCPUnavailableError struct {
	reason string
}

func (err *connectorsMCPUnavailableError) Error() string {
	return err.reason
}

// connectorsMCPServer is one acquired server: started by this dexcli, or reused from the state directory.
type connectorsMCPServer struct {
	announcement connectorsMCPAnnouncement
	// process is nil for a reused server.
	process *connectorsMCPProcess
}

// connectorsMCPManager keeps a Connectors MCP server available behind the /mcp proxy. It reuses a compatible
// running server or starts one, and restarts it with backoff when it stops.
type connectorsMCPManager struct {
	cfg         *ConnectorsMCPConfig
	resolver    *connectorsMCPReleaseResolver
	localClient *http.Client
	proxy       *connectorsMCPProxy
	logs        io.Writer
	// proxyURL is the stable /mcp URL on the Dex Web port.
	proxyURL string
	cancel   context.CancelFunc
	done     chan struct{}
	// binary is resolved once, on the first start.
	binary connectorsMCPResolvedBinary
	// stopErr is the error of stopping a started server; run sets it before done closes.
	stopErr error
}

func newConnectorsMCPManager(
	cfg *ConnectorsMCPConfig,
	resolver *connectorsMCPReleaseResolver,
	localClient *http.Client,
	proxy *connectorsMCPProxy,
	logs io.Writer,
	proxyURL string,
) *connectorsMCPManager {
	if cfg == nil || resolver == nil || localClient == nil || proxy == nil || logs == nil {
		panic("connectors MCP manager dependencies must not be nil")
	}
	return &connectorsMCPManager{
		cfg: cfg, resolver: resolver, localClient: localClient, proxy: proxy, logs: logs, proxyURL: proxyURL,
	}
}

// Start runs the manager in the background until Stop or ctx ends.
func (m *connectorsMCPManager) Start(ctx context.Context) {
	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.done = make(chan struct{})
	go func() {
		defer close(m.done)
		m.run(runCtx)
	}()
}

// Stop ends the manager and stops a server it started. It returns the server's shutdown error.
func (m *connectorsMCPManager) Stop() error {
	if m.cancel == nil {
		return nil
	}
	m.cancel()
	<-m.done
	return m.stopErr
}

func (m *connectorsMCPManager) run(ctx context.Context) {
	restartDelay := m.cfg.RestartBackoffBase
	consecutiveStartFailures := 0
	for {
		server, err := m.acquire(ctx)
		if ctx.Err() != nil {
			m.stopStartedServer(server)
			return
		}
		if err != nil {
			var unavailable *connectorsMCPUnavailableError
			consecutiveStartFailures++
			if errors.As(err, &unavailable) || consecutiveStartFailures >= m.cfg.MaxConsecutiveStartFailures {
				m.reportUnavailable(err)
				return
			}
			fmt.Fprintf(m.logs, "Connectors MCP: %v; retrying in %s\n", err, restartDelay)
			if !sleepContext(ctx, restartDelay) {
				return
			}
			restartDelay = min(2*restartDelay, m.cfg.RestartBackoffMax)
			continue
		}
		consecutiveStartFailures = 0
		m.proxy.setUpstream(mustParseLoopbackHTTPURL(server.announcement.URL))
		m.reportReady(server)
		startedAt := time.Now()
		stopReason := m.awaitServerStop(ctx, server)
		m.proxy.setUnavailable("the Connectors MCP server is restarting")
		if ctx.Err() != nil {
			m.stopStartedServer(server)
			return
		}
		if time.Since(startedAt) >= m.cfg.StableRunDuration {
			restartDelay = m.cfg.RestartBackoffBase
		}
		fmt.Fprintf(m.logs, "Connectors MCP: server stopped (%s); restarting in %s\n", stopReason, restartDelay)
		if !sleepContext(ctx, restartDelay) {
			return
		}
		restartDelay = min(2*restartDelay, m.cfg.RestartBackoffMax)
	}
}

// acquire returns a reusable running server or starts one. A *connectorsMCPUnavailableError means retrying cannot
// help; another error is a failed start.
func (m *connectorsMCPManager) acquire(ctx context.Context) (connectorsMCPServer, error) {
	if announcement, result, _ := discoverConnectorsMCPServer(ctx, m.cfg, m.localClient); result == connectorsMCPServerReusable {
		return connectorsMCPServer{announcement: announcement}, nil
	}
	if m.binary.path == "" {
		binary, err := m.resolver.resolveBinary(ctx)
		if err != nil {
			return connectorsMCPServer{}, &connectorsMCPUnavailableError{reason: err.Error()}
		}
		m.binary = binary
	}
	process, announcement, err := startConnectorsMCPProcess(ctx, m.cfg, m.binary.path, m.logs)
	if errors.Is(err, errConnectorsMCPInstanceLocked) {
		announcement, result, reason := discoverConnectorsMCPServer(ctx, m.cfg, m.localClient)
		switch result {
		case connectorsMCPServerReusable:
			return connectorsMCPServer{announcement: announcement}, nil
		case connectorsMCPServerIncompatible:
			return connectorsMCPServer{}, &connectorsMCPUnavailableError{reason: fmt.Sprintf(
				"another Connectors MCP server holds %s: %s; stop it first", m.cfg.StateDirectory, reason)}
		default:
			return connectorsMCPServer{}, fmt.Errorf("%w: %s", err, reason)
		}
	}
	if err != nil {
		return connectorsMCPServer{}, err
	}
	return connectorsMCPServer{announcement: announcement, process: process}, nil
}

// awaitServerStop returns why server stopped: its process exited, or a reused server failed a health check. It
// returns "" when ctx ends first.
func (m *connectorsMCPManager) awaitServerStop(ctx context.Context, server connectorsMCPServer) string {
	if server.process != nil {
		select {
		case <-ctx.Done():
			return ""
		case <-server.process.done:
			return fmt.Sprintf("exited: %v", server.process.Err())
		}
	}
	ticker := time.NewTicker(m.cfg.HealthCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ""
		case <-ticker.C:
		}
		health, err := checkConnectorsMCPHealth(ctx, m.localClient, server.announcement.HealthURL)
		if ctx.Err() != nil {
			return ""
		}
		if err != nil {
			return err.Error()
		}
		if health.InstanceID != server.announcement.InstanceID {
			return "another instance answers at " + server.announcement.HealthURL
		}
	}
}

// stopStartedServer stops server when this dexcli started it and records a failure for Stop.
func (m *connectorsMCPManager) stopStartedServer(server connectorsMCPServer) {
	if server.process == nil {
		return
	}
	if err := server.process.stop(m.cfg.ShutdownTimeout); err != nil {
		m.stopErr = fmt.Errorf("stop Connectors MCP server: %w", err)
	}
}

func (m *connectorsMCPManager) reportReady(server connectorsMCPServer) {
	origin := "started"
	if server.process == nil {
		origin = "reusing running"
	}
	version := server.announcement.Version
	if version == "" {
		version = "(unknown version)"
	}
	fmt.Fprintf(m.logs, "Connectors MCP: %s server %s with %d tools at %s, proxied at %s\n",
		origin, version, len(server.announcement.Tools), server.announcement.URL, m.proxyURL)
	for _, warning := range server.announcement.Warnings {
		fmt.Fprintf(m.logs, "Connectors MCP: warning: %s\n", strings.TrimSpace(string(warning)))
	}
}

func (m *connectorsMCPManager) reportUnavailable(err error) {
	reason := err.Error()
	m.proxy.setUnavailable("the Connectors MCP server is unavailable: " + reason)
	fmt.Fprintf(m.logs, "Connectors MCP: unavailable: %s. %s\n", reason, connectorsMCPHint)
}

// mustParseLoopbackHTTPURL parses a URL that validateEndpoint already accepted.
func mustParseLoopbackHTTPURL(rawURL string) *url.URL {
	parsed, err := parseLoopbackHTTPURL(rawURL)
	if err != nil {
		panic(fmt.Sprintf("validated Connectors MCP URL %q no longer parses: %v", rawURL, err))
	}
	return parsed
}

// sleepContext waits for delay and reports false when ctx ends first.
func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
