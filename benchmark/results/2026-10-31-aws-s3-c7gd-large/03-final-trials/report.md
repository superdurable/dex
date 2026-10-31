Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## attribute-read-direct

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | default | 285146 | 0 | 6332.45 reads/s | 40.21 | 55.71 | 69.53 | 103.71 | 197.1 | 98.5 | 10.2 | 0.3 | 216.3 | trial 1; 4 connections |
| 256 | default | 271643 | 0 | 6032.12 reads/s | 42.36 | 58.32 | 72.21 | 110.39 | 195.3 | 97.7 | 9.8 | 0.3 | 208.1 | trial 2; 4 connections |
| 256 | default | 251250 | 0 | 5578.86 reads/s | 45.84 | 63.48 | 79.03 | 109.29 | 193.9 | 97.0 | 9.0 | 0.3 | 198.8 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 256 | default | 3 | 6032.12 reads/s | 6332.45 reads/s | 72.21 |

## channel-wake

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | default | 9010 | 0 | 199.12 wakes/s | 96.17 | 140.33 | 253.49 | 336.44 | 110.0 | 55.0 | 1.6 | 115.8 | 152.4 | trial 1; 4 connections |
| 64 | default | 9445 | 0 | 208.70 wakes/s | 93.32 | 130.56 | 216.88 | 372.52 | 112.4 | 56.2 | 1.6 | 120.4 | 155.7 | trial 2; 4 connections |
| 64 | default | 10243 | 0 | 226.22 wakes/s | 88.35 | 121.50 | 153.68 | 211.30 | 119.9 | 59.9 | 1.6 | 129.7 | 167.4 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 64 | default | 3 | 208.70 wakes/s | 226.22 wakes/s | 216.88 |

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 768 | default | 82065 | 0 | 1804.87 rpcs/s | 448.52 | 534.26 | 631.22 | 773.89 | 194.2 | 97.1 | 2.4 | 116.2 | 248.8 | trial 1; 4 connections |
| 768 | default | 81468 | 0 | 1793.17 rpcs/s | 451.76 | 532.98 | 636.73 | 826.55 | 195.2 | 97.6 | 2.4 | 114.7 | 248.5 | trial 2; 4 connections |
| 768 | default | 83404 | 0 | 1833.84 rpcs/s | 436.50 | 538.50 | 625.09 | 803.43 | 195.3 | 97.7 | 2.4 | 113.3 | 253.6 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 768 | default | 3 | 1804.87 rpcs/s | 1833.84 rpcs/s | 631.22 |

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 32 | default | 322615 | 0 | 7168.54 rpcs/s | 4.52 | 6.37 | 8.37 | 15.38 | 195.7 | 97.8 | 7.8 | 0.3 | 41.5 | trial 1; 4 connections |
| 32 | default | 334208 | 0 | 7426.30 rpcs/s | 4.35 | 6.15 | 8.10 | 15.28 | 195.3 | 97.6 | 8.0 | 0.3 | 42.0 | trial 2; 4 connections |
| 32 | default | 339482 | 0 | 7543.56 rpcs/s | 4.29 | 6.06 | 7.93 | 16.00 | 195.5 | 97.7 | 8.1 | 0.3 | 43.0 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 32 | default | 3 | 7426.30 rpcs/s | 7543.56 rpcs/s | 8.10 |

## sequential-steps

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 512 | default | 6755 | 0 | 1435.63 steps/s | 3500.96 | 4538.94 | 360.93 | 466.77 | 574.32 | 849.31 | 193.7 | 96.9 | 1.6 | 107.3 | 257.1 | trial 1; 4 connections |
| 512 | default | 6756 | 0 | 1440.23 steps/s | 3531.30 | 4354.75 | 363.20 | 467.67 | 575.13 | 860.88 | 193.5 | 96.8 | 1.6 | 108.1 | 250.0 | trial 2; 4 connections |
| 512 | default | 6666 | 0 | 1418.43 steps/s | 3561.98 | 4587.68 | 362.44 | 488.76 | 631.09 | 824.43 | 189.1 | 94.6 | 1.6 | 103.7 | 253.8 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median Flow p99 ms | Median Step p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: | ---: |
| 512 | default | 3 | 1435.63 steps/s | 1440.23 steps/s | 4538.94 | 575.13 |

## sequential-steps-async

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 32 | default | 6946 | 0 | 1538.08 steps/s | 208.58 | 332.18 | 8.79 | 22.55 | 35.59 | 96.73 | 191.1 | 95.6 | 1.7 | 93.2 | 223.0 | trial 1; 4 connections |
| 32 | default | 6800 | 0 | 1507.61 steps/s | 209.04 | 383.14 | 7.77 | 21.35 | 33.98 | 74.45 | 184.8 | 92.4 | 1.7 | 88.4 | 220.2 | trial 2; 4 connections |
| 32 | default | 7051 | 0 | 1561.91 steps/s | 206.17 | 323.37 | 9.17 | 22.80 | 35.69 | 74.02 | 191.9 | 95.9 | 1.8 | 96.9 | 237.9 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median Flow p99 ms | Median Step p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: | ---: |
| 32 | default | 3 | 1538.08 steps/s | 1561.91 steps/s | 332.18 | 35.59 |

## start-complete

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 512 | default | 40283 | 0 | 888.83 flows/s | 584.94 | 733.28 | 940.31 | 1096.23 | 194.6 | 97.3 | 1.5 | 103.4 | 298.1 | trial 1; 4 connections |
| 512 | default | 40405 | 0 | 891.51 flows/s | 605.60 | 722.03 | 866.81 | 1010.45 | 195.1 | 97.6 | 1.5 | 109.6 | 304.8 | trial 2; 4 connections |
| 512 | default | 39888 | 0 | 880.41 flows/s | 602.81 | 733.08 | 872.89 | 1071.42 | 191.4 | 95.7 | 1.5 | 103.3 | 301.8 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 512 | default | 3 | 888.83 flows/s | 891.51 flows/s | 872.89 |

