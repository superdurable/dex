Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## sequential-steps

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | default | 5991 | 0 | 1294.83 steps/s | 1916.13 | 2832.59 | 175.67 | 276.51 | 434.12 | 846.66 | 180.3 | 90.2 | 1.5 | 99.4 | 220.6 | trial 1; 4 connections |
| 256 | default | 6285 | 0 | 1360.75 steps/s | 1844.33 | 2506.94 | 166.99 | 261.07 | 419.66 | 1015.95 | 190.4 | 95.2 | 1.6 | 110.1 | 229.0 | trial 2; 4 connections |
| 256 | default | 6037 | 0 | 1305.26 steps/s | 1887.55 | 2917.59 | 172.92 | 272.60 | 439.16 | 826.58 | 179.8 | 89.9 | 1.5 | 101.3 | 224.4 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 256 | default | 3 | 1305.26 steps/s | 1360.75 steps/s | 2832.59 |

## sequential-steps-async

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 32 | default | 6641 | 0 | 1471.55 steps/s | 213.77 | 371.78 | 6.39 | 22.38 | 49.67 | 159.26 | 179.3 | 89.7 | 1.7 | 84.7 | 229.3 | trial 1; 4 connections |
| 32 | default | 6895 | 0 | 1529.45 steps/s | 206.70 | 339.24 | 7.09 | 23.21 | 51.19 | 136.38 | 189.0 | 94.5 | 1.8 | 91.5 | 223.3 | trial 2; 4 connections |
| 32 | default | 6741 | 0 | 1491.99 steps/s | 208.48 | 412.17 | 7.55 | 24.64 | 52.23 | 173.64 | 186.4 | 93.2 | 1.7 | 92.2 | 210.7 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 32 | default | 3 | 1491.99 steps/s | 1529.45 steps/s | 371.78 |

