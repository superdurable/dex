package mongo

import (
	"context"
	"testing"
	"time"

	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getTasklistStore(t *testing.T) p.TasklistStore {
	store, err := NewTasklistStoreWithDatabase(context.Background(), getMongoURI(t), testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func TestTasklistStore_ClaimTasklist(t *testing.T) {
	ctx := context.Background()
	store := getTasklistStore(t)

	md, err := store.ClaimTasklist(ctx, "ns1", "tl1", 0, "member-A", "addr-A:7234")
	require.Nil(t, err)
	assert.Equal(t, int32(1), md.RangeID)
	assert.Equal(t, int64(0), md.AckLevel)
	assert.Equal(t, "member-A", md.OwnerMemberID)
	assert.Equal(t, "addr-A:7234", md.OwnerAddress)

	// Second claim steals ownership: range_id increments
	md2, err2 := store.ClaimTasklist(ctx, "ns1", "tl1", 0, "member-B", "addr-B:7234")
	require.Nil(t, err2)
	assert.Equal(t, int32(2), md2.RangeID)
	assert.Equal(t, "member-B", md2.OwnerMemberID)

	// Separate partition has its own range_id
	md3, err3 := store.ClaimTasklist(ctx, "ns1", "tl1", 1, "member-A", "addr-A:7234")
	require.Nil(t, err3)
	assert.Equal(t, int32(1), md3.RangeID)
}

func TestTasklistStore_UpdateTasklistMetadata_Fenced(t *testing.T) {
	ctx := context.Background()
	store := getTasklistStore(t)

	md, _ := store.ClaimTasklist(ctx, "ns1", "tl-ack", 0, "owner1", "addr1")

	// Update with correct range_id succeeds
	err := store.UpdateTasklistMetadata(ctx, "ns1", "tl-ack", 0, md.RangeID, 100)
	require.Nil(t, err)

	// Read back to verify
	got, gErr := store.GetTasklistMetadata(ctx, "ns1", "tl-ack", 0)
	require.Nil(t, gErr)
	assert.Equal(t, int64(100), got.AckLevel)

	// Update with stale range_id fails
	staleErr := store.UpdateTasklistMetadata(ctx, "ns1", "tl-ack", 0, md.RangeID-1, 200)
	require.NotNil(t, staleErr)
	assert.True(t, staleErr.IsConflictError())
}

func TestTasklistStore_CreateTasks_Fenced(t *testing.T) {
	ctx := context.Background()
	store := getTasklistStore(t)

	md, _ := store.ClaimTasklist(ctx, "ns1", "tl-create", 0, "owner1", "addr1")

	now := time.Now()
	tasks := []*p.TasklistTaskRow{
		{Namespace: "ns1", TasklistName: "tl-create", PartitionID: 0, TaskID: 1001, RunID: "run-1", ShardID: 0, CreatedAt: now},
		{Namespace: "ns1", TasklistName: "tl-create", PartitionID: 0, TaskID: 1002, RunID: "run-2", ShardID: 1, CreatedAt: now},
	}

	// Create with correct range_id succeeds
	err := store.CreateTasks(ctx, "ns1", "tl-create", 0, md.RangeID, tasks)
	require.Nil(t, err)

	// Verify tasks are readable
	got, gErr := store.GetTasks(ctx, "ns1", "tl-create", 0, 1000, 1100, 10)
	require.Nil(t, gErr)
	assert.Len(t, got, 2)
	assert.Equal(t, int64(1001), got[0].TaskID)
	assert.Equal(t, "run-1", got[0].RunID)
	assert.Equal(t, int64(1002), got[1].TaskID)
	assert.Equal(t, "run-2", got[1].RunID)

	// Create with stale range_id fails (after another member steals)
	store.ClaimTasklist(ctx, "ns1", "tl-create", 0, "thief", "thief-addr")
	staleErr := store.CreateTasks(ctx, "ns1", "tl-create", 0, md.RangeID, []*p.TasklistTaskRow{
		{Namespace: "ns1", TasklistName: "tl-create", PartitionID: 0, TaskID: 1003, RunID: "run-3", ShardID: 0, CreatedAt: now},
	})
	require.NotNil(t, staleErr)
	assert.True(t, staleErr.IsConflictError())
}

func TestTasklistStore_GetTasks_RangeRead(t *testing.T) {
	ctx := context.Background()
	store := getTasklistStore(t)

	md, _ := store.ClaimTasklist(ctx, "ns1", "tl-range", 0, "owner1", "addr1")

	now := time.Now()
	tasks := make([]*p.TasklistTaskRow, 5)
	for i := range tasks {
		tasks[i] = &p.TasklistTaskRow{
			Namespace: "ns1", TasklistName: "tl-range", PartitionID: 0,
			TaskID: int64(100 + i), RunID: "run", ShardID: 0, CreatedAt: now,
		}
	}
	require.Nil(t, store.CreateTasks(ctx, "ns1", "tl-range", 0, md.RangeID, tasks))

	// Read range (101, 103] => taskIDs 102, 103
	got, err := store.GetTasks(ctx, "ns1", "tl-range", 0, 101, 103, 10)
	require.Nil(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, int64(102), got[0].TaskID)
	assert.Equal(t, int64(103), got[1].TaskID)

	// batchSize limits results
	got2, err2 := store.GetTasks(ctx, "ns1", "tl-range", 0, 99, 104, 2)
	require.Nil(t, err2)
	assert.Len(t, got2, 2)
	assert.Equal(t, int64(100), got2[0].TaskID)
	assert.Equal(t, int64(101), got2[1].TaskID)
}

func TestTasklistStore_DeleteTasksLessThan(t *testing.T) {
	ctx := context.Background()
	store := getTasklistStore(t)

	md, _ := store.ClaimTasklist(ctx, "ns1", "tl-del", 0, "owner1", "addr1")
	now := time.Now()
	tasks := make([]*p.TasklistTaskRow, 5)
	for i := range tasks {
		tasks[i] = &p.TasklistTaskRow{
			Namespace: "ns1", TasklistName: "tl-del", PartitionID: 0,
			TaskID: int64(200 + i), RunID: "r", ShardID: 0, CreatedAt: now,
		}
	}
	require.Nil(t, store.CreateTasks(ctx, "ns1", "tl-del", 0, md.RangeID, tasks))

	// Delete tasks <= 202 => deletes taskIDs 200, 201, 202
	deleted, err := store.DeleteTasksLessThan(ctx, "ns1", "tl-del", 0, 202, 10)
	require.Nil(t, err)
	assert.Equal(t, 3, deleted)

	// Remaining tasks are 203, 204
	remaining, _ := store.GetTasks(ctx, "ns1", "tl-del", 0, 0, 300, 10)
	assert.Len(t, remaining, 2)
	assert.Equal(t, int64(203), remaining[0].TaskID)

	// `limit` is a hint only and is ignored: deleting <= 204 removes BOTH
	// remaining rows (203, 204) even though limit=1.
	deleted2, err2 := store.DeleteTasksLessThan(ctx, "ns1", "tl-del", 0, 204, 1)
	require.Nil(t, err2)
	assert.Equal(t, 2, deleted2)
}

func TestTasklistStore_DeleteTasksByIDBatch(t *testing.T) {
	ctx := context.Background()
	store := getTasklistStore(t)

	md, _ := store.ClaimTasklist(ctx, "ns1", "tl-delbatch", 0, "owner1", "addr1")
	now := time.Now()
	tasks := []*p.TasklistTaskRow{
		{Namespace: "ns1", TasklistName: "tl-delbatch", PartitionID: 0, TaskID: 500, RunID: "r", ShardID: 0, CreatedAt: now},
		{Namespace: "ns1", TasklistName: "tl-delbatch", PartitionID: 0, TaskID: 501, RunID: "r", ShardID: 0, CreatedAt: now},
		{Namespace: "ns1", TasklistName: "tl-delbatch", PartitionID: 0, TaskID: 502, RunID: "r", ShardID: 0, CreatedAt: now},
	}
	require.Nil(t, store.CreateTasks(ctx, "ns1", "tl-delbatch", 0, md.RangeID, tasks))

	// Delete specific IDs: 500 and 502
	err := store.DeleteTasksByIDBatch(ctx, "ns1", "tl-delbatch", 0, []int64{500, 502})
	require.Nil(t, err)

	// Only 501 remains
	remaining, _ := store.GetTasks(ctx, "ns1", "tl-delbatch", 0, 0, 600, 10)
	assert.Len(t, remaining, 1)
	assert.Equal(t, int64(501), remaining[0].TaskID)
}

func TestTasklistStore_GetTasklistMetadata_NotFound(t *testing.T) {
	ctx := context.Background()
	store := getTasklistStore(t)

	_, err := store.GetTasklistMetadata(ctx, "ns-nonexistent", "tl-nonexistent", 99)
	require.NotNil(t, err)
	assert.True(t, err.IsNotFoundError())
}
