Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## start-complete

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | default | 6577 | 0 | 435.12 flows/s | 139.90 | 184.80 | 249.25 | 292.06 | 78.1 | 39.1 | 0.6 | 121.9 | 103.3 | 4 connections |
| 512 | default | 16997 | 0 | 1111.27 flows/s | 457.06 | 559.67 | 649.83 | 752.59 | 190.8 | 95.4 | 2.2 | 88.0 | 230.9 | 4 connections |
| 1024 | default | 15218 | 0 | 979.87 flows/s | 1011.98 | 1309.92 | 1573.00 | 1809.94 | 189.5 | 94.7 | 2.0 | 54.1 | 279.3 | 4 connections |

