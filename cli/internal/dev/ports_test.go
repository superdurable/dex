// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package dev

import (
	"bytes"
	"net"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestReserveOwnedListenersSkipsOccupiedPorts(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := occupied.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	occupiedPort := occupied.Addr().(*net.TCPAddr).Port

	cfg := testConfig(t)
	cfg.DexPort = occupiedPort
	listeners, err := reserveOwnedListeners(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listeners.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if cfg.DexPort == occupiedPort {
		t.Fatalf("allocated occupied Dex port %d", occupiedPort)
	}
	if cfg.TemporalDBFilename != cfg.autoTemporalDBFilename() {
		t.Fatalf("unexpected Temporal database: %q", cfg.TemporalDBFilename)
	}
}

func TestReserveOwnedListenersSkipsWildcardOccupiedPort(t *testing.T) {
	occupied, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := occupied.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	occupiedPort := occupied.Addr().(*net.TCPAddr).Port

	cfg := testConfig(t)
	cfg.TemporalPort = occupiedPort
	listeners, err := reserveOwnedListeners(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listeners.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if cfg.TemporalPort == occupiedPort {
		t.Fatalf("allocated wildcard-occupied Temporal port %d", occupiedPort)
	}
	if cfg.TemporalDBFilename != filepath.Join(cfg.StateDirectory, "dev", strconv.Itoa(cfg.TemporalPort), localSQLiteFileName) {
		t.Fatalf("unexpected Temporal database: %q", cfg.TemporalDBFilename)
	}
}

func TestReserveOwnedListenersExplicitPortFailsWhenOccupied(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := occupied.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	occupiedPort := occupied.Addr().(*net.TCPAddr).Port

	cfg := testConfig(t)
	cfg.DexPort = occupiedPort
	cfg.explicitLocalFlags["dex-port"] = true
	if _, err := reserveOwnedListeners(cfg); err == nil {
		t.Fatal("expected explicit occupied Dex port to fail")
	}
}

func TestReserveOwnedListenersKeepsExplicitDatabase(t *testing.T) {
	database := filepath.Join(t.TempDir(), "custom.db")
	cfg, err := parseConfig([]string{"--temporal-db-filename", database}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateDirectory = t.TempDir()
	listeners, err := reserveOwnedListeners(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listeners.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if cfg.TemporalDBFilename != database {
		t.Fatalf("explicit Temporal database was replaced: %q", cfg.TemporalDBFilename)
	}
}

func TestReserveOwnedListenersExternalModeSkipsTemporalDatabase(t *testing.T) {
	cfg, err := parseConfig([]string{"--temporal-address", "127.0.0.1:7233"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateDirectory = t.TempDir()
	listeners, err := reserveOwnedListeners(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listeners.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if cfg.TemporalDBFilename != "" {
		t.Fatalf("external mode assigned a Temporal database: %q", cfg.TemporalDBFilename)
	}
}

func TestConcurrentReserveOwnedListenersUsesDistinctPortsAndDatabases(t *testing.T) {
	configs := []*Config{testConfig(t), testConfig(t), testConfig(t)}
	listeners := make([]*ownedListeners, len(configs))
	errors := make([]error, len(configs))
	var wg sync.WaitGroup
	for i, cfg := range configs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			listeners[i], errors[i] = reserveOwnedListeners(cfg)
		}()
	}
	wg.Wait()
	t.Cleanup(func() {
		for _, item := range listeners {
			if item == nil {
				continue
			}
			if err := item.Close(); err != nil {
				t.Errorf("close listeners: %v", err)
			}
		}
	})
	ports := make(map[string]int)
	databases := make(map[string]int)
	for i, cfg := range configs {
		if errors[i] != nil {
			t.Fatalf("instance %d: %v", i, errors[i])
		}
		for name, address := range cfg.ownedAddresses() {
			if owner, exists := ports[address]; exists {
				t.Fatalf("%s address %s reused by instances %d and %d", name, address, owner, i)
			}
			ports[address] = i
		}
		if cfg.TemporalDBFilename == "" {
			t.Fatalf("instance %d missing Temporal database", i)
		}
		if owner, exists := databases[cfg.TemporalDBFilename]; exists {
			t.Fatalf("Temporal database %s reused by instances %d and %d", cfg.TemporalDBFilename, owner, i)
		}
		databases[cfg.TemporalDBFilename] = i
	}
}
