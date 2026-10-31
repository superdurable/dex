# server-zd on c7gd.large with S3

One server-zd node on a c7gd.large (2 vCPU Graviton3, local NVMe) in us-west-2b, with the log in S3 Standard. A c7g.8xlarge load host in the same zone ran `dexbench` with 4 gRPC connections. The server used the default configuration.

- server-zd: commit `3784ba838`, merged as `f2e9dd611` (#1131, the Worker dispatch fix for #1118).
- Each point: a newly started server, 10 s warmup, 45 s measurement.
- Search rounds ran with `DEXBENCH_PROFILE=1`, so each point has a perf profile and thread stacks. The profile slows a 2-vCPU server noticeably, so search numbers are lower than the final trials.
- `config.env` in each round records the exact settings. `benchmark/aws/reproduce.sh <round directory>` reruns a round.

## Results

Median and maximum of three runs, from `03-final-trials`, with profiling off.

| Scenario | Concurrency | Median | Max | Runs |
| --- | ---: | ---: | ---: | --- |
| S2 SYNC Steps | 512 | 1,436 Steps/s | 1,440 | 1,418 · 1,436 · 1,440 |
| S3 ASYNC Steps | 32 | 1,538 Steps/s | 1,562 | 1,508 · 1,538 · 1,562 |
| S1 Flows started and completed | 512 | 889 Flows/s | 892 | 880 · 889 · 892 |
| S4 plain RPC | 32 | 7,426 RPCs/s | 7,544 | 7,169 · 7,426 · 7,544 |
| S4 Attribute-locking RPC | 768 | 1,805 RPCs/s | 1,834 | 1,793 · 1,805 · 1,834 |
| S5 Attribute reads | 256 | 6,032 reads/s | 6,332 | 5,579 · 6,032 · 6,332 |
| S6 Channel wakes | 64 | 209 wakes/s; p50 93 ms | 226 | 199 · 209 · 226 |

Server CPU was 92–98% of both cores at every throughput point. The load host stayed below 11% of its CPU.

## Rounds

| Round | What it did | Outcome |
| --- | --- | --- |
| `01-search` | Every scenario at three concurrencies around the previous peaks | S2 and S3 are flat above C=512 and C=32. Plain RPC is best at C=32. Attribute-locking RPC at C=1,024 hit the default 1,000 in-flight RPC limit, so that point is invalid. |
| `02-search` | Edges: S2 at 768, plain RPC at 16, Attribute-locking RPC at 384 and 768, S5 at 256 and 1,024 | Attribute-locking RPC peaks at C=768. Plain RPC at 16 equals 32. S5 is best at 256. |
| `03-final-trials` | Three runs of each chosen point, profiling off | The table above |
