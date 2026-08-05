// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

//go:build integration

package dev

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLocalStackStartsAndReleasesPorts(t *testing.T) {
	ports := freePorts(t, 4)
	cfg := testConfig(t)
	blobStoreDirectory := filepath.Join(t.TempDir(), "blobs")
	cfg.BlobStoreDirectory = blobStoreDirectory
	cfg.TemporalPort = ports[0]
	cfg.TemporalUIPort = ports[1]
	cfg.DexPort = ports[2]
	cfg.WebPort = ports[3]
	output := &synchronizedBuffer{}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runFinished := make(chan error, 1)
	go func() {
		runFinished <- newSupervisor(cfg, output, output).Run(runCtx)
	}()

	webURL := "http://127.0.0.1:" + strconv.Itoa(cfg.WebPort)
	waitForHealthyWeb(t, webURL+"/healthz", runFinished)
	searchResponse, err := http.Post(
		webURL+"/api/flows/search",
		"application/json",
		strings.NewReader(`{"pageSize":10}`),
	)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(searchResponse.Body)
	closeErr := searchResponse.Body.Close()
	if readErr != nil || closeErr != nil {
		cancel()
		t.Fatal(fmt.Errorf("read search response: %w", errors.Join(readErr, closeErr)))
	}
	if searchResponse.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"flows":[]`)) {
		cancel()
		t.Fatalf("search response: status=%d body=%s", searchResponse.StatusCode, body)
	}

	cancel()
	select {
	case err := <-runFinished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("local stack did not stop")
	}
	if !strings.Contains(output.String(), "Dex development environment is ready") {
		t.Fatalf("missing readiness output: %s", output.String())
	}
	if _, err := os.Stat(blobStoreDirectory); err != nil {
		t.Fatalf("blob store was not retained after shutdown: %v", err)
	}
	for _, port := range ports {
		listener, err := net.Listen("tcp", net.JoinHostPort(cfg.BindAddress, strconv.Itoa(port)))
		if err != nil {
			t.Fatalf("port %d was not released: %v", port, err)
		}
		if err := listener.Close(); err != nil {
			t.Fatalf("close port probe %d: %v", port, err)
		}
	}
}

func TestExternalTemporalRemainsRunning(t *testing.T) {
	ports := freePorts(t, 4)
	localConfig := testConfig(t)
	localConfig.TemporalPort = ports[0]
	localConfig.TemporalUIPort = ports[1]
	output := &synchronizedBuffer{}
	temporal, err := startTemporalProcess(localConfig, output, output)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := temporal.Stop(localConfig.ShutdownTimeout); err != nil {
			t.Errorf("stop Temporal: %v", err)
		}
	})
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), localConfig.StartupTimeout)
	temporalClient, err := waitForTemporal(startupCtx, localConfig, temporal)
	if err != nil {
		cancelStartup()
		t.Fatal(err)
	}
	if err := validateSearchAttributes(startupCtx, temporalClient, localConfig.TemporalNamespace); err != nil {
		temporalClient.Close()
		cancelStartup()
		t.Fatal(err)
	}
	temporalClient.Close()
	cancelStartup()

	externalConfig := testConfig(t)
	externalConfig.BlobStoreDirectory = filepath.Join(t.TempDir(), "blobs")
	externalConfig.TemporalAddress = localConfig.temporalAddress()
	externalConfig.DexPort = ports[2]
	externalConfig.WebPort = ports[3]
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	runFinished := make(chan error, 1)
	go func() {
		runFinished <- newSupervisor(externalConfig, output, output).Run(runCtx)
	}()
	waitForHealthyWeb(
		t,
		"http://127.0.0.1:"+strconv.Itoa(externalConfig.WebPort)+"/healthz",
		runFinished,
	)
	cancelRun()
	select {
	case err := <-runFinished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("external-mode Dex stack did not stop")
	}

	healthCtx, cancelHealth := context.WithTimeout(context.Background(), 5*time.Second)
	temporalClient, err = waitForTemporal(healthCtx, localConfig, temporal)
	if err != nil {
		cancelHealth()
		t.Fatalf("external Temporal was stopped: %v", err)
	}
	temporalClient.Close()
	cancelHealth()
}

func TestBlobStoreDirectorySelection(t *testing.T) {
	root := t.TempDir()
	output := &synchronizedBuffer{}

	explicitDirectory := filepath.Join(root, "explicit")
	explicitConfig, err := parseConfig([]string{
		"--blob-store-dir", explicitDirectory,
		"--temporal-db-filename", filepath.Join(root, "ignored-temporal.db"),
	}, output)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := newSupervisor(explicitConfig, output, output).prepareBlobStoreDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if directory != explicitDirectory {
		t.Fatalf("unexpected explicit directory: %q", directory)
	}

	databaseConfig := testConfig(t)
	databaseConfig.TemporalDBFilename = filepath.Join(root, "temporal.db")
	directory, err = newSupervisor(databaseConfig, output, output).prepareBlobStoreDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if directory != databaseConfig.TemporalDBFilename+".dex-blobs" {
		t.Fatalf("unexpected database directory: %q", directory)
	}

	defaultStoreConfig, err := parseConfig(nil, output)
	if err != nil {
		t.Fatal(err)
	}
	expectedDefault, err := defaultBlobStoreDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if defaultStoreConfig.BlobStoreDirectory != expectedDefault {
		t.Fatalf("unexpected default directory: %q", defaultStoreConfig.BlobStoreDirectory)
	}
	defaultStoreConfig.BlobStoreDirectory = filepath.Join(root, "default")
	directory, err = newSupervisor(defaultStoreConfig, output, output).prepareBlobStoreDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if directory != defaultStoreConfig.BlobStoreDirectory {
		t.Fatalf("unexpected prepared default directory: %q", directory)
	}
}

func testConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := defaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func waitForHealthyWeb(t *testing.T, url string, runFinished <-chan error) {
	t.Helper()
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		requestCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr == nil {
			closeErr := response.Body.Close()
			cancel()
			if closeErr != nil {
				t.Fatal(closeErr)
			}
			if response.StatusCode == http.StatusOK {
				return
			}
		} else {
			cancel()
		}
		select {
		case err := <-runFinished:
			t.Fatalf("local stack exited before readiness: %v", err)
		case <-deadline.C:
			t.Fatal("local stack did not become ready")
		case <-ticker.C:
		}
	}
}

func freePorts(t *testing.T, count int) []int {
	t.Helper()
	listeners := make([]net.Listener, 0, count)
	ports := make([]int, 0, count)
	for range count {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
	}
	for _, listener := range listeners {
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return ports
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
