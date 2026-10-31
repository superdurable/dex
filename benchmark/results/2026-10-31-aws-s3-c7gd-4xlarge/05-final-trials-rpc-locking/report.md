Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 8192 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=16384 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 600021 | 0 | 13180.47 rpcs/s | 625.79 | 754.51 | 855.94 | 1016.72 | 1369.8 | 85.6 | 14.6 | 52.5 | 1007.5 | trial 1; 16 connections |
| 8192 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=16384 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 594800 | 0 | 13063.11 rpcs/s | 625.38 | 776.02 | 874.51 | 1079.01 | 1364.6 | 85.3 | 14.5 | 54.6 | 1041.6 | trial 2; 16 connections |
| 8192 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=16384 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 596840 | 0 | 13124.09 rpcs/s | 627.51 | 750.91 | 848.80 | 1040.54 | 1366.7 | 85.4 | 14.5 | 56.7 | 1123.0 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 8192 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=16384 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 13124.09 rpcs/s | 13180.47 rpcs/s | 855.94 |

