Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## sequential-steps

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | APPLIER_LANES=2 | 6309 | 0 | 1363.22 steps/s | 1811.24 | 2522.70 | 169.83 | 223.13 | 343.51 | 543.19 | 187.8 | 93.9 | 1.6 | 122.5 | 213.1 | 4 connections |
| 256 | BATCH_WINDOW=50ms | 6319 | 0 | 1364.68 steps/s | 1825.62 | 2553.74 | 165.22 | 260.80 | 424.72 | 970.77 | 188.7 | 94.4 | 1.6 | 110.2 | 227.1 | 4 connections |
| 256 | MAX_IN_FLIGHT=2 | 5396 | 0 | 1165.19 steps/s | 2156.28 | 2595.16 | 202.52 | 257.12 | 310.71 | 544.47 | 152.3 | 76.2 | 1.3 | 66.5 | 227.7 | 4 connections |
| 256 | MAX_IN_FLIGHT=8 | 5830 | 0 | 1257.88 steps/s | 1979.15 | 2849.61 | 175.52 | 315.89 | 482.12 | 1075.91 | 181.2 | 90.6 | 1.4 | 200.9 | 227.2 | 4 connections |
| 256 | default | 6273 | 0 | 1359.84 steps/s | 1830.02 | 2675.35 | 165.56 | 272.81 | 467.55 | 1254.20 | 192.3 | 96.1 | 1.5 | 119.8 | 227.2 | 4 connections |

## sequential-steps-async

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 32 | default | 6933 | 0 | 1536.96 steps/s | 205.43 | 353.35 | 8.15 | 25.27 | 53.99 | 166.57 | 191.5 | 95.7 | 1.7 | 99.0 | 225.0 | 4 connections |
| 64 | APPLIER_LANES=2 | 6780 | 0 | 1498.41 steps/s | 381.01 | 1182.91 | 19.80 | 79.03 | 208.86 | 933.12 | 195.7 | 97.8 | 1.7 | 102.0 | 219.7 | 4 connections |
| 64 | BATCH_WINDOW=50ms | 6936 | 0 | 1532.92 steps/s | 384.21 | 971.79 | 18.64 | 76.08 | 181.50 | 595.73 | 193.0 | 96.5 | 1.7 | 85.5 | 227.6 | 4 connections |
| 64 | MAX_IN_FLIGHT=2 | 6937 | 0 | 1533.63 steps/s | 398.23 | 849.28 | 20.06 | 76.66 | 173.26 | 543.12 | 194.3 | 97.2 | 1.8 | 60.9 | 219.2 | 4 connections |
| 64 | MAX_IN_FLIGHT=8 | 6753 | 0 | 1493.00 steps/s | 400.59 | 1022.96 | 19.54 | 78.66 | 188.90 | 810.11 | 194.0 | 97.0 | 1.7 | 129.8 | 228.5 | 4 connections |

