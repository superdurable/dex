Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## channel-wake

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 16 | default | 3079 | 0 | 68.06 wakes/s | 87.74 | 120.63 | 161.88 | 461.55 | 43.0 | 21.5 | 0.5 | 120.5 | 77.7 | 4 connections |
| 64 | default | 8903 | 0 | 196.90 wakes/s | 106.51 | 177.95 | 450.49 | 676.65 | 114.7 | 57.3 | 1.8 | 97.9 | 132.4 | 4 connections |
| 256 | default | 20201 | 0 | 443.98 wakes/s | 216.47 | 297.05 | 402.30 | 524.24 | 186.6 | 93.3 | 3.3 | 83.4 | 218.2 | 4 connections |

## sequential-steps

| Concurrency | Settings | Steps | Errors | Throughput | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | default | 25651 | 0 | 570.02 steps/s | 103.23 | 152.60 | 214.39 | 338.87 | 67.0 | 33.5 | 0.4 | 112.3 | 150.7 | 4 connections |
| 128 | default | 48275 | 0 | 1072.76 steps/s | 112.48 | 154.33 | 269.70 | 347.70 | 123.2 | 61.6 | 0.7 | 108.1 | 202.6 | 4 connections |
| 256 | default | 69699 | 0 | 1548.86 steps/s | 151.13 | 233.26 | 323.28 | 492.38 | 179.4 | 89.7 | 1.2 | 103.2 | 239.1 | 4 connections |
| 512 | default | 72436 | 0 | 1609.65 steps/s | 293.17 | 440.47 | 610.00 | 999.12 | 184.1 | 92.0 | 1.4 | 92.0 | 275.8 | 4 connections |
| 768 | default | 70261 | 0 | 1561.34 steps/s | 463.64 | 656.41 | 910.49 | 1474.79 | 184.1 | 92.1 | 1.4 | 83.4 | 300.2 | 4 connections |
| 1024 | default | 71403 | 0 | 1586.70 steps/s | 589.82 | 850.83 | 1567.41 | 1767.40 | 182.8 | 91.4 | 1.4 | 76.6 | 331.0 | 4 connections |

## sequential-steps-async

| Concurrency | Settings | Steps | Errors | Throughput | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 8 | default | 73139 | 0 | 1625.31 steps/s | 4.00 | 8.45 | 14.26 | 213.65 | 191.6 | 95.8 | 0.7 | 43.5 | 295.2 | 4 connections |
| 32 | default | 87371 | 0 | 1941.56 steps/s | 15.66 | 25.81 | 39.03 | 108.64 | 189.1 | 94.5 | 0.8 | 38.1 | 239.1 | 4 connections |
| 128 | default | 82450 | 0 | 1832.19 steps/s | 66.84 | 95.27 | 141.80 | 194.03 | 189.0 | 94.5 | 1.0 | 33.6 | 264.5 | 4 connections |
| 512 | default | 81455 | 0 | 1810.10 steps/s | 262.61 | 357.44 | 633.60 | 724.17 | 188.2 | 94.1 | 1.4 | 29.5 | 416.1 | 4 connections |

