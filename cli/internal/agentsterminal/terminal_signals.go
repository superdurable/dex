// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package agentsterminal

import (
	"os"
	"os/signal"
	"sync"
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
	ReleaseTerminal() error
	RestoreTerminal() error
}

// sessionRefresher reads the session again after the chat resumes.
type sessionRefresher interface {
	RequestRefresh()
}

// terminalSignals handles SIGINT, SIGTERM, SIGHUP, SIGTSTP, and SIGCONT for
// the chat. SIGTSTP releases the terminal and stops only this process;
// SIGCONT touches the terminal again only in the foreground process group.
type terminalSignals struct {
	program   terminalProgram
	terminal  *os.File
	reporter  *readyReporter
	refresher sessionRefresher

	isForeground func() bool
	stopProcess  func()

	signals chan os.Signal
	done    chan struct{}
	wait    sync.WaitGroup
}

func newTerminalSignals(program terminalProgram, terminal *os.File, reporter *readyReporter, refresher sessionRefresher) *terminalSignals {
	handler := &terminalSignals{
		program:   program,
		terminal:  terminal,
		reporter:  reporter,
		refresher: refresher,
		signals:   make(chan os.Signal, 8),
		done:      make(chan struct{}),
	}
	handler.isForeground = func() bool { return isForegroundProcessGroup(terminal) }
	handler.stopProcess = stopThisProcess
	return handler
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
	isReleased := false
	var foregroundPoll <-chan time.Time
	for {
		select {
		case <-handler.done:
			return
		case received := <-handler.signals:
			switch received {
			case syscall.SIGINT:
				handler.program.Send(externalSignalMsg{exitCode: exitCodeInterrupted})
			case syscall.SIGTERM:
				handler.program.Send(externalSignalMsg{exitCode: exitCodeTerminated})
			case syscall.SIGHUP:
				handler.program.Send(externalSignalMsg{exitCode: exitCodeHangUp})
			case syscall.SIGTSTP:
				if !isReleased {
					if err := handler.program.ReleaseTerminal(); err != nil {
						debugf("release the terminal: %v", err)
					}
					isReleased = true
				}
				handler.suspend()
				isReleased = !handler.resumeInForeground()
			case syscall.SIGCONT:
				if isReleased {
					isReleased = !handler.resumeInForeground()
				}
			}
		case <-foregroundPoll:
			isReleased = !handler.resumeInForeground()
		}
		foregroundPoll = nil
		if isReleased {
			foregroundPoll = time.After(foregroundPollInterval)
		}
	}
}

// suspend stops this process with the default SIGTSTP action and returns
// after SIGCONT. It signals only its own pid, never the process group.
func (handler *terminalSignals) suspend() {
	signal.Reset(syscall.SIGTSTP)
	handler.stopProcess()
	signal.Notify(handler.signals, syscall.SIGTSTP)
}

// resumeInForeground takes the terminal back when this process is in the
// foreground process group, and reports whether it did.
func (handler *terminalSignals) resumeInForeground() bool {
	if !handler.isForeground() {
		return false
	}
	if err := handler.program.RestoreTerminal(); err != nil {
		debugf("restore the terminal: %v", err)
		return false
	}
	handler.refresher.RequestRefresh()
	return true
}

func stopThisProcess() {
	if err := syscall.Kill(os.Getpid(), syscall.SIGTSTP); err != nil {
		debugf("stop this process: %v", err)
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
