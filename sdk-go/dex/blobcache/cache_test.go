// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package blobcache

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCacheRoundTripAndDelete(t *testing.T) {
	cache, rootDir := newTestCache(t, 1<<20)

	stringPayload := []byte("hello")
	cached, err := cache.Put("string-id", stringPayload)
	require.NoError(t, err)
	require.True(t, cached)

	objectPayload := []byte{0x0a, 0x04, 'j', 's', 'o', 'n'}
	cached, err = cache.Put("object-id", objectPayload)
	require.NoError(t, err)
	require.True(t, cached)

	actualString, found, err := cache.Get("string-id")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, stringPayload, actualString)
	actualString[0] = 'X'
	actualString, found, err = cache.Get("string-id")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, stringPayload, actualString)

	actualObject, found, err := cache.Get("object-id")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, objectPayload, actualObject)

	require.NoError(t, cache.Delete("string-id"))
	_, found, err = cache.Get("string-id")
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, cache.Delete("missing-id"))
	require.LessOrEqual(t, cacheOwnedBytes(t, rootDir), cache.cfg.MaxBytes)
}

func TestCacheRejectsOversizedAndMismatchedContent(t *testing.T) {
	cache, rootDir := newTestCache(t, 64)

	cached, err := cache.Put("oversized", make([]byte, 128))
	require.NoError(t, err)
	require.False(t, cached)
	require.Zero(t, cacheOwnedBytes(t, rootDir))

	smallCache, smallRootDir := newTestCache(t, 1<<20)
	require.NotEmpty(t, smallRootDir)
	cached, err = smallCache.Put("immutable-id", []byte("first"))
	require.NoError(t, err)
	require.True(t, cached)

	cached, err = smallCache.Put("immutable-id", []byte("first"))
	require.NoError(t, err)
	require.True(t, cached)

	cached, err = smallCache.Put("immutable-id", []byte("second"))
	require.ErrorIs(t, err, ErrContentMismatch)
	require.False(t, cached)
}

func TestCacheDeleteAllIsReusable(t *testing.T) {
	cache, rootDir := newTestCache(t, 1<<20)
	require.True(t, putForTest(t, cache, "first", []byte("one")))
	require.True(t, putForTest(t, cache, "second", []byte("two")))

	require.NoError(t, os.WriteFile(filepath.Join(rootDir, "tmp", "orphan.tmp"), []byte("partial"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(rootDir, "blobs", "corrupt.blob"), []byte("bad"), 0o600))

	require.NoError(t, cache.DeleteAll())
	require.Zero(t, cacheOwnedBytes(t, rootDir))
	_, found, err := cache.Get("first")
	require.NoError(t, err)
	require.False(t, found)

	require.True(t, putForTest(t, cache, "after-purge", []byte("fresh")))
	payload, found, err := cache.Get("after-purge")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("fresh"), payload)
}

func TestCacheEnforcesLogicalByteBudget(t *testing.T) {
	const maxBytes int64 = 256
	cache, rootDir := newTestCache(t, maxBytes)

	admitted := 0
	for index := 0; index < 40; index++ {
		blobID := "blob-" + strings.Repeat("x", index%7) + string(rune('A'+index))
		cached, err := cache.Put(blobID, make([]byte, 48))
		require.NoError(t, err)
		if cached {
			admitted++
		}
		require.LessOrEqual(t, cacheOwnedBytes(t, rootDir), maxBytes)
		require.Empty(t, regularFiles(t, filepath.Join(rootDir, "tmp")))
	}
	require.Positive(t, admitted)
}

func TestCacheValidatesInputsAndClosedState(t *testing.T) {
	require.Panics(t, func() {
		cache, err := New(nil)
		require.Nil(t, cache)
		require.NoError(t, err)
	})

	_, err := New(&Config{Dir: t.TempDir(), MaxBytes: 0})
	require.ErrorIs(t, err, ErrInvalidConfig)

	cache, rootDir := newTestCache(t, 1<<20)
	require.NotEmpty(t, rootDir)
	_, err = cache.Put("", nil)
	require.ErrorIs(t, err, ErrInvalidBlob)

	require.NoError(t, cache.Close())
	require.NoError(t, cache.Close())
	_, _, err = cache.Get("id")
	require.ErrorIs(t, err, ErrClosed)
	_, err = cache.Put("id", nil)
	require.ErrorIs(t, err, ErrClosed)
	require.ErrorIs(t, cache.Delete("id"), ErrClosed)
	require.ErrorIs(t, cache.DeleteAll(), ErrClosed)
}

func TestCacheUsesSafeHashedPaths(t *testing.T) {
	cache, rootDir := newTestCache(t, 1<<20)
	blobID := "../../../../outside/雪"
	require.True(t, putForTest(t, cache, blobID, []byte("safe")))

	entry, found := cache.policy.Get(blobID)
	require.True(t, found)
	relativePath, err := filepath.Rel(rootDir, entry.path)
	require.NoError(t, err)
	require.False(t, strings.HasPrefix(relativePath, ".."))
	require.Regexp(t, `^blobs[/\\][0-9a-f]{2}[/\\][0-9a-f]{2}[/\\][0-9a-f]{64}\.blob$`, relativePath)

	fileInfo, err := os.Stat(entry.path)
	require.NoError(t, err)
	require.Zero(t, fileInfo.Mode().Perm()&0o077)
}

func newTestCache(t *testing.T, maxBytes int64) (*Cache, string) {
	t.Helper()

	rootDir := t.TempDir()
	cache, err := New(&Config{
		Dir:               rootDir,
		MaxBytes:          maxBytes,
		FrequencyCounters: 10_000,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})
	return cache, rootDir
}

func putForTest(t *testing.T, cache *Cache, blobID string, payload []byte) bool {
	t.Helper()

	cached, err := cache.Put(blobID, payload)
	require.NoError(t, err)
	return cached
}

func cacheOwnedBytes(t *testing.T, rootDir string) int64 {
	t.Helper()

	var total int64
	for _, subdirectory := range []string{"tmp", "blobs"} {
		for _, path := range regularFiles(t, filepath.Join(rootDir, subdirectory)) {
			fileInfo, err := os.Stat(path)
			require.NoError(t, err)
			total += fileInfo.Size()
		}
	}
	return total
}

func regularFiles(t *testing.T, rootDir string) []string {
	t.Helper()

	collector := &regularFileCollector{}
	err := filepath.WalkDir(rootDir, collector.visit)
	require.NoError(t, err)
	return collector.paths
}

type regularFileCollector struct {
	paths []string
}

func (collector *regularFileCollector) visit(
	path string,
	entry os.DirEntry,
	walkErr error,
) error {
	if errors.Is(walkErr, os.ErrNotExist) {
		return nil
	}
	if walkErr != nil {
		return walkErr
	}
	if entry.Type().IsRegular() {
		collector.paths = append(collector.paths, path)
	}
	return nil
}
