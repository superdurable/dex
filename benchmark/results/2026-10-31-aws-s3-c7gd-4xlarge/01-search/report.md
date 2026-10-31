Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 48 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 1885651 | 0 | 41900.54 rpcs/s | 1.03 | 1.70 | 2.58 | 7.39 | 1121.9 | 70.1 | 41.6 | 0.3 | 62.4 | 16 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 2005429 | 0 | 44560.00 rpcs/s | 5.84 | 7.49 | 9.29 | 18.33 | 1248.9 | 78.1 | 44.3 | 0.3 | 75.3 | 16 connections |

## sequential-steps

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 36710 | 0 | 7853.99 steps/s | 2527.44 | 3591.17 | 235.43 | 304.44 | 396.63 | 625.02 | 1063.4 | 66.5 | 7.5 | 74.1 | 778.2 | 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 41318 | 0 | 8693.73 steps/s | 4566.67 | 6259.03 | 418.42 | 541.67 | 694.41 | 1255.92 | 1161.4 | 72.6 | 8.4 | 60.4 | 918.5 | 16 connections |
| 8192 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 48967 | 0 | 9828.92 steps/s | 7980.11 | 10854.74 | 751.18 | 960.55 | 1139.31 | 1487.62 | 1244.9 | 77.8 | 9.3 | 54.7 | 1761.3 | 16 connections |

## sequential-steps-async

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 42679 | 0 | 9439.57 steps/s | 266.23 | 491.73 | 8.43 | 17.69 | 26.22 | 45.12 | 1218.6 | 76.2 | 8.8 | 93.9 | 1120.8 | 16 connections |
| 1024 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 42723 | 0 | 9360.32 steps/s | 1090.92 | 1663.13 | 61.07 | 93.42 | 114.49 | 138.24 | 1263.5 | 79.0 | 8.7 | 50.9 | 2539.3 | 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 46462 | 0 | 9834.65 steps/s | 4044.17 | 5497.23 | 297.35 | 404.71 | 510.24 | 571.27 | 1294.5 | 80.9 | 9.4 | 30.0 | 2959.7 | 16 connections |

## start-complete

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 3072 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 220305 | 0 | 4853.29 flows/s | 623.68 | 768.59 | 937.65 | 1178.70 | 1182.6 | 73.9 | 7.5 | 66.5 | 982.1 | 16 connections |
| 6144 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 241326 | 0 | 5275.90 flows/s | 1148.13 | 1370.26 | 1561.73 | 2349.70 | 1229.3 | 76.8 | 8.3 | 43.6 | 1179.5 | 16 connections |

