# Dex server benchmark

`dexbench` measures server-zd with a Go SDK Worker. It runs six scenarios at
fixed client concurrency and records throughput, latency percentiles, server
CPU and memory, and object storage requests for each run.

## Latest results

Results from a run on AWS with real S3 are pending. Until they are committed,
this repository has no published result set.

Each result set will live in `benchmark/results/<run>/`. The AWS mode, added
separately, will also write `config.env` and `zd-config.yaml` next to the JSON
files. They will record the instance types, placement, disk, server-zd
version and configuration, and the exact `dexbench` command, so any headline
number can be run again.

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
included.

### Result metadata

Each JSON contains the configuration, server and SDK versions, the Git commit,
machine details, attempts, successes, measured errors, and unmeasured warmup
or cleanup errors. It also has throughput over the actual elapsed time, exact
latency percentiles, per-interval CPU and RSS samples, and server-zd
object-store and commit metrics when available. Object storage requests per minute are
reported per operation (GET, PUT, LIST, DELETE) and per object kind. `host.txt`
records the CPU model, core count, RAM, and free disk.
