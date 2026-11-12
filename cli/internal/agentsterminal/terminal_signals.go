// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package agentsterminal

import (
	"io"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/sys/unix"
)

// foregroundPollInterval is how often a backgrounded chat checks whether it
// is in the foreground again.
const foregroundPollInterval = 500 * time.Millisecond

// The exit codes of external signals.
const (
	exitCodeInterrupted = 130
	exitCodeHangUp      = 1
)

// terminalProgram is the part of tea.Program the signal handling uses.
type terminalProgram interface {
	Send(message tea.Msg)
}

// suspendRequestedMsg asks the program to release the terminal and run the
// suspend command on its event loop, as tea.Exec does.
type suspendRequestedMsg struct {
	command tea.ExecCommand
}

// resumedMsg reports that the suspend command returned in the foreground and
// the program took the terminal back.
type resumedMsg struct{}

// terminalSignals handles SIGINT, SIGTERM, SIGHUP, SIGTSTP, and SIGCONT from
// the start of the handoff to the end of the chat. Before the chat program
// is attached it records the first exit signal and calls onEarlySignal, so
// the handoff stops; Attach hands a recorded signal to the program. SIGTSTP
// releases the terminal and stops only this process; the terminal is touched
// again only in the foreground process group.
type terminalSignals struct {
	mutex   sync.Mutex
	program terminalProgram
	// terminalMode guards the launch terminal check and the Attach handoff.
	terminalMode sync.Mutex
	// beforeTerminalModeLock is a test hook; nil in production.
	beforeTerminalModeLock func()
	earlyExitCode          int
	onEarlySignal          func()
	isEarlyReported        bool

	isForeground func() bool
	stopProcess  func()

	signals      chan os.Signal
	continued    chan struct{}
	isSuspending atomic.Bool
	done         chan struct{}
	wait         sync.WaitGroup
}

func newTerminalSignals(terminal *os.File, onEarlySignal func()) *terminalSignals {
	handler := &terminalSignals{
		onEarlySignal: onEarlySignal,
		isForeground:  func() bool { return isForegroundProcessGroup(terminal) },
		signals:       make(chan os.Signal, 8),
		continued:     make(chan struct{}, 1),
		done:          make(chan struct{}),
	}
	handler.stopProcess = handler.stopUntilContinued
	return handler
}

// Start begins handling the signals.
func (handler *terminalSignals) Start() {
	signal.Notify(handler.signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGTSTP, syscall.SIGCONT)
	handler.wait.Add(1)
	go handler.run()
}

// Attach hands the signals to the chat program from now on, starting with a
// signal that came before it.
func (handler *terminalSignals) Attach(program terminalProgram) {
	// Taking terminalMode orders the handoff against a stop before the chat.
	handler.terminalMode.Lock()
	handler.mutex.Lock()
	handler.program = program
	exitCode := handler.earlyExitCode
	handler.mutex.Unlock()
	handler.terminalMode.Unlock()
	if exitCode != 0 {
		go program.Send(externalSignalMsg{exitCode: exitCode})
	}
}

// HoldTerminalMode runs change, which sets and restores the terminal mode,
// while no stop can happen; a SIGTSTP meanwhile stops after it.
func (handler *terminalSignals) HoldTerminalMode(change func()) {
	handler.terminalMode.Lock()
	defer handler.terminalMode.Unlock()
	change()
}

// EarlyExitCode returns the exit code of a signal that came before Attach,
// or 0.
func (handler *terminalSignals) EarlyExitCode() int {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	return handler.earlyExitCode
}

// Stop ends the handling and restores the default dispositions.
func (handler *terminalSignals) Stop() {
	signal.Stop(handler.signals)
	close(handler.done)
	handler.wait.Wait()
}

func (handler *terminalSignals) run() {
	defer handler.wait.Done()
	for {
		select {
		case <-handler.done:
			return
		case received := <-handler.signals:
			switch received {
			case syscall.SIGINT:
				handler.exit(exitCodeInterrupted)
			case syscall.SIGTERM:
				handler.exit(exitCodeTerminated)
			case syscall.SIGHUP:
				handler.exit(exitCodeHangUp)
			case syscall.SIGTSTP:
				handler.suspend()
			case syscall.SIGCONT:
				select {
				case handler.continued <- struct{}{}:
				default:
				}
			}
		}
	}
}

// exit sends an exit signal to the program, or records it before Attach.
func (handler *terminalSignals) exit(exitCode int) {
	handler.mutex.Lock()
	program := handler.program
	isFirstEarly := program == nil && handler.earlyExitCode == 0
	if isFirstEarly {
		handler.earlyExitCode = exitCode
	}
	handler.mutex.Unlock()
	switch {
	case program != nil:
		go program.Send(externalSignalMsg{exitCode: exitCode})
	case isFirstEarly && handler.onEarlySignal != nil:
		handler.onEarlySignal()
	}
}

// suspend stops this process. Before the chat owns the terminal there is
// nothing to release; afterwards the program releases it on its event loop.
func (handler *terminalSignals) suspend() {
	if !handler.isSuspending.CompareAndSwap(false, true) {
		return
	}
	if program := handler.attachedProgram(); program != nil {
		go program.Send(suspendRequestedMsg{command: &suspendCommand{handler: handler}})
		return
	}
	// Off the signal loop, so exit signals never wait for the terminal mode.
	go handler.stopBeforeChat()
}

// stopBeforeChat stops this process once no one changes the terminal mode,
// unless the chat was attached meanwhile.
func (handler *terminalSignals) stopBeforeChat() {
	if handler.beforeTerminalModeLock != nil {
		handler.beforeTerminalModeLock()
	}
	handler.terminalMode.Lock()
	if program := handler.attachedProgram(); program != nil {
		handler.terminalMode.Unlock()
		// The chat owns the terminal now: suspend through its event loop.
		go program.Send(suspendRequestedMsg{command: &suspendCommand{handler: handler}})
		return
	}
	defer handler.terminalMode.Unlock()
	defer handler.isSuspending.Store(false)
	handler.stopProcess()
}

func (handler *terminalSignals) attachedProgram() terminalProgram {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	return handler.program
}

// suspendCommand runs on the program's event loop with the terminal
// released. It stops this process and returns once the process is in the
// foreground again, so the program never draws or reads in the background.
type suspendCommand struct {
	handler *terminalSignals
}

// Run stops this process, then waits for the foreground.
func (command *suspendCommand) Run() error {
	handler := command.handler
	defer handler.isSuspending.Store(false)
	handler.stopProcess()
	ticker := time.NewTicker(foregroundPollInterval)
	defer ticker.Stop()
	for !handler.isForeground() {
		select {
		case <-handler.continued:
		case <-ticker.C:
		case <-handler.done:
			return nil
		}
	}
	return nil
}

// SetStdin is unused: the command reads nothing.
func (command *suspendCommand) SetStdin(io.Reader) {}

// SetStdout is unused: the command writes nothing.
func (command *suspendCommand) SetStdout(io.Writer) {}

// SetStderr is unused: the command writes nothing.
func (command *suspendCommand) SetStderr(io.Writer) {}

// stopUntilContinued stops this process with SIGSTOP and returns after
// SIGCONT. SIGSTOP cannot be caught, so the stop never comes back as another
// Ctrl-Z.
func (handler *terminalSignals) stopUntilContinued() {
	select {
	case <-handler.continued:
	default:
	}
	if err := unix.Kill(os.Getpid(), unix.SIGSTOP); err != nil {
		debugLog("stop this process", err)
		return
	}
	select {
	case <-handler.continued:
	case <-handler.done:
	}
}

// isForegroundProcessGroup compares the terminal's foreground process group
// with this process's group. A background process may ask this without
// SIGTTOU.
func isForegroundProcessGroup(terminal *os.File) bool {
	foreground, err := unix.IoctlGetInt(int(terminal.Fd()), unix.TIOCGPGRP)
	if err != nil {
		return false
	}
	return foreground == unix.Getpgrp()
}
