// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"context"
	"errors"
	"fmt"

	"github.com/superdurable/dex/sdk-go/dex"
)

const unknownSessionFailure = "the session's Flow failed"

// AgentSessionFailedError reports a message sent to a session that failed.
//
// AgentSessionClient.SendUserMessage returns it in place of the Dex error for
// a closed Flow, which stays in its chain, so errors.As still finds the
// *dex.FlowNotActiveOrNotFoundError. The message did not reach the session.
// Messages queued earlier stay readable in AgentSnapshot.QueuedMessages and
// never run. It is AgentSessionFailedError in the Python and TypeScript cores.
//
//	_, err := sessions.SendUserMessage(ctx, "session-42", "Where is order 7?")
//	var failed *dexagents.AgentSessionFailedError
//	if errors.As(err, &failed) {
//		log.Printf("session %s failed: %s", failed.SessionID, failed.Reason)
//	}
//
// Since 0.6.16.
type AgentSessionFailedError struct {
	// SessionID is the failed session.
	SessionID string
	// Reason is why the session failed, the failed Flow's reason, such as
	// "active run state of 135741 bytes exceeds the maximum of 131072 bytes".
	Reason string
	// Err is the Dex error for the closed Flow, a *dex.FlowNotActiveOrNotFoundError.
	Err error
}

// Error names the session and the reason.
func (e *AgentSessionFailedError) Error() string {
	return fmt.Sprintf("session %s failed: %s", e.SessionID, e.Reason)
}

// Unwrap returns the Dex error for the closed Flow.
func (e *AgentSessionFailedError) Unwrap() error {
	return e.Err
}

// markSessionFailed gives a stored snapshot what the session stores when it has room to record its failure.
func markSessionFailed(snapshot *AgentSnapshot, reason string) {
	state := snapshot.State
	if state.ActiveTurnID != nil {
		state.TurnCount++
	}
	state.Status = AgentSessionStatusFailed
	state.ActiveTurnID = nil
	state.AcceptsTurnControl = false
	state.LastTurnError = &reason
	snapshot.PendingInterrupts = []AgentInterrupt{}
	// ToolCalls lists the active turn's records; a stale turn's calls never finish.
	snapshot.ToolCalls = []AgentToolCall{}
}

// mayHideSessionFailure reports whether the Flow's close could add to this snapshot.
func mayHideSessionFailure(snapshot AgentSnapshot) bool {
	return snapshot.State != nil && snapshot.State.Status != AgentSessionStatusFailed
}

// readSessionFailure returns why the session's Flow failed, or false when it did not fail or does not exist.
func readSessionFailure(ctx context.Context, client *dex.Client, sessionID string) (string, bool, error) {
	info, err := client.DescribeFlow(ctx, sessionID)
	var notFound *dex.FlowNotFoundError
	if errors.As(err, &notFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Status != dex.FlowFailed {
		return "", false, nil
	}
	result, err := client.WaitForFlow(ctx, sessionID, dex.WaitForFlowOptions{})
	if err != nil {
		return "", false, err
	}
	if result.ErrorMessage == "" {
		return unknownSessionFailure, true, nil
	}
	return result.ErrorMessage, true, nil
}
