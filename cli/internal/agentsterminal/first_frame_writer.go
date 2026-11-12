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
	"sync"
	"sync/atomic"
)

// terminalFile is the terminal output: Bubble Tea needs its Fd for the
// window size and terminal state.
type terminalFile interface {
	io.ReadWriteCloser
	Fd() uintptr
}

// firstFrameWriter is the terminal output that reports when the first
// interactive frame reached the terminal: the first write that returns after
// the screen rendered a frame with a window size.
type firstFrameWriter struct {
	terminal      terminalFile
	onFirstFrame  func()
	isFrameReady  atomic.Bool
	firstFrameRun sync.Once
}

func newFirstFrameWriter(terminal terminalFile, onFirstFrame func()) *firstFrameWriter {
	return &firstFrameWriter{terminal: terminal, onFirstFrame: onFirstFrame}
}

// markFrameRendered records that the screen rendered its first sized frame;
// the renderer writes it next.
func (writer *firstFrameWriter) markFrameRendered() {
	writer.isFrameReady.Store(true)
}

// Write writes to the terminal, then reports the first frame once it was
// written.
func (writer *firstFrameWriter) Write(data []byte) (int, error) {
	written, err := writer.terminal.Write(data)
	if err == nil && writer.isFrameReady.Load() {
		writer.firstFrameRun.Do(func() { go writer.onFirstFrame() })
	}
	return written, err
}

// Read reads from the terminal.
func (writer *firstFrameWriter) Read(data []byte) (int, error) {
	return writer.terminal.Read(data)
}

// Close closes the terminal.
func (writer *firstFrameWriter) Close() error {
	return writer.terminal.Close()
}

// Fd returns the terminal's file descriptor.
func (writer *firstFrameWriter) Fd() uintptr {
	return writer.terminal.Fd()
}
