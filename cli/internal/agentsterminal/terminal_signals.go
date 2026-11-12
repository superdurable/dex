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

// terminalSignals handles SIGINT, SIGTERM, SIGHUP, SIGTSTP, and SIGCONT for
// the chat. SIGTSTP releases the terminal and stops only this process; the
// terminal is touched again only in the foreground process group.
type terminalSignals struct {
	program terminalProgram

	isForeground func() bool
	stopProcess  func()

	signals      chan os.Signal
	continued    chan struct{}
	isSuspending atomic.Bool
	done         chan struct{}
	wait         sync.WaitGroup
}

func newTerminalSignals(program terminalProgram, terminal *os.File) *terminalSignals {
	return &terminalSignals{
		program:      program,
		isForeground: func() bool { return isForegroundProcessGroup(terminal) },
		stopProcess:  stopThisProcess,
		signals:      make(chan os.Signal, 8),
		continued:    make(chan struct{}, 1),
		done:         make(chan struct{}),
	}
}

// Start begins handling the signals.
func (handler *terminalSignals) Start() {
	signal.Notify(handler.signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGTSTP, syscall.SIGCONT)
	handler.wait.Add(1)
	go handler.run()
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
				go handler.program.Send(externalSignalMsg{exitCode: exitCodeInterrupted})
			case syscall.SIGTERM:
				go handler.program.Send(externalSignalMsg{exitCode: exitCodeTerminated})
			case syscall.SIGHUP:
				go handler.program.Send(externalSignalMsg{exitCode: exitCodeHangUp})
			case syscall.SIGTSTP:
				if handler.isSuspending.CompareAndSwap(false, true) {
					go handler.program.Send(suspendRequestedMsg{command: &suspendCommand{handler: handler}})
				}
			case syscall.SIGCONT:
				select {
				case handler.continued <- struct{}{}:
				default:
				}
			}
		}
	}
}

// suspendCommand runs on the program's event loop with the terminal
// released. It stops this process and returns once the process is in the
// foreground again, so the program never draws or reads in the background.
type suspendCommand struct {
	handler *terminalSignals
}

// Run stops this process with the default SIGTSTP action, signalling only
// its own pid, then waits for the foreground.
func (command *suspendCommand) Run() error {
	handler := command.handler
	defer handler.isSuspending.Store(false)
	signal.Reset(syscall.SIGTSTP)
	handler.stopProcess()
	signal.Notify(handler.signals, syscall.SIGTSTP)
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

func stopThisProcess() {
	if err := syscall.Kill(os.Getpid(), syscall.SIGTSTP); err != nil {
		debugLog("stop this process", err)
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
