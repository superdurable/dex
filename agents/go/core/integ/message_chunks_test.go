// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/sdk-go/dex"
)

// Many rollovers move the oldest messages into chunks of two. The Worker dies inside the
// RunModel whose commit rolls over; its retry commits the rollover once, and every later
// model call still sees the whole conversation.
func TestMessagesRollOverIntoChunksAcrossAWorkerKill(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	worker := stack.startWorker()
	sessions := stack.sessions(chunkedAgentFlowType)
	ctx := context.Background()
	const sessionID = "rollover"
	const sayTurns = 12

	_, err := sessions.StartSession(ctx, sessionID, ptr("say 1"), dex.StartFlowOptions{})
	require.NoError(t, err)
	for turn := 2; turn <= sayTurns; turn++ {
		stack.sendUserMessage(chunkedAgentFlowType, sessionID, fmt.Sprintf("say %d", turn))
	}
	stack.waitForTurnCount(chunkedAgentFlowType, sessionID, sayTurns)
	// With a window of 5 and chunks of 2, storing an assistant reply rolls over, so this RunModel does.
	stack.sendUserMessage(chunkedAgentFlowType, sessionID, "think slowly")
	stack.waitForMarker("model-attempt-1")
	worker.Kill()
	stack.startWorker()
	stack.waitForTurnCount(chunkedAgentFlowType, sessionID, sayTurns+1)
	stack.sendUserMessage(chunkedAgentFlowType, sessionID, "count user texts")
	snapshot := stack.waitForTurnCount(chunkedAgentFlowType, sessionID, sayTurns+2)

	const messageCount = 2 * (sayTurns + 2)
	messages := stack.listAllMessages(chunkedAgentFlowType, sessionID, 3)
	require.Len(t, messages, messageCount)
	for index, message := range messages {
		require.Equal(t, int64(index+1), message.Sequence)
		require.Equal(t, fmt.Sprintf("turn-%d", index/2+1), message.TurnID)
	}
	require.Equal(t, "You said: say 7", *messages[13].Text)
	require.Equal(t, "answered on attempt 2", *messages[2*sayTurns+1].Text)
	require.Equal(t, fmt.Sprintf("I see %d user texts", sayTurns+2), *messages[messageCount-1].Text)
	require.Equal(t, messages[messageCount-snapshotMessages(messageCount):], snapshot.Messages)
	for _, entry := range stack.stepHistory(sessionID) {
		if entry.StepType == "RunModel" && entry.EventType == "StepExecuteCompleted" && entry.FinalAttempt > 1 {
			require.Equal(t, 2, entry.FinalAttempt)
		}
	}

	index := stack.requireMessageIndex(chunkedAgentFlowType, sessionID)
	require.Equal(t, dexagents.AgentMessageIndex{
		Chunks: chunkRanges(1, messageCount-4, chunkedChunkSize), FirstSequence: 1,
		WindowFirstSequence: messageCount - 3,
	}, index)
	require.Equal(t, dexagents.TurnReplySequence{TurnID: fmt.Sprintf("turn-%d", sayTurns+2), Sequence: messageCount},
		stack.requireTurnReplySequence(sessionID))
	stack.requireStoredLayout(chunkedAgentFlowType, sessionID, index, messages)
}

// A Step writes AgentMessageIndex only when it adds or deletes a chunk, so an ordinary turn that
// rolls nothing over leaves the index, with its growing chunk list, untouched.
func TestTheIndexIsWrittenOnlyWhenAChunkChanges(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(chunkedAgentFlowType)
	const sessionID = "index-writes"
	const turns = 12

	_, err := sessions.StartSession(context.Background(), sessionID, ptr("say 1"), dex.StartFlowOptions{})
	require.NoError(t, err)
	for turn := 2; turn <= turns; turn++ {
		stack.sendUserMessage(chunkedAgentFlowType, sessionID, fmt.Sprintf("say %d", turn))
	}
	stack.waitForTurnCount(chunkedAgentFlowType, sessionID, turns)

	indexWrites := 0
	turnReplyWrites := 0
	for _, keys := range stack.writtenAttributeKeysByStep(sessionID) {
		writesIndex := slices.Contains(keys, "AgentMessageIndex")
		writesChunk := slices.ContainsFunc(keys, func(key string) bool { return strings.HasPrefix(key, "AgentMessageChunks/") })
		require.Equal(t, writesChunk, writesIndex, "a Step writes the index exactly when it writes a chunk: %v", keys)
		if writesIndex {
			indexWrites++
		}
		if slices.Contains(keys, "TurnReplySequence") {
			turnReplyWrites++
		}
	}
	// A window of five and chunks of two roll over on every other message once the window fills.
	require.Positive(t, indexWrites)
	require.Less(t, indexWrites, 2*turns)
	require.Equal(t, turns, turnReplyWrites, "every turn start records where its replies begin")
}

// A chunk stops taking messages at the byte cap before it reaches the chunk size, and a message
// larger than the cap is archived alone. Every page and the snapshot still read across them.
func TestChunksStopAtTheByteCapAndHoldAnOversizeMessageAlone(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(chunkedBytesAgentFlowType)
	ctx := context.Background()
	const sessionID = "byte-cap"
	commands := []string{"pad 700", "pad 700", "pad 3000", "pad 10", "pad 10", "pad 700", "pad 10", "pad 10", "pad 10", "pad 10"}

	_, err := sessions.StartSession(ctx, sessionID, ptr(commands[0]), dex.StartFlowOptions{})
	require.NoError(t, err)
	for _, command := range commands[1:] {
		stack.sendUserMessage(chunkedBytesAgentFlowType, sessionID, command)
	}
	snapshot := stack.waitForTurnCount(chunkedBytesAgentFlowType, sessionID, int64(len(commands)))
	messages := stack.listAllMessages(chunkedBytesAgentFlowType, sessionID, 3)
	require.Len(t, messages, 2*len(commands))
	require.Equal(t, snapshot.Messages, messages)
	require.Equal(t, strings.Repeat("x", 3000), *messages[5].Text)

	index := stack.requireMessageIndex(chunkedBytesAgentFlowType, sessionID)
	stack.requireStoredLayout(chunkedBytesAgentFlowType, sessionID, index, messages)
	hasShortChunk := false
	hasOversizeChunk := false
	for _, chunkRange := range index.Chunks {
		count := chunkRange.LastSequence - chunkRange.FirstSequence + 1
		require.LessOrEqual(t, count, int64(chunkedBytesChunkSize))
		encoded, err := json.Marshal(dexagents.AgentMessageChunk{
			Messages: messages[chunkRange.FirstSequence-1 : chunkRange.LastSequence],
		})
		require.NoError(t, err)
		if len(encoded) > chunkedBytesMaxBytes {
			require.Equal(t, int64(1), count, "only a message over the cap by itself makes a larger chunk")
			hasOversizeChunk = true
		} else if count < chunkedBytesChunkSize {
			hasShortChunk = true
		}
	}
	require.True(t, hasOversizeChunk, "the 3,000-character answer is archived alone")
	require.True(t, hasShortChunk, "the byte cap closes a chunk before the chunk size")
	// The oversized answer went straight into its own chunk; no AgentMessages instance ever held it.
	oversizedKey := mustSequenceKey(t, 6)
	isChunkWritten := false
	for _, keys := range stack.writtenAttributeKeysByStep(sessionID) {
		require.NotContains(t, keys, "AgentMessages/"+oversizedKey)
		isChunkWritten = isChunkWritten || slices.Contains(keys, "AgentMessageChunks/"+oversizedKey)
	}
	require.True(t, isChunkWritten)
}

// A message whose single-message chunk would exceed values.maxValueBytes fails the commit that
// first stores it. The turn fails clearly, and the session keeps working: no later rollover can
// meet that message. Before direct chunks it fit in the window and wedged the next rollover.
func TestAMessageTooLargeForItsOwnChunkFailsWhenFirstStored(t *testing.T) {
	t.Parallel()
	const maxValueBytes = 8192
	stack := newAgentTestStackWithServerZDConfig(t, fmt.Sprintf("values:\n  maxValueBytes: %d\n  blobDbMinValueBytes: 4096\n", maxValueBytes))
	stack.startWorker()
	sessions := stack.sessions(chunkedBytesAgentFlowType)
	ctx := context.Background()

	// The answer's encoded size grows by a fixed number of bytes per character of padding.
	sizes := map[int]int{}
	for _, characters := range []int{1000, 1100} {
		sessionID := fmt.Sprintf("calibrate-%d", characters)
		_, err := sessions.StartSession(ctx, sessionID, ptr(fmt.Sprintf("pad %d", characters)), dex.StartFlowOptions{})
		require.NoError(t, err)
		snapshot := stack.waitForTurnCount(chunkedBytesAgentFlowType, sessionID, 1)
		encoded, err := json.Marshal(snapshot.Messages[1])
		require.NoError(t, err)
		sizes[characters] = len(encoded)
	}
	bytesPerCharacter := (sizes[1100] - sizes[1000]) / 100
	require.Equal(t, 100*bytesPerCharacter, sizes[1100]-sizes[1000])
	// In the old window this answer's value fit; alone in a chunk, 15 bytes larger, it does not.
	targetBytes := maxValueBytes - 20
	characters := 1000 + (targetBytes-sizes[1000])/bytesPerCharacter
	const sessionID = "edge"
	_, err := sessions.StartSession(ctx, sessionID, ptr(fmt.Sprintf("pad %d", characters)), dex.StartFlowOptions{})
	require.NoError(t, err)
	failed := stack.waitForSnapshot(chunkedBytesAgentFlowType, sessionID, "the failed first turn",
		func(snapshot dexagents.AgentSnapshot) bool {
			return snapshot.State != nil && snapshot.State.TurnCount == 1 && snapshot.State.ActiveTurnID == nil
		})
	require.NotNil(t, failed.State.LastTurnError)
	require.Contains(t, *failed.State.LastTurnError, "exceeds")
	require.Len(t, failed.Messages, 1, "the oversized answer was never stored")

	stack.sendUserMessage(chunkedBytesAgentFlowType, sessionID, "say after")
	snapshot := stack.waitForTurnCount(chunkedBytesAgentFlowType, sessionID, 2)
	require.Nil(t, snapshot.State.LastTurnError)
	require.Equal(t, "You said: say after", *snapshot.Messages[len(snapshot.Messages)-1].Text)
}

// Pages of every size walk back across window and chunk boundaries and return each message once.
func TestListAgentMessagesPagesAcrossChunkBoundaries(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(chunkedAgentFlowType)
	ctx := context.Background()
	const sessionID = "paging"
	const turns = 9

	_, err := sessions.StartSession(ctx, sessionID, ptr("say 1"), dex.StartFlowOptions{})
	require.NoError(t, err)
	for turn := 2; turn <= turns; turn++ {
		stack.sendUserMessage(chunkedAgentFlowType, sessionID, fmt.Sprintf("say %d", turn))
	}
	snapshot := stack.waitForTurnCount(chunkedAgentFlowType, sessionID, turns)
	index := stack.requireMessageIndex(chunkedAgentFlowType, sessionID)
	require.Equal(t, int64(2*turns-3), index.WindowFirstSequence)
	require.Len(t, snapshot.Messages, 2*turns)

	for _, limit := range []int64{1, 2, 3, 4, 5, 7, 200} {
		messages := stack.listAllMessages(chunkedAgentFlowType, sessionID, limit)
		require.Len(t, messages, 2*turns, "limit %d", limit)
		for position, message := range messages {
			require.Equal(t, int64(position+1), message.Sequence, "limit %d", limit)
		}
		require.Equal(t, snapshot.Messages, messages)
	}
	page, err := sessions.ListAgentMessages(ctx, sessionID, ptr(index.WindowFirstSequence+1), 3)
	require.NoError(t, err)
	require.Equal(t, []int64{index.WindowFirstSequence - 2, index.WindowFirstSequence - 1, index.WindowFirstSequence},
		sequencesOf(page.Messages))
	require.Equal(t, ptr(index.WindowFirstSequence-2), page.NextBeforeSequence)
	page, err = sessions.ListAgentMessages(ctx, sessionID, ptr(int64(1)), 3)
	require.NoError(t, err)
	require.Empty(t, page.Messages)
	require.Nil(t, page.NextBeforeSequence)
	require.Nil(t, page.DeletedBeforeSequence, "nothing is deleted without compaction")
}

// A session in the layout dex-agents 0.1.0 wrote, every message its own AgentMessages instance
// and no AgentMessageIndex, continues here. Each Step that stores a message moves at most four
// chunks, so a long history migrates over several commits, and reads stay whole meanwhile.
func TestA010SessionMovesIntoChunksOverSeveralCommits(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(chunkedAgentFlowType)
	ctx := context.Background()
	const turns = 25

	// A source session supplies real stored messages to copy into the 0.1.0 layout.
	_, err := sessions.StartSession(ctx, "source", ptr("say 1"), dex.StartFlowOptions{})
	require.NoError(t, err)
	for turn := 2; turn <= turns; turn++ {
		stack.sendUserMessage(chunkedAgentFlowType, "source", fmt.Sprintf("say %d", turn))
	}
	source := stack.waitForTurnCount(chunkedAgentFlowType, "source", turns)
	_, err = sessions.StartSession(ctx, "released", nil, dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForSnapshot(chunkedAgentFlowType, "released", "a started session", func(snapshot dexagents.AgentSnapshot) bool {
		return snapshot.State != nil
	})
	_, isSet := stack.readMessageIndex(chunkedAgentFlowType, "released")
	require.False(t, isSet, "a session without messages has no index yet")
	legacy := map[string]any{"AgentState": dexagents.AgentState{
		Status: dexagents.AgentSessionStatusWaitingForUserMessage, TurnCount: turns,
		NextMessageSequence: 2*turns + 1, InteractionMode: dexagents.InteractionModeChat,
	}}
	for _, message := range source.Messages {
		legacy["AgentMessages/"+mustSequenceKey(t, message.Sequence)] = message
	}
	stack.setAttributesWithDexcli("released", legacy)
	before := stack.listAllMessages(chunkedAgentFlowType, "released", 4)
	require.Equal(t, source.Messages, before, "this release reads the 0.1.0 layout")

	// AwaitUserMessage and RunModel each store a message and move four chunks of two.
	stack.sendUserMessage(chunkedAgentFlowType, "released", "count user texts")
	snapshot := stack.waitForTurnCount(chunkedAgentFlowType, "released", turns+1)
	require.Equal(t, fmt.Sprintf("I see %d user texts", turns+1), *snapshot.Messages[2*turns+1].Text)
	halfMoved := stack.requireMessageIndex(chunkedAgentFlowType, "released")
	require.Equal(t, dexagents.AgentMessageIndex{
		Chunks: chunkRanges(1, 16, chunkedChunkSize), FirstSequence: 1, WindowFirstSequence: 17,
	}, halfMoved)
	require.Equal(t, dexagents.TurnReplySequence{TurnID: fmt.Sprintf("turn-%d", turns+1), Sequence: 2*turns + 2},
		stack.requireTurnReplySequence("released"))
	messages := stack.listAllMessages(chunkedAgentFlowType, "released", 3)
	require.Equal(t, source.Messages, messages[:2*turns])
	stack.requireStoredLayout(chunkedAgentFlowType, "released", halfMoved, messages)
	completed, err := stack.firstEventPayload(chunkedAgentFlowType, "released", string(dexagents.AgentEventTypeTurnCompleted))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"message_count": float64(1)}, completed)

	for turn := turns + 2; turn <= turns+4; turn++ {
		stack.sendUserMessage(chunkedAgentFlowType, "released", "count user texts")
		snapshot = stack.waitForTurnCount(chunkedAgentFlowType, "released", int64(turn))
		require.Equal(t, fmt.Sprintf("I see %d user texts", turn), *snapshot.Messages[len(snapshot.Messages)-1].Text)
	}
	migrated := stack.requireMessageIndex(chunkedAgentFlowType, "released")
	require.LessOrEqual(t, snapshot.State.NextMessageSequence-migrated.WindowFirstSequence, int64(chunkedWindowSize))
	messages = stack.listAllMessages(chunkedAgentFlowType, "released", 7)
	require.Len(t, messages, 2*(turns+4))
	require.Equal(t, source.Messages, messages[:2*turns])
	stack.requireStoredLayout(chunkedAgentFlowType, "released", migrated, messages)
}

// A long session without compaction keeps at most the window in AgentMessages after every turn;
// every older message stays in a chunk, because retention needs a context summary.
func TestTheMessageWindowStaysBoundedOverALongSession(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(chunkedAgentFlowType)
	ctx := context.Background()
	const sessionID = "long"
	const turns = 60

	_, err := sessions.StartSession(ctx, sessionID, nil, dex.StartFlowOptions{})
	require.NoError(t, err)
	largestWindow := int64(0)
	for turn := 1; turn <= turns; turn++ {
		stack.sendUserMessage(chunkedAgentFlowType, sessionID, fmt.Sprintf("say %d", turn))
		snapshot := stack.waitForTurnCount(chunkedAgentFlowType, sessionID, int64(turn))
		// No index yet means no chunk yet: every message is in the window.
		index, isSet := stack.readMessageIndex(chunkedAgentFlowType, sessionID)
		if !isSet {
			index = dexagents.AgentMessageIndex{FirstSequence: 1, WindowFirstSequence: 1}
		}
		window := snapshot.State.NextMessageSequence - index.WindowFirstSequence
		largestWindow = max(largestWindow, window)
		require.LessOrEqual(t, window, int64(chunkedWindowSize))
		require.Equal(t, int64(1), index.FirstSequence, "no chunk is deleted without compaction")
	}
	messages := stack.listAllMessages(chunkedAgentFlowType, sessionID, 200)
	require.Len(t, messages, 2*turns)
	index := stack.requireMessageIndex(chunkedAgentFlowType, sessionID)
	stack.requireStoredLayout(chunkedAgentFlowType, sessionID, index, messages)
	t.Logf("long session: %d turns, %d messages, %d archive chunks, largest window %d messages",
		turns, len(messages), len(index.Chunks), largestWindow)
}

func (stack *agentTestStack) sendUserMessage(flowType string, sessionID string, text string) {
	stack.t.Helper()
	isQueued, err := stack.sessions(flowType).SendUserMessage(context.Background(), sessionID, text)
	require.NoError(stack.t, err)
	require.True(stack.t, isQueued)
}

// listAllMessages walks list_agent_messages back from the newest page and returns every message.
func (stack *agentTestStack) listAllMessages(flowType string, sessionID string, limit int64) []dexagents.AgentMessage {
	stack.t.Helper()
	var messages []dexagents.AgentMessage
	var before *int64
	for {
		page, err := stack.sessions(flowType).ListAgentMessages(context.Background(), sessionID, before, limit)
		require.NoError(stack.t, err)
		require.LessOrEqual(stack.t, int64(len(page.Messages)), limit)
		messages = append(page.Messages, messages...)
		if page.NextBeforeSequence == nil {
			return messages
		}
		require.Equal(stack.t, page.Messages[0].Sequence, *page.NextBeforeSequence)
		before = page.NextBeforeSequence
	}
}

func (stack *agentTestStack) readMessageIndex(flowType string, sessionID string) (dexagents.AgentMessageIndex, bool) {
	stack.t.Helper()
	stack.requireFlow(flowType)
	var index dexagents.AgentMessageIndex
	result, err := stack.client.GetAttributes(context.Background(), sessionID,
		dex.ReadAttribute(dexagents.AgentMessageIndexAttribute, &index))
	require.NoError(stack.t, err)
	return index, result.IsSet(dexagents.AgentMessageIndexAttribute)
}

func (stack *agentTestStack) requireMessageIndex(flowType string, sessionID string) dexagents.AgentMessageIndex {
	stack.t.Helper()
	index, isSet := stack.readMessageIndex(flowType, sessionID)
	require.True(stack.t, isSet, "session %s has no AgentMessageIndex", sessionID)
	return index
}

// requireStoredLayout checks that every message lives exactly where the index says.
func (stack *agentTestStack) requireStoredLayout(
	flowType string,
	sessionID string,
	index dexagents.AgentMessageIndex,
	messages []dexagents.AgentMessage,
) {
	stack.t.Helper()
	stack.requireFlow(flowType)
	ctx := context.Background()
	for _, message := range messages {
		key := mustSequenceKey(stack.t, message.Sequence)
		var stored dexagents.AgentMessage
		result, err := stack.client.GetAttributes(ctx, sessionID,
			dex.ReadAttributeMapInstance(dexagents.AgentMessagesAttributeMap, key, &stored))
		require.NoError(stack.t, err)
		isInChunk := slices.ContainsFunc(index.Chunks, func(chunkRange dexagents.AgentMessageChunkRange) bool {
			return chunkRange.FirstSequence <= message.Sequence && message.Sequence <= chunkRange.LastSequence
		})
		isInWindow := message.Sequence >= index.WindowFirstSequence && !isInChunk
		require.Equal(stack.t, isInWindow, result.IsMapInstanceSet(dexagents.AgentMessagesAttributeMap, key),
			"AgentMessages instance %s", key)
		if isInWindow {
			require.Equal(stack.t, message, stored)
		}
	}
	nextFirst := index.FirstSequence
	for _, chunkRange := range index.Chunks {
		if chunkRange.FirstSequence >= index.WindowFirstSequence {
			require.Equal(stack.t, chunkRange.FirstSequence, chunkRange.LastSequence, "a chunk in the window holds one oversized message")
		} else {
			require.Equal(stack.t, nextFirst, chunkRange.FirstSequence, "archived chunks are contiguous")
			nextFirst = chunkRange.LastSequence + 1
		}
		require.LessOrEqual(stack.t, chunkRange.FirstSequence, chunkRange.LastSequence)
		key := mustSequenceKey(stack.t, chunkRange.FirstSequence)
		var chunk dexagents.AgentMessageChunk
		result, err := stack.client.GetAttributes(ctx, sessionID,
			dex.ReadAttributeMapInstance(dexagents.AgentMessageChunksAttributeMap, key, &chunk))
		require.NoError(stack.t, err)
		require.True(stack.t, result.IsMapInstanceSet(dexagents.AgentMessageChunksAttributeMap, key), "chunk %s", key)
		require.Equal(stack.t, messages[chunkRange.FirstSequence-1:chunkRange.LastSequence], chunk.Messages, "chunk %s", key)
	}
	require.Equal(stack.t, index.WindowFirstSequence, nextFirst, "the last chunk ends before the window")
}

func (stack *agentTestStack) requireTurnReplySequence(sessionID string) dexagents.TurnReplySequence {
	stack.t.Helper()
	var replySequence dexagents.TurnReplySequence
	result, err := stack.client.GetAttributes(context.Background(), sessionID,
		dex.ReadAttribute(dexagents.TurnReplySequenceAttribute, &replySequence))
	require.NoError(stack.t, err)
	require.True(stack.t, result.IsSet(dexagents.TurnReplySequenceAttribute), "session %s has no TurnReplySequence", sessionID)
	return replySequence
}

// writtenAttributeKeysByStep returns the Attribute keys each Step Execute of the session wrote or
// deleted, from dexcli flow history.
func (stack *agentTestStack) writtenAttributeKeysByStep(sessionID string) [][]string {
	stack.t.Helper()
	output, err := exec.Command(stack.engine.Dexcli(), "flow", "history", sessionID,
		"--server", stack.engine.Address(), "--all", "--output", "json").Output()
	require.NoError(stack.t, err)
	var history struct {
		Events []struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		} `json:"events"`
	}
	require.NoError(stack.t, json.Unmarshal(output, &history))
	keysByStep := [][]string{}
	for _, event := range history.Events {
		if event.Type != "StepExecuteCompleted" {
			continue
		}
		var payload struct {
			Output struct {
				UpsertAttributes []struct {
					Key string `json:"key"`
				} `json:"upsertAttributes"`
			} `json:"output"`
		}
		require.NoError(stack.t, json.Unmarshal(event.Payload, &payload))
		keys := []string{}
		for _, attribute := range payload.Output.UpsertAttributes {
			keys = append(keys, attribute.Key)
		}
		keysByStep = append(keysByStep, keys)
	}
	return keysByStep
}

func (stack *agentTestStack) requireFlow(flowType string) {
	stack.t.Helper()
	_, isKnown := stack.flows[flowType]
	require.True(stack.t, isKnown, flowType)
}

// setAttributesWithDexcli writes Attribute values as a Client would, with dexcli's SetAttributes call.
func (stack *agentTestStack) setAttributesWithDexcli(sessionID string, values map[string]any) {
	stack.t.Helper()
	attributes := make([]map[string]any, 0, len(values))
	for key, value := range values {
		encoded, err := json.Marshal(value)
		require.NoError(stack.t, err)
		attributes = append(attributes, map[string]any{"key": key, "value": map[string]any{
			"objValue": map[string]any{"encoding": "json", "payload": base64.StdEncoding.EncodeToString(encoded)},
		}})
	}
	request, err := json.Marshal(map[string]any{
		"flowId": sessionID, "requestId": rand.Text(), "attributes": attributes,
	})
	require.NoError(stack.t, err)
	output, err := exec.Command(stack.engine.Dexcli(), "api", "call", "SetAttributes",
		"--server", stack.engine.Address(), "--yes", "--data", string(request)).CombinedOutput()
	require.NoError(stack.t, err, string(output))
}

// firstEventPayload returns the payload of the session's first AgentEvents event of eventType.
func (stack *agentTestStack) firstEventPayload(flowType string, sessionID string, eventType string) (map[string]any, error) {
	sessions := stack.sessions(flowType)
	resumeToken := ""
	for {
		readContext, cancel := context.WithTimeout(context.Background(), sessionConvergenceTimeout)
		event, message, err := sessions.ReadAgentEvent(readContext, sessionID, resumeToken)
		cancel()
		if err != nil {
			return nil, err
		}
		resumeToken = message.ResumeToken
		if event.Type == eventType {
			var payload map[string]any
			return payload, json.Unmarshal([]byte(event.Payload), &payload)
		}
	}
}

func mustSequenceKey(t *testing.T, sequence int64) string {
	t.Helper()
	key, err := dexagents.MessageSequenceKey(sequence)
	require.NoError(t, err)
	return key
}

func snapshotMessages(messageCount int) int {
	return min(messageCount, 200)
}

// chunkRanges returns the ranges of chunks of chunkSize messages from first through last.
func chunkRanges(first int64, last int64, chunkSize int64) []dexagents.AgentMessageChunkRange {
	ranges := []dexagents.AgentMessageChunkRange{}
	for start := first; start <= last; start += chunkSize {
		ranges = append(ranges, dexagents.AgentMessageChunkRange{FirstSequence: start, LastSequence: start + chunkSize - 1})
	}
	return ranges
}

func sequencesOf(messages []dexagents.AgentMessage) []int64 {
	sequences := make([]int64, 0, len(messages))
	for _, message := range messages {
		sequences = append(sequences, message.Sequence)
	}
	return sequences
}
