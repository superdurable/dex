Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## sequential-steps

| Concurrency | Settings | Steps | Errors | Throughput | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 12288 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 434090 | 0 | 9646.39 steps/s | 1286.44 | 1423.25 | 1552.61 | 1791.28 | 1267.6 | 79.2 | 7.3 | 62.7 | 4481.8 | trial 1; 48 connections |
| 12288 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 322397 | 0 | 7164.37 steps/s | 1417.81 | 2806.12 | 3347.52 | 4003.92 | 963.1 | 60.2 | 5.7 | 39.1 | 4832.3 | trial 2; 48 connections |
| 12288 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 378142 | 0 | 8403.05 steps/s | 1273.89 | 2770.26 | 3403.87 | 4068.79 | 1105.2 | 69.1 | 6.7 | 50.5 | 4512.9 | trial 3; 48 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median Step p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 12288 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 8403.05 steps/s | 9646.39 steps/s | 3347.52 |

