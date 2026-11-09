Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## start-complete

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 3072 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 76434 | 0 | 4902.46 flows/s | 610.81 | 709.16 | 801.01 | 931.88 | 1155.5 | 72.2 | 9.2 | 55.1 | 921.8 | 48 connections |
| 6144 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 83762 | 0 | 5308.95 flows/s | 1145.83 | 1336.15 | 1532.93 | 1936.47 | 1232.6 | 77.0 | 10.0 | 43.9 | 1365.6 | 48 connections |
| 12288 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=4096 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 92373 | 0 | 5626.04 flows/s | 2163.90 | 2493.12 | 2747.08 | 3250.43 | 1300.8 | 81.3 | 10.3 | 30.9 | 2433.3 | 48 connections |

