Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## attribute-read-direct

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 512 | default | 271912 | 0 | 6034.17 reads/s | 84.90 | 112.99 | 137.19 | 213.53 | 197.2 | 98.6 | 10.1 | 0.3 | 351.2 | 4 connections |
| 1024 | default | 247163 | 0 | 5477.00 reads/s | 185.90 | 233.24 | 276.61 | 402.54 | 197.2 | 98.6 | 10.2 | 0.3 | 517.8 | 4 connections |

## rpc-locking

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 64 | default | 41198 | 0 | 913.78 rpcs/s | 63.81 | 100.26 | 158.32 | 280.76 | 176.6 | 88.3 | 1.3 | 115.1 | 121.9 | 4 connections |

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 32 | default | 300678 | 0 | 6680.98 rpcs/s | 3.48 | 10.03 | 19.49 | 61.75 | 194.1 | 97.0 | 7.5 | 0.3 | 42.1 | 4 connections |
| 64 | default | 266672 | 0 | 5924.89 rpcs/s | 7.34 | 24.71 | 49.97 | 138.92 | 194.0 | 97.0 | 6.8 | 0.3 | 44.6 | 4 connections |

