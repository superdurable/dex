# Local search outbox benchmark — 2026-11-01

This set measures the SearchOutbox projection of issue
[#1128](https://github.com/superdurable/dex/issues/1128) at the 1M ten-Attribute points. It
repeats the PostgreSQL and ParadeDB points of the
[2026-10-31 baseline](../2026-10-31-search-local/README.md) on the same host. These are
single-trial local measurements, not production capacity claims.

## Hardware and versions

- The baseline host: MacBook Pro, Apple M5 Pro, 18 cores, 48 GiB RAM, internal SSD, macOS 26.6.2.
- Rust 1.97.1 and Go 1.26.5. Docker Engine 29.4.0 through OrbStack, with 10 virtual CPUs and
  15,765,602,304 bytes of VM memory.
- Unrelated workloads shared the host. Its load average was between 11 and 31 during these runs.
- Release-mode server-zd source: `736e97b05539dd5c0610de9da63c4f16642039f2`, recorded in
  [measured-commit.txt](measured-commit.txt). Later commits on the branch change only
  documentation and this result set.
- `postgres:17` (PostgreSQL 17.11) and `paradedb/paradedb:0.26.1` (ParadeDB on PostgreSQL 18.6),
  the baseline images. Image IDs are saved beside each point.

## Method

Each point ran `benchmark/local/search-local.sh` unchanged except for two things. The database
container name starts with `dex-outbox-bench-`. A sampler scraped the outbox metrics every five
seconds into `outbox-metrics.csv`. Every point used a fresh server data directory and a fresh
database container, 1,024 partitions, 128 seed clients, and the default search settings:
`outboxBatchSize: 1000`, `outboxPollInterval: 100ms`, and `outboxAckInterval: 1s`. The seed,
convergence check, WAL probe, and query measurement are the baseline's. Server logs are not
committed. `server-log-summary.json` records each log's SHA-256, its line counts, and the
sampled metric totals.

## Projection throughput and WAL

| Backend | Seed s | Catchup s | Runs/s | WAL bytes/run | Table | Baseline runs/s | Baseline catchup s | Baseline WAL bytes/run | Baseline table |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| postgres | 347.71 | 17.94 | 2,734.9 | 4,469.90 | 2,804 MB | 1,444.9 | 338.36 | 10,670.61 | 3,354 MB |
| paradedb | 370.07 | 3.65 | 2,675.8 | 2,975.30 | 1,757 MB | 2,129.0 | 167.50 | 7,934.30 | 2,634 MB |

Runs/s is unique runs divided by seed plus catchup time, as in the baseline.

| Backend | Committed records | Ack commands | Outbox entries | Rows written | Peak lag s | Peak unprojected entries | Initial-load lines | ERROR lines |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| postgres | 3,200,575 | 99,551 (3.1%) | 1,950,000 | 1,013,394 | 63.2 | 12,396 | 1,024 | 0 |
| paradedb | 3,255,978 | 154,954 (4.8%) | 1,950,000 | 1,066,353 | 70.2 | 17,617 | 1,024 | 0 |

- **Catchup.** The projection kept up during the seed, so the outbox drained 18 s and 4 s after
  the last write. The baseline needed 338 s and 168 s, because its queue overflowed 3,027 and
  4,421 times and rebuilt partitions.
- **WAL.** WAL per run fell by 58% on PostgreSQL and 62% on ParadeDB. There are no partition
  rebuilds, and a run's start and close changes often coalesce into one row write: 1.95 million
  entries became about 1.03 million row writes.
- **Acks.** Ack commands were 3.1% and 4.8% of committed records. With the one-second interval, a
  node acks each busy partition at most once per second.
- **Lag.** `dexzd_search_outbox_oldest_entry_age_seconds` peaked at 63 s and 70 s during the
  seed, then returned to 0. No write was lost: every point converged to the exact total,
  Completed, and Running counts.
- **Initial load.** Each partition logged one load of the empty schema at startup, and no batch
  was retried.

## SearchFlows latency

Each cell is **p50 / p99 in milliseconds** at 1M runs with ten Attributes.

| Query | PostgreSQL | PostgreSQL baseline | ParadeDB | ParadeDB baseline |
| --- | ---: | ---: | ---: | ---: |
| Default order, page 1 | 0.623 / 1.034 | 1.112 / 2.858 | 0.945 / 2.176 | 0.715 / 1.084 |
| Common Flow type | 0.661 / 1.276 | 0.984 / 2.185 | 1.187 / 7.994 | 0.620 / 1.086 |
| Rare Flow type | 0.629 / 4.223 | 0.973 / 3.056 | 1.034 / 5.770 | 205.622 / 347.556 |
| Keyword equality, rare value | 0.768 / 1.135 | 0.710 / 1.384 | 444.229 / 542.396 | 231.693 / 262.974 |
| Int range, 50000–50099 | 2.887 / 3.416 | 3.227 / 5.013 | 450.428 / 748.226 | 230.993 / 256.688 |
| KeywordArray contains tag42 | 82.283 / 92.050 | 122.693 / 155.287 | 1034.257 / 1603.629 | 663.304 / 778.021 |
| Text contains topic42 | 36.430 / 41.985 | 37.900 / 44.146 | 566.652 / 737.508 | 444.802 / 576.401 |
| Default order, page 500 | 6.228 / 9.463 | 5.353 / 7.981 | 7.269 / 9.922 | 5.779 / 10.415 |
| ORDER BY StartTime DESC | 687.955 / 992.912 | 693.548 / 885.327 | 334.079 / 374.895 | 347.630 / 387.582 |

PostgreSQL latencies match the baseline or improve. ParadeDB's Rare Flow type query no longer
shows the baseline's Flow type collation finding. Its BM25 predicates are about
1.3 to 1.9 times slower. The baseline's rebuilds reinserted whole partitions, while this run
upserted rows in many small transactions. The BM25 index layout that each write pattern leaves
is a likely cause. This set does not isolate it.

## Ack interval

Before the 1M points, a 100,000-run ten-Attribute ParadeDB point measured the ack cost. Each
row is one trial on the shared host. The commit timeline comes from the embedded object store's
request log.

| Ack setting | Runs/s | Ack commands | Committed records | Note |
| --- | ---: | ---: | ---: | --- |
| 100 ms, one range delete per ack | 1,494.1 | 50,576 | 361,600 | Commits stalled for about 36 s |
| 1 h, so one ack per partition | 4,100.4 | 1,024 | 312,048 | No stall |
| 100 ms, point deletes scanned from the queue start | 2,259.1 | 42,810 | 353,834 | Commits stalled for about 17 s |
| 100 ms, point deletes after the acked seq | 1,905.5 | 48,569 | 359,593 | No stall, but 13% of records were acks |
| 1 s, point deletes after the acked seq | 4,635.4 | 21,646 | 332,670 | No stall |
| 5 s, point deletes after the acked seq | 2,336.2 | 10,085 | 321,109 | Object store writes slowed to 40–80 ms for about 15 s |
| 1 s, repeated | 2,222.7 | 32,655 | 343,679 | Object store writes slowed to 40–80 ms for about 20 s |

- A range delete per ack leaves a range tombstone in the memtable, and every read of the index
  must skip it. Acks now point-delete the entries after the previously acked seq.
- An ack that scanned from the start of the queue revisited every earlier tombstone. The scan
  now starts after the acked seq.
- The last two rows stalled while the embedded object store's PUT latency rose from under 1 ms
  to 40–80 ms, with the database container writing to the same disk. Ack volume does not explain
  them, so they are host contention.
- The default ack interval is one second.
