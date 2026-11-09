Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## channel-wake

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | default | 9984 | 0 | 220.59 wakes/s | 106.52 | 155.16 | 208.09 | 372.70 | 120.5 | 60.2 | 1.7 | 106.9 | 183.4 | trial 1; 4 connections |
| 64 | default | 9235 | 0 | 203.79 wakes/s | 111.82 | 176.79 | 303.90 | 405.49 | 113.3 | 56.6 | 1.7 | 99.8 | 131.9 | trial 2; 4 connections |
| 64 | default | 7880 | 0 | 172.20 wakes/s | 116.48 | 255.51 | 501.88 | 877.25 | 102.3 | 51.2 | 1.8 | 85.4 | 124.7 | trial 3; 4 connections |
| 256 | default | 20972 | 0 | 460.40 wakes/s | 208.50 | 275.10 | 408.17 | 543.51 | 195.1 | 97.5 | 3.5 | 80.9 | 216.9 | trial 1; 4 connections |
| 256 | default | 20721 | 0 | 454.07 wakes/s | 210.24 | 277.20 | 467.90 | 639.72 | 196.2 | 98.1 | 3.9 | 80.9 | 220.5 | trial 2; 4 connections |
| 256 | default | 21310 | 0 | 468.02 wakes/s | 209.20 | 258.11 | 430.07 | 590.14 | 196.4 | 98.2 | 3.8 | 85.4 | 224.6 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 64 | default | 3 | 203.79 wakes/s | 220.59 wakes/s | 303.90 |
| 256 | default | 3 | 460.40 wakes/s | 468.02 wakes/s | 430.07 |

## sequential-steps

| Concurrency | Settings | Steps | Errors | Throughput | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 96 | default | 38250 | 0 | 850.00 steps/s | 104.25 | 150.10 | 241.62 | 384.92 | 88.1 | 44.0 | 0.5 | 112.2 | 189.0 | trial 1; 4 connections |
| 96 | default | 32814 | 0 | 729.19 steps/s | 115.38 | 175.94 | 431.44 | 664.46 | 76.3 | 38.2 | 0.4 | 97.7 | 182.2 | trial 2; 4 connections |
| 96 | default | 38065 | 0 | 845.88 steps/s | 105.00 | 146.82 | 284.51 | 345.45 | 87.0 | 43.5 | 0.5 | 109.3 | 189.2 | trial 3; 4 connections |
| 512 | default | 81571 | 0 | 1812.69 steps/s | 280.38 | 335.66 | 396.29 | 503.05 | 194.8 | 97.4 | 1.4 | 93.2 | 266.6 | trial 1; 4 connections |
| 512 | default | 77803 | 0 | 1728.95 steps/s | 283.86 | 370.29 | 583.36 | 893.19 | 186.8 | 93.4 | 1.3 | 91.3 | 266.6 | trial 2; 4 connections |
| 512 | default | 79761 | 0 | 1772.43 steps/s | 281.99 | 343.70 | 484.39 | 647.75 | 191.6 | 95.8 | 1.4 | 93.0 | 269.0 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median Step p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 96 | default | 3 | 845.88 steps/s | 850.00 steps/s | 284.51 |
| 512 | default | 3 | 1772.43 steps/s | 1812.69 steps/s | 484.39 |

## sequential-steps-async

| Concurrency | Settings | Steps | Errors | Throughput | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 4 | default | 67805 | 0 | 1506.76 steps/s | 2.13 | 4.63 | 7.97 | 108.02 | 192.5 | 96.3 | 0.7 | 46.2 | 240.2 | trial 1; 4 connections |
| 4 | default | 66306 | 0 | 1473.45 steps/s | 2.29 | 4.46 | 7.51 | 93.97 | 191.2 | 95.6 | 0.7 | 46.4 | 242.1 | trial 2; 4 connections |
| 4 | default | 67825 | 0 | 1507.22 steps/s | 2.04 | 4.89 | 8.58 | 135.95 | 194.4 | 97.2 | 0.7 | 46.1 | 261.5 | trial 3; 4 connections |
| 32 | default | 94962 | 0 | 2110.24 steps/s | 14.47 | 23.94 | 33.23 | 51.68 | 196.6 | 98.3 | 0.9 | 38.9 | 255.6 | trial 1; 4 connections |
| 32 | default | 94709 | 0 | 2104.63 steps/s | 14.53 | 23.92 | 32.63 | 53.18 | 196.4 | 98.2 | 0.9 | 38.4 | 245.0 | trial 2; 4 connections |
| 32 | default | 94539 | 0 | 2100.84 steps/s | 14.59 | 23.78 | 32.39 | 51.90 | 196.6 | 98.3 | 0.9 | 38.5 | 240.4 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median Step p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 4 | default | 3 | 1506.76 steps/s | 1507.22 steps/s | 7.97 |
| 32 | default | 3 | 2104.63 steps/s | 2110.24 steps/s | 32.63 |

