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
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
// Ports come from 20000-32767, below the Linux and macOS ephemeral ranges, so
// outbound connections and listeners on port 0 cannot take one before it is
// used. No port is returned twice in one process. Another process can still
// bind a returned port. It returns an error when count is not positive or
// too few ports are free.
func FindFreePorts(count int) ([]int, error) {
	if count < 1 {
		return nil, errors.New("count must be at least one")
	}
	return testPorts.allocate(count)
}

const (
	firstTestPort = 20000
	lastTestPort  = 32767
)

var testPorts = &testPortAllocator{handedOut: map[int]bool{}}

// testPortAllocator hands out each free port at most once per process.
type testPortAllocator struct {
	mu        sync.Mutex
	handedOut map[int]bool
}

func (allocator *testPortAllocator) allocate(count int) ([]int, error) {
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	portCount := lastTestPort - firstTestPort + 1
	// A random start keeps test processes running side by side from probing the same ports first.
	offset := rand.IntN(portCount)
	ports := make([]int, 0, count)
	for index := 0; index < portCount && len(ports) < count; index++ {
		port := firstTestPort + (offset+index)%portCount
		if allocator.handedOut[port] {
			continue
		}
		isFree, err := isPortFree(port)
		if err != nil {
			return nil, err
		}
		if isFree {
			allocator.handedOut[port] = true
			ports = append(ports, port)
		}
	}
	if len(ports) < count {
		return nil, fmt.Errorf("found %d of %d free ports in %d-%d", len(ports), count, firstTestPort, lastTestPort)
	}
	return ports, nil
}

// isPortFree reports whether a listener can bind port on 127.0.0.1.
//
// It returns an error only when the probe listener cannot be closed.
func isPortFree(port int) (bool, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		// A failed bind is the answer: something else holds the port.
		return false, nil
	}
	if err := listener.Close(); err != nil {
		return false, fmt.Errorf("close the probe listener on port %d: %w", port, err)
	}
	return true, nil
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
//
// The child is reaped only while no signal is being sent, so a signal never
// reaches a process group whose number the system reused after the child
// exited. After the child exits, Kill and SignalProcessGroup send nothing.
type KillableProcess struct {
	name      string
	logPath   string
	logOffset int64
	// mu orders reaping and signalling; the child is reaped only while it is held.
	mu    sync.Mutex
	child *killableChild
}

// killableChild is one started child; its fields after exited is closed are final.
type killableChild struct {
	command   *exec.Cmd
	hasExited bool
	exitErr   error
	exited    chan struct{}
}

// childReapPollInterval is how often the reaper checks, without blocking, whether the child exited.
const childReapPollInterval = 20 * time.Millisecond

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
// readyTimeout, after which it is killed. A child that exited after an
// earlier Start may be started again.
func (process *KillableProcess) Start(
	ctx context.Context,
	arguments []string,
	environment []string,
	readyLine string,
	readyTimeout time.Duration,
) error {
	if process.IsRunning() {
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
	child := &killableChild{command: command, exited: make(chan struct{})}
	process.mu.Lock()
	process.child = child
	process.mu.Unlock()
	go process.reapChild(child)
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
		case <-child.exited:
			// Re-read: the child may have written its failure after the read above.
			finalOutput, readErr := process.ReadLog()
			if readErr == nil && strings.Contains(finalOutput, readyLine) {
				// It was ready, then exited on its own.
				return nil
			}
			process.forgetChild(child)
			if readErr != nil {
				return fmt.Errorf("%s exited before it was ready (%v); read its log: %w", process.name, child.exitErr, readErr)
			}
			return fmt.Errorf("%s exited before it was ready (%v); output tail of %s:\n%s",
				process.name, child.exitErr, process.logPath, tailOfOutput(finalOutput))
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
	return fmt.Errorf("%s was not ready in %s; output tail of %s:\n%s",
		process.name, readyTimeout, process.logPath, tailOfOutput(output))
}

// reapChild polls until the child exits, then reaps it while holding mu.
//
// exec.Cmd.Wait would reap outside the lock, and a signal sent after that
// could reach a reused process group.
func (process *KillableProcess) reapChild(child *killableChild) {
	ticker := time.NewTicker(childReapPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		if process.tryReapChild(child) {
			return
		}
	}
}

// tryReapChild reaps the child if it exited and reports whether it did.
func (process *KillableProcess) tryReapChild(child *killableChild) bool {
	process.mu.Lock()
	defer process.mu.Unlock()
	var status syscall.WaitStatus
	pid, err := syscall.Wait4(child.command.Process.Pid, &status, syscall.WNOHANG, nil)
	if errors.Is(err, syscall.EINTR) || (err == nil && pid == 0) {
		return false
	}
	if err != nil {
		child.exitErr = fmt.Errorf("wait for %s: %w", process.name, err)
	} else {
		child.exitErr = describeChildExit(status)
	}
	child.hasExited = true
	if releaseErr := child.command.Process.Release(); releaseErr != nil {
		fmt.Fprintf(os.Stderr, "release %s: %v\n", process.name, releaseErr)
	}
	close(child.exited)
	return true
}

// describeChildExit returns nil for exit status 0 and an error naming the status or signal otherwise.
func describeChildExit(status syscall.WaitStatus) error {
	switch {
	case status.Signaled():
		return fmt.Errorf("signal: %v", status.Signal())
	case status.ExitStatus() != 0:
		return fmt.Errorf("exit status %d", status.ExitStatus())
	default:
		return nil
	}
}

// forgetChild drops child if it is still the current one.
func (process *KillableProcess) forgetChild(child *killableChild) {
	process.mu.Lock()
	defer process.mu.Unlock()
	if process.child == child {
		process.child = nil
	}
}

// startFailureOutputTailBytes bounds the child output quoted in a Start error.
const startFailureOutputTailBytes = 8000

// tailOfOutput returns the last startFailureOutputTailBytes of output, or a placeholder for no output.
func tailOfOutput(output string) string {
	if output == "" {
		return "(no output)"
	}
	if len(output) > startFailureOutputTailBytes {
		return "..." + output[len(output)-startFailureOutputTailBytes:]
	}
	return output
}

// IsRunning reports whether the child runs.
func (process *KillableProcess) IsRunning() bool {
	process.mu.Lock()
	defer process.mu.Unlock()
	return process.child != nil && !process.child.hasExited
}

// Kill sends SIGKILL to the child's process group and waits for the child to exit.
//
// It sends nothing when the child already exited: its process group number
// may belong to another process by then.
func (process *KillableProcess) Kill() {
	process.mu.Lock()
	child := process.child
	if child == nil {
		process.mu.Unlock()
		return
	}
	if !child.hasExited {
		if err := syscall.Kill(-child.command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			fmt.Fprintf(os.Stderr, "kill %s: %v\n", process.name, err)
		}
	}
	process.mu.Unlock()
	// The exit status of a killed child is always an error; only the exit matters.
	<-child.exited
	process.forgetChild(child)
}

// SignalProcessGroup sends signal to the child's process group.
//
// Use syscall.SIGSTOP to freeze a Worker, so a request it would serve waits
// with its locks held, and syscall.SIGCONT to resume it. It returns an error
// when the child is not running, including after it exited on its own, or
// the signal cannot be sent. Since 0.6.0.
func (process *KillableProcess) SignalProcessGroup(signal syscall.Signal) error {
	process.mu.Lock()
	defer process.mu.Unlock()
	if process.child == nil || process.child.hasExited {
		return fmt.Errorf("%s is not running", process.name)
	}
	if err := syscall.Kill(-process.child.command.Process.Pid, signal); err != nil {
		return fmt.Errorf("send %v to %s: %w", signal, process.name, err)
	}
	return nil
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

// processCrashSafeServerZDConfig skips fsync; a config file also works with dexcli releases that predate
// --durability.
const processCrashSafeServerZDConfig = "dev:\n  durabilityMode: processCrashSafe\n"

// PrivateDexEngine runs one dexcli dev engine on free ports with its state in a directory.
//
// Every test that needs a clean server, or kills the server, gets its own
// engine. A killed engine started again resumes every Flow.
type PrivateDexEngine struct {
	stateDir string
	dexcli   string
	serverZD string
	// serverZDConfig is a dex-server-zd YAML file, or "" for the processCrashSafe file Start writes.
	serverZDConfig string
	dexPort        int
	webPort        int
	// hasStarted is set once the engine was ready; its ports never change after that.
	hasStarted bool
	process    *KillableProcess
}

// NewPrivateDexEngine describes an engine without starting it.
//
// It returns an error when dexcli or dex-server-zd cannot be found, or no free
// ports are available.
func NewPrivateDexEngine(stateDir string) (*PrivateDexEngine, error) {
	return NewPrivateDexEngineWithServerZDConfig(stateDir, "")
}

// NewPrivateDexEngineWithServerZDConfig describes an engine whose dex-server-zd reads a YAML
// configuration file, such as one that lowers values.maxValueBytes, without starting it.
//
// An empty serverZDConfigPath passes a file that sets only dev.durabilityMode: processCrashSafe,
// as NewPrivateDexEngine does. That mode skips fsync and survives a process kill, but not a host
// crash. A file of your own replaces it, so set dev.durabilityMode there too, or the engine uses
// the slower strict default of dex-server-zd 0.1.18 and later. It returns an error when dexcli or
// dex-server-zd cannot be found, or no free ports are available.
func NewPrivateDexEngineWithServerZDConfig(stateDir string, serverZDConfigPath string) (*PrivateDexEngine, error) {
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
		stateDir: stateDir, dexcli: dexcli, serverZD: serverZD, serverZDConfig: serverZDConfigPath,
		dexPort: ports[0], webPort: ports[1],
		process: NewKillableProcess("dexcli dev", filepath.Join(stateDir, "dexcli.log")),
	}, nil
}

// Address returns "127.0.0.1:<dex port>".
//
// The first Start can move the engine to new ports when another process took
// one, so read the address after Start returns.
func (engine *PrivateDexEngine) Address() string {
	return fmt.Sprintf("127.0.0.1:%d", engine.dexPort)
}

// Dexcli returns the dexcli executable the engine runs.
func (engine *PrivateDexEngine) Dexcli() string {
	return engine.dexcli
}

// Start runs dexcli dev and waits up to a minute for it to be ready.
//
// When the first Start fails because another process took one of the engine's
// ports, it retries on new ports. A later Start keeps the ports, so a
// restarted engine has the same address. It returns an error when dexcli dev
// does not become ready.
func (engine *PrivateDexEngine) Start(ctx context.Context) error {
	configPath := engine.serverZDConfig
	if configPath == "" {
		configPath = filepath.Join(engine.stateDir, "zd-overrides.yaml")
		if err := os.MkdirAll(engine.stateDir, 0o755); err != nil {
			return fmt.Errorf("create engine state directory: %w", err)
		}
		if err := os.WriteFile(configPath, []byte(processCrashSafeServerZDConfig), 0o600); err != nil {
			return fmt.Errorf("write the dex-server-zd durability config: %w", err)
		}
	}
	for attempt := 1; ; attempt++ {
		startErr := engine.process.Start(ctx, engine.devArguments(configPath), nil, EngineReadyLine, time.Minute)
		if startErr == nil {
			engine.hasStarted = true
			return nil
		}
		if engine.hasStarted || attempt == maxEngineStartAttempts || ctx.Err() != nil {
			return startErr
		}
		hasLostAPort, err := engine.hasLostAPort()
		if err != nil {
			return errors.Join(startErr, err)
		}
		if !hasLostAPort {
			return startErr
		}
		ports, err := FindFreePorts(2)
		if err != nil {
			return errors.Join(startErr, err)
		}
		engine.dexPort, engine.webPort = ports[0], ports[1]
	}
}

// maxEngineStartAttempts bounds how often the first Start moves to new ports.
const maxEngineStartAttempts = 3

// portInUseText appears in dexcli's and dex-server-zd's errors for a port another process holds.
const portInUseText = "already in use"

func (engine *PrivateDexEngine) devArguments(configPath string) []string {
	return []string{
		engine.dexcli, "dev",
		"-engine", "zd",
		"-zd-binary", engine.serverZD,
		"-zd-data-dir", filepath.Join(engine.stateDir, "zd"),
		"-connector-config-dir", filepath.Join(engine.stateDir, "connectors"),
		"-bind-address", "127.0.0.1",
		"-dex-port", fmt.Sprint(engine.dexPort),
		"-web-port", fmt.Sprint(engine.webPort),
		"-open=false",
		"-zd-config", configPath,
	}
}

// hasLostAPort reports whether the last start exited because one of its ports was in use.
func (engine *PrivateDexEngine) hasLostAPort() (bool, error) {
	if engine.process.IsRunning() {
		return false, nil
	}
	output, err := engine.process.ReadLog()
	if err != nil {
		return false, fmt.Errorf("read the dexcli dev log: %w", err)
	}
	return strings.Contains(output, portInUseText), nil
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
