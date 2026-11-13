# Dex server benchmark

`dexbench` measures server-zd with a Go SDK Worker. It runs six execution scenarios at
fixed client concurrency and records throughput, latency percentiles, server
CPU and memory, and object storage requests for each run.

## Latest results

These results were measured on 2026-11-09 in AWS us-west-2, on on-demand instances.

- **Server:** one server-zd node on a c7gd instance. RocksDB was on the local NVMe instance store, and the log went to S3 Standard in the same Region.
- **Load host:** a separate c7g.8xlarge ran `dexbench` in the same Availability Zone and cluster placement group. Its mean CPU stayed below 38% of the host at every point.
- **Version:** S4 and S5: server-zd v0.1.25 (`4875e0083`); S1, S2, S3, and S6: server-zd v0.1.23 (`f27e3295f`).
- **Measurement:** each point is a 10-second warmup and a 45-second measurement on a newly started server. Every headline point ran three times with profiling off. S1 is a rough single run per point.

Each table lists the peak, with bold throughput, and a "~50% of peak" row per scenario; rows marked "search" and S1 rows are single runs, and the others are the median of three runs with that run's percentiles.

### c7gd.large (2 vCPU)

| Scenario | C | Throughput | p50 / p90 / p99 (ms) |
| --- | ---: | ---: | --- |
| S2 SYNC Steps, latency per Step | 96 (~50% of peak) | 846 Steps/s | 105 / 147 / 285 |
|  | 256 (search) | 1,549 Steps/s | 151 / 233 / 323 |
|  | 512 (peak) | **1,772 Steps/s** | 282 / 344 / 484 |
|  | 1,024 (search) | 1,587 Steps/s | 590 / 851 / 1,567 |
| S3 ASYNC Steps, latency per Step | 2 (~50% of peak) | 1,224 Steps/s | 1.3 / 2.6 / 4.9 |
|  | 32 (peak) | **2,105 Steps/s** | 14.5 / 23.9 / 32.6 |
|  | 512 (search) | 1,810 Steps/s | 263 / 357 / 634 |
| S1 Flows started and completed | 64 | 435 Flows/s | 140 / 185 / 249 |
|  | 512 (peak) | **1,111 Flows/s** | 457 / 560 / 650 |
|  | 1,024 | 980 Flows/s | 1,012 / 1,310 / 1,573 |
| S4 plain RPC, reads one 8-byte Attribute | 4 (~50% of peak) | 7,501 RPCs/s | 0.5 / 0.6 / 0.9 |
|  | 64 (search) | 13,223 RPCs/s | 4.7 / 6.5 / 8.6 |
|  | 256 (peak) | **15,392 RPCs/s** | 16.7 / 22.3 / 26.7 |
|  | 512 (search) | 14,670 RPCs/s | 34.4 / 47.1 / 61.2 |
| S4 Attribute-locking RPC, updates one 8-byte Attribute | 64 (~50% of peak) | 904 RPCs/s | 67.0 / 91.3 / 183.5 |
|  | 960 (peak) | **2,165 RPCs/s** | 426 / 590 / 774 |
| S5 direct read of ten 16 KiB Attributes (160 KiB) | 3 (~50% of peak) | 3,091 reads/s | 0.9 / 1.2 / 1.5 |
|  | 256 (peak) | **5,941 reads/s** | 42.8 / 59.5 / 73.8 |
| S6 Channel wake, latency per wake | 64 (~50% of peak) | 204 wakes/s | 112 / 177 / 304 |
|  | 256 (peak) | **460 wakes/s** | 209 / 275 / 408 |

### c7gd.4xlarge (16 vCPU)

| Scenario | C | Throughput | p50 / p90 / p99 (ms) |
| --- | ---: | ---: | --- |
| S2 SYNC Steps, latency per Step | 1,024 (~50% of peak) | 4,527 Steps/s | 218 / 295 / 382 |
|  | 8,192 (search) | 8,531 Steps/s | 910 / 1,248 / 1,468 |
|  | 12,288 (peak) | **8,403 Steps/s** | 1,274 / 2,770 / 3,404 |
| S3 ASYNC Steps, latency per Step | 8 (~50% of peak) | 4,766 Steps/s | 1.6 / 2.0 / 3.3 |
|  | 1,024 (search) | 9,201 Steps/s | 106 / 135 / 173 |
|  | 4,096 (peak) | **10.1k Steps/s** | 405 / 466 / 537 |
| S1 Flows started and completed | 3,072 | 4,902 Flows/s | 611 / 709 / 801 |
|  | 6,144 | 5,309 Flows/s | 1,146 / 1,336 / 1,533 |
|  | 12,288 (peak) | **5,626 Flows/s** | 2,164 / 2,493 / 2,747 |
| S4 plain RPC, reads one 8-byte Attribute | 24 (~50% of peak) | 35.0k RPCs/s | 0.6 / 1.0 / 2.1 |
|  | 128 | 65.1k RPCs/s | 1.8 / 3.0 / 4.5 |
|  | 512 (peak) | **67.3k RPCs/s** | 7.3 / 11.5 / 15.6 |
|  | 2,048 (search) | 62.0k RPCs/s | 31.8 / 49.4 / 67.5 |
| S4 Attribute-locking RPC, updates one 8-byte Attribute | 512 (~50% of peak) | 6,093 RPCs/s | 76.9 / 111.4 / 277.1 |
|  | 4,096 | 13.0k RPCs/s | 313 / 363 / 553 |
|  | 8,192 (peak) | **13.8k RPCs/s** | 589 / 682 / 755 |
| S5 direct read of ten 16 KiB Attributes (160 KiB) | 5 (~50% of peak) | 5,731 reads/s | 0.8 / 1.2 / 1.6 |
|  | 256 (peak) | **11.3k reads/s** | 22.8 / 33.2 / 45.6 |
| S6 Channel wake, latency per wake | 64 (search) | 243 wakes/s | 92.1 / 143.8 / 308.8 |
|  | 512 (~50% of peak) | 1,400 wakes/s | 139 / 197 / 308 |
|  | 4,096 (peak) | **2,278 wakes/s** | 821 / 1,008 / 1,147 |

**Notes.**
- **C** is the number of concurrent clients. Load is closed-loop, so latency grows with C once the server is saturated.
- **S1** is a single 15-second run per point after a 5-second warmup. Each Flow keeps its history local (`FlowConfig.offload_history` false), so it uploads no history segment. Its S3 PUTs are log batches only: 0.06–0.28 PUTs per completed Flow on c7gd.large and 0.005–0.011 on c7gd.4xlarge.
- **S2 at C=12,288** on c7gd.4xlarge gave 7,164, 8,403, and 9,646 Steps/s in its three runs, following each run's S3 commit latency.
- **S2 at C=4,096** on c7gd.4xlarge is left out. With 16 connections its p99 was 5,475 ms; with 32 connections it was 725 ms. See the [c7gd.4xlarge v0.1.23 results](results/2026-11-09-aws-s3-c7gd-4xlarge-v0.1.23/README.md).
- **Plain RPC on c7gd.4xlarge:** 65.1k RPCs/s at C=128, against 49.3k on 2026-10-31 and 30.9k before #1445. The server now uses 68–73% of the host; see the [c7gd.4xlarge v0.1.25 results](results/2026-11-09-aws-s3-c7gd-4xlarge-v0.1.25/README.md).
- **Attribute-locking RPC at C=8,192** on c7gd.4xlarge used 32 connections and a 16,384 in-flight RPC limit.
- **Run age:** S2, S3, and S6 keep each Flow open for the whole point. Their throughput no longer falls as a Flow ages; see [`results/2026-11-09-long-lived-runs/`](results/2026-11-09-long-lived-runs/README.md).
- **Run age on c7gd.large:** S2 at C=128 gave 1,031–1,104 Steps/s after a 2-second warmup and 1,042 after a 240-second warmup. Before v0.1.23 it lost a factor of three to four after 90 seconds.
- **Snapshots:** a snapshot is requested after 256 MiB of new log data. Every c7gd.4xlarge S2 point above C=4,096 took one or two snapshots of 7–9 s during the point.
- **S3 requests:** plain RPC and S5 write nothing, so their only PUTs are the lease, about 0.3 per second. Steps, wakes, and Attribute-locking RPCs made 14–120 log PUTs per second.

### Settings that produced these numbers

- **c7gd.large:** the default server-zd configuration, 4 gRPC connections.
- **c7gd.4xlarge:** the defaults plus the settings below, with 16 gRPC connections. Attribute-locking RPC at C=8,192 also sets `limits.maxInFlightRpcsPerNode: 16384`.

  ```yaml
  commit:
    maxUnappliedBytes: 268435456
  limits:
    maxInFlightRpcsPerNode: 4096
  localStorage:
    blockCacheBytes: 8589934592
    writeBufferBytes: 134217728
  ```

- **History:** the `serve` defaults. Every run offloads its history unless it opts out, and the history window is 50,000 events. S1 Flows opt out.
- **Connections:** `matching.maxConcurrentTasksCap` (default 256) limits the task slots of each pull Worker, and `dexbench` gives each connection C divided by the connection count. Points above C=4,096 therefore used 32 or 48 connections. S1 used 48.

The defaults follow the host size: applier lanes are `max(8, CPUs)`, and RocksDB background jobs are `max(2, CPUs / 2)`.

The result sets are:

- S4 and S5 on v0.1.25:
  - [`results/2026-11-09-aws-s3-c7gd-large-v0.1.25/`](results/2026-11-09-aws-s3-c7gd-large-v0.1.25/README.md)
  - [`results/2026-11-09-aws-s3-c7gd-4xlarge-v0.1.25/`](results/2026-11-09-aws-s3-c7gd-4xlarge-v0.1.25/README.md)
- S1, S2, S3, and S6 on v0.1.23:
  - [`results/2026-11-09-aws-s3-c7gd-large-v0.1.23/`](results/2026-11-09-aws-s3-c7gd-large-v0.1.23/README.md)
  - [`results/2026-11-09-aws-s3-c7gd-4xlarge-v0.1.23/`](results/2026-11-09-aws-s3-c7gd-4xlarge-v0.1.23/README.md)

Each set's `README.md` lists every round of the search and its outcome. Each round directory holds what is needed to rerun it:
- `config.env`: instance types, placement, disk, versions, and `dexbench` settings.
- `zd-config.yaml`: the exact server-zd configuration.
- `points.txt`: every point in the round.

Search rounds also keep a perf profile and thread stacks for every point, in `<point>.server/`.

Rerun a headline point with `benchmark/aws/reproduce.sh`. See [Reproduce a result](#reproduce-a-result).

The [local search backend results](results/2026-10-31-search-local/README.md)
measure S7 at 100k and 1M runs on builtin, ParadeDB, and PostgreSQL. They include
projection throughput, WAL, Attribute backfill, and a confirmed ParadeDB Flow
type index collation finding.
The [local search outbox results](results/2026-11-01-search-outbox-local/README.md)
repeat the 1M ten-Attribute ParadeDB and PostgreSQL points with the SearchOutbox
projection, and measure the cost of its acks.

## Reading the results

### What the numbers mean

- **A SYNC Step waits for its commits to reach S3, and an ASYNC Step does not.**
  - Each log commit is one S3 PUT round trip. At the S2 peaks, commits averaged 41–42 ms on c7gd.large and 59–98 ms on c7gd.4xlarge.
  - Under load, a SYNC Step also waits for its batch and for a free PUT slot, so its per-Step latency is 280 ms at the c7gd.large peak and 1.3 s at the c7gd.4xlarge peak.
  - Closed-loop SYNC throughput is roughly concurrency divided by Step latency, so SYNC Steps need thousands of concurrent Flows to reach the peak.
  - An ASYNC Step moves on before its commit, so its per-Step latency is 1.3–1.6 ms at low concurrency. ASYNC throughput is limited by the server, not by latency.
- **S3 PUTs scale with batches, not with Steps.** Commands are grouped into log batches, with at most `commit.maxInFlight` (default 4) PUTs in flight. A busy node made 23–98 log PUTs per second at the S2, S3, and S6 peaks, independent of instance size. At USD 0.005 per 1,000 PUTs, that is at most about USD 1.8 per busy hour per node.
- **What limits each instance type:**
  - c7gd.large uses 93–99% of its two cores at every throughput peak.
  - c7gd.4xlarge uses 68–87% of its 16 cores at the S2, S3, S4, and S6 peaks.
  - S5 on c7gd.4xlarge is limited by the 15 Gbps network, not by the server, so its CPU stays near 27%.

Each run writes one JSON file per scenario variant and concurrency, plus
`report.md`, `charts.html`, and `host.txt`. `report.md` has one table per
variant. `charts.html` is a self-contained page that plots each metric against
concurrency.

### Scenarios

| ID | Variant | What it measures |
| --- | --- | --- |
| S1 | `start-complete` | Start and complete a short Flow with one Step |
| S2 | `sequential-steps` | Back-to-back SYNC Steps in one long-lived Flow per client |
| S3 | `sequential-steps-async` | Back-to-back ASYNC Steps in one long-lived Flow per client |
| S4 | `rpc-plain`, `rpc-locking` | An RPC that reads one 8-byte Attribute, and an RPC that locks and updates it |
| S5 | `attribute-read-direct` | Read ten 16 KiB Attributes, 160 KiB in total, from the client without a Worker |
| S6 | `channel-wake` | Latency from a Channel publish to the waiting Step of a long-lived Flow |
| S7 | `dexbench search` | Search latency, seed projection throughput, WAL, and PostgreSQL backfill |

The scenario name `rpc` runs both S4 variants. The name `attribute-read` runs
S5.

**S1 `start-complete`.** Each client starts a new Flow whose only Step
completes it. The client then waits for the Flow result. The timed operation
is StartFlow plus WaitForFlow. Throughput is in Flows per second. Every
operation creates a new, short Flow. Each Flow starts with
`FlowConfig.offload_history` set to false, the recommended mode for short runs,
so it keeps its history local and uploads no history segment when it closes.

**S2 `sequential-steps`.** Before warmup, each client starts one Flow that
stays open for the whole point. Its SYNC Step moves to itself with GoTo, so
the Flow runs Steps back to back until the point ends and the client stops
it. The timed operation is one Step: the gap between the start of one Step's
Execute and the start of the next one, measured inside the Worker.
Throughput is in Steps per second. `DEXBENCH_STEPS` is no longer used.

**S3 `sequential-steps-async`.** This is S2 with ASYNC Step durability. A SYNC
Step result is committed to object storage before the Flow moves on. An ASYNC
result lets the Flow move on first, and the commit follows in a later batch.
If the server node fails before that commit, the Step runs again, and so does
any work that started from it. Timing, throughput unit, and per-Step latency
are the same as in S2.

**S4 `rpc-plain` and `rpc-locking`.** Before warmup, each client starts one
Flow that stays open. The Flow has one `int64` counter Attribute, 8 bytes on
the wire. The plain variant invokes an RPC that reads the counter and returns
it. The locking variant invokes an RPC that locks the counter, reads it, and
writes it back. Both RPCs run on the Worker and return only the `int64`. Each client uses its own Flow, so the lock has no
contention. The timed operation is one InvokeRPC call. Throughput is in RPCs
per second. Keep the concurrency below the server's
`limits.maxInFlightRpcsPerNode`, which defaults to 1000.

**S5 `attribute-read-direct`.** Before warmup, each client starts one Flow
that stays open and holds ten 16 KiB string Attributes. Each read calls
FlowService GetAttributes through the generated gRPC client. No Worker is
involved. Values over the server's inline limit,
`values.wireInlineThresholdBytes` (default 100 bytes), come back as blob
references, so all ten 16 KiB values do. The client resolves every
reference with one LoadBlobs call, as an uncached client would. It then
compares every value with the value written at Flow start. The timed
operation is GetAttributes plus LoadBlobs plus the check. Each read is
therefore two calls and returns 160 KiB, about 20,000 times the data of an S4
RPC. Throughput is in reads per second, and it is limited by bandwidth: at
11,258 reads per second, c7gd.4xlarge moves about 1.8 GB/s. JSON results record the verified bytes per read and the
number of blobs loaded.

**S6 `channel-wake`.** Before warmup, each client starts one Flow that stays
open for the whole point. Its Step waits on a Channel, and after each wake it
moves to itself to wait again. After the Step's WaitFor returns, the client
polls GetFlowState until the Step is waiting. Gating the poll on WaitFor keeps it near one
GetFlowState call per wake. The client then publishes one message with
FlowService PublishToChannel. Only the time from the publish call to the start
of the Step's Execute is timed. Throughput includes the untimed setup, so use
only the latency of this scenario.

### Latency and coordinated omission

The load is closed-loop. Each client sends its next request only after the
previous one returns, and the client count is fixed. When the server slows
down, clients send less. Queueing delay that an open-loop workload would see
never appears in the samples. This coordinated omission makes p99 and max
optimistic near saturation. Read the tail percentiles together with
throughput, and compare runs at the same concurrency. Percentiles cover
successful operations only. Failed and timed-out operations are counted as
errors.

## Running the benchmark

### Local smoke

A short local run checks that every scenario works. Its numbers are not
results. It needs a `dexcli` whose `dexcli dev --help` lists `-engine`, and a
`dex-server-zd` binary:

```bash
DEXBENCH_DURATION=10 DEXBENCH_WARMUP=1 DEXBENCH_CONCURRENCY=2 \
  DEXBENCH_DEXCLI_BINARY="$(command -v dexcli)" \
  DEXBENCH_ZD_BINARY=/path/to/dex-server-zd \
  benchmark/local/run.sh
```

To build both binaries from this checkout, run the commands below. The
`dexcli` build also needs Node.js 22 and npm to compile the embedded Web
assets.

```bash
(cd web && npm ci && npm run build)
(cd cli && go build -o "$HOME/.local/bin/dexcli" ./cmd/dexcli)
(cd server-zd && cargo build --release -p dexzd-server)
```

The release build writes `server-zd/target/release/dex-server-zd`.

`run.sh` starts server-zd through `dexcli dev` with its embedded s3local. It
does not start Docker or use AWS. It sets `dev.durabilityMode: processCrashSafe`,
so results stay comparable with earlier runs; the dev default, strict, fsyncs
every write.

### Full local run

```bash
benchmark/local/run.sh
benchmark/bin/dexbench report <result-directory>/*.json
benchmark/bin/dexbench chart <result-directory>/*.json > charts.html
```

By default the script writes to a new temporary directory outside the
repository and prints its path at the end. `benchmark/results/` holds
committed result sets from recorded benchmark hosts. To write one, set
`DEXBENCH_OUT=benchmark/results/<run>`. Never commit a smoke run. The temporary
server data and logs are removed when the script exits.

The defaults are a 15-second warmup and a 1-minute measurement at concurrency
1 and 32. The six scenarios expand to seven variants, so a run has 14 steps.
Each step takes 75 seconds plus a few seconds of server restart. Expect about
19 minutes. For a longer sweep, set
`DEXBENCH_DURATION=180 DEXBENCH_WARMUP=30 DEXBENCH_CONCURRENCY=1,8,32,128`.
It takes about 1 hour 45 minutes.

### Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `DEXBENCH_DURATION` | `60` | Measured seconds per step |
| `DEXBENCH_WARMUP` | `15` | Warmup seconds per step |
| `DEXBENCH_CONCURRENCY` | `1,32` | Comma-separated client counts |
| `DEXBENCH_SCENARIOS` | all six | Comma-separated scenario or variant names |
| `DEXBENCH_STEPS` | `10` | Unused; S2 and S3 run long-lived Flows |
| `DEXBENCH_WORKERS` | `0` | Pull Worker task slots; `0` uses the concurrency |
| `DEXBENCH_CONNECTIONS` | `1` | Client and Worker pairs, each with its own gRPC connections |
| `DEXBENCH_OUT` | new temporary directory | Result directory |
| `DEXBENCH_DEXCLI_BINARY` | `dexcli` on `PATH` | The `dexcli` to run |
| `DEXBENCH_ZD_BINARY` | `dex-server-zd` on `PATH` | The server-zd binary |
| `DEXBENCH_MAX_LOAD` | core count | Wait until the 1-minute load average is below this |

Before each build and step, the script waits until the one-minute load average
is below `DEXBENCH_MAX_LOAD`.

### State between steps

Every combination of scenario variant and concurrency runs on a newly started
server with empty state. The script starts `dexcli dev` with a new server-zd
data directory. S2 to S6 start one Flow per client before warmup, so the warmup
and the measurement of one step share those Flows. The client stops them after
the measurement. S1 starts a new Flow for every operation.

### Resource sampling

CPU and RSS are sampled every second during the measurement. Each sample
covers only the preceding interval. CPU % is 100 per fully used core. On
Linux, CPU is the delta of utime plus stime from `/proc/<pid>/stat` over the
whole process tree of each root PID. The tree is resolved again at every
sample, so forked processes are included. On macOS, the sampler uses the delta
of cumulative `ps` CPU time with 10 ms resolution. This is best effort and
labeled as such in `cpu_method`.

The root is the `dexcli dev` process, so server-zd and its embedded s3local are
included. `dexbench` also samples its own process as the load host. The report
shows that CPU as a share of all load-host cores.

### Result metadata

Each JSON contains the configuration, server and SDK versions, the Git commit,
machine details, attempts, successes, measured errors, and unmeasured warmup
or cleanup errors. It also has throughput over the actual elapsed time, exact
latency percentiles, per-interval CPU and RSS samples, and server-zd
object-store and commit metrics when available. Object storage requests per minute are
reported per operation (GET, PUT, LIST, DELETE) and per object kind. `host.txt`
records the CPU model, core count, RAM, and free disk.

Each JSON also records the measurement start and end times, the load host CPU
samples, the connection count, the trial number, and the server settings that
the point overrides.

### AWS with real S3

The AWS mode runs server-zd on one EC2 host against an S3 bucket. `dexbench`
runs on a second host in the same placement group. For every point, the load
host connects to the server host over SSH. It stops the server, deletes the
local store, and starts the server with a new object storage prefix,
`<run id>/<point id>`. Every point therefore starts from empty state.

The scripts live in `benchmark/aws/`:

| Script | Runs on | Does |
| --- | --- | --- |
| `launch.sh` | your machine | Creates the bucket, endpoint, IAM role, security groups, placement group, and instances |
| `serve.sh` | server host | Writes the server-zd config, starts and stops the server, and collects server-side data |
| `run-remote.sh` | load host | Runs every point and writes the result set |
| `reproduce.sh` | load host | Reruns points of a recorded result set |
| `teardown.sh` | your machine | Deletes every resource of the run and checks that none remains |

#### Prerequisites

- The AWS CLI v2 and `openssl` on your machine, with a profile that can manage
  EC2, S3, and IAM.
- An EC2 key pair in the region.
- Linux arm64 builds of `dexbench` and `dex-server-zd`. Both hosts run Ubuntu
  24.04, so build both binaries on Ubuntu 24.04 arm64, such as on the load
  host, to match its glibc. Build `dexbench` with
  `cd benchmark/dexbench && go build -buildvcs=true -o ../bin/dexbench ./cmd/dexbench`, so
  `config.env` records its revision.
- The `DEXBENCH_ZD_APPLIER_LANES`, `DEXBENCH_ZD_APPLIER_QUEUE_FRAMES`,
  `DEXBENCH_ZD_MAX_BATCH_RECORDS`, `DEXBENCH_ZD_MAX_UNAPPLIED_BYTES`,
  `DEXBENCH_ZD_MAX_BACKGROUND_JOBS`, and `DEXBENCH_ZD_WRITE_BUFFER_BYTES`
  settings need a server-zd build that includes PR #1110. Older builds reject
  those keys at startup.

#### Launch

Put both hosts in one Availability Zone and one cluster placement group.
Instances are Spot by default; set `DEXBENCH_MARKET=on-demand` for an
on-demand instance, such as a load host larger than the Spot vCPU quota. The
server is a c7gd instance, so RocksDB runs on its local NVMe instance store.
A c7gd instance has the same Graviton3 CPU, vCPU count, and memory as the c7g
of the same size. The load host needs no instance store. S3 traffic goes
through a free gateway endpoint. The bucket expires objects after one day.
If an Availability Zone has no capacity for the c7gd type, launch the server
first in another zone, then put the load host in the same zone and placement group.

```bash
export DEXBENCH_RUN_ID=2026-10-30-s3 DEXBENCH_AWS_PROFILE=<profile> DEXBENCH_AWS_REGION=us-west-2
export DEXBENCH_AZ=us-west-2b DEXBENCH_KEY_NAME=<key pair>
benchmark/aws/launch.sh infra
DEXBENCH_SERVER_INSTANCE_TYPE=c7gd.large benchmark/aws/launch.sh server
DEXBENCH_MARKET=on-demand DEXBENCH_LOAD_INSTANCE_TYPE=c7g.8xlarge benchmark/aws/launch.sh load
```

`launch.sh infra` writes the resource IDs and the bucket name to
`~/.dexbench-<run id>.env`. The server security group accepts SSH, the API
port 8801, and the metrics port 9901 from the load host only. SSH from your
machine is open to your current IP address.

Each instance runs `shutdown -h +150` at boot and terminates on shutdown. If
you lose your connection, the instances delete themselves after two and a half
hours. Set `DEXBENCH_SHUTDOWN_MINUTES` to change that limit. The load host
deletes its results with it, so copy the result directory off the load host
before its timer runs out.

#### Run

Wait for boot setup to finish on both hosts with `cloud-init status --wait`.
It mounts the instance store, sets the core dump path, and installs `iostat`,
`eu-stack`, and perf.

Copy `dexbench`, `dex-server-zd`, and `serve.sh` to `~/dexbench/` on the
server host. Clone this repository on the load host at the commit under test,
and copy `dexbench` there. Connect to the load host with `ssh -A`, so it can
reach the server with your key. Then run from the clone on the load host:

```bash
export DEXBENCH_SERVER_HOST=ubuntu@<server private IP>
export DEXBENCH_SERVER_ADDRESS=<server private IP>
export DEXBENCH_S3_BUCKET=<bucket from launch.sh>
export DEXBENCH_BENCH_BINARY=$HOME/dexbench/dexbench
export DEXBENCH_OUT=$HOME/results/2026-10-30-aws-s3-c7gd-large
export DEXBENCH_SERVER_ZD_COMMIT=<commit the server binary was built from>
DEXBENCH_SCENARIOS=sequential-steps DEXBENCH_CONCURRENCY=1,128,256,512 \
  benchmark/aws/run-remote.sh
```

Each point uses a 10-second warmup and a 45-second measurement by default.
A source build of server-zd reports only its crate version, so set
`DEXBENCH_SERVER_ZD_COMMIT` to record its commit in `config.env`. The
`dexbench` revision comes from the binary itself. Copy the result directory
back with `scp -r` and commit it under `benchmark/results/`.

A point that fails, for example because the server exits during startup, is
counted and skipped, and the run continues. If `dexbench` itself fails, its
JSON is kept as `<point id>.failed.json` and left out of the report. Each
invocation writes to a new object storage prefix, `<run id>/<invocation>/<point
id>`, recorded as `RECORDED_S3_PREFIX_ROOT` in `config.env`.

#### Points and server settings

Without `DEXBENCH_POINTS`, the script runs every scenario in
`DEXBENCH_SCENARIOS` at every concurrency in `DEXBENCH_CONCURRENCY`. For a
tuning sweep, list the points in a file and set `DEXBENCH_POINTS` to its path.
Each line names a scenario, a concurrency, and optional server settings for
that point:

```text
# scenario concurrency [DEXBENCH_ZD_NAME=value ...]
sequential-steps 512
sequential-steps 512 DEXBENCH_ZD_MAX_IN_FLIGHT=8
sequential-steps 512 DEXBENCH_ZD_MAX_IN_FLIGHT=16 DEXBENCH_ZD_BATCH_WINDOW=5ms
```

Settings exported on the load host apply to every point. Settings on a line
override them for that point. `serve.sh` writes a key into the server-zd
config only when its variable is set:

| Variable | server-zd key |
| --- | --- |
| `DEXBENCH_ZD_PARTITIONS` | `cluster.partitionCount` |
| `DEXBENCH_ZD_MAX_IN_FLIGHT` | `commit.maxInFlight` |
| `DEXBENCH_ZD_BATCH_WINDOW` | `commit.batchWindow` |
| `DEXBENCH_ZD_APPLIER_LANES` | `commit.applierLanes` |
| `DEXBENCH_ZD_APPLIER_QUEUE_FRAMES` | `commit.applierQueueFrames` |
| `DEXBENCH_ZD_MAX_BATCH_RECORDS` | `commit.maxBatchRecords` |
| `DEXBENCH_ZD_MAX_UNAPPLIED_BYTES` | `commit.maxUnappliedBytes` |
| `DEXBENCH_ZD_MAX_BACKGROUND_JOBS` | `localStorage.maxBackgroundJobs` |
| `DEXBENCH_ZD_WRITE_BUFFER_BYTES` | `localStorage.writeBufferBytes` |
| `DEXBENCH_ZD_BLOCK_CACHE_BYTES` | `localStorage.blockCacheBytes` |
| `DEXBENCH_ZD_MAX_INFLIGHT_RPCS` | `limits.maxInFlightRpcsPerNode` |

For any other key, set `DEXBENCH_ZD_EXTRA` to comma-separated
`section.key=value` pairs, such as `snapshot.interval=1h`. server-zd rejects
unknown keys at startup, so a wrong key fails fast. If a key is renamed, edit
the table at the top of `serve.sh`.

Other settings:

| Variable | Default | Meaning |
| --- | --- | --- |
| `DEXBENCH_TRIALS` | `1` | Runs of each point; the report adds the median and maximum |
| `DEXBENCH_CONNECTIONS` | `1` | Client and Worker pairs; raise it so the load host is never the limit |
| `DEXBENCH_DURATION`, `DEXBENCH_WARMUP` | `45`, `10` | Seconds per point |
| `DEXBENCH_REMOTE` | `ssh` | `local` runs the server-side commands on this host |
| `DEXBENCH_SERVER_HOME` | `dexbench` | Directory of `serve.sh` on the server host |
| `DEXBENCH_STORAGE_DIR` | `/mnt/nvme/dexzd` | RocksDB directory on the server host; deleted at every point |
| `DEXBENCH_S3_REGION` | `us-west-2` | Bucket region |
| `DEXBENCH_S3_ENDPOINT` | empty | Custom S3 endpoint, such as a local s3local |
| `DEXBENCH_S3_CREDENTIALS` | `defaultChain` | `defaultChain` uses the instance profile |
| `DEXBENCH_PROFILE` | `1` | `1` profiles the server in the middle of each measurement; `0` turns it off |
| `DEXBENCH_PROFILE_SECONDS`, `DEXBENCH_PROFILE_FREQUENCY` | `10`, `49` | Length and sampling rate (Hz) of the perf profile |

#### What each point collects

Next to `<point id>.json`, the directory `<point id>.server/` holds:

- `zd-config.yaml`: the exact server-zd config of the point.
- `server-samples.json`: server host CPU and RSS every second. They are merged
  into the JSON for the measurement window only.
- `metrics-before.txt` and `metrics-after.txt`: the raw server `/metrics`
  output. They cover the warmup and the measurement.
- `iostat.txt`: `iostat -dxmy 5` for the whole point, on Linux.
- `rocksdb-stalls.txt`: counts of `Stalling writes` and `Stopping writes` in
  each RocksDB `LOG` file.
- `server.log`: the server log.
- `perf-report.txt` and `perf-script.txt.gz`: a perf profile with DWARF call
  stacks, taken a third of the way into the measurement. `perf-script.txt.gz`
  can be turned into a flame graph off the host.
- `thread-stacks-1.txt` and `thread-stacks-2.txt`: `eu-stack` stacks of every
  server thread, before and after the profile. Two snapshots show which
  threads stay blocked in the same place.
- `crash.txt`: written only when the server was not running at the end of the
  point. It holds the backtrace of each core dump. The cores stay in
  `/mnt/nvme/cores` on the server host, so copy one off before teardown when
  you need it.

The profile slows the server while it runs: `eu-stack` pauses every thread
briefly, and perf copies each sampled stack. The profile is symbolized after
the measurement. Set `DEXBENCH_PROFILE=0` for the trials whose numbers you
report.

Only cores written during the point are reported. Cores are never deleted, so
a series of crashes can fill `/mnt/nvme`.

`report.md` shows server CPU, its share of the server host, the load host CPU
share, and PUT requests per second. A point whose load host CPU is above 70%
is marked invalid, because the load generator may have been the bottleneck.

#### Reproduce a result

`run-remote.sh` writes `config.env`, `zd-config.yaml`, `points.txt`, and
`host.txt` into every result set. To rerun a point on new hosts, set the host
and bucket variables as above and run:

```bash
benchmark/aws/reproduce.sh benchmark/results/<run> sequential-steps 512
```

It loads the recorded settings from `config.env` and the matching lines from
`points.txt`. Omit the scenario and concurrency to rerun every point.

#### Test without AWS

Run the same loop on one machine against a standalone s3local:

```bash
(cd s3local && cargo build --release -p s3local-server)
s3local/target/release/s3local --listen-address 127.0.0.1:9100 --data-directory /tmp/s3data \
  --signature-verification strict &
curl --aws-sigv4 aws:amz:us-east-1:s3 --user s3local:s3local -X PUT \
  -H 'x-amz-content-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855' \
  http://127.0.0.1:9100/dexbench
DEXBENCH_REMOTE=local DEXBENCH_SERVER_ZD_BINARY=server-zd/target/release/dex-server-zd \
  DEXBENCH_SERVER_BENCH_BINARY="$PWD/benchmark/bin/dexbench" \
  DEXBENCH_STORAGE_DIR=/tmp/dexbench-store DEXBENCH_STATE_DIR=/tmp/dexbench-state \
  DEXBENCH_LISTEN_HOST=127.0.0.1 DEXBENCH_S3_BUCKET=dexbench DEXBENCH_S3_REGION=us-east-1 \
  DEXBENCH_S3_ENDPOINT=http://127.0.0.1:9100 DEXBENCH_S3_CREDENTIALS=environment \
  DEX_ZD_OBJECT_STORE_ACCESS_KEY_ID=s3local DEX_ZD_OBJECT_STORE_SECRET_ACCESS_KEY=s3local \
  DEXBENCH_DURATION=10 DEXBENCH_WARMUP=2 benchmark/aws/run-remote.sh
```

The s3local region is `us-east-1`. Its numbers are not results.

#### Clean up

Terminate the server after its measurements, before you launch the next size.
This keeps the load host and its results:

```bash
benchmark/aws/teardown.sh server
```

`teardown.sh instances` terminates every instance of the run.

After the last run, delete everything:

```bash
benchmark/aws/teardown.sh
```

It terminates the instances and deletes the placement group, security groups,
gateway endpoint, IAM role and instance profile, and the bucket with its
objects. It deletes only the bucket named in `~/.dexbench-<run id>.env` or
`DEXBENCH_S3_BUCKET`, and only when its `dex-benchmark` tag matches the run. It then lists each resource type by tag or name and fails if anything
remains. An S3 gateway endpoint that existed before `launch.sh` is left in
place.

## S7 search backend benchmark

S7 runs separately from the timed execution scenarios. Use a fresh server and
empty search database for each point. Build the release server and harness:

```bash
(cd server-zd && cargo build --release -p dexzd-server)
(cd benchmark/dexbench && GOWORK=off go build -o ../bin/dexbench ./cmd/dexbench)
benchmark/local/search-local.sh builtin 100000 10 /tmp/search-builtin-100k
benchmark/local/search-local.sh paradedb 100000 10 /tmp/search-paradedb-100k
benchmark/local/search-local.sh postgres 100000 10 /tmp/search-postgres-100k
benchmark/local/search-local.sh postgres 100000 10 /tmp/search-backfill-100k --backfill
```

The runner starts a release server with embedded s3local, processCrashSafe
durability, and the default 1,024 partitions. External backends use temporary, uniquely named Docker containers:
PostgreSQL 17 or ParadeDB 0.26.1, matching server-zd CI. It removes its server,
data directory, and container on exit. It does not use docker compose.
`DEXBENCH_PARTITIONS`, `DEXBENCH_SEARCH_SCAN_BUDGET` (100000),
`DEXBENCH_SEARCH_SAMPLES` (default 100),
`DEXBENCH_SEARCH_CONCURRENCY` (128), `DEXBENCH_SEARCH_PORT` (18815), and
`DEXBENCH_SEARCH_METRICS_PORT` (19195) override runner settings.

Each point seeds N unique runs in one tenant. Initial indexed Attribute writes
are followed by a short completing Step; 5% proceed to a waiting Step instead.
Flow types are about 80% common, 19.95% secondary, and 0.05% rare. Keyword
values are about 80% common, 19.9% secondary, and 0.1% rare. Int values cycle
through 0–99,999; KeywordArray has a shared element and one of 100 tags; Text
has three shared words and one of 100 topic terms. The ten-Attribute case adds
six Keywords to those four fields. The three-Attribute case omits Text.

Seed throughput is N divided by elapsed time from the first start through
projection convergence. It includes Flow execution, so it measures achieved
projection throughput under this workload, not the projection's maximum
capacity in isolation. Every completing Flow is awaited. Builtin search reads
the committed local view; external search additionally waits for exact row
and Completed/Running counts. Initial PostgreSQL slots must be ready before
seeding. Run 0, 3, and 10 Attributes on separate fresh servers:

```bash
for attributes in 0 3 10; do
  benchmark/local/search-local.sh postgres 100000 "$attributes" "/tmp/search-pg-$attributes"
done
```

Zero- and three-Attribute runner points measure projection only. The ten-Attribute
point measures every query at page size 20, with ten warmups and 100 sequential
samples: default order, common/rare Flow type, Keyword equality, Int range,
KeywordArray membership, Text terms, explicit StartTime descending, and page
500 reached by following 499 opaque next-page tokens. Only the final page
request is timed. These are end-to-end Go Client timings, including transport
and indexed-value hydration; compare them cautiously with SQL EXPLAIN results.
Raw JSON retains each successful latency and each error. PostgreSQL ten-Attribute
points also retain matching SQL EXPLAIN ANALYZE plans after the timed queries. Builtin scan-budget
failures are recorded. For full-scan timings above 100k runs, set
`DEXBENCH_SEARCH_SCAN_BUDGET` to the dataset size and record that override.

PostgreSQL and ParadeDB record pg_current_wal_lsn before and after seeding and
catchup. Their difference divided by N includes all database WAL in that
interval, including updates, checkpoints, and background maintenance. The
container contains only this experiment's database.

The separate `--backfill` run withholds Keyword registration while writing its
indexed values. After catchup it registers that key, observes backfilling,
and measures rare and common equality while the slot is still backfilling.
A sample is retained only when state probes bracket it with backfilling. Failed
requests receive the same probes; errors spanning readiness are recorded as
`transition_errors`, and errors never end sampling without an observed state change.
An independent readiness monitor measures registration-to-ready time. It then
measures the same equality predicates with the ready slot. This diagnostic
point has nine registered Attributes during seeding; exclude its seed throughput
and WAL from the ordinary ten-Attribute comparison.

For an already running isolated server, invoke `dexbench search --help`.
Use `--projection-only` to omit query timings. Small smoke runs must reduce
`--deep-page` to fit their dataset. Result sets are local measurements and
should state host contention, versions, Docker resources, and method explicitly.
