# Go SDK Disk Blob Cache Plan

Status: implemented as a standalone component; SDK hydration wiring remains a
follow-up.

## Summary

Add an independently implementable and testable disk blob cache under
`sdk-go/dex/blobcache`.

The cache is an optimization for server-minted blob IDs returned in
[`Value`](../../../protos/dex.proto). It is never a source of truth. A miss,
admission rejection, or cache failure must leave the caller able to use a fresh
`LoadBlobs` result.

The component owns payload files and uses
[`ristretto/v2`](https://github.com/dgraph-io/ristretto) only for in-memory
TinyLFU admission, SampledLFU eviction, and access-frequency tracking.
Ristretto never stores blob payload bytes.

This plan ends with a standalone component and its test gate. Wiring lazy
hydration into Go SDK Client/Worker code is a separate follow-up.

## Goals

- Store hydrated string and object blobs on disk, not in Go heap cache entries.
- Enforce a configured logical disk-byte limit before committing a new file.
- Protect frequently used blobs from one-use scan traffic.
- Preserve valid cache files across clean shutdown and process restart.
- Allow callers to explicitly purge the complete cache before shutdown.
- Recover safely from crashes, incomplete files, corruption, and a reduced
  restart budget.
- Support concurrent reads with serialized admission, eviction, deletion, and
  shutdown.
- Expose explicit hit, miss, rejection, and error outcomes to the future
  hydration coordinator.
- Test the component without Dex server, Temporal, or a Go SDK rewrite.

## Non-goals

- No server, IDL, Java SDK, Python SDK, or sample changes.
- No `LoadBlobs` RPC calls inside the cache package.
- No memory tier for payload bytes.
- No persistence of TinyLFU counters or exact eviction order across restart.
- No TTL; capacity and explicit deletion are the only removal policies.
- No shared cache directory across multiple processes.
- No operating-system disk quota or accounting for unrelated directory data.
- No backward-compatible wrapper around the current Go SDK.

## Existing contract

[`FlowService.LoadBlobs`](../../../protos/dex.proto) accepts only:

- `internal_blob_id_for_string_value`
- `internal_blob_id_for_obj_value`

It returns concrete `Value` messages keyed by blob ID. The future hydration
coordinator remains responsible for:

1. Reading string-versus-object semantics from the blob-id arm.
2. Looking up that ID in this component.
3. Calling `LoadBlobs` on a miss.
4. Encoding the concrete response for `Put`.
5. Using the fresh response even if `Put` rejects or fails.

The cache package does not depend on a gRPC client or generated service stub.

## Library decision

Pin `github.com/dgraph-io/ristretto/v2` in
[`sdk-go/go.mod`](../../../sdk-go/go.mod). The initial implementation uses
v2.4.2.

Ristretto provides the desired frequency policy, but its asynchronous API
requires an adapter:

- `Set=false` means the write was dropped before policy evaluation.
- `Set=true` does not mean admission succeeded.
- `Wait()` drains buffered writes; `Get()` after `Wait()` confirms admission.
- `OnEvict` runs while buffered writes are processed and before the following
  `Wait()` barrier completes.
- `OnReject` reports TinyLFU policy rejection.
- `Close()` calls `Clear()`, which invokes eviction callbacks for retained
  entries.
- `OnExit` also runs for rejection, eviction, deletion, and close.

Therefore:

- Disk deletion happens in a cache method reached from `OnEvict`, not `OnExit`.
- The eviction method skips disk deletion while the cache is closing.
- Explicit `Delete` removes the file itself because Ristretto `Del` does not
  invoke `OnEvict`.
- Every policy mutation is followed by `Wait()` before the component reports a
  stable result.

Ristretto is configured with:

```go
ristretto.Config[string, *diskEntry]{
    NumCounters:       cfg.FrequencyCounters,
    MaxCost:           cfg.MaxBytes,
    BufferItems:       64,
    IgnoreInternalCost: true,
}
```

Each Ristretto cost is the complete logical cache-file size. Public
`FrequencyCounters` maps to Ristretto's `NumCounters`; it controls
frequency-sketch memory and accuracy, not disk capacity or an exact rolling
access window. For a budget expected to hold about 1,000 blobs,
`FrequencyCounters=10_000` follows Ristretto's recommended starting point of
roughly 10 counters per cached item. The counters approximately distinguish
frequently reused blobs from one-use scan traffic; they do not retain exact
per-key read histories. `FrequencyCounters=1_000` still supports those 1,000
disk entries, but increases sketch collisions and scan pollution. It saves
policy memory while potentially reducing cache hit ratio; correctness and
`MaxBytes` enforcement are unchanged.

## Package and files

```text
sdk-go/dex/blobcache/
  cache.go
  config.go
  entry.go
  file_store.go
  format.go
  policy.go
  cache_test.go
  recovery_test.go
  concurrency_test.go
```

Responsibilities:

- `cache.go`: lifecycle and public operations.
- `config.go`: defaults and validation.
- `entry.go`: metadata, state transitions, and read leases.
- `file_store.go`: safe paths, scans, atomic writes, reads, and removals.
- `format.go`: versioned header and checksum.

Ristretto callbacks delegate directly to methods on `Cache`; do not use
stateful closures for callback behavior.

## API

```go
package blobcache

type Config struct {
  Dir               string
  MaxBytes          int64
  FrequencyCounters int64
}

type Cache struct {
    cfg *Config
}

func New(cfg *Config) (*Cache, error)

func (c *Cache) Get(blobID string) (payload []byte, found bool, err error)

func (c *Cache) Put(blobID string, payload []byte) (cached bool, err error)

func (c *Cache) Delete(blobID string) error

func (c *Cache) DeleteAll() error

func (c *Cache) Close() error
```

Semantics:

- `New(nil)` panics because configuration is a required dependency.
- `Dir` must be non-empty and exclusively owned by one cache process.
- `MaxBytes` must be positive.
- `FrequencyCounters=0` uses `10_000`; negative values are invalid.
- `Get` returns `found=false, err=nil` for a normal miss.
- `Put` returns `cached=false, err=nil` for oversized or policy-rejected data.
- `Put` returns `cached=false, err!=nil` for disk or reconciliation failures.
- `DeleteAll` purges the cache but leaves the open component reusable.
- `Close` is idempotent and preserves committed files.
- Operations after `Close` return a package sentinel error.

The package owns input `payload` only for the duration of `Put`; it does not
retain the slice. `Get` returns a new byte slice and treats it as opaque.

The hydration adapter stores concrete string bytes directly. For object blobs,
it deterministically marshals the complete `EncodedObject`, preserving both
`encoding` and `payload`. On a cache hit, the original blob-id arm tells the
adapter which representation to decode. A malformed cached `EncodedObject` is
deleted and reloaded through `LoadBlobs`.

## In-memory model

Ristretto values contain metadata only:

```go
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
```

An entry supplies two internal operations:

- `acquireRead`: succeeds only for `entryReady`, then registers a reader.
- `beginEviction`: changes ready to evicted and waits for registered readers.

This guarantees that a concurrent `Get` either:

- opens and reads a complete immutable file before deletion, or
- observes an evicted entry and returns a miss.

It also avoids relying on platform-specific behavior when deleting an open
file.

`Cache` additionally owns:

- a lifecycle `sync.RWMutex`;
- `commitMu` for `Put`, `Delete`, `DeleteAll`, recovery admission, and `Close`;
- an atomic closing/closed state;
- a cleanup backlog for files that could not be removed;
- the Ristretto policy;
- a constructor-injected file-store implementation.

Reads may run concurrently. All operations that can change disk ownership or
policy cost are serialized by `commitMu`.

## Disk layout

```text
BlobCacheDir/
  tmp/
    <unique>.tmp
  blobs/
    ab/
      cd/
        <sha256(blob-id)>.blob
```

- Hash the complete blob ID with SHA-256.
- Use the first four hex characters as two directory shards.
- Never concatenate a blob ID into a filesystem path.
- Store the original blob ID inside the file and verify that it hashes to the
  path during reads and recovery.
- Create cache directories with mode `0700` and new files with mode `0600`.
- Ignore non-regular files during scans and never follow them outside the cache
  root.

The cache root is an operationally exclusive directory. A Client and Worker in
the same process may share one `Cache` instance. Different processes must use
different directories.

## File format

Version 1 uses a fixed prefix followed by variable data:

```text
magic[4] = "DXBC"
version uint8
reserved uint24
blob_id_length uint32
payload_length uint64
crc32c uint32
blob_id bytes
payload bytes
```

- Integers use little-endian encoding.
- The fixed header is 24 bytes.
- CRC32C covers the blob ID and payload.
- The complete cost is prefix + blob ID + payload.
- Declared lengths must equal the regular file's actual size.
- Unknown versions, non-zero reserved bits, bad hashes, and checksum failures
  are corruption.
- Length arithmetic must reject overflow before allocating.
- Blob IDs larger than 1 MiB are rejected before writing or recovery allocation.

The format is private to this cache. A future incompatible format increments
the version and treats unsupported files as recoverable misses.

## Disk budget invariant

The invariant after `New` and after every completed mutation is:

```text
sum(size of cache-owned committed files and active temp files) <= MaxBytes
```

The invariant uses logical file sizes, including headers and blob IDs. It does
not promise filesystem-block, sparse-file, metadata, or `du` accounting.

Ristretto `MaxCost` equals `MaxBytes`. A pending candidate is admitted at its
complete final size before its temp file is written.

### Put transaction

```text
validate ID, lengths, and complete file size
  → if complete size > MaxBytes: cached=false
  → acquire lifecycle read lock and commitMu
  → retry cleanup backlog; stop on failure
  → if the same ready ID exists:
       same checksum/size/payload → record access and cached=true
       different content → integrity error
  → create pending diskEntry
  → policy.Set(blobID, entry, completeSize)
  → Set=false: cached=false, no file
  → policy.Wait()
  → capacity OnEvict callbacks drain readers and remove victim files
  → confirm the candidate with policy.Get
  → rejected candidate: cached=false, no file
  → any eviction failure:
       remove candidate metadata
       do not create its file
       retain failed victim in cleanup backlog
       return error
  → write header/blob ID/payload into tmp/<unique>.tmp
  → flush and close the temp file
  → atomically rename it to the final path
  → mark the candidate ready
  → cached=true
```

The temp directory and final directory are on the same filesystem. The
reservation already counts the temp file at full final size, so writing the
temporary file does not exceed the logical limit.

If writing, flushing, closing, or renaming fails:

1. Remove the candidate from Ristretto and call `Wait()`.
2. Remove the temporary file.
3. Add an unremovable temporary file to the cleanup backlog.
4. Return an error.

Evicted files may already be gone; cache population can shrink after a failed
candidate without affecting application data.

### Eviction deletion failures

Ristretto callbacks cannot return errors. The `OnEvict` method records the
first removal failure for the mutation currently holding `commitMu`.

The failed victim is already absent from Ristretto but still occupies disk. It
is added to the cleanup backlog. No new file is committed, and subsequent
`Put` operations retry the backlog before asking Ristretto for more capacity.
This prevents untracked files from allowing the directory to grow beyond
`MaxBytes`.

### Admission rejection

- `Set=false`: the set buffer dropped the candidate; no callback is expected.
- `Set=true` plus no candidate after `Wait`: TinyLFU rejected it.
- Both cases return `cached=false, err=nil`.
- The future hydration caller continues with the freshly loaded server value.

Do not assert which exact blob Ristretto chooses as a victim.

## Read path

```text
policy.Get(blobID)
  → miss: found=false
  → acquire entry read lease
  → open final file
  → validate path hash, header, lengths, and checksum
  → release read lease
  → return a new payload slice
```

`policy.Get` records the access for TinyLFU/SampledLFU before disk I/O.

If the file disappeared after policy lookup, invalidate the matching metadata
and return a miss. If a file is structurally corrupt or has a bad checksum,
invalidate and remove it, then return a miss. Failure to invalidate or remove
is returned as an error.

Other I/O failures are returned. The future SDK adapter must log/observe the
error and may degrade it to a `LoadBlobs` miss; it must not silently discard the
error.

## Delete

`Delete` is explicit cache invalidation, not source-data deletion:

1. Acquire the lifecycle read lock and `commitMu`.
2. Retry the cleanup backlog.
3. Find the current entry and mark it evicted.
4. Wait for its active readers.
5. Remove the final file.
6. Call Ristretto `Del` and `Wait`.

A missing key is successful. If file removal fails, restore the entry to ready
state and return the error without deleting its policy metadata.

## DeleteAll

`DeleteAll` is an explicit destructive cache operation. It removes every file
owned by the cache while leaving the component open and reusable:

1. Acquire the lifecycle write lock and `commitMu`.
2. Block new operations until the purge finishes.
3. Call Ristretto `Clear()`.
4. Allow `OnEvict` to drain readers and remove every retained final file.
5. Remove the complete `tmp/` and `blobs/` trees to catch cleanup-backlog,
   corrupt, and otherwise untracked cache files.
6. Recreate empty `tmp/` and `blobs/` directories.
7. Clear the cleanup backlog only after its files are absent.
8. Return an error if final removal or directory recreation failed.

Unlike `Close`, `DeleteAll` does not set the closing state. Ristretto
`Clear()` therefore invokes normal disk-removing `OnEvict` behavior and resets
its frequency counters.

The purge operates only on the two exact component-owned subdirectories under
the validated cache root. It never follows a caller-controlled path or deletes
the root's parent.

If any removal fails, Ristretto is empty but remaining files stay in the
cleanup backlog. Reads return misses, and later `Put` operations retry cleanup
before admitting new data. Calling `DeleteAll` again retries the complete
purge.

An `OnEvict` removal failure recovered by the complete subtree purge is handled,
not ignored. `DeleteAll` succeeds when the owned subdirectories are verified
empty and recreated.

`DeleteAll` after `Close` returns the closed-cache error. A shutdown path that
wants ephemeral cache behavior must call:

```go
purgeErr := cache.DeleteAll()
closeErr := cache.Close()
return errors.Join(purgeErr, closeErr)
```

The SDK must still close the cache if purge fails. Its shutdown orchestration
should retain both errors rather than skipping `Close`.

## Close and warm restart

`Close` takes the lifecycle write lock so no operation overlaps shutdown:

1. Mark the cache closing.
2. Finish or reject any current mutation.
3. Retry the cleanup backlog and remember any error.
4. Call Ristretto `Wait()`.
5. Call Ristretto `Close()`.
6. Mark the cache closed.

Ristretto `Close()` internally clears retained values and invokes eviction
callbacks. `OnEvict` checks the closing state and skips disk deletion during
this path. `OnExit` never owns disk deletion.

Committed files therefore survive clean shutdown. `Close` still completes the
in-memory shutdown if backlog cleanup fails, then returns that error.

Callers choose lifecycle semantics explicitly:

- `Close()` preserves the cache for warm restart.
- `DeleteAll()` followed by `Close()` provides ephemeral shutdown cleanup.

## Startup and crash recovery

`New` completes reconciliation before returning a usable cache:

1. Validate configuration and create the directory structure.
2. Remove every regular file under `tmp/`.
3. Scan regular `.blob` files under `blobs/`.
4. Validate header, lengths, blob ID, and path hash.
5. Remove invalid, truncated, oversized, duplicate, and unsupported files.
6. Sort candidates by modification time, newest first, with path as a stable
   tie-breaker.
7. Admit each file into Ristretto using its actual logical size.
8. Call `Wait()` and remove any dropped or rejected candidate.
9. Allow capacity callbacks to remove victims.
10. Fail construction if cleanup or reconciliation cannot restore the budget.

Startup performs structural validation without loading every payload into
memory at once. CRC32C verification occurs while streaming each candidate;
recovery may be I/O intensive but never allocates the whole cache.

Access-frequency counters are intentionally reset. Newest committed files get
deterministic restart preference, but exact pre-restart heat is not preserved.

Crash points are safe:

- Before rename: startup removes the incomplete temp file.
- After rename: startup discovers and admits the complete final file.
- During eviction: deleted entries are cache misses and reloadable from server.
- During failed cleanup: startup either removes the orphan or fails `New`.

## Duplicate IDs and immutability

Blob IDs are treated as immutable content identifiers:

- Re-putting the same ID, size, checksum, and payload records an access and
  reuses the file.
- Re-putting the same ID with different content returns an integrity error.
- A valid final file is never overwritten in place.
- Atomic rename is only used when the final path does not exist.

This prevents readers from observing partial or replaced content.

## Error and observability boundary

Define sentinel errors for:

- closed cache;
- invalid configuration;
- invalid blob ID;
- immutable-ID content mismatch;
- corrupted entry;
- disk-budget reconciliation failure.

Wrap filesystem errors with operation and cache-relative path. Do not include
payload bytes in errors or logs.

The first implementation does not expose a stable metrics API. Future SDK
wiring may add cache-hit, miss, rejection, bytes, and disk-error telemetry
without changing the storage contract.

For deterministic failure tests, the production constructor uses an
OS-backed file store and an unexported constructor accepts a file-store
dependency. This is constructor injection only; do not add setters.

## Implementation phases

### Phase 1 — format and file store

1. Add `Config`, blob-ID validation, safe path derivation, and directory
   creation.
2. Implement streaming encode/decode, CRC32C, length validation, and atomic
   temp-to-final commit.
3. Implement scan, remove, and duplicate-file detection.
4. Test disk layout, permissions, purge, corruption, and crash leftovers
   without Ristretto.

Exit criterion: the file store can independently round-trip, validate, scan,
and remove immutable entries.

### Phase 2 — Ristretto policy and exact budget

1. Add the pinned Ristretto dependency.
2. Implement metadata-only policy entries and read leases.
3. Implement reserve-before-write `Put`.
4. Implement capacity eviction, rejection detection, cleanup backlog, and
   explicit `Delete`.
5. Test the byte invariant and hot-entry behavior.

Exit criterion: every completed mutation respects `MaxBytes`; payload bytes
never reside in Ristretto.

### Phase 3 — lifecycle, recovery, and concurrency

1. Implement startup reconciliation and reduced-budget restart.
2. Implement closing-aware callbacks and warm restart.
3. Implement reusable `DeleteAll` and ephemeral shutdown cleanup.
4. Implement concurrent `Get` versus eviction/delete/purge and idempotent
   `Close`.
5. Add fault injection and race coverage.

Exit criterion: all component and race tests pass without server or Temporal.

### Phase 4 — standalone handoff

1. Add package documentation and the Makefile test target.
2. Document the future hydration adapter contract.
3. Stop before modifying Client, Worker, codec, or integration suites.

Exit criterion: the cache is ready to be consumed by the later Go SDK rewrite
through `Get`/`Put`, with no dependency on that rewrite.

## Tests

The user explicitly requested an independently testable component, so these
package-level tests are the primary test layer. They cover filesystem and
callback boundaries that cannot be reached reliably through a Temporal E2E
test.

Add:

```make
blobCacheTests:
	go test -race ./dex/blobcache
```

Run through the Makefile:

```text
make -C sdk-go blobCacheTests 2>&1 | tee /tmp/test-go-sdk-blob-cache.log
make copyright-check          2>&1 | tee /tmp/test-go-sdk-blob-cache-copyright.log
```

### File-store scenarios

| Scenario | Assertion |
|----------|-----------|
| String round trip | ID, payload, and complete size survive write/read |
| Object round trip | Opaque deterministic object bytes survive unchanged |
| Safe path | Arbitrary blob IDs cannot escape the cache root |
| Atomic visibility | Readers see no final file before rename and a complete file afterward |
| Format validation | Bad magic/version/reserved bits/lengths are rejected |
| Checksum validation | Payload and ID corruption are detected |
| Path verification | Header ID must hash to its sharded filename |
| Permission defaults | New directories/files use `0700`/`0600`, subject to stricter umask |
| Duplicate immutable ID | Same content reuses the file; different content errors |
| Overflow defense | Malicious lengths fail before allocation |

### Budget and policy scenarios

| Scenario | Assertion |
|----------|-----------|
| Metadata-only policy | Ristretto retains no payload byte slice |
| Exact file cost | Header + ID + payload count against `MaxBytes` |
| Oversized candidate | Returns `cached=false` and creates no temp/final file |
| Set-buffer drop | Returns `cached=false` and creates no file |
| TinyLFU rejection | Post-`Wait` rejection creates no file |
| Reserve before write | Required victims disappear before candidate temp write |
| Multiple-victim eviction | One large candidate can remove several smaller files |
| Byte invariant | Committed plus active temp logical bytes never exceed `MaxBytes` |
| Policy integration | Capacity decisions preserve the byte invariant and remove corresponding files |
| Ristretto behavior | Upstream tests cover probabilistic TinyLFU/SampledLFU retention choices |
| Delete | Removes disk and policy entry only after readers drain |
| Delete failure | Restores readable metadata and returns the error |
| DeleteAll | Removes every committed, temp, corrupt, and backlog file |
| DeleteAll reuse | A successful purge resets policy and accepts later puts |
| DeleteAll failure | Returns the error, retains backlog, and blocks new writes |
| Eviction failure | Aborts candidate, records backlog, and blocks further writes |
| Backlog recovery | Later mutation retries cleanup before reserving capacity |

Component tests do not assert a particular hot-key retention outcome.
Ristretto's production `BufferItems=64` uses an asynchronous, lossy Get buffer,
and `Wait()` flushes writes only. A deterministic assertion would require
non-production policy settings or internal Ristretto synchronization.

### Recovery and lifecycle scenarios

| Scenario | Assertion |
|----------|-----------|
| Warm restart | `Close` preserves files; `New` serves them without a loader |
| Ephemeral shutdown | `DeleteAll` then `Close` leaves no cache files |
| Close callbacks | Ristretto clear-on-close does not delete committed files |
| Reduced budget | `New` removes enough entries before returning |
| Temp recovery | Startup removes interrupted writes |
| Corrupt recovery | Startup removes malformed and checksum-invalid files |
| Oversized recovered file | Startup removes a file larger than the new budget |
| Rejected recovered file | Every on-disk file is admitted or removed |
| Frequency reset | Restart does not claim to restore TinyLFU history |
| Cleanup failure | `New` fails instead of exposing an over-budget cache |
| Idempotent close | Repeated `Close` is safe |
| Closed operations | `Get`/`Put`/`Delete`/`DeleteAll` return the closed-cache error |

### Concurrency scenarios

| Scenario | Assertion |
|----------|-----------|
| Concurrent reads | Readers obtain independent complete payloads |
| Get versus eviction | Read completes or returns a clean miss; no partial bytes |
| Get versus delete | Delete waits for an acquired reader |
| Get versus DeleteAll | Purge waits for readers and blocks new operations |
| Concurrent puts | Serialized commits preserve policy and byte accounting |
| Same-ID puts | Only one immutable file is committed |
| Put versus DeleteAll | Put completes before purge or starts after empty reset |
| Put versus close | Mutation completes before close or returns closed |
| Race suite | All scenarios pass `go test -race` |

No Temporal integration test belongs to this standalone phase. The later SDK
hydration plan must add integration coverage for blob-id arms, batched
`LoadBlobs`, fresh-value fallback after rejection, and Client/Worker wiring.

## Documentation

- Add [`sdk-go/dex/blobcache/README.md`](../../../sdk-go/dex/blobcache/README.md)
  with ownership, configuration, disk accounting, restart, `DeleteAll`, and
  exclusivity semantics.
- Update [`sdk-go/CONTRIBUTION.md`](../../../sdk-go/CONTRIBUTION.md) with
  `blobCacheTests`, the race requirement, and fault-injection guidance.
- Do not update the user-facing [`sdk-go/README.md`](../../../sdk-go/README.md)
  until Client/Worker options actually expose the cache.
- A later hydration document must explain `LoadBlobs` batching and cache-error
  fallback.

## UI/UX

N/A: no in-repo web UI.
