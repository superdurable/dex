# SQL search shedding with persisted pending runs — 2026-10-31

The latest PostgreSQL rerun measures the reviewed source for [#1128](https://github.com/superdurable/dex/issues/1128). It reaches exact final counts and exits cleanly after the 1,024-partition shutdown. The original PostgreSQL and ParadeDB measurements and earlier variants remain below for comparison.

## Latest PostgreSQL rerun after review fixes

The measured server source is **27a0295842b2bd3f91ae054a5440846d94ed6e2e**. The [source and binary receipt](review-fixes/source-and-binaries.json) records SHA-256 hashes; [raw result](review-fixes/postgres-1000000-a10/search.json), [counter and log summary](review-fixes/postgres-1000000-a10/server-log-summary.json), and [container removal](review-fixes/postgres-1000000-a10/container-removal.txt) record the completed point. The compiled version label remains **0.1.0**. Later artifact commits change no measured server or load-generator source.

| Measurement | Baseline | Original persisted pending | Review fixes |
| --- | ---: | ---: | ---: |
| Seed seconds | 353.73 | 384.92 | 240.55 |
| Full catch-up seconds | 338.36 | 383.36 | 288.55 |
| Runs/s | 1,444.9 | 1,301.6 | 1,890.0 |
| WAL bytes/run | 10,670.61 | 5,700.23 | 5,426.77 |

The reviewed point is **+30.8%** in runs/s and **-49.1%** in WAL/run versus baseline. This is one shared-host trial on the same Apple M5 Pro, with the same PostgreSQL 17 image and 128-client, one-million-run, ten-Attribute workload. It uses **--projection-only** and requires exact convergence; no query-latency phase ran. These observations do not establish a causal or universal performance result.

Final SQL counts are **1,000,000 total**, **950,000 Completed**, and **50,000 Running**, with zero absent or stale runs. Pending repairs, sync failures, permanent projection failures, unknown pending objects, rebuild-needed partitions, automatic rebuilds, staging overflow, skipped documents, and ERROR-level log lines all finish at zero. The server exits successfully and its temporary SQL container is removed.

The reviewed defaults guarantee one repair batch after at most 32 live frames, run up to eight object operations concurrently, and batch fenced idle checkpoints in SQL. Shutdown attempts one configured object-sync batch and preserves checkpoint guards on remaining unsynced work. The persisted pending list still rewrites complete partition objects; this point records **17,782** synced/removed partition objects. Detailed per-reason drop/repair counts and object-store payload costs are in the summary and the **reviewed** entry of [comparison.json](comparison.json).

## Source and method

The original persisted-pending release server uses **d09000382d01d14b53dd89f1ab5a07908293c795**; [binary SHA-256](server-binary-sha256.txt) and [host metadata](host.txt) identify the measured build. Its compiled version label is **0.1.0**. Artifact commits change no measured server or benchmark source. The [baseline](../2026-10-31-search-local/README.md#projection-throughput-and-wal) uses **131ebf1f0e0d899f94cbf246500adf9d0a37bb97**; the in-memory phase uses **579ca2373**.

These are single trials on a shared Apple M5 Pro, 18 cores and 48 GiB RAM, with unrelated workloads active. The two points ran serially after compilation and verification, each in a fresh temporary SQL container and native server directory. PostgreSQL uses **postgres:17** and ParadeDB uses **paradedb/paradedb:0.26.1**, with **shared_buffers=256MB** and **max_wal_size=4GB**. Exact images, engine versions, configurations and removal receipts are saved per point. The containers and native temporary directories were removed after each run.

The unchanged ten-Attribute S7 workload uses 128 clients and 1,024 partitions. It starts one million runs with seven Keywords, one Int, one KeywordArray and one Text; 95% complete and 5% remain Running. Queue defaults are 4,096 frames / 64 MiB soft and 8,192 frames / 128 MiB hard, staging capacity 1,000,000, repair batches of 256, pending sync every 1s and at most 64 serialized partition objects per batch. The primary points use **--projection-only**, without **--allow-shedding**. No query latency phase ran.

**Full catch-up includes repair time and requires exact total, Completed and Running counts, with pending repair work drained.** Runs/s is unique runs divided by seed plus full catch-up seconds. WAL/run is the WAL difference from before seeding to convergence divided by unique runs, including maintenance and full-page images. It does not measure RocksDB or object-store bytes. Object-sync counts are shown separately.

## Before and after

This section retains the original measurements; the PostgreSQL review-fix rerun above is current. The ParadeDB rows retain their original source and measurements.

Every row represents one million runs with ten Attributes. Arrows mean **baseline → in-memory repair → persisted pending**.

| Backend | Seed seconds | Full catch-up seconds | Runs/s | Persisted vs baseline | Persisted vs in-memory |
| --- | ---: | ---: | ---: | ---: | ---: |
| postgres | 353.73 → 343.86 → 384.92 | 338.36 → 257.55 → 383.36 | 1,444.9 → 1,662.7 → 1,301.6 | -9.9% | -21.7% |
| paradedb | 302.21 → 361.32 → 360.50 | 167.50 → 204.95 → 249.74 | 2,129.0 → 1,765.9 → 1,638.7 | -23.0% | -7.2% |

| Backend | Baseline WAL bytes/run | In-memory WAL bytes/run | Persisted WAL bytes/run | Persisted vs baseline | Persisted vs in-memory |
| --- | ---: | ---: | ---: | ---: | ---: |
| postgres | 10,670.61 | 4,525.75 | 5,700.23 | -46.6% | +26.0% |
| paradedb | 7,934.30 | 2,343.21 | 2,510.69 | -68.4% | +7.1% |

The [machine-readable comparison](comparison.json) retains unrounded values. A shared-host single trial does not establish a causal improvement or a universal throughput result.

## Shedding and repair

| Backend | Coalesced | Dropped intermediate | Dropped close | Dropped start | Rebuild requests | Staging overflows |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| postgres | 13,329 | 0 | 927,671 | 985,972 | 0 | 0 |
| paradedb | 18,786 | 0 | 914,830 | 980,255 | 0 | 0 |

| Backend | Repaired intermediate | Repaired close | Repaired start | Synced/removed partition objects | Pending-object sync failures |
| --- | ---: | ---: | ---: | ---: | ---: |
| postgres | 0 | 4,856 | 986,246 | 26,141 | 0 |
| paradedb | 0 | 6,636 | 980,648 | 24,991 | 0 |

All values above are exact differences between before/after OpenMetrics scrapes. Drops count changes, not distinct runs. A run can have several drops; its pending record retains the highest repair priority, so a dropped start later closed is repaired under **start**. This short-Flow workload emphasizes starts and closes and can have zero intermediate drops. Repairs count successful document offers; one run can be counted again after a concurrent drop. Synced-object counts include successful empty-list removals. Rebuild-needed partitions and the persisted pending gauge end at zero.

| Backend | Projected | Completed | Running | Absent | Stale Running | Pending repairs |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| postgres | 1,000,000 | 950,000 | 50,000 | 0 | 0 | 0 |
| paradedb | 1,000,000 | 950,000 | 50,000 | 0 | 0 | 0 |

Per-point **projected-counts.txt** and **projected-state.txt** check the seed's run-number status rule, including impossible Completed states. Raw OpenMetrics scrapes, results, EXPLAIN output and server-log hashes/counts are saved. Raw compressed server logs remain local and gitignored; they are not committed.

## Pending object-store work

| Backend | Pending PUT requests | Pending DELETE requests | PUT payload MiB | Sum of server request durations, s | Maximum request duration, ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| postgres | 25,782 | 423 | 385.08 | 56.69 | 3,561 |
| paradedb | 24,868 | 187 | 395.14 | 62.02 | 3,732 |

These activity totals come from the complete local server log, including startup and shutdown, rather than the exact metrics interval. Duration sums cover server request handling; they overlap other work and are not a critical-path or CPU attribution. Payload bytes cover pending-list PUTs, separate from SQL WAL. These numbers quantify the additional persistent-sync work without assigning the whole throughput difference to it. Catch-up ends at exact SQL visibility and local pending drain; it does not wait for every empty-list object cleanup.

## Policy and durability changes

- Coalesce complete documents; shed intermediate updates above soft limits, then closes and starts above hard limits. Inherited start priority does not prevent hard-limit shedding. Delete is never shed and can exceed hard limits. No automatic partition rebuild path remains.
- Stage identities without object or SQL I/O on the commit callback. Persist pending identities in a separate local RocksDB sidecar and sync versioned per-partition objects. CAS writes, conditional deletes and owner epochs fence stale object writers. SQL checkpoints never pass unsynced shed work.
- On takeover, load pending objects before projecting. For behind progress and new schemas, compare committed local identities/versions with bounded SQL pages, queue missing/newer rows and SQL-only purged Deletes, sync, then advance progress. No engine, replicated state or apply-format marker was added.
- Idle repair reads the current committed document, uses versioned SQL writes, and removes local pending entries only after SQL commit. Revisions preserve new drops during a repair. Live frames have priority between batches, without a fixed pause between successful idle batches. Quarantined partitions cannot starve valid repairs.
- Corruption and unsafe divergence mark/quarantine only affected partitions. Leader-only **AdminService** and **dexcli search-projection status/rebuild** schedule explicit partition/list, marked or all-partition rebuilding, one partition at a time. Configured authentication requires an existing JWT issuer and a dedicated **auth.adminAudiences** value; static tokens cannot administer, and the empty default denies admin calls. Unauthenticated dev servers allow admin calls.
- Drop and repair counters retain reason labels. Added persisted pending, object sync/failure and rebuild-needed metrics. The pending gauge is published from persistent-store state, so staging counts cannot overwrite it. English and Simplified Chinese operations, CLI and module/layout docs describe the semantics and read-your-writes relaxation.

## Variant: protect closes and starts

The [first-round report](protect-closes-and-starts/README.md) and all four original raw points are preserved. This policy protected starts/closes, dropped no intermediate updates in this seed and triggered partition rebuilding.

| Backend | Runs | Catch-up seconds | Runs/s | WAL bytes/run | Rebuild requests |
| --- | ---: | ---: | ---: | ---: | ---: |
| postgres | 100,000 | 26.47 | 1,752.9 | 6,353.41 | 2,172 |
| postgres | 1,000,000 | 437.38 | 1,247.6 | 10,333.08 | 2,609 |
| paradedb | 100,000 | 14.45 | 2,217.3 | 3,689.21 | 1,602 |
| paradedb | 1,000,000 | 184.24 | 1,732.9 | 6,249.95 | 3,322 |

The [four-point in-memory repair phase](in-memory-repair/README.md) keeps all of its 100k and 1M results, original counters and binary hash. The [no-repair variant](shed-without-repair/README.md) retains its completed PostgreSQL 100k point and explicit incompleteness; it is not compared at equal visibility.

## Regression findings

The bounded investigation found a redundant full SearchDocumentRecord decode under the queue mutex. Classification now reads only version/status before taking that mutex, avoiding a second set of Attribute allocations. Coalescing removes empty queued frames while transferring visibility dependencies, avoiding unnecessary empty SQL batches. The [paired timing probe](in-memory-repair/queue-timing.txt) used five optimized trials of 200,000 iterations on a 909-byte synthetic ten-Keyword document: median full decode **0.181889s / 0.909µs per change**, partial classification **0.010487s / 0.052µs**, a **17.3×** reduction in that classifier work. The in-memory phase's no-overload queue pipeline was **1.365µs/frame**; this is not a measurement of the new durable worker.

The [slot-lock/version-read experiment](no-slot-lock/README.md) measured **1,628.4 runs/s** against the safe in-memory **1,662.7**, or **−2.1%**, with exact convergence. It demonstrated no throughput benefit. The unsafe patch was reverted, source restoration verified, and the safe binary restored before further implementation. The final source retains slot coordination and version checks.

These probes do not causally decompose the historical 9–19% regression. SQL round trips, batching, full repair work, object synchronization and shared-host variation remain separate effects. The protected policy kept empty coalesced frames and allowed twice the baseline hard-frame count; these indicate possible extra per-frame transaction work, rather than measured transaction counts. The primary tables report the observed end-to-end result and include the work needed for equal final visibility.

## Verification and release follow-up

- **30 policy/persistence tests** pass: ordered drops, protected Deletes, staging overflow without rebuilding, priority/deduplication, revision races, real RocksDB reopen, two owners sharing object storage, stale-owner fencing, outage guard, unreadable-object isolation, quarantine non-starvation, fair dirty-partition sync and persisted-gauge ownership.
- Both actual SQL dialect process suites pass, including tiny limits, overflow-triggered version comparison, crashes before pending sync, purges while down, fresh-schema startup, injected SQL progress divergence, zero automatic rebuilds and successful explicit marked-partition recovery. Every projected test row must equal a committed version; final repairs require the latest version and every purged run absent. Ordinary outage/coalescing and warm restart recovery also pass.
- Both actual SQL differential suites pass, including the projector/repair scenarios they invoke. Current-server **dexcli dev** E2E, admin selector/RPC tests and real JWT allow/deny/default-deny tests pass. The CLI E2E has its own temporary PostgreSQL container and a dedicated CI target.
- Touched server/API/search/metrics all-target suites, object-store conformance, full s3mock tests, formatting and all-targets Clippy pass. Existing child/soak/audit ignores remain unchanged. **make rust-rules-check**, **make copyright-check**, **make -C server-zd/integ compile**, CLI tests and **cd docs && npm run check** pass; the docs audit covers **164 routes and 17 redirects**. Dedicated TLS and SQL Attribute Store scenarios remain CI coverage; no dependency Docker Compose stack ran locally.
- Root **make generated-code** regenerated all checked-in SDK outputs after the additive AdminService proto change, using pinned protoc 35.1. Existing wire fields and persisted engine formats are unchanged.

**Release-level completion remains blocked on a published Dex tag containing this work.** After that release, advance **skills/DEX_BASELINE** to the exact tag, refresh the five language references/source links and release metadata, and run skill validators. No skill baseline or pinned link was advanced to an unreleased commit.

## Reproduce

Run from the recorded source above with the release binary built, using temporary containers:

```bash
make -C server-zd build-server
for backend in postgres paradedb; do
  benchmark/search-local.sh "$backend" 1000000 10 \
    "benchmark/results/2026-10-31-search-shedding-local/$backend-1000000-a10" --projection-only
done
python3 benchmark/results/2026-10-31-search-shedding-local/summarize.py
```

The summarizer checks exact status counts, persisted repair drain, zero rebuilds/overflow/sync failures/skips/errors, reason counters, WAL and throughput arithmetic, and container removal. Regenerating log summaries requires the local ignored compressed logs. The [container audit](containers-after.txt) is empty after both final runs. EXPLAIN whitespace was trimmed without changing plan text or timings.
