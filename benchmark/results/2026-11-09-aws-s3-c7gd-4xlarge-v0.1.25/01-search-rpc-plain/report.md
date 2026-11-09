Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 8 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 710661 | 0 | 15792.31 rpcs/s | 0.48 | 0.59 | 1.00 | 2.79 | 372.0 | 23.2 | 11.3 | 0.3 | 52.6 | 16 connections |
| 48 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 2215192 | 0 | 49225.78 rpcs/s | 0.82 | 1.54 | 2.84 | 219.68 | 1066.8 | 66.7 | 35.7 | 0.3 | 67.1 | 16 connections |
| 128 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 2827950 | 0 | 62841.62 rpcs/s | 1.88 | 3.04 | 4.46 | 13.55 | 1140.3 | 71.3 | 35.3 | 0.3 | 77.8 | 16 connections |
| 256 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 2910555 | 0 | 64675.82 rpcs/s | 3.73 | 6.02 | 8.24 | 18.01 | 1107.0 | 69.2 | 34.3 | 0.3 | 94.9 | 16 connections |
| 512 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 2916117 | 0 | 64796.44 rpcs/s | 7.55 | 12.09 | 16.54 | 33.67 | 1101.3 | 68.8 | 32.6 | 0.3 | 127.3 | 16 connections |
| 2048 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 2792692 | 0 | 62032.96 rpcs/s | 31.80 | 49.38 | 67.48 | 137.79 | 988.4 | 61.8 | 29.5 | 0.3 | 333.9 | 16 connections |

