# Dex server benchmark

`dexbench` compares server-zd and the Go Server with the same Go SDK, Worker
configuration, machine, warmup, measurement window, and concurrency. It runs one
server at a time. Quick mode uses local development servers and gives a rough
comparison. Prod mode uses a dedicated EC2 host, PostgreSQL-backed Temporal,
and real S3. Use prod results for performance conclusions.

Each run writes one JSON file per scenario, target, and concurrency, plus
`report.md`, `charts.html`, and `host.txt`. `charts.html` is a self-contained
page that plots both targets against concurrency for each scenario. The default directory is
`benchmark/results/<YYYY-MM-DD>-<mode>-<host label>/`. The host label is the
instance type in prod mode and `<os>-<arch>` in quick mode; set
`DEXBENCH_HOST_LABEL` to override it. The temporary server data and logs are
removed when the script exits.

`benchmark/results/` holds only committed result sets: full quick runs on a
recorded cloud host (mode A) and full prod runs on the dedicated EC2 host
(mode B). A 10-second smoke run on a laptop is not a result. Write smoke output
outside the repository with `DEXBENCH_OUT`, and never commit it.

## Quick mode: fresh Ubuntu 22.04 or 24.04

Install Git, curl, jq, and Python with apt. Install Go and the Temporal CLI in
your home directory; the remaining steps need no sudo. Use Go 1.26 or newer.

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl git jq python3 tar xz-utils
mkdir -p "$HOME/.local"
go_archive="$(curl -fsSL 'https://go.dev/dl/?mode=json' | jq -r '.[0].files[] | select(.os=="linux" and .arch=="amd64" and .kind=="archive") | .filename' | head -1)"
curl -fsSL "https://go.dev/dl/$go_archive" -o "/tmp/$go_archive"
tar -C "$HOME/.local" -xzf "/tmp/$go_archive"
export PATH="$HOME/.local/go/bin:$HOME/.local/bin:$HOME/.temporalio/bin:$PATH"
curl -fsSL https://temporal.download/cli.sh -o /tmp/temporal-cli-install.sh
bash /tmp/temporal-cli-install.sh
go version
temporal --version
```

Clone Dex and install a recent `dexcli`. A release binary works if
`dexcli dev --help` lists `-engine`. A source build from this checkout is the
most direct option. Building from source also requires Node.js 22 and npm to
compile the embedded Web assets. Check `uptime` before each build and wait until
its one-minute load is below 24:

```bash
git clone https://github.com/superdurable/dex.git
cd dex
mkdir -p "$HOME/.local/bin"
node_archive="$(curl -fsSL https://nodejs.org/dist/latest-v22.x/SHASUMS256.txt | awk '$2 ~ /linux-x64.tar.xz$/ {print $2; exit}')"
curl -fsSL "https://nodejs.org/dist/latest-v22.x/$node_archive" -o "/tmp/$node_archive"
mkdir -p "$HOME/.local/node"
tar -xJf "/tmp/$node_archive" -C "$HOME/.local/node" --strip-components=1
export PATH="$HOME/.local/node/bin:$PATH"
(cd web && nice -n 10 npm ci && nice -n 10 npm run build)
(cd cli && nice -n 10 go build -o "$HOME/.local/bin/dexcli" ./cmd/dexcli)
export DEXBENCH_DEXCLI_BINARY="$HOME/.local/bin/dexcli"
```

Install the newest server-zd release binary in the same directory. The
commands select the asset for the host architecture.

```bash
zd_arch="$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')"
zd_tag="$(curl -fsSL 'https://api.github.com/repos/superdurable/dex/releases?per_page=100' | jq -r '.[] | select(.draft == false and .prerelease == false and (.tag_name | startswith("server-zd/v"))) | .tag_name' | sort -V | tail -1)"
zd_version="${zd_tag#server-zd/}"
curl -fsSL "https://github.com/superdurable/dex/releases/download/$zd_tag/dex-server-zd_${zd_version}_linux_${zd_arch}.tar.gz" -o /tmp/dex-server-zd.tar.gz
tar -xzf /tmp/dex-server-zd.tar.gz -C "$HOME/.local/bin" dex-server-zd
export DEXBENCH_ZD_BINARY="$HOME/.local/bin/dex-server-zd"
```

Quick mode starts both targets through `dexcli dev`. The ZD target uses its
embedded s3mock; the Go target uses the Temporal CLI dev server with SQLite.
It does not start Docker or use AWS. Check that the host has enough free disk
for recovery. The script records CPU model, core count, RAM, and free disk. It
scales recovery log targets from 100/500/1000 MiB to 10/50/100 MiB below 10
GiB free, and skips recovery below 2 GiB.

```bash
benchmark/run.sh quick
benchmark/bin/dexbench report benchmark/results/<run-directory>/*.json
benchmark/bin/dexbench chart benchmark/results/<run-directory>/*.json > charts.html
```

For a short local smoke of all scenarios on ZD, use the command below. It only
checks that the scenarios run; its numbers are not results.

```bash
DEXBENCH_TARGETS=zd DEXBENCH_DURATION=10 DEXBENCH_WARMUP=1 \
  DEXBENCH_CONCURRENCY=2 DEXBENCH_RECOVERY_MB=1 DEXBENCH_OUT="$(mktemp -d)" \
  benchmark/run.sh quick
```

Quick mode defaults to a 15-second warmup and a 1-minute measurement at
concurrency 1 and 32. It runs 9 scenario variants at 2 concurrency levels, 18
steps per target. Each step takes 75 seconds plus a few seconds of server
restart, so expect about 25 minutes per target. With S8 seeding and replaying
up to 1000 MiB, the default quick run takes about 1 hour. For the full sweep,
set `DEXBENCH_DURATION=180 DEXBENCH_WARMUP=30 DEXBENCH_CONCURRENCY=1,8,32,128`;
it takes about 4.5 hours.

## Prod mode: dedicated EC2 host

### Create the AWS resources

Run these from a workstation with AWS credentials for the benchmark account.
The scripts take the region and a name, contain no account IDs or secrets, and
tag every resource `dex-benchmark=<name>`. They create a private S3 bucket, an
instance role and instance profile that can reach only that bucket plus
Systems Manager, a security group with no ingress, and one `m6id.2xlarge`
Ubuntu 24.04 instance with IMDSv2. Set `DEXBENCH_INSTANCE_TYPE` to use another
instance type with instance-store NVMe.

```bash
benchmark/aws/create.sh us-west-2 dexbench-run1
aws ssm start-session --region us-west-2 --target <instance-id>
```

`create.sh` prints the bucket name and instance ID. It is safe to rerun; it
reuses resources that already exist. Before it modifies an existing role,
instance profile, or bucket, it checks that the resource carries the tag
`dex-benchmark=<name>`, and it refuses otherwise. It finds instances and
security groups only by that tag.

### Prepare the host

In the SSM session, switch to the `ubuntu` user, clone Dex, and mount the
instance-store NVMe. `mount-nvme.sh` selects the disk whose model is
`Amazon EC2 NVMe Instance Storage`, so it never formats an EBS volume. It
formats the disk as ext4, mounts it at `/mnt/nvme`, and sets Docker's
`data-root` to `/mnt/nvme/docker`. Temporal's PostgreSQL and Elasticsearch data
then sit on the same NVMe as the server-zd data directory. Run it before
`host-setup.sh`, which refuses to install Docker without it.

```bash
sudo -iu ubuntu
git clone https://github.com/superdurable/dex.git && cd dex
benchmark/prod/mount-nvme.sh
benchmark/prod/host-setup.sh
newgrp docker
```

`host-setup.sh` installs Docker Compose, Go, the Temporal CLI, tmux, and the
newest `server-zd/v*` and `server/v*` releases. It downloads Linux release
assets. When a Go Server release has no binary asset, it builds that release
tag from source.

### Run inside tmux

A prod run takes hours, and an SSM session can drop. Start it inside tmux so
it keeps running, then detach with `Ctrl-b d`. Reattach later with
`tmux attach -t dexbench`.

```bash
tmux new -s dexbench
export DEXBENCH_S3_BUCKET=<bucket from create.sh>
export DEXBENCH_S3_PREFIX=run-$(date -u +%Y%m%dT%H%M%SZ)
export DEXBENCH_S3_REGION=us-west-2
export DEXBENCH_NVME_DIR=/mnt/nvme/work
benchmark/prod/run-prod.sh 2>&1 | tee "$HOME/dexbench-prod.log"
```

Set `DEXBENCH_TEMPORAL_VISIBILITY=sql` to run Temporal without Elasticsearch.
Temporal then keeps visibility records in its own PostgreSQL. The default,
`elasticsearch`, uses Temporal advanced visibility. Run the two settings into
separate result directories with `DEXBENCH_OUT`.

Without tmux, use
`nohup benchmark/prod/run-prod.sh > "$HOME/dexbench-prod.log" 2>&1 &`.

The prod script runs the standalone Go Server against Temporal with
PostgreSQL and Elasticsearch visibility from `server/docker-compose/`. It then
runs `dex-server-zd serve` on real S3. Both targets use a PostgreSQL Attribute
Store for the synchronized S7 variant. Both targets run at the default
scheduling priority. The script removes its Docker containers and local
temporary state on exit.

The prod sweep runs 10 scenario variants at 4 concurrency levels, 40 steps per
target. Each step takes 210 seconds of warmup and measurement. ZD restarts in a
few seconds, so its sweep takes about 2.4 hours. Temporal recreates PostgreSQL,
Elasticsearch, and Temporal for every step, which adds about 1 to 1.5 minutes,
so its sweep takes about 3 to 3.3 hours. S8 seeds and replays 100, 500, and
1000 MiB, which adds roughly 15 to 30 minutes. Plan for about 6 hours.

### Collect results and tear down

Copy `benchmark/results/<date>-prod-<instance type>/` off the host, for
example with `git` from the host or an S3 copy to the benchmark bucket. Then
remove every resource from the workstation:

```bash
benchmark/aws/destroy.sh us-west-2 dexbench-run1
```

`destroy.sh` terminates the instance and deletes the security group, the
instance profile, the role and its policies, and the bucket with its objects.
It is safe to rerun. It applies the same tag check and refuses to delete a
role, instance profile, or bucket without `dex-benchmark=<name>`. It ends by
running `benchmark/aws/leftovers.sh`, which
lists anything still tagged `dex-benchmark=<name>` and exits nonzero when
something remains.

## Configuration and metrics

Prod mode defaults to a 30-second warmup and a 3-minute measurement at
concurrency 1, 8, 32, and 128. Quick mode defaults to 15 seconds, 1 minute, and
concurrency 1 and 32. Set `DEXBENCH_DURATION`, `DEXBENCH_WARMUP`,
`DEXBENCH_CONCURRENCY` (comma-separated), `DEXBENCH_WORKERS`, `DEXBENCH_STEPS`,
`DEXBENCH_SCENARIOS`, `DEXBENCH_TARGETS`, `DEXBENCH_RECOVERY_MB`, or
`DEXBENCH_OUT` to override them. `DEXBENCH_TARGETS=zd` runs only ZD. Before
each build and step, the scripts wait until the one-minute load average is
below `DEXBENCH_MAX_LOAD`, which defaults to the core count.

S1 starts and completes a one-Step Flow. S2 and S3 run ten sequential SYNC or
ASYNC Steps; the report shows whole-Flow latency and per-Step latency, which is
the gap between consecutive Step executions. S4 measures plain and
Attribute-locked RPCs. Each client uses its own Flow, so the locked RPC has no
lock contention.

S5 reads ten 16 KiB Attributes. The RPC variant reads them inside a read-only
RPC on the Worker. The RPC compares every value with the value written at Flow
start and returns the count of matching values and their total size, which the
client checks. The direct variant calls FlowService GetAttributes through the
generated gRPC client. Values over the server's inline limit come back as blob
references, so it then resolves every reference with one LoadBlobs call, as an
uncached client would, and compares every value on the client. Both variants
therefore check the full content and size of every value on every read. The Worker serves the RPC variant from its local
blob cache after the first load, so the direct variant moves more bytes per
read. JSON results record the verified bytes per read and the number of blobs
loaded.

S6 starts a Flow whose Step waits on a Channel. After the Step's WaitFor
returns, it polls GetFlowState until that Step is waiting, and then publishes
with FlowService PublishToChannel. Gating the poll on WaitFor keeps it near one
GetFlowState call per wake, which matters because the Go Server answers it
with a Temporal query. Only
the time from the publish call to the start of the Step's Execute is measured.
Its throughput includes the untimed setup, so use only its latency.

S7 measures external SetAttributes writes with synchronization disabled and,
when a PostgreSQL Attribute Store is configured, enabled.

S8 runs only on ZD. It writes each configured log payload size, stops the
server, deletes the local state, and restarts. It measures a cold-node restart
that replays from object storage. `dexbench wait-ready` polls FlowService
health every 10 ms, so the restart-to-serving time is not quantized to whole
seconds. S8 also records the server-reported replay duration and replayed byte
count.

### State between steps

Every combination of scenario variant and concurrency runs on a newly started
target with empty state. Quick mode starts `dexcli dev` with new SQLite, blob,
and ZD data directories. In prod, ZD gets a new S3 prefix and data directory,
and Temporal gets recreated PostgreSQL, Elasticsearch, and Temporal containers
with empty volumes. The Attribute Store tables are truncated before every
step. Inside one step, the warmup and the measurement share the same Flows.

### Latency and coordinated omission

The load is closed-loop. Each client sends its next request only after the
previous one returns, and the client count is fixed. When the server slows
down, clients send less, so queueing delay that an open-loop workload would
see never appears in the samples. This coordinated omission makes p99 and max
optimistic near saturation. Read the tail percentiles together with
throughput, and compare targets at the same concurrency. Percentiles cover
successful operations only; failed and timed-out operations are counted as
errors.

### Resource sampling

CPU and RSS are sampled every second during the measurement, and each sample
covers only the preceding interval. On Linux, CPU is the delta of utime plus
stime from `/proc/<pid>/stat` over the whole process tree of each root PID.
The tree is resolved again at every sample, so forked processes are included.
Docker containers are sampled through their cgroup v2 `cpu.stat` and
`memory.stat`; container memory is anon plus file_mapped bytes. On macOS, the
sampler uses the delta of cumulative `ps` CPU time with 10 ms resolution; this
is best effort and labeled as such in `cpu_method`.

In quick mode, the root is the `dexcli dev` process for both targets, so
Temporal, server-zd, and the Go Server are included. In prod, the Go Server
sample includes the Temporal, Temporal PostgreSQL, and Elasticsearch
containers. Both targets include the Attribute Store PostgreSQL container.

### Known differences between targets

The Go Server stores blobs in a local directory on NVMe, while ZD stores them
in S3. Temporal uses Elasticsearch for visibility, which ZD does not need.
Each JSON records the release versions, and prod `host.txt` records the
Temporal, PostgreSQL, and Elasticsearch image versions.

Each JSON contains configuration, release and SDK identity, the Git commit,
machine details, attempts, successes, measured errors and unmeasured warmup or
cleanup errors, throughput over the actual elapsed time, exact latency
percentiles, per-interval CPU and RSS samples, and ZD object-store and commit
metrics when available. S3 requests per minute are reported per operation
(GET, PUT, LIST, DELETE) and per object kind. `report.md` shows both targets
side by side with ZD/Temporal throughput and latency ratios for each scenario
and concurrency.
