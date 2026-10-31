Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## attribute-read-direct

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 512 | default | 241412 | 0 | 5358.51 reads/s | 89.32 | 135.23 | 240.90 | 369.31 | 176.1 | 88.1 | 8.7 | 0.3 | 346.4 | 4 connections |

## channel-wake

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | default | 8531 | 0 | 187.78 wakes/s | 100.58 | 151.32 | 333.63 | 538.54 | 112.4 | 56.2 | 1.4 | 110.6 | 149.1 | 4 connections |

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | default | 60230 | 0 | 1332.37 rpcs/s | 197.02 | 260.97 | 366.53 | 513.66 | 182.6 | 91.3 | 1.8 | 108.4 | 174.1 | 4 connections |
| 512 | default | 73226 | 0 | 1614.38 rpcs/s | 333.76 | 398.18 | 530.35 | 640.10 | 188.2 | 94.1 | 2.1 | 107.2 | 202.2 | 4 connections |
| 1024 | default | 62179 | 388509 | 1364.53 rpcs/s | 785.78 | 854.45 | 907.50 | 1009.85 | 195.2 | 97.6 | 4.4 | 115.1 | 210.4 | 4 connections |

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 32 | default | 326954 | 0 | 7264.95 rpcs/s | 4.39 | 6.30 | 8.75 | 18.85 | 189.4 | 94.7 | 9.0 | 0.3 | 41.5 | 4 connections |
| 128 | default | 297786 | 0 | 6615.15 rpcs/s | 20.84 | 24.75 | 34.84 | 50.05 | 191.6 | 95.8 | 7.4 | 0.3 | 46.3 | 4 connections |
| 512 | default | 263238 | 0 | 5842.19 rpcs/s | 88.41 | 97.02 | 221.63 | 366.26 | 175.6 | 87.8 | 6.6 | 0.3 | 66.5 | 4 connections |

## sequential-steps

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | default | 5396 | 0 | 1165.42 steps/s | 2046.66 | 3454.30 | 185.80 | 328.68 | 538.54 | 855.49 | 167.0 | 83.5 | 1.4 | 100.9 | 227.6 | 4 connections |
| 512 | default | 6028 | 0 | 1278.50 steps/s | 3774.85 | 6223.88 | 383.48 | 610.87 | 806.21 | 1015.63 | 184.7 | 92.3 | 1.5 | 106.5 | 250.8 | 4 connections |
| 1024 | default | 6185 | 0 | 1283.23 steps/s | 7587.84 | 12265.39 | 798.63 | 1259.01 | 1726.22 | 2061.43 | 188.0 | 94.0 | 1.6 | 110.7 | 281.5 | 4 connections |

## sequential-steps-async

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 32 | default | 6377 | 0 | 1413.02 steps/s | 216.44 | 480.95 | 10.57 | 26.07 | 46.99 | 147.77 | 184.0 | 92.0 | 1.7 | 91.7 | 212.6 | 4 connections |
| 128 | default | 6460 | 0 | 1422.12 steps/s | 863.38 | 1614.14 | 84.35 | 138.88 | 207.20 | 292.67 | 188.4 | 94.2 | 1.7 | 94.6 | 242.2 | 4 connections |
| 512 | default | 6502 | 0 | 1390.53 steps/s | 3500.95 | 5820.76 | 388.16 | 588.16 | 762.13 | 976.46 | 188.9 | 94.5 | 1.7 | 89.9 | 283.1 | 4 connections |

## start-complete

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | default | 36356 | 0 | 803.83 flows/s | 299.35 | 446.87 | 594.38 | 758.66 | 184.4 | 92.2 | 1.4 | 108.3 | 279.9 | 4 connections |
| 512 | default | 37080 | 0 | 817.98 flows/s | 602.99 | 895.11 | 1122.36 | 1268.91 | 187.4 | 93.7 | 1.4 | 101.9 | 303.3 | 4 connections |
| 1024 | default | 36402 | 0 | 797.86 flows/s | 1306.66 | 1865.07 | 2139.40 | 2314.17 | 187.5 | 93.8 | 1.9 | 105.0 | 330.6 | 4 connections |

