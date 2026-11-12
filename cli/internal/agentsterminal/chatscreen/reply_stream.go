// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"fmt"
	"strings"

	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// replyStream turns streamed text into finished blocks. Corrections are
// appended, because the scrollback cannot change.
type replyStream struct {
	groups          map[replyGroupKey]*replyGroup
	activeGroupKey  replyGroupKey
	hasActiveGroup  bool
	pendingText     string
	reconciledTurns map[string]bool
}

// replyGroupKey groups deltas of one model Step in one turn.
type replyGroupKey struct {
	turnID          string
	stepExecutionID string
}

type replyGroup struct {
	attempt      int
	streamedText string
	isRecorded   bool
}

// replyOutputKind is the kind of one block the stream hands to the screen.
type replyOutputKind int

const (
	replyOutputMarkdown replyOutputKind = iota
	replyOutputCorrection
)

// replyOutput is one block for the scrollback.
type replyOutput struct {
	kind replyOutputKind
	text string
}

func newReplyStream() *replyStream {
	return &replyStream{
		groups:          map[replyGroupKey]*replyGroup{},
		reconciledTurns: map[string]bool{},
	}
}

// AppendDelta adds streamed text and returns the blocks it completed.
func (stream *replyStream) AppendDelta(delta sessionfeed.AssistantTextDelta) []replyOutput {
	if stream.reconciledTurns[delta.TurnID] {
		return nil
	}
	key := replyGroupKey{turnID: delta.TurnID, stepExecutionID: delta.StepExecutionID}
	group, exists := stream.groups[key]
	if !exists {
		group = &replyGroup{attempt: delta.Attempt}
		stream.groups[key] = group
	}
	if group.isRecorded || delta.Attempt < group.attempt {
		return nil
	}
	var outputs []replyOutput
	if !stream.hasActiveGroup || stream.activeGroupKey != key {
		outputs = append(outputs, stream.FlushPending()...)
		stream.activeGroupKey, stream.hasActiveGroup = key, true
	}
	if delta.Attempt > group.attempt {
		outputs = append(outputs, stream.FlushPending()...)
		if group.streamedText != "" {
			outputs = append(outputs, replyOutput{
				kind: replyOutputCorrection,
				text: fmt.Sprintf("retrying model call (attempt %d); text above is from attempt %d", delta.Attempt, group.attempt),
			})
		}
		group.attempt = delta.Attempt
		group.streamedText = ""
	}
	group.streamedText += delta.Text
	stream.pendingText += delta.Text
	return append(outputs, stream.takeCompletedBlocks()...)
}

// RecordFinalMessage reconciles a Step's final message with its streamed text
// and returns the blocks to print.
func (stream *replyStream) RecordFinalMessage(message sessionfeed.AssistantMessageRecorded) []replyOutput {
	if stream.reconciledTurns[message.TurnID] {
		return nil
	}
	key := replyGroupKey{turnID: message.TurnID, stepExecutionID: message.StepExecutionID}
	group, exists := stream.groups[key]
	if !exists {
		group = &replyGroup{attempt: 1}
		stream.groups[key] = group
	}
	if group.isRecorded {
		return nil
	}
	group.isRecorded = true
	isActive := stream.hasActiveGroup && stream.activeGroupKey == key
	var outputs []replyOutput
	if !isActive {
		outputs = append(outputs, stream.FlushPending()...)
	}
	streamedText := strings.TrimRight(group.streamedText, " \n")
	switch {
	case streamedText == "":
		stream.pendingText = message.Text
	case strings.HasPrefix(message.Text, streamedText):
		stream.pendingText += message.Text[len(streamedText):]
	default:
		outputs = append(outputs, stream.FlushPending()...)
		outputs = append(outputs, replyOutput{kind: replyOutputCorrection, text: "reply from the session record:"})
		stream.pendingText = message.Text
	}
	outputs = append(outputs, stream.FlushPending()...)
	stream.hasActiveGroup = false
	return outputs
}

// EndTurn flushes the pending text and drops later deltas of the turn.
func (stream *replyStream) EndTurn(turnID string) []replyOutput {
	outputs := stream.FlushPending()
	stream.reconciledTurns[turnID] = true
	for key := range stream.groups {
		if key.turnID == turnID {
			delete(stream.groups, key)
		}
	}
	stream.hasActiveGroup = false
	return outputs
}

// FlushPending returns the unfinished block as it is, for example before a
// tool row is printed.
func (stream *replyStream) FlushPending() []replyOutput {
	text := strings.Trim(stream.pendingText, "\n")
	stream.pendingText = ""
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []replyOutput{{kind: replyOutputMarkdown, text: text}}
}

// PendingText returns the unfinished block shown in the live area.
func (stream *replyStream) PendingText() string {
	return strings.Trim(stream.pendingText, "\n")
}

func (stream *replyStream) takeCompletedBlocks() []replyOutput {
	boundary := completedBlockBoundary(stream.pendingText)
	if boundary == 0 {
		return nil
	}
	completed := strings.Trim(stream.pendingText[:boundary], "\n")
	stream.pendingText = stream.pendingText[boundary:]
	if strings.TrimSpace(completed) == "" {
		return nil
	}
	return []replyOutput{{kind: replyOutputMarkdown, text: completed}}
}

// completedBlockBoundary returns the byte offset after the last finished
// markdown block in text: after a blank line outside a code fence, or after
// a closing fence. It returns 0 when no block is finished.
func completedBlockBoundary(text string) int {
	boundary := 0
	offset := 0
	isInFence := false
	fenceMarker := ""
	for {
		newline := strings.IndexByte(text[offset:], '\n')
		if newline < 0 {
			return boundary
		}
		line := text[offset : offset+newline]
		lineEnd := offset + newline + 1
		trimmed := strings.TrimSpace(line)
		switch {
		case isInFence:
			if strings.HasPrefix(trimmed, fenceMarker) && strings.Trim(trimmed, fenceMarker[:1]) == "" {
				isInFence = false
				boundary = lineEnd
			}
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			isInFence = true
			fenceMarker = trimmed[:3]
		case trimmed == "":
			boundary = lineEnd
		}
		offset = lineEnd
	}
}
