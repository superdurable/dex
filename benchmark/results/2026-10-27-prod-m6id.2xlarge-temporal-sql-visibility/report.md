Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. CPU % is 100 per fully used core. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## async-steps

| Concurrency | Target | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 6037 | 0 | 335.35 steps/s | 29.21 | 44.80 | 1.33 | 1.47 | 2.10 | 11.83 | 172.8 | 653.2 |
| 8 | temporal | 16573 | 0 | 910.26 steps/s | 81.64 | 199.26 | 1.96 | 2.87 | 5.36 | 36.34 | 470.8 | 1014.7 |
| 32 | temporal | 25887 | 0 | 1437.11 steps/s | 213.96 | 405.23 | 4.74 | 10.67 | 24.82 | 125.51 | 699.5 | 1334.4 |
| 128 | temporal | 25860 | 0 | 1400.41 steps/s | 667.11 | 2615.16 | 6.54 | 47.68 | 199.93 | 871.26 | 694.3 | 1406.0 |

## attribute-read-direct

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 50311 | 0 | 279.50 reads/s | 3.46 | 3.83 | 5.03 | 12.18 | 183.6 | 295.9 |
| 8 | temporal | 71529 | 0 | 396.04 reads/s | 7.11 | 9.86 | 893.84 | 10109.70 | 237.5 | 318.4 |
| 32 | temporal | 70413 | 0 | 388.73 reads/s | 31.59 | 40.37 | 983.25 | 10114.84 | 236.5 | 346.5 |
| 128 | temporal | 72085 | 0 | 399.36 reads/s | 132.83 | 1009.36 | 1088.49 | 1143.22 | 241.7 | 403.7 |

## attribute-read-rpc

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 35724 | 0 | 198.46 reads/s | 4.92 | 5.25 | 7.16 | 13.35 | 175.4 | 291.3 |
| 8 | temporal | 72206 | 0 | 400.01 reads/s | 8.95 | 12.70 | 869.72 | 1865.50 | 311.5 | 308.3 |
| 32 | temporal | 69893 | 0 | 388.24 reads/s | 32.32 | 45.57 | 983.18 | 10136.24 | 285.4 | 336.5 |
| 128 | temporal | 71773 | 0 | 378.45 reads/s | 140.69 | 1028.58 | 1096.82 | 10121.02 | 266.9 | 395.2 |

## attribute-write-local

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 37489 | 0 | 208.27 writes/s | 2.68 | 3.97 | 6.36 | 485.75 | 101.6 | 406.6 |
| 8 | temporal | 96178 | 0 | 533.38 writes/s | 10.48 | 17.06 | 34.03 | 502.98 | 568.1 | 555.1 |
| 32 | temporal | 106863 | 0 | 592.56 writes/s | 46.45 | 92.33 | 125.10 | 574.53 | 627.7 | 626.8 |
| 128 | temporal | 125483 | 177374 | 695.23 writes/s | 130.34 | 312.95 | 407.12 | 9365.41 | 661.5 | 705.1 |

## attribute-write-sync

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 36158 | 0 | 200.88 writes/s | 2.69 | 4.48 | 6.38 | 485.14 | 112.1 | 402.6 |
| 8 | temporal | 86155 | 0 | 477.76 writes/s | 10.37 | 23.73 | 41.79 | 503.12 | 600.2 | 533.1 |
| 32 | temporal | 93522 | 0 | 518.65 writes/s | 48.70 | 97.33 | 180.40 | 607.98 | 656.7 | 598.1 |
| 128 | temporal | 119492 | 149731 | 662.37 writes/s | 126.67 | 326.84 | 391.55 | 9590.84 | 674.8 | 709.7 |

## channel-wake

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 1555 | 0 | 8.64 wakes/s | 12.71 | 96.50 | 97.24 | 98.05 | 86.3 | 489.3 |
| 8 | temporal | 5025 | 0 | 27.87 wakes/s | 94.25 | 98.20 | 104.04 | 134.06 | 275.6 | 694.1 |
| 32 | temporal | 11716 | 0 | 64.94 wakes/s | 125.69 | 199.22 | 261.13 | 308.83 | 628.5 | 1040.1 |
| 128 | temporal | 11879 | 0 | 64.08 wakes/s | 170.07 | 273.03 | 344.56 | 483.45 | 639.4 | 1107.4 |

## rpc-locking

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 17299 | 0 | 96.10 rpcs/s | 9.37 | 9.91 | 81.89 | 121.75 | 161.0 | 435.9 |
| 8 | temporal | 72417 | 0 | 402.08 rpcs/s | 17.36 | 22.92 | 123.37 | 404.27 | 647.1 | 606.6 |
| 32 | temporal | 82458 | 0 | 457.46 rpcs/s | 64.64 | 87.74 | 164.76 | 895.89 | 739.0 | 686.6 |
| 128 | temporal | 81686 | 7 | 389.25 rpcs/s | 272.80 | 331.21 | 425.22 | 5207.91 | 642.1 | 767.4 |

## rpc-plain

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 38494 | 0 | 213.85 rpcs/s | 4.60 | 4.77 | 6.34 | 15.50 | 183.4 | 290.4 |
| 8 | temporal | 70764 | 0 | 392.08 rpcs/s | 7.72 | 10.73 | 865.57 | 10117.78 | 310.1 | 303.1 |
| 32 | temporal | 68438 | 0 | 380.01 rpcs/s | 31.47 | 41.95 | 979.50 | 5017.77 | 285.7 | 339.0 |
| 128 | temporal | 69359 | 0 | 384.30 rpcs/s | 139.55 | 1026.58 | 1091.31 | 10133.22 | 287.4 | 395.5 |

## sequential-steps

| Concurrency | Target | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 347 | 0 | 19.27 steps/s | 150.76 | 1051.14 | 13.18 | 100.38 | 101.11 | 110.79 | 51.1 | 379.3 |
| 8 | temporal | 1444 | 0 | 79.77 steps/s | 1042.52 | 1068.81 | 99.94 | 103.22 | 107.90 | 120.30 | 205.6 | 553.9 |
| 32 | temporal | 4152 | 0 | 229.33 steps/s | 1292.71 | 2239.76 | 115.03 | 194.66 | 239.37 | 330.07 | 594.7 | 707.4 |
| 128 | temporal | 4408 | 0 | 238.53 steps/s | 5626.93 | 9307.23 | 199.30 | 324.11 | 435.99 | 633.97 | 617.5 | 748.8 |

## start-complete

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 3235 | 0 | 17.97 flows/s | 28.46 | 143.56 | 151.38 | 164.86 | 100.2 | 590.7 |
| 8 | temporal | 9562 | 0 | 53.10 flows/s | 150.04 | 162.07 | 250.97 | 443.54 | 305.2 | 839.4 |
| 32 | temporal | 20913 | 0 | 116.03 flows/s | 264.30 | 395.80 | 507.57 | 688.05 | 656.4 | 1478.6 |
| 128 | temporal | 21350 | 0 | 116.78 flows/s | 987.09 | 2076.23 | 2691.32 | 3546.43 | 660.8 | 1410.3 |
