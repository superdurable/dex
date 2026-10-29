// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

//go:build integration

package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/superdurable/dex/cli/internal/command"
)

func TestLocalStackStartsAndReleasesPorts(t *testing.T) {
	ports := freePorts(t, 2)
	cfg := testConfig(t)
	cfg.DexPort = ports[0]
	cfg.WebPort = ports[1]
	cfg.explicitLocalFlags["dex-port"] = true
	cfg.explicitLocalFlags["web-port"] = true
	cfg.LogDirectory = t.TempDir()
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
	cliOutput := &bytes.Buffer{}
	cliErrors := &bytes.Buffer{}
	cliApp := command.NewApp(bytes.NewReader(nil), cliOutput, cliErrors)
	dexAddress := net.JoinHostPort(cfg.BindAddress, strconv.Itoa(cfg.DexPort))
	if err := cliApp.Execute(
		context.Background(),
		[]string{"health", "--server", dexAddress},
	); err != nil {
		cancel()
		t.Fatalf("CLI health failed: %v stderr=%s", err, cliErrors.String())
	}
	if !bytes.Contains(cliOutput.Bytes(), []byte(`"condition":"OK"`)) {
		cancel()
		t.Fatalf("unexpected CLI health response: %s", cliOutput.String())
	}
	cliOutput.Reset()
	if err := cliApp.Execute(
		context.Background(),
		[]string{"flow", "search", "--server", dexAddress},
	); err != nil {
		cancel()
		t.Fatalf("CLI search failed: %v stderr=%s", err, cliErrors.String())
	}
	if !bytes.Contains(cliOutput.Bytes(), []byte(`"flows":[]`)) {
		cancel()
		t.Fatalf("unexpected CLI search response: %s", cliOutput.String())
	}
	cliOutput.Reset()
	if err := cliApp.Execute(
		context.Background(),
		[]string{"api", "call", "HealthCheck", "--server", dexAddress},
	); err != nil {
		cancel()
		t.Fatalf("CLI API call failed: %v stderr=%s", err, cliErrors.String())
	}
	if !bytes.Contains(cliOutput.Bytes(), []byte(`"condition":"OK"`)) {
		cancel()
		t.Fatalf("unexpected CLI API response: %s", cliOutput.String())
	}
	for _, value := range []string{"thinking", "still-thinking"} {
		cliOutput.Reset()
		requestJSON := fmt.Sprintf(
			`{"flowId":"cli-flow","flowType":"CliFlow","streamName":"progress","streamCapacityBytes":"1048576","value":{"stringValue":%q},"source":"cli#writer"}`,
			value,
		)
		if err := cliApp.Execute(
			context.Background(),
			[]string{
				"api", "call", "WriteStream",
				"--data", requestJSON,
				"--yes", "--server", dexAddress,
			},
		); err != nil {
			cancel()
			t.Fatalf("CLI Stream write failed: %v stderr=%s", err, cliErrors.String())
		}
	}
	cliOutput.Reset()
	if err := cliApp.Execute(
		context.Background(),
		[]string{
			"api", "call", "ReadStream",
			"--data", `{"flowId":"cli-flow","flowType":"CliFlow","streamName":"progress","waitTimeSeconds":1}`,
			"--server", dexAddress,
		},
	); err != nil {
		cancel()
		t.Fatalf("CLI Stream read failed: %v stderr=%s", err, cliErrors.String())
	}
	var firstRead struct {
		Message struct {
			Value struct {
				StringValue string `json:"stringValue"`
			} `json:"value"`
			ResumeToken string `json:"resumeToken"`
			Source      string `json:"source"`
		} `json:"message"`
	}
	if err := json.Unmarshal(cliOutput.Bytes(), &firstRead); err != nil {
		cancel()
		t.Fatalf("decode first CLI Stream response: %v output=%s", err, cliOutput.String())
	}
	if firstRead.Message.Value.StringValue != "thinking" ||
		firstRead.Message.Source != "cli#writer" || firstRead.Message.ResumeToken == "" {
		cancel()
		t.Fatalf("unexpected CLI Stream response: %s", cliOutput.String())
	}
	cliOutput.Reset()
	secondReadRequest := fmt.Sprintf(
		`{"flowId":"cli-flow","flowType":"CliFlow","streamName":"progress","resumeToken":%q,"waitTimeSeconds":1}`,
		firstRead.Message.ResumeToken,
	)
	if err := cliApp.Execute(
		context.Background(),
		[]string{
			"api", "call", "ReadStream",
			"--data", secondReadRequest,
			"--server", dexAddress,
		},
	); err != nil {
		cancel()
		t.Fatalf("CLI second Stream read failed: %v stderr=%s", err, cliErrors.String())
	}
	if !bytes.Contains(cliOutput.Bytes(), []byte(`"stringValue":"still-thinking"`)) ||
		!bytes.Contains(cliOutput.Bytes(), []byte(`"source":"cli#writer"`)) {
		cancel()
		t.Fatalf("unexpected second CLI Stream response: %s", cliOutput.String())
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
	if !strings.Contains(output.String(), "State dir:     "+cfg.zdDataDirectory()) {
		t.Fatalf("missing state directory: %s", output.String())
	}
	if _, err := os.Stat(cfg.zdDataDirectory()); err != nil {
		t.Fatal(err)
	}

	for _, port := range []int{cfg.DexPort, cfg.WebPort} {
		listener, err := net.Listen("tcp", net.JoinHostPort(cfg.BindAddress, strconv.Itoa(port)))
		if err != nil {
			t.Fatalf("port %d was not released: %v", port, err)
		}
		if err := listener.Close(); err != nil {
			t.Fatalf("close port probe %d: %v", port, err)
		}
	}
}

func TestRemovedTemporalFlagsExplainMigration(t *testing.T) {
	for _, arguments := range [][]string{
		{"--engine", "temporal"}, {"--engine=temporal"},
		{"--external-temporal-address", "localhost:7233"}, {"--external-temporal-namespace=default"},
		{"--external-temporal-new-flag=value"}, {"--temporal-port", "7233"},
		{"--sqlite-db-filename=state.db"}, {"--verbose-engine-log=false"}, {"--blob-store-dir", "blobs"},
	} {
		_, err := parseConfig(arguments, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "Temporal engine was removed; server-zd is the only engine") {
			t.Fatalf("parseConfig(%v) = %v", arguments, err)
		}
	}
}

func TestConcurrentLocalStacksUseDistinctPortsAndDataDirectories(t *testing.T) {
	stateDirectory := t.TempDir()
	configs := []*Config{testConfig(t), testConfig(t)}
	outputs := []*synchronizedBuffer{{}, {}}
	cancellations := make([]context.CancelFunc, len(configs))
	completions := make([]chan error, len(configs))
	for i, cfg := range configs {
		cfg.StateDirectory = stateDirectory
		runCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cancellations[i] = cancel
		completions[i] = make(chan error, 1)
		go func() { completions[i] <- newSupervisor(cfg, outputs[i], outputs[i]).Run(runCtx) }()
	}
	for i := range configs {
		waitForReadyStack(t, outputs[i], completions[i])
	}
	if configs[0].DexPort == configs[1].DexPort || configs[0].WebPort == configs[1].WebPort {
		t.Fatalf("stacks reused ports: %+v and %+v", configs[0], configs[1])
	}
	if configs[0].zdDataDirectory() == configs[1].zdDataDirectory() {
		t.Fatalf("stacks reused state directory %s", configs[0].zdDataDirectory())
	}
	for _, cancel := range cancellations {
		cancel()
	}
	for i, completed := range completions {
		select {
		case err := <-completed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("local stack did not stop")
		}
		if _, err := os.Stat(configs[i].zdDataDirectory()); err != nil {
			t.Fatal(err)
		}
	}
}

func waitForReadyStack(t *testing.T, output *synchronizedBuffer, runFinished <-chan error) {
	t.Helper()
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		text := output.String()
		if strings.Contains(text, "Dex development environment is ready") {
			return
		}
		select {
		case err := <-runFinished:
			t.Fatalf("local stack exited before readiness: %v\n%s", err, text)
		case <-deadline.C:
			t.Fatalf("local stack did not become ready: %s", text)
		case <-ticker.C:
		}
	}
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
