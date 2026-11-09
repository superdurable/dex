# server-zd on c7gd.4xlarge with S3 (partial)

One server-zd node ran on an on-demand c7gd.4xlarge (16 vCPU Graviton3, local NVMe) in us-west-2b, with the log in S3 Standard. An on-demand c7g.8xlarge load host in the same zone and cluster placement group ran `dexbench`, with 16 gRPC connections unless a round says otherwise. The server was in the placement group. The 2026-10-31 run used a Spot server outside the group.

- server-zd: `f9254ef20`. Its server-zd tree is the same as `e3457b6c6`. The load host ran `dexbench` from `d99724b0d`. The `dexbench` revision field in `config.env` reads `unknown`, because the Go toolchain on the load host could not read the binary's build information.
- Server settings beyond the defaults:
  - the four settings of 2026-10-31: `commit.maxUnappliedBytes: 268435456`, `limits.maxInFlightRpcsPerNode: 4096`, `localStorage.blockCacheBytes: 8589934592`, `localStorage.writeBufferBytes: 134217728`;
  - history offload with deletion of expired history objects and a 50,000-event window, which match the upcoming server-zd defaults: `history.offloadEnabled: true`, `history.deleteExpiredObjects: true`, `history.windowEvents: 50000`.

  Rounds 02 and 05 raise `limits.maxInFlightRpcsPerNode` to 16,384 for Attribute-locking RPC at C=6,144 and C=8,192.
- Connections:
  - `matching.maxConcurrentTasksCap` (default 256) limits the task slots of each pull Worker.
  - `dexbench` gives each connection C divided by the connection count, so a point above C=4,096 needs more than 16 connections.
  - Rounds 02 and 05 use 32 connections, and round 03 uses 48.
- Each point: a newly started server, 10 s warmup, 45 s measurement. The S1 points in round 03 used a 5 s warmup and a 15 s measurement.
- Search rounds ran with `DEXBENCH_PROFILE=1`, so each point has a perf profile and thread stacks. Final trials and S1 ran with `DEXBENCH_PROFILE=0`.
- `config.env` in each round records the exact settings. `benchmark/aws/reproduce.sh <round directory>` reruns a round.

**This set is partial.** It does not report S2, S3, or S6. Their Step throughput on long-lived runs falls as each run ages. See [`../2026-11-09-long-lived-runs/`](../2026-11-09-long-lived-runs/README.md).

## Results

Median and maximum of three runs, from `04-final-trials-s4-s5` and `05-final-trials-rpc-locking-c32`, with profiling off. The "~50% of peak" rows show latency at about half of the scenario's peak throughput.

| Scenario | Concurrency | Median | Max | Runs |
| --- | ---: | ---: | ---: | --- |
| S4 plain RPC, peak | 512 | 32,137 RPCs/s | 32,796 | 30,894 · 32,137 · 32,796 |
| S4 plain RPC, ~50% of peak | 8 | 15,279 RPCs/s | 15,465 | 15,140 · 15,279 · 15,465 |
| S4 Attribute-locking RPC, 16,384 RPC limit, 32 connections | 8,192 | 12,076 RPCs/s | 12,209 | 12,064 · 12,076 · 12,209 |
| S4 Attribute-locking RPC, 4,096 RPC limit, peak | 4,096 | 11,660 RPCs/s | 11,876 | 11,658 · 11,660 · 11,876 |
| S4 Attribute-locking RPC, ~50% of peak | 512 | 6,286 RPCs/s | 6,642 | 6,285 · 6,286 · 6,642 |
| S5 Attribute reads, peak | 256 | 11,259 reads/s | 11,259 | 11,258 · 11,259 · 11,259 |
| S5 Attribute reads, ~50% of peak | 5 | 5,536 reads/s | 5,561 | 5,317 · 5,536 · 5,561 |

- Server CPU:
  - plain RPC peak: 36% of the host;
  - Attribute-locking RPC peaks: 64–72%;
  - S5: about 27%, because the 15 Gbps network limits it.
- The load host mean CPU stayed below 35%.

### Plain RPC is lower than on 2026-10-31

Plain RPC peaked at 32,137 RPCs/s. On 2026-10-31 it peaked at 49,327. This time the server was not CPU-bound.

| Point | Throughput | Server CPU | Threads blocked on a mutex |
| --- | ---: | ---: | --- |
| 2026-10-31, C=128 (search) | 45,092 RPCs/s | 1,216% (76% of the host) | 3 and 5 of 27, on any mutex |
| 2026-11-09, C=256 (search) | 30,947 RPCs/s | 649% (41% of the host) | 9 and 11 of 27, all on `WorkerDispatch` |

- In every thread snapshot of the 2026-11-09 plain RPC search points at C=48, 256, and 512, 6 to 11 of the 27 server threads waited in `Mutex::lock_contended` inside `dexzd_worker_dispatch::worker_registry::WorkerDispatch`.
- That is the `registry: Mutex<Registry>` field (`server-zd/crates/dexzd-worker-dispatch/src/worker_registry.rs:74`).
- The callers are `lock_registry`, `next_waiter_sequence`, `register_partition_search`, `search_queue`, and `QueueLease::drop`, reached from `PullWorkerRpcInvoker::invoke`.
- See `01-search-s4-s5/p00[3-6]-rpc-plain-*.server/thread-stacks-*.txt`. The lock contention is the likely limit; this run did not test a change to confirm it.

### S1, rough

S1 is rough: a single 15-second run per point, on short runs, with 48 connections and profiling off, from `03-s1-rough`.

| C | Throughput | p50 / p90 / p99 (ms) | History uploads/s | S3 PUTs/s | PUTs per completed Flow |
| ---: | ---: | --- | ---: | ---: | ---: |
| 3,072 | 4,733 Flows/s | 618 / 822 / 1,054 | 547 | 609 | 0.13 |
| 6,144 | 4,925 Flows/s | 1,221 / 1,471 / 1,657 | 532 | 574 | 0.12 |
| 12,288 | 5,351 Flows/s | 2,250 / 2,636 / 2,931 | 503 | 533 | 0.10 |

- History uploads leveled off at about 500–550 per second, far below the rate of completed Flows. At C=12,288, 124,992 Flows completed and 11,157 segments were uploaded during the point.
- `dexzd_history_upload_backlog_segments` still read 0 afterwards.
- There were no object storage request errors and no failed uploads.

## Rounds

| Round | What it did | Outcome |
| --- | --- | --- |
| `01-search-s4-s5` | S4 and S5 from low concurrency to the previous peaks, 16 connections | Plain RPC is flat at 29.5k–32.1k from C=48 to 2,048. Attribute-locking RPC rises to 11.7k at C=4,096. S5 reaches the network limit, 11.2k, at C=16. |
| `02-search-rpc-locking-c32` | Attribute-locking RPC at C=6,144 and 8,192, 32 connections, 16,384 RPC limit | 11,410 and 11,949 RPCs/s: flat above C=4,096 |
| `03-s1-rough` | S1 at C=3,072, 6,144, and 12,288, 15 s each, 48 connections | The S1 table above |
| `04-final-trials-s4-s5` | Three runs at each peak and at about 50% of peak | The results table above |
| `05-final-trials-rpc-locking-c32` | Three runs of Attribute-locking RPC at C=8,192, 32 connections, 16,384 RPC limit | 12,076 RPCs/s median |
