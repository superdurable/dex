Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## start-complete

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 6730 | 0 | 444.83 flows/s | 138.82 | 176.12 | 205.21 | 239.08 | 144.3 | 72.2 | 0.7 | 572.8 | 120.9 | 4 connections |
| 512 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 10770 | 0 | 694.91 flows/s | 725.37 | 873.76 | 1001.13 | 1268.31 | 194.1 | 97.0 | 1.3 | 474.1 | 291.5 | 4 connections |
| 1024 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 11573 | 0 | 727.90 flows/s | 1372.22 | 1633.85 | 1870.72 | 2154.04 | 193.2 | 96.6 | 1.6 | 442.9 | 347.1 | 4 connections |

