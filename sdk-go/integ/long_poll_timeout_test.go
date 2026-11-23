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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

// longPollCapCallerTimeout outlasts the Server's default 60-second long-poll cap by more than one call.
const longPollCapCallerTimeout = 3 * time.Minute

// TestLongPollCapBeforeCallerDeadline takes about one long-poll cap, 60 seconds by default.
func TestLongPollCapBeforeCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), longPollCapCallerTimeout)
	defer cancel()
	flow := rpcFlow{}
	flowID := newFlowID(t, "long-poll-cap")
	streamFlowID := newFlowID(t, "long-poll-cap-stream")
	_, err := integClient.StartFlow(ctx, flow, flowID, 1, dex.StartFlowOptions{})
	require.NoError(t, err)

	started := time.Now()
	flowWait := make(chan error, 1)
	go func() {
		_, err := integClient.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{})
		flowWait <- err
	}()
	streamRead := make(chan error, 1)
	go func() {
		var value string
		_, err := integClient.ReadStream(ctx, streamFlowID, streamTestProgress, "", &value)
		streamRead <- err
	}()
	attributeWait := make(chan error, 1)
	go func() {
		var matched string
		attributeWait <- integClient.WaitForAttributeMatch(
			ctx,
			flowID,
			rpcFlowWaitStatus,
			dex.AttributeMatchEqual("ready"),
			&matched,
			dex.WaitForAttributeOptions{},
		)
	}()
	stepWait := make(chan error, 1)
	go func() {
		stepWait <- integClient.WaitForStepCompletion(
			context.Background(),
			flowID,
			dex.StepExecutionID{StepType: dex.GetFinalStepType(rpcFlowStep{})},
			dex.WaitForStepCompletionOptions{RequestTimeout: longPollCapCallerTimeout},
		)
	}()

	for name, wait := range map[string]chan error{"WaitForFlow": flowWait, "ReadStream": streamRead} {
		err := <-wait
		var longPollTimeout *dex.LongPollTimeoutError
		require.ErrorAs(t, err, &longPollTimeout, name)
		require.NotErrorIs(t, err, context.DeadlineExceeded, name)
	}
	require.NoError(t, ctx.Err(), "the long-poll cap must expire before the caller's deadline")
	t.Logf("the Server's long-poll cap expired after %s", time.Since(started).Round(time.Second))

	// The durable waits reached the same cap; they must call the Server again instead of returning.
	select {
	case err := <-attributeWait:
		t.Fatalf("WaitForAttributeMatch returned at the long-poll cap: %v", err)
	case err := <-stepWait:
		t.Fatalf("WaitForStepCompletion returned at the long-poll cap: %v", err)
	case <-time.After(2 * time.Second):
	}

	var noOutput dex.None
	require.NoError(t, integClient.InvokeRPC(ctx, flowID, flow.SetWaitTargets, nil, &noOutput))
	require.NoError(t, <-attributeWait)
	var incremented rpcIncrementOutput
	require.NoError(t, integClient.InvokeRPC(ctx, flowID, flow.Increment, 1, &incremented))
	require.NoError(t, <-stepWait)
	result := waitForFlow(t, flowID, false)
	require.Equal(t, dex.FlowCompleted, result.Status)
}
