# Long-lived runs and history offload checks

These measurements were taken on 2026-11-09, at server-zd `f9254ef20`. They check `dexbench` d99724b0d, which keeps one run open per client for `sequential-steps`, `sequential-steps-async`, and `channel-wake`. They found that Step throughput on a long-lived run falls as the run ages. They also count the object storage PUTs of short runs with history offload on.

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
