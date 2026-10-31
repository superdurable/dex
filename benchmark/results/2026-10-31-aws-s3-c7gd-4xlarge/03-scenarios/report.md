Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## attribute-read-direct

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 506826 | 0 | 11257.42 reads/s | 21.97 | 35.73 | 44.43 | 65.83 | 441.4 | 27.6 | 22.0 | 0.3 | 285.6 | 8 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 507162 | 0 | 11259.67 reads/s | 20.04 | 86.54 | 164.82 | 261.80 | 438.0 | 27.4 | 20.3 | 0.3 | 398.9 | 8 connections |
| 1024 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 507603 | 0 | 11258.41 reads/s | 76.51 | 145.71 | 178.25 | 264.69 | 527.2 | 33.0 | 24.4 | 0.3 | 627.6 | 8 connections |

## channel-wake

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 10010 | 0 | 221.26 wakes/s | 83.36 | 130.89 | 215.60 | 367.65 | 110.6 | 6.9 | 1.7 | 123.3 | 199.3 | 8 connections |

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 284740 | 0 | 6319.55 rpcs/s | 74.93 | 107.12 | 187.96 | 372.71 | 978.4 | 61.2 | 6.8 | 100.9 | 437.5 | 8 connections |
| 1024 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 376866 | 0 | 8358.59 rpcs/s | 115.85 | 154.16 | 317.57 | 1120.52 | 1166.4 | 72.9 | 8.7 | 91.2 | 522.2 | 8 connections |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 141000 | 0 | 3100.75 rpcs/s | 473.71 | 1458.99 | 2849.46 | 5241.27 | 1077.0 | 67.3 | 4.0 | 118.1 | 474.5 | 8 connections |

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 428276 | 0 | 9509.77 rpcs/s | 36.63 | 125.65 | 253.83 | 744.59 | 954.5 | 59.7 | 10.4 | 0.3 | 88.1 | 8 connections |
| 1024 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 246984 | 0 | 5474.96 rpcs/s | 127.92 | 435.11 | 878.30 | 2377.02 | 948.7 | 59.3 | 6.6 | 0.3 | 112.4 | 8 connections |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 134050 | 0 | 2954.47 rpcs/s | 477.39 | 1595.46 | 3231.92 | 5010.25 | 874.0 | 54.6 | 3.9 | 0.3 | 168.3 | 8 connections |

## start-complete

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 103357 | 0 | 2287.60 flows/s | 219.91 | 295.23 | 371.88 | 514.31 | 462.9 | 28.9 | 3.6 | 98.1 | 588.0 | 8 connections |
| 1024 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 141168 | 0 | 3117.21 flows/s | 317.01 | 408.10 | 676.90 | 881.78 | 654.3 | 40.9 | 4.9 | 71.0 | 674.5 | 8 connections |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 225139 | 0 | 4975.71 flows/s | 404.39 | 495.39 | 584.25 | 683.93 | 1080.5 | 67.5 | 7.6 | 67.2 | 972.0 | 8 connections |

