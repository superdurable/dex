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
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex"
	"google.golang.org/grpc/codes"
)

var runTargetingLabelAttribute = dex.DefineAttribute[string]("run-label")

// runTargetingFlow has no Steps, so each run stays running until a Client stops it.
type runTargetingFlow struct {
	dex.FlowDefaults
}

func (runTargetingFlow) GetSteps() []dex.StepDef {
	return nil
}

func (runTargetingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Attributes: []dex.AttributeDef{runTargetingLabelAttribute}}
}

// TestClientCallsTargetAnOlderRunByRunID reads, waits for, and stops a closed run of a Flow ID by
// its run ID while a newer run of the same Flow ID is running.
func TestClientCallsTargetAnOlderRunByRunID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runtime := startDexService(t, serverZDTestConfig{})
	client := newRunTargetingClient(t, runtime.PublicAddress)
	flowID := "run-id-targeting-" + uuid.NewString()

	olderRunID := startRunTargetingRun(t, ctx, client, flowID, "older")
	require.NoError(t, client.StopFlow(ctx, flowID, dex.StopOptions{}))
	olderResult, err := client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{})
	require.NoError(t, err)
	require.Equal(t, dex.FlowCanceled, olderResult.Status)
	newerRunID := startRunTargetingRun(t, ctx, client, flowID, "newer")
	require.NotEqual(t, olderRunID, newerRunID)

	t.Run("DescribeFlowRunReadsTheOlderRun", func(t *testing.T) {
		info, err := client.DescribeFlowRun(ctx, flowID, olderRunID)
		require.NoError(t, err)
		require.Equal(t, olderRunID, info.RunID)
		require.Equal(t, dex.FlowCanceled, info.Status)
		require.False(t, info.ClosedAt.IsZero())
		latest, err := client.DescribeFlow(ctx, flowID)
		require.NoError(t, err)
		require.Equal(t, newerRunID, latest.RunID)
		require.Equal(t, dex.FlowRunning, latest.Status)
	})

	t.Run("GetFlowRunAttributesReadsTheClosedRunValues", func(t *testing.T) {
		var olderLabel string
		_, err := client.GetFlowRunAttributes(ctx, flowID, olderRunID,
			dex.ReadAttribute(runTargetingLabelAttribute, &olderLabel))
		require.NoError(t, err)
		require.Equal(t, "older", olderLabel)
		var latestLabel string
		_, err = client.GetAttributes(ctx, flowID, dex.ReadAttribute(runTargetingLabelAttribute, &latestLabel))
		require.NoError(t, err)
		require.Equal(t, "newer", latestLabel)
	})

	t.Run("WaitForFlowOnTheOlderRunReturnsItsResultAtOnce", func(t *testing.T) {
		waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
		defer waitCancel()
		startedAt := time.Now()
		result, err := client.WaitForFlow(waitCtx, flowID, dex.WaitForFlowOptions{RunID: olderRunID})
		require.NoError(t, err)
		require.Equal(t, dex.FlowCanceled, result.Status)
		require.Less(t, time.Since(startedAt), 2*time.Second)
	})

	t.Run("StopFlowOfTheClosedRunLeavesTheNewerRunRunning", func(t *testing.T) {
		err := client.StopFlow(ctx, flowID, dex.StopOptions{Type: dex.TerminateFlow, RunID: olderRunID})
		var notActive *dex.FlowNotActiveOrNotFoundError
		require.ErrorAs(t, err, &notActive)
		latest, err := client.DescribeFlowRun(ctx, flowID, newerRunID)
		require.NoError(t, err)
		require.Equal(t, dex.FlowRunning, latest.Status)
	})

	t.Run("MissingRunReturnsNotFound", func(t *testing.T) {
		assertRunTargetingNotFound(t, ctx, client, flowID, uuid.NewString())
	})

	t.Run("RunOfAnotherFlowIDReturnsNotFound", func(t *testing.T) {
		otherFlowID := "run-id-targeting-other-" + uuid.NewString()
		otherRunID := startRunTargetingRun(t, ctx, client, otherFlowID, "other")
		assertRunTargetingNotFound(t, ctx, client, flowID, otherRunID)
		other, err := client.DescribeFlowRun(ctx, otherFlowID, otherRunID)
		require.NoError(t, err)
		require.Equal(t, dex.FlowRunning, other.Status)
		require.NoError(t, client.StopFlow(ctx, otherFlowID, dex.StopOptions{RunID: otherRunID}))
	})

	t.Run("NonCanonicalRunIDReturnsInvalidArgument", func(t *testing.T) {
		_, err := client.DescribeFlowRun(ctx, flowID, "not-a-run-id")
		var serviceError *dex.ServiceError
		require.ErrorAs(t, err, &serviceError)
		require.Equal(t, codes.InvalidArgument, serviceError.Code)
	})

	t.Run("StopFlowOfTheNewerRunByRunIDStopsIt", func(t *testing.T) {
		require.NoError(t, client.StopFlow(ctx, flowID, dex.StopOptions{RunID: newerRunID}))
		result, err := client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{RunID: newerRunID})
		require.NoError(t, err)
		require.Equal(t, dex.FlowCanceled, result.Status)
	})
}

func newRunTargetingClient(t *testing.T, address string) *dex.Client {
	t.Helper()
	registry, err := dex.NewRegistry([]dex.Flow{runTargetingFlow{}})
	require.NoError(t, err)
	cache, err := blobcache.New(&blobcache.Config{Dir: t.TempDir(), MaxBytes: 16 << 20})
	require.NoError(t, err)
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: address})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, errors.Join(client.Close(), cache.Close()))
	})
	return client
}

func startRunTargetingRun(
	t *testing.T,
	ctx context.Context,
	client *dex.Client,
	flowID string,
	label string,
) string {
	t.Helper()
	initialLabel, err := dex.InitialAttribute(runTargetingLabelAttribute, label)
	require.NoError(t, err)
	runID, err := client.StartFlow(ctx, runTargetingFlow{}, flowID, nil, dex.StartFlowOptions{
		IDReusePolicy: dex.IDReuseAllowIfNotRunning,
		Attributes:    []dex.InitialAttributeDef{initialLabel},
	})
	require.NoError(t, err)
	return runID
}

// assertRunTargetingNotFound checks that every run-targeted call rejects runID for flowID as not found.
func assertRunTargetingNotFound(
	t *testing.T,
	ctx context.Context,
	client *dex.Client,
	flowID string,
	runID string,
) {
	t.Helper()
	var notFound *dex.FlowNotFoundError
	_, err := client.DescribeFlowRun(ctx, flowID, runID)
	require.ErrorAs(t, err, &notFound)
	var label string
	_, err = client.GetFlowRunAttributes(ctx, flowID, runID, dex.ReadAttribute(runTargetingLabelAttribute, &label))
	require.ErrorAs(t, err, &notFound)
	_, err = client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{RunID: runID})
	require.ErrorAs(t, err, &notFound)
	var notActive *dex.FlowNotActiveOrNotFoundError
	err = client.StopFlow(ctx, flowID, dex.StopOptions{RunID: runID})
	require.ErrorAs(t, err, &notActive)
}
