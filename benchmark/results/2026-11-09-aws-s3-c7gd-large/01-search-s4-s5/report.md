Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## attribute-read-direct

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 2 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 95328 | 0 | 2118.37 reads/s | 0.90 | 1.14 | 1.53 | 3.53 | 98.1 | 49.0 | 4.7 | 0.3 | 51.5 | 4 connections |
| 16 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 212534 | 0 | 4722.63 reads/s | 3.23 | 4.69 | 6.13 | 12.21 | 192.9 | 96.4 | 8.5 | 0.3 | 87.9 | 4 connections |
| 128 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 235098 | 0 | 5222.30 reads/s | 23.95 | 33.95 | 46.21 | 85.41 | 193.3 | 96.7 | 11.6 | 0.3 | 193.8 | 4 connections |
| 256 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 257348 | 0 | 5714.53 reads/s | 44.23 | 61.99 | 83.00 | 143.41 | 192.6 | 96.3 | 14.1 | 0.3 | 265.7 | 4 connections |
| 512 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 231653 | 0 | 5139.93 reads/s | 98.89 | 134.08 | 176.59 | 289.67 | 190.7 | 95.4 | 14.9 | 0.3 | 383.2 | 4 connections |
| 1024 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 222847 | 0 | 4938.04 reads/s | 204.73 | 259.57 | 343.84 | 578.94 | 192.6 | 96.3 | 17.1 | 0.3 | 638.0 | 4 connections |

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 128 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 52312 | 0 | 1160.56 rpcs/s | 106.38 | 149.85 | 216.67 | 391.17 | 184.3 | 92.2 | 1.2 | 92.3 | 165.3 | 4 connections |
| 256 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 66074 | 0 | 1464.31 rpcs/s | 167.92 | 237.08 | 352.96 | 525.03 | 184.3 | 92.1 | 1.7 | 76.9 | 229.8 | 4 connections |
| 384 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 76086 | 0 | 1683.81 rpcs/s | 220.23 | 306.28 | 410.52 | 587.37 | 188.4 | 94.2 | 1.8 | 70.9 | 245.9 | 4 connections |
| 512 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 80205 | 0 | 1773.34 rpcs/s | 277.60 | 392.32 | 517.00 | 865.65 | 186.2 | 93.1 | 2.0 | 65.3 | 254.7 | 4 connections |
| 768 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 87480 | 0 | 1929.62 rpcs/s | 384.01 | 557.26 | 729.86 | 997.42 | 184.8 | 92.4 | 2.1 | 55.2 | 287.7 | 4 connections |
| 960 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 90875 | 0 | 2002.26 rpcs/s | 463.94 | 661.26 | 844.68 | 1138.33 | 185.1 | 92.5 | 2.2 | 50.8 | 325.9 | 4 connections |

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 4 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 310975 | 0 | 6910.52 rpcs/s | 0.56 | 0.68 | 0.98 | 4.60 | 175.8 | 87.9 | 5.0 | 0.3 | 45.2 | 4 connections |
| 16 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 482834 | 0 | 10729.48 rpcs/s | 1.46 | 1.93 | 2.55 | 10.12 | 190.2 | 95.1 | 6.4 | 0.3 | 47.1 | 4 connections |
| 32 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 502851 | 0 | 11174.09 rpcs/s | 2.79 | 3.83 | 5.05 | 11.94 | 189.9 | 95.0 | 6.7 | 0.3 | 50.3 | 4 connections |
| 64 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 556248 | 0 | 12360.19 rpcs/s | 5.04 | 7.01 | 9.18 | 16.56 | 188.6 | 94.3 | 7.7 | 0.3 | 54.9 | 4 connections |
| 128 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 573963 | 0 | 12752.83 rpcs/s | 9.91 | 13.58 | 17.68 | 33.66 | 189.0 | 94.5 | 8.4 | 0.3 | 62.6 | 4 connections |
| 256 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 590320 | 0 | 13114.94 rpcs/s | 19.30 | 26.66 | 34.99 | 58.29 | 188.1 | 94.1 | 10.2 | 0.3 | 76.0 | 4 connections |
| 512 | EXTRA=history.offloadEnabled=true,history.deleteExpiredObjects=true,history.windowEvents=50000 | 582486 | 0 | 12937.26 rpcs/s | 39.09 | 52.73 | 68.19 | 112.24 | 186.4 | 93.2 | 10.4 | 0.3 | 107.8 | 4 connections |

