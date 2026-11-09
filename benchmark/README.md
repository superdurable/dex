# Dex server benchmark

`dexbench` measures server-zd with a Go SDK Worker. It runs six execution scenarios at
fixed client concurrency and records throughput, latency percentiles, server
CPU and memory, and object storage requests for each run.

## Latest results

These results were measured on 2026-11-09 in AWS us-west-2. **They are partial:** they cover S4, S5, and a rough S1, but not S2, S3, or S6.

- **Server:** one server-zd node on an on-demand c7gd instance. RocksDB was on the local NVMe instance store, and the log went to S3 Standard in the same Region. The 2026-10-31 run used Spot servers.
- **Load host:** a separate on-demand c7g.8xlarge ran `dexbench` in the same Availability Zone and cluster placement group. Its mean CPU stayed below 35% of the host at every point.
- **Version:** server-zd `f9254ef20`, whose server-zd tree is the same as `e3457b6c6`. `dexbench` ran from `d99724b0d`.
- **Configuration:** history offload was on, with deletion of expired history objects and a 50,000-event history window. These match the upcoming server-zd defaults. Everything else was the same as on 2026-10-31; see [Settings for 2026-11-09](#settings-for-2026-11-09).
- **Measurement:** each point is a 10-second warmup and a 45-second measurement on a newly started server. Every headline point ran three times with profiling off. A point with three runs shows the median of the three.

**Why S2, S3, and S6 are missing.** `dexbench` now runs S2, S3, and S6 on long-lived Flows: each client keeps one Flow open for the whole point. On a long-lived Flow, Step throughput falls as the Flow executes more Steps. With eight clients, Steps per second fell by a factor of three to four after 90 seconds, with history offload on or off. A number from these scenarios would therefore depend on the length of the point. The evidence and the code path are in [`results/2026-11-09-long-lived-runs/`](results/2026-11-09-long-lived-runs/README.md). These scenarios will be measured again after a server-zd fix. Their numbers would not be directly comparable with 2026-10-31, which started a new ten-Step Flow for every operation.

Each table lists the peak and a "~50% of peak" row per scenario. The ~50% row shows latency at a normal, unsaturated load. The peak row has bold throughput. Rows marked "search" are single runs from the search rounds, with profiling on. The other rows show the median throughput of three runs. Their latency percentiles come from the run with that median throughput; they are not medians of each percentile.

### c7gd.large (2 vCPU)

| Scenario | C | Throughput | p50 / p90 / p99 (ms) |
| --- | ---: | ---: | --- |
| S4 plain RPC, reads one 8-byte Attribute | 4 (~50% of peak) | 7,492 RPCs/s | 0.5 / 0.6 / 0.8 |
|  | 64 (search) | 12,360 RPCs/s | 5.0 / 7.0 / 9.2 |
|  | 256 (peak) | **13,926 RPCs/s** | 18.5 / 24.7 / 29.6 |
|  | 512 (search) | 12,937 RPCs/s | 39.1 / 52.7 / 68.2 |
| S4 Attribute-locking RPC, updates one 8-byte Attribute | 64 (~50% of peak) | 950 RPCs/s | 64.7 / 88.3 / 115.2 |
|  | 512 (search) | 1,773 RPCs/s | 278 / 392 / 517 |
|  | 960 (peak) | **2,149 RPCs/s** | 435 / 608 / 749 |
| S5 direct read of ten 16 KiB Attributes (160 KiB) | 3 (~50% of peak) | 3,058 reads/s | 0.9 / 1.2 / 1.7 |
|  | 256 (peak) | **5,891 reads/s** | 43.4 / 59.9 / 74.2 |
|  | 1,024 (search) | 4,938 reads/s | 205 / 260 / 344 |

### c7gd.4xlarge (16 vCPU)

| Scenario | C | Throughput | p50 / p90 / p99 (ms) |
| --- | ---: | ---: | --- |
| S4 plain RPC, reads one 8-byte Attribute | 8 (~50% of peak) | 15.3k RPCs/s | 0.5 / 0.6 / 1.2 |
|  | 48 (search) | 29.5k RPCs/s | 1.5 / 2.4 / 3.8 |
|  | 512 (peak) | **32.1k RPCs/s** | 15.4 / 24.1 / 32.2 |
|  | 2,048 (search) | 29.8k RPCs/s | 66.7 / 102.4 / 134.3 |
| S4 Attribute-locking RPC, updates one 8-byte Attribute | 512 (~50% of peak) | 6,286 RPCs/s | 73.8 / 104.7 / 252.0 |
|  | 2,048 (search) | 10.4k RPCs/s | 201 / 244 / 271 |
|  | 4,096 (peak) | **11.7k RPCs/s** | 347 / 432 / 558 |
|  | 8,192 | 12.1k RPCs/s | 667 / 770 / 935 |
| S5 direct read of ten 16 KiB Attributes (160 KiB) | 5 (~50% of peak) | 5,536 reads/s | 0.8 / 1.2 / 1.9 |
|  | 16 (search) | 11.0k reads/s | 1.3 / 2.3 / 3.2 |
|  | 256 (peak) | **11.3k reads/s** | 16.2 / 55.2 / 80.3 |

Attribute-locking RPC at C=8,192 used 32 connections and a 16,384 in-flight RPC limit. It is within 4% of the C=4,096 point, so the scenario is flat above C=4,096.

### S1 Flows started and completed (rough)

S1 is rough: a single 15-second run per point, after a 5-second warmup, with profiling off. It uses short Flows: each operation starts a new Flow and waits for it to complete. With history offload on, each completed Flow seals one history segment for upload.

| Instance | C | Throughput | p50 / p90 / p99 (ms) | History uploads/s | S3 PUTs/s | PUTs per completed Flow |
| --- | ---: | ---: | --- | ---: | ---: | ---: |
| c7gd.large | 64 | 445 Flows/s | 139 / 176 / 205 | 445 | 573 | 1.29 |
|  | 512 | 695 Flows/s | 725 / 874 / 1,001 | 394 | 474 | 0.68 |
|  | 1,024 | 728 Flows/s | 1,372 / 1,634 / 1,871 | 385 | 443 | 0.61 |
| c7gd.4xlarge | 3,072 | 4,733 Flows/s | 618 / 822 / 1,054 | 547 | 609 | 0.13 |
|  | 6,144 | 4,925 Flows/s | 1,221 / 1,471 / 1,657 | 532 | 574 | 0.12 |
|  | 12,288 | 5,351 Flows/s | 2,250 / 2,636 / 2,931 | 503 | 533 | 0.10 |

- History uploads keep up only at low throughput. At C=64 on c7gd.large, uploads matched completed Flows one for one: 8,975 each.
- Above that, uploads leveled off at about 390 per second on c7gd.large and 500–550 per second on c7gd.4xlarge. This is close to the default `history.uploadConcurrency` of 16 times one S3 PUT round trip. Completed Flows ran ahead of the uploads: at C=12,288 on c7gd.4xlarge, 124,992 Flows completed and 11,157 segments were uploaded during the point.
- PUTs per completed Flow fall at high throughput only because the uploads trail. `dexzd_history_upload_backlog_segments` still read 0 after these points.
- A local check counted exactly one history PUT, of about 926 bytes, per closed short Flow. See [`results/2026-11-09-long-lived-runs/`](results/2026-11-09-long-lived-runs/README.md).
- There were no object storage request errors and no failed uploads.

### Notes

- **C** is the number of concurrent clients. Load is closed-loop, so latency grows with C once the server is saturated.
- **Changes from 2026-10-31 above 15%:**
  - Plain RPC on c7gd.large rose from 7,426 to 13,926 RPCs/s. Server CPU was above 92% of both cores in both runs, so each RPC now costs less CPU. The cause was not isolated.
  - Attribute-locking RPC on c7gd.large rose from 1,805 to 2,149 RPCs/s. The new peak is at C=960 rather than 768.
  - Plain RPC on c7gd.4xlarge fell from 49,327 to 32,137 RPCs/s. The server was not CPU-bound: it used 36% of the host at the peak, against 76% on 2026-10-31. In every thread snapshot, 6 to 11 of the 27 server threads waited on the `WorkerDispatch` registry mutex in `dexzd-worker-dispatch`. That lock is the likely limit. See the [c7gd.4xlarge results](results/2026-11-09-aws-s3-c7gd-4xlarge/README.md).
  - S1 on c7gd.large fell from 889 to 728 Flows/s at its peak, and on c7gd.4xlarge from 6,176 to 5,351 Flows/s at C=12,288. These are single 15-second runs with history offload on, so the comparison is indicative only.
- **S3 requests:** plain RPC and S5 write nothing, so their only PUTs are the lease, about 0.3 per second. Attribute-locking RPC made 14–120 log PUTs per second. The open S4 and S5 Flows wrote no history segments within a point, because a segment seals at 4 MiB or when its Flow closes.

### Settings for 2026-11-09

- **Both instance types:** history offload on, with deletion of expired history objects and a 50,000-event window. These match the upcoming server-zd defaults.

  ```yaml
  history:
    offloadEnabled: true
    deleteExpiredObjects: true
    windowEvents: 50000
  ```

- **c7gd.large:** otherwise the default configuration, with 4 gRPC connections.
- **c7gd.4xlarge:** the four settings of 2026-10-31, with 16 gRPC connections. Attribute-locking RPC at C=8,192 used 32 connections and `limits.maxInFlightRpcsPerNode: 16384`.
  - `matching.maxConcurrentTasksCap` (default 256) now limits the task slots of each pull Worker.
  - `dexbench` gives each connection C divided by the connection count, so a point above 256 slots per connection needs more connections. S1 used 48 connections for that reason.

The result sets are in
[`results/2026-11-09-aws-s3-c7gd-large/`](results/2026-11-09-aws-s3-c7gd-large/)
and
[`results/2026-11-09-aws-s3-c7gd-4xlarge/`](results/2026-11-09-aws-s3-c7gd-4xlarge/).
They have the same layout as the 2026-10-31 sets.

## Previous results: 2026-10-31

These results were measured on 2026-10-31 in AWS us-west-2, after the Worker dispatch fix in #1131. They are the latest full run, and the latest numbers for S2, S3, and S6. They used the old S2, S3, and S6 method, with a new Flow per operation, and history offload was off.

- **Server:** one server-zd node on a c7gd instance. RocksDB was on the local NVMe instance store, and the log went to S3 Standard in the same Region.
- **Load host:** a separate c7g.8xlarge ran `dexbench` in the same Availability Zone. Its mean CPU stayed below 47% of the host at every point.
- **Version:** the headline runs used server-zd `3784ba838`, which merged as `f2e9dd611` with the same server-zd tree. The search rounds used `b4a733cce`, an earlier revision of #1131 without its lost-wakeup fix.
- **Measurement:** each point is a 10-second warmup and a 45-second measurement on a newly started server. Every headline point ran three times with profiling off. A point with three runs shows the median of the three.

Each table lists several concurrencies per scenario, so you can compare latency at different throughput levels. The peak row of a scenario has bold throughput. S2 and S3 show the latency of one Step. Points that have three final trials show the median. The others are single runs from the search rounds.

### c7gd.large (2 vCPU)

| Scenario | C | Throughput | p50 / p90 / p99 (ms) |
| --- | ---: | ---: | --- |
| S2 SYNC Steps, latency per Step | 256 | 1,165 Steps/s | 186 / 329 / 538 |
|  | 512 (peak) | **1,436 Steps/s** | 362 / 468 / 575 |
|  | 768 | 1,268 Steps/s | 578 / 882 / 1,239 |
|  | 1,024 | 1,283 Steps/s | 799 / 1,259 / 1,726 |
| S3 ASYNC Steps, latency per Step | 32 (peak) | **1,538 Steps/s** | 8.8 / 22.5 / 35.6 |
|  | 128 | 1,422 Steps/s | 84.4 / 138.9 / 207.2 |
|  | 512 | 1,391 Steps/s | 388 / 588 / 762 |
| S1 Flows started and completed | 256 | 804 Flows/s | 299 / 447 / 594 |
|  | 512 (peak) | **889 Flows/s** | 603 / 733 / 873 |
|  | 1,024 | 798 Flows/s | 1,307 / 1,865 / 2,139 |
| S4 plain RPC, reads one 8-byte Attribute | 16 | 7,248 RPCs/s | 2.0 / 3.6 / 5.5 |
|  | 32 (peak) | **7,426 RPCs/s** | 4.4 / 6.2 / 8.1 |
|  | 128 | 6,615 RPCs/s | 20.8 / 24.7 / 34.8 |
|  | 512 | 5,842 RPCs/s | 88.4 / 97.0 / 221.6 |
| S4 Attribute-locking RPC, updates one 8-byte Attribute | 256 | 1,332 RPCs/s | 197 / 261 / 366 |
|  | 384 | 1,514 RPCs/s | 267 / 324 / 417 |
|  | 512 | 1,614 RPCs/s | 334 / 398 / 530 |
|  | 768 (peak) | **1,805 RPCs/s** | 448 / 534 / 631 |
|  | 1,024 | 1,365 RPCs/s | 786 / 854 / 908 |
| S5 direct read of ten 16 KiB Attributes (160 KiB) | 256 (peak) | **6,032 reads/s** | 42.4 / 58.3 / 72.2 |
|  | 512 | 5,359 reads/s | 89.3 / 135.2 / 240.9 |
|  | 1,024 | 5,417 reads/s | 188 / 236 / 279 |
| S6 Channel wake | 64 | — | 93.3 / 130.6 / 216.9 |

### c7gd.4xlarge (16 vCPU)

| Scenario | C | Throughput | p50 / p90 / p99 (ms) |
| --- | ---: | ---: | --- |
| S2 SYNC Steps, latency per Step | 2,048 | 7,854 Steps/s | 235 / 304 / 397 |
|  | 4,096 | 8,694 Steps/s | 418 / 542 / 694 |
|  | 8,192 (peak) | **10.8k Steps/s** | 683 / 837 / 1,013 |
|  | 12,288 | 10.8k Steps/s | 1,028 / 1,220 / 1,485 |
|  | 16,384 | 9,790 Steps/s | 1,498 / 1,856 / 2,277 |
| S3 ASYNC Steps, latency per Step | 256 | 10.2k Steps/s | 6.9 / 15.2 / 20.9 |
|  | 1,024 | 9,360 Steps/s | 61.1 / 93.4 / 114.5 |
|  | 4,096 (peak) | **10.6k Steps/s** | 280 / 339 / 380 |
| S1 Flows started and completed | 3,072 | 4,853 Flows/s | 624 / 769 / 938 |
|  | 6,144 | 5,276 Flows/s | 1,148 / 1,370 / 1,562 |
|  | 12,288 (peak) | **6,176 Flows/s** | 1,970 / 2,294 / 2,580 |
| S4 plain RPC, reads one 8-byte Attribute | 48 | 41.9k RPCs/s | 1.0 / 1.7 / 2.6 |
|  | 128 (peak) | **49.3k RPCs/s** | 2.6 / 3.5 / 4.5 |
|  | 256 | 44.6k RPCs/s | 5.8 / 7.5 / 9.3 |
|  | 512 | 42.9k RPCs/s | 12.2 / 15.3 / 18.1 |
|  | 2,048 | 40.9k RPCs/s | 53.4 / 57.6 / 61.3 |
| S4 Attribute-locking RPC, updates one 8-byte Attribute | 1,024 | 8,263 RPCs/s | 121 / 156 / 245 |
|  | 2,048 | 9,940 RPCs/s | 204 / 252 / 310 |
|  | 4,096 | 12.2k RPCs/s | 336 / 396 / 444 |
|  | 6,144 | 12.9k RPCs/s | 478 / 567 / 656 |
|  | 8,192 (peak) | **13.1k RPCs/s** | 626 / 754 / 856 |
| S5 direct read of ten 16 KiB Attributes (160 KiB) | 256 (peak) | **11.3k reads/s** | 21.4 / 36.6 / 53.2 |
| S6 Channel wake | 64 | — | 83.8 / 124.6 / 180.8 |

**Notes.**
- **C** is the number of concurrent clients. S6 reports latency only, from a Channel publish to the waiting Step.
- Load is closed-loop, so latency grows with C once the server is saturated. The lowest-C row of a scenario is the closest to an idle server. S1 and S2 were not measured at lower C than shown.

C is the number of concurrent clients. Each concurrency was chosen by searching first; the per-instance `README.md` files list every point.

- **ASYNC with low latency:** on c7gd.4xlarge, S3 at C=256 reaches 10,210 Steps/s median with a Step p50 of 7.1 ms. The C=4,096 point is only 4% higher.
- **Attribute-locking RPC on c7gd.4xlarge:** the C=8,192 point raises `limits.maxInFlightRpcsPerNode` to 16,384. With the 4,096 limit used everywhere else, C=4,096 gives 12,174 RPCs/s median.
- **S4 and S5 are not comparable.** An S4 RPC reads or updates one 8-byte counter and returns 8 bytes. An S5 read returns 160 KiB and takes two calls. At its peak, S5 on c7gd.4xlarge moves about 1.8 GB/s, the 15 Gbps network limit of that instance.
- **Before the fix:** the previous run of this benchmark, before #1131, measured 7,544 SYNC Steps/s and 34,611 plain RPCs/s on c7gd.4xlarge. Throughput then collapsed above about 2,048 concurrent Flows. It now stays flat: S2 gives 9.8k–10.8k Steps/s from C=8,192 to 16,384.

### Settings that produced these numbers

- **c7gd.large:** the default server-zd configuration, 4 gRPC connections.
- **c7gd.4xlarge:** the defaults plus the settings below, with 16 gRPC connections. Attribute-locking RPC at C=8,192 also sets `limits.maxInFlightRpcsPerNode: 16384`.

  ```yaml
  commit:
    maxUnappliedBytes: 268435456
  limits:
    maxInFlightRpcsPerNode: 4096
  localStorage:
    blockCacheBytes: 8589934592
    writeBufferBytes: 134217728
  ```

With server-zd 121a3c156 or later, the defaults follow the host size. Applier lanes are `max(8, CPUs)`, and RocksDB background jobs are `max(2, CPUs / 2)`.

The result sets are in
[`results/2026-10-31-aws-s3-c7gd-large/`](results/2026-10-31-aws-s3-c7gd-large/)
and
[`results/2026-10-31-aws-s3-c7gd-4xlarge/`](results/2026-10-31-aws-s3-c7gd-4xlarge/).
Each set's `README.md` lists every round of the search and its outcome. Each round directory holds what is needed to rerun it:
- `config.env`: instance types, placement, disk, versions, and `dexbench` settings.
- `zd-config.yaml`: the exact server-zd configuration.
- `points.txt`: every point in the round.

Search rounds also keep a perf profile and thread stacks for every point, in `<point>.server/`.

Rerun a headline point with `benchmark/aws/reproduce.sh`. See [Reproduce a result](#reproduce-a-result).

The [local search backend results](results/2026-10-31-search-local/README.md)
measure S7 at 100k and 1M runs on builtin, ParadeDB, and PostgreSQL. They include
projection throughput, WAL, Attribute backfill, and a confirmed ParadeDB Flow
type index collation finding.
The [local search outbox results](results/2026-11-01-search-outbox-local/README.md)
repeat the 1M ten-Attribute ParadeDB and PostgreSQL points with the SearchOutbox
projection, and measure the cost of its acks.

## Reading the results

### What the numbers mean

The figures in this section come from the 2026-10-31 run.

- **A SYNC Step waits for its commits to reach S3, and an ASYNC Step does not.**
  - Each log commit is one S3 PUT round trip. At the SYNC peaks, commits averaged 36–39 ms on c7gd.large and 51–57 ms on c7gd.4xlarge.
  - Under load, a SYNC Step also waits for its batch and for a free PUT slot, so its per-Step latency is 360–710 ms at the peaks.
  - Closed-loop SYNC throughput is roughly concurrency divided by Step latency, so SYNC Steps need thousands of concurrent Flows to reach the peak.
  - An ASYNC Step moves on before its commit, so its per-Step latency is 7–9 ms at low concurrency. ASYNC throughput is limited by the server, not by latency.
- **S3 PUTs scale with batches, not with Steps.** Commands are grouped into log batches, with at most `commit.maxInFlight` (default 4) PUTs in flight. A busy node made 28–132 log PUTs per second at the peaks, independent of instance size. At USD 0.005 per 1,000 PUTs, that is at most about USD 2.3 per busy hour per node.
- **Both instance types are now limited by CPU.**
  - c7gd.large uses 92–98% of its two cores at every throughput peak.
  - c7gd.4xlarge uses 77–86% of its 16 cores at the S1–S4 peaks. RocksDB never stalls, and the load host is far from busy.
  - S5 on c7gd.4xlarge is limited by the 15 Gbps network, not by the server, so its CPU stays near 28%.
- **Tuning beyond the defaults did not help.** The earlier search tried `commit.maxInFlight` from 2 to 16, applier lanes from 2 to 32, and `commit.batchWindow` of 50 ms. None beat the defaults beyond run-to-run noise. This run kept those defaults.

Each run writes one JSON file per scenario variant and concurrency, plus
`report.md`, `charts.html`, and `host.txt`. `report.md` has one table per
variant. `charts.html` is a self-contained page that plots each metric against
concurrency.

### Scenarios

| ID | Variant | What it measures |
| --- | --- | --- |
| S1 | `start-complete` | Start and complete a short Flow with one Step |
| S2 | `sequential-steps` | Back-to-back SYNC Steps in one long-lived Flow per client |
| S3 | `sequential-steps-async` | Back-to-back ASYNC Steps in one long-lived Flow per client |
| S4 | `rpc-plain`, `rpc-locking` | An RPC that reads one 8-byte Attribute, and an RPC that locks and updates it |
| S5 | `attribute-read-direct` | Read ten 16 KiB Attributes, 160 KiB in total, from the client without a Worker |
| S6 | `channel-wake` | Latency from a Channel publish to the waiting Step of a long-lived Flow |
| S7 | `dexbench search` | Search latency, seed projection throughput, WAL, and PostgreSQL backfill |

The scenario name `rpc` runs both S4 variants. The name `attribute-read` runs
S5.

**S1 `start-complete`.** Each client starts a new Flow whose only Step
completes it. The client then waits for the Flow result. The timed operation
is StartFlow plus WaitForFlow. Throughput is in Flows per second. Every
operation creates a new, short Flow. With history offload on, each closed
Flow uploads one history segment, so S1 also measures that cost.

**S2 `sequential-steps`.** Before warmup, each client starts one Flow that
stays open for the whole point. Its SYNC Step moves to itself with GoTo, so
the Flow runs Steps back to back until the point ends and the client stops
it. The timed operation is one Step: the gap between the start of one Step's
Execute and the start of the next one, measured inside the Worker.
Throughput is in Steps per second. `DEXBENCH_STEPS` is no longer used.
Results of 2026-10-31 and earlier used a new ten-Step Flow per operation, so
they are not directly comparable.

**S3 `sequential-steps-async`.** This is S2 with ASYNC Step durability. A SYNC
Step result is committed to object storage before the Flow moves on. An ASYNC
result lets the Flow move on first, and the commit follows in a later batch.
If the server node fails before that commit, the Step runs again, and so does
any work that started from it. Timing, throughput unit, and per-Step latency
are the same as in S2.

**S4 `rpc-plain` and `rpc-locking`.** Before warmup, each client starts one
Flow that stays open. The Flow has one `int64` counter Attribute, 8 bytes on
the wire. The plain variant invokes an RPC that reads the counter and returns
it. The locking variant invokes an RPC that locks the counter, reads it, and
writes it back. Both RPCs run on the Worker and return only the `int64`. Each client uses its own Flow, so the lock has no
contention. The timed operation is one InvokeRPC call. Throughput is in RPCs
per second. Keep the concurrency below the server's
`limits.maxInFlightRpcsPerNode`, which defaults to 1000.

**S5 `attribute-read-direct`.** Before warmup, each client starts one Flow
that stays open and holds ten 16 KiB string Attributes. Each read calls
FlowService GetAttributes through the generated gRPC client. No Worker is
involved. Values over the server's inline limit,
`values.wireInlineThresholdBytes` (default 100 bytes), come back as blob
references, so all ten 16 KiB values do. The client resolves every
reference with one LoadBlobs call, as an uncached client would. It then
compares every value with the value written at Flow start. The timed
operation is GetAttributes plus LoadBlobs plus the check. Each read is
therefore two calls and returns 160 KiB, about 20,000 times the data of an S4
RPC. Throughput is in reads per second, and it is limited by bandwidth: at
11,258 reads per second, c7gd.4xlarge moves about 1.8 GB/s. JSON results record the verified bytes per read and the
number of blobs loaded.

**S6 `channel-wake`.** Before warmup, each client starts one Flow that stays
open for the whole point. Its Step waits on a Channel, and after each wake it
moves to itself to wait again. After the Step's WaitFor returns, the client
polls GetFlowState until the Step is waiting. Gating the poll on WaitFor keeps it near one
GetFlowState call per wake. The client then publishes one message with
FlowService PublishToChannel. Only the time from the publish call to the start
of the Step's Execute is timed. Throughput includes the untimed setup, so use
only the latency of this scenario.

### Latency and coordinated omission

The load is closed-loop. Each client sends its next request only after the
previous one returns, and the client count is fixed. When the server slows
down, clients send less. Queueing delay that an open-loop workload would see
never appears in the samples. This coordinated omission makes p99 and max
optimistic near saturation. Read the tail percentiles together with
throughput, and compare runs at the same concurrency. Percentiles cover
successful operations only. Failed and timed-out operations are counted as
errors.

## Running the benchmark

### Local smoke

A short local run checks that every scenario works. Its numbers are not
results. It needs a `dexcli` whose `dexcli dev --help` lists `-engine`, and a
`dex-server-zd` binary:

```bash
DEXBENCH_DURATION=10 DEXBENCH_WARMUP=1 DEXBENCH_CONCURRENCY=2 \
  DEXBENCH_DEXCLI_BINARY="$(command -v dexcli)" \
  DEXBENCH_ZD_BINARY=/path/to/dex-server-zd \
  benchmark/local/run.sh
```

To build both binaries from this checkout, run the commands below. The
`dexcli` build also needs Node.js 22 and npm to compile the embedded Web
assets.

```bash
(cd web && npm ci && npm run build)
(cd cli && go build -o "$HOME/.local/bin/dexcli" ./cmd/dexcli)
(cd server-zd && cargo build --release -p dexzd-server)
```

The release build writes `server-zd/target/release/dex-server-zd`.

`run.sh` starts server-zd through `dexcli dev` with its embedded s3local. It
does not start Docker or use AWS. It sets `dev.durabilityMode: processCrashSafe`,
so results stay comparable with earlier runs; the dev default, strict, fsyncs
every write.

### Full local run

```bash
benchmark/local/run.sh
benchmark/bin/dexbench report <result-directory>/*.json
benchmark/bin/dexbench chart <result-directory>/*.json > charts.html
```

By default the script writes to a new temporary directory outside the
repository and prints its path at the end. `benchmark/results/` holds
committed result sets from recorded benchmark hosts. To write one, set
`DEXBENCH_OUT=benchmark/results/<run>`. Never commit a smoke run. The temporary
server data and logs are removed when the script exits.

The defaults are a 15-second warmup and a 1-minute measurement at concurrency
1 and 32. The six scenarios expand to seven variants, so a run has 14 steps.
Each step takes 75 seconds plus a few seconds of server restart. Expect about
19 minutes. For a longer sweep, set
`DEXBENCH_DURATION=180 DEXBENCH_WARMUP=30 DEXBENCH_CONCURRENCY=1,8,32,128`.
It takes about 1 hour 45 minutes.

### Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `DEXBENCH_DURATION` | `60` | Measured seconds per step |
| `DEXBENCH_WARMUP` | `15` | Warmup seconds per step |
| `DEXBENCH_CONCURRENCY` | `1,32` | Comma-separated client counts |
| `DEXBENCH_SCENARIOS` | all six | Comma-separated scenario or variant names |
| `DEXBENCH_STEPS` | `10` | Unused; S2 and S3 run long-lived Flows |
| `DEXBENCH_WORKERS` | `0` | Pull Worker task slots; `0` uses the concurrency |
| `DEXBENCH_CONNECTIONS` | `1` | Client and Worker pairs, each with its own gRPC connections |
| `DEXBENCH_OUT` | new temporary directory | Result directory |
| `DEXBENCH_DEXCLI_BINARY` | `dexcli` on `PATH` | The `dexcli` to run |
| `DEXBENCH_ZD_BINARY` | `dex-server-zd` on `PATH` | The server-zd binary |
| `DEXBENCH_MAX_LOAD` | core count | Wait until the 1-minute load average is below this |

Before each build and step, the script waits until the one-minute load average
is below `DEXBENCH_MAX_LOAD`.

### State between steps

Every combination of scenario variant and concurrency runs on a newly started
server with empty state. The script starts `dexcli dev` with a new server-zd
data directory. S2 to S6 start one Flow per client before warmup, so the warmup
and the measurement of one step share those Flows. The client stops them after
the measurement. S1 starts a new Flow for every operation.

### Resource sampling

CPU and RSS are sampled every second during the measurement. Each sample
covers only the preceding interval. CPU % is 100 per fully used core. On
Linux, CPU is the delta of utime plus stime from `/proc/<pid>/stat` over the
whole process tree of each root PID. The tree is resolved again at every
sample, so forked processes are included. On macOS, the sampler uses the delta
of cumulative `ps` CPU time with 10 ms resolution. This is best effort and
labeled as such in `cpu_method`.

The root is the `dexcli dev` process, so server-zd and its embedded s3local are
included. `dexbench` also samples its own process as the load host. The report
shows that CPU as a share of all load-host cores.

### Result metadata

Each JSON contains the configuration, server and SDK versions, the Git commit,
machine details, attempts, successes, measured errors, and unmeasured warmup
or cleanup errors. It also has throughput over the actual elapsed time, exact
latency percentiles, per-interval CPU and RSS samples, and server-zd
object-store and commit metrics when available. Object storage requests per minute are
reported per operation (GET, PUT, LIST, DELETE) and per object kind. `host.txt`
records the CPU model, core count, RAM, and free disk.

Each JSON also records the measurement start and end times, the load host CPU
samples, the connection count, the trial number, and the server settings that
the point overrides.

### AWS with real S3

The AWS mode runs server-zd on one EC2 host against an S3 bucket. `dexbench`
runs on a second host in the same placement group. For every point, the load
host connects to the server host over SSH. It stops the server, deletes the
local store, and starts the server with a new object storage prefix,
`<run id>/<point id>`. Every point therefore starts from empty state.

The scripts live in `benchmark/aws/`:

| Script | Runs on | Does |
| --- | --- | --- |
| `launch.sh` | your machine | Creates the bucket, endpoint, IAM role, security groups, placement group, and instances |
| `serve.sh` | server host | Writes the server-zd config, starts and stops the server, and collects server-side data |
| `run-remote.sh` | load host | Runs every point and writes the result set |
| `reproduce.sh` | load host | Reruns points of a recorded result set |
| `teardown.sh` | your machine | Deletes every resource of the run and checks that none remains |

#### Prerequisites

- The AWS CLI v2 and `openssl` on your machine, with a profile that can manage
  EC2, S3, and IAM.
- An EC2 key pair in the region.
- Linux arm64 builds of `dexbench` and `dex-server-zd`. Both hosts run Ubuntu
  24.04, so build both binaries on Ubuntu 24.04 arm64, such as on the load
  host, to match its glibc. Build `dexbench` with
  `cd benchmark/dexbench && go build -buildvcs=true -o ../bin/dexbench ./cmd/dexbench`, so
  `config.env` records its revision.
- The `DEXBENCH_ZD_APPLIER_LANES`, `DEXBENCH_ZD_APPLIER_QUEUE_FRAMES`,
  `DEXBENCH_ZD_MAX_BATCH_RECORDS`, `DEXBENCH_ZD_MAX_UNAPPLIED_BYTES`,
  `DEXBENCH_ZD_MAX_BACKGROUND_JOBS`, and `DEXBENCH_ZD_WRITE_BUFFER_BYTES`
  settings need a server-zd build that includes PR #1110. Older builds reject
  those keys at startup.

#### Launch

Put both hosts in one Availability Zone and one cluster placement group.
Instances are Spot by default; set `DEXBENCH_MARKET=on-demand` for an
on-demand instance, such as a load host larger than the Spot vCPU quota. The
server is a c7gd instance, so RocksDB runs on its local NVMe instance store.
A c7gd instance has the same Graviton3 CPU, vCPU count, and memory as the c7g
of the same size. The load host needs no instance store. S3 traffic goes
through a free gateway endpoint. The bucket expires objects after one day.
If an Availability Zone has no capacity for the c7gd type, launch the server
first in another zone, then put the load host in the same zone and placement group.

```bash
export DEXBENCH_RUN_ID=2026-10-30-s3 DEXBENCH_AWS_PROFILE=<profile> DEXBENCH_AWS_REGION=us-west-2
export DEXBENCH_AZ=us-west-2b DEXBENCH_KEY_NAME=<key pair>
benchmark/aws/launch.sh infra
DEXBENCH_SERVER_INSTANCE_TYPE=c7gd.large benchmark/aws/launch.sh server
DEXBENCH_MARKET=on-demand DEXBENCH_LOAD_INSTANCE_TYPE=c7g.8xlarge benchmark/aws/launch.sh load
```

`launch.sh infra` writes the resource IDs and the bucket name to
`~/.dexbench-<run id>.env`. The server security group accepts SSH, the API
port 8801, and the metrics port 9901 from the load host only. SSH from your
machine is open to your current IP address.

Each instance runs `shutdown -h +150` at boot and terminates on shutdown. If
you lose your connection, the instances delete themselves after two and a half
hours. Set `DEXBENCH_SHUTDOWN_MINUTES` to change that limit. The load host
deletes its results with it, so copy the result directory off the load host
before its timer runs out.

#### Run

Wait for boot setup to finish on both hosts with `cloud-init status --wait`.
It mounts the instance store, sets the core dump path, and installs `iostat`,
`eu-stack`, and perf.

Copy `dexbench`, `dex-server-zd`, and `serve.sh` to `~/dexbench/` on the
server host. Clone this repository on the load host at the commit under test,
and copy `dexbench` there. Connect to the load host with `ssh -A`, so it can
reach the server with your key. Then run from the clone on the load host:

```bash
export DEXBENCH_SERVER_HOST=ubuntu@<server private IP>
export DEXBENCH_SERVER_ADDRESS=<server private IP>
export DEXBENCH_S3_BUCKET=<bucket from launch.sh>
export DEXBENCH_BENCH_BINARY=$HOME/dexbench/dexbench
export DEXBENCH_OUT=$HOME/results/2026-10-30-aws-s3-c7gd-large
export DEXBENCH_SERVER_ZD_COMMIT=<commit the server binary was built from>
DEXBENCH_SCENARIOS=sequential-steps DEXBENCH_CONCURRENCY=1,128,256,512 \
  benchmark/aws/run-remote.sh
```

Each point uses a 10-second warmup and a 45-second measurement by default.
A source build of server-zd reports only its crate version, so set
`DEXBENCH_SERVER_ZD_COMMIT` to record its commit in `config.env`. The
`dexbench` revision comes from the binary itself. Copy the result directory
back with `scp -r` and commit it under `benchmark/results/`.

A point that fails, for example because the server exits during startup, is
counted and skipped, and the run continues. If `dexbench` itself fails, its
JSON is kept as `<point id>.failed.json` and left out of the report. Each
invocation writes to a new object storage prefix, `<run id>/<invocation>/<point
id>`, recorded as `RECORDED_S3_PREFIX_ROOT` in `config.env`.

#### Points and server settings

Without `DEXBENCH_POINTS`, the script runs every scenario in
`DEXBENCH_SCENARIOS` at every concurrency in `DEXBENCH_CONCURRENCY`. For a
tuning sweep, list the points in a file and set `DEXBENCH_POINTS` to its path.
Each line names a scenario, a concurrency, and optional server settings for
that point:

```text
# scenario concurrency [DEXBENCH_ZD_NAME=value ...]
sequential-steps 512
sequential-steps 512 DEXBENCH_ZD_MAX_IN_FLIGHT=8
sequential-steps 512 DEXBENCH_ZD_MAX_IN_FLIGHT=16 DEXBENCH_ZD_BATCH_WINDOW=5ms
```

Settings exported on the load host apply to every point. Settings on a line
override them for that point. `serve.sh` writes a key into the server-zd
config only when its variable is set:

| Variable | server-zd key |
| --- | --- |
| `DEXBENCH_ZD_PARTITIONS` | `cluster.partitionCount` |
| `DEXBENCH_ZD_MAX_IN_FLIGHT` | `commit.maxInFlight` |
| `DEXBENCH_ZD_BATCH_WINDOW` | `commit.batchWindow` |
| `DEXBENCH_ZD_APPLIER_LANES` | `commit.applierLanes` |
| `DEXBENCH_ZD_APPLIER_QUEUE_FRAMES` | `commit.applierQueueFrames` |
| `DEXBENCH_ZD_MAX_BATCH_RECORDS` | `commit.maxBatchRecords` |
| `DEXBENCH_ZD_MAX_UNAPPLIED_BYTES` | `commit.maxUnappliedBytes` |
| `DEXBENCH_ZD_MAX_BACKGROUND_JOBS` | `localStorage.maxBackgroundJobs` |
| `DEXBENCH_ZD_WRITE_BUFFER_BYTES` | `localStorage.writeBufferBytes` |
| `DEXBENCH_ZD_BLOCK_CACHE_BYTES` | `localStorage.blockCacheBytes` |
| `DEXBENCH_ZD_MAX_INFLIGHT_RPCS` | `limits.maxInFlightRpcsPerNode` |

For any other key, set `DEXBENCH_ZD_EXTRA` to comma-separated
`section.key=value` pairs, such as `snapshot.interval=1h`. server-zd rejects
unknown keys at startup, so a wrong key fails fast. If a key is renamed, edit
the table at the top of `serve.sh`.

Other settings:

| Variable | Default | Meaning |
| --- | --- | --- |
| `DEXBENCH_TRIALS` | `1` | Runs of each point; the report adds the median and maximum |
| `DEXBENCH_CONNECTIONS` | `1` | Client and Worker pairs; raise it so the load host is never the limit |
| `DEXBENCH_DURATION`, `DEXBENCH_WARMUP` | `45`, `10` | Seconds per point |
| `DEXBENCH_REMOTE` | `ssh` | `local` runs the server-side commands on this host |
| `DEXBENCH_SERVER_HOME` | `dexbench` | Directory of `serve.sh` on the server host |
| `DEXBENCH_STORAGE_DIR` | `/mnt/nvme/dexzd` | RocksDB directory on the server host; deleted at every point |
| `DEXBENCH_S3_REGION` | `us-west-2` | Bucket region |
| `DEXBENCH_S3_ENDPOINT` | empty | Custom S3 endpoint, such as a local s3local |
| `DEXBENCH_S3_CREDENTIALS` | `defaultChain` | `defaultChain` uses the instance profile |
| `DEXBENCH_PROFILE` | `1` | `1` profiles the server in the middle of each measurement; `0` turns it off |
| `DEXBENCH_PROFILE_SECONDS`, `DEXBENCH_PROFILE_FREQUENCY` | `10`, `49` | Length and sampling rate (Hz) of the perf profile |

#### What each point collects

Next to `<point id>.json`, the directory `<point id>.server/` holds:

- `zd-config.yaml`: the exact server-zd config of the point.
- `server-samples.json`: server host CPU and RSS every second. They are merged
  into the JSON for the measurement window only.
- `metrics-before.txt` and `metrics-after.txt`: the raw server `/metrics`
  output. They cover the warmup and the measurement.
- `iostat.txt`: `iostat -dxmy 5` for the whole point, on Linux.
- `rocksdb-stalls.txt`: counts of `Stalling writes` and `Stopping writes` in
  each RocksDB `LOG` file.
- `server.log`: the server log.
- `perf-report.txt` and `perf-script.txt.gz`: a perf profile with DWARF call
  stacks, taken a third of the way into the measurement. `perf-script.txt.gz`
  can be turned into a flame graph off the host.
- `thread-stacks-1.txt` and `thread-stacks-2.txt`: `eu-stack` stacks of every
  server thread, before and after the profile. Two snapshots show which
  threads stay blocked in the same place.
- `crash.txt`: written only when the server was not running at the end of the
  point. It holds the backtrace of each core dump. The cores stay in
  `/mnt/nvme/cores` on the server host, so copy one off before teardown when
  you need it.

The profile slows the server while it runs: `eu-stack` pauses every thread
briefly, and perf copies each sampled stack. The profile is symbolized after
the measurement. Set `DEXBENCH_PROFILE=0` for the trials whose numbers you
report.

Only cores written during the point are reported. Cores are never deleted, so
a series of crashes can fill `/mnt/nvme`.

`report.md` shows server CPU, its share of the server host, the load host CPU
share, and PUT requests per second. A point whose load host CPU is above 70%
is marked invalid, because the load generator may have been the bottleneck.

#### Reproduce a result

`run-remote.sh` writes `config.env`, `zd-config.yaml`, `points.txt`, and
`host.txt` into every result set. To rerun a point on new hosts, set the host
and bucket variables as above and run:

```bash
benchmark/aws/reproduce.sh benchmark/results/<run> sequential-steps 512
```

It loads the recorded settings from `config.env` and the matching lines from
`points.txt`. Omit the scenario and concurrency to rerun every point.

#### Test without AWS

Run the same loop on one machine against a standalone s3local:

```bash
(cd s3local && cargo build --release -p s3local-server)
s3local/target/release/s3local --listen-address 127.0.0.1:9100 --data-directory /tmp/s3data \
  --signature-verification strict &
curl --aws-sigv4 aws:amz:us-east-1:s3 --user s3local:s3local -X PUT \
  -H 'x-amz-content-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855' \
  http://127.0.0.1:9100/dexbench
DEXBENCH_REMOTE=local DEXBENCH_SERVER_ZD_BINARY=server-zd/target/release/dex-server-zd \
  DEXBENCH_SERVER_BENCH_BINARY="$PWD/benchmark/bin/dexbench" \
  DEXBENCH_STORAGE_DIR=/tmp/dexbench-store DEXBENCH_STATE_DIR=/tmp/dexbench-state \
  DEXBENCH_LISTEN_HOST=127.0.0.1 DEXBENCH_S3_BUCKET=dexbench DEXBENCH_S3_REGION=us-east-1 \
  DEXBENCH_S3_ENDPOINT=http://127.0.0.1:9100 DEXBENCH_S3_CREDENTIALS=environment \
  DEX_ZD_OBJECT_STORE_ACCESS_KEY_ID=s3local DEX_ZD_OBJECT_STORE_SECRET_ACCESS_KEY=s3local \
  DEXBENCH_DURATION=10 DEXBENCH_WARMUP=2 benchmark/aws/run-remote.sh
```

The s3local region is `us-east-1`. Its numbers are not results.

#### Clean up

Terminate the server after its measurements, before you launch the next size.
This keeps the load host and its results:

```bash
benchmark/aws/teardown.sh server
```

`teardown.sh instances` terminates every instance of the run.

After the last run, delete everything:

```bash
benchmark/aws/teardown.sh
```

It terminates the instances and deletes the placement group, security groups,
gateway endpoint, IAM role and instance profile, and the bucket with its
objects. It deletes only the bucket named in `~/.dexbench-<run id>.env` or
`DEXBENCH_S3_BUCKET`, and only when its `dex-benchmark` tag matches the run. It then lists each resource type by tag or name and fails if anything
remains. An S3 gateway endpoint that existed before `launch.sh` is left in
place.

## S7 search backend benchmark

S7 runs separately from the timed execution scenarios. Use a fresh server and
empty search database for each point. Build the release server and harness:

```bash
(cd server-zd && cargo build --release -p dexzd-server)
(cd benchmark/dexbench && GOWORK=off go build -o ../bin/dexbench ./cmd/dexbench)
benchmark/local/search-local.sh builtin 100000 10 /tmp/search-builtin-100k
benchmark/local/search-local.sh paradedb 100000 10 /tmp/search-paradedb-100k
benchmark/local/search-local.sh postgres 100000 10 /tmp/search-postgres-100k
benchmark/local/search-local.sh postgres 100000 10 /tmp/search-backfill-100k --backfill
```

The runner starts a release server with embedded s3local, processCrashSafe
durability, and the default 1,024 partitions. External backends use temporary, uniquely named Docker containers:
PostgreSQL 17 or ParadeDB 0.26.1, matching server-zd CI. It removes its server,
data directory, and container on exit. It does not use docker compose.
`DEXBENCH_PARTITIONS`, `DEXBENCH_SEARCH_SCAN_BUDGET` (100000),
`DEXBENCH_SEARCH_SAMPLES` (default 100),
`DEXBENCH_SEARCH_CONCURRENCY` (128), `DEXBENCH_SEARCH_PORT` (18815), and
`DEXBENCH_SEARCH_METRICS_PORT` (19195) override runner settings.

Each point seeds N unique runs in one tenant. Initial indexed Attribute writes
are followed by a short completing Step; 5% proceed to a waiting Step instead.
Flow types are about 80% common, 19.95% secondary, and 0.05% rare. Keyword
values are about 80% common, 19.9% secondary, and 0.1% rare. Int values cycle
through 0–99,999; KeywordArray has a shared element and one of 100 tags; Text
has three shared words and one of 100 topic terms. The ten-Attribute case adds
six Keywords to those four fields. The three-Attribute case omits Text.

Seed throughput is N divided by elapsed time from the first start through
projection convergence. It includes Flow execution, so it measures achieved
projection throughput under this workload, not the projection's maximum
capacity in isolation. Every completing Flow is awaited. Builtin search reads
the committed local view; external search additionally waits for exact row
and Completed/Running counts. Initial PostgreSQL slots must be ready before
seeding. Run 0, 3, and 10 Attributes on separate fresh servers:

```bash
for attributes in 0 3 10; do
  benchmark/local/search-local.sh postgres 100000 "$attributes" "/tmp/search-pg-$attributes"
done
```

Zero- and three-Attribute runner points measure projection only. The ten-Attribute
point measures every query at page size 20, with ten warmups and 100 sequential
samples: default order, common/rare Flow type, Keyword equality, Int range,
KeywordArray membership, Text terms, explicit StartTime descending, and page
500 reached by following 499 opaque next-page tokens. Only the final page
request is timed. These are end-to-end Go Client timings, including transport
and indexed-value hydration; compare them cautiously with SQL EXPLAIN results.
Raw JSON retains each successful latency and each error. PostgreSQL ten-Attribute
points also retain matching SQL EXPLAIN ANALYZE plans after the timed queries. Builtin scan-budget
failures are recorded. For full-scan timings above 100k runs, set
`DEXBENCH_SEARCH_SCAN_BUDGET` to the dataset size and record that override.

PostgreSQL and ParadeDB record pg_current_wal_lsn before and after seeding and
catchup. Their difference divided by N includes all database WAL in that
interval, including updates, checkpoints, and background maintenance. The
container contains only this experiment's database.

The separate `--backfill` run withholds Keyword registration while writing its
indexed values. After catchup it registers that key, observes backfilling,
and measures rare and common equality while the slot is still backfilling.
A sample is retained only when state probes bracket it with backfilling. Failed
requests receive the same probes; errors spanning readiness are recorded as
`transition_errors`, and errors never end sampling without an observed state change.
An independent readiness monitor measures registration-to-ready time. It then
measures the same equality predicates with the ready slot. This diagnostic
point has nine registered Attributes during seeding; exclude its seed throughput
and WAL from the ordinary ten-Attribute comparison.

For an already running isolated server, invoke `dexbench search --help`.
Use `--projection-only` to omit query timings. Small smoke runs must reduce
`--deep-page` to fit their dataset. Result sets are local measurements and
should state host contention, versions, Docker resources, and method explicitly.
