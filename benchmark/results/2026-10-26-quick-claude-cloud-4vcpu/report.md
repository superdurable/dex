Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. CPU % is 100 per fully used core. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## async-steps

| Concurrency | Target | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 600 | 0 | 99.95 steps/s | 99.85 | 124.87 | 2.61 | 3.30 | 5.03 | 235.41 | 78.9 | 328.5 |
| 1 | zd | 142 | 0 | 23.50 steps/s | 440.20 | 456.06 | 44.00 | 44.38 | 48.16 | 88.02 | 14.7 | 114.1 |
| 1 | zd / temporal | — | — | 0.24x | — | — | 16.86x | 13.45x | 9.57x | 0.37x | — | — |
| 8 | temporal | 1723 | 0 | 286.37 steps/s | 276.48 | 390.22 | 3.14 | 5.22 | 8.89 | 69.95 | 176.6 | 405.2 |
| 8 | zd | 345 | 0 | 56.65 steps/s | 1412.54 | 2223.27 | 131.51 | 307.96 | 535.95 | 809.92 | 20.2 | 123.6 |
| 8 | zd / temporal | — | — | 0.20x | — | — | 41.84x | 58.98x | 60.28x | 11.58x | — | — |
| 32 | temporal | 1620 | 0 | 267.54 steps/s | 781.22 | 3457.36 | 3.30 | 5.37 | 9.37 | 54.92 | 173.5 | 399.2 |
| 32 | zd | 358 | 0 | 56.95 steps/s | 5227.54 | 10923.46 | 400.23 | 1377.14 | 2847.86 | 5015.24 | 21.6 | 125.0 |
| 32 | zd / temporal | — | — | 0.21x | — | — | 121.46x | 256.47x | 303.94x | 91.31x | — | — |

## attribute-read-direct

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 10257 | 0 | 170.94 reads/s | 5.63 | 6.76 | 9.49 | 50.10 | 169.8 | 290.6 |
| 1 | zd | 7503 | 0 | 125.04 reads/s | 1.43 | 43.33 | 45.67 | 87.85 | 9.6 | 100.3 |
| 1 | zd / temporal | — | — | 0.73x | 0.25x | 6.41x | 4.81x | 1.75x | — | — |
| 8 | temporal | 24081 | 0 | 399.95 reads/s | 11.99 | 20.47 | 365.07 | 1015.58 | 241.3 | 309.3 |
| 8 | zd | 125614 | 0 | 2093.43 reads/s | 2.94 | 4.53 | 45.33 | 92.04 | 97.4 | 121.6 |
| 8 | zd / temporal | — | — | 5.23x | 0.25x | 0.22x | 0.12x | 0.09x | — | — |
| 32 | temporal | 23091 | 0 | 380.77 reads/s | 26.58 | 166.80 | 959.29 | 5762.63 | 239.6 | 353.5 |
| 32 | zd | 196280 | 0 | 3271.10 reads/s | 9.36 | 13.69 | 19.29 | 64.45 | 122.9 | 149.7 |
| 32 | zd / temporal | — | — | 8.59x | 0.35x | 0.08x | 0.02x | 0.01x | — | — |

## attribute-read-rpc

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 7419 | 0 | 123.63 reads/s | 7.79 | 9.51 | 13.13 | 50.34 | 170.2 | 284.8 |
| 1 | zd | 1408 | 0 | 23.46 reads/s | 43.98 | 45.21 | 86.86 | 90.60 | 3.4 | 99.2 |
| 1 | zd / temporal | — | — | 0.19x | 5.65x | 4.76x | 6.62x | 1.80x | — | — |
| 8 | temporal | 24454 | 0 | 407.41 reads/s | 18.19 | 27.00 | 38.41 | 1023.20 | 269.0 | 306.7 |
| 8 | zd | 6941 | 0 | 115.48 reads/s | 44.34 | 131.69 | 219.47 | 311.94 | 8.9 | 121.5 |
| 8 | zd / temporal | — | — | 0.28x | 2.44x | 4.88x | 5.71x | 0.30x | — | — |
| 32 | temporal | 24007 | 0 | 396.78 reads/s | 47.22 | 120.38 | 930.53 | 4616.13 | 243.3 | 329.1 |
| 32 | zd | 7098 | 0 | 117.51 reads/s | 197.59 | 586.74 | 1130.88 | 2156.00 | 10.6 | 141.9 |
| 32 | zd / temporal | — | — | 0.30x | 4.18x | 4.87x | 1.22x | 0.47x | — | — |

## attribute-write-local

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 5165 | 0 | 85.88 writes/s | 7.27 | 9.31 | 170.97 | 493.94 | 109.2 | 321.9 |
| 1 | zd | 1494 | 0 | 24.88 writes/s | 43.97 | 44.17 | 47.90 | 50.88 | 15.1 | 114.7 |
| 1 | zd / temporal | — | — | 0.29x | 6.05x | 4.74x | 0.28x | 0.10x | — | — |
| 8 | temporal | 8120 | 0 | 135.21 writes/s | 54.20 | 64.28 | 171.46 | 542.08 | 156.5 | 344.1 |
| 8 | zd | 11001 | 0 | 183.22 writes/s | 44.00 | 44.28 | 51.94 | 58.67 | 66.6 | 152.5 |
| 8 | zd / temporal | — | — | 1.36x | 0.81x | 0.69x | 0.30x | 0.11x | — | — |
| 32 | temporal | 8083 | 0 | 134.06 writes/s | 227.39 | 282.31 | 656.37 | 951.41 | 162.4 | 373.5 |
| 32 | zd | 46543 | 0 | 775.29 writes/s | 43.92 | 55.05 | 67.29 | 92.26 | 209.2 | 221.1 |
| 32 | zd / temporal | — | — | 5.78x | 0.19x | 0.20x | 0.10x | 0.10x | — | — |

## channel-wake

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 299 | 0 | 4.98 wakes/s | 51.00 | 53.86 | 66.23 | 80.86 | 59.8 | 318.9 |
| 1 | zd | 677 | 0 | 11.28 wakes/s | 37.50 | 39.61 | 41.77 | 56.86 | 23.0 | 125.2 |
| 1 | zd / temporal | — | — | 2.26x | 0.74x | 0.74x | 0.63x | 0.70x | — | — |
| 8 | temporal | 784 | 0 | 12.99 wakes/s | 134.23 | 171.58 | 207.93 | 289.64 | 136.2 | 362.5 |
| 8 | zd | 1887 | 0 | 31.37 wakes/s | 79.49 | 92.16 | 176.22 | 297.22 | 46.6 | 159.2 |
| 8 | zd / temporal | — | — | 2.41x | 0.59x | 0.54x | 0.85x | 1.03x | — | — |
| 32 | temporal | 755 | 0 | 12.26 wakes/s | 480.14 | 572.30 | 655.83 | 709.76 | 134.0 | 367.5 |
| 32 | zd | 2165 | 0 | 35.68 wakes/s | 132.18 | 440.21 | 1147.87 | 1995.01 | 50.7 | 167.5 |
| 32 | zd / temporal | — | — | 2.91x | 0.28x | 0.77x | 1.75x | 2.81x | — | — |

## recovery

| Target log MiB | Replayed MiB | Target | Restart to serving s | Server-reported replay s | Errors |
| ---: | ---: | --- | ---: | ---: | ---: |
| 10 | 10.06 | zd | 1.437 | 1.285 | 0 |
| 50 | 50.14 | zd | 8.464 | 8.250 | 0 |
| 100 | 100.27 | zd | 17.468 | 17.234 | 0 |

## rpc-locking

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 3119 | 0 | 51.98 rpcs/s | 17.43 | 20.62 | 81.10 | 222.93 | 132.2 | 325.2 |
| 1 | zd | 1360 | 0 | 22.65 rpcs/s | 44.06 | 46.76 | 49.41 | 88.41 | 15.1 | 112.6 |
| 1 | zd / temporal | — | — | 0.44x | 2.53x | 2.27x | 0.61x | 0.40x | — | — |
| 8 | temporal | 7011 | 0 | 116.56 rpcs/s | 64.58 | 78.16 | 178.00 | 567.54 | 217.0 | 361.1 |
| 8 | zd | 2967 | 0 | 49.31 rpcs/s | 132.05 | 307.25 | 566.01 | 842.87 | 24.9 | 121.1 |
| 8 | zd / temporal | — | — | 0.42x | 2.04x | 3.93x | 3.18x | 1.49x | — | — |
| 32 | temporal | 7358 | 0 | 122.33 rpcs/s | 244.94 | 278.32 | 452.47 | 2498.08 | 216.7 | 378.4 |
| 32 | zd | 3005 | 0 | 49.56 rpcs/s | 441.03 | 1431.75 | 3045.17 | 5027.79 | 23.7 | 120.8 |
| 32 | zd / temporal | — | — | 0.41x | 1.80x | 5.14x | 6.73x | 2.01x | — | — |

## rpc-plain

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 8092 | 0 | 134.86 rpcs/s | 7.15 | 8.55 | 12.02 | 46.35 | 178.0 | 284.1 |
| 1 | zd | 1405 | 0 | 23.41 rpcs/s | 43.98 | 44.39 | 48.03 | 89.80 | 3.1 | 93.8 |
| 1 | zd / temporal | — | — | 0.17x | 6.15x | 5.19x | 4.00x | 1.94x | — | — |
| 8 | temporal | 24298 | 0 | 402.63 rpcs/s | 13.96 | 21.89 | 77.46 | 1018.57 | 268.6 | 300.2 |
| 8 | zd | 2820 | 0 | 46.93 rpcs/s | 132.15 | 308.71 | 573.80 | 1155.86 | 4.1 | 98.2 |
| 8 | zd / temporal | — | — | 0.12x | 9.46x | 14.11x | 7.41x | 1.13x | — | — |
| 32 | temporal | 23013 | 0 | 383.25 rpcs/s | 40.13 | 119.12 | 984.00 | 2984.18 | 256.2 | 328.7 |
| 32 | zd | 2731 | 0 | 45.10 rpcs/s | 485.15 | 1631.95 | 3088.77 | 5066.69 | 4.8 | 98.1 |
| 32 | zd / temporal | — | — | 0.12x | 12.09x | 13.70x | 3.14x | 1.70x | — | — |

## sequential-steps

| Concurrency | Target | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 109 | 0 | 18.12 steps/s | 550.02 | 599.03 | 49.86 | 53.58 | 62.13 | 117.33 | 56.0 | 310.5 |
| 1 | zd | 136 | 0 | 22.63 steps/s | 440.17 | 485.04 | 44.01 | 44.20 | 48.06 | 118.91 | 20.0 | 118.4 |
| 1 | zd / temporal | — | — | 1.25x | — | — | 0.88x | 0.82x | 0.77x | 1.01x | — | — |
| 8 | temporal | 362 | 0 | 59.60 steps/s | 1333.94 | 1553.31 | 110.74 | 149.75 | 187.74 | 307.77 | 144.9 | 353.4 |
| 8 | zd | 337 | 0 | 55.43 steps/s | 1408.57 | 2287.26 | 131.73 | 307.64 | 571.70 | 880.14 | 33.8 | 138.3 |
| 8 | zd / temporal | — | — | 0.93x | — | — | 1.19x | 2.05x | 3.05x | 2.86x | — | — |
| 32 | temporal | 363 | 0 | 57.43 steps/s | 5305.16 | 8520.35 | 432.24 | 515.17 | 614.05 | 674.54 | 142.9 | 362.5 |
| 32 | zd | 340 | 0 | 53.95 steps/s | 5728.70 | 10548.66 | 441.94 | 1415.03 | 2912.48 | 5047.11 | 33.6 | 143.1 |
| 32 | zd / temporal | — | — | 0.94x | — | — | 1.02x | 2.75x | 4.74x | 7.48x | — | — |

## start-complete

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 591 | 0 | 9.83 flows/s | 100.12 | 105.09 | 148.63 | 171.69 | 72.4 | 330.7 |
| 1 | zd | 802 | 0 | 13.36 flows/s | 87.95 | 88.19 | 92.15 | 112.18 | 15.3 | 117.0 |
| 1 | zd / temporal | — | — | 1.36x | 0.88x | 0.84x | 0.62x | 0.65x | — | — |
| 8 | temporal | 1156 | 0 | 19.20 flows/s | 413.26 | 476.82 | 553.85 | 648.88 | 122.1 | 369.5 |
| 8 | zd | 2918 | 0 | 48.54 flows/s | 132.04 | 307.91 | 584.08 | 1080.07 | 40.1 | 150.5 |
| 8 | zd / temporal | — | — | 2.53x | 0.32x | 0.65x | 1.05x | 1.66x | — | — |
| 32 | temporal | 1093 | 0 | 17.90 flows/s | 1455.55 | 2969.77 | 3701.40 | 4134.03 | 122.6 | 381.2 |
| 32 | zd | 2822 | 0 | 46.54 flows/s | 483.85 | 1567.32 | 2959.56 | 6276.27 | 40.7 | 154.2 |
| 32 | zd / temporal | — | — | 2.60x | 0.33x | 0.53x | 0.80x | 1.52x | — | — |
