// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package agentstest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// DexcliEnvironmentVariable names the dexcli executable for PrivateDexEngine.
const DexcliEnvironmentVariable = "DEXCLI"

// DexServerZDEnvironmentVariable names the dex-server-zd executable; the default sits next to dexcli.
const DexServerZDEnvironmentVariable = "DEX_SERVER_ZD_BINARY"

// EngineReadyLine is the line dexcli dev prints once its Dex Server accepts calls.
const EngineReadyLine = "Dex development environment is ready"

const readyPollInterval = 50 * time.Millisecond

// FindFreePorts returns count distinct free TCP ports on 127.0.0.1.
//
// A port can still be taken after this call returns, which is rare enough for
// tests. It returns an error when count is not positive or a port cannot be
// probed.
func FindFreePorts(count int) ([]int, error) {
	if count < 1 {
		return nil, errors.New("count must be at least one")
	}
	listeners := make([]net.Listener, 0, count)
	defer func() {
		for _, listener := range listeners {
			// Closing a probe listener cannot lose data; a failure only leaks one test socket.
			if err := listener.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "close port probe: %v\n", err)
			}
		}
	}()
	ports := make([]int, 0, count)
	for range count {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("probe a free port: %w", err)
		}
		listeners = append(listeners, listener)
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
	}
	return ports, nil
}

// ResolveDexcliPath returns $DEXCLI when set, otherwise dexcli from PATH.
//
// It returns an error when no executable is found.
func ResolveDexcliPath() (string, error) {
	candidate := os.Getenv(DexcliEnvironmentVariable)
	if candidate == "" {
		found, err := exec.LookPath("dexcli")
		if err != nil {
			return "", fmt.Errorf("set %s to the dexcli executable", DexcliEnvironmentVariable)
		}
		candidate = found
	}
	return filepath.Abs(candidate)
}

// KillableProcess runs one child process in its own process group, so a test can SIGKILL it.
//
// The child writes stdout and stderr to a log file. Start returns once a
// ready line appears in the output written by this start. Kill sends SIGKILL
// to the whole process group, so neither the child nor anything it started
// shuts down cleanly. A killed process can be started again.
type KillableProcess struct {
	name      string
	logPath   string
	command   *exec.Cmd
	logOffset int64
	exited    chan error
}

// NewKillableProcess creates a stopped process whose output appends to logPath.
func NewKillableProcess(name string, logPath string) *KillableProcess {
	return &KillableProcess{name: name, logPath: logPath}
}

// LogPath returns the log path passed to NewKillableProcess.
func (process *KillableProcess) LogPath() string {
	return process.logPath
}

// Start runs arguments with environment until readyLine appears in its output.
//
// environment nil inherits this process's environment. It returns an error
// when the process is already running, exits before it is ready, or misses
// readyTimeout, after which it is killed.
func (process *KillableProcess) Start(
	ctx context.Context,
	arguments []string,
	environment []string,
	readyLine string,
	readyTimeout time.Duration,
) error {
	if process.command != nil {
		return fmt.Errorf("%s is already running", process.name)
	}
	if err := os.MkdirAll(filepath.Dir(process.logPath), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(process.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		// The child holds its own descriptor; this one is no longer needed.
		if closeErr := logFile.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "close %s log: %v\n", process.name, closeErr)
		}
	}()
	info, err := logFile.Stat()
	if err != nil {
		return err
	}
	process.logOffset = info.Size()
	command := exec.Command(arguments[0], arguments[1:]...)
	command.Env = environment
	command.Stdout, command.Stderr = logFile, logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start %s: %w", process.name, err)
	}
	process.command = command
	process.exited = make(chan error, 1)
	go func(exited chan<- error) { exited <- command.Wait() }(process.exited)
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		output, err := process.ReadLog()
		if err != nil {
			return err
		}
		if strings.Contains(output, readyLine) {
			return nil
		}
		select {
		case exitErr := <-process.exited:
			process.command = nil
			return fmt.Errorf("%s exited before it was ready (%v):\n%s", process.name, exitErr, output)
		case <-ctx.Done():
			process.Kill()
			return ctx.Err()
		case <-time.After(readyPollInterval):
		}
	}
	output, err := process.ReadLog()
	process.Kill()
	if err != nil {
		return err
	}
	return fmt.Errorf("%s was not ready in %s:\n%s", process.name, readyTimeout, output)
}

// IsRunning reports whether the child runs.
func (process *KillableProcess) IsRunning() bool {
	if process.command == nil {
		return false
	}
	select {
	case exitErr := <-process.exited:
		process.exited <- exitErr
		return false
	default:
		return true
	}
}

// Kill sends SIGKILL to the child's process group and waits for the child to exit.
func (process *KillableProcess) Kill() {
	if process.command == nil {
		return
	}
	if err := syscall.Kill(-process.command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		fmt.Fprintf(os.Stderr, "kill %s: %v\n", process.name, err)
	}
	// The exit status of a killed child is always an error; only the exit matters.
	<-process.exited
	process.command = nil
}

// ReadLog returns the output written since the last Start.
func (process *KillableProcess) ReadLog() (string, error) {
	content, err := os.ReadFile(process.logPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if int64(len(content)) < process.logOffset {
		return "", nil
	}
	return string(content[process.logOffset:]), nil
}

// PrivateDexEngine runs one dexcli dev engine on free ports with its state in a directory.
//
// Every test that needs a clean server, or kills the server, gets its own
// engine. A killed engine started again resumes every Flow.
type PrivateDexEngine struct {
	stateDir string
	dexcli   string
	serverZD string
	dexPort  int
	webPort  int
	process  *KillableProcess
}

// NewPrivateDexEngine describes an engine without starting it.
//
// It returns an error when dexcli or dex-server-zd cannot be found, or no free
// ports are available.
func NewPrivateDexEngine(stateDir string) (*PrivateDexEngine, error) {
	dexcli, err := ResolveDexcliPath()
	if err != nil {
		return nil, err
	}
	serverZD := os.Getenv(DexServerZDEnvironmentVariable)
	if serverZD == "" {
		serverZD = filepath.Join(filepath.Dir(dexcli), "dex-server-zd")
	}
	if _, err := os.Stat(serverZD); err != nil {
		return nil, fmt.Errorf("put dex-server-zd next to dexcli or set %s: %w", DexServerZDEnvironmentVariable, err)
	}
	ports, err := FindFreePorts(2)
	if err != nil {
		return nil, err
	}
	return &PrivateDexEngine{
		stateDir: stateDir, dexcli: dexcli, serverZD: serverZD, dexPort: ports[0], webPort: ports[1],
		process: NewKillableProcess("dexcli dev", filepath.Join(stateDir, "dexcli.log")),
	}, nil
}

// Address returns "127.0.0.1:<dex port>".
func (engine *PrivateDexEngine) Address() string {
	return fmt.Sprintf("127.0.0.1:%d", engine.dexPort)
}

// Dexcli returns the dexcli executable the engine runs.
func (engine *PrivateDexEngine) Dexcli() string {
	return engine.dexcli
}

// Start runs dexcli dev and waits up to a minute for it to be ready.
func (engine *PrivateDexEngine) Start(ctx context.Context) error {
	return engine.process.Start(ctx, []string{
		engine.dexcli, "dev",
		"-engine", "zd",
		"-zd-binary", engine.serverZD,
		"-zd-data-dir", filepath.Join(engine.stateDir, "zd"),
		"-connector-config-dir", filepath.Join(engine.stateDir, "connectors"),
		"-bind-address", "127.0.0.1",
		"-dex-port", fmt.Sprint(engine.dexPort),
		"-web-port", fmt.Sprint(engine.webPort),
		"-open=false",
	}, nil, EngineReadyLine, time.Minute)
}

// Kill SIGKILLs the engine.
func (engine *PrivateDexEngine) Kill() {
	engine.process.Kill()
}

// StepHistoryEntry describes one completed Step method in a Flow's history.
type StepHistoryEntry struct {
	// EventType is "StepWaitForCompleted" or "StepExecuteCompleted".
	EventType string
	// StepType is the Step type, such as "RunModel".
	StepType string
	// StepExecutionID is the Step execution ID, such as "RunModel-2".
	StepExecutionID string
	// FinalAttempt is the one-based attempt that completed the method.
	FinalAttempt int
}

// ExecuteCompletedEvent is the history event type of a completed Execute.
const ExecuteCompletedEvent = "StepExecuteCompleted"

// ReadStepHistory returns every completed Step method of a Flow's current run, in history order.
//
// It runs dexcli flow history. Steps that still wait or run are not included.
// It returns an error when dexcli fails or prints unexpected JSON.
func ReadStepHistory(ctx context.Context, dexcli string, serverAddress string, flowID string) ([]StepHistoryEntry, error) {
	output, err := exec.CommandContext(ctx, dexcli, "flow", "history", flowID,
		"--server", serverAddress, "--all", "--output", "json").Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return nil, fmt.Errorf("dexcli flow history failed: %s", exitError.Stderr)
		}
		return nil, err
	}
	var history struct {
		Events []struct {
			Type    string `json:"type"`
			Payload struct {
				Context struct {
					StepType        string          `json:"stepType"`
					StepExecutionID string          `json:"stepExecutionId"`
					FinalAttempt    json.RawMessage `json:"finalAttempt"`
				} `json:"context"`
			} `json:"payload"`
		} `json:"events"`
	}
	if err := json.Unmarshal(output, &history); err != nil {
		return nil, fmt.Errorf("decode dexcli flow history: %w", err)
	}
	entries := []StepHistoryEntry{}
	for _, event := range history.Events {
		if event.Type != "StepWaitForCompleted" && event.Type != ExecuteCompletedEvent {
			continue
		}
		attemptText := strings.Trim(string(event.Payload.Context.FinalAttempt), `"`)
		var finalAttempt int
		if _, err := fmt.Sscan(attemptText, &finalAttempt); err != nil {
			return nil, fmt.Errorf("history attempt %q: %w", attemptText, err)
		}
		entries = append(entries, StepHistoryEntry{
			EventType: event.Type, StepType: event.Payload.Context.StepType,
			StepExecutionID: event.Payload.Context.StepExecutionID, FinalAttempt: finalAttempt,
		})
	}
	return entries, nil
}

// ExecutedStepTypes returns the Step types of the completed Executes, in history order.
func ExecutedStepTypes(entries []StepHistoryEntry) []string {
	stepTypes := []string{}
	for _, entry := range entries {
		if entry.EventType == ExecuteCompletedEvent {
			stepTypes = append(stepTypes, entry.StepType)
		}
	}
	return stepTypes
}
