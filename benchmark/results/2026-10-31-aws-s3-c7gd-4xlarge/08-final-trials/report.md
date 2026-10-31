Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## sequential-steps

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 1536 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 27660 | 0 | 5976.26 steps/s | 2517.64 | 2894.66 | 233.29 | 321.44 | 429.00 | 544.92 | 757.9 | 47.4 | 5.3 | 65.2 | 741.2 | trial 1; 8 connections |
| 1536 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 29181 | 0 | 6133.23 steps/s | 2459.11 | 3123.17 | 226.95 | 301.13 | 405.67 | 561.77 | 766.3 | 47.9 | 5.4 | 65.2 | 626.2 | trial 2; 8 connections |
| 1536 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 27626 | 0 | 5887.57 steps/s | 2540.20 | 3202.28 | 237.05 | 332.62 | 443.20 | 529.07 | 738.4 | 46.2 | 5.1 | 62.8 | 694.1 | trial 3; 8 connections |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 34715 | 0 | 7342.74 steps/s | 2734.68 | 3330.96 | 248.10 | 329.27 | 470.41 | 605.40 | 917.8 | 57.4 | 6.4 | 60.8 | 708.5 | trial 1; 8 connections |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 36039 | 0 | 7665.50 steps/s | 2607.90 | 3165.88 | 240.75 | 312.39 | 386.99 | 604.16 | 981.2 | 61.3 | 6.9 | 65.9 | 800.1 | trial 2; 8 connections |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 35680 | 0 | 7544.44 steps/s | 2645.49 | 3178.16 | 242.84 | 311.69 | 407.35 | 512.21 | 967.7 | 60.5 | 6.6 | 64.3 | 759.2 | trial 3; 8 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 1536 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 5976.26 steps/s | 6133.23 steps/s | 3123.17 |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 7544.44 steps/s | 7665.50 steps/s | 3178.16 |

## sequential-steps-async

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 45445 | 0 | 10049.87 steps/s | 254.77 | 405.54 | 5.41 | 20.23 | 52.64 | 198.80 | 1272.3 | 79.5 | 8.7 | 92.7 | 933.3 | trial 1; 8 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 45211 | 0 | 10007.45 steps/s | 256.23 | 429.00 | 5.94 | 21.17 | 53.33 | 185.77 | 1249.4 | 78.1 | 8.6 | 91.6 | 917.7 | trial 2; 8 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 44717 | 0 | 9899.74 steps/s | 256.74 | 461.87 | 5.78 | 20.53 | 52.21 | 185.31 | 1226.0 | 76.6 | 8.4 | 88.6 | 951.9 | trial 3; 8 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 10007.45 steps/s | 10049.87 steps/s | 429.00 |

