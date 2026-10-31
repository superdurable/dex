# Dex server benchmark

`dexbench` measures server-zd with a Go SDK Worker. It runs six scenarios at
fixed client concurrency and records throughput, latency percentiles, server
CPU and memory, and object storage requests for each run.

## Latest results

Results from a run on AWS with real S3 are pending. Until they are committed,
this repository has no published result set.

Each result set will live in `benchmark/results/<run>/`. Next to the JSON
files, `config.env` and `zd-config.yaml` record the instance types,
placement, disk, server-zd version and configuration, and the exact
`dexbench` settings. `points.txt` lists every point. Rerun any point with
`benchmark/aws/reproduce.sh`; see [Reproduce a result](#reproduce-a-result).

## Reading the results

Each run writes one JSON file per scenario variant and concurrency, plus
`report.md`, `charts.html`, and `host.txt`. `report.md` has one table per
variant. `charts.html` is a self-contained page that plots each metric against
concurrency.

### Scenarios

| ID | Variant | What it measures |
| --- | --- | --- |
| S1 | `start-complete` | Start and complete a Flow with one Step |
| S2 | `sequential-steps` | Ten sequential SYNC Steps in one Flow |
| S3 | `sequential-steps-async` | Ten sequential ASYNC Steps in one Flow |
| S4 | `rpc-plain`, `rpc-locking` | A plain RPC, and an RPC that locks one Attribute |
| S5 | `attribute-read-direct` | Read ten 16 KiB Attributes from the client |
| S6 | `channel-wake` | Latency from a Channel publish to the waiting Step |

The scenario name `rpc` runs both S4 variants. The name `attribute-read` runs
S5.

**S1 `start-complete`.** Each client starts a new Flow whose only Step
completes it. The client then waits for the Flow result. The timed operation
is StartFlow plus WaitForFlow. Throughput is in Flows per second. Every
operation creates a new Flow.

**S2 `sequential-steps`.** Each client starts a new Flow that runs ten SYNC
Steps one after another. Each Step moves to the next with GoTo. The timed
operation is the whole Flow, from StartFlow to the result of WaitForFlow.
Throughput is in Steps per second: completed Flows times ten. The report also
shows per-Step latency. That is the gap between the start of one Step's
Execute and the start of the next one, measured inside the Worker. Every
operation creates a new Flow. Set the Step count with `DEXBENCH_STEPS`.

**S3 `sequential-steps-async`.** This is S2 with ASYNC Step durability. A SYNC
Step result is committed to object storage before the Flow moves on. An ASYNC
result lets the Flow move on first, and the commit follows in a later batch.
If the server node fails before that commit, the Step runs again, and so does
any work that started from it. Timing, throughput unit, and per-Step latency
are the same as in S2. Starting and completing the Flow still need commits, so
the Flow latency of S3 still includes commit time. Per-Step latency shows the
effect of ASYNC most directly.

**S4 `rpc-plain` and `rpc-locking`.** Before warmup, each client starts one
Flow that stays open. The plain variant invokes an RPC that reads an
Attribute. The locking variant invokes an RPC that locks the Attribute, reads
it, and writes it back. Each client uses its own Flow, so the lock has no
contention. The timed operation is one InvokeRPC call. Throughput is in RPCs
per second. Keep the concurrency below the server's
`limits.maxInFlightRpcsPerNode`, which defaults to 1000.

**S5 `attribute-read-direct`.** Before warmup, each client starts one Flow
that stays open and holds ten 16 KiB string Attributes. Each read calls
FlowService GetAttributes through the generated gRPC client. Values over the
server's inline limit come back as blob references. The client resolves every
reference with one LoadBlobs call, as an uncached client would. It then
compares every value with the value written at Flow start. The timed
operation is GetAttributes plus LoadBlobs plus the check. Throughput is in
reads per second. JSON results record the verified bytes per read and the
number of blobs loaded.

**S6 `channel-wake`.** Each client starts a new Flow whose Step waits on a
Channel. After the Step's WaitFor returns, the client polls GetFlowState until
the Step is waiting. Gating the poll on WaitFor keeps it near one
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
  benchmark/run.sh
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

`run.sh` starts server-zd through `dexcli dev` with its embedded s3mock. It
does not start Docker or use AWS.

### Full local run

```bash
benchmark/run.sh
benchmark/bin/dexbench report <result-directory>/*.json
benchmark/bin/dexbench chart <result-directory>/*.json > charts.html
```

By default the script writes to a new temporary directory outside the
repository and prints its path at the end. `benchmark/results/` holds only
committed result sets from a recorded cloud host. To write one, set
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
| `DEXBENCH_STEPS` | `10` | Steps per S2 and S3 Flow |
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
data directory. S4 and S5 start their Flows before warmup, so the warmup and the
measurement of one step share those Flows. S1, S2, S3, and S6 start a new Flow
for every operation.

### Resource sampling

CPU and RSS are sampled every second during the measurement. Each sample
covers only the preceding interval. CPU % is 100 per fully used core. On
Linux, CPU is the delta of utime plus stime from `/proc/<pid>/stat` over the
whole process tree of each root PID. The tree is resolved again at every
sample, so forked processes are included. On macOS, the sampler uses the delta
of cumulative `ps` CPU time with 10 ms resolution. This is best effort and
labeled as such in `cpu_method`.

The root is the `dexcli dev` process, so server-zd and its embedded s3mock are
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
  `cd benchmark && go build -buildvcs=true -o dexbench ./cmd/dexbench`, so
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
| `DEXBENCH_S3_ENDPOINT` | empty | Custom S3 endpoint, such as a local s3mock |
| `DEXBENCH_S3_CREDENTIALS` | `defaultChain` | `defaultChain` uses the instance profile |

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

Run the same loop on one machine against a standalone s3mock:

```bash
(cd s3mock && cargo build --release -p s3mock-server)
s3mock/target/release/s3mock --listen-address 127.0.0.1:9100 --data-directory /tmp/s3data \
  --signature-verification strict &
curl --aws-sigv4 aws:amz:us-east-1:s3 --user s3mock:s3mock -X PUT \
  -H 'x-amz-content-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855' \
  http://127.0.0.1:9100/dexbench
DEXBENCH_REMOTE=local DEXBENCH_SERVER_ZD_BINARY=server-zd/target/release/dex-server-zd \
  DEXBENCH_SERVER_BENCH_BINARY="$PWD/benchmark/bin/dexbench" \
  DEXBENCH_STORAGE_DIR=/tmp/dexbench-store DEXBENCH_STATE_DIR=/tmp/dexbench-state \
  DEXBENCH_LISTEN_HOST=127.0.0.1 DEXBENCH_S3_BUCKET=dexbench DEXBENCH_S3_REGION=us-east-1 \
  DEXBENCH_S3_ENDPOINT=http://127.0.0.1:9100 DEXBENCH_S3_CREDENTIALS=environment \
  DEX_ZD_OBJECT_STORE_ACCESS_KEY_ID=s3mock DEX_ZD_OBJECT_STORE_SECRET_ACCESS_KEY=s3mock \
  DEXBENCH_DURATION=10 DEXBENCH_WARMUP=2 benchmark/aws/run-remote.sh
```

The s3mock region is `us-east-1`. Its numbers are not results.

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
