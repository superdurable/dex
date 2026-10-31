Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## sequential-steps

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 384 | default | 5969 | 0 | 1273.83 steps/s | 2879.37 | 4774.72 | 250.00 | 520.90 | 955.02 | 2213.75 | 192.6 | 96.3 | 1.5 | 106.8 | 236.4 | 4 connections |
| 512 | MAX_IN_FLIGHT=2 | 5192 | 0 | 1089.15 steps/s | 4606.69 | 5266.29 | 426.85 | 506.16 | 612.23 | 814.78 | 146.7 | 73.4 | 1.2 | 65.6 | 229.9 | 4 connections |

## sequential-steps-async

| Concurrency | Settings | Successes | Errors | Throughput | Flow p50 ms | Flow p99 ms | Step p50 ms | Step p90 ms | Step p99 ms | Step max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 16 | default | 5699 | 0 | 1263.35 steps/s | 112.79 | 389.59 | 1.90 | 5.39 | 11.84 | 33.89 | 146.0 | 73.0 | 1.4 | 92.6 | 212.5 | 4 connections |

