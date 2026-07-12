package taskprocessor

import (
	"testing"
	"time"

	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/stretchr/testify/assert"
)

// TestSplitOpsBatch verifies that mixed history/visibility task slices are
// partitioned by TaskType while preserving per-group order.
func TestSplitOpsBatch(t *testing.T) {
	tasks := []*p.OpsFIFOTaskRow{
		{SortKey: 1, TaskType: p.OpsFIFOTaskHistoryWrite, HistoryPayload: &p.HistoryEvent{EventID: 10}},
		{SortKey: 2, TaskType: p.OpsFIFOTaskVisibilityWrite, VisibilityPayload: &p.VisibilityEntry{RunID: "r1"}},
		{SortKey: 3, TaskType: p.OpsFIFOTaskHistoryWrite, HistoryPayload: &p.HistoryEvent{EventID: 11}},
		{SortKey: 4, TaskType: p.OpsFIFOTaskHistoryWrite, HistoryPayload: nil}, // nil payload -> dropped
		{SortKey: 5, TaskType: p.OpsFIFOTaskVisibilityWrite, VisibilityPayload: &p.VisibilityEntry{RunID: "r2"}},
	}
	hist, vis := splitOpsBatch(tasks)

	if assert.Len(t, hist, 2, "two non-nil history payloads") {
		assert.Equal(t, int64(10), hist[0].EventID, "history order preserved")
		assert.Equal(t, int64(11), hist[1].EventID)
	}
	if assert.Len(t, vis, 2, "two visibility payloads") {
		assert.Equal(t, "r1", vis[0].RunID, "visibility order preserved")
		assert.Equal(t, "r2", vis[1].RunID)
	}
}

// TestMergeVisibilityByRunID covers the documented merge contract:
//   - latest entry wins for status / updated_at / task_list_name / flow_type
//   - earliest non-zero start_time wins (immutable per run)
//   - distinct (namespace, run_id) tuples produce distinct output rows
func TestMergeVisibilityByRunID(t *testing.T) {
	t1 := time.UnixMilli(1000)
	t2 := time.UnixMilli(2000)
	t3 := time.UnixMilli(3000)

	in := []p.VisibilityEntry{
		// Same run "a" three times — latest status should win.
		{Namespace: "ns", RunID: "a", FlowType: "ft", TaskListName: "g", Status: p.RunStatusPending, StartTime: t1, UpdatedAt: t1},
		{Namespace: "ns", RunID: "a", FlowType: "ft", TaskListName: "g", Status: p.RunStatusRunning, StartTime: t2, UpdatedAt: t2},
		{Namespace: "ns", RunID: "a", FlowType: "ft", TaskListName: "g", Status: p.RunStatusCompleted, StartTime: t3, UpdatedAt: t3},

		// Different run "b" — should produce its own output row.
		{Namespace: "ns", RunID: "b", FlowType: "ft2", TaskListName: "g2", Status: p.RunStatusRunning, StartTime: t2, UpdatedAt: t2},

		// Same run_id "a" but different namespace — distinct slot.
		{Namespace: "ns2", RunID: "a", FlowType: "ft", TaskListName: "g", Status: p.RunStatusRunning, StartTime: t2, UpdatedAt: t2},
	}
	out := mergeVisibilityByRunID(in)

	if !assert.Len(t, out, 3, "three distinct (namespace, run_id) tuples") {
		return
	}

	// First slot: namespace=ns, run_id=a — latest status wins, EARLIEST
	// non-zero start_time pinned (t1).
	merged := out[0]
	assert.Equal(t, "ns", merged.Namespace)
	assert.Equal(t, "a", merged.RunID)
	assert.Equal(t, p.RunStatusCompleted, merged.Status, "latest status wins")
	assert.True(t, merged.StartTime.Equal(t1), "earliest start_time pinned")
	assert.True(t, merged.UpdatedAt.Equal(t3), "latest updated_at wins")

	// Second slot: namespace=ns, run_id=b — single entry, untouched.
	assert.Equal(t, "b", out[1].RunID)
	assert.Equal(t, p.RunStatusRunning, out[1].Status)

	// Third slot: namespace=ns2, run_id=a — distinct from out[0] despite same run_id.
	assert.Equal(t, "ns2", out[2].Namespace)
	assert.Equal(t, "a", out[2].RunID)
}

// TestMergeVisibilityByRunID_PreservesEarliestNonZeroStartTime covers the
// edge case where the first entry has a zero StartTime (engine couldn't
// stamp it for some reason) and the next entry carries the real value.
func TestMergeVisibilityByRunID_PreservesEarliestNonZeroStartTime(t *testing.T) {
	in := []p.VisibilityEntry{
		{Namespace: "ns", RunID: "a", Status: p.RunStatusPending, StartTime: time.Time{}, UpdatedAt: time.UnixMilli(1)},
		{Namespace: "ns", RunID: "a", Status: p.RunStatusRunning, StartTime: time.UnixMilli(2000), UpdatedAt: time.UnixMilli(2)},
	}
	out := mergeVisibilityByRunID(in)
	if assert.Len(t, out, 1) {
		assert.True(t, out[0].StartTime.Equal(time.UnixMilli(2000)),
			"zero StartTime in earliest entry must be replaced by the next entry's non-zero value")
	}
}
