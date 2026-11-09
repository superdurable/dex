Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## attribute-read-direct

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 5 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 250243 | 0 | 5560.88 reads/s | 0.80 | 1.24 | 1.96 | 2.87 | 202.4 | 12.7 | 14.0 | 0.3 | 72.4 | trial 1; 16 connections |
| 5 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 249141 | 0 | 5536.43 reads/s | 0.81 | 1.22 | 1.89 | 4.15 | 214.3 | 13.4 | 13.0 | 0.3 | 71.9 | trial 2; 16 connections |
| 5 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 239273 | 0 | 5317.14 reads/s | 0.84 | 1.30 | 2.03 | 10.21 | 197.3 | 12.3 | 13.8 | 0.3 | 70.6 | trial 3; 16 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 506888 | 0 | 11258.75 reads/s | 16.16 | 55.21 | 80.29 | 152.54 | 414.6 | 25.9 | 31.5 | 0.3 | 372.6 | trial 1; 16 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 506869 | 0 | 11258.41 reads/s | 14.01 | 51.15 | 69.90 | 135.95 | 431.7 | 27.0 | 31.5 | 0.3 | 367.3 | trial 2; 16 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 506874 | 0 | 11258.95 reads/s | 20.81 | 33.89 | 45.30 | 87.10 | 422.9 | 26.4 | 33.8 | 0.3 | 364.2 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 5 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 5536.43 reads/s | 5560.88 reads/s | 1.96 |
| 256 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 11258.75 reads/s | 11258.95 reads/s | 69.90 |

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 512 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 283253 | 0 | 6286.43 rpcs/s | 73.85 | 104.71 | 252.04 | 488.95 | 961.2 | 60.1 | 7.2 | 97.3 | 510.0 | trial 1; 16 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 299267 | 0 | 6642.12 rpcs/s | 73.62 | 101.18 | 151.73 | 213.18 | 1037.2 | 64.8 | 7.9 | 107.1 | 535.6 | trial 2; 16 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 283964 | 0 | 6285.04 rpcs/s | 75.85 | 106.28 | 194.88 | 277.19 | 972.7 | 60.8 | 7.3 | 98.7 | 479.5 | trial 3; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 527176 | 0 | 11658.49 rpcs/s | 350.98 | 422.50 | 511.36 | 741.36 | 1113.6 | 69.6 | 10.6 | 37.8 | 1042.4 | trial 1; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 536941 | 0 | 11875.98 rpcs/s | 346.31 | 419.53 | 514.32 | 746.97 | 1147.2 | 71.7 | 12.0 | 42.2 | 1076.5 | trial 2; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 527342 | 0 | 11660.43 rpcs/s | 346.86 | 431.47 | 558.05 | 706.28 | 1111.6 | 69.5 | 11.4 | 39.7 | 1065.0 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 512 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 6286.43 rpcs/s | 6642.12 rpcs/s | 194.88 |
| 4096 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 11660.43 rpcs/s | 11875.98 rpcs/s | 514.32 |

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 8 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 681284 | 0 | 15139.53 rpcs/s | 0.50 | 0.62 | 1.27 | 4.90 | 376.1 | 23.5 | 12.0 | 0.3 | 56.5 | trial 1; 16 connections |
| 8 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 687555 | 0 | 15278.84 rpcs/s | 0.49 | 0.61 | 1.23 | 3.63 | 381.3 | 23.8 | 11.9 | 0.3 | 56.3 | trial 2; 16 connections |
| 8 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 695936 | 0 | 15465.07 rpcs/s | 0.49 | 0.61 | 1.18 | 3.22 | 378.7 | 23.7 | 12.0 | 0.3 | 53.0 | trial 3; 16 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 1476138 | 0 | 32795.86 rpcs/s | 15.09 | 23.68 | 31.71 | 54.32 | 586.6 | 36.7 | 26.0 | 0.3 | 126.5 | trial 1; 16 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 1446533 | 0 | 32136.70 rpcs/s | 15.41 | 24.10 | 32.22 | 57.41 | 580.3 | 36.3 | 25.8 | 0.3 | 131.9 | trial 2; 16 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 1390536 | 0 | 30893.92 rpcs/s | 15.96 | 24.68 | 33.03 | 232.64 | 568.0 | 35.5 | 23.1 | 0.3 | 128.8 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 8 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 15278.84 rpcs/s | 15465.07 rpcs/s | 1.23 |
| 512 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 32136.70 rpcs/s | 32795.86 rpcs/s | 32.22 |

