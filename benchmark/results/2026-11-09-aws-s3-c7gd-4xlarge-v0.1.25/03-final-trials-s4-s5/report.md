Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## attribute-read-direct

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 5 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 259653 | 0 | 5769.96 reads/s | 0.79 | 1.17 | 1.55 | 2.56 | 211.6 | 13.2 | 12.1 | 0.3 | 65.9 | trial 1; 16 connections |
| 5 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 256108 | 0 | 5691.19 reads/s | 0.80 | 1.19 | 1.58 | 2.27 | 211.4 | 13.2 | 12.4 | 0.3 | 68.4 | trial 2; 16 connections |
| 5 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 257912 | 0 | 5731.27 reads/s | 0.80 | 1.17 | 1.57 | 2.29 | 212.8 | 13.3 | 12.3 | 0.3 | 67.8 | trial 3; 16 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 506854 | 0 | 11258.14 reads/s | 22.51 | 33.09 | 40.15 | 61.34 | 461.2 | 28.8 | 27.0 | 0.3 | 376.8 | trial 1; 16 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 506883 | 0 | 11258.73 reads/s | 22.81 | 33.16 | 45.55 | 59.45 | 433.2 | 27.1 | 25.0 | 0.3 | 373.1 | trial 2; 16 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 506914 | 0 | 11259.33 reads/s | 18.12 | 42.15 | 62.74 | 87.25 | 426.5 | 26.7 | 26.6 | 0.3 | 357.7 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 5 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 5731.27 reads/s | 5769.96 reads/s | 1.57 |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 11258.73 reads/s | 11259.33 reads/s | 45.55 |

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 267705 | 0 | 5941.53 rpcs/s | 77.16 | 118.91 | 247.74 | 369.21 | 868.6 | 54.3 | 5.3 | 90.3 | 493.4 | trial 1; 16 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 280011 | 0 | 6215.30 rpcs/s | 76.22 | 112.28 | 183.65 | 291.94 | 906.5 | 56.7 | 5.5 | 96.2 | 469.9 | trial 2; 16 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 274531 | 0 | 6093.46 rpcs/s | 76.92 | 111.42 | 277.13 | 360.16 | 883.1 | 55.2 | 5.6 | 93.7 | 501.2 | trial 3; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 571947 | 0 | 12650.94 rpcs/s | 325.13 | 378.66 | 424.81 | 629.34 | 1262.0 | 78.9 | 10.5 | 59.3 | 1123.4 | trial 1; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 586636 | 0 | 12982.11 rpcs/s | 312.54 | 362.56 | 553.39 | 766.33 | 1277.9 | 79.9 | 10.7 | 62.3 | 1100.2 | trial 2; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 604739 | 0 | 13378.94 rpcs/s | 304.08 | 367.31 | 440.34 | 596.03 | 1330.0 | 83.1 | 11.2 | 65.7 | 1052.4 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 6093.46 rpcs/s | 6215.30 rpcs/s | 247.74 |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 12982.11 rpcs/s | 13378.94 rpcs/s | 440.34 |

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 24 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 1608064 | 0 | 35734.28 rpcs/s | 0.58 | 0.93 | 2.11 | 7.02 | 803.5 | 50.2 | 29.3 | 0.3 | 56.2 | trial 1; 16 connections |
| 24 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 1559394 | 0 | 34652.79 rpcs/s | 0.59 | 1.04 | 2.18 | 8.23 | 800.3 | 50.0 | 29.0 | 0.3 | 56.8 | trial 2; 16 connections |
| 24 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 1574105 | 0 | 34979.69 rpcs/s | 0.59 | 0.98 | 2.14 | 6.66 | 810.0 | 50.6 | 29.0 | 0.3 | 59.2 | trial 3; 16 connections |
| 128 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 2927381 | 0 | 65050.77 rpcs/s | 1.80 | 2.95 | 4.51 | 13.32 | 1164.8 | 72.8 | 37.1 | 0.3 | 77.1 | trial 1; 16 connections |
| 128 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 2947485 | 0 | 65497.56 rpcs/s | 1.80 | 2.89 | 4.42 | 14.14 | 1169.5 | 73.1 | 36.8 | 0.3 | 72.1 | trial 2; 16 connections |
| 128 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 2900357 | 0 | 64450.31 rpcs/s | 1.84 | 2.92 | 4.40 | 14.26 | 1163.9 | 72.7 | 35.8 | 0.3 | 79.9 | trial 3; 16 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 2968259 | 0 | 65950.16 rpcs/s | 7.44 | 11.86 | 16.00 | 30.03 | 1061.2 | 66.3 | 33.8 | 0.3 | 128.6 | trial 1; 16 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3029480 | 0 | 67315.44 rpcs/s | 7.30 | 11.52 | 15.60 | 29.44 | 1086.6 | 67.9 | 32.5 | 0.3 | 125.9 | trial 2; 16 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3163997 | 0 | 70304.66 rpcs/s | 6.98 | 11.12 | 15.00 | 25.08 | 1097.2 | 68.6 | 34.6 | 0.3 | 126.9 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 24 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 34979.69 rpcs/s | 35734.28 rpcs/s | 2.14 |
| 128 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 65050.77 rpcs/s | 65497.56 rpcs/s | 4.42 |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 67315.44 rpcs/s | 70304.66 rpcs/s | 15.60 |

