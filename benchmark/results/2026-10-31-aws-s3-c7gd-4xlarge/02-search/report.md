Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## attribute-read-direct

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 506823 | 0 | 11257.55 reads/s | 23.81 | 33.20 | 40.35 | 71.38 | 450.1 | 28.1 | 23.0 | 0.3 | 282.5 | 16 connections |

## channel-wake

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 9373 | 0 | 207.30 wakes/s | 86.51 | 142.64 | 286.87 | 406.19 | 118.9 | 7.4 | 1.7 | 115.8 | 197.0 | 16 connections |

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 1024 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 372531 | 0 | 8263.14 rpcs/s | 120.91 | 155.81 | 245.38 | 487.77 | 1180.1 | 73.8 | 9.2 | 93.9 | 552.3 | 16 connections |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 448611 | 0 | 9939.84 rpcs/s | 203.62 | 252.17 | 310.53 | 502.11 | 1276.7 | 79.8 | 11.0 | 84.5 | 578.6 | 16 connections |
| 4096 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 537398 | 0 | 11889.96 rpcs/s | 345.80 | 399.60 | 457.02 | 637.74 | 1324.5 | 82.8 | 12.9 | 67.5 | 776.7 | 16 connections |

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 128 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 2029316 | 0 | 45092.80 rpcs/s | 2.77 | 3.96 | 5.36 | 216.35 | 1216.2 | 76.0 | 42.6 | 0.3 | 69.3 | 16 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 1931388 | 0 | 42908.62 rpcs/s | 12.21 | 15.25 | 18.10 | 226.08 | 1282.5 | 80.2 | 42.6 | 0.3 | 88.9 | 16 connections |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 1843799 | 0 | 40931.61 rpcs/s | 53.41 | 57.63 | 61.25 | 70.81 | 1245.2 | 77.8 | 43.3 | 0.3 | 170.3 | 16 connections |

## sequential-steps

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 12288 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 49243 | 0 | 9917.16 steps/s | 11845.53 | 15143.67 | 1079.71 | 1429.73 | 1712.70 | 2166.41 | 1254.5 | 78.4 | 9.6 | 65.5 | 2207.0 | 16 connections |
| 16384 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 49152 | 0 | 9790.43 steps/s | 15696.78 | 19557.61 | 1498.48 | 1856.14 | 2277.27 | 2974.21 | 1263.7 | 79.0 | 9.5 | 69.3 | 2157.5 | 16 connections |

## start-complete

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 12288 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 259115 | 0 | 5594.43 flows/s | 2178.30 | 2551.78 | 2852.39 | 3377.11 | 1256.5 | 78.5 | 8.7 | 27.2 | 1876.8 | 16 connections |

