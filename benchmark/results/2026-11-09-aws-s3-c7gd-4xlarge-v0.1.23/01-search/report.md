Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## channel-wake

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 10979 | 0 | 242.64 wakes/s | 92.13 | 143.84 | 308.77 | 493.11 | 170.8 | 10.7 | 2.0 | 112.4 | 216.8 | 16 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 36717 | 0 | 810.32 wakes/s | 113.36 | 174.80 | 311.19 | 606.25 | 532.2 | 33.3 | 8.1 | 102.1 | 385.2 | 16 connections |
| 1024 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 78833 | 0 | 1732.47 wakes/s | 229.38 | 295.71 | 401.96 | 637.39 | 1278.9 | 79.9 | 24.7 | 90.1 | 681.8 | 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 96869 | 0 | 2089.13 wakes/s | 870.67 | 1179.80 | 1356.09 | 1531.06 | 1357.2 | 84.8 | 26.8 | 79.2 | 2045.9 | 16 connections |

## sequential-steps

| Concurrency | Settings | Steps | Errors | Throughput | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 1024 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 205863 | 0 | 4574.71 steps/s | 217.12 | 291.69 | 361.43 | 566.92 | 620.1 | 38.8 | 3.5 | 68.9 | 648.1 | 16 connections |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 325996 | 0 | 7244.23 steps/s | 269.67 | 358.19 | 625.59 | 856.67 | 984.7 | 61.5 | 5.8 | 61.2 | 971.8 | 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 343700 | 0 | 7637.77 steps/s | 430.17 | 545.71 | 5475.22 | 5707.34 | 1031.8 | 64.5 | 6.4 | 55.7 | 1886.8 | 16 connections |

## sequential-steps-async

| Concurrency | Settings | Steps | Errors | Throughput | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 390511 | 0 | 8677.82 steps/s | 6.88 | 10.16 | 17.07 | 211.92 | 1269.1 | 79.3 | 4.3 | 45.5 | 1368.7 | 16 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 369911 | 0 | 8220.23 steps/s | 30.05 | 38.98 | 48.46 | 76.86 | 1286.8 | 80.4 | 4.9 | 32.9 | 1969.5 | 16 connections |
| 1024 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 414042 | 0 | 9200.91 steps/s | 106.12 | 135.01 | 172.83 | 220.01 | 1258.6 | 78.7 | 6.6 | 29.8 | 2648.8 | 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 427386 | 0 | 9497.45 steps/s | 421.95 | 511.24 | 555.28 | 662.83 | 1262.6 | 78.9 | 7.5 | 25.6 | 3391.5 | 16 connections |

