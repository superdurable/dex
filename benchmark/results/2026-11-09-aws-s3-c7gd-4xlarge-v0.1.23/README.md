# server-zd v0.1.23 on c7gd.4xlarge with S3

One server-zd node ran on an on-demand c7gd.4xlarge (16 vCPU Graviton3, local NVMe) in us-west-2b, with the log in S3 Standard. An on-demand c7g.8xlarge load host in the same zone and cluster placement group ran `dexbench`. The server was in the placement group.

- server-zd: v0.1.23, commit `f27e3295f`. The load host ran this branch at `b7605a380`; its server-zd tree is the same. The `dexbench` revision field in `config.env` reads `unknown`, because the Go toolchain on the load host could not read the binary's build information.
- Server settings: the `serve` defaults, plus the four settings of 2026-10-31: `commit.maxUnappliedBytes: 268435456`, `limits.maxInFlightRpcsPerNode: 4096`, `localStorage.blockCacheBytes: 8589934592`, `localStorage.writeBufferBytes: 134217728`. In v0.1.23 every run offloads its history unless it opts out, and the `serve` history window is 50,000 events.
- S2, S3, and S6 run on long-lived Flows. S1 starts each short Flow with `FlowConfig.offload_history` set to false.
- Connections:
  - `matching.maxConcurrentTasksCap` (default 256) limits the task slots of each pull Worker. `dexbench` gives each connection C divided by the connection count.
  - Rounds use 16 connections up to C=4,096, and 48 connections above that. S1 uses 48 connections.
  - Round 04 repeats S2 at C=4,096 with 32 connections.
- Each point: a newly started server, 10 s warmup, 45 s measurement. S1 used a 5 s warmup and a 15 s measurement.
- Search rounds ran with `DEXBENCH_PROFILE=1`, so each point has a perf profile and thread stacks. All other rounds ran with `DEXBENCH_PROFILE=0`.
- `config.env` in each round records the exact settings. `benchmark/aws/reproduce.sh <round directory>` reruns a round.

S4 and S5 were measured earlier the same day at `f9254ef20`. See [`../2026-11-09-aws-s3-c7gd-4xlarge/`](../2026-11-09-aws-s3-c7gd-4xlarge/README.md).

## Results

Median and maximum of three runs, from `05-final-trials` and `06-final-trials-c48`, with profiling off. Latency is per Step for S2 and S3 and per wake for S6. It comes from the run with the median throughput.

| Scenario | Concurrency | Median | Max | Runs | p50 / p90 / p99 (ms) |
| --- | ---: | ---: | ---: | --- | --- |
| S2 SYNC Steps, peak, 48 connections | 12,288 | 8,403 Steps/s | 9,646 | 7,164 · 8,403 · 9,646 | 1,274 / 2,770 / 3,404 |
| S2 SYNC Steps, ~50% of peak | 1,024 | 4,527 Steps/s | 4,610 | 4,055 · 4,527 · 4,610 | 218 / 295 / 382 |
| S3 ASYNC Steps, peak | 4,096 | 10,062 Steps/s | 10,275 | 9,988 · 10,062 · 10,275 | 405 / 466 / 537 |
| S3 ASYNC Steps, ~50% of peak | 8 | 4,766 Steps/s | 4,778 | 4,748 · 4,766 · 4,778 | 1.6 / 2.0 / 3.3 |
| S6 Channel wakes, peak | 4,096 | 2,278 wakes/s | 2,335 | 2,231 · 2,278 · 2,335 | 821 / 1,008 / 1,147 |
| S6 Channel wakes, ~50% of peak | 512 | 1,400 wakes/s | 1,416 | 1,253 · 1,400 · 1,416 | 139 / 197 / 308 |

- Server CPU at the peaks: 69–87% of the host.
- The load host mean CPU stayed below 30%.
- No run failed, and cleanup stopped every Flow.
- S6 at C=512 gave 61% of peak and C=256 gave 36% in the search, so C=512 is the closer ~50% point.

### S2 at C=12,288 varies between runs

The three runs gave 9,646, 7,164, and 8,403 Steps/s. Mean commit latency was 59, 98, and 74 ms in the same order, so throughput followed the S3 PUT round trip. The search run at the same point gave 9,522 Steps/s.

Every point above C=4,096 also took a snapshot of 7–9 s during the measurement. A snapshot is requested after 256 MiB of new log data. The snapshots overlapped all three runs at different offsets, so they do not explain the spread.

### S2 tail at C=4,096 with 16 connections

In `01-search`, S2 at C=4,096 with 16 connections had a p99 of 5,475 ms and a maximum of 5,707 ms, at 7,638 Steps/s. The same point with 32 connections, in `04-diag-s2-c4096-32-connections`, had a p99 of 725 ms at 8,936 Steps/s.

- With 16 connections, each pull Worker has exactly 256 task slots, the `matching.maxConcurrentTasksCap` default.
- The server metrics show 5,033 Steps (0.9%) with a schedule-to-start latency between 2.5 and 10 s, and no server warnings.
- One hypothesis is that a Step became ready before its Worker released the slot of the previous attempt, and then waited for the 5 s `matching.stickyWait`. This run did not test that further. S3 and S6 at the same concurrency showed no such tail.

The headline S2 rows therefore use C=1,024 with 16 connections and C=12,288 with 48 connections.

### S1, rough

S1 is rough: a single 15-second run per point, on short runs with local history, with 48 connections, from `03-s1-rough`.

| C | Throughput | p50 / p90 / p99 (ms) | History uploads/s | S3 PUTs/s | PUTs per completed Flow |
| ---: | ---: | --- | ---: | ---: | ---: |
| 3,072 | 4,902 Flows/s | 611 / 709 / 801 | 0 | 55 | 0.011 |
| 6,144 | 5,309 Flows/s | 1,146 / 1,336 / 1,533 | 0 | 44 | 0.008 |
| 12,288 | 5,626 Flows/s | 2,164 / 2,493 / 2,747 | 0 | 31 | 0.005 |

No history segment was uploaded. All PUTs are log batches plus the lease. There were no object storage request errors.

## Rounds

| Round | What it did | Outcome |
| --- | --- | --- |
| `01-search` | S2, S3, and S6 up to C=4,096, 16 connections, profiling on | S2 rises to 7.6k at C=4,096, with the tail described above. S3 is 8.2k–9.5k from C=64. S6 rises to 2.1k wakes/s at C=4,096. |
| `02-search-c48` | S2 at C=8,192 and 12,288, 48 connections | 8,531 and 9,522 Steps/s |
| `03-s1-rough` | S1 at C=3,072, 6,144, and 12,288, 15 s each | The S1 table |
| `04-diag-s2-c4096-32-connections` | S2 at C=4,096 with 32 connections | 8,936 Steps/s, p99 725 ms |
| `05-final-trials` | Three runs at the S3 and S6 peaks and at the ~50% points | The results table |
| `06-final-trials-c48` | Three runs of S2 at C=12,288, 48 connections | The S2 peak row |
