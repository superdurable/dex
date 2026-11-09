Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## sequential-steps-async

| Concurrency | Settings | Steps | Errors | Throughput | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 2 | default | 55423 | 0 | 1231.62 steps/s | 1.29 | 2.62 | 4.87 | 86.97 | 176.7 | 88.3 | 0.6 | 47.1 | 226.4 | trial 1; 4 connections |
| 2 | default | 55066 | 0 | 1223.68 steps/s | 1.31 | 2.59 | 4.85 | 72.98 | 176.6 | 88.3 | 0.6 | 47.2 | 232.7 | trial 2; 4 connections |
| 2 | default | 54914 | 0 | 1220.30 steps/s | 1.30 | 2.61 | 4.88 | 88.43 | 176.6 | 88.3 | 0.6 | 47.1 | 240.6 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median Step p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 2 | default | 3 | 1223.68 steps/s | 1231.62 steps/s | 4.87 |

