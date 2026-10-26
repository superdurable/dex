# Dex server benchmark

`dexbench` uses the Go SDK and an in-process pull Worker pool to compare the Go
Dex Server on Temporal with server-zd. It runs one target at a time and writes
one JSON file per target and scenario.

## Prepare an Ubuntu 22.04 or 24.04 instance

Install Go 1.26 or newer from [go.dev/dl](https://go.dev/dl/), the
[Temporal CLI](https://docs.temporal.io/cli), and `git`. Prod mode also needs
[Docker Engine with Compose](https://docs.docker.com/engine/install/ubuntu/)
and AWS credentials that the default AWS SDK chain can read.

```bash
git clone https://github.com/superdurable/dex.git
cd dex
go version
temporal --version
go build -o cli/dexcli ./cli/cmd/dexcli
cd server-zd && cargo build --release -p dexzd-server && cd ..
export DEXBENCH_DEXCLI_BINARY="$PWD/cli/dexcli"
export DEXBENCH_ZD_BINARY="$PWD/server-zd/target/release/dex-server-zd"
```

The latest `server-zd/v*` GitHub release also provides a Linux binary. Replace
the source build with its `dex-server-zd_v*_linux_amd64.tar.gz` asset when
appropriate. The release binary and the source build both work with `run.sh`.

## Run

```bash
benchmark/run.sh quick
```

Quick mode starts each server through `dexcli dev`. The Go target uses the
SQLite-backed Temporal CLI dev server. The ZD target uses its embedded S3
emulator. Both use fresh local state. The default is 60 seconds of measurement
and 10 seconds of warmup for each of six scenario rows per target, so allow
about 15–20 minutes including startup.

Prod mode starts Temporal and PostgreSQL using the existing
`server/docker-compose/integ-dependencies.yml` file. It runs the Go Dex Server
through `dexcli dev --external-temporal-address`, then runs
`dex-server-zd serve` against a real S3 bucket. The prefix gains a unique
timestamp and the ZD local disk directory is new for every run. The script
stops the server and Compose stack on exit; it does not delete S3 objects.

```bash
export AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=...
export DEXBENCH_S3_BUCKET=your-bucket
export DEXBENCH_S3_PREFIX=dex-benchmark
export DEXBENCH_S3_REGION=us-east-1
benchmark/run.sh prod
```

Use instance roles or other default-chain credentials instead of access-key
environment variables when available. Restrict the bucket to the benchmark
prefix. For either mode, use `DEXBENCH_DURATION`, `DEXBENCH_WARMUP`,
`DEXBENCH_CONCURRENCY`, `DEXBENCH_WORKERS`, `DEXBENCH_STEPS`,
`DEXBENCH_SCENARIOS`, `DEXBENCH_TARGETS` and `DEXBENCH_OUT` to change the run.
`DEXBENCH_TARGETS=zd` runs only ZD. `--short` on `dexbench run` uses five seconds
per scenario, one second of warmup, and two clients and Worker slots.

The script prints its result directory. Render a comparison from its JSON
files:

```bash
benchmark/bin/dexbench report benchmark/results/<run-directory>/*.json
```

Each file includes operation counts and errors, throughput, latency percentiles,
server and SDK identity, machine details, one-second CPU and RSS samples, and
ZD object-store and commit metrics when the metrics endpoint is enabled.
`start-complete` measures StartFlow through WaitForFlow; `sequential-steps` and
`async-steps` measure K-Step chains; `rpc` produces plain and Attribute-locked
RPC rows; `channel-wake` measures publication through the waiting Step's Execute
start. Chain Step latency measures one Execute start to the next, including
server scheduling. The publish timestamp and Execute timestamp share the same
benchmark process clock.

Use the same instance, concurrency, Worker pool, duration, and Step count for
both targets. Quick mode is a developer comparison: Temporal uses SQLite and ZD
uses an embedded object store. Prod mode uses PostgreSQL-backed Temporal and
real S3 for ZD, but the Go Dex Server still runs under `dexcli dev`; its local
blob store and the two storage designs are different. CPU and RSS cover the
specified Dex server process; they do not include Temporal, PostgreSQL, or S3.
The benchmark does not currently implement the optional log-size recovery
scenario.
