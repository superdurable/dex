# c7gd.4xlarge on S3, 2026-10-31

These results are for server-zd on one c7gd.4xlarge (16 vCPU Graviton3, 32 GiB RAM) in AWS us-west-2b. RocksDB ran on the local NVMe instance store, and the log and blobs went to an S3 Standard bucket in the same Region. `dexbench` ran on a c7g.8xlarge in the same cluster placement group, over 8 gRPC connections. Load-host mean CPU stayed below 31% at every point.

- **server-zd:** built from commit `479ef22a9`, the head of PR #1110, which merged as `121a3c156`.
- **dexbench:** built from commit `997b409e8`. The binary carries no VCS stamp, so `config.env` records its revision as `unknown`. Its measurement code matches main, except for one line that records the total number of Worker task slots.
- **Timing:** every point used a 10-second warmup and a 45-second measurement, on a newly started server with an empty local store and a new S3 prefix.

## Peak results

| Scenario | Concurrency | Throughput | Latency p50 / p99 | Server CPU | S3 PUT/s |
| --- | ---: | ---: | --- | ---: | ---: |
| S2 `sequential-steps` (SYNC) | 2,048 | **7,544 Steps/s**, median of 3 (max 7,666) | Step 243 / 407 ms | 968% of 1,600% | 64 |
| S3 `sequential-steps-async` | 256 | **10,007 Steps/s**, median of 3 (max 10,050) | Step 5.8 / 53 ms | 1,249% | 92 |
| S1 `start-complete` | 3,072 | 5,104 Flows/s | 559 / 1,553 ms | 1,213% | 76 |
| S4 `rpc-plain` | 48 | 34,611 RPCs/s | 1.2 / 4.1 ms | 969% | 0 |
| S4 `rpc-locking` | 1,024 | 8,359 RPCs/s | 116 / 318 ms | 1,166% | 91 |
| S5 `attribute-read-direct` | 256 | 11,257 reads/s | 22 / 44 ms | 441% | 0 |
| S6 `channel-wake` | 64 | latency only | 83 / 216 ms | 111% | 123 |

Every point uses these server-zd settings, recorded in each `zd-config.yaml`:

```yaml
commit:
  maxUnappliedBytes: 268435456      # 256 MiB, so thousands of concurrent Flows are not rejected
limits:
  maxInFlightRpcsPerNode: 4096      # lets S4 run above the default 1,000 callers
localStorage:
  blockCacheBytes: 8589934592       # 8 GiB
  writeBufferBytes: 134217728       # 128 MiB
```

Everything else is the default. With PR #1110, the defaults on this host are 16 applier lanes and 8 RocksDB background jobs.

## What limits this instance

- **CPU is not the limit.** Server CPU stays at 60–80% of the 16 cores at the S1–S4 peaks.
- **RocksDB does not stall.** Its `LOG` shows no write stalls or stops.
- **S3 PUTs are not the limit.** Commit latency averages 32–71 ms per point, and 56–61 ms at the S2 headline.
- **The load host is not the limit.** Raising the Worker task slots to 6,144 (`07-worker-slots`) does not change the result. That is three times the default at C=2,048 and twice at C=3,072.
- **Throughput collapses above the peak concurrency.**
  - S2 falls from about 7.5k Steps/s at 2,048 Flows to 2.6k at 3,072. Its Step schedule-to-start latency grows from a few milliseconds to over 600 ms.
  - S3 and S4 `rpc-plain` also fall steadily as concurrency rises.
  - Issue #1118 tracks this collapse. Until it is fixed, run this instance at or below the peak concurrency.
- **S5 is limited by the network.** Each read moves 160 KiB, so 11.3k reads/s is about 14.8 Gbps, at the instance's 15 Gbps limit. Server CPU is only 28% there.
- **Tuning has no effect.** `commit.maxInFlight` of 8 or 16, or 32 applier lanes, gives no gain beyond run-to-run noise.
- **Run-to-run spread is about ±10% at the S2 peak.** One single run reached 8,506 Steps/s, and the three final trials ranged from 7,343 to 7,666.

## Rounds

| Directory | Purpose | Outcome |
| --- | --- | --- |
| `01-concurrency` | S2 at 512–4,096 and S3 at 256–2,048 | S2 peaks at 2,048 (8,506 in this single run) and collapses at 4,096 with client timeouts. S3 is best at 256. |
| `02-tuning` | `maxInFlight` 8 and 16, 32 lanes at the peaks; S2 at 3,072; S3 at 128 | No setting beats the defaults beyond noise. S2 at 3,072 collapses. |
| `03-scenarios` | S1, S4, S5, S6 | S1 keeps rising, and S4 `rpc-plain` falls with concurrency. |
| `04-scenarios-extra` | S4 `rpc-plain` at 128 and 256; S1 at 3,072 and 4,096 | S1 peaks at 3,072. |
| `05-rpc-plain-low-concurrency`, `06-rpc-plain-low-concurrency` | S4 `rpc-plain` at 32–96 | Peak of 34.6k RPCs/s at 48. |
| `07-worker-slots` | S2 at 2,048 and 3,072 with 6,144 Worker task slots | No change, so the collapse is in the server. |
| `08-final-trials` | S2 at 1,536 and 2,048, S3 at 256, three trials each | The headline medians. |

## Reproduce

From a clone on a load host set up as described in `benchmark/README.md`, rerun the S3 headline:

```bash
benchmark/aws/reproduce.sh benchmark/results/2026-10-31-aws-s3-c7gd-4xlarge/08-final-trials sequential-steps-async 256
```

`config.env` restores the `DEXBENCH_ZD_*` settings above, the 8 connections, and three trials.
