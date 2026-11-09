# server-zd v0.1.25 on c7gd.large with S3: S4 and S5

One server-zd node ran on an on-demand c7gd.large (2 vCPU Graviton3, local NVMe) in us-west-2b, with the log in S3 Standard. An on-demand c7g.8xlarge load host in the same zone and cluster placement group ran `dexbench` with 4 gRPC connections.

- server-zd: the official v0.1.25 release asset `dex-server-zd_v0.1.25_linux_arm64.tar.gz`, built from `4875e0083`. Its SHA-256 matched the release `checksums.txt`. It ran on Ubuntu 24.04. v0.1.25 includes the `WorkerDispatch` registry change of #1445 (issue #1419).
- `dexbench`: built on the load host from `9b37482ed`. The JSON results record that commit. The `dexbench` revision field in `config.env` reads `unknown`.
- Server settings: the `serve` defaults.
- Each point: a newly started server, 10 s warmup, 45 s measurement.
- The search round ran with `DEXBENCH_PROFILE=1`, so each point has a perf profile and thread stacks. Final trials ran with `DEXBENCH_PROFILE=0`.
- `config.env` in each round records the exact settings. `benchmark/aws/reproduce.sh <round directory>` reruns a round.

S1, S2, S3, and S6 were measured on v0.1.23 in [`../2026-11-09-aws-s3-c7gd-large-v0.1.23/`](../2026-11-09-aws-s3-c7gd-large-v0.1.23/README.md).

## Results

Median and maximum of three runs, from `02-final-trials-s4-s5`, with profiling off. Latency comes from the run with the median throughput.

| Scenario | Concurrency | Median | Max | Runs | p50 / p90 / p99 (ms) |
| --- | ---: | ---: | ---: | --- | --- |
| S4 plain RPC, peak | 256 | 15,392 RPCs/s | 15,417 | 15,231 · 15,392 · 15,417 | 16.7 / 22.3 / 26.7 |
| S4 plain RPC, ~50% of peak | 4 | 7,501 RPCs/s | 7,878 | 7,236 · 7,501 · 7,878 | 0.5 / 0.6 / 0.9 |
| S4 Attribute-locking RPC, peak | 960 | 2,165 RPCs/s | 2,189 | 2,132 · 2,165 · 2,189 | 426 / 590 / 774 |
| S4 Attribute-locking RPC, ~50% of peak | 64 | 904 RPCs/s | 934 | 892 · 904 · 934 | 67.0 / 91.3 / 183.5 |
| S5 Attribute reads, peak | 256 | 5,941 reads/s | 5,968 | 5,924 · 5,941 · 5,968 | 42.8 / 59.5 / 73.8 |
| S5 Attribute reads, ~50% of peak | 3 | 3,091 reads/s | 3,095 | 3,054 · 3,091 · 3,095 | 0.9 / 1.2 / 1.5 |

- At the peaks, server CPU was 93–99% of both cores.
- The load host stayed below 13% of its CPU.
- No run failed.
- Plain RPC rose from 13,926 RPCs/s on `f9254ef20` to 15,392, about 11%. Attribute-locking RPC and S5 are within 1% of `f9254ef20`.
- No search-point thread snapshot showed a thread waiting on the `WorkerDispatch` registry mutex. The server has 7 threads on this host.
- Attribute-locking RPC stops at C=960, below the default limit of 1,000 in-flight RPCs.

## Rounds

| Round | What it did | Outcome |
| --- | --- | --- |
| `01-search-rpc-plain` | Plain RPC at C=4, 16, 32, 64, 128, 256, and 512, profiling on | Rises to 15.0k at C=256, 14.7k at C=512; C=4 gives 7.5k, about 50% of peak |
| `02-final-trials-s4-s5` | Three runs of plain RPC at C=256 and 4, Attribute-locking RPC at C=960 and 64, and S5 at C=256 and 3 | The results table above |
