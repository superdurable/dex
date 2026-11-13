// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

// Rollover limits per commit, so one Step never moves a long 0.1.0 history at once.
const (
	// MessageRolloverMaxChunksPerCommit is the most AgentMessageChunks instances one Step writes.
	MessageRolloverMaxChunksPerCommit = 4
	// MessageRolloverMaxBytesPerCommit is the most chunk bytes one Step writes, unless its first
	// chunk alone is larger.
	MessageRolloverMaxBytesPerCommit = 8 * 1024 * 1024
	// chunkWrapperBytes is the size of {"messages":[ and ]} around a chunk's messages.
	chunkWrapperBytes = 15
)

// appendMessages stores messages after the last one, moves the oldest window messages into
// chunks, and writes the advanced AgentState. A Step that calls it must load the whole
// AgentMessages map.
//
// isTurnStart marks the user messages that start a turn, so TurnReplySequence records where the
// turn's replies begin. An append with no messages that starts no turn writes nothing else. The
// AgentMessageIndex is written only when a chunk is added or deleted. origins, nil or one per
// message, marks the messages the session wrote itself; senderNames, nil or one per message,
// holds their declared senders.
func (flow *AgentSessionFlow) appendMessages(
	ctx dex.Context,
	state AgentState,
	turnID string,
	messages []FrameworkMessage,
	isTurnStart bool,
	origins []*AgentMessageOrigin,
	senderNames []*string,
) (AgentState, error) {
	if origins != nil && len(origins) != len(messages) {
		panic("appendMessages: origins must be nil or have one value per message")
	}
	if senderNames != nil && len(senderNames) != len(messages) {
		panic("appendMessages: senderNames must be nil or have one value per message")
	}
	if len(messages) == 0 && !isTurnStart {
		return state, AgentStateAttribute.Set(ctx, state)
	}
	storedIndex, err := readOptionalAttribute(ctx, AgentMessageIndexAttribute)
	if err != nil {
		return state, err
	}
	index := AgentMessageIndex{Chunks: []AgentMessageChunkRange{}, FirstSequence: 1, WindowFirstSequence: 1}
	if storedIndex != nil {
		index = *storedIndex
		index.Chunks = append([]AgentMessageChunkRange{}, storedIndex.Chunks...)
	}
	if !isTurnStart && state.ActiveTurnID != nil {
		if err := recordActiveTurnReplySequence(ctx, storedIndex, state); err != nil {
			return state, err
		}
	}
	isIndexChanged := false
	sequence := state.NextMessageSequence
	createdAt := formatRFC3339(time.Now())
	for position, message := range messages {
		var origin *AgentMessageOrigin
		if origins != nil {
			origin = origins[position]
		}
		var senderName *string
		if senderNames != nil {
			senderName = senderNames[position]
		}
		stored := AgentMessage{
			Sequence:      sequence,
			TurnID:        turnID,
			Role:          message.Role,
			Text:          message.Text,
			ToolCallID:    message.ToolCallID,
			ToolName:      message.ToolName,
			ToolArguments: message.ToolArguments,
			ToolOutput:    message.ToolOutput,
			FrameworkItem: message.FrameworkItem,
			CreatedAt:     createdAt,
			Origin:        origin,
			SenderName:    senderName,
		}
		messageBytes, err := encodedMessageBytes(stored)
		if err != nil {
			return state, err
		}
		key := mustMessageSequenceKey(sequence)
		if chunkWrapperBytes+messageBytes > flow.options.messageChunkMaxBytes {
			// The first store writes the largest value this message needs, so a later rollover cannot fail on it.
			if err := AgentMessageChunksAttributeMap.Set(ctx, key, AgentMessageChunk{Messages: []AgentMessage{stored}}); err != nil {
				return state, err
			}
			index.Chunks = append(index.Chunks, AgentMessageChunkRange{FirstSequence: sequence, LastSequence: sequence})
			isIndexChanged = true
		} else if err := AgentMessagesAttributeMap.Set(ctx, key, stored); err != nil {
			return state, err
		}
		sequence++
	}
	state.NextMessageSequence = sequence
	if err := AgentStateAttribute.Set(ctx, state); err != nil {
		return state, err
	}
	if isTurnStart {
		if err := TurnReplySequenceAttribute.Set(ctx, TurnReplySequence{TurnID: turnID, Sequence: sequence}); err != nil {
			return state, err
		}
	}
	isRolledOver, err := flow.rollOverMessageWindow(ctx, &index, sequence)
	if err != nil {
		return state, err
	}
	if isIndexChanged || isRolledOver {
		if err := AgentMessageIndexAttribute.Set(ctx, index); err != nil {
			return state, err
		}
	}
	return state, nil
}

// recordActiveTurnReplySequence writes TurnReplySequence for the active turn when it names another
// turn, as for a turn a dex-agents 0.1.0 Worker started, before new messages can roll its start
// out of the window.
func recordActiveTurnReplySequence(ctx dex.Context, index *AgentMessageIndex, state AgentState) error {
	stored, err := readOptionalAttribute(ctx, TurnReplySequenceAttribute)
	if err != nil {
		return err
	}
	if stored != nil && stored.TurnID == *state.ActiveTurnID {
		return nil
	}
	reader := &storedMessageReader{ctx: ctx, index: index, chunks: map[int64]AgentMessageChunk{}}
	replySequence, err := scanTurnReplySequence(reader, state, *state.ActiveTurnID)
	if err != nil {
		return err
	}
	return TurnReplySequenceAttribute.Set(ctx, TurnReplySequence{TurnID: *state.ActiveTurnID, Sequence: replySequence})
}

// rollOverMessageWindow moves the oldest window messages into chunks while the window holds more
// than the window size, at most MessageRolloverMaxChunksPerCommit chunks and
// MessageRolloverMaxBytesPerCommit bytes per commit. It reports whether it moved any.
//
// A chunk takes consecutive window messages until it holds the message chunk size, the next
// message would carry it past the message chunk max bytes, or the next sequence is a direct chunk
// of an oversized message, which the window start then skips.
func (flow *AgentSessionFlow) rollOverMessageWindow(ctx dex.Context, index *AgentMessageIndex, nextSequence int64) (bool, error) {
	isMoved := skipDirectChunks(index)
	movedChunks := 0
	movedBytes := int64(0)
	for windowMessageCount(index, nextSequence) > flow.options.messageWindowSize &&
		movedChunks < MessageRolloverMaxChunksPerCommit {
		chunk, chunkBytes, err := flow.takeWindowChunk(ctx, index, nextSequence)
		if err != nil {
			return false, err
		}
		if movedChunks > 0 && movedBytes+chunkBytes > MessageRolloverMaxBytesPerCommit {
			break
		}
		for _, message := range chunk.Messages {
			if err := AgentMessagesAttributeMap.Delete(ctx, mustMessageSequenceKey(message.Sequence)); err != nil {
				return false, err
			}
		}
		if err := AgentMessageChunksAttributeMap.Set(ctx, mustMessageSequenceKey(index.WindowFirstSequence), chunk); err != nil {
			return false, err
		}
		lastSequence := index.WindowFirstSequence + int64(len(chunk.Messages)) - 1
		insertChunkRange(index, AgentMessageChunkRange{FirstSequence: index.WindowFirstSequence, LastSequence: lastSequence})
		index.WindowFirstSequence = lastSequence + 1
		skipDirectChunks(index)
		movedChunks++
		movedBytes += chunkBytes
		isMoved = true
	}
	if isMoved {
		index.FirstSequence = index.WindowFirstSequence
		if len(index.Chunks) > 0 && index.Chunks[0].FirstSequence < index.WindowFirstSequence {
			index.FirstSequence = index.Chunks[0].FirstSequence
		}
	}
	return isMoved, nil
}

// skipDirectChunks advances the window start past direct chunks that begin at it, and reports
// whether it moved.
func skipDirectChunks(index *AgentMessageIndex) bool {
	isMoved := false
	for {
		chunkRange, isFound := findChunkRange(index.Chunks, index.WindowFirstSequence)
		if !isFound {
			return isMoved
		}
		index.WindowFirstSequence = chunkRange.LastSequence + 1
		isMoved = true
	}
}

// windowMessageCount counts the messages in AgentMessages: the window's sequences without the
// direct chunks among them.
func windowMessageCount(index *AgentMessageIndex, nextSequence int64) int64 {
	count := nextSequence - index.WindowFirstSequence
	for _, chunkRange := range index.Chunks {
		if chunkRange.FirstSequence >= index.WindowFirstSequence {
			count -= chunkRange.LastSequence - chunkRange.FirstSequence + 1
		}
	}
	return count
}

func insertChunkRange(index *AgentMessageIndex, chunkRange AgentMessageChunkRange) {
	position := sort.Search(len(index.Chunks), func(i int) bool {
		return index.Chunks[i].FirstSequence > chunkRange.FirstSequence
	})
	index.Chunks = append(index.Chunks, AgentMessageChunkRange{})
	copy(index.Chunks[position+1:], index.Chunks[position:])
	index.Chunks[position] = chunkRange
}

// takeWindowChunk reads the next chunk's messages from the window, starting at the window start,
// and returns the chunk and its encoded size.
func (flow *AgentSessionFlow) takeWindowChunk(ctx dex.Context, index *AgentMessageIndex, nextSequence int64) (AgentMessageChunk, int64, error) {
	chunk := AgentMessageChunk{Messages: []AgentMessage{}}
	chunkBytes := int64(chunkWrapperBytes)
	for sequence := index.WindowFirstSequence; sequence < nextSequence && int64(len(chunk.Messages)) < flow.options.messageChunkSize; sequence++ {
		if _, isDirect := findChunkRange(index.Chunks, sequence); isDirect {
			break
		}
		message, err := AgentMessagesAttributeMap.Get(ctx, mustMessageSequenceKey(sequence))
		if err != nil {
			return AgentMessageChunk{}, 0, err
		}
		messageBytes, err := encodedMessageBytes(message)
		if err != nil {
			return AgentMessageChunk{}, 0, err
		}
		withMessage := chunkBytes + messageBytes
		if len(chunk.Messages) > 0 {
			withMessage++
			if withMessage > flow.options.messageChunkMaxBytes {
				break
			}
		}
		chunk.Messages = append(chunk.Messages, message)
		chunkBytes = withMessage
	}
	return chunk, chunkBytes, nil
}

func encodedMessageBytes(message AgentMessage) (int64, error) {
	encoded, err := json.Marshal(message)
	if err != nil {
		return 0, err
	}
	return int64(len(encoded)), nil
}

// storedMessageReader reads stored messages from the window and the loaded archive chunks.
type storedMessageReader struct {
	ctx    dex.Context
	index  *AgentMessageIndex
	chunks map[int64]AgentMessageChunk
}

func newStoredMessageReader(ctx dex.Context) (*storedMessageReader, error) {
	index, err := readOptionalAttribute(ctx, AgentMessageIndexAttribute)
	if err != nil {
		return nil, err
	}
	return &storedMessageReader{ctx: ctx, index: index, chunks: map[int64]AgentMessageChunk{}}, nil
}

// firstSequence returns the sequence of the oldest stored message.
func (reader *storedMessageReader) firstSequence() int64 {
	if reader.index == nil {
		return 1
	}
	return reader.index.FirstSequence
}

// readMessages returns messages with sequences in [start, end), starting no earlier than the oldest
// stored message.
func (reader *storedMessageReader) readMessages(start int64, end int64) ([]AgentMessage, error) {
	messages := []AgentMessage{}
	for sequence := max(reader.firstSequence(), start); sequence < end; sequence++ {
		message, err := reader.readMessage(sequence)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func (reader *storedMessageReader) readMessage(sequence int64) (AgentMessage, error) {
	if reader.index == nil {
		return AgentMessagesAttributeMap.Get(reader.ctx, mustMessageSequenceKey(sequence))
	}
	chunkRange, isFound := findChunkRange(reader.index.Chunks, sequence)
	if !isFound {
		if sequence >= reader.index.WindowFirstSequence {
			return AgentMessagesAttributeMap.Get(reader.ctx, mustMessageSequenceKey(sequence))
		}
		return AgentMessage{}, fmt.Errorf("AgentMessageIndex lists no chunk that holds message %d", sequence)
	}
	chunk, isRead := reader.chunks[chunkRange.FirstSequence]
	if !isRead {
		key := mustMessageSequenceKey(chunkRange.FirstSequence)
		var err error
		chunk, err = AgentMessageChunksAttributeMap.Get(reader.ctx, key)
		var notLoaded *dex.AttributeMapNotLoadedError
		if errors.As(err, &notLoaded) {
			return AgentMessage{}, fmt.Errorf("AgentMessageChunks instance %s is not loaded: %w", key, errChunkNotLoaded)
		}
		if err != nil {
			return AgentMessage{}, err
		}
		if !isChunkOfRange(chunk, chunkRange) {
			return AgentMessage{}, fmt.Errorf("AgentMessageChunks instance %s does not hold messages %d through %d",
				key, chunkRange.FirstSequence, chunkRange.LastSequence)
		}
		reader.chunks[chunkRange.FirstSequence] = chunk
	}
	return chunk.Messages[sequence-chunkRange.FirstSequence], nil
}

// findChunkRange returns the kept chunk that holds the sequence.
func findChunkRange(chunks []AgentMessageChunkRange, sequence int64) (AgentMessageChunkRange, bool) {
	position := sort.Search(len(chunks), func(i int) bool { return chunks[i].LastSequence >= sequence })
	if position == len(chunks) || chunks[position].FirstSequence > sequence {
		return AgentMessageChunkRange{}, false
	}
	return chunks[position], true
}

func isChunkOfRange(chunk AgentMessageChunk, chunkRange AgentMessageChunkRange) bool {
	if int64(len(chunk.Messages)) != chunkRange.LastSequence-chunkRange.FirstSequence+1 {
		return false
	}
	for offset, message := range chunk.Messages {
		if message.Sequence != chunkRange.FirstSequence+int64(offset) {
			return false
		}
	}
	return true
}

// readStoredMessages returns stored messages with sequences in [start, end); the Step or RPC must
// load AgentMessages and the AgentMessageChunks instances that hold them.
func readStoredMessages(ctx dex.Context, start int64, end int64) ([]AgentMessage, error) {
	reader, err := newStoredMessageReader(ctx)
	if err != nil {
		return nil, err
	}
	return reader.readMessages(start, end)
}

// errChunkNotLoaded marks a read of an AgentMessageChunks instance the Step did not load.
var errChunkNotLoaded = errors.New("chunk not loaded")

// countMessagesAfterUserMessages counts the turn's stored messages after the user messages that started it.
func countMessagesAfterUserMessages(ctx dex.Context, state AgentState, turnID string) (int64, error) {
	stored, err := readOptionalAttribute(ctx, TurnReplySequenceAttribute)
	if err != nil {
		return 0, err
	}
	if stored != nil && stored.TurnID == turnID {
		return state.NextMessageSequence - stored.Sequence, nil
	}
	// A turn that a dex-agents 0.1.0 Worker started, before this release stored a message in it.
	reader, err := newStoredMessageReader(ctx)
	if err != nil {
		return 0, err
	}
	replySequence, err := scanTurnReplySequence(reader, state, turnID)
	if err != nil {
		return 0, err
	}
	return state.NextMessageSequence - replySequence, nil
}

// scanTurnReplySequence reads the turn's messages back from the newest and returns the first
// sequence after its leading user messages. It stops at another turn's message, the oldest
// stored message, or a chunk the Step did not load.
func scanTurnReplySequence(reader *storedMessageReader, state AgentState, turnID string) (int64, error) {
	var turnMessages []AgentMessage
	for sequence := state.NextMessageSequence - 1; sequence >= reader.firstSequence(); sequence-- {
		message, err := reader.readMessage(sequence)
		if errors.Is(err, errChunkNotLoaded) {
			break
		}
		if err != nil {
			return 0, err
		}
		if message.TurnID != turnID {
			break
		}
		turnMessages = append([]AgentMessage{message}, turnMessages...)
	}
	for _, message := range turnMessages {
		if message.Role != MessageRoleUser {
			return message.Sequence, nil
		}
	}
	return state.NextMessageSequence, nil
}
