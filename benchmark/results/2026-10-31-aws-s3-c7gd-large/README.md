# c7gd.large on S3, 2026-10-31

These results are for server-zd on one c7gd.large (2 vCPU Graviton3, 4 GiB RAM) in AWS us-west-2b. RocksDB ran on the local NVMe instance store, and the log and blobs went to an S3 Standard bucket in the same Region. `dexbench` ran on a c7g.8xlarge in the same cluster placement group, over 4 gRPC connections. Load-host CPU stayed below 11% at every point.

- **server-zd:** built from commit `479ef22a9`, the head of PR #1110, which merged as `121a3c156`.
- **dexbench:** built from commit `997b409e8`. Its measurement code matches main, except for one line that records the total number of Worker task slots.
- **Scripts:** `run-remote.sh` from the commits recorded in each `config.env`.
- **Timing:** every point used a 10-second warmup and a 45-second measurement, on a newly started server with an empty local store and a new S3 prefix.

## Peak results

| Scenario | Concurrency | Throughput | Latency p50 / p99 | Server CPU | S3 PUT/s |
| --- | ---: | ---: | --- | ---: | ---: |
| S2 `sequential-steps` (SYNC) | 256 | **1,305 Steps/s**, median of 3 (max 1,361) | Step 173 / 434 ms | 180% of 200% | 101 |
| S3 `sequential-steps-async` | 32 | **1,492 Steps/s**, median of 3 (max 1,529) | Step 7.1 / 51 ms | 186% | 92 |
| S1 `start-complete` | 256 | 855 Flows/s | 279 / 650 ms | 192% | 108 |
| S4 `rpc-plain` | 32 | 6,681 RPCs/s | 3.5 / 19.5 ms | 194% | 0 |
| S4 `rpc-locking` | 256 | 1,324 RPCs/s | 151 / 712 ms | 192% | 111 |
| S5 `attribute-read-direct` | 512 | 6,034 reads/s | 85 / 137 ms | 197% | 0 |
| S6 `channel-wake` | 64 | latency only | 106 / 258 ms | 105% | 109 |

All points use the default server-zd configuration. The only change is `api.listen` and `metrics.listen` on all interfaces, as recorded in each `zd-config.yaml`. With PR #1110, the defaults on this host are 8 applier lanes and 2 RocksDB background jobs.

## What limits this instance

- **CPU.** Both cores are close to saturation at every peak.
- **Tuning has no effect.** Changing applier lanes (2 or 8), `commit.batchWindow` (20 or 50 ms), or `commit.maxInFlight` (2, 4, or 8) moves S2 and S3 by less than the 5% run-to-run spread.
- **Fewer PUTs in flight is worse.** With `commit.maxInFlight: 2`, CPU drops to about 75% and throughput falls. With only two PUTs in flight, the S3 round trip becomes the limit.
- **Commit latency is the S3 round trip.** It averaged 32–38 ms per log batch.

## Rounds

Each directory is one `run-remote.sh` invocation, with its own `report.md`, `charts.html`, `config.env`, `zd-config.yaml`, and `points.txt`.

| Directory | Purpose | Outcome |
| --- | --- | --- |
| `01-concurrency` | S2 at 256–1,024 and S3 at 64–512, defaults | S2 peaks at 256, S3 at 64 or lower. Its first point reused an S3 prefix from an aborted run and replayed old state; treat it as a warmup. |
| `02-tuning` | One setting changed per point at the peak | Every variant is within run-to-run noise of the default. S3 at 32 is the best S3 point. |
| `03-tuning` | `maxInFlight: 2` at higher concurrency; S2 at 384; S3 at 16 | None beats the defaults. |
| `04-scenarios` | S1, S4, S5, S6 with defaults | S4 `rpc-plain` and S5 keep improving outside the tested range. |
| `05-scenarios-extra` | S4 `rpc-plain` at 32 and 64, `rpc-locking` at 64, S5 at 512 and 1,024 | Peaks for `rpc-plain` (32) and S5 (512). |
| `06-final-trials` | S2 at 256 and S3 at 32, three trials each | The headline medians. |

## Reproduce

From a clone on a load host set up as described in `benchmark/README.md`, rerun the S2 headline:

```bash
benchmark/aws/reproduce.sh benchmark/results/2026-10-31-aws-s3-c7gd-large/06-final-trials sequential-steps 256
```

`reproduce.sh` reads `config.env` and `points.txt` from that directory. Point it at your own server host and bucket with `DEXBENCH_SERVER_HOST`, `DEXBENCH_SERVER_ADDRESS`, and `DEXBENCH_S3_BUCKET`.
