Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 6144 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=16384 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 582676 | 0 | 12854.10 rpcs/s | 477.71 | 567.25 | 656.39 | 863.07 | 1349.6 | 84.3 | 14.0 | 52.3 | 921.8 | 16 connections |
| 8192 | BLOCK_CACHE_BYTES=8589934592 MAX_INFLIGHT_RPCS=16384 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 605157 | 0 | 13311.75 rpcs/s | 620.38 | 735.37 | 847.46 | 1099.43 | 1354.8 | 84.7 | 14.8 | 52.0 | 1017.2 | 16 connections |

