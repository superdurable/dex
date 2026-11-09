# Long-lived runs and history offload checks

These measurements were taken on 2026-11-09. The first sections use server-zd `f9254ef20`; the recheck uses v0.1.23. They check `dexbench` d99724b0d (now `92622eb1b` after a rebase), which keeps one run open per client for `sequential-steps`, `sequential-steps-async`, and `channel-wake`. They found that Step throughput on a long-lived run falls as the run ages. They also count the object storage PUTs of short runs with history offload on.

The server settings match the AWS rounds of this date: history offload on, deletion of expired history objects on, and a 50,000-event history window. See `scripts/zd.yaml`. The offload-off control uses `scripts/zd-off.yaml`.

## Step throughput falls as a long-lived run ages

Each client runs one chain run. The chain Step moves to itself, so the run gains one Step execution per Step. Only the warmup changes between the two points of a pair, so the measured window starts at a different run age.

| Case | C | Warmup | Measurement | Throughput | Step p50 |
| --- | ---: | ---: | ---: | ---: | ---: |
| S3 ASYNC, offload on, local | 8 | 2 s | 15 s | 958 Steps/s | 7.3 ms |
| S3 ASYNC, offload on, local | 8 | 90 s | 15 s | 247 Steps/s | 27.5 ms |
| S2 SYNC, offload on, local | 8 | 2 s | 15 s | 991 Steps/s | 7.8 ms |
| S2 SYNC, offload on, local | 8 | 90 s | 15 s | 291 Steps/s | 27.5 ms |
| S2 SYNC, offload off, local | 8 | 2 s | 15 s | 881 Steps/s | 8.6 ms |
| S2 SYNC, offload off, local | 8 | 90 s | 15 s | 320 Steps/s | 24.2 ms |
| S2 SYNC, offload on, c7gd.4xlarge | 8 | 180 s | 30 s | 214 Steps/s | 34.1 ms |

- The fall is the same with offload off, so history offload does not cause it.
- On the c7gd.4xlarge run, server CPU per committed command rose with run age:
  - 6.9 ms from 0 to 60 s;
  - 17.0 ms from 60 to 185 s;
  - 23.5 ms from 185 to 205 s.

  These values come from `process_cpu_seconds_total` and `dexzd_commit_records_total` in `aws-aged-profile/metrics-*.txt`. With eight clients, the server kept about nine of its 16 cores busy.
- `aws-aged-profile/perf-report*.txt` is a perf profile of that run, taken from 185 to 205 s. The flat profile is led by scan and decode work:
  - `BTreeMap<Vec<u8>, Vec<u8>>::insert`, 7.1%;
  - `memcmp`, 4.1%;
  - `malloc` and `free`, 6.3%;
  - varint decoding, 2.7%;
  - `StepExecutionRecord::decode`, 2.2%;
  - `from_utf8`, 1.6%;
  - `MemTableIterator::NextAndGetResult`, 1.2%.
- Most sampled stacks are truncated. Among the samples whose stack reaches `apply_in_scope`, about 97% pass through `refresh_search_document` and about 88% through `RunRows::step_executions`.

The code path that matches these measurements:

1. `RunCursor::finish` calls `refresh_search_document` for every command (`server-zd/crates/dexzd-apply/src/command_handlers/run_cursor.rs:286`).
2. It returns early only for `StepAttemptStarted`, `StepAttemptHeartbeat`, and `StepAttemptReturned` (`projections/search_document.rs:41-51`).
3. Otherwise it scans and decodes every Step execution row of the current attempt to find the active Step types (`projections/search_document.rs:60-62`, `state_access/run_rows.rs:182-187`).
4. Every Step of the chain commits `StepMethodCompleted`, so each Step pays one full scan.
5. Each Step creates a new Step execution row (`steps/step_creation.rs:40-51`). No code deletes the rows of completed Steps during an attempt.

Per-Step apply cost is therefore linear in the number of Steps the run has executed. The AWS rounds of this date did not report S2, S3, and S6 because of this. Their stopped search points are kept in `../2026-11-09-aws-s3-c7gd-large/00-long-lived-search-stopped/`.

## Rechecked with server-zd v0.1.23

server-zd v0.1.23 (`f27e3295f`) deletes the rows of ended Steps and keeps search counts incrementally. On AWS c7gd.large with S2 at C=128, run age no longer lowers throughput:

| Warmup | Steps per run at the end | Throughput | Step p50 / p99 (ms) | Server CPU per Step |
| ---: | ---: | ---: | --- | ---: |
| 2 s | ~380 | 1,031 Steps/s | 116 / 253 | 1.02 ms |
| 2 s, repeat | ~410 | 1,104 Steps/s | 109 / 235 | 1.03 ms |
| 90 s | ~880 | 833 Steps/s | 133 / 453 | 1.21 ms |
| 90 s, repeat | ~1,050 | 992 Steps/s | 119 / 302 | 1.24 ms |
| 240 s | ~2,320 | 1,042 Steps/s | 114 / 333 | 1.27 ms |

- Each row is one 45-second measurement with profiling off. The server is not CPU-bound at this point: it used 101–133% of its 200%.
- Throughput moves with the mean commit latency of each run (36–45 ms), not with run age. The 240-second point matches the 2-second points.
- Server CPU per Step is about 20–25% higher on older runs, and it levels off: 1.21–1.24 ms at about 1,000 Steps per run and 1.27 ms at about 2,300. On a CPU-bound point this could lower throughput by up to that much.

The rounds are `02-age-warmup-2s`, `03-age-warmup-90s`, `06-age-warmup-2s-repeat`, `07-age-warmup-90s-repeat`, and `08-age-warmup-240s` in [`../2026-11-09-aws-s3-c7gd-large-v0.1.23/`](../2026-11-09-aws-s3-c7gd-large-v0.1.23/README.md).

## PUTs per closed short run

`local-validation/start-complete-c4` runs `start-complete` at C=4 for a 2-second warmup and a 15-second measurement against `dex-server-zd dev`. The deltas of `metrics-before.txt` and `metrics-after.txt` are:

| Metric | Delta |
| --- | ---: |
| `dexzd_flow_closes_total{close_status="completed"}` | 18,514 |
| `dexzd_history_uploads_total{outcome="created"}` | 18,514 |
| `dexzd_object_store_requests_total{operation="put",object_kind="history"}` | 18,514 |
| `dexzd_object_store_requests_total{operation="put",object_kind="log"}` | 55,543 |
| `dexzd_history_upload_bytes_total` | 17,139,304 |

- Each closed run uploads exactly one history segment with one PUT, of about 926 bytes on average.
- Closing a run seals its open segment: `seal_history_segment_if_due` treats `has_closed_run` as due (`dexzd-apply/src/history_offload/segment_seal.rs`).
- Log PUTs depend on batching. At C=4 they were about 3.0 per run.

The other `local-validation` points check the long-lived scenarios. During the measurement, their runs stayed open and wrote no history segments. Each wrote one segment per run when the cleanup stopped it.

## Reproduce

The scripts run inside an Ubuntu 24.04 arm64 container, with the Linux `dex-server-zd`, `dexbench`, and the scripts mounted at `/v`:

```bash
docker run --rm -v "$PWD/v:/v" ubuntu:24.04 bash -c \
  'apt-get update && apt-get install -y curl jq && /v/age.sh sequential-steps 8 90 15'
ZD_CONFIG=/v/zd-off.yaml AGE_TAG=-off /v/age.sh sequential-steps 8 90 15   # inside the same container
/v/validate.sh start-complete 4 15                                         # inside the same container
```
