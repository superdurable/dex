// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesessionfeed

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
	"github.com/superdurable/dex/sdk-go/dex"
)

func streamStoreDisabledError() error {
	return &dex.ServiceError{Op: "ListStreamMessages", Code: codes.FailedPrecondition, SubStatus: dex.ErrorSubStatusUncategorized, Detail: "Stream Store is disabled"}
}

func TestIsStreamStoreOffMatchesOnlyTheDisabledStore(t *testing.T) {
	require.True(t, isStreamStoreOff(streamStoreDisabledError()))
	require.True(t, isStreamStoreOff(fmt.Errorf("list agent events: %w", streamStoreDisabledError())), "a wrapped error still matches")
	for name, err := range map[string]error{
		"another precondition": &dex.ServiceError{Code: codes.FailedPrecondition, SubStatus: dex.ErrorSubStatusUncategorized, Detail: "the session is not ready"},
		"another sub-status":   &dex.ServiceError{Code: codes.FailedPrecondition, SubStatus: dex.ErrorSubStatusFlowClosing, Detail: "Stream Store is disabled"},
		"another code":         &dex.ServiceError{Code: codes.Unavailable, SubStatus: dex.ErrorSubStatusUncategorized, Detail: "Stream Store is disabled"},
		"not a service error":  fmt.Errorf("Stream Store is disabled"),
	} {
		require.False(t, isStreamStoreOff(err), name)
	}
}

// followEventsFeed is a feed whose loop inputs the test reads itself.
func followEventsFeed(sessions *fakeSessions) *RemoteSessionFeed {
	return NewRemoteSessionFeed(RemoteSessionFeedConfig{
		Sessions: sessions,
		Requests: noLocalRequests{},
		Launch: launchcontract.LaunchDocument{
			Command: launchcontract.LaunchCommand{Harness: "test"},
			Session: launchcontract.LaunchSession{FlowType: "Test", SessionID: "session-1"},
		},
		Timings: FeedTimings{LockConflictBudget: 200 * time.Millisecond, SnapshotTimeout: time.Second, RPCTimeout: time.Second},
	})
}

// A disabled Stream store turns live output off for good.
func TestFollowAgentEventsStopsWhenTheStreamStoreIsDisabled(t *testing.T) {
	sessions := &fakeSessions{listEvents: func(int32, string) (dex.StreamMessagesPage[dexagents.AgentEvent], error) {
		return dex.StreamMessagesPage[dexagents.AgentEvent]{}, streamStoreDisabledError()
	}}
	feed := followEventsFeed(sessions)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go followAgentEvents(ctx, feed, "session-1", 1)
	select {
	case input := <-feed.inputs:
		unavailable, isUnavailable := input.(eventsUnavailable)
		require.True(t, isUnavailable, "got %T", input)
		require.Contains(t, unavailable.reason, "Stream Store is disabled")
	case <-ctx.Done():
		t.Fatal("live output was never turned off")
	}
}

// Any other precondition failure is retried, and live output goes on.
func TestFollowAgentEventsRetriesOtherPreconditionFailures(t *testing.T) {
	var listCalls atomic.Int32
	sessions := &fakeSessions{listEvents: func(int32, string) (dex.StreamMessagesPage[dexagents.AgentEvent], error) {
		if listCalls.Add(1) == 1 {
			return dex.StreamMessagesPage[dexagents.AgentEvent]{}, &dex.ServiceError{Code: codes.FailedPrecondition, SubStatus: dex.ErrorSubStatusUncategorized, Detail: "the session is not ready"}
		}
		return dex.StreamMessagesPage[dexagents.AgentEvent]{}, nil
	}}
	feed := followEventsFeed(sessions)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go followAgentEvents(ctx, feed, "session-1", 1)
	require.Eventually(t, func() bool { return listCalls.Load() >= 2 }, 5*time.Second, 20*time.Millisecond, "the list is retried")
	select {
	case input := <-feed.inputs:
		t.Fatalf("live output must stay on, got %T", input)
	default:
	}
}
