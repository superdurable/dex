Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. CPU % is 100 per fully used core. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## attribute-read-direct

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 10866 | 0 | 181.08 reads/s | 5.35 | 6.33 | 9.08 | 23.96 | 170.9 | 290.2 |
| 4 | temporal | 24353 | 0 | 401.22 reads/s | 6.55 | 10.14 | 17.98 | 1011.29 | 235.6 | 299.8 |
| 8 | temporal | 24131 | 0 | 399.48 reads/s | 11.30 | 19.25 | 405.09 | 1011.63 | 233.8 | 310.8 |
| 16 | temporal | 24147 | 0 | 402.29 reads/s | 21.67 | 43.25 | 793.92 | 1545.80 | 208.2 | 331.2 |
| 32 | temporal | 22757 | 0 | 378.53 reads/s | 27.43 | 153.64 | 985.50 | 5266.87 | 217.8 | 362.8 |
| 64 | temporal | 23790 | 0 | 395.43 reads/s | 36.20 | 305.74 | 2372.08 | 11328.07 | 231.3 | 385.0 |

## attribute-read-rpc

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 8193 | 0 | 136.55 reads/s | 6.97 | 8.48 | 12.41 | 145.50 | 168.1 | 285.0 |
| 4 | temporal | 20941 | 0 | 348.96 reads/s | 10.75 | 15.66 | 23.05 | 76.97 | 244.9 | 295.8 |
| 8 | temporal | 23911 | 0 | 397.99 reads/s | 15.99 | 24.28 | 36.64 | 1010.38 | 244.3 | 303.4 |
| 16 | temporal | 23683 | 0 | 394.62 reads/s | 28.80 | 49.56 | 464.73 | 1204.70 | 241.3 | 315.0 |
| 32 | temporal | 24068 | 0 | 400.88 reads/s | 44.73 | 113.82 | 937.57 | 1864.12 | 223.1 | 329.4 |
| 64 | temporal | 23421 | 0 | 388.45 reads/s | 74.39 | 337.68 | 1544.41 | 6466.80 | 253.1 | 357.9 |

## attribute-write-local

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 5938 | 0 | 98.96 writes/s | 6.18 | 7.78 | 16.33 | 489.20 | 104.8 | 322.5 |
| 4 | temporal | 9208 | 0 | 153.31 writes/s | 21.96 | 29.28 | 65.03 | 505.09 | 160.7 | 343.1 |
| 8 | temporal | 9469 | 0 | 157.70 writes/s | 45.98 | 55.50 | 193.72 | 535.44 | 162.6 | 351.9 |
| 16 | temporal | 9138 | 0 | 151.72 writes/s | 97.32 | 118.58 | 463.58 | 703.12 | 162.3 | 356.2 |
| 32 | temporal | 8592 | 0 | 142.77 writes/s | 210.59 | 267.29 | 624.04 | 963.89 | 163.3 | 375.4 |
| 64 | temporal | 9967 | 0 | 163.49 writes/s | 416.83 | 473.89 | 664.50 | 1585.20 | 161.7 | 398.3 |

## channel-wake

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 300 | 0 | 5.00 wakes/s | 50.80 | 53.54 | 55.96 | 72.43 | 51.6 | 321.7 |
| 4 | temporal | 752 | 0 | 12.49 wakes/s | 81.14 | 99.53 | 122.32 | 207.43 | 121.3 | 367.1 |
| 8 | temporal | 868 | 0 | 14.41 wakes/s | 117.94 | 151.71 | 177.55 | 225.28 | 141.0 | 371.6 |
| 16 | temporal | 843 | 0 | 13.91 wakes/s | 266.58 | 306.41 | 350.03 | 454.20 | 142.3 | 380.1 |
| 32 | temporal | 902 | 0 | 14.71 wakes/s | 401.84 | 478.37 | 546.87 | 578.64 | 138.4 | 389.1 |
| 64 | temporal | 876 | 0 | 12.22 wakes/s | 464.95 | 543.27 | 620.00 | 795.48 | 121.1 | 385.8 |

## rpc-locking

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 3509 | 0 | 58.47 rpcs/s | 15.24 | 18.19 | 60.06 | 194.26 | 134.0 | 330.6 |
| 4 | temporal | 7705 | 0 | 128.18 rpcs/s | 28.36 | 37.10 | 160.51 | 379.78 | 210.5 | 357.8 |
| 8 | temporal | 7992 | 0 | 133.10 rpcs/s | 55.81 | 69.10 | 249.06 | 671.95 | 218.6 | 365.7 |
| 16 | temporal | 8195 | 0 | 136.42 rpcs/s | 110.17 | 128.83 | 315.71 | 1014.02 | 222.7 | 376.2 |
| 32 | temporal | 7892 | 0 | 131.22 rpcs/s | 222.11 | 267.84 | 1078.57 | 2503.89 | 222.5 | 400.4 |
| 64 | temporal | 8057 | 9 | 133.63 rpcs/s | 443.97 | 484.89 | 1107.88 | 5543.04 | 223.1 | 408.3 |

## rpc-plain

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 8904 | 0 | 148.40 rpcs/s | 6.53 | 7.75 | 11.09 | 28.18 | 179.1 | 284.5 |
| 4 | temporal | 24514 | 0 | 408.52 rpcs/s | 8.78 | 12.30 | 19.31 | 985.64 | 277.0 | 296.1 |
| 8 | temporal | 24055 | 0 | 399.99 rpcs/s | 11.54 | 19.38 | 393.59 | 1397.36 | 252.7 | 304.8 |
| 16 | temporal | 23813 | 0 | 396.16 rpcs/s | 23.30 | 41.99 | 709.82 | 1304.15 | 238.3 | 316.2 |
| 32 | temporal | 23961 | 0 | 399.14 rpcs/s | 33.98 | 101.03 | 1001.99 | 5013.13 | 216.7 | 328.1 |
| 64 | temporal | 23331 | 0 | 388.15 rpcs/s | 64.09 | 346.51 | 1808.91 | 11103.92 | 268.9 | 359.3 |

## sequential-steps

| Concurrency | Target | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 109 | 0 | 18.11 steps/s | 550.55 | 600.44 | 49.82 | 52.69 | 60.44 | 120.90 | 51.1 | 306.3 |
| 4 | temporal | 345 | 0 | 57.07 steps/s | 695.16 | 892.27 | 54.05 | 91.69 | 108.98 | 198.73 | 135.0 | 351.0 |
| 8 | temporal | 396 | 0 | 65.14 steps/s | 1211.02 | 1453.71 | 104.13 | 139.59 | 177.78 | 298.69 | 148.9 | 358.6 |
| 16 | temporal | 418 | 0 | 68.13 steps/s | 2339.81 | 2650.84 | 200.13 | 253.33 | 316.65 | 387.15 | 149.1 | 367.4 |
| 32 | temporal | 393 | 0 | 62.63 steps/s | 4972.06 | 7352.09 | 402.30 | 479.41 | 563.04 | 664.81 | 146.5 | 389.7 |
| 64 | temporal | 408 | 0 | 60.29 steps/s | 10228.93 | 18572.07 | 462.45 | 540.62 | 604.82 | 841.42 | 143.9 | 388.5 |
