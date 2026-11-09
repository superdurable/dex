# server-zd on c7gd.large with S3 (partial)

One server-zd node ran on an on-demand c7gd.large (2 vCPU Graviton3, local NVMe) in us-west-2b, with the log in S3 Standard. An on-demand c7g.8xlarge load host in the same zone and cluster placement group ran `dexbench` with 4 gRPC connections. The 2026-10-31 run used a Spot server.

- server-zd: `f9254ef20`. Its server-zd tree is the same as `e3457b6c6`, the last server-zd change before it. The load host ran `dexbench` from `d99724b0d`. The `dexbench` revision field in `config.env` reads `unknown`, because the Go toolchain on the load host could not read the binary's build information.
- Server settings: the `serve` defaults, plus history offload with deletion of expired history objects and a 50,000-event history window. These match the upcoming server-zd defaults.

  ```yaml
  history:
    offloadEnabled: true
    deleteExpiredObjects: true
    windowEvents: 50000
  ```

- Each point: a newly started server, 10 s warmup, 45 s measurement. The S1 points are the exception: 5 s warmup and 15 s measurement.
- Search rounds ran with `DEXBENCH_PROFILE=1`, so each point has a perf profile and thread stacks. Final trials and S1 ran with `DEXBENCH_PROFILE=0`.
- `config.env` in each round records the exact settings. `benchmark/aws/reproduce.sh <round directory>` reruns a round.

**This set is partial.** S2, S3, and S6 now run on long-lived runs, and their Step throughput falls as each run ages. Numbers from them would depend on the point length, so this set does not report them. See [`../2026-11-09-long-lived-runs/`](../2026-11-09-long-lived-runs/README.md).

## Results

Median and maximum of three runs, from `03-final-trials-s4-s5`, with profiling off. The "~50% of peak" rows show latency at about half of the scenario's peak throughput.

| Scenario | Concurrency | Median | Max | Runs |
| --- | ---: | ---: | ---: | --- |
| S4 plain RPC, peak | 256 | 13,926 RPCs/s | 14,093 | 13,921 · 13,926 · 14,093 |
| S4 plain RPC, ~50% of peak | 4 | 7,492 RPCs/s | 7,577 | 7,488 · 7,492 · 7,577 |
| S4 Attribute-locking RPC, peak | 960 | 2,149 RPCs/s | 2,151 | 2,078 · 2,149 · 2,151 |
| S4 Attribute-locking RPC, ~50% of peak | 64 | 950 RPCs/s | 960 | 917 · 950 · 960 |
| S5 Attribute reads, peak | 256 | 5,891 reads/s | 5,905 | 5,777 · 5,891 · 5,905 |
| S5 Attribute reads, ~50% of peak | 3 | 3,058 reads/s | 3,109 | 3,048 · 3,058 · 3,109 |

At the peaks, server CPU was 94–98% of both cores. The load host stayed below 15% of its CPU. Attribute-locking RPC stops at C=960, below the default limit of 1,000 in-flight RPCs.

### S1, rough

S1 is rough: a single 15-second run per point, on short runs. It ran with profiling off, from `02-s1-rough`.

| C | Throughput | p50 / p90 / p99 (ms) | History uploads/s | S3 PUTs/s | PUTs per completed Flow |
| ---: | ---: | --- | ---: | ---: | ---: |
| 64 | 445 Flows/s | 139 / 176 / 205 | 445 | 573 | 1.29 |
| 512 | 695 Flows/s | 725 / 874 / 1,001 | 394 | 474 | 0.68 |
| 1,024 | 728 Flows/s | 1,372 / 1,634 / 1,871 | 385 | 443 | 0.61 |

- Each closed run seals one history segment for upload. At C=64, uploads matched closes one for one: 8,975 of each.
- At C=512 and above, uploads leveled off at about 390 per second and fell behind the closes. At C=1,024 there were 16,326 closes and 8,347 uploads over the point. The default `history.uploadConcurrency` of 16, times one S3 PUT round trip, gives about that rate.
- `dexzd_history_upload_backlog_segments` still read 0 after these points, although uploads trailed closes.
- There were no object storage request errors and no failed uploads.

## Rounds

| Round | What it did | Outcome |
| --- | --- | --- |
| `00-long-lived-search-stopped` | S2 and S3 search points on long-lived runs, profiling on | Stopped after 10 points. Throughput rose with C only because runs were younger at the end of each point. Kept as evidence of the run-age effect. |
| `01-search-s4-s5` | S4 and S5 from low concurrency to the previous peaks | Plain RPC is flat from C=64 to 512 and peaks at C=256. Attribute-locking RPC still rises at C=960. S5 peaks at C=256. |
| `02-s1-rough` | S1 at C=64, 512, and 1,024, 15 s each, profiling off | The table above |
| `03-final-trials-s4-s5` | Three runs at each peak and at about 50% of peak, profiling off | The table above |
