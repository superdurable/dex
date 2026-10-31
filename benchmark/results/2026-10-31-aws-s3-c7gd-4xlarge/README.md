# server-zd on c7gd.4xlarge with S3

One server-zd node on a c7gd.4xlarge (16 vCPU Graviton3, local NVMe) in us-west-2b, with the log in S3 Standard. A c7g.8xlarge load host in the same zone ran `dexbench` with 16 gRPC connections. No cluster placement group had Spot capacity for the server, so it ran outside the group, in the same zone.

- server-zd: commit `3784ba838`, merged as `f2e9dd611` (#1131, the Worker dispatch fix for #1118).
- Server settings beyond the defaults: `commit.maxUnappliedBytes: 268435456`, `limits.maxInFlightRpcsPerNode: 4096`, `localStorage.blockCacheBytes: 8589934592`, `localStorage.writeBufferBytes: 134217728`. Rounds 03 and 05 raise `limits.maxInFlightRpcsPerNode` to 16,384.
- Each point: a newly started server, 10 s warmup, 45 s measurement.
- Search rounds ran with `DEXBENCH_PROFILE=1`, so each point has a perf profile and thread stacks.
- `config.env` in each round records the exact settings. `benchmark/aws/reproduce.sh <round directory>` reruns a round.

## Results

Median and maximum of three runs, from `04-final-trials` and `05-final-trials-rpc-locking`, with profiling off.

| Scenario | Concurrency | Median | Max | Runs |
| --- | ---: | ---: | ---: | --- |
| S2 SYNC Steps | 8,192 | 10,765 Steps/s | 10,842 | 10,514 · 10,765 · 10,842 |
| S2 SYNC Steps | 12,288 | 10,752 Steps/s | 10,861 | 10,574 · 10,752 · 10,861 |
| S3 ASYNC Steps | 4,096 | 10,647 Steps/s | 10,655 | 10,528 · 10,647 · 10,655 |
| S3 ASYNC Steps | 256 | 10,210 Steps/s; Step p50 7.1 ms | 10,271 | 10,171 · 10,210 · 10,271 |
| S1 Flows started and completed | 12,288 | 6,176 Flows/s | 6,212 | 6,073 · 6,176 · 6,212 |
| S4 plain RPC | 128 | 49,327 RPCs/s | 49,592 | 48,346 · 49,327 · 49,592 |
| S4 Attribute-locking RPC, 16,384 RPC limit | 8,192 | 13,124 RPCs/s | 13,180 | 13,063 · 13,124 · 13,180 |
| S4 Attribute-locking RPC, 4,096 RPC limit | 4,096 | 12,174 RPCs/s | 12,376 | 12,116 · 12,174 · 12,376 |
| S5 Attribute reads | 256 | 11,258 reads/s | 11,259 | 11,257 · 11,258 · 11,259 |
| S6 Channel wakes | 64 | 227 wakes/s; p50 84 ms | 239 | 217 · 227 · 239 |

Server CPU was 78–86% of the host at the S1–S4 points. S5 is limited by the 15 Gbps network, at about 28% CPU. The load host stayed below 47% of its CPU.

## Rounds

| Round | What it did | Outcome |
| --- | --- | --- |
| `01-search` | Every scenario around and above the previous peaks | S2 rises to 9,829 Steps/s at C=8,192 with no collapse. S3 is flat at 9.4k–9.8k from 256 to 4,096. The Spot server was reclaimed during plain RPC at C=2,048, so that point and the four after it failed. |
| `02-search` | On a new server: S2 at 12,288 and 16,384, the failed points, and higher S1 and Attribute-locking RPC concurrency | S2 stays at 9.8k–9.9k up to C=16,384. Plain RPC is flat at 41k–45k from 128 to 2,048. Attribute-locking RPC still rises at C=4,096, the RPC limit. |
| `03-rpc-locking-limit` | Attribute-locking RPC at 6,144 and 8,192 with a 16,384 RPC limit | 12,854 and 13,312 RPCs/s |
| `04-final-trials` | Three runs of each chosen point, profiling off | The table above |
| `05-final-trials-rpc-locking` | Three runs of Attribute-locking RPC at 8,192 with the 16,384 limit | 13,124 RPCs/s median |
