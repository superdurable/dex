// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package dex

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const (
	defaultBufferedTextStreamFlushInterval = time.Second
	defaultBufferedTextStreamMaxBytes      = 16 << 10
)

// BufferedTextStream batches text chunks before appending them to a Step Stream.
//
// Create one writer per logical text feed with NewBufferedTextStream. Write preserves every byte
// and appends the current batch when its one-second default interval or 16 KiB default threshold is
// reached. The invocation flushes and closes the writer before its final result or error is sent.
// Stream Store failures remain unacknowledged, matching Stream.Write.
//
//	progress, err := dex.NewBufferedTextStream(ctx, Thinking)
//	if err != nil {
//		return nil, err
//	}
//	if err := progress.Write(delta); err != nil {
//		return nil, err
//	}
type BufferedTextStream struct {
	mu               sync.Mutex
	invocation       *invocationContext
	stream           Stream[string]
	flushInterval    time.Duration
	maxBufferedBytes int
	buffer           string
	bufferedBytes    int
	timer            *time.Timer
	timerGeneration  uint64
	stopCancellation func() bool
	isClosed         bool
	terminalErr      error
}

type bufferedTextStreamConfig struct {
	flushInterval    time.Duration
	maxBufferedBytes int
}

// BufferedTextStreamOption configures a BufferedTextStream.
type BufferedTextStreamOption interface {
	applyBufferedTextStream(*bufferedTextStreamConfig)
}

// NewBufferedTextStream creates an invocation-managed writer for a registered string Stream.
//
// The default flush interval is one second and the default soft size threshold is 16 KiB of UTF-8
// text. The Context must belong to an active WaitFor or Execute invocation. The SDK automatically
// flushes remaining text and stops the timer before sending that invocation's final result or error.
//
// The returned writer is safe for concurrent Write calls. Reuse it for the entire logical feed;
// creating multiple writers gives each writer an independent buffer and timer.
//
// Returns an error for an inactive, RPC, or Flow-timeout Context, an unregistered Stream, or invalid
// options.
func NewBufferedTextStream(
	ctx Context,
	stream Stream[string],
	options ...BufferedTextStreamOption,
) (*BufferedTextStream, error) {
	config := bufferedTextStreamConfig{
		flushInterval:    defaultBufferedTextStreamFlushInterval,
		maxBufferedBytes: defaultBufferedTextStreamMaxBytes,
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("dex: BufferedTextStream option is nil")
		}
		option.applyBufferedTextStream(&config)
	}
	if config.flushInterval <= 0 {
		return nil, fmt.Errorf("dex: BufferedTextStream flush interval must be positive")
	}
	if config.maxBufferedBytes <= 0 {
		return nil, fmt.Errorf("dex: BufferedTextStream maximum buffered bytes must be positive")
	}
	invocation, ok := ctx.(*invocationContext)
	if !ok || invocation.outputEmitter == nil {
		return nil, errInvalidInvocationContext
	}
	if _, err := invocation.flow.resolveStream(stream.definition); err != nil {
		return nil, fmt.Errorf("dex: %w", err)
	}
	writer := &BufferedTextStream{
		invocation:       invocation,
		stream:           stream,
		flushInterval:    config.flushInterval,
		maxBufferedBytes: config.maxBufferedBytes,
	}
	writer.stopCancellation = context.AfterFunc(invocation, writer.cancel)
	if err := invocation.registerOutputFinalizer(writer); err != nil {
		return nil, err
	}
	return writer, nil
}

// BufferedTextStreamFlushInterval overrides the one-second batch interval.
//
// The duration must be positive. A non-empty buffer is appended when the interval elapses even if
// the handler does not produce another chunk.
func BufferedTextStreamFlushInterval(value time.Duration) BufferedTextStreamOption {
	return bufferedTextStreamFlushIntervalOption{value: value}
}

// BufferedTextStreamMaxBytes overrides the 16 KiB soft UTF-8 size threshold.
//
// The value must be positive. A chunk is never split, so an emitted batch may exceed this threshold
// by the size of its final chunk.
func BufferedTextStreamMaxBytes(value int) BufferedTextStreamOption {
	return bufferedTextStreamMaxBytesOption{value: value}
}

// Write appends one text chunk to the current batch.
//
// Empty chunks are ignored. Write flushes immediately when the soft size threshold is reached. It
// otherwise returns after buffering locally; it does not wait for the interval or Stream Store.
// A timer or transport failure is returned by the next Write or invocation finalization.
func (writer *BufferedTextStream) Write(chunk string) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if err := writer.requireOpenLocked(); err != nil {
		return err
	}
	if chunk == "" {
		return nil
	}
	wasEmpty := writer.buffer == ""
	writer.buffer += chunk
	writer.bufferedBytes += len(chunk)
	if wasEmpty {
		writer.startTimerLocked()
	}
	if writer.bufferedBytes < writer.maxBufferedBytes {
		return nil
	}
	writer.stopTimerLocked()
	return writer.flushLocked()
}

func (writer *BufferedTextStream) finalize() error {
	writer.stopCancellation()
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.isClosed {
		return writer.terminalErr
	}
	writer.stopTimerLocked()
	if writer.terminalErr == nil {
		writer.terminalErr = writer.flushLocked()
	}
	writer.isClosed = true
	return writer.terminalErr
}

func (writer *BufferedTextStream) cancel() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.stopTimerLocked()
	writer.buffer = ""
	writer.bufferedBytes = 0
	writer.isClosed = true
}

func (writer *BufferedTextStream) startTimerLocked() {
	writer.timerGeneration++
	generation := writer.timerGeneration
	writer.timer = time.AfterFunc(writer.flushInterval, func() {
		writer.flushFromTimer(generation)
	})
}

func (writer *BufferedTextStream) flushFromTimer(generation uint64) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.isClosed || writer.terminalErr != nil || generation != writer.timerGeneration {
		return
	}
	writer.timer = nil
	if err := writer.flushLocked(); err != nil {
		writer.terminalErr = err
	}
}

func (writer *BufferedTextStream) stopTimerLocked() {
	writer.timerGeneration++
	if writer.timer != nil {
		writer.timer.Stop()
		writer.timer = nil
	}
}

func (writer *BufferedTextStream) flushLocked() error {
	if writer.buffer == "" {
		return nil
	}
	value := writer.buffer
	writer.buffer = ""
	writer.bufferedBytes = 0
	if err := writer.stream.Write(writer.invocation, value); err != nil {
		writer.terminalErr = err
		return err
	}
	return nil
}

func (writer *BufferedTextStream) requireOpenLocked() error {
	if writer.terminalErr != nil {
		return writer.terminalErr
	}
	if writer.isClosed {
		return errInvalidInvocationContext
	}
	return nil
}

type bufferedTextStreamFlushIntervalOption struct {
	value time.Duration
}

func (option bufferedTextStreamFlushIntervalOption) applyBufferedTextStream(
	config *bufferedTextStreamConfig,
) {
	config.flushInterval = option.value
}

type bufferedTextStreamMaxBytesOption struct {
	value int
}

func (option bufferedTextStreamMaxBytesOption) applyBufferedTextStream(
	config *bufferedTextStreamConfig,
) {
	config.maxBufferedBytes = option.value
}
