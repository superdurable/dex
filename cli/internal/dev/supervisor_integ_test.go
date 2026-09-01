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
	"encoding/json"
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

	"github.com/superdurable/dex/cli/internal/command"
)

func TestLocalStackStartsAndReleasesPorts(t *testing.T) {
	ports := freePorts(t, 4)
	cfg := testConfig(t)
	cfg.TemporalPort = ports[0]
	cfg.TemporalUIPort = ports[1]
	cfg.DexPort = ports[2]
	cfg.WebPort = ports[3]
	cfg.explicitLocalFlags["dex-port"] = true
	cfg.explicitLocalFlags["web-port"] = true
	cfg.LogDirectory = t.TempDir()
	cfg.VerboseEngineLog = true
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
	if strings.Contains(output.String(), "Temporal") ||
		strings.Contains(output.String(), cfg.temporalAddress()) ||
		strings.Contains(output.String(), ":"+strconv.Itoa(cfg.TemporalUIPort)) {
		t.Fatalf("workflow backend leaked in output: %s", output.String())
	}
	if !strings.Contains(output.String(), "Local DB:      "+cfg.SQLiteDBFilename) {
		t.Fatalf("missing Local DB path: %s", output.String())
	}
	if !strings.Contains(output.String(), "Blob store:    "+cfg.adjacentBlobStoreDirectory()) {
		t.Fatalf("missing blob store path: %s", output.String())
	}
	if !strings.Contains(output.String(), "Server log folder: "+cfg.LogDirectory) {
		t.Fatalf("missing server log folder: %s", output.String())
	}
	logContents, err := os.ReadFile(cfg.temporalEngineLogPath())
	if err != nil {
		t.Fatalf("read workflow engine log file: %v", err)
	}
	logText := string(logContents)
	if !strings.Contains(logText, cfg.temporalAddress()) {
		t.Fatalf("workflow engine log missing gRPC address: %s", logText)
	}
	if !strings.Contains(logText, strconv.Itoa(cfg.TemporalUIPort)) {
		t.Fatalf("workflow engine log missing Web port: %s", logText)
	}
	if !strings.Contains(logText, cfg.sqliteDBDirectory()) {
		t.Fatalf("workflow engine log missing DB directory: %s", logText)
	}
	if !strings.Contains(logText, "level=INFO") {
		t.Fatalf("workflow engine log missing Temporal Server info logs: %s", logText)
	}
	dexLog, err := os.ReadFile(cfg.dexServerLogPath())
	if err != nil {
		t.Fatalf("Dex server log file missing: %v", err)
	}
	if !bytes.Contains(dexLog, []byte("Started Worker")) {
		t.Fatalf("Dex server log missing Temporal worker logs: %s", dexLog)
	}
	if cfg.SQLiteDBFilename == "" {
		t.Fatal("missing Temporal database")
	}
	if _, err := os.Stat(cfg.SQLiteDBFilename); err != nil {
		t.Fatalf("Temporal database was not retained after shutdown: %v", err)
	}
	if _, err := os.Stat(cfg.adjacentBlobStoreDirectory()); err != nil {
		t.Fatalf("blob store was not retained after shutdown: %v", err)
	}
	for _, port := range []int{cfg.DexPort, cfg.WebPort, cfg.TemporalPort, cfg.TemporalUIPort} {
		listener, err := net.Listen("tcp", net.JoinHostPort(cfg.BindAddress, strconv.Itoa(port)))
		if err != nil {
			t.Fatalf("port %d was not released: %v", port, err)
		}
		if err := listener.Close(); err != nil {
			t.Fatalf("close port probe %d: %v", port, err)
		}
	}
}

func TestExternalWorkflowBackendRemainsRunning(t *testing.T) {
	ports := freePorts(t, 4)
	localConfig := testConfig(t)
	localConfig.TemporalPort = ports[0]
	localConfig.TemporalUIPort = ports[1]
	output := &synchronizedBuffer{}
	temporal, err := startTemporalProcess(localConfig, nil)
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
	temporalClient.Close()
	cancelStartup()

	externalConfig := testConfig(t)
	externalConfig.BlobStoreDirectory = filepath.Join(t.TempDir(), "blobs")
	externalConfig.ExternalTemporalAddress = localConfig.temporalAddress()
	externalConfig.DexPort = ports[2]
	externalConfig.WebPort = ports[3]
	externalConfig.explicitLocalFlags["dex-port"] = true
	externalConfig.explicitLocalFlags["web-port"] = true
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	runFinished := make(chan error, 1)
	go func() {
		runFinished <- newSupervisor(externalConfig, output, output).Run(runCtx)
	}()
	waitForReadyStack(t, output, runFinished)
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
	if strings.Contains(output.String(), "Temporal") ||
		strings.Contains(output.String(), localConfig.temporalAddress()) {
		t.Fatalf("external workflow backend leaked in output: %s", output.String())
	}
	if strings.Contains(output.String(), "Local DB:") {
		t.Fatalf("external mode printed Local DB: %s", output.String())
	}
	if !strings.Contains(output.String(), "Server log folder:") {
		t.Fatalf("missing server log folder: %s", output.String())
	}
	if !strings.Contains(output.String(), "Blob store:") {
		t.Fatalf("missing blob store path: %s", output.String())
	}
}

func TestConcurrentLocalStacksUseDistinctPortsAndDatabases(t *testing.T) {
	stateDirectory := t.TempDir()
	cfg1 := testConfig(t)
	cfg1.StateDirectory = stateDirectory
	cfg2 := testConfig(t)
	cfg2.StateDirectory = stateDirectory
	output1 := &synchronizedBuffer{}
	output2 := &synchronizedBuffer{}
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	finished1 := make(chan error, 1)
	finished2 := make(chan error, 1)
	go func() {
		finished1 <- newSupervisor(cfg1, output1, output1).Run(ctx1)
	}()
	go func() {
		finished2 <- newSupervisor(cfg2, output2, output2).Run(ctx2)
	}()

	waitForReadyStack(t, output1, finished1)
	waitForReadyStack(t, output2, finished2)
	logDir1 := cfg1.LogDirectory
	logDir2 := cfg2.LogDirectory
	if logDir1 == "" || logDir1 == logDir2 {
		cancel1()
		cancel2()
		t.Fatalf("stacks reused server log folders: %q vs %q", logDir1, logDir2)
	}
	if !strings.Contains(output1.String(), "Server log folder: "+logDir1) {
		cancel1()
		cancel2()
		t.Fatalf("missing first server log folder: %s", output1.String())
	}
	if !strings.Contains(output2.String(), "Server log folder: "+logDir2) {
		cancel1()
		cancel2()
		t.Fatalf("missing second server log folder: %s", output2.String())
	}
	if _, err := os.Stat(cfg1.temporalEngineLogPath()); err != nil {
		cancel1()
		cancel2()
		t.Fatalf("first workflow engine log missing during run: %v", err)
	}
	if _, err := os.Stat(cfg2.temporalEngineLogPath()); err != nil {
		cancel1()
		cancel2()
		t.Fatalf("second workflow engine log missing during run: %v", err)
	}
	if _, err := os.Stat(cfg1.dexServerLogPath()); err != nil {
		cancel1()
		cancel2()
		t.Fatalf("first Dex server log missing during run: %v", err)
	}
	if _, err := os.Stat(cfg2.dexServerLogPath()); err != nil {
		cancel1()
		cancel2()
		t.Fatalf("second Dex server log missing during run: %v", err)
	}
	if cfg1.DexPort == cfg2.DexPort ||
		cfg1.WebPort == cfg2.WebPort ||
		cfg1.TemporalPort == cfg2.TemporalPort ||
		cfg1.TemporalUIPort == cfg2.TemporalUIPort {
		cancel1()
		cancel2()
		t.Fatalf("stacks reused ports: %+v vs %+v", cfg1, cfg2)
	}
	if cfg1.SQLiteDBFilename == "" || cfg1.SQLiteDBFilename == cfg2.SQLiteDBFilename {
		cancel1()
		cancel2()
		t.Fatalf("stacks reused Temporal databases: %q vs %q", cfg1.SQLiteDBFilename, cfg2.SQLiteDBFilename)
	}
	if _, err := os.Stat(cfg1.SQLiteDBFilename); err != nil {
		cancel1()
		cancel2()
		t.Fatalf("first Temporal database missing: %v", err)
	}
	if _, err := os.Stat(cfg2.SQLiteDBFilename); err != nil {
		cancel1()
		cancel2()
		t.Fatalf("second Temporal database missing: %v", err)
	}

	cancel1()
	cancel2()
	for _, finished := range []<-chan error{finished1, finished2} {
		select {
		case err := <-finished:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("local stack did not stop")
		}
	}
	if _, err := os.Stat(logDir1); !os.IsNotExist(err) {
		t.Fatalf("ephemeral server log folder was kept: %s (%v)", logDir1, err)
	}
	if _, err := os.Stat(logDir2); !os.IsNotExist(err) {
		t.Fatalf("ephemeral server log folder was kept: %s (%v)", logDir2, err)
	}
}

func TestBlobStoreDirectorySelection(t *testing.T) {
	root := t.TempDir()
	output := &synchronizedBuffer{}

	explicitDirectory := filepath.Join(root, "explicit")
	explicitConfig, err := parseConfig([]string{
		"--blob-store-dir", explicitDirectory,
		"--sqlite-db-filename", filepath.Join(root, "ignored-temporal.db"),
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
	databaseConfig.SQLiteDBFilename = filepath.Join(root, "temporal.db")
	directory, err = newSupervisor(databaseConfig, output, output).prepareBlobStoreDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if directory != filepath.Join(root, localBlobStoreName) {
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
