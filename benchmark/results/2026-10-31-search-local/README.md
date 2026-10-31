# Local SearchFlows and search projection benchmark — 2026-10-31

This set measures issue [#1123](https://github.com/superdurable/dex/issues/1123) on one shared local host. These are single-trial local measurements, not production capacity claims.

## Hardware and versions

- MacBook Pro, Apple M5 Pro, 18 cores (6 Super and 12 Performance), 48 GiB RAM, internal SSD; about 170 GiB free at the start.
- macOS 26.6.2; Go 1.26.5; Rust 1.97.1. The server and Go load generator ran natively on the same arm64 host.
- Docker Engine 29.4.0 through OrbStack, with 10 virtual CPUs and 15,765,602,304 bytes of VM memory. Unrelated workloads remained active on the shared machine.
- Release-mode server-zd source: `131ebf1f0e0d899f94cbf246500adf9d0a37bb97`. Its compiled version label is `0.1.0`; the source revision identifies the implementation. Server code was unchanged throughout the run.
- The Go SDK comes from the same source checkout through the benchmark module’s local replacement. Each raw JSON records the harness checkout revision. Harness changes added diagnostics and error reporting; the seed workload and successful-request timing method stayed the same.
- PostgreSQL 17.11: `postgres:17`; ParadeDB 0.26.1 on PostgreSQL 18.6: `paradedb/paradedb:0.26.1`, the CI service image. Exact engine versions, image IDs, and architectures are saved beside each point.
- Both SQL containers used `shared_buffers=256MB` and `max_wal_size=4GB`, with their remaining PostgreSQL settings at image defaults. The server used its default 1,024 partitions. Builtin used a 100,000-document scan budget at 100k and an explicit 1,000,000-document budget at 1M; the larger override permits the requested full scans.

## Method

Every point starts with a fresh server data directory and, for SQL search, a fresh dedicated database container. The server uses its embedded s3mock with process-crash-safe durability. The object-store emulator runs inside the server process.

The seed creates 100,000 or 1,000,000 runs in one tenant, with 128 concurrent clients. Initial indexed Attribute writes are followed by one completing Step; 5% proceed to a Channel wait and remain Running. Flow types are 80% common, 19.95% secondary, and 0.05% rare. Keyword values are 80% common, 19.9% secondary, and 0.1% rare. Int values cycle through 0–99,999. KeywordArray contains a shared element plus one of 100 tags; Text contains shared words plus one of 100 topic terms. Ten Attributes means seven Keywords, one Int, one KeywordArray, and one Text. Three Attributes means one Keyword, one Int, and one KeywordArray.

Seed throughput is unique runs divided by elapsed seed-plus-projection-catchup time. It includes Flow execution and achieved projection work, rather than isolating maximum projection capacity. Each closing Flow is awaited. SQL convergence requires the exact total, Completed, and Running counts; builtin reads committed local state. SQL status counts are polled during catchup, so their read cost is included. WAL is the difference between pg_current_wal_lsn probes before seed and after convergence, divided by unique runs. It includes all updates, rebuilds, full-page images, and database maintenance in that interval.

Query latency is end-to-end Go Client time, including gRPC and indexed-value hydration, at page size 20 with ten warmups and 100 sequential measured requests per query. p50 and p99 cover successes; errors and individual samples remain in raw JSON. Page 500 is reached by following 499 opaque next-page tokens, then only its final request is timed. At 100k, page 500 is among closed runs; at 1M, it is still among the 50k Running runs.

All requested 1M datasets were practical; the 300k fallback was not used. Optional partition-count comparisons were not run. Two three-Attribute projection observations that overlapped earlier filesystem cleanup were retained under `attempts/` and replaced by runs that started after cleanup fully exited. The startup-readiness and macOS Bash argument-expansion failures under `attempts/` happened before seeding and are excluded.

## SearchFlows latency

Each cell is **p50 / p99 in milliseconds**. The main query tables use the ten-Attribute dataset.

### Builtin

| Query | 100k | 1M |
| --- | ---: | ---: |
| Default order, page 1 | 8.412 / 10.429 | 13.031 / 16.519 |
| Common Flow type | 7.044 / 8.111 | 11.568 / 14.394 |
| Rare Flow type | 4.261 / 4.987 | 7.993 / 9.328 |
| Keyword equality, rare value | 188.670 / 201.366 | 743.839 / 1084.217 |
| Int range, 50000–50099 | 444.990 / 485.155 | 209.190 / 281.932 |
| KeywordArray contains tag42 | 74.865 / 77.964 | 641.125 / 835.190 |
| Text contains topic42 | 76.590 / 86.468 | 633.879 / 815.617 |
| Default order, page 500 | 4.502 / 5.304 | 2.407 / 2.996 |
| ORDER BY StartTime DESC | 688.126 / 703.456 | 7066.161 / 7804.011 |

### ParadeDB

| Query | 100k | 1M |
| --- | ---: | ---: |
| Default order, page 1 | 0.590 / 0.692 | 0.715 / 1.084 |
| Common Flow type | 0.574 / 1.304 | 0.620 / 1.086 |
| Rare Flow type | 10.388 / 15.484 | 205.622 / 347.556 |
| Keyword equality, rare value | 27.096 / 32.968 | 231.693 / 262.974 |
| Int range, 50000–50099 | 28.833 / 34.035 | 230.993 / 256.688 |
| KeywordArray contains tag42 | 54.015 / 68.331 | 663.304 / 778.021 |
| Text contains topic42 | 52.744 / 57.690 | 444.802 / 576.401 |
| Default order, page 500 | 2.635 / 3.705 | 5.779 / 10.415 |
| ORDER BY StartTime DESC | 41.240 / 45.795 | 347.630 / 387.582 |

### PostgreSQL

| Query | 100k | 1M |
| --- | ---: | ---: |
| Default order, page 1 | 0.530 / 1.131 | 1.112 / 2.858 |
| Common Flow type | 0.527 / 0.745 | 0.984 / 2.185 |
| Rare Flow type | 0.508 / 0.677 | 0.973 / 3.056 |
| Keyword equality, rare value | 0.714 / 2.124 | 0.710 / 1.384 |
| Int range, 50000–50099 | 0.957 / 2.273 | 3.227 / 5.013 |
| KeywordArray contains tag42 | 6.089 / 8.527 | 122.693 / 155.287 |
| Text contains topic42 | 5.527 / 6.248 | 37.900 / 44.146 |
| Default order, page 500 | 2.894 / 4.902 | 5.353 / 7.981 |
| ORDER BY StartTime DESC | 69.114 / 76.802 | 693.548 / 885.327 |

## Projection throughput and WAL

The backfill experiment is excluded from this table because its Keyword is not registered during seed. Queue overflow counts come from the preserved server logs.

| Backend | Runs | Attributes | Seed s | Catchup s | Runs/s | WAL bytes/run | Projection queue overflows |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| builtin | 100,000 | 0 | 23.73 | 0.01 | 4,211.0 | — | 0 |
| builtin | 100,000 | 3 | 24.48 | 0.01 | 4,082.3 | — | 0 |
| builtin | 100,000 | 10 | 26.40 | 0.01 | 3,785.4 | — | 0 |
| builtin | 1,000,000 | 0 | 253.28 | 0.14 | 3,946.0 | — | 0 |
| builtin | 1,000,000 | 3 | 261.99 | 0.02 | 3,816.7 | — | 0 |
| builtin | 1,000,000 | 10 | 310.46 | 0.15 | 3,219.5 | — | 0 |
| paradedb | 100,000 | 0 | 25.01 | 12.85 | 2,640.7 | 2,471.07 | 2,125 |
| paradedb | 100,000 | 3 | 25.91 | 10.05 | 2,781.0 | 3,312.53 | 2,280 |
| paradedb | 100,000 | 10 | 27.61 | 13.47 | 2,434.5 | 4,078.74 | 1,966 |
| paradedb | 1,000,000 | 0 | 293.03 | 87.69 | 2,626.6 | 3,660.34 | 5,032 |
| paradedb | 1,000,000 | 3 | 267.39 | 105.92 | 2,678.7 | 5,900.89 | 5,338 |
| paradedb | 1,000,000 | 10 | 302.21 | 167.50 | 2,129.0 | 7,934.30 | 4,421 |
| postgres | 100,000 | 0 | 26.73 | 13.85 | 2,464.2 | 1,945.33 | 1,975 |
| postgres | 100,000 | 3 | 26.42 | 13.85 | 2,483.2 | 3,414.68 | 2,311 |
| postgres | 100,000 | 10 | 28.33 | 21.86 | 1,992.3 | 6,620.59 | 2,002 |
| postgres | 1,000,000 | 0 | 257.36 | 130.69 | 2,576.9 | 2,887.21 | 4,543 |
| postgres | 1,000,000 | 3 | 310.12 | 162.32 | 2,116.7 | 5,418.98 | 4,153 |
| postgres | 1,000,000 | 10 | 353.73 | 338.36 | 1,444.9 | 10,670.61 | 3,027 |

## PostgreSQL Attribute backfill

Registration start to observed ready: **433.62 seconds** on 1M existing runs. The readiness monitor polls independently at 100 ms, with Docker/psql probe overhead. This time includes the concurrent fallback-query workload.

The Keyword value is written before registration. After projection catchup, the benchmark registers it, observes the backfilling state, and alternates rare/common equality queries. A fallback sample is retained only when state probes before and after it both say backfilling. The same predicates are measured again after ready.

| Predicate | State | Samples | p50 / p99 ms |
| --- | --- | ---: | ---: |
| Rare Keyword equality | backfilling | 100 | 292.900 / 695.676 |
| Rare Keyword equality | ready | 100 | 0.696 / 2.055 |
| Common Keyword equality | backfilling | 100 | 1.684 / 4.835 |
| Common Keyword equality | ready | 100 | 0.548 / 0.692 |

## Comparison with issue #1123 expectations

API timings include transport and result hydration. The saved `explain.txt` files provide SQL-only execution plans with the same selected columns, ordering, and 21-row server fetch limit. Their timings are individual observations, not percentile distributions.

- postgres 1M SQL EXPLAIN execution time: rare-flow-type 0.201 ms, keyword-equality 0.135 ms, int-range 6.818 ms, start-time-desc 778.548 ms.
- paradedb 1M SQL EXPLAIN execution time: rare-flow-type 177.666 ms, start-time-desc 447.128 ms, rare-flow-type-default-collation-control 0.214 ms.
- postgres, 100,000 runs: ten Attributes produced 3.40× zero-Attribute WAL per unique run (6,621 versus 1,945 bytes).
- paradedb, 100,000 runs: ten Attributes produced 1.65× zero-Attribute WAL per unique run (4,079 versus 2,471 bytes).
- postgres, 1,000,000 runs: ten Attributes produced 3.70× zero-Attribute WAL per unique run (10,671 versus 2,887 bytes).
- paradedb, 1,000,000 runs: ten Attributes produced 2.17× zero-Attribute WAL per unique run (7,934 versus 3,660 bytes).

The WAL comparison describes real completing Flows, including their projection updates and any partition rebuilds. It should not be equated with a single SQL INSERT measurement.

The PostgreSQL flow-type query used its composite index, but the one-shot SQL execution time was 0.201 ms, with 19 buffer reads; this run did not reproduce the study’s under-0.1 ms figure. Keyword equality used its partial index at 0.135 ms. The Int range used its partial index but fetched 1,000 rows and sorted them, taking 6.818 ms SQL execution time with 840 buffer reads; API p50 was 3.227 ms. The well-under-1 ms range figure was not reproduced for this range and row payload.

PostgreSQL explicit StartTime ordering was 693.548 ms API p50 and 778.548 ms in the saved SQL plan, above the study’s approximate 190 ms. API p50 grew from 69.114 ms at 100k to 693.548 ms at 1M, almost exactly 10×. Ten Attributes produced 3.40× and 3.70× the zero-Attribute WAL at 100k and 1M, respectively, rather than approximately 2×. These measurements retain the full search-row payload, Flow completion updates, and projection rebuild work.

The deterministic Int range has 100 matches at 100k runs, including five Running runs, and 1,000 matches at 1M, including fifty Running runs. The default order places Running first. This lets builtin fill its twenty-row page earlier at 1M and explains its lower Int-range latency at that size.

## Operations documentation check

See the [PostgreSQL search guidance](../../../docs/content/production/server-zd-operations.mdx#postgresql-search). The results test the cost of explicit timestamp ordering and JSONB fallback during backfill. They exercise only seven Keyword slots and one slot for each other tested type; they do not test maximum registration capacity or Keyword size limits. No direct contradiction of this guidance was established by the tested workload. Explicit timestamp ordering grew with matching rows. The backfill table above quantifies the documented fallback window. Capacity limits, oversized Keyword rejection, and multi-term Text semantics were not exercised.

## Follow-up finding: ParadeDB Flow type index collation

At 1M, the actual SearchFlows FlowType predicate uses explicit C collation and its SQL plan took 177.666 ms. It used the default-order index, retained only the tenant as an index condition, and removed 88,039 rows by filtering to return 21. The default-collation control took 0.214 ms and used the composite Flow type index with both tenant and Flow type in its index condition. API rare-type p50/p99 were 205.622/347.556 ms.

The controlled statements differ only in the explicit collation on the ASCII Flow type predicate. The query renderer applies C collation to Keyword comparisons. The shared Flow type index uses its column collation; the PostgreSQL dialect changes that column to C, while ParadeDB retains its original column collation. The saved plans demonstrate the resulting missed index key on this ParadeDB schema.

A follow-up fix should preserve exact string comparison semantics and existing schema compatibility, make the server-generated rare FlowType predicate use the matching composite index, and add integration coverage for fresh and existing ParadeDB schemas with a non-C default collation. Raw evidence is in `paradedb-1000000-a10/explain.sql` and `explain.txt`. This is an additional performance finding; the operations page does not promise a fixed latency for this predicate.

## Reproduce and inspect

Build the release server and harness from the repository checkout, then run:

```bash
for backend in builtin paradedb postgres; do
  for runs in 100000 1000000; do
    for attributes in 0 3 10; do
      scan_budget=100000
      if [[ "$backend" == builtin ]]; then scan_budget="$runs"; fi
      DEXBENCH_SEARCH_SCAN_BUDGET="$scan_budget" benchmark/search-local.sh "$backend" "$runs" "$attributes" "/tmp/search-$backend-$runs-a$attributes"
    done
  done
done
benchmark/search-local.sh postgres 1000000 10 /tmp/search-postgres-backfill --backfill
```

Each point directory holds `search.json` (all successful latencies and errors), `client.txt`, the exact server configuration, server version, and compressed server logs. SQL points also hold engine/image metadata and removal confirmation; available counts, table sizes, and container logs are retained beside them. Early points predate some optional diagnostics. The 1M ten-Attribute SQL points retain `explain.sql` and `explain.txt`. Full host details are in `host.txt`.

Run `python3 benchmark/results/2026-10-31-search-local/validate_results.py` from the repository root to check all nineteen primary points, successful sample counts, twenty-row responses, percentile ordering, and WAL arithmetic. Validation and cleanup records are included in this directory. Temporary benchmark containers and their owned database volumes were removed; the final filtered `docker ps -a` check is saved in `containers-after.txt`.

## Validation

- Release server build: `cd server-zd && cargo build --release -p dexzd-server`.
- Benchmark module: `GOWORK=off go test -count=1 ./...` and `GOWORK=off go vet ./...` from `benchmark/`.
- Go formatting, runner Bash syntax, `make copyright`, and `make copyright-check` passed.
- The data validator passed all 19 primary points, 66 query distributions, and 6,600 measured successful requests.

Raw validation output is saved beside this README. The optional partition-count sweep was not run.
