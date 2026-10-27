Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. CPU % is 100 per fully used core. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## async-steps

| Concurrency | Target | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 32 | zd | 7389 | 0 | 1229.42 steps/s | 249.59 | 490.99 | 17.47 | 58.96 | 121.37 | 276.53 | 239.3 | 314.3 |
| 64 | zd | 6695 | 0 | 1110.52 steps/s | 551.43 | 1141.22 | 41.09 | 142.13 | 290.50 | 740.14 | 242.3 | 297.1 |
| 128 | zd | 5817 | 0 | 959.76 steps/s | 1281.17 | 2605.30 | 97.82 | 339.06 | 695.49 | 1746.96 | 248.4 | 297.4 |

## attribute-read-direct

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 32 | zd | 174188 | 0 | 2902.83 reads/s | 10.63 | 15.56 | 21.80 | 45.99 | 122.6 | 146.5 |
| 64 | zd | 187276 | 0 | 3120.95 reads/s | 20.09 | 28.69 | 40.14 | 87.27 | 120.8 | 162.5 |
| 128 | zd | 177672 | 0 | 2959.94 reads/s | 43.37 | 58.37 | 76.34 | 197.66 | 129.4 | 203.2 |

## attribute-read-rpc

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 32 | zd | 73240 | 0 | 1220.10 reads/s | 17.96 | 59.20 | 123.15 | 658.49 | 85.7 | 137.7 |
| 64 | zd | 64942 | 0 | 1081.30 reads/s | 40.15 | 138.63 | 282.46 | 851.57 | 99.0 | 158.0 |
| 128 | zd | 55929 | 0 | 930.53 reads/s | 92.30 | 324.97 | 667.15 | 1698.81 | 119.2 | 189.1 |

## attribute-write-local

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 32 | zd | 50472 | 0 | 840.77 writes/s | 37.00 | 51.11 | 66.00 | 105.69 | 328.5 | 256.0 |
| 64 | zd | 62147 | 0 | 1034.93 writes/s | 59.16 | 89.10 | 123.26 | 176.47 | 332.0 | 283.1 |
| 128 | zd | 87584 | 0 | 1458.30 writes/s | 86.52 | 116.18 | 145.03 | 254.41 | 327.3 | 320.7 |

## channel-wake

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 32 | zd | 20292 | 0 | 337.89 wakes/s | 30.01 | 41.36 | 56.59 | 268.66 | 283.6 | 388.4 |
| 64 | zd | 22744 | 0 | 378.56 wakes/s | 54.05 | 116.68 | 232.21 | 596.77 | 289.2 | 388.4 |
| 128 | zd | 20811 | 0 | 345.74 wakes/s | 183.43 | 604.42 | 1213.76 | 2896.84 | 290.3 | 396.9 |

## rpc-locking

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 32 | zd | 39635 | 0 | 660.02 rpcs/s | 37.24 | 96.34 | 183.78 | 374.17 | 290.9 | 276.6 |
| 64 | zd | 46720 | 0 | 777.52 rpcs/s | 57.75 | 183.60 | 363.89 | 984.78 | 279.9 | 300.3 |
| 128 | zd | 42336 | 0 | 703.61 rpcs/s | 124.35 | 420.91 | 849.03 | 2513.35 | 272.3 | 285.7 |

## rpc-plain

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 32 | zd | 130386 | 0 | 2172.66 rpcs/s | 10.09 | 33.80 | 68.38 | 190.05 | 136.3 | 97.2 |
| 64 | zd | 118365 | 0 | 1971.72 rpcs/s | 22.04 | 76.14 | 154.93 | 407.50 | 151.3 | 98.9 |
| 128 | zd | 97033 | 0 | 1615.80 rpcs/s | 53.34 | 187.59 | 376.86 | 1276.15 | 181.5 | 104.2 |

## sequential-steps

| Concurrency | Target | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 32 | zd | 5199 | 0 | 863.49 steps/s | 363.21 | 560.67 | 32.06 | 62.03 | 110.08 | 322.77 | 275.4 | 359.0 |
| 64 | zd | 5287 | 0 | 875.40 steps/s | 710.12 | 1301.98 | 58.14 | 160.89 | 313.81 | 748.33 | 282.6 | 367.5 |
| 128 | zd | 4409 | 0 | 724.24 steps/s | 1689.95 | 3291.39 | 134.07 | 431.93 | 858.33 | 2362.58 | 281.4 | 369.1 |

## start-complete

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 32 | zd | 36889 | 0 | 614.39 flows/s | 49.19 | 70.71 | 102.59 | 174.35 | 282.4 | 412.2 |
| 64 | zd | 38159 | 0 | 635.20 flows/s | 79.96 | 187.28 | 361.76 | 758.87 | 286.4 | 426.9 |
| 128 | zd | 35719 | 0 | 593.99 flows/s | 155.49 | 468.85 | 912.33 | 2138.55 | 291.3 | 420.0 |
