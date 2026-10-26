## async-steps

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 2 | zd | 790 | 0 | 790.00 steps/s | 25.28 | 26.26 | 29.89 | 30.81 |
## attribute-read-direct

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 2 | zd | 307353 | 0 | 30735.30 reads/s | 0.06 | 0.08 | 0.17 | 1.26 |
## attribute-read-rpc

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 2 | zd | 17816 | 0 | 1781.60 reads/s | 0.97 | 1.93 | 2.19 | 4.54 |
## attribute-write-local

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 2 | zd | 11079 | 0 | 1107.90 writes/s | 1.71 | 2.31 | 4.42 | 30.18 |
## channel-wake

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 2 | zd | 5358 | 0 | 535.80 wakes/s | 1.76 | 2.65 | 10.45 | 31.18 |
## recovery

| Target log MiB | Replayed MiB | Target | Recovery s | Errors |
| ---: | ---: | --- | ---: | ---: |
| 1 | 1.04 | zd | 1.03 | 0 |
## rpc-locking

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 2 | zd | 12604 | 0 | 1260.40 rpcs/s | 1.33 | 2.30 | 4.31 | 35.95 |
## rpc-plain

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 2 | zd | 43228 | 0 | 4322.80 rpcs/s | 0.20 | 1.42 | 1.70 | 3.71 |
## sequential-steps

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 2 | zd | 1407 | 0 | 1407.00 steps/s | 12.81 | 19.32 | 36.38 | 74.54 |
## start-complete

| Concurrency | Target | Successes | Errors | Throughput | p50 ms | p90 ms | p99 ms | max ms |
| ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 2 | zd | 11643 | 0 | 1164.30 flows/s | 1.56 | 2.05 | 4.23 | 21.02 |
