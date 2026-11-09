Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## start-complete

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 3072 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 72997 | 0 | 4732.69 flows/s | 618.41 | 821.94 | 1054.36 | 1466.27 | 1172.3 | 73.3 | 7.9 | 608.5 | 1699.2 | 48 connections |
| 6144 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 77991 | 0 | 4924.89 flows/s | 1220.51 | 1470.64 | 1657.12 | 1947.99 | 1236.4 | 77.3 | 8.8 | 573.9 | 2188.8 | 48 connections |
| 12288 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 87947 | 0 | 5351.05 flows/s | 2250.29 | 2635.81 | 2931.39 | 3532.93 | 1297.2 | 81.1 | 9.4 | 533.3 | 3633.7 | 48 connections |

