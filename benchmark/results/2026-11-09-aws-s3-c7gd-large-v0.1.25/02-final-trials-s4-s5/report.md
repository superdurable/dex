Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## attribute-read-direct

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 3 | default | 139269 | 0 | 3094.79 reads/s | 0.93 | 1.20 | 1.51 | 3.79 | 139.0 | 69.5 | 6.6 | 0.3 | 52.1 | trial 1; 4 connections |
| 3 | default | 137445 | 0 | 3054.24 reads/s | 0.93 | 1.22 | 1.66 | 17.96 | 140.0 | 70.0 | 6.5 | 0.3 | 53.8 | trial 2; 4 connections |
| 3 | default | 139098 | 0 | 3091.01 reads/s | 0.93 | 1.20 | 1.52 | 3.75 | 139.8 | 69.9 | 6.5 | 0.3 | 53.5 | trial 3; 4 connections |
| 256 | default | 267526 | 0 | 5941.49 reads/s | 42.84 | 59.46 | 73.79 | 94.76 | 197.0 | 98.5 | 12.0 | 0.3 | 276.1 | trial 1; 4 connections |
| 256 | default | 268708 | 0 | 5967.56 reads/s | 42.72 | 59.43 | 74.18 | 99.75 | 196.2 | 98.1 | 11.1 | 0.3 | 251.1 | trial 2; 4 connections |
| 256 | default | 266729 | 0 | 5923.58 reads/s | 43.07 | 59.63 | 74.58 | 108.96 | 197.1 | 98.5 | 11.6 | 0.3 | 264.4 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 3 | default | 3 | 3091.01 reads/s | 3094.79 reads/s | 1.52 |
| 256 | default | 3 | 5941.49 reads/s | 5967.56 reads/s | 74.18 |

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | default | 40733 | 0 | 904.04 rpcs/s | 67.01 | 91.26 | 183.54 | 300.70 | 160.9 | 80.5 | 0.8 | 100.5 | 131.1 | trial 1; 4 connections |
| 64 | default | 42058 | 0 | 933.52 rpcs/s | 65.52 | 88.92 | 123.52 | 334.33 | 169.1 | 84.5 | 0.8 | 106.2 | 132.2 | trial 2; 4 connections |
| 64 | default | 40196 | 0 | 892.05 rpcs/s | 66.36 | 93.11 | 203.65 | 309.47 | 158.8 | 79.4 | 0.8 | 100.8 | 131.4 | trial 3; 4 connections |
| 960 | default | 98114 | 0 | 2165.31 rpcs/s | 425.62 | 590.18 | 774.16 | 1252.69 | 186.8 | 93.4 | 1.6 | 50.5 | 333.8 | trial 1; 4 connections |
| 960 | default | 99106 | 0 | 2189.48 rpcs/s | 429.35 | 582.87 | 747.03 | 1107.84 | 190.4 | 95.2 | 1.7 | 52.4 | 325.6 | trial 2; 4 connections |
| 960 | default | 96567 | 0 | 2132.15 rpcs/s | 436.21 | 615.19 | 842.69 | 1114.79 | 184.4 | 92.2 | 1.6 | 50.2 | 332.1 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 64 | default | 3 | 904.04 rpcs/s | 933.52 rpcs/s | 183.54 |
| 960 | default | 3 | 2165.31 rpcs/s | 2189.48 rpcs/s | 774.16 |

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 4 | default | 325639 | 0 | 7236.34 rpcs/s | 0.52 | 0.69 | 0.97 | 6.08 | 163.1 | 81.6 | 5.3 | 0.3 | 42.3 | trial 1; 4 connections |
| 4 | default | 354504 | 0 | 7877.79 rpcs/s | 0.49 | 0.58 | 0.83 | 3.40 | 169.7 | 84.9 | 5.7 | 0.3 | 43.3 | trial 2; 4 connections |
| 4 | default | 337568 | 0 | 7501.42 rpcs/s | 0.52 | 0.62 | 0.87 | 2.75 | 172.4 | 86.2 | 5.4 | 0.3 | 42.7 | trial 3; 4 connections |
| 256 | default | 685552 | 0 | 15231.39 rpcs/s | 16.87 | 22.50 | 27.05 | 34.48 | 193.2 | 96.6 | 8.4 | 0.3 | 77.3 | trial 1; 4 connections |
| 256 | default | 692816 | 0 | 15392.43 rpcs/s | 16.67 | 22.31 | 26.65 | 38.24 | 193.4 | 96.7 | 8.5 | 0.3 | 77.4 | trial 2; 4 connections |
| 256 | default | 693979 | 0 | 15417.46 rpcs/s | 16.66 | 22.26 | 26.63 | 35.09 | 193.1 | 96.5 | 8.3 | 0.3 | 77.9 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 4 | default | 3 | 7501.42 rpcs/s | 7877.79 rpcs/s | 0.87 |
| 256 | default | 3 | 15392.43 rpcs/s | 15417.46 rpcs/s | 26.65 |

