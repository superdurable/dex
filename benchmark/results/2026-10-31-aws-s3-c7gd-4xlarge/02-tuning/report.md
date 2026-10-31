Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## sequential-steps

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 2048 | APPLIER_LANES=32 BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 29514 | 0 | 6257.70 steps/s | 3010.63 | 6212.36 | 270.86 | 446.82 | 998.05 | 4053.58 | 896.4 | 56.0 | 5.9 | 61.6 | 844.6 | 8 connections |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_IN_FLIGHT=16 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 31531 | 0 | 6709.53 steps/s | 2669.24 | 4988.12 | 251.34 | 445.70 | 636.68 | 860.35 | 824.5 | 51.5 | 5.9 | 206.4 | 747.4 | 8 connections |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_IN_FLIGHT=8 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 34814 | 0 | 7436.38 steps/s | 2684.05 | 3326.75 | 247.56 | 329.75 | 465.34 | 548.78 | 926.0 | 57.9 | 6.6 | 118.1 | 757.8 | 8 connections |
| 3072 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 12791 | 0 | 2559.59 steps/s | 11303.14 | 21040.33 | 883.11 | 2859.94 | 5127.60 | 5396.77 | 1002.3 | 62.6 | 3.0 | 107.5 | 676.6 | 8 connections |

## sequential-steps-async

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 128 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 43797 | 0 | 9710.82 steps/s | 127.90 | 220.97 | 1.78 | 4.74 | 11.53 | 52.93 | 1112.3 | 69.5 | 8.6 | 111.6 | 695.6 | 8 connections |
| 256 | APPLIER_LANES=32 BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 44347 | 0 | 9815.92 steps/s | 260.46 | 440.47 | 7.09 | 21.61 | 51.33 | 175.14 | 1191.4 | 74.5 | 8.1 | 82.4 | 980.9 | 8 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_IN_FLIGHT=16 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 42764 | 0 | 9471.36 steps/s | 263.24 | 498.32 | 4.09 | 17.80 | 48.18 | 183.51 | 1246.2 | 77.9 | 8.2 | 299.7 | 949.4 | 8 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_IN_FLIGHT=8 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 44890 | 0 | 9926.79 steps/s | 256.38 | 446.74 | 5.07 | 19.52 | 51.05 | 184.13 | 1240.4 | 77.5 | 9.1 | 174.1 | 949.6 | 8 connections |

