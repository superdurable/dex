Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## sequential-steps

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 15872 | 0 | 3440.62 steps/s | 1497.79 | 1791.63 | 137.38 | 183.34 | 247.65 | 331.32 | 407.1 | 25.4 | 3.1 | 104.6 | 480.8 | 8 connections |
| 1024 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 22548 | 0 | 4845.12 steps/s | 2055.11 | 2557.92 | 186.05 | 264.56 | 365.69 | 543.82 | 592.9 | 37.1 | 4.2 | 77.7 | 584.0 | 8 connections |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 39754 | 0 | 8506.02 steps/s | 2374.27 | 2824.14 | 216.14 | 280.74 | 370.39 | 480.01 | 1050.5 | 65.7 | 7.4 | 75.4 | 698.2 | 8 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 12019 | 106 | 2266.19 steps/s | 16777.19 | 28203.04 | 1343.43 | 4582.89 | 5295.57 | 5637.66 | 1032.8 | 64.6 | 2.6 | 106.9 | 726.7 | 8 connections |

## sequential-steps-async

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 42677 | 0 | 9450.88 steps/s | 268.44 | 492.18 | 5.77 | 22.74 | 56.54 | 188.52 | 1177.0 | 73.6 | 8.0 | 83.8 | 888.1 | 8 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 39630 | 0 | 8726.90 steps/s | 571.62 | 1248.84 | 22.87 | 76.13 | 220.29 | 894.08 | 1234.9 | 77.2 | 7.8 | 72.9 | 1503.0 | 8 connections |
| 1024 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 22665 | 0 | 4918.07 steps/s | 2031.62 | 4573.48 | 132.85 | 529.45 | 1132.17 | 4085.75 | 1082.3 | 67.6 | 5.0 | 104.7 | 1334.0 | 8 connections |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 14154 | 0 | 2979.97 steps/s | 6507.62 | 13385.87 | 483.17 | 1738.83 | 3590.76 | 5283.05 | 970.8 | 60.7 | 3.3 | 99.4 | 1107.9 | 8 connections |

