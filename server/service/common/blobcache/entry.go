// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package blobcache

import (
	"container/heap"
	"sync"
)

type entryState uint8

const (
	entryPending entryState = iota
	entryReady
	entryEvicted
)

type diskEntry struct {
	blobID         string
	path           string
	size           int64
	checksum       uint32
	hitCount       uint64
	accessSequence uint64
	heapIndex      int

	mu      sync.Mutex
	readers sync.WaitGroup
	state   entryState
}

func newPendingEntry(metadata fileMetadata) *diskEntry {
	return &diskEntry{
		blobID:    metadata.blobID,
		path:      metadata.path,
		size:      metadata.size,
		checksum:  metadata.checksum,
		heapIndex: -1,
		state:     entryPending,
	}
}

func newReadyEntry(metadata fileMetadata) *diskEntry {
	entry := newPendingEntry(metadata)
	entry.state = entryReady
	return entry
}

func (entry *diskEntry) acquireRead() bool {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.state != entryReady {
		return false
	}
	entry.readers.Add(1)
	return true
}

func (entry *diskEntry) releaseRead() {
	entry.readers.Done()
}

func (entry *diskEntry) beginEviction() bool {
	entry.mu.Lock()
	if entry.state == entryEvicted {
		entry.mu.Unlock()
		return false
	}
	wasReady := entry.state == entryReady
	entry.state = entryEvicted
	entry.mu.Unlock()
	if wasReady {
		entry.readers.Wait()
	}
	return wasReady
}

func (entry *diskEntry) markReady() bool {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.state != entryPending {
		return false
	}
	entry.state = entryReady
	return true
}

type entryHeap []*diskEntry

func (entries entryHeap) Len() int {
	return len(entries)
}

func (entries entryHeap) Less(leftIndex, rightIndex int) bool {
	left := entries[leftIndex]
	right := entries[rightIndex]
	if left.hitCount != right.hitCount {
		return left.hitCount < right.hitCount
	}
	if left.accessSequence != right.accessSequence {
		return left.accessSequence < right.accessSequence
	}
	return left.blobID < right.blobID
}

func (entries entryHeap) Swap(leftIndex, rightIndex int) {
	entries[leftIndex], entries[rightIndex] = entries[rightIndex], entries[leftIndex]
	entries[leftIndex].heapIndex = leftIndex
	entries[rightIndex].heapIndex = rightIndex
}

func (entries *entryHeap) Push(value any) {
	entry := value.(*diskEntry)
	entry.heapIndex = len(*entries)
	*entries = append(*entries, entry)
}

func (entries *entryHeap) Pop() any {
	previous := *entries
	lastIndex := len(previous) - 1
	entry := previous[lastIndex]
	previous[lastIndex] = nil
	entry.heapIndex = -1
	*entries = previous[:lastIndex]
	return entry
}

func (entries *entryHeap) remove(entry *diskEntry) {
	if entry.heapIndex < 0 {
		return
	}
	heap.Remove(entries, entry.heapIndex)
}
