Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## sequential-steps

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | default | 5704 | 0 | 1233.14 steps/s | 2023.13 | 2954.88 | 181.24 | 314.17 | 521.19 | 1151.02 | 191.6 | 95.8 | 1.4 | 116.6 | 265.0 | 4 connections |
| 512 | default | 5696 | 0 | 1205.98 steps/s | 4007.01 | 7298.53 | 331.93 | 836.37 | 1652.40 | 3865.12 | 194.8 | 97.4 | 1.4 | 114.9 | 241.7 | 4 connections |
| 1024 | default | 5700 | 0 | 1145.14 steps/s | 8458.37 | 13825.53 | 757.89 | 1453.33 | 2525.57 | 5217.13 | 194.5 | 97.3 | 1.4 | 93.5 | 263.3 | 4 connections |

## sequential-steps-async

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | default | 6779 | 0 | 1499.50 steps/s | 397.99 | 944.50 | 20.96 | 78.94 | 183.02 | 670.98 | 194.6 | 97.3 | 1.7 | 97.1 | 222.8 | 4 connections |
| 128 | default | 6476 | 0 | 1423.47 steps/s | 829.25 | 2092.73 | 52.01 | 210.23 | 468.64 | 1177.31 | 195.4 | 97.7 | 1.7 | 99.9 | 232.8 | 4 connections |
| 256 | default | 6066 | 0 | 1322.05 steps/s | 1806.98 | 4069.80 | 127.07 | 483.91 | 1031.99 | 2467.95 | 195.9 | 98.0 | 1.5 | 97.8 | 235.9 | 4 connections |
| 512 | default | 5539 | 0 | 1177.89 steps/s | 4035.08 | 9101.66 | 291.85 | 1085.16 | 2284.09 | 5028.55 | 195.1 | 97.6 | 1.4 | 91.6 | 244.1 | 4 connections |

