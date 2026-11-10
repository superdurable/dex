Latency percentiles cover successful operations only. Errors counts failed measured operations, including timeouts. Load is closed-loop, so p99 and max understate queueing delay at saturation. Server CPU % is 100 per fully used core; host % is the share of all cores on that host. A point whose load host CPU exceeds 70% is marked invalid because the load generator may have been the bottleneck. channel-wake latency runs from PublishToChannel to Step Execute start; its throughput also includes the untimed Flow start and wait confirmation.

## rpc-plain

| Concurrency | Settings | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms | Server CPU % | Server host % | Load host % | PUT/s | RSS MiB max | Notes |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 4 | default | 335360 | 0 | 7452.35 rpcs/s | 0.51 | 0.64 | 0.95 | 3.65 | 169.8 | 84.9 | 5.4 | 0.3 | 42.6 | 4 connections |
| 16 | default | 544107 | 0 | 12091.03 rpcs/s | 1.30 | 1.70 | 2.23 | 7.43 | 190.9 | 95.5 | 7.3 | 0.3 | 45.0 | 4 connections |
| 32 | default | 569309 | 0 | 12650.93 rpcs/s | 2.46 | 3.37 | 4.48 | 11.89 | 191.5 | 95.8 | 6.9 | 0.3 | 48.0 | 4 connections |
| 64 | default | 595079 | 0 | 13222.99 rpcs/s | 4.72 | 6.51 | 8.58 | 15.99 | 191.2 | 95.6 | 7.3 | 0.3 | 52.3 | 4 connections |
| 128 | default | 646856 | 0 | 14373.16 rpcs/s | 8.79 | 11.98 | 15.64 | 32.96 | 191.0 | 95.5 | 7.6 | 0.3 | 61.8 | 4 connections |
| 256 | default | 676907 | 0 | 15039.99 rpcs/s | 16.85 | 23.15 | 30.29 | 50.07 | 188.5 | 94.3 | 8.4 | 0.3 | 78.5 | 4 connections |
| 512 | default | 660494 | 0 | 14670.46 rpcs/s | 34.40 | 47.10 | 61.17 | 95.83 | 187.8 | 93.9 | 8.1 | 0.3 | 106.8 | 4 connections |

