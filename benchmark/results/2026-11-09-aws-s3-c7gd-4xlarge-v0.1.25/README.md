# server-zd v0.1.25 on c7gd.4xlarge with S3: S4 and S5

One server-zd node ran on an on-demand c7gd.4xlarge (16 vCPU Graviton3, local NVMe) in us-west-2b, with the log in S3 Standard. An on-demand c7g.8xlarge load host in the same zone and cluster placement group ran `dexbench`.

- server-zd: the official v0.1.25 release asset `dex-server-zd_v0.1.25_linux_arm64.tar.gz`, built from `4875e0083`. Its SHA-256 matched the release `checksums.txt`. It ran on Ubuntu 24.04. v0.1.25 includes the `WorkerDispatch` registry change of #1445 (issue #1419).
- `dexbench`: built on the load host from `9b37482ed`. The JSON results record that commit. The `dexbench` revision field in `config.env` reads `unknown`.
- Server settings: the `serve` defaults, plus the four settings of 2026-10-31: `commit.maxUnappliedBytes: 268435456`, `limits.maxInFlightRpcsPerNode: 4096`, `localStorage.blockCacheBytes: 8589934592`, `localStorage.writeBufferBytes: 134217728`. Round 04 raises `limits.maxInFlightRpcsPerNode` to 16,384.
- Connections: 16 gRPC connections, and 32 in round 04. `matching.maxConcurrentTasksCap` (default 256) limits the task slots of each pull Worker, so C=8,192 needs more than 16 connections.
- Each point: a newly started server, 10 s warmup, 45 s measurement.
- Search rounds ran with `DEXBENCH_PROFILE=1`, so each point has a perf profile and thread stacks. Final trials ran with `DEXBENCH_PROFILE=0`.
- `config.env` in each round records the exact settings. `benchmark/aws/reproduce.sh <round directory>` reruns a round.

S1, S2, S3, and S6 were measured on v0.1.23 in [`../2026-11-09-aws-s3-c7gd-4xlarge-v0.1.23/`](../2026-11-09-aws-s3-c7gd-4xlarge-v0.1.23/README.md).

## Results

Median and maximum of three runs, from `03-final-trials-s4-s5` and `04-final-trials-rpc-locking-c32`, with profiling off. Latency comes from the run with the median throughput.

| Scenario | Concurrency | Median | Max | Runs | p50 / p90 / p99 (ms) |
| --- | ---: | ---: | ---: | --- | --- |
| S4 plain RPC, peak | 512 | 67,315 RPCs/s | 70,305 | 65,950 · 67,315 · 70,305 | 7.3 / 11.5 / 15.6 |
| S4 plain RPC | 128 | 65,051 RPCs/s | 65,498 | 64,450 · 65,051 · 65,498 | 1.8 / 3.0 / 4.5 |
| S4 plain RPC, ~50% of peak | 24 | 34,980 RPCs/s | 35,734 | 34,653 · 34,980 · 35,734 | 0.6 / 1.0 / 2.1 |
| S4 Attribute-locking RPC, peak, 16,384 RPC limit, 32 connections | 8,192 | 13,785 RPCs/s | 13,829 | 13,496 · 13,785 · 13,829 | 589 / 682 / 755 |
| S4 Attribute-locking RPC, 4,096 RPC limit | 4,096 | 12,982 RPCs/s | 13,379 | 12,651 · 12,982 · 13,379 | 313 / 363 / 553 |
| S4 Attribute-locking RPC, ~50% of peak | 512 | 6,093 RPCs/s | 6,215 | 5,942 · 6,093 · 6,215 | 76.9 / 111.4 / 277.1 |
| S5 Attribute reads, peak | 256 | 11,259 reads/s | 11,259 | 11,258 · 11,259 · 11,259 | 22.8 / 33.2 / 45.6 |
| S5 Attribute reads, ~50% of peak | 5 | 5,731 reads/s | 5,770 | 5,691 · 5,731 · 5,770 | 0.8 / 1.2 / 1.6 |

- Server CPU:
  - plain RPC: 66–69% of the host at C=512, and 73% at C=128;
  - Attribute-locking RPC peaks: 69% at C=8,192 and 79–83% at C=4,096;
  - S5: about 27%, because the 15 Gbps network limits it.
- The load host mean CPU stayed below 38%.
- No run failed.

### Plain RPC is back above the 2026-10-31 level

Issue #1419 asked whether plain RPC returns to 49.3k RPCs/s at C=128, the 2026-10-31 level. It does, and goes past it.

| Run | server-zd | C=128 | Peak | Server CPU at the peak |
| --- | --- | ---: | ---: | ---: |
| 2026-10-31 | `3784ba838` | 49,327 RPCs/s | 49,327 RPCs/s at C=128 | 80% of the host |
| 2026-11-09 | `f9254ef20` | 30,863 RPCs/s (search) | 32,137 RPCs/s at C=512 | 36% |
| 2026-11-09 | v0.1.25 | 65,051 RPCs/s | 67,315 RPCs/s at C=512 | 68% |

- Throughput is now flat at 62k–67k from C=128 to C=2,048.
- The `WorkerDispatch` registry mutex still shows up, but less often:
  - We counted threads whose frame above `Mutex::lock_contended` is a `WorkerDispatch` method, in both snapshots of each search point.
  - At C=128, 256, 512, and 2,048, 2 to 6 of the 27 server threads waited there, in `make_idle` or `pick_worker`. At C=48 it was 1.
  - The same count on `f9254ef20` gives 6 to 12 at C=128 to 2,048, and 3 to 6 at C=48.
- Server CPU is 66–73% of the host, not 36%. The mutex is no longer the main limit.
- See `01-search-rpc-plain/p00[3-6]-rpc-plain-*.server/thread-stacks-*.txt`.

### Attribute-locking RPC

Attribute-locking RPC also rose: 12,982 RPCs/s at C=4,096, against 11,660 on `f9254ef20`, and 13,785 at C=8,192, against 12,076. The C=8,192 point is now 6% above C=4,096, so it is the peak. No new search point was added.

## Rounds

| Round | What it did | Outcome |
| --- | --- | --- |
| `01-search-rpc-plain` | Plain RPC at C=8, 48, 128, 256, 512, and 2,048, profiling on | 62.8k at C=128, 64.8k at C=512, 62.0k at C=2,048 |
| `02-search-rpc-plain-low` | Plain RPC at C=16 and 24, to find the ~50% point | 25.1k and 33.4k; C=24 is the ~50% point |
| `03-final-trials-s4-s5` | Three runs of plain RPC at C=512, 128, and 24, Attribute-locking RPC at C=4,096 and 512, and S5 at C=256 and 5 | The results table above |
| `04-final-trials-rpc-locking-c32` | Three runs of Attribute-locking RPC at C=8,192, 32 connections, 16,384 RPC limit | 13,785 RPCs/s median |
