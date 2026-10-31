Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## attribute-read-direct

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | default | 225364 | 0 | 5006.94 reads/s | 12.72 | 17.10 | 21.18 | 28.62 | 196.6 | 98.3 | 8.7 | 0.3 | 101.2 | 4 connections |
| 128 | default | 248288 | 0 | 5515.58 reads/s | 22.87 | 31.97 | 40.36 | 59.36 | 195.2 | 97.6 | 9.1 | 0.3 | 133.3 | 4 connections |
| 256 | default | 262100 | 0 | 5820.19 reads/s | 43.92 | 60.75 | 75.89 | 103.97 | 195.4 | 97.7 | 9.6 | 0.3 | 198.7 | 4 connections |

## channel-wake

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | default | 8339 | 0 | 184.37 wakes/s | 105.76 | 155.90 | 258.26 | 416.54 | 105.0 | 52.5 | 1.5 | 108.9 | 147.3 | 4 connections |

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 128 | default | 50621 | 0 | 1112.98 rpcs/s | 95.97 | 189.03 | 371.73 | 1149.77 | 184.2 | 92.1 | 1.5 | 109.6 | 147.5 | 4 connections |
| 256 | default | 59841 | 0 | 1324.30 rpcs/s | 150.79 | 373.30 | 711.90 | 1828.92 | 191.6 | 95.8 | 1.8 | 110.5 | 165.5 | 4 connections |
| 512 | default | 59733 | 0 | 1316.76 rpcs/s | 283.22 | 836.53 | 1620.96 | 3983.49 | 193.2 | 96.6 | 1.8 | 107.1 | 179.2 | 4 connections |

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 128 | default | 217444 | 0 | 4830.00 rpcs/s | 17.60 | 62.70 | 127.99 | 326.22 | 194.6 | 97.3 | 5.9 | 0.3 | 47.3 | 4 connections |
| 256 | default | 160295 | 0 | 3558.36 rpcs/s | 47.81 | 172.14 | 348.64 | 1028.30 | 193.2 | 96.6 | 4.5 | 0.3 | 52.8 | 4 connections |
| 512 | default | 120173 | 0 | 2663.38 rpcs/s | 128.32 | 460.60 | 920.08 | 2311.43 | 190.5 | 95.3 | 3.5 | 0.3 | 67.7 | 4 connections |

## sequential-steps

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | default | 6133 | 0 | 1318.76 steps/s | 1872.09 | 2746.33 | 166.81 | 272.41 | 512.54 | 1104.30 | 184.6 | 92.3 | 1.5 | 109.0 | 232.9 | 4 connections |

## sequential-steps-async

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 32 | default | 6943 | 0 | 1536.59 steps/s | 204.22 | 373.08 | 8.28 | 25.71 | 54.50 | 153.90 | 190.6 | 95.3 | 1.8 | 99.5 | 221.5 | 4 connections |

## start-complete

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 128 | default | 33246 | 0 | 736.92 flows/s | 166.81 | 214.07 | 317.73 | 455.58 | 157.2 | 78.6 | 1.3 | 115.5 | 250.5 | 4 connections |
| 256 | default | 38657 | 0 | 855.32 flows/s | 279.16 | 410.15 | 649.79 | 1246.27 | 192.2 | 96.1 | 1.4 | 107.9 | 286.7 | 4 connections |
| 512 | default | 36630 | 0 | 806.38 flows/s | 555.22 | 1020.28 | 1749.22 | 3540.82 | 193.1 | 96.5 | 1.4 | 102.5 | 287.9 | 4 connections |

