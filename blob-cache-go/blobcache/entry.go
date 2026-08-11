// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

// This file defines the concurrency state machine for one disk entry. A
// pending entry reserves policy capacity but cannot be read before its file
// commit; a ready entry grants read leases; an evicted entry rejects new
// readers and waits for existing leases before removal. For example, when
// Delete races with Get, the active Get finishes reading a complete file,
// Delete waits for that lease, and subsequent Gets observe a clean miss
// instead of partial bytes.

package blobcache

import "sync"

type entryState uint8

const (
	entryPending entryState = iota
	entryReady
	entryEvicted
)

type diskEntry struct {
	blobID   string
	path     string
	size     int64
	checksum uint32

	mu      sync.Mutex
	readers sync.WaitGroup
	state   entryState
}

func newPendingEntry(metadata fileMetadata) *diskEntry {
	return &diskEntry{
		blobID:   metadata.blobID,
		path:     metadata.path,
		size:     metadata.size,
		checksum: metadata.checksum,
		state:    entryPending,
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

func (entry *diskEntry) restoreReady() {
	entry.mu.Lock()
	entry.state = entryReady
	entry.mu.Unlock()
}
