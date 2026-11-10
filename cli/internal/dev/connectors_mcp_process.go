// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// connectorsMCPInstanceLockedExitCode is the serve exit code when another server holds the state directory.
	connectorsMCPInstanceLockedExitCode = 3
	connectorsMCPMaxAnnouncementBytes   = 1 << 20
)

// errConnectorsMCPInstanceLocked reports that another server holds the instance lock of the state directory.
var errConnectorsMCPInstanceLocked = errors.New("another Connectors MCP server holds the state directory lock")

// connectorsMCPAnnouncement is the server's ready or error line, and the content of server.json.
type connectorsMCPAnnouncement struct {
	Type            string `json:"type"`
	Message         string `json:"message,omitempty"`
	URL             string `json:"url"`
	HealthURL       string `json:"healthUrl"`
	Version         string `json:"version"`
	LaunchContract  int    `json:"launchContract"`
	InstanceID      string `json:"instanceId"`
	PID             int    `json:"pid"`
	StateDir        string `json:"stateDir"`
	ConnectionsFile string `json:"connectionsFile"`
	// ConnectionsConfigDigest identifies the connection selection; JSON null means the server has no selection.
	ConnectionsConfigDigest connectorsMCPDigestField `json:"connectionsConfigDigest"`
	Tools                   []string                 `json:"tools"`
	Warnings                []json.RawMessage        `json:"warnings"`
}

// validateEndpoint checks the untrusted URLs and launch contract of a ready announcement.
func (announcement *connectorsMCPAnnouncement) validateEndpoint() error {
	if !isSupportedConnectorsMCPLaunchContract(announcement.LaunchContract) {
		return fmt.Errorf("launch contract %d is not supported by this dexcli", announcement.LaunchContract)
	}
	if announcement.InstanceID == "" {
		return errors.New("instanceId is empty")
	}
	return announcement.validateAddresses()
}

// validateAddresses requires url and healthUrl to be /mcp and /healthz on one loopback http address.
func (announcement *connectorsMCPAnnouncement) validateAddresses() error {
	mcpURL, err := parseLoopbackHTTPURL(announcement.URL)
	if err != nil {
		return fmt.Errorf("url: %w", err)
	}
	healthURL, err := parseLoopbackHTTPURL(announcement.HealthURL)
	if err != nil {
		return fmt.Errorf("healthUrl: %w", err)
	}
	if mcpURL.Host != healthURL.Host {
		return fmt.Errorf("url %s and healthUrl %s name different addresses", announcement.URL, announcement.HealthURL)
	}
	if mcpURL.Path != connectorsMCPMountPath || healthURL.Path != "/healthz" ||
		mcpURL.RawQuery != "" || healthURL.RawQuery != "" || mcpURL.Fragment != "" || healthURL.Fragment != "" {
		return fmt.Errorf("url %s and healthUrl %s must end in /mcp and /healthz", announcement.URL, announcement.HealthURL)
	}
	return nil
}

// parseLoopbackHTTPURL returns rawURL when it is an http URL whose host is a loopback IP address or localhost.
func parseLoopbackHTTPURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", rawURL, err)
	}
	if parsed.Scheme != "http" || parsed.User != nil || !isLoopbackRequestHost(parsed.Hostname()) {
		return nil, fmt.Errorf("%q is not an http URL on a loopback address", rawURL)
	}
	return parsed, nil
}

// connectorsMCPProcess is a dex-connectors-mcp serve child that exits when dexcli closes its stdin.
type connectorsMCPProcess struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	done    chan struct{}
	mu      sync.Mutex
	waitErr error
}

// startConnectorsMCPProcess starts serve for cfg and waits for its ready line. It returns
// errConnectorsMCPInstanceLocked when the state directory is locked, and another error when the server fails, exits,
// prints an invalid line, or misses cfg.ReadyTimeout.
func startConnectorsMCPProcess(
	ctx context.Context,
	cfg *ConnectorsMCPConfig,
	binaryPath string,
	logs io.Writer,
) (*connectorsMCPProcess, connectorsMCPAnnouncement, error) {
	if err := os.MkdirAll(cfg.StateDirectory, 0o700); err != nil {
		return nil, connectorsMCPAnnouncement{}, fmt.Errorf("create connectors MCP state directory: %w", err)
	}
	command := exec.Command(binaryPath, connectorsMCPServeArguments(cfg)...)
	command.Env = connectorsMCPChildEnvironment(os.Environ())
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, connectorsMCPAnnouncement{}, fmt.Errorf("create connectors MCP stdin: %w", err)
	}
	announcementLines := &firstLineWriter{lines: make(chan []byte, 1), rest: logs}
	command.Stdout = announcementLines
	command.Stderr = logs
	if err := command.Start(); err != nil {
		return nil, connectorsMCPAnnouncement{}, errors.Join(fmt.Errorf("start %s: %w", binaryPath, err), stdin.Close())
	}
	process := &connectorsMCPProcess{command: command, stdin: stdin, done: make(chan struct{})}
	go process.wait()
	announcement, err := process.awaitReady(ctx, announcementLines.lines, cfg.ReadyTimeout)
	if err != nil {
		return nil, connectorsMCPAnnouncement{}, errors.Join(err, process.stop(cfg.ShutdownTimeout))
	}
	return process, announcement, nil
}

// connectorsMCPServeArguments returns the serve command line for cfg, following the server's launch contract 1.
func connectorsMCPServeArguments(cfg *ConnectorsMCPConfig) []string {
	arguments := []string{
		"serve",
		"--listen", "127.0.0.1:0",
		"--connections-file", cfg.ConnectionsFile,
		"--state-dir", cfg.StateDirectory,
		"--output", "json",
		"--exit-on-stdin-close",
	}
	for _, connection := range cfg.Connections {
		arguments = append(arguments, "--connection", connection)
	}
	return arguments
}

// connectorsMCPChildEnvironment drops the variables that would conflict with --connections-file.
func connectorsMCPChildEnvironment(environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if name == "DEX_CONNECTOR_CONFIG_FILE" || strings.HasPrefix(name, "DEX_PROJECT_") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func (p *connectorsMCPProcess) wait() {
	err := p.command.Wait()
	p.mu.Lock()
	p.waitErr = err
	p.mu.Unlock()
	close(p.done)
}

func (p *connectorsMCPProcess) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

// awaitReady returns the validated ready announcement, or an error when the process exits, reports an error, or
// times out first.
func (p *connectorsMCPProcess) awaitReady(
	ctx context.Context,
	lines <-chan []byte,
	timeout time.Duration,
) (connectorsMCPAnnouncement, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case line := <-lines:
		return parseConnectorsMCPReadyLine(line)
	case <-p.done:
		// Wait drains stdout before done closes, so a line printed before exit is already buffered.
		select {
		case line := <-lines:
			if _, err := parseConnectorsMCPReadyLine(line); err != nil {
				return connectorsMCPAnnouncement{}, err
			}
			return connectorsMCPAnnouncement{}, errors.New("Connectors MCP server exited right after its ready line")
		default:
		}
		var exitError *exec.ExitError
		if errors.As(p.Err(), &exitError) && exitError.ExitCode() == connectorsMCPInstanceLockedExitCode {
			return connectorsMCPAnnouncement{}, errConnectorsMCPInstanceLocked
		}
		return connectorsMCPAnnouncement{}, fmt.Errorf("Connectors MCP server exited before readiness: %v", p.Err())
	case <-timer.C:
		return connectorsMCPAnnouncement{}, fmt.Errorf("Connectors MCP server printed no ready line within %s", timeout)
	case <-ctx.Done():
		return connectorsMCPAnnouncement{}, ctx.Err()
	}
}

// parseConnectorsMCPReadyLine returns a validated ready announcement, or an error for an error line or invalid line.
func parseConnectorsMCPReadyLine(line []byte) (connectorsMCPAnnouncement, error) {
	var announcement connectorsMCPAnnouncement
	if err := json.Unmarshal(bytes.TrimSpace(line), &announcement); err != nil {
		return connectorsMCPAnnouncement{}, fmt.Errorf("Connectors MCP server printed an invalid ready line: %w", err)
	}
	switch announcement.Type {
	case "ready":
		if err := announcement.validateEndpoint(); err != nil {
			return connectorsMCPAnnouncement{}, fmt.Errorf("Connectors MCP server ready line: %w", err)
		}
		return announcement, nil
	case "error":
		return connectorsMCPAnnouncement{}, fmt.Errorf("Connectors MCP server failed to start: %s", announcement.Message)
	default:
		return connectorsMCPAnnouncement{}, fmt.Errorf("Connectors MCP server printed an unknown line type %q", announcement.Type)
	}
}

// stop closes stdin, which the server treats as a shutdown request, sends SIGTERM, and kills the server after
// timeout.
func (p *connectorsMCPProcess) stop(timeout time.Duration) error {
	select {
	case <-p.done:
		return nil
	default:
	}
	stdinErr := p.stdin.Close()
	if errors.Is(stdinErr, os.ErrClosed) {
		stdinErr = nil
	}
	if err := p.command.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		stdinErr = errors.Join(stdinErr, fmt.Errorf("signal Connectors MCP server: %w", err))
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-p.done:
		return stdinErr
	case <-timer.C:
		if err := p.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return errors.Join(stdinErr, fmt.Errorf("kill Connectors MCP server: %w", err))
		}
		<-p.done
		return stdinErr
	}
}

// firstLineWriter delivers the first stdout line on lines and forwards later output to rest.
type firstLineWriter struct {
	mu        sync.Mutex
	buffer    []byte
	isLineSet bool
	lines     chan []byte
	rest      io.Writer
}

func (w *firstLineWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.isLineSet {
		return w.rest.Write(data)
	}
	w.buffer = append(w.buffer, data...)
	lineEnd := bytes.IndexByte(w.buffer, '\n')
	if lineEnd < 0 && len(w.buffer) <= connectorsMCPMaxAnnouncementBytes {
		return len(data), nil
	}
	if lineEnd < 0 {
		lineEnd = len(w.buffer)
	}
	w.isLineSet = true
	w.lines <- w.buffer[:lineEnd]
	if remainder := w.buffer[min(lineEnd+1, len(w.buffer)):]; len(remainder) > 0 {
		if _, err := w.rest.Write(remainder); err != nil {
			return 0, err
		}
	}
	w.buffer = nil
	return len(data), nil
}

// isLoopbackRequestHost reports whether host, without a port, is localhost, a *.localhost name, or a loopback IP.
func isLoopbackRequestHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
