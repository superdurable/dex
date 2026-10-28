Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. CPU % is 100 per fully used core. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## sequential-steps

| Concurrency | Target | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 109 | 0 | 18.02 steps/s | 551.23 | 600.73 | 49.72 | 54.35 | 66.50 | 149.88 | 73.7 | 305.7 |
| 4 | temporal | 239 | 0 | 39.41 steps/s | 1017.80 | 1240.69 | 93.09 | 112.35 | 136.72 | 289.75 | 129.0 | 335.4 |
| 8 | temporal | 288 | 0 | 47.10 steps/s | 1693.78 | 1996.55 | 146.17 | 188.96 | 230.10 | 383.18 | 150.1 | 343.3 |

## start-complete

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 486 | 0 | 8.09 flows/s | 109.67 | 150.62 | 159.32 | 232.56 | 85.5 | 318.9 |
| 4 | temporal | 936 | 0 | 15.57 flows/s | 253.78 | 297.12 | 343.01 | 553.42 | 129.1 | 354.8 |
| 8 | temporal | 1002 | 0 | 16.61 flows/s | 476.14 | 552.63 | 657.91 | 804.67 | 134.9 | 366.1 |
| 16 | temporal | 995 | 0 | 16.42 flows/s | 961.28 | 1080.04 | 1234.15 | 1335.40 | 138.9 | 370.1 |
| 32 | temporal | 988 | 0 | 16.16 flows/s | 1646.08 | 3243.22 | 4837.89 | 5535.37 | 135.4 | 371.5 |
| 64 | temporal | 1004 | 0 | 16.04 flows/s | 3353.57 | 6808.27 | 7389.40 | 8211.21 | 134.2 | 384.0 |
