Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## attribute-read-direct

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 506810 | 0 | 11257.13 reads/s | 21.44 | 36.44 | 53.20 | 72.55 | 444.9 | 27.8 | 24.4 | 0.3 | 279.5 | trial 1; 16 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 506880 | 0 | 11258.47 reads/s | 23.32 | 36.55 | 44.73 | 69.42 | 452.6 | 28.3 | 25.8 | 0.3 | 279.5 | trial 2; 16 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 506886 | 0 | 11258.74 reads/s | 12.22 | 48.29 | 80.04 | 107.95 | 433.5 | 27.1 | 24.6 | 0.3 | 281.8 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 11258.47 reads/s | 11258.74 reads/s | 53.20 |

## channel-wake

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 10829 | 0 | 239.32 wakes/s | 80.43 | 113.91 | 167.18 | 352.19 | 123.0 | 7.7 | 2.0 | 131.6 | 204.4 | trial 1; 16 connections |
| 64 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 10290 | 0 | 226.76 wakes/s | 83.81 | 124.56 | 180.76 | 286.43 | 115.8 | 7.2 | 1.8 | 127.1 | 209.7 | trial 2; 16 connections |
| 64 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 9849 | 0 | 216.98 wakes/s | 85.11 | 134.23 | 256.34 | 360.45 | 114.2 | 7.1 | 1.8 | 120.8 | 201.5 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 64 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 226.76 wakes/s | 239.32 wakes/s | 180.76 |

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 559334 | 0 | 12375.72 rpcs/s | 329.54 | 396.36 | 454.93 | 567.45 | 1333.3 | 83.3 | 13.4 | 64.7 | 767.2 | trial 1; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 550367 | 0 | 12173.59 rpcs/s | 336.30 | 390.67 | 440.65 | 570.43 | 1337.9 | 83.6 | 13.2 | 65.1 | 783.8 | trial 2; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 547602 | 0 | 12116.20 rpcs/s | 339.52 | 395.62 | 444.10 | 538.96 | 1336.7 | 83.5 | 13.2 | 65.6 | 774.7 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 12173.59 rpcs/s | 12375.72 rpcs/s | 444.10 |

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 128 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 2175758 | 0 | 48346.16 rpcs/s | 2.63 | 3.60 | 4.68 | 8.60 | 1284.9 | 80.3 | 45.0 | 0.3 | 65.2 | trial 1; 16 connections |
| 128 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 2231852 | 0 | 49592.41 rpcs/s | 2.55 | 3.48 | 4.52 | 8.57 | 1288.7 | 80.5 | 45.9 | 0.3 | 64.4 | trial 2; 16 connections |
| 128 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 2219864 | 0 | 49327.31 rpcs/s | 2.57 | 3.49 | 4.53 | 8.84 | 1285.8 | 80.4 | 46.2 | 0.3 | 67.1 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 128 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 49327.31 rpcs/s | 49592.41 rpcs/s | 4.53 |

## sequential-steps

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 8192 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 53708 | 0 | 10841.53 steps/s | 7299.39 | 9675.79 | 678.54 | 828.01 | 1024.34 | 1321.22 | 1309.0 | 81.8 | 11.1 | 63.2 | 1994.6 | trial 1; 16 connections |
| 8192 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 53716 | 0 | 10764.65 steps/s | 7283.37 | 9412.05 | 683.16 | 836.91 | 982.84 | 1267.30 | 1306.0 | 81.6 | 11.1 | 58.5 | 2032.6 | trial 2; 16 connections |
| 8192 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 51879 | 0 | 10514.34 steps/s | 7498.56 | 9730.90 | 705.92 | 840.51 | 1012.79 | 1293.59 | 1279.1 | 79.9 | 10.6 | 59.6 | 1662.6 | trial 3; 16 connections |
| 12288 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 54839 | 0 | 10752.31 steps/s | 11020.92 | 13646.39 | 1028.08 | 1220.48 | 1499.47 | 2161.21 | 1302.8 | 81.4 | 10.2 | 68.4 | 2518.0 | trial 1; 16 connections |
| 12288 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 53271 | 0 | 10574.20 steps/s | 11224.78 | 13581.11 | 1046.32 | 1223.15 | 1484.60 | 1933.10 | 1300.8 | 81.3 | 10.0 | 67.5 | 2335.1 | trial 2; 16 connections |
| 12288 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 56351 | 0 | 10860.93 steps/s | 11022.43 | 12865.30 | 1009.34 | 1182.68 | 1435.15 | 2093.70 | 1301.6 | 81.3 | 10.3 | 66.1 | 2142.4 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median Flow p99 ms | Median Step p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: | ---: |
| 8192 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 10764.65 steps/s | 10841.53 steps/s | 9675.79 | 1012.79 |
| 12288 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 10752.31 steps/s | 10860.93 steps/s | 13581.11 | 1484.60 |

## sequential-steps-async

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 46145 | 0 | 10209.67 steps/s | 249.07 | 465.91 | 7.13 | 15.15 | 21.30 | 39.76 | 1247.7 | 78.0 | 9.4 | 92.9 | 1171.4 | trial 1; 16 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 46412 | 0 | 10271.14 steps/s | 247.56 | 421.41 | 6.53 | 14.82 | 20.27 | 104.35 | 1268.8 | 79.3 | 9.6 | 95.0 | 1145.7 | trial 2; 16 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 45988 | 0 | 10171.44 steps/s | 251.85 | 385.23 | 6.89 | 15.34 | 20.87 | 35.13 | 1239.1 | 77.4 | 9.4 | 87.0 | 1179.2 | trial 3; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 49882 | 0 | 10527.85 steps/s | 3923.61 | 4754.61 | 274.75 | 344.30 | 389.24 | 426.38 | 1328.7 | 83.0 | 9.9 | 28.8 | 2932.9 | trial 1; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 50662 | 0 | 10655.38 steps/s | 3804.38 | 4569.19 | 279.79 | 331.62 | 369.07 | 399.21 | 1331.5 | 83.2 | 10.2 | 29.9 | 3094.8 | trial 2; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 51146 | 0 | 10647.11 steps/s | 3859.59 | 4720.85 | 284.06 | 339.22 | 379.79 | 403.23 | 1317.3 | 82.3 | 10.0 | 28.9 | 3165.3 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median Flow p99 ms | Median Step p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: | ---: |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 10209.67 steps/s | 10271.14 steps/s | 421.41 | 20.87 |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 10647.11 steps/s | 10655.38 steps/s | 4720.85 | 379.79 |

## start-complete

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 12288 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 287339 | 0 | 6212.12 flows/s | 1953.40 | 2293.88 | 2580.26 | 2968.45 | 1279.9 | 80.0 | 9.3 | 29.3 | 1603.4 | trial 1; 16 connections |
| 12288 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 281218 | 0 | 6072.57 flows/s | 1999.97 | 2356.43 | 2648.22 | 3027.52 | 1277.0 | 79.8 | 9.1 | 28.4 | 1856.7 | trial 2; 16 connections |
| 12288 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 285858 | 0 | 6175.73 flows/s | 1969.95 | 2287.35 | 2537.94 | 2959.64 | 1279.4 | 80.0 | 9.3 | 28.8 | 1763.9 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 12288 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 6175.73 flows/s | 6212.12 flows/s | 2580.26 |

