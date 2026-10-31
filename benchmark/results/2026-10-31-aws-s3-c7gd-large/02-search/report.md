Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## attribute-read-direct

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 256 | default | 263302 | 0 | 5847.37 reads/s | 43.30 | 60.65 | 81.99 | 155.32 | 192.9 | 96.4 | 9.4 | 0.3 | 204.7 | 4 connections |
| 1024 | default | 244417 | 0 | 5417.37 reads/s | 188.51 | 235.55 | 278.63 | 403.26 | 195.7 | 97.9 | 9.3 | 0.3 | 528.7 | 4 connections |

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 384 | default | 68329 | 0 | 1513.87 rpcs/s | 266.65 | 323.79 | 417.24 | 533.11 | 188.2 | 94.1 | 2.1 | 108.6 | 189.0 | 4 connections |
| 768 | default | 82141 | 0 | 1807.60 rpcs/s | 445.66 | 527.32 | 659.50 | 750.76 | 192.7 | 96.3 | 2.8 | 109.5 | 243.5 | 4 connections |

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 16 | default | 326162 | 0 | 7247.67 rpcs/s | 1.99 | 3.58 | 5.55 | 15.12 | 188.8 | 94.4 | 8.0 | 0.3 | 41.8 | 4 connections |

## sequential-steps

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 768 | default | 6106 | 0 | 1267.75 steps/s | 5737.15 | 8976.53 | 578.01 | 881.80 | 1238.93 | 1666.67 | 184.0 | 92.0 | 1.5 | 98.0 | 268.2 | 4 connections |

