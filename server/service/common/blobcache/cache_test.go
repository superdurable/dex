// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package blobcache

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
)

func TestCacheGuaranteedAdmissionEvictsColdestEntry(t *testing.T) {
	payload := []byte("0123456789")
	maxBytes := cacheEntrySize(t, "entry-a", payload) * 2
	cache := newTestCache(t, maxBytes)

	requireCached(t, cache, "entry-a", payload)
	requireCached(t, cache, "entry-b", payload)
	_, found, err := cache.Get("entry-a")
	require.NoError(t, err)
	require.True(t, found)

	result, err := cache.Put("entry-c", payload)
	require.NoError(t, err)
	require.True(t, result.Cached)
	require.Equal(t, 1, result.Evicted)
	requireCacheHit(t, cache, "entry-a", payload)
	requireCacheMiss(t, cache, "entry-b")
	requireCacheHit(t, cache, "entry-c", payload)
}

func TestCacheGuaranteedAdmissionUsesOldestAccessAsTieBreaker(t *testing.T) {
	payload := []byte("0123456789")
	maxBytes := cacheEntrySize(t, "entry-a", payload) * 2
	cache := newTestCache(t, maxBytes)

	requireCached(t, cache, "entry-a", payload)
	requireCached(t, cache, "entry-b", payload)
	result, err := cache.Put("entry-c", payload)
	require.NoError(t, err)
	require.True(t, result.Cached)
	require.Equal(t, 1, result.Evicted)
	requireCacheMiss(t, cache, "entry-a")
	requireCacheHit(t, cache, "entry-b", payload)
	requireCacheHit(t, cache, "entry-c", payload)
}

func TestCacheEvictionUsesBlobIDAsFinalTieBreaker(t *testing.T) {
	entries := entryHeap{
		{blobID: "entry-b", hitCount: 1, accessSequence: 2},
		{blobID: "entry-a", hitCount: 1, accessSequence: 2},
	}
	require.True(t, entries.Less(1, 0))
}

func TestCacheRepeatedPutUpdatesHeat(t *testing.T) {
	payload := []byte("0123456789")
	maxBytes := cacheEntrySize(t, "entry-a", payload) * 2
	cache := newTestCache(t, maxBytes)

	requireCached(t, cache, "entry-a", payload)
	requireCached(t, cache, "entry-b", payload)
	requireCached(t, cache, "entry-a", payload)
	requireCached(t, cache, "entry-c", payload)
	requireCacheHit(t, cache, "entry-a", payload)
	requireCacheMiss(t, cache, "entry-b")
	requireCacheHit(t, cache, "entry-c", payload)
}

func TestCacheGuaranteedAdmissionEvictsUntilCapacityFits(t *testing.T) {
	smallPayload := []byte("small")
	largePayload := make([]byte, 80)
	maxBytes := cacheEntrySize(t, "entry-a", smallPayload) * 4
	cache := newTestCache(t, maxBytes)

	requireCached(t, cache, "entry-a", smallPayload)
	requireCached(t, cache, "entry-b", smallPayload)
	requireCached(t, cache, "entry-c", smallPayload)
	result, err := cache.Put("entry-d", largePayload)
	require.NoError(t, err)
	require.True(t, result.Cached)
	require.GreaterOrEqual(t, result.Evicted, 2)
	requireCacheHit(t, cache, "entry-d", largePayload)
	require.LessOrEqual(t, cache.usedBytesForTest(), maxBytes)
	require.LessOrEqual(t, regularFileBytes(t, cache.cfg.Directory), maxBytes)
}

func TestCacheOversizedBypassAndImmutableContent(t *testing.T) {
	cache := newTestCache(t, 64)
	result, err := cache.Put("oversized", make([]byte, 128))
	require.NoError(t, err)
	require.False(t, result.Cached)
	require.Zero(t, result.Evicted)
	requireCacheMiss(t, cache, "oversized")

	largeCache := newTestCache(t, 1<<20)
	result, err = largeCache.Put("immutable", []byte("first"))
	require.NoError(t, err)
	require.True(t, result.Cached)
	result, err = largeCache.Put("immutable", []byte("first"))
	require.NoError(t, err)
	require.True(t, result.Cached)
	result, err = largeCache.Put("immutable", []byte("second"))
	require.ErrorIs(t, err, ErrContentMismatch)
	require.False(t, result.Cached)
}

func TestCacheWarmRestartResetsHeat(t *testing.T) {
	rootDir := t.TempDir()
	payload := []byte("data")
	cfg := &config.BlobCacheConfig{
		Directory: rootDir,
		MaxBytes:  cacheEntrySize(t, "warm-a", payload) * 2,
	}
	firstCache, err := New(cfg)
	require.NoError(t, err)
	requireCached(t, firstCache, "warm-a", payload)
	requireCached(t, firstCache, "warm-b", payload)
	requireCacheHit(t, firstCache, "warm-a", payload)
	require.NoError(t, firstCache.Close())

	secondCache, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, secondCache.Close())
	})
	requireCached(t, secondCache, "warm-c", payload)
	requireCacheMiss(t, secondCache, "warm-a")
	requireCacheHit(t, secondCache, "warm-b", payload)
	requireCacheHit(t, secondCache, "warm-c", payload)
}

func TestCacheCorruptionFailsOnceThenBecomesMiss(t *testing.T) {
	cache := newTestCache(t, 1<<20)
	requireCached(t, cache, "corrupt", []byte("payload"))
	entryPath := cache.entryPathForTest("corrupt")
	file, err := os.OpenFile(entryPath, os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = file.WriteAt([]byte{0xff}, fixedHeaderSize)
	require.NoError(t, err)
	require.NoError(t, file.Close())

	_, found, err := cache.Get("corrupt")
	require.ErrorIs(t, err, ErrCorrupt)
	require.False(t, found)
	require.NoFileExists(t, entryPath)
	requireCacheMiss(t, cache, "corrupt")
}

func TestCacheEvictionWaitsForActiveReader(t *testing.T) {
	rootDir := t.TempDir()
	payload := []byte("payload")
	maxBytes := cacheEntrySize(t, "entry-a", payload)
	cfg := &config.BlobCacheConfig{Directory: rootDir, MaxBytes: maxBytes}
	baseStore, err := newLocalFileStore(rootDir)
	require.NoError(t, err)
	blockingStore := &blockingReadStore{
		fileStore:    baseStore,
		readStarted:  make(chan struct{}),
		releaseRead:  make(chan struct{}),
		removeCalled: make(chan struct{}, 1),
	}
	cache, err := newCache(cfg, blockingStore)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})
	requireCached(t, cache, "entry-a", payload)

	getDone := make(chan error, 1)
	go func() {
		_, _, getErr := cache.Get("entry-a")
		getDone <- getErr
	}()
	<-blockingStore.readStarted
	putDone := make(chan error, 1)
	go func() {
		_, putErr := cache.Put("entry-b", payload)
		putDone <- putErr
	}()
	require.Eventually(t, func() bool {
		return cache.entryStateForTest("entry-a") == entryEvicted
	}, 2*time.Second, 10*time.Millisecond)
	select {
	case <-blockingStore.removeCalled:
		t.Fatal("eviction removed a leased file")
	default:
	}
	close(blockingStore.releaseRead)
	require.NoError(t, <-getDone)
	require.NoError(t, <-putDone)
	select {
	case <-blockingStore.removeCalled:
	default:
		t.Fatal("eviction did not remove the released file")
	}
}

func TestCacheEvictionFailureIsStrictAndRetryable(t *testing.T) {
	rootDir := t.TempDir()
	payload := []byte("payload")
	maxBytes := cacheEntrySize(t, "entry-a", payload)
	cfg := &config.BlobCacheConfig{Directory: rootDir, MaxBytes: maxBytes}
	baseStore, err := newLocalFileStore(rootDir)
	require.NoError(t, err)
	failingStore := &failOnceRemoveStore{fileStore: baseStore}
	cache, err := newCache(cfg, failingStore)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})
	requireCached(t, cache, "entry-a", payload)
	failingStore.failNextRemove()
	result, err := cache.Put("entry-b", payload)
	require.ErrorIs(t, err, ErrReconciliation)
	require.False(t, result.Cached)

	result, err = cache.Put("entry-b", payload)
	require.NoError(t, err)
	require.True(t, result.Cached)
	requireCacheHit(t, cache, "entry-b", payload)
}

func newTestCache(t *testing.T, maxBytes int64) *Cache {
	t.Helper()
	cache, err := New(&config.BlobCacheConfig{Directory: t.TempDir(), MaxBytes: maxBytes})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})
	return cache
}

func cacheEntrySize(t *testing.T, blobID string, payload []byte) int64 {
	t.Helper()
	metadata, err := calculateMetadata(blobID, payload, filepath.Join(t.TempDir(), "entry"))
	require.NoError(t, err)
	return metadata.size
}

func requireCached(t *testing.T, cache *Cache, blobID string, payload []byte) {
	t.Helper()
	result, err := cache.Put(blobID, payload)
	require.NoError(t, err)
	require.True(t, result.Cached)
}

func requireCacheHit(t *testing.T, cache *Cache, blobID string, expected []byte) {
	t.Helper()
	payload, found, err := cache.Get(blobID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, expected, payload)
}

func requireCacheMiss(t *testing.T, cache *Cache, blobID string) {
	t.Helper()
	_, found, err := cache.Get(blobID)
	require.NoError(t, err)
	require.False(t, found)
}

func (cache *Cache) entryPathForTest(blobID string) string {
	cache.policyMu.Lock()
	defer cache.policyMu.Unlock()
	return cache.entries[blobID].path
}

func (cache *Cache) entryStateForTest(blobID string) entryState {
	cache.policyMu.Lock()
	entry := cache.entries[blobID]
	cache.policyMu.Unlock()
	if entry == nil {
		return entryEvicted
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.state
}

func (cache *Cache) usedBytesForTest() int64 {
	cache.policyMu.Lock()
	defer cache.policyMu.Unlock()
	return cache.usedBytes
}

type blockingReadStore struct {
	fileStore
	readStarted  chan struct{}
	releaseRead  chan struct{}
	removeCalled chan struct{}
	startOnce    sync.Once
}

type failOnceRemoveStore struct {
	fileStore
	mu         sync.Mutex
	failRemove bool
}

func (store *failOnceRemoveStore) failNextRemove() {
	store.mu.Lock()
	store.failRemove = true
	store.mu.Unlock()
}

func (store *failOnceRemoveStore) remove(path string) error {
	store.mu.Lock()
	shouldFail := store.failRemove
	store.failRemove = false
	store.mu.Unlock()
	if shouldFail {
		return errors.New("injected removal failure")
	}
	return store.fileStore.remove(path)
}

func (store *blockingReadStore) read(entry *diskEntry) ([]byte, error) {
	store.startOnce.Do(func() {
		close(store.readStarted)
	})
	<-store.releaseRead
	return store.fileStore.read(entry)
}

func (store *blockingReadStore) remove(path string) error {
	select {
	case store.removeCalled <- struct{}{}:
	default:
	}
	return store.fileStore.remove(path)
}

func regularFileBytes(t *testing.T, rootDir string) int64 {
	t.Helper()
	var total int64
	err := filepath.WalkDir(rootDir, func(path string, entry os.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			fileInfo, err := entry.Info()
			if err != nil {
				return err
			}
			total += fileInfo.Size()
		}
		return nil
	})
	require.NoError(t, err)
	return total
}
