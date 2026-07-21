// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package postgres_test

import (
	"common-go/ids"
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	p "github.com/superdurable/dex/server/internal/persistence"
)

func blobScope(t *testing.T) (int32, string, string) {
	t.Helper()
	return nextShardID(t), "blobns-" + ids.NewUID().String(), "run-" + ids.NewUID().String()
}

func blobByID(entries []p.BlobEntry) map[ids.UID]p.BlobEntry {
	out := make(map[ids.UID]p.BlobEntry, len(entries))
	for _, entry := range entries {
		out[entry.BlobID] = entry
	}
	return out
}

func TestBlobStore_EmptyBatchNoop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := blobScope(t)

	require.NoError(t, blobStore.BatchInsert(ctx, shardID, namespace, runID, nil))
	require.NoError(t, blobStore.BatchInsert(ctx, shardID, namespace, runID, []p.BlobEntry{}))

	got, err := blobStore.BatchGet(ctx, shardID, namespace, runID, nil)
	require.NoError(t, err)
	require.Nil(t, got)

	got, err = blobStore.BatchGet(ctx, shardID, namespace, runID, []ids.UID{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestBlobStore_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := blobScope(t)

	blobID := ids.NewUID()
	payload := []byte(`{"hello":"world"}`)
	require.NoError(t, blobStore.BatchInsert(ctx, shardID, namespace, runID, []p.BlobEntry{
		{BlobID: blobID, Encoding: "json", Payload: payload},
	}))

	got, err := blobStore.BatchGet(ctx, shardID, namespace, runID, []ids.UID{blobID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, blobID, got[0].BlobID)
	require.Equal(t, "json", got[0].Encoding)
	require.Equal(t, payload, got[0].Payload)
}

func TestBlobStore_BatchInsertAndGet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := blobScope(t)

	entries := []p.BlobEntry{
		{BlobID: ids.NewUID(), Encoding: "raw", Payload: []byte("a")},
		{BlobID: ids.NewUID(), Encoding: "json", Payload: []byte(`{"n":1}`)},
		{BlobID: ids.NewUID(), Encoding: "proto", Payload: []byte{0x00, 0x01, 0xff}},
	}
	require.NoError(t, blobStore.BatchInsert(ctx, shardID, namespace, runID, entries))

	idsToGet := []ids.UID{entries[0].BlobID, entries[1].BlobID, entries[2].BlobID}
	got, err := blobStore.BatchGet(ctx, shardID, namespace, runID, idsToGet)
	require.NoError(t, err)
	require.Len(t, got, 3)

	byID := blobByID(got)
	for _, want := range entries {
		have, ok := byID[want.BlobID]
		require.True(t, ok, "missing blob %s", want.BlobID)
		require.Equal(t, want.Encoding, have.Encoding)
		require.Equal(t, want.Payload, have.Payload)
	}
}

// Missing IDs are omitted (not an error); callers must check completeness.
func TestBlobStore_BatchGetPartial(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := blobScope(t)

	present := ids.NewUID()
	missing := ids.NewUID()
	require.NoError(t, blobStore.BatchInsert(ctx, shardID, namespace, runID, []p.BlobEntry{
		{BlobID: present, Encoding: "raw", Payload: []byte("ok")},
	}))

	got, err := blobStore.BatchGet(ctx, shardID, namespace, runID, []ids.UID{present, missing})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, present, got[0].BlobID)
}

// Re-inserting the same primary key is a no-op (whole-RPC retry safety).
func TestBlobStore_InsertIdempotentSamePayload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := blobScope(t)

	blobID := ids.NewUID()
	entry := p.BlobEntry{BlobID: blobID, Encoding: "raw", Payload: []byte("same")}
	require.NoError(t, blobStore.BatchInsert(ctx, shardID, namespace, runID, []p.BlobEntry{entry}))
	require.NoError(t, blobStore.BatchInsert(ctx, shardID, namespace, runID, []p.BlobEntry{entry}))

	got, err := blobStore.BatchGet(ctx, shardID, namespace, runID, []ids.UID{blobID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, []byte("same"), got[0].Payload)
}

// First writer wins: a conflicting payload for an existing ID is ignored.
func TestBlobStore_InsertConflictKeepsFirstPayload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := blobScope(t)

	blobID := ids.NewUID()
	require.NoError(t, blobStore.BatchInsert(ctx, shardID, namespace, runID, []p.BlobEntry{
		{BlobID: blobID, Encoding: "raw", Payload: []byte("first")},
	}))
	require.NoError(t, blobStore.BatchInsert(ctx, shardID, namespace, runID, []p.BlobEntry{
		{BlobID: blobID, Encoding: "json", Payload: []byte("second")},
	}))

	got, err := blobStore.BatchGet(ctx, shardID, namespace, runID, []ids.UID{blobID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "raw", got[0].Encoding)
	require.Equal(t, []byte("first"), got[0].Payload)
}

func TestBlobStore_IsolatedByNamespace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID := nextShardID(t)
	runID := "run-" + ids.NewUID().String()
	nsA := "blobns-a-" + ids.NewUID().String()
	nsB := "blobns-b-" + ids.NewUID().String()
	blobID := ids.NewUID()

	require.NoError(t, blobStore.BatchInsert(ctx, shardID, nsA, runID, []p.BlobEntry{
		{BlobID: blobID, Encoding: "raw", Payload: []byte("in-a")},
	}))

	got, err := blobStore.BatchGet(ctx, shardID, nsB, runID, []ids.UID{blobID})
	require.NoError(t, err)
	require.Empty(t, got)

	got, err = blobStore.BatchGet(ctx, shardID, nsA, runID, []ids.UID{blobID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, []byte("in-a"), got[0].Payload)
}

func TestBlobStore_IsolatedByRunID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, _ := blobScope(t)
	runA := "run-a-" + ids.NewUID().String()
	runB := "run-b-" + ids.NewUID().String()
	blobID := ids.NewUID()

	require.NoError(t, blobStore.BatchInsert(ctx, shardID, namespace, runA, []p.BlobEntry{
		{BlobID: blobID, Encoding: "raw", Payload: []byte("in-a")},
	}))

	got, err := blobStore.BatchGet(ctx, shardID, namespace, runB, []ids.UID{blobID})
	require.NoError(t, err)
	require.Empty(t, got)

	got, err = blobStore.BatchGet(ctx, shardID, namespace, runA, []ids.UID{blobID})
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestBlobStore_IsolatedByShardID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardA := nextShardID(t)
	shardB := nextShardID(t)
	namespace := "blobns-" + ids.NewUID().String()
	runID := "run-" + ids.NewUID().String()
	blobID := ids.NewUID()

	require.NoError(t, blobStore.BatchInsert(ctx, shardA, namespace, runID, []p.BlobEntry{
		{BlobID: blobID, Encoding: "raw", Payload: []byte("in-a")},
	}))

	got, err := blobStore.BatchGet(ctx, shardB, namespace, runID, []ids.UID{blobID})
	require.NoError(t, err)
	require.Empty(t, got)

	got, err = blobStore.BatchGet(ctx, shardA, namespace, runID, []ids.UID{blobID})
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestBlobStore_NilAndEmptyPayload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := blobScope(t)

	nilID := ids.NewUID()
	emptyID := ids.NewUID()
	require.NoError(t, blobStore.BatchInsert(ctx, shardID, namespace, runID, []p.BlobEntry{
		{BlobID: nilID, Encoding: "raw", Payload: nil},
		{BlobID: emptyID, Encoding: "raw", Payload: []byte{}},
	}))

	got, err := blobStore.BatchGet(ctx, shardID, namespace, runID, []ids.UID{nilID, emptyID})
	require.NoError(t, err)
	require.Len(t, got, 2)

	byID := blobByID(got)
	require.Empty(t, byID[nilID].Payload)
	require.Empty(t, byID[emptyID].Payload)
}

func TestBlobStore_LargePayload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := blobScope(t)

	blobID := ids.NewUID()
	payload := make([]byte, 256*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	require.NoError(t, blobStore.BatchInsert(ctx, shardID, namespace, runID, []p.BlobEntry{
		{BlobID: blobID, Encoding: "raw", Payload: payload},
	}))

	got, err := blobStore.BatchGet(ctx, shardID, namespace, runID, []ids.UID{blobID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, payload, got[0].Payload)
}

// Concurrent inserts of distinct IDs under one run must all succeed.
func TestBlobStore_ConcurrentDistinctInserts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := blobScope(t)
	const n = 16

	idsToGet := make([]ids.UID, n)
	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < n; i++ {
		idsToGet[i] = ids.NewUID()
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := blobStore.BatchInsert(ctx, shardID, namespace, runID, []p.BlobEntry{
				{BlobID: idsToGet[i], Encoding: "raw", Payload: []byte{byte(i)}},
			})
			if err != nil {
				failures.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, int32(0), failures.Load())

	got, err := blobStore.BatchGet(ctx, shardID, namespace, runID, idsToGet)
	require.NoError(t, err)
	require.Len(t, got, n)
}

// Racing the same ID is safe: every caller succeeds and exactly one row exists.
func TestBlobStore_ConcurrentSameIDInsert(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := blobScope(t)
	blobID := ids.NewUID()
	const writers = 8

	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := blobStore.BatchInsert(ctx, shardID, namespace, runID, []p.BlobEntry{
				{BlobID: blobID, Encoding: "raw", Payload: []byte{byte(i)}},
			})
			if err != nil {
				failures.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, int32(0), failures.Load())

	got, err := blobStore.BatchGet(ctx, shardID, namespace, runID, []ids.UID{blobID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Len(t, got[0].Payload, 1)
}
