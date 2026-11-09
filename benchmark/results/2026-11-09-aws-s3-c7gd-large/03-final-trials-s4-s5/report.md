Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## attribute-read-direct

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 3 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 137156 | 0 | 3047.89 reads/s | 0.93 | 1.22 | 1.67 | 4.48 | 141.7 | 70.9 | 6.8 | 0.3 | 54.8 | trial 1; 4 connections |
| 3 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 137605 | 0 | 3057.83 reads/s | 0.93 | 1.22 | 1.67 | 3.68 | 143.2 | 71.6 | 6.5 | 0.3 | 55.0 | trial 2; 4 connections |
| 3 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 139921 | 0 | 3109.31 reads/s | 0.91 | 1.20 | 1.66 | 3.57 | 139.1 | 69.5 | 6.7 | 0.3 | 53.0 | trial 3; 4 connections |
| 256 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 260203 | 0 | 5776.89 reads/s | 44.33 | 61.04 | 74.48 | 103.91 | 195.8 | 97.9 | 13.9 | 0.3 | 270.1 | trial 1; 4 connections |
| 256 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 265252 | 0 | 5891.34 reads/s | 43.40 | 59.93 | 74.21 | 103.31 | 195.4 | 97.7 | 15.0 | 0.3 | 264.1 | trial 2; 4 connections |
| 256 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 265922 | 0 | 5905.03 reads/s | 43.31 | 59.77 | 74.76 | 96.92 | 196.1 | 98.0 | 14.0 | 0.3 | 260.2 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 3 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 3 | 3057.83 reads/s | 3109.31 reads/s | 1.67 |
| 256 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 3 | 5891.34 reads/s | 5905.03 reads/s | 74.48 |

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 42826 | 0 | 950.33 rpcs/s | 64.66 | 88.28 | 115.16 | 315.61 | 178.9 | 89.4 | 0.9 | 112.5 | 137.5 | trial 1; 4 connections |
| 64 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 41347 | 0 | 917.48 rpcs/s | 66.40 | 90.94 | 151.05 | 286.64 | 174.2 | 87.1 | 0.9 | 109.0 | 136.1 | trial 2; 4 connections |
| 64 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 43271 | 0 | 960.05 rpcs/s | 64.27 | 88.93 | 116.37 | 156.79 | 183.3 | 91.7 | 0.9 | 113.5 | 138.0 | trial 3; 4 connections |
| 960 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 94256 | 0 | 2078.45 rpcs/s | 445.25 | 632.65 | 766.82 | 1016.16 | 188.4 | 94.2 | 2.3 | 51.1 | 321.5 | trial 1; 4 connections |
| 960 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 97500 | 0 | 2150.73 rpcs/s | 430.47 | 600.92 | 770.45 | 1017.70 | 191.2 | 95.6 | 2.4 | 52.4 | 333.2 | trial 2; 4 connections |
| 960 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 97557 | 0 | 2149.06 rpcs/s | 434.72 | 607.53 | 748.80 | 995.05 | 191.6 | 95.8 | 2.4 | 54.4 | 321.8 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 64 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 3 | 950.33 rpcs/s | 960.05 rpcs/s | 116.37 |
| 960 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 3 | 2149.06 rpcs/s | 2150.73 rpcs/s | 766.82 |

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 4 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 340967 | 0 | 7576.99 rpcs/s | 0.52 | 0.61 | 0.83 | 3.14 | 176.6 | 88.3 | 5.4 | 0.3 | 45.6 | trial 1; 4 connections |
| 4 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 336960 | 0 | 7487.94 rpcs/s | 0.52 | 0.61 | 0.83 | 4.19 | 178.0 | 89.0 | 5.4 | 0.3 | 44.7 | trial 2; 4 connections |
| 4 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 337131 | 0 | 7491.73 rpcs/s | 0.52 | 0.61 | 0.84 | 5.48 | 172.6 | 86.3 | 5.4 | 0.3 | 45.2 | trial 3; 4 connections |
| 256 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 626610 | 0 | 13920.83 rpcs/s | 18.44 | 24.73 | 29.69 | 39.56 | 192.6 | 96.3 | 10.5 | 0.3 | 76.0 | trial 1; 4 connections |
| 256 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 626796 | 0 | 13925.80 rpcs/s | 18.45 | 24.67 | 29.60 | 40.40 | 192.7 | 96.3 | 9.8 | 0.3 | 76.8 | trial 2; 4 connections |
| 256 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 634323 | 0 | 14093.00 rpcs/s | 18.19 | 24.40 | 29.47 | 38.50 | 192.7 | 96.3 | 10.9 | 0.3 | 75.7 | trial 3; 4 connections |

| Concurrency | Settings | Trials | Median throughput | Max throughput | Median p99 ms |
| ---: | --- | ---: | ---: | ---: | ---: |
| 4 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 3 | 7491.73 rpcs/s | 7576.99 rpcs/s | 0.83 |
| 256 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 3 | 13925.80 rpcs/s | 14093.00 rpcs/s | 29.60 |

