Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 6144 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=16384 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 516378 | 0 | 11409.77 rpcs/s | 540.34 | 622.43 | 684.10 | 827.74 | 1032.8 | 64.5 | 10.9 | 19.0 | 1308.7 | 32 connections |
| 8192 | BLOCK_CACHE_BYTES=8589934592 EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 MAX_INFLIGHT_RPCS=16384 MAX_UNAPPLIED_BYTES=268435456 WRITE_BUFFER_BYTES=134217728 | 541271 | 0 | 11948.61 rpcs/s | 687.96 | 764.93 | 829.62 | 957.25 | 1040.6 | 65.0 | 11.1 | 13.8 | 1564.2 | 32 connections |

