// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dev

import (
	"net"
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
	cfg.WebPort = occupiedPort
	listeners, err := reserveOwnedListeners(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listeners.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if cfg.WebPort == occupiedPort {
		t.Fatalf("allocated wildcard-occupied Web port %d", occupiedPort)
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

func TestConcurrentReserveOwnedListenersUsesDistinctPortsAndDataDirectories(t *testing.T) {
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
	dataDirectories := make(map[string]int)
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
		directory := cfg.zdDataDirectory()
		if owner, exists := dataDirectories[directory]; exists {
			t.Fatalf("state directory %s reused by instances %d and %d", directory, owner, i)
		}
		dataDirectories[directory] = i
	}
}
