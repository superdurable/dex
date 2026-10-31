# Variant: protect closes and starts — 2026-10-31

This archived first-round variant protects starts and closes; [the primary results](../README.md) use the final owner shedding policy.

This set measures [#1128](https://github.com/superdurable/dex/issues/1128) against the matching ten-Attribute points in [the earlier S7 search set](../../2026-10-31-search-local/README.md#projection-throughput-and-wal). These are single-trial measurements on a shared local host. Differences include host load and other changes on main; they do not isolate a causal effect or establish production capacity.

All four datasets converged with zero skipped documents and zero ERROR-level server lines. WAL per run fell about 3–21%, while observed runs/s fell about 9–19%. No intermediate updates were dropped in this seed, and hard-limit rebuilding remained substantial. These results support further investigation of sustained projection capacity before claiming a throughput improvement.

## Source, host, and method

The release server and Go harness use implementation commit **adf4ad317** on **codex/projection-shedding**, based on **ea1ad39089acda7c55fe85b7a01992b199a6c0b1**. The earlier server used **131ebf1f0e0d899f94cbf246500adf9d0a37bb97**. The compiled version label remains **0.1.0**; each point records its source commit, and [the binary SHA-256](server-binary-sha256.txt) identifies this release build. The measured release binary stayed unchanged throughout these measurements. The final follow-up only moves a private helper, removes an unnecessary owned-position clone, explains start classification, and records these artifacts; it preserves the measured queue policy.

The native server and harness ran on the same Apple M5 Pro host as before: 18 cores, 48 GiB RAM, macOS 26.6.2. [Host metadata](host.txt) records the current toolchain and Docker VM allocation. Unrelated workloads remained active. Each SQL point used a fresh temporary container and server directory. PostgreSQL used **postgres:17**, and ParadeDB used **paradedb/paradedb:0.26.1**, with **shared_buffers=256MB** and **max_wal_size=4GB**. Exact engine versions and image IDs are saved per point. Points ran serially after validation and release compilation ended; each point's server, container, and data-directory cleanup finished before the next started.

The unchanged S7 seed uses 128 clients, 1,024 partitions, and ten indexed Attributes: seven Keywords, one Int, one KeywordArray, and one Text. It starts 100k or 1M runs, writes initial Attributes, and executes one Step. Exactly 95% complete and 5% remain Running on a Channel wait. Convergence requires exact total, Completed, and Running counts. Each saved **projected-counts.txt** independently records the exact total and Running count.

Runs/s is unique runs divided by seed plus catch-up seconds. WAL/run is the difference between pre-seed and post-convergence WAL positions divided by unique runs. It includes updates, rebuilds, full-page images, and maintenance during that interval. The **--projection-only** flag omits the query-latency phase after convergence; it does not change the seed, catch-up, or WAL measurement. No new query-latency comparison is made here.

Queue defaults are 4,096 frames / 64 MiB soft and 8,192 frames / 128 MiB hard. Complete queued documents coalesce per run. Starts, deletes, and terminal documents remain protected; their replacements inherit protection. Above a soft limit, incoming intermediate Running updates may be dropped without repair. Only hard admission failure invokes the existing partition rebuild. The S7 seed emphasizes protected starts and closes, rather than repeated intermediate Attribute updates of long-running runs; zero dropped updates is therefore possible even under heavy overload.

## Throughput and WAL

Each cell is **before → after**; both sets have ten Attributes.

| Backend | Runs | Seed s | Catch-up s | Runs/s | WAL bytes/run |
| --- | ---: | ---: | ---: | ---: | ---: |
| postgres | 100,000 | 28.33 → 30.58 | 21.86 → 26.47 | 1,992.3 → 1,752.9 | 6,620.59 → 6,353.41 |
| postgres | 1,000,000 | 353.73 → 364.15 | 338.36 → 437.38 | 1,444.9 → 1,247.6 | 10,670.61 → 10,333.08 |
| paradedb | 100,000 | 27.61 → 30.65 | 13.47 → 14.45 | 2,434.5 → 2,217.3 | 4,078.74 → 3,689.21 |
| paradedb | 1,000,000 | 302.21 → 392.82 | 167.50 → 184.24 | 2,129.0 → 1,732.9 | 7,934.30 → 6,249.95 |

## Queue events

The old overflow warnings counted new pending partition rebuild requests. The new rebuild counter has that scope, including unreadable-document requests; warnings are now rate-limited to once per second per node. Compare the old warning column to the new rebuild counter, not to the new warning column. The new coalesced, dropped, and rebuild columns are exact differences between [before/after OpenMetrics scrapes](postgres-100000-a10/metrics-after.openmetrics), not counts inferred from logs.

| Backend | Runs | Old overflow warnings | New coalesced changes | New dropped updates | New rebuild requests | New warnings | New skipped documents |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| postgres | 100,000 | 2,002 | 4,145 | 0 | 2,172 | [29](postgres-100000-a10/server-log-summary.json) | 0 |
| postgres | 1,000,000 | 3,027 | 4,061 | 0 | 2,609 | [172](postgres-1000000-a10/server-log-summary.json) | 0 |
| paradedb | 100,000 | 1,966 | 4,062 | 0 | 1,602 | [20](paradedb-100000-a10/server-log-summary.json) | 0 |
| paradedb | 1,000,000 | 4,421 | 4,062 | 0 | 3,322 | [201](paradedb-1000000-a10/server-log-summary.json) | 0 |

All four points converged to the exact final status counts. Their archived server logs contained zero ERROR-level lines. Each linked **server-log-summary.json** contains the compressed log SHA-256, size, exact warning match, warning and error counts, and counter deltas. Raw before/after scrapes are retained beside the summary. Server logs remain gitignored.

These observations do not establish that coalescing and priority shedding remove the rebuild cost for this workload. Protected traffic can still fill the hard limit, and the new rebuild counter measures that fallback directly. A reduction in rate-limited warnings is not evidence of reduced rebuilding. The separate forced-overload integration tests exercise repeated intermediate updates and require all three new counters to be nonzero.

## Reproduce and validate

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

The runner captures counters before seed and after convergence. The summarizer requires all four points, checks their success, throughput and WAL arithmetic, exact projected counts, nonnegative counters, zero skipped documents, zero ERROR-level log lines, and container-removal records. [Validation output](validation.txt) records the result. Raw compressed logs must be present to recompute their summaries; they are kept locally and are not committed.

## Testing and deviations

- Queue-policy tests: all 13 passed, including newest document, delete supersession, watermark dependencies, protected replacements, soft frame/byte limits, hard frame/byte fallback, and in-flight byte accounting.
- **dexzd-server** and **dexzd-metrics**, all targets with one test thread: passed, including 39 process tests. The existing child-process scenarios and nightly soak keep their pre-existing ignore attributes. Final queue-test additions and the ownership-only cleanup were rerun separately.
- PostgreSQL: all four SQL process tests passed with a dedicated temporary database, including forced tiny-queue overload. ParadeDB: all five passed, including purging reads. Both differential evaluator tests passed with actual databases; those include the existing projector/rebuild scenarios.
- **dexzd-search**, all targets: 54 passed. Database-dependent branches in this separate general run had no database environment; the actual SQL differential and process runs above supply the relevant database coverage. Direct TLS, slot/backfill suites, and Attribute Store integration remain CI coverage.
- Rust formatting and Clippy with **--all-targets -- -D warnings** passed for the server, metrics, and search crates. **make rust-rules-check**, **make copyright-check**, and **make -C server-zd/integ compile** passed.
- **make -C server-zd/integ zdIntegTests** passed the targeted **TestSearchRunsTemporal**, **TestSetSearchAttributes**, and **TestWfStateOptionsSearchAttributesLoading** scenarios with the release server. No Docker Compose stack was started.
- **cd docs && npm run check** passed both locales and the route, redirect, canonical, robots, and sitemap audit: 164 indexable routes and 17 redirects.
- Benchmark Go tests and vet, runner Bash syntax, release build, and result validation passed.

An initial parallel server-library run hit the existing released-data-directory lock test, alongside the expected sample-configuration failure before the new keys were added. After updating the sample, the complete library and process runs passed with one test thread. No assertions were skipped or weakened for that lock test, and its production code was unchanged. The benchmark reruns only the requested seed measurements; query latency, backfill, other Attribute counts, and builtin were not rerun. Per the task, **skills/** is unchanged and awaits the published release follow-up.

All temporary verification and benchmark containers, including their owned volumes, were removed. Per-point **container-removal.txt** records the removed container; the final filtered [container check](containers-after.txt) is empty.
