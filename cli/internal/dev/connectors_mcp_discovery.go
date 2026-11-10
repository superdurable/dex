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
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	connectorsMCPServerFileName       = "server.json"
	connectorsMCPLockFileName         = "server.lock"
	connectorsMCPHealthRequestTimeout = 2 * time.Second
	connectorsMCPMaxHealthBytes       = 64 << 10
)

// connectorsMCPDiscoveryResult says whether a running server in the state directory can be reused.
type connectorsMCPDiscoveryResult int

const (
	// connectorsMCPServerAbsent means no live, healthy server announced itself in the state directory.
	connectorsMCPServerAbsent connectorsMCPDiscoveryResult = iota
	// connectorsMCPServerReusable means a healthy server matches this dexcli's settings.
	connectorsMCPServerReusable
	// connectorsMCPServerIncompatible means a healthy server runs with other settings.
	connectorsMCPServerIncompatible
)

// connectorsMCPHealth is the /healthz response body.
type connectorsMCPHealth struct {
	Status         string `json:"status"`
	Version        string `json:"version"`
	LaunchContract int    `json:"launchContract"`
	InstanceID     string `json:"instanceId"`
	ToolCount      int    `json:"toolCount"`
}

// discoverConnectorsMCPServer reads server.json in cfg.StateDirectory and decides whether its server can be reused.
// server.json is only a claim: the server must also hold the instance lock, and its /healthz on the same address
// as url must confirm the instance, launch contract, and version. It returns the announcement, the discovery result,
// and, unless the server is reusable, the reason.
func discoverConnectorsMCPServer(
	ctx context.Context,
	cfg *ConnectorsMCPConfig,
	localClient *http.Client,
) (connectorsMCPAnnouncement, connectorsMCPDiscoveryResult, string) {
	content, err := os.ReadFile(filepath.Join(cfg.StateDirectory, connectorsMCPServerFileName))
	if err != nil {
		return connectorsMCPAnnouncement{}, connectorsMCPServerAbsent, fmt.Sprintf("read server.json: %v", err)
	}
	var announcement connectorsMCPAnnouncement
	if err := json.Unmarshal(content, &announcement); err != nil {
		return connectorsMCPAnnouncement{}, connectorsMCPServerAbsent, fmt.Sprintf("decode server.json: %v", err)
	}
	if err := announcement.validateAddresses(); err != nil {
		return announcement, connectorsMCPServerAbsent, fmt.Sprintf("server.json: %v", err)
	}
	if !filepath.IsAbs(announcement.StateDir) || filepath.Clean(announcement.StateDir) != filepath.Clean(cfg.StateDirectory) {
		return announcement, connectorsMCPServerAbsent, fmt.Sprintf(
			"server.json names state directory %q, not %s", announcement.StateDir, cfg.StateDirectory)
	}
	if announcement.PID <= 0 || !isProcessAlive(announcement.PID) {
		return announcement, connectorsMCPServerAbsent, fmt.Sprintf("process %d from server.json is not running", announcement.PID)
	}
	isLockHeld, err := isConnectorsMCPInstanceLockHeld(cfg.StateDirectory)
	if err != nil {
		return announcement, connectorsMCPServerAbsent, err.Error()
	}
	if !isLockHeld {
		return announcement, connectorsMCPServerAbsent, "no server holds the instance lock, so server.json is stale"
	}
	health, err := checkConnectorsMCPHealth(ctx, localClient, announcement.HealthURL)
	if err != nil {
		return announcement, connectorsMCPServerAbsent, err.Error()
	}
	if health.InstanceID != announcement.InstanceID || health.LaunchContract != announcement.LaunchContract ||
		health.Version == "" || health.Version != announcement.Version {
		return announcement, connectorsMCPServerAbsent, "/healthz does not confirm the instance, launch contract, and version in server.json"
	}
	if err := announcement.validateEndpoint(); err != nil {
		return announcement, connectorsMCPServerIncompatible, fmt.Sprintf("server at %s: %v", announcement.URL, err)
	}
	if !filepath.IsAbs(announcement.ConnectionsFile) ||
		filepath.Clean(announcement.ConnectionsFile) != filepath.Clean(cfg.ConnectionsFile) {
		return announcement, connectorsMCPServerIncompatible, fmt.Sprintf(
			"server at %s reads %s, not %s", announcement.URL, announcement.ConnectionsFile, cfg.ConnectionsFile)
	}
	wantedDigest, err := connectorsMCPConnectionsConfigDigest(cfg.Connections)
	if err != nil {
		return announcement, connectorsMCPServerIncompatible, err.Error()
	}
	if !announcement.ConnectionsConfigDigest.isPresent {
		return announcement, connectorsMCPServerIncompatible, fmt.Sprintf(
			"server at %s does not record connectionsConfigDigest, so its connection selection is unknown", announcement.URL)
	}
	if !isSameConnectionsConfigDigest(announcement.ConnectionsConfigDigest.value, wantedDigest) {
		return announcement, connectorsMCPServerIncompatible, fmt.Sprintf(
			"server at %s offers another connection selection than --connectors-mcp-connection", announcement.URL)
	}
	return announcement, connectorsMCPServerReusable, ""
}

// isConnectorsMCPInstanceLockHeld reports whether another open file holds server.lock in stateDirectory. It tries
// a non-blocking lock and releases it at once when it succeeds.
func isConnectorsMCPInstanceLockHeld(stateDirectory string) (bool, error) {
	lockFile, err := os.Open(filepath.Join(stateDirectory, connectorsMCPLockFileName))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open instance lock: %w", err)
	}
	lockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(lockErr, syscall.EWOULDBLOCK) {
		return true, lockFile.Close()
	}
	if lockErr != nil {
		return false, errors.Join(fmt.Errorf("try instance lock: %w", lockErr), lockFile.Close())
	}
	// Closing the file releases the lock this probe took.
	return false, lockFile.Close()
}

// checkConnectorsMCPHealth returns the /healthz body at healthURL, or an error unless it is HTTP 200 with status ok.
func checkConnectorsMCPHealth(ctx context.Context, localClient *http.Client, healthURL string) (connectorsMCPHealth, error) {
	requestCtx, cancel := context.WithTimeout(ctx, connectorsMCPHealthRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		return connectorsMCPHealth{}, fmt.Errorf("create health request: %w", err)
	}
	response, err := localClient.Do(request)
	if err != nil {
		return connectorsMCPHealth{}, fmt.Errorf("check %s: %w", healthURL, err)
	}
	content, readErr := io.ReadAll(io.LimitReader(response.Body, connectorsMCPMaxHealthBytes))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return connectorsMCPHealth{}, fmt.Errorf("read %s: %w", healthURL, errors.Join(readErr, closeErr))
	}
	if response.StatusCode != http.StatusOK {
		return connectorsMCPHealth{}, fmt.Errorf("%s returned HTTP %d", healthURL, response.StatusCode)
	}
	var health connectorsMCPHealth
	if err := json.Unmarshal(content, &health); err != nil {
		return connectorsMCPHealth{}, fmt.Errorf("decode %s: %w", healthURL, err)
	}
	if health.Status != "ok" {
		return connectorsMCPHealth{}, fmt.Errorf("%s reports status %q", healthURL, health.Status)
	}
	return health, nil
}

// isProcessAlive reports whether pid names a running process, including one owned by another user.
func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
