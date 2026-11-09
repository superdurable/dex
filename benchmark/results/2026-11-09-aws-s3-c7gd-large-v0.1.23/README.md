# server-zd v0.1.23 on c7gd.large with S3

One server-zd node ran on an on-demand c7gd.large (2 vCPU Graviton3, local NVMe) in us-west-2b, with the log in S3 Standard. An on-demand c7g.8xlarge load host in the same zone and cluster placement group ran `dexbench` with 4 gRPC connections.

- server-zd: v0.1.23, commit `f27e3295f`. The load host ran this branch at `b7605a380`; its server-zd tree is the same. The `dexbench` revision field in `config.env` reads `unknown`, because the Go toolchain on the load host could not read the binary's build information.
- Server settings: the `serve` defaults. In v0.1.23 every run offloads its history unless it opts out, and the `serve` history window is 50,000 events.
- S2, S3, and S6 run on long-lived Flows: each client keeps one Flow open for the whole point.
- S1 starts each short Flow with `FlowConfig.offload_history` set to false, so short runs keep their history local and upload no history segment.
- Each point: a newly started server, 10 s warmup, 45 s measurement. S1 used a 5 s warmup and a 15 s measurement. The run-age rounds change only the warmup.
- Search rounds ran with `DEXBENCH_PROFILE=1`, so each point has a perf profile and thread stacks. All other rounds ran with `DEXBENCH_PROFILE=0`.
- `config.env` in each round records the exact settings. `benchmark/aws/reproduce.sh <round directory>` reruns a round.

S4 and S5 were measured earlier the same day at `f9254ef20`. See [`../2026-11-09-aws-s3-c7gd-large/`](../2026-11-09-aws-s3-c7gd-large/README.md).

## Results

Median and maximum of three runs, from `05-final-trials` and `09-final-trials-s3-c2`, with profiling off. Latency is per Step for S2 and S3 and per wake for S6. It comes from the run with the median throughput.

| Scenario | Concurrency | Median | Max | Runs | p50 / p90 / p99 (ms) |
| --- | ---: | ---: | ---: | --- | --- |
| S2 SYNC Steps, peak | 512 | 1,772 Steps/s | 1,813 | 1,729 · 1,772 · 1,813 | 282 / 344 / 484 |
| S2 SYNC Steps, ~50% of peak | 96 | 846 Steps/s | 850 | 729 · 846 · 850 | 105 / 147 / 285 |
| S3 ASYNC Steps, peak | 32 | 2,105 Steps/s | 2,110 | 2,101 · 2,105 · 2,110 | 14.5 / 23.9 / 32.6 |
| S3 ASYNC Steps, ~50% of peak | 2 | 1,224 Steps/s | 1,232 | 1,220 · 1,224 · 1,232 | 1.3 / 2.6 / 4.9 |
| S6 Channel wakes, peak | 256 | 460 wakes/s | 468 | 454 · 460 · 468 | 209 / 275 / 408 |
| S6 Channel wakes, ~50% of peak | 64 | 204 wakes/s | 221 | 172 · 204 · 221 | 112 / 177 / 304 |

- Server CPU was 96–98% of both cores at the peaks.
- The load host stayed below 4% of its CPU.
- No run failed, and cleanup stopped every Flow.
- S3 at C=4 ran about 20,000 Steps per Flow in each 55-second point, at a median of 1,507 Steps/s.
- S6 throughput includes the untimed setup of each wake, so use its latency.

### Run age

S2 at C=128, with only the warmup changed. Profiling was off.

| Round | Warmup | Steps per run at the end | Throughput | Step p50 / p99 (ms) | Server CPU per Step |
| --- | ---: | ---: | ---: | --- | ---: |
| `02-age-warmup-2s` | 2 s | ~380 | 1,031 Steps/s | 116 / 253 | 1.02 ms |
| `06-age-warmup-2s-repeat` | 2 s | ~410 | 1,104 Steps/s | 109 / 235 | 1.03 ms |
| `03-age-warmup-90s` | 90 s | ~880 | 833 Steps/s | 133 / 453 | 1.21 ms |
| `07-age-warmup-90s-repeat` | 90 s | ~1,050 | 992 Steps/s | 119 / 302 | 1.24 ms |
| `08-age-warmup-240s` | 240 s | ~2,320 | 1,042 Steps/s | 114 / 333 | 1.27 ms |

- Throughput no longer falls with run age. The 240-second point matches the 2-second points.
- The spread follows each run's mean commit latency, 36–45 ms.
- Server CPU per Step is about 20–25% higher on older runs and levels off.

### S1, rough

S1 is rough: a single 15-second run per point, on short runs with local history, from `04-s1-rough`.

| C | Throughput | p50 / p90 / p99 (ms) | History uploads/s | S3 PUTs/s | PUTs per completed Flow |
| ---: | ---: | --- | ---: | ---: | ---: |
| 64 | 435 Flows/s | 140 / 185 / 249 | 0 | 122 | 0.28 |
| 512 | 1,111 Flows/s | 457 / 560 / 650 | 0 | 88 | 0.08 |
| 1,024 | 980 Flows/s | 1,012 / 1,310 / 1,573 | 0 | 54 | 0.06 |

No history segment was uploaded. All PUTs are log batches plus the lease.

## Rounds

| Round | What it did | Outcome |
| --- | --- | --- |
| `01-search` | S2, S3, and S6 from low concurrency to above the previous peaks, profiling on | S2 is flat at 1.55k–1.61k from C=256. S3 peaks at C=32. S6 rises to 444 wakes/s at C=256, where the CPU is saturated. |
| `02-age-warmup-2s`, `03-age-warmup-90s` | S2 at C=128 with a 2 s and a 90 s warmup | The run age table |
| `04-s1-rough` | S1 at C=64, 512, and 1,024, 15 s each | The S1 table |
| `05-final-trials` | Three runs at each peak and at the first ~50% points | The results table. S3 at C=4 gave 72% of peak, so round 09 adds C=2. |
| `06`, `07`, `08` age rounds | A repeat of the 2 s and 90 s points, and a 240 s point | The run age table |
| `09-final-trials-s3-c2` | Three runs of S3 at C=2 | The S3 ~50% row |
