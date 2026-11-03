// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import "fmt"

// AgentEventsStreamName is the name of every agent session Flow type's event Stream.
const AgentEventsStreamName = "AgentEvents"

// DefaultAgentEventsCapacityBytes is the default AgentEvents Stream capacity: 10 MiB.
const DefaultAgentEventsCapacityBytes int64 = 10 * 1024 * 1024

// MessageSequenceKey returns the AgentMessages or AgentMessageChunks map instance for a message sequence.
//
// A message in the window is the AgentMessages instance of its own sequence.
// An archived chunk is the AgentMessageChunks instance of its first message's
// sequence; AgentMessageIndex says which messages are in the window. Prefer
// AgentSessionClient.ListAgentMessages, which reads both. Use it to read one
// message of the window with dex.ReadAttributeMapInstance:
//
//	key, err := dexagents.MessageSequenceKey(7)
//	if err != nil {
//		return err
//	}
//	var message dexagents.AgentMessage
//	_, err = client.GetAttributes(ctx, "session-42",
//		dex.ReadAttributeMapInstance(dexagents.AgentMessagesAttributeMap, key, &message))
//
// It returns the sequence as a 12-digit zero-padded decimal, such as
// "000000000007", and an error when sequence is not positive.
func MessageSequenceKey(sequence int64) (string, error) {
	if sequence <= 0 {
		return "", fmt.Errorf("message sequence must be positive, got %d", sequence)
	}
	return fmt.Sprintf("%012d", sequence), nil
}

// mustMessageSequenceKey formats a sequence the session derived from AgentState, which is positive.
func mustMessageSequenceKey(sequence int64) string {
	key, err := MessageSequenceKey(sequence)
	if err != nil {
		panic(err)
	}
	return key
}
