Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. CPU % is 100 per fully used core. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## sequential-steps

| Concurrency | Target | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 109 | 0 | 18.02 steps/s | 551.23 | 600.73 | 49.72 | 54.35 | 66.50 | 149.88 | 73.7 | 305.7 |
| 1 | zd | 927 | 0 | 154.44 steps/s | 63.17 | 94.98 | 5.85 | 8.04 | 10.82 | 24.71 | 101.1 | 192.4 |
| 1 | zd / temporal | — | — | 8.57x | — | — | 0.12x | 0.15x | 0.16x | 0.16x | — | — |
| 4 | temporal | 239 | 0 | 39.41 steps/s | 1017.80 | 1240.69 | 93.09 | 112.35 | 136.72 | 289.75 | 129.0 | 335.4 |
| 4 | zd | 3356 | 0 | 559.02 steps/s | 69.62 | 109.43 | 6.47 | 8.74 | 12.38 | 31.75 | 227.5 | 406.6 |
| 4 | zd / temporal | — | — | 14.19x | — | — | 0.07x | 0.08x | 0.09x | 0.11x | — | — |
| 8 | temporal | 288 | 0 | 47.10 steps/s | 1693.78 | 1996.55 | 146.17 | 188.96 | 230.10 | 383.18 | 150.1 | 343.3 |
| 8 | zd | 4162 | 0 | 692.83 steps/s | 113.44 | 166.72 | 10.55 | 14.34 | 19.30 | 56.75 | 252.7 | 403.5 |
| 8 | zd / temporal | — | — | 14.71x | — | — | 0.07x | 0.08x | 0.08x | 0.15x | — | — |

## start-complete

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | CPU % mean | RSS MiB max |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | temporal | 486 | 0 | 8.09 flows/s | 109.67 | 150.62 | 159.32 | 232.56 | 85.5 | 318.9 |
| 1 | zd | 7529 | 0 | 125.48 flows/s | 7.73 | 9.60 | 13.21 | 26.93 | 103.1 | 218.0 |
| 1 | zd / temporal | — | — | 15.52x | 0.07x | 0.06x | 0.08x | 0.12x | — | — |
| 4 | temporal | 936 | 0 | 15.57 flows/s | 253.78 | 297.12 | 343.01 | 553.42 | 129.1 | 354.8 |
| 4 | zd | 21123 | 0 | 351.99 flows/s | 11.06 | 14.03 | 18.16 | 55.22 | 226.1 | 416.5 |
| 4 | zd / temporal | — | — | 22.61x | 0.04x | 0.05x | 0.05x | 0.10x | — | — |
| 8 | temporal | 1002 | 0 | 16.61 flows/s | 476.14 | 552.63 | 657.91 | 804.67 | 134.9 | 366.1 |
| 8 | zd | 28195 | 0 | 469.80 flows/s | 16.53 | 21.25 | 27.64 | 54.47 | 251.0 | 429.7 |
| 8 | zd / temporal | — | — | 28.28x | 0.03x | 0.04x | 0.04x | 0.07x | — | — |
| 16 | temporal | 995 | 0 | 16.42 flows/s | 961.28 | 1080.04 | 1234.15 | 1335.40 | 138.9 | 370.1 |
| 16 | zd | 34323 | 0 | 571.83 flows/s | 27.10 | 36.01 | 46.10 | 69.82 | 267.7 | 419.1 |
| 16 | zd / temporal | — | — | 34.82x | 0.03x | 0.03x | 0.04x | 0.05x | — | — |
| 32 | temporal | 988 | 0 | 16.16 flows/s | 1646.08 | 3243.22 | 4837.89 | 5535.37 | 135.4 | 371.5 |
| 32 | zd | 40832 | 0 | 680.18 flows/s | 45.07 | 63.04 | 85.19 | 137.50 | 281.2 | 429.1 |
| 32 | zd / temporal | — | — | 42.10x | 0.03x | 0.02x | 0.02x | 0.02x | — | — |
| 64 | temporal | 1004 | 0 | 16.04 flows/s | 3353.57 | 6808.27 | 7389.40 | 8211.21 | 134.2 | 384.0 |
| 64 | zd | 45548 | 0 | 758.31 flows/s | 68.34 | 154.53 | 277.75 | 654.64 | 285.8 | 455.3 |
| 64 | zd / temporal | — | — | 47.27x | 0.02x | 0.02x | 0.04x | 0.08x | — | — |
