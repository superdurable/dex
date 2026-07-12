package mongo

import (
	"context"
	"os"
	"testing"
	"time"

	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// st is a tiny helper to build the *RunStatus filter pointer in tests.
func st(s p.RunStatus) *p.RunStatus { return &s }

// TestVisibilityStore_BatchUpsertVisibility_Idempotent checks that replaying
// the same batch is a no-op (matters for the OpsFIFO retry-forever model).
// Also verifies $setOnInsert keeps start_time stable across replays.
func TestVisibilityStore_BatchUpsertVisibility_Idempotent(t *testing.T) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}
	ctx := context.Background()

	store, err := NewVisibilityStoreWithDatabase(ctx, uri, testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()
	require.NoError(t, store.DeleteAll(ctx))

	ns := "ns-" + uuid.NewString()
	runID := uuid.NewString()
	originalStart := time.Now().UTC().Truncate(time.Millisecond)

	// First write: insert with status=Pending, capture start_time.
	require.Nil(t, store.BatchUpsertVisibility(ctx, []p.VisibilityEntry{{
		Namespace: ns, RunID: runID, FlowType: "ft", TaskListName: "g",
		Status: p.RunStatusPending, StartTime: originalStart, UpdatedAt: originalStart,
	}}))

	// Second write: status moves to Running. Replay would also include the
	// same start_time but $setOnInsert protects against accidental drift.
	laterUpdated := originalStart.Add(2 * time.Second)
	differentStart := originalStart.Add(1 * time.Hour) // pretend a buggy writer got start_time wrong
	require.Nil(t, store.BatchUpsertVisibility(ctx, []p.VisibilityEntry{{
		Namespace: ns, RunID: runID, FlowType: "ft", TaskListName: "g",
		Status: p.RunStatusRunning, StartTime: differentStart, UpdatedAt: laterUpdated,
	}}))

	// Read back: status updated, but start_time pinned to the original
	// thanks to $setOnInsert.
	page, listErr := store.ListRuns(ctx, p.ListRunsQuery{
		Namespace: ns, FlowType: "ft", Status: st(p.RunStatusRunning),
		OrderBy: p.ListByStartTimeDesc, Limit: 10,
	})
	require.Nil(t, listErr)
	require.Len(t, page.Entries, 1)
	got := page.Entries[0]
	assert.Equal(t, runID, got.RunID)
	assert.Equal(t, p.RunStatusRunning, got.Status)
	assert.True(t, got.StartTime.Equal(originalStart),
		"start_time pinned by $setOnInsert: want %v, got %v", originalStart, got.StartTime)
	assert.True(t, got.UpdatedAt.Equal(laterUpdated))
}

// TestVisibilityStore_ListRuns_OrderingAndPagination exercises both
// orderings and the page-token cursor.
func TestVisibilityStore_ListRuns_OrderingAndPagination(t *testing.T) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}
	ctx := context.Background()

	store, err := NewVisibilityStoreWithDatabase(ctx, uri, testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()
	require.NoError(t, store.DeleteAll(ctx))

	ns := "ns-" + uuid.NewString()
	flowType := "ft"
	base := time.Now().UTC().Truncate(time.Millisecond)

	// Insert 5 runs, each spaced 1 second apart on start_time. updated_at
	// is reversed on purpose so the two orderings produce different sequences.
	const total = 5
	runs := make([]p.VisibilityEntry, total)
	for i := 0; i < total; i++ {
		runs[i] = p.VisibilityEntry{
			Namespace: ns, RunID: uuid.NewString(),
			FlowType: flowType, TaskListName: "g",
			Status:    p.RunStatusRunning,
			StartTime: base.Add(time.Duration(i) * time.Second),            // ascending
			UpdatedAt: base.Add(time.Duration(total-i) * 10 * time.Second), // descending vs StartTime
		}
	}
	require.Nil(t, store.BatchUpsertVisibility(ctx, runs))

	// Order by start_time DESC: expect i=4,3,2,1,0.
	page, listErr := store.ListRuns(ctx, p.ListRunsQuery{
		Namespace: ns, FlowType: flowType, Status: st(p.RunStatusRunning),
		OrderBy: p.ListByStartTimeDesc, Limit: total,
	})
	require.Nil(t, listErr)
	require.Len(t, page.Entries, total)
	for i, got := range page.Entries {
		want := runs[total-1-i]
		assert.Equal(t, want.RunID, got.RunID, "start-time order, idx %d", i)
	}

	// Order by updated_at DESC: runs[0] has the largest updated_at, so the
	// expected sequence is 0,1,2,3,4.
	page, listErr = store.ListRuns(ctx, p.ListRunsQuery{
		Namespace: ns, FlowType: flowType, Status: st(p.RunStatusRunning),
		OrderBy: p.ListByUpdatedAtDesc, Limit: total,
	})
	require.Nil(t, listErr)
	require.Len(t, page.Entries, total)
	for i, got := range page.Entries {
		assert.Equal(t, runs[i].RunID, got.RunID, "updated-at order, idx %d", i)
	}

	// Pagination: ask for 2 at a time, walk to the end.
	var seen []string
	pageToken := ""
	for {
		page, listErr = store.ListRuns(ctx, p.ListRunsQuery{
			Namespace: ns, FlowType: flowType, Status: st(p.RunStatusRunning),
			OrderBy: p.ListByStartTimeDesc, Limit: 2, PageToken: pageToken,
		})
		require.Nil(t, listErr)
		for _, e := range page.Entries {
			seen = append(seen, e.RunID)
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	assert.Len(t, seen, total, "pagination should cover every run")
	// Order preserved across pages (start_time DESC).
	for i, runID := range seen {
		assert.Equal(t, runs[total-1-i].RunID, runID, "paged idx %d", i)
	}
}

// TestVisibilityStore_ListRuns_StatusAndFlowTypeFilters confirms that the
// filters actually narrow the result set (sanity for index correctness).
func TestVisibilityStore_ListRuns_StatusAndFlowTypeFilters(t *testing.T) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}
	ctx := context.Background()

	store, err := NewVisibilityStoreWithDatabase(ctx, uri, testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()
	require.NoError(t, store.DeleteAll(ctx))

	ns := "ns-" + uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)
	mk := func(flowType string, status p.RunStatus) p.VisibilityEntry {
		return p.VisibilityEntry{
			Namespace: ns, RunID: uuid.NewString(),
			FlowType: flowType, TaskListName: "g",
			Status:    status,
			StartTime: now,
			UpdatedAt: now,
		}
	}
	require.Nil(t, store.BatchUpsertVisibility(ctx, []p.VisibilityEntry{
		mk("a", p.RunStatusRunning),
		mk("a", p.RunStatusRunning),
		mk("a", p.RunStatusCompleted),
		mk("b", p.RunStatusRunning),
	}))

	// FlowType=a, Status=Running -> 2.
	page, listErr := store.ListRuns(ctx, p.ListRunsQuery{
		Namespace: ns, FlowType: "a", Status: st(p.RunStatusRunning),
		OrderBy: p.ListByStartTimeDesc, Limit: 100,
	})
	require.Nil(t, listErr)
	assert.Len(t, page.Entries, 2)

	// FlowType=a, Status=Completed -> 1.
	page, listErr = store.ListRuns(ctx, p.ListRunsQuery{
		Namespace: ns, FlowType: "a", Status: st(p.RunStatusCompleted),
		OrderBy: p.ListByStartTimeDesc, Limit: 100,
	})
	require.Nil(t, listErr)
	assert.Len(t, page.Entries, 1)

	// FlowType=b, Status=Running -> 1.
	page, listErr = store.ListRuns(ctx, p.ListRunsQuery{
		Namespace: ns, FlowType: "b", Status: st(p.RunStatusRunning),
		OrderBy: p.ListByStartTimeDesc, Limit: 100,
	})
	require.Nil(t, listErr)
	assert.Len(t, page.Entries, 1)
}

// TestVisibilityStore_ListRuns_AnyFlowType verifies that an empty
// FlowType filter returns all flow types within the namespace.
func TestVisibilityStore_ListRuns_AnyFlowType(t *testing.T) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}
	ctx := context.Background()

	store, err := NewVisibilityStoreWithDatabase(ctx, uri, testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()
	require.NoError(t, store.DeleteAll(ctx))

	ns := "ns-" + uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)
	mk := func(flowType string) p.VisibilityEntry {
		return p.VisibilityEntry{
			Namespace: ns, RunID: uuid.NewString(),
			FlowType: flowType, TaskListName: "g",
			Status: p.RunStatusRunning, StartTime: now, UpdatedAt: now,
		}
	}
	require.Nil(t, store.BatchUpsertVisibility(ctx, []p.VisibilityEntry{
		mk("a"), mk("a"), mk("b"),
	}))

	// Empty FlowType + explicit Running status -> all 3 returned.
	page, listErr := store.ListRuns(ctx, p.ListRunsQuery{
		Namespace: ns, FlowType: "", Status: st(p.RunStatusRunning),
		OrderBy: p.ListByStartTimeDesc, Limit: 100,
	})
	require.Nil(t, listErr)
	assert.Len(t, page.Entries, 3)
}

// TestVisibilityStore_ListRuns_AnyStatus verifies that a nil Status
// filter returns all statuses for the (namespace, flow_type) pair.
func TestVisibilityStore_ListRuns_AnyStatus(t *testing.T) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}
	ctx := context.Background()

	store, err := NewVisibilityStoreWithDatabase(ctx, uri, testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()
	require.NoError(t, store.DeleteAll(ctx))

	ns := "ns-" + uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)
	mk := func(status p.RunStatus) p.VisibilityEntry {
		return p.VisibilityEntry{
			Namespace: ns, RunID: uuid.NewString(),
			FlowType: "ft", TaskListName: "g",
			Status: status, StartTime: now, UpdatedAt: now,
		}
	}
	require.Nil(t, store.BatchUpsertVisibility(ctx, []p.VisibilityEntry{
		mk(p.RunStatusPending),
		mk(p.RunStatusRunning),
		mk(p.RunStatusCompleted),
		mk(p.RunStatusFailed),
	}))

	page, listErr := store.ListRuns(ctx, p.ListRunsQuery{
		Namespace: ns, FlowType: "ft", Status: nil,
		OrderBy: p.ListByStartTimeDesc, Limit: 100,
	})
	require.Nil(t, listErr)
	assert.Len(t, page.Entries, 4)
}

// TestVisibilityStore_ListRuns_AnyBoth verifies that empty FlowType +
// nil Status returns every run in the namespace regardless of either field.
func TestVisibilityStore_ListRuns_AnyBoth(t *testing.T) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}
	ctx := context.Background()

	store, err := NewVisibilityStoreWithDatabase(ctx, uri, testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()
	require.NoError(t, store.DeleteAll(ctx))

	ns := "ns-" + uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)
	mk := func(flowType string, status p.RunStatus) p.VisibilityEntry {
		return p.VisibilityEntry{
			Namespace: ns, RunID: uuid.NewString(),
			FlowType: flowType, TaskListName: "g",
			Status: status, StartTime: now, UpdatedAt: now,
		}
	}
	require.Nil(t, store.BatchUpsertVisibility(ctx, []p.VisibilityEntry{
		mk("a", p.RunStatusRunning),
		mk("a", p.RunStatusCompleted),
		mk("b", p.RunStatusFailed),
		mk("c", p.RunStatusFailed),
	}))

	page, listErr := store.ListRuns(ctx, p.ListRunsQuery{
		Namespace: ns, FlowType: "", Status: nil,
		OrderBy: p.ListByStartTimeDesc, Limit: 100,
	})
	require.Nil(t, listErr)
	assert.Len(t, page.Entries, 4)
}

func TestVisibilityStore_ListRuns_RequiresNamespace(t *testing.T) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}
	store, err := NewVisibilityStoreWithDatabase(context.Background(), uri, testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()

	_, listErr := store.ListRuns(context.Background(), p.ListRunsQuery{
		Namespace: "", FlowType: "ft", Status: st(p.RunStatusRunning), OrderBy: p.ListByStartTimeDesc,
	})
	require.NotNil(t, listErr)
	assert.True(t, listErr.IsInvalidInputError(), "expected InvalidInput, got %T (%v)", listErr, listErr)
}

func TestVisibilityStore_BatchUpsertVisibility_EmptyIsNoop(t *testing.T) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}
	store, err := NewVisibilityStoreWithDatabase(context.Background(), uri, testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()
	assert.Nil(t, store.BatchUpsertVisibility(context.Background(), nil))
}
