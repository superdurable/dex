Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 8192 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=16384 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 554666 | 0 | 12208.53 rpcs/s | 661.48 | 758.18 | 917.37 | 1044.02 | 1042.3 | 65.1 | 11.8 | 13.9 | 1580.0 | trial 1; 32 connections |
| 8192 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=16384 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 548533 | 0 | 12063.93 rpcs/s | 670.88 | 770.30 | 951.48 | 1372.14 | 1025.0 | 64.1 | 11.5 | 14.5 | 1631.4 | trial 2; 32 connections |
| 8192 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=16384 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 549298 | 0 | 12076.23 rpcs/s | 667.12 | 769.57 | 935.40 | 1086.64 | 1027.3 | 64.2 | 11.8 | 13.8 | 1587.0 | trial 3; 32 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 8192 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=16384 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 12076.23 rpcs/s | 12208.53 rpcs/s | 935.40 |

