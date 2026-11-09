Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## sequential-steps

| Concurrency | Settings | Steps | Errors | Throughput | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 25133 | 0 | 558.51 steps/s | 106.85 | 154.96 | 220.83 | 325.11 | 166.1 | 83.1 | 0.4 | 117.2 | 184.7 | 4 connections |
| 128 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 32670 | 0 | 725.98 steps/s | 171.04 | 244.54 | 340.27 | 454.95 | 176.4 | 88.2 | 0.5 | 93.0 | 210.6 | 4 connections |
| 256 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 39988 | 0 | 888.61 steps/s | 269.17 | 409.64 | 548.72 | 778.21 | 185.4 | 92.7 | 0.7 | 85.7 | 223.4 | 4 connections |
| 512 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 45946 | 0 | 1021.02 steps/s | 480.95 | 672.13 | 879.71 | 1373.78 | 184.4 | 92.2 | 0.9 | 82.6 | 263.8 | 4 connections |
| 768 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 49613 | 0 | 1102.51 steps/s | 663.70 | 917.96 | 1223.04 | 1711.23 | 186.0 | 93.0 | 1.0 | 76.8 | 292.7 | 4 connections |
| 1024 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 51828 | 0 | 1151.71 steps/s | 858.90 | 1172.61 | 1601.48 | 2074.12 | 184.8 | 92.4 | 1.0 | 64.8 | 331.5 | 4 connections |

## sequential-steps-async

| Concurrency | Settings | Steps | Errors | Throughput | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 8 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 8946 | 0 | 198.80 steps/s | 36.29 | 66.31 | 106.08 | 181.84 | 188.7 | 94.3 | 0.1 | 15.2 | 191.8 | 4 connections |
| 32 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 19353 | 0 | 430.06 steps/s | 69.93 | 118.83 | 174.09 | 301.01 | 187.2 | 93.6 | 0.2 | 21.8 | 213.6 | 4 connections |
| 128 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 34744 | 0 | 772.07 steps/s | 148.83 | 270.72 | 447.56 | 574.25 | 187.2 | 93.6 | 0.5 | 19.7 | 288.6 | 4 connections |
| 512 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 50073 | 0 | 1112.72 steps/s | 429.95 | 663.74 | 927.23 | 1159.46 | 186.6 | 93.3 | 0.8 | 21.2 | 368.5 | 4 connections |

