# SQL search projection with in-memory idle repair — 2026-10-31

All four primary points converged to exact final status counts with zero pending repairs, repair overflows, rebuild requests, skipped documents, and ERROR-level server lines. These results measure [#1128](https://github.com/superdurable/dex/issues/1128) against [the earlier ten-Attribute S7 set](../2026-10-31-search-local/README.md#projection-throughput-and-wal). They record the in-memory idle-repair policy, before the subsequent request for SQL-persisted repair and manual-only partition rebuilds: coalesce, shed intermediates above soft limits, shed closes and starts above hard limits, retain Deletes, and repair shed runs when live projection drains.

## Source, host, and method

The release server and Go harness use **579ca2373**. All four primary points use the same [release binary SHA-256](server-binary-sha256.txt), with compiled version label **0.1.0**. The final artifact commit changes no server or harness source. The baseline used **131ebf1f0e0d899f94cbf246500adf9d0a37bb97**; the protected variant used **adf4ad317**; the no-repair variant used **dcf3492c09fe583df0dc8988ea3ccbf139a37458**. The intervening baseline-to-main server changes affect ParadeDB query translation and tests, rather than the seed write path.

These are single trials on a shared Apple M5 Pro: 18 cores, 48 GiB RAM, macOS 26.6.2. [Host metadata](host.txt) records toolchains and the Docker VM allocation. Unrelated workloads remained active. Points ran serially after compilation and validation ended, each with a fresh temporary SQL container and native server directory. PostgreSQL used **postgres:17**, ParadeDB **paradedb/paradedb:0.26.1**, both with **shared_buffers=256MB** and **max_wal_size=4GB**. Exact engine versions, image IDs, configurations, and removal receipts are retained per point.

The unchanged S7 seed uses 128 clients, 1,024 partitions, and ten indexed Attributes: seven Keywords, one Int, one KeywordArray, and one Text. It starts 100k or 1M runs and executes one Step; 95% complete and 5% remain Running. Defaults are 4,096 frames / 64 MiB soft, 8,192 frames / 128 MiB hard, **maxRepairEntries=1000000**, and **repairBatchRuns=256**. Repair entries deduplicate by run and retain priority start, close, intermediate. The worker prefers live frames between repair batches and reads current committed documents for versioned multi-row upserts across partitions. Delete-only overflow and repair-set overflow retain partition rebuild fallback.

**Full catch-up includes all repair time after seeding and requires exact total, Completed, and Running counts again.** Primary runs use **--projection-only**, without **--allow-shedding**. The diagnostic shedding option ends at the kept-change visibility barrier and is used only by the archived no-repair variant. No query-latency phase ran. EXPLAIN output has only trailing padding and terminal blank lines removed; plan text and timings are unchanged. Runs/s is unique runs divided by seed plus full catch-up seconds, not projected rows/s. WAL/run is the pre-seed to post-convergence WAL difference divided by unique runs, including maintenance and full-page images during that interval.

## Throughput and WAL

| Backend | Runs | Seed s, baseline → repair | Full catch-up s, baseline → repair | Runs/s, baseline → repair | Change | WAL bytes/run, baseline → repair | Change |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| postgres | 100,000 | 28.33 → 30.74 | 21.86 → 31.06 | 1,992.3 → 1,618.0 | -18.8% | 6,620.59 → 4,368.77 | -34.0% |
| postgres | 1,000,000 | 353.73 → 343.86 | 338.36 → 257.55 | 1,444.9 → 1,662.7 | +15.1% | 10,670.61 → 4,525.75 | -57.6% |
| paradedb | 100,000 | 27.61 → 28.12 | 13.47 → 25.06 | 2,434.5 → 1,880.4 | -22.8% | 4,078.74 → 2,444.19 | -40.1% |
| paradedb | 1,000,000 | 302.21 → 361.32 | 167.50 → 204.95 | 2,129.0 → 1,765.9 | -17.1% | 7,934.30 → 2,343.21 | -70.5% |

## Queue and repair events

| Backend | Runs | Coalesced | Dropped intermediate | Dropped close | Dropped start | Repaired intermediate | Repaired close | Repaired start | Rebuild requests | Repair overflows |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| postgres | 100,000 | 8,971 | 0 | 84,332 | 90,555 | 0 | 10 | 90,555 | 0 | 0 |
| postgres | 1,000,000 | 14,905 | 0 | 921,976 | 984,307 | 0 | 71 | 984,307 | 0 | 0 |
| paradedb | 100,000 | 11,878 | 0 | 78,904 | 87,497 | 0 | 19 | 87,497 | 0 | 0 |
| paradedb | 1,000,000 | 17,558 | 0 | 917,338 | 981,494 | 0 | 90 | 981,494 | 0 | 0 |

Counters are exact differences between before/after OpenMetrics scrapes. Drops count changes, not unique runs; a run can contribute several drops. Repairs count current documents successfully offered by their retained repair priority. A start drop keeps the start repair priority even if closes are later dropped for that run. This short-Flow seed emphasizes starts and closes, so intermediate drops can remain zero. Zero rebuild requests refers to overload requests; ordinary startup catch-up is separate.

## Final visibility

| Backend | Runs | Projected | Completed | Running | Absent | Stale Running | Pending repairs at end |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| postgres | 100,000 | 100,000 | 95,000 | 5,000 | 0 | 0 | 0 |
| postgres | 1,000,000 | 1,000,000 | 950,000 | 50,000 | 0 | 0 | 0 |
| paradedb | 100,000 | 100,000 | 95,000 | 5,000 | 0 | 0 | 0 |
| paradedb | 1,000,000 | 1,000,000 | 950,000 | 50,000 | 0 | 0 | 0 |

**projected-counts.txt** records total and Running counts; **projected-state.txt** also checks the seed's run-number rule for stale Running or impossible Completed states. Each retained row is a complete committed version. The process tests additionally require equality of every SearchFlows entry to the latest committed version after repair, and every purged run absent. Raw scrapes and per-point log summaries are saved; compressed server logs remain local and gitignored.

## Variant: protect closes and starts

The [archived first-round report](protect-closes-and-starts/README.md) and all four raw points retain their original source and validation. This policy protected starts and closes, shed no intermediate updates in the seed, and rebuilt partitions under protected traffic. It required exact final-state convergence.

| Backend | Runs | Catch-up s | Runs/s | WAL bytes/run | Coalesced | Dropped | Rebuild requests |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| postgres | 100,000 | 26.47 | 1,752.9 | 6,353.41 | 4,145 | 0 | 2,172 |
| postgres | 1,000,000 | 437.38 | 1,247.6 | 10,333.08 | 4,061 | 0 | 2,609 |
| paradedb | 100,000 | 14.45 | 2,217.3 | 3,689.21 | 4,062 | 0 | 1,602 |
| paradedb | 1,000,000 | 184.24 | 1,732.9 | 6,249.95 | 4,062 | 0 | 3,322 |

## Variant: shed without repair

The [no-repair PostgreSQL 100k point](shed-without-repair/README.md) measured **1,955.8 runs/s**, **22.27 s** kept-change catch-up, and **768.71 WAL bytes/run**, with no rebuild requests. It dropped **81,965 close** and **89,925 start** changes. Only **17,194** runs were projected, **82,806** were absent, and **34** closed runs still appeared Running. These change counts do not equal unique absent runs. Its low WAL is not comparable at equal completeness. The owner replaced that policy with bounded repair. The following 1M seed was canceled at about 830k runs and its container removed; no partial throughput result is published.

## Throughput regression investigation

The bounded investigation found a second full SearchDocumentRecord decode under the queue tracking mutex, after extraction had already decoded and validated it. The final code classifies only version and status using a small protobuf reader before taking the mutex, avoiding a second set of Attribute allocations. Coalescing now removes empty queued frames while transferring their visibility dependencies, avoiding unnecessary empty SQL batches. Encoded documents retain their existing single queue ownership copy from the borrowed committed outcome.

Five optimized paired trials used a 909-byte synthetic document with ten 64-byte Keyword Attributes and 200,000 iterations each. Median full second decode was **0.181889 s** (**0.909 us/change**); the two-field classifier was **0.010487 s** (**0.052 us/change**), **17.3x** faster. The final no-overload record/enqueue/dequeue/complete pipeline measured **0.272926 s / 200,000 frames**, or **1.365 us/frame**. [Timing output](queue-timing.txt) and the [temporary probe patch](queue-timing-probe.patch) make the measurement reviewable; the test patch was removed afterward.

These synthetic CPU timings do not isolate mutex contention or causally decompose the historical 9–19% regression. The protected variant kept empty coalesced frames and allowed twice the baseline's hard frame count before rebuilding, increasing possible per-frame SQL transaction work. PostgreSQL's protected points coalesced 4,145 and 4,061 changes; these are potential work indicators, not measured empty-transaction counts. Full repair adds work that the no-repair policy omitted, and its full catch-up must be included. SQL round trips, rebuild batching, and shared-host variation remain separate effects. The extra experiment below measures one current SQL write-path cost without claiming that its unsafe patch is a shipping fix.

## Experiment: no per-transaction slot lock

**Unsafe for production; measurement only.** On the final repair source, a [local patch](no-slot-lock/unsafe-local-experiment.patch) replaced **attribute_slots_for_write** with its cached snapshot, skipping both **lock_attribute_slots_shared** and the per-transaction registry-version read. This removes coordination with slot registration/backfill and registry-freshness validation. It measures the two operations together, not the advisory lock in isolation. The patch was not committed to server source and was reverted after the run; the safe release binary was restored.

| PostgreSQL, 1M runs, ten Attributes | Primary repair | No per-transaction slot lock/version read | Change |
| --- | ---: | ---: | ---: |
| Seed s | 343.86 | 358.72 | +4.3% |
| Full catch-up s | 257.55 | 255.38 | -0.8% |
| Runs/s | 1,662.7 | 1,628.4 | -2.1% |
| WAL bytes/run | 4,525.75 | 4,500.67 | -0.6% |

Experiment counters: coalesced **14,075**; dropped intermediate/close/start **0 / 924,850 / 985,167**; repaired intermediate/close/start **0 / 63 / 985,167**; rebuild requests **0**; repair overflows **0**. Exact final counts converged, pending repairs reached zero, and the container was removed. The [experiment binary hash](no-slot-lock/server-binary-sha256.txt), raw scrapes, database metadata, and validation summary identify this trial. This is one intervention on a shared host; it does not establish a production-safe replacement or isolate the historical baseline regression.

## Reproduce

```bash
make -C server-zd build-server
cd benchmark && GOWORK=off go build -o bin/dexbench ./cmd/dexbench && cd ..
for backend in postgres paradedb; do
  for runs in 100000 1000000; do
    benchmark/search-local.sh "$backend" "$runs" 10 \
      "benchmark/results/2026-10-31-search-shedding-local/$backend-$runs-a10" --projection-only
  done
done
python3 benchmark/results/2026-10-31-search-shedding-local/summarize.py
```

The timing and experiment patches use zero context; apply them only to their recorded source revision with **git apply --unidiff-zero**.

The summarizer validates all four points, success, exact final counts, full repair drain, zero overload rebuilds and repair overflows, nonnegative per-reason counters, zero skipped documents and ERROR-level lines, throughput/WAL arithmetic, stale/impossible status checks, and container removal. Raw compressed logs are required to regenerate log summaries; they are local, gitignored files.

## Testing and release follow-up

- All 20 queue-policy tests passed, including ordered shedding, inherited start shedding without resurrection, Delete reclamation by priority and age, Delete-only rebuilds, watermark transfer, in-flight byte bounds, repair priority/deduplication, live-frame preference, repair overflow, and drops during an in-flight repair.
- All four forced-overload process scenarios passed against real temporary databases: normal repair and repair-set overflow on PostgreSQL and ParadeDB. They require exact latest-state convergence, every purge absent, pending repairs zero, and reason counters. Both actual outage/coalescing scenarios passed.
- Both SQL differential evaluator/projector suites passed, including new cross-partition repair batching, version protection, unchanged live progress, and rejection of mismatched progress. Direct TLS branches without their dedicated environment remain CI coverage.
- All targets for **dexzd-server**, **dexzd-metrics**, and **dexzd-search** passed with one test thread: 73 server-library tests, 54 search tests, 2 metrics tests, and 41 process tests, plus authentication and snapshot tests. Existing child scenarios and long soak retain their prior ignores. General SQL branches without environment variables are covered by the real database runs above.
- Formatting; Clippy **--all-targets -- -D warnings** for all three crates; **make rust-rules-check**; **make copyright-check**; and **make -C server-zd/integ compile** passed.
- **make -C server-zd/integ zdIntegTests** passed **TestSearchRunsTemporal**, **TestSetSearchAttributes**, and **TestWfStateOptionsSearchAttributesLoading** with the final release server. No Docker Compose stack was started.
- **cd docs && npm run check** passed both locales and the 164-route / 17-redirect canonical, robots, and sitemap audit. Benchmark Go tests, vet, runner syntax, and release compilation passed.

Repairs are in memory. During uninterrupted ownership, complete visibility returns when repair and live projection drain. Restart or leader loss discards repair entries; startup cannot detect every lost repair after SQL progress has advanced, so stale or missing rows may remain until a later write or purge. A zero pending gauge after restart does not prove pre-restart recovery. The operations docs in both locales state this limitation.

The skill-release follow-up is blocked until a published Dex tag contains this change; **skills/DEX_BASELINE** and pinned references cannot use an unreleased commit, so **skills/** remains unchanged. All owned temporary verification and benchmark containers are removed after their runs; final removal receipts and the filtered container check record cleanup.
