package sdke2e

import (
	"context"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSDKE2E_WaitForHistoryEvent drives a worker-executed run and uses the SDK
// to (1) block on WaitForHistoryEvent until the first history event is readable
// and (2) block on WaitForRunComplete until the run reaches a terminal state —
// the long-poll path that avoids per-event GetRun polling.
func TestSDKE2E_WaitForHistoryEvent(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&SeqFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID := uuid.NewString()
	startWorkerBg(t, worker)

	err := client.StartRunWithOptions(ctx, runID, &SeqFlow{}, &dex.RunOptions{TaskListName: taskListName})
	require.NoError(t, err)

	// WaitForHistoryEvent: block until the RunStart event (id 1) is readable.
	firstTip, err := client.WaitForHistoryEvent(ctx, runID, 1)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, firstTip, int64(1))

	// WaitForRunComplete: long-poll until terminal, returning the status
	// directly (no GetRun round trips).
	start := time.Now()
	st, err := client.WaitForRunComplete(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, dex.RunStatusCompleted, st)
	assert.Less(t, time.Since(start), 20*time.Second, "must return promptly once the run closes")
}
