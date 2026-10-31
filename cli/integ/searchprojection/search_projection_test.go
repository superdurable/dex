// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build search_projection_e2e

package searchprojection

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSearchProjectionCLIWithLocalDevServer(t *testing.T) {
	serverBinary := os.Getenv("DEX_SERVER_ZD_BINARY")
	databaseURL := os.Getenv("DEX_ZD_POSTGRES_URL")
	if serverBinary == "" || databaseURL == "" {
		t.Fatal("DEX_SERVER_ZD_BINARY and DEX_ZD_POSTGRES_URL are required")
	}
	cliBinary, err := filepath.Abs("../../dexcli")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	configuration := filepath.Join(directory, "server.yaml")
	if err := os.WriteFile(configuration, []byte(fmt.Sprintf("cluster:\n  partitionCount: 16\nsearch:\n  backend: postgres\n  postgresConnectionString: %s\n", databaseURL)), 0600); err != nil {
		t.Fatal(err)
	}
	dexPort := freePort(t)
	webPort := freePort(t)
	for webPort == dexPort {
		webPort = freePort(t)
	}
	logs, err := os.Create(filepath.Join(directory, "dev.log"))
	if err != nil {
		t.Fatal(err)
	}
	process := exec.Command(cliBinary, "dev", "--open=false", "--zd-binary", serverBinary,
		"--zd-data-dir", filepath.Join(directory, "state"), "--zd-config", configuration,
		"--dex-port", strconv.Itoa(dexPort), "--web-port", strconv.Itoa(webPort))
	process.Stdout, process.Stderr = logs, logs
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- process.Wait() }()
	t.Cleanup(func() {
		if err := process.Process.Signal(os.Interrupt); err != nil {
			t.Error(err)
		}
		select {
		case err := <-stopped:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(15 * time.Second):
			if err := process.Process.Kill(); err != nil {
				t.Error(err)
			}
			t.Errorf("dev server did not stop: %v", <-stopped)
		}
		if err := logs.Close(); err != nil {
			t.Error(err)
		}
	})
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(dexPort))
	budget, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := exec.CommandContext(budget, cliBinary, "health", "--server", address, "--timeout", "1s").CombinedOutput()
		if err == nil {
			break
		}
		select {
		case <-budget.Done():
			t.Fatalf("dev server did not become healthy: %s: %v", result, err)
		case <-ticker.C:
		}
	}
	response := runCLI(t, cliBinary, address, "status")
	if !json.Valid(response) {
		t.Fatalf("invalid status: %s", response)
	}
	response = runCLI(t, cliBinary, address, "rebuild", "--all")
	var scheduled struct {
		Partitions []uint32 `json:"scheduled_partition_ids"`
	}
	if err := json.Unmarshal(response, &scheduled); err != nil {
		t.Fatal(err)
	}
	if len(scheduled.Partitions) != 16 {
		t.Fatalf("expected 16 partitions: %s", response)
	}
	for {
		response = runCLI(t, cliBinary, address, "status")
		var state struct {
			Partitions []struct {
				IsScheduled  bool `json:"is_scheduled"`
				IsInProgress bool `json:"is_in_progress"`
			} `json:"partitions"`
		}
		if err := json.Unmarshal(response, &state); err != nil {
			t.Fatal(err)
		}
		hasActiveRebuild := false
		for _, partition := range state.Partitions {
			hasActiveRebuild = hasActiveRebuild || partition.IsScheduled || partition.IsInProgress
		}
		if !hasActiveRebuild {
			break
		}
		select {
		case <-budget.Done():
			t.Fatalf("manual rebuilding did not finish: %s", response)
		case <-ticker.C:
		}
	}
	result, err := exec.Command(cliBinary, "search-projection", "rebuild", "--partitions", "16", "--server", address).CombinedOutput()
	if err == nil || !strings.Contains(string(result), "partition 16") {
		t.Fatalf("invalid partition was not rejected: %s %v", result, err)
	}
}

func runCLI(t *testing.T, binary, address string, arguments ...string) []byte {
	t.Helper()
	arguments = append([]string{"search-projection"}, arguments...)
	arguments = append(arguments, "--server", address)
	result, err := exec.Command(binary, arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s: %v", arguments, result, err)
	}
	return result
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
