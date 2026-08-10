// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package blobcache

import (
	"bytes"
	"container/heap"
	"errors"
	"fmt"
	"math"
	"os"
	"sync"

	"github.com/superdurable/dex/config"
)

var (
	// ErrClosed indicates use after cache shutdown.
	ErrClosed = errors.New("blob cache is closed")
	// ErrInvalidConfig indicates invalid cache configuration.
	ErrInvalidConfig = errors.New("invalid blob cache configuration")
	// ErrInvalidBlob indicates invalid cache input.
	ErrInvalidBlob = errors.New("invalid blob")
	// ErrContentMismatch indicates mutable content under one key.
	ErrContentMismatch = errors.New("blob ID content mismatch")
	// ErrCorrupt indicates malformed cached data.
	ErrCorrupt = errors.New("corrupt blob cache entry")
	// ErrReconciliation indicates incomplete disk cleanup.
	ErrReconciliation = errors.New("blob cache reconciliation failed")
)

// PutResult describes one cache write.
type PutResult struct {
	Cached  bool
	Evicted int
}

// Cache stores immutable Attribute blobs on disk.
type Cache struct {
	cfg   *config.BlobCacheConfig
	store fileStore

	lifecycleMu sync.RWMutex
	commitMu    sync.Mutex
	policyMu    sync.Mutex
	closed      bool

	entries            map[string]*diskEntry
	victims            entryHeap
	usedBytes          int64
	nextAccessSequence uint64
	cleanupBacklog     map[string]struct{}
}

// New creates and recovers an exclusively owned cache directory.
func New(cfg *config.BlobCacheConfig) (*Cache, error) {
	if cfg == nil {
		panic("blobcache.New requires BlobCacheConfig")
	}
	if cfg.Directory == "" {
		return nil, fmt.Errorf("%w: directory must not be empty", ErrInvalidConfig)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	store, err := newLocalFileStore(cfg.Directory)
	if err != nil {
		return nil, err
	}
	return newCache(cfg, store)
}

func newCache(cfg *config.BlobCacheConfig, store fileStore) (*Cache, error) {
	if err := store.prepare(); err != nil {
		return nil, err
	}
	cache := &Cache{
		cfg:            cfg,
		store:          store,
		entries:        make(map[string]*diskEntry),
		cleanupBacklog: make(map[string]struct{}),
	}
	heap.Init(&cache.victims)
	if err := cache.recover(); err != nil {
		return nil, err
	}
	return cache, nil
}

// Get returns a copied payload and records access heat.
func (cache *Cache) Get(blobID string) ([]byte, bool, error) {
	if err := validateBlobID(blobID); err != nil {
		return nil, false, err
	}
	cache.lifecycleMu.RLock()
	defer cache.lifecycleMu.RUnlock()
	if cache.closed {
		return nil, false, ErrClosed
	}
	entry, found := cache.acquireEntry(blobID)
	if !found {
		return nil, false, nil
	}
	payload, err := cache.store.read(entry)
	entry.releaseRead()
	if err == nil {
		return payload, true, nil
	}
	if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, ErrCorrupt) {
		return nil, false, err
	}
	invalidateErr := cache.invalidateEntry(entry)
	return nil, false, errors.Join(err, invalidateErr)
}

// Put writes every capacity-eligible payload after evicting colder entries.
func (cache *Cache) Put(blobID string, payload []byte) (PutResult, error) {
	if err := validateBlobID(blobID); err != nil {
		return PutResult{}, err
	}
	cache.lifecycleMu.RLock()
	defer cache.lifecycleMu.RUnlock()
	if cache.closed {
		return PutResult{}, ErrClosed
	}
	cache.commitMu.Lock()
	defer cache.commitMu.Unlock()
	if err := cache.retryCleanup(); err != nil {
		return PutResult{}, err
	}
	metadata, err := calculateMetadata(blobID, payload, cache.store.pathFor(blobID))
	if err != nil {
		return PutResult{}, err
	}
	if metadata.size > cache.cfg.EffectiveMaxBytes() {
		return PutResult{}, nil
	}
	reused, found, err := cache.reuseExisting(metadata, payload)
	if err != nil {
		return PutResult{}, err
	}
	if found {
		return PutResult{Cached: reused}, nil
	}
	result := PutResult{}
	result.Evicted, err = cache.evictUntilFits(metadata.size)
	if err != nil {
		return result, err
	}
	commitResult, err := cache.store.commit(metadata, payload)
	if err != nil {
		if commitResult.orphanPath != "" {
			cache.addCleanupPath(commitResult.orphanPath)
		}
		return result, err
	}
	entry := newPendingEntry(metadata)
	if !entry.markReady() {
		panic("committed blob cache entry left pending state")
	}
	cache.addEntry(entry, 1)
	result.Cached = true
	return result, nil
}

// Close preserves committed files and releases the cache lifecycle.
func (cache *Cache) Close() error {
	cache.lifecycleMu.Lock()
	defer cache.lifecycleMu.Unlock()
	if cache.closed {
		return nil
	}
	cache.commitMu.Lock()
	defer cache.commitMu.Unlock()
	cleanupErr := cache.retryCleanup()
	cache.closed = true
	return cleanupErr
}

func (cache *Cache) recover() error {
	cache.commitMu.Lock()
	defer cache.commitMu.Unlock()
	if err := cache.store.purgeTemp(); err != nil {
		return fmt.Errorf("%w: %v", ErrReconciliation, err)
	}
	scanned, err := cache.store.scan()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrReconciliation, err)
	}
	for _, invalidPath := range scanned.invalidPaths {
		if err := cache.store.remove(invalidPath); err != nil {
			return fmt.Errorf("%w: %v", ErrReconciliation, err)
		}
	}
	for _, metadata := range scanned.entries {
		if err := cache.recoverEntry(metadata); err != nil {
			return err
		}
	}
	return nil
}

func (cache *Cache) recoverEntry(metadata fileMetadata) error {
	if metadata.size > cache.cfg.EffectiveMaxBytes() {
		if err := cache.store.remove(metadata.path); err != nil {
			return fmt.Errorf("%w: %v", ErrReconciliation, err)
		}
		return nil
	}
	if _, err := cache.evictUntilFits(metadata.size); err != nil {
		return err
	}
	cache.addEntry(newReadyEntry(metadata), 0)
	return nil
}

func (cache *Cache) reuseExisting(
	metadata fileMetadata,
	payload []byte,
) (bool, bool, error) {
	entry, found := cache.acquireEntry(metadata.blobID)
	if !found {
		return false, false, nil
	}
	existingPayload, err := cache.store.read(entry)
	entry.releaseRead()
	if err != nil {
		invalidateErr := cache.invalidateEntryLocked(entry)
		return false, true, errors.Join(err, invalidateErr)
	}
	if entry.size != metadata.size || entry.checksum != metadata.checksum ||
		!bytes.Equal(existingPayload, payload) {
		return false, true, fmt.Errorf("%w: blob %q", ErrContentMismatch, metadata.blobID)
	}
	return true, true, nil
}

func (cache *Cache) evictUntilFits(requiredBytes int64) (int, error) {
	evicted := 0
	for !cache.hasCapacity(requiredBytes) {
		entry, found := cache.detachColdestEntry()
		if !found {
			return evicted, fmt.Errorf("%w: no eviction candidate", ErrReconciliation)
		}
		entry.beginEviction()
		if err := cache.store.remove(entry.path); err != nil {
			cache.addCleanupPath(entry.path)
			return evicted, fmt.Errorf("%w: %v", ErrReconciliation, err)
		}
		evicted++
	}
	return evicted, nil
}

func (cache *Cache) hasCapacity(requiredBytes int64) bool {
	cache.policyMu.Lock()
	defer cache.policyMu.Unlock()
	return cache.usedBytes <= cache.cfg.EffectiveMaxBytes()-requiredBytes
}

func (cache *Cache) detachColdestEntry() (*diskEntry, bool) {
	cache.policyMu.Lock()
	defer cache.policyMu.Unlock()
	if len(cache.victims) == 0 {
		return nil, false
	}
	entry := heap.Pop(&cache.victims).(*diskEntry)
	delete(cache.entries, entry.blobID)
	cache.usedBytes -= entry.size
	return entry, true
}

func (cache *Cache) invalidateEntry(entry *diskEntry) error {
	cache.commitMu.Lock()
	defer cache.commitMu.Unlock()
	return cache.invalidateEntryLocked(entry)
}

func (cache *Cache) invalidateEntryLocked(entry *diskEntry) error {
	if !cache.detachEntry(entry) {
		return nil
	}
	entry.beginEviction()
	if err := cache.store.remove(entry.path); err != nil {
		cache.addCleanupPath(entry.path)
		return err
	}
	return nil
}

func (cache *Cache) detachEntry(entry *diskEntry) bool {
	cache.policyMu.Lock()
	defer cache.policyMu.Unlock()
	current, found := cache.entries[entry.blobID]
	if !found || current != entry {
		return false
	}
	cache.victims.remove(entry)
	delete(cache.entries, entry.blobID)
	cache.usedBytes -= entry.size
	return true
}

func (cache *Cache) acquireEntry(blobID string) (*diskEntry, bool) {
	cache.policyMu.Lock()
	defer cache.policyMu.Unlock()
	entry, found := cache.entries[blobID]
	if !found || !entry.acquireRead() {
		return nil, false
	}
	cache.touchEntry(entry)
	return entry, true
}

func (cache *Cache) addEntry(entry *diskEntry, hitCount uint64) {
	cache.policyMu.Lock()
	defer cache.policyMu.Unlock()
	cache.nextAccessSequence++
	entry.hitCount = hitCount
	entry.accessSequence = cache.nextAccessSequence
	cache.entries[entry.blobID] = entry
	cache.usedBytes += entry.size
	heap.Push(&cache.victims, entry)
}

func (cache *Cache) touchEntry(entry *diskEntry) {
	if entry.hitCount < math.MaxUint64 {
		entry.hitCount++
	}
	cache.nextAccessSequence++
	entry.accessSequence = cache.nextAccessSequence
	heap.Fix(&cache.victims, entry.heapIndex)
}

func (cache *Cache) retryCleanup() error {
	var cleanupErr error
	for path := range cache.cleanupBacklog {
		if err := cache.store.remove(path); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
		delete(cache.cleanupBacklog, path)
	}
	if cleanupErr != nil {
		return fmt.Errorf("%w: %v", ErrReconciliation, cleanupErr)
	}
	return nil
}

func (cache *Cache) addCleanupPath(path string) {
	cache.cleanupBacklog[path] = struct{}{}
}
