Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## channel-wake

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 63477 | 0 | 1400.23 wakes/s | 138.80 | 197.29 | 307.52 | 456.80 | 898.1 | 56.1 | 16.9 | 98.4 | 465.7 | trial 1; 16 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 56783 | 0 | 1252.55 wakes/s | 148.85 | 235.37 | 382.63 | 595.75 | 833.5 | 52.1 | 16.6 | 86.7 | 473.3 | trial 2; 16 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 64173 | 0 | 1416.46 wakes/s | 137.20 | 193.10 | 255.50 | 302.44 | 880.1 | 55.0 | 17.7 | 98.4 | 492.6 | trial 3; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 103458 | 0 | 2231.42 wakes/s | 851.64 | 975.44 | 1072.31 | 1194.64 | 1413.1 | 88.3 | 26.0 | 92.1 | 1617.7 | trial 1; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 107850 | 0 | 2335.10 wakes/s | 814.68 | 955.79 | 1069.69 | 1193.32 | 1405.3 | 87.8 | 24.9 | 93.5 | 1675.3 | trial 2; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 105756 | 0 | 2278.09 wakes/s | 820.86 | 1008.25 | 1146.98 | 1324.74 | 1397.6 | 87.4 | 27.6 | 90.6 | 1917.7 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 1400.23 wakes/s | 1416.46 wakes/s | 307.52 |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 2278.09 wakes/s | 2335.10 wakes/s | 1072.31 |

## sequential-steps

| Concurrency | Settings | Steps | Errors | Throughput | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 1024 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 207448 | 0 | 4609.95 steps/s | 212.98 | 302.44 | 381.39 | 452.89 | 589.0 | 36.8 | 3.2 | 69.1 | 603.2 | trial 1; 16 connections |
| 1024 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 182463 | 0 | 4054.72 steps/s | 229.12 | 346.79 | 642.67 | 738.05 | 504.2 | 31.5 | 2.8 | 60.0 | 592.4 | trial 2; 16 connections |
| 1024 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 203728 | 0 | 4527.18 steps/s | 218.25 | 295.14 | 382.11 | 581.58 | 577.9 | 36.1 | 3.5 | 68.9 | 590.1 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median Step p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 1024 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 4527.18 steps/s | 4609.95 steps/s | 382.11 |

## sequential-steps-async

| Concurrency | Settings | Steps | Errors | Throughput | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 8 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 215001 | 0 | 4777.79 steps/s | 1.56 | 2.03 | 3.24 | 72.51 | 868.1 | 54.3 | 2.4 | 48.6 | 546.0 | trial 1; 16 connections |
| 8 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 214491 | 0 | 4766.40 steps/s | 1.56 | 2.04 | 3.27 | 66.77 | 869.6 | 54.3 | 2.4 | 48.6 | 581.2 | trial 2; 16 connections |
| 8 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 213639 | 0 | 4747.53 steps/s | 1.57 | 2.05 | 3.31 | 66.91 | 869.2 | 54.3 | 2.4 | 48.7 | 566.0 | trial 3; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 452790 | 0 | 10061.86 steps/s | 404.71 | 466.31 | 537.13 | 567.41 | 1278.3 | 79.9 | 8.3 | 25.3 | 3121.5 | trial 1; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 462372 | 0 | 10274.88 steps/s | 395.59 | 438.40 | 472.02 | 491.03 | 1283.4 | 80.2 | 7.7 | 24.6 | 3238.8 | trial 2; 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 449460 | 0 | 9987.99 steps/s | 408.47 | 454.14 | 497.42 | 518.04 | 1286.5 | 80.4 | 7.8 | 23.1 | 3178.5 | trial 3; 16 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median Step p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 8 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 4766.40 steps/s | 4777.79 steps/s | 3.27 |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 3 | 10061.86 steps/s | 10274.88 steps/s | 497.42 |

