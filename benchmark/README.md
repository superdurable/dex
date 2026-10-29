# Dex server benchmark

`dexbench` measures server-zd with a Go SDK Worker on a local development server.
The Go Server comparison target was removed because it used the retired
`dexcli dev --engine temporal` path. Historical comparison results remain readable.

Each run writes one JSON file per scenario, target, and concurrency, plus
`report.md`, `charts.html`, and `host.txt`. `charts.html` is a self-contained
page that plots results against concurrency for each scenario. The default directory is
`benchmark/results/<YYYY-MM-DD>-quick-<host label>/`. The host label is
`<os>-<arch>`; set `DEXBENCH_HOST_LABEL` to override it. The temporary server data and logs are
removed when the script exits.

`benchmark/results/` holds only committed result sets: full runs on a
recorded cloud host. A 10-second smoke run on a laptop is not a result. Write smoke output
outside the repository with `DEXBENCH_OUT`, and never commit it.

## Run on a fresh Ubuntu 22.04 or 24.04 host

Install Git, curl, jq, and Python with apt. Install Go in
your home directory; the remaining steps need no sudo. Use Go 1.26 or newer.

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl git jq python3 tar xz-utils
mkdir -p "$HOME/.local"
go_archive="$(curl -fsSL 'https://go.dev/dl/?mode=json' | jq -r '.[0].files[] | select(.os=="linux" and .arch=="amd64" and .kind=="archive") | .filename' | head -1)"
curl -fsSL "https://go.dev/dl/$go_archive" -o "/tmp/$go_archive"
tar -C "$HOME/.local" -xzf "/tmp/$go_archive"
export PATH="$HOME/.local/go/bin:$HOME/.local/bin:$PATH"
go version
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

The script starts server-zd through `dexcli dev` with its embedded s3mock.
It does not start Docker or use AWS. Check that the host has enough free disk
for recovery. The script records CPU model, core count, RAM, and free disk. It
scales recovery log targets from 100/500/1000 MiB to 10/50/100 MiB below 10
GiB free, and skips recovery below 2 GiB.

```bash
benchmark/run.sh
benchmark/bin/dexbench report benchmark/results/<run-directory>/*.json
benchmark/bin/dexbench chart benchmark/results/<run-directory>/*.json > charts.html
```

For a short local smoke of all scenarios on ZD, use the command below. It only
checks that the scenarios run; its numbers are not results.

```bash
DEXBENCH_TARGETS=zd DEXBENCH_DURATION=10 DEXBENCH_WARMUP=1 \
  DEXBENCH_CONCURRENCY=2 DEXBENCH_RECOVERY_MB=1 DEXBENCH_OUT="$(mktemp -d)" \
  benchmark/run.sh
```

The defaults are a 15-second warmup and a 1-minute measurement at
concurrency 1 and 32. It runs 8 scenario variants at 2 concurrency levels, 16
steps. Each step takes 75 seconds plus a few seconds of server
restart, so expect about 22 minutes. With S8 seeding and replaying
up to 1000 MiB, the default quick run takes about 30 minutes. For the full sweep,
set `DEXBENCH_DURATION=180 DEXBENCH_WARMUP=30 DEXBENCH_CONCURRENCY=1,8,32,128`;
it takes about 2 hours.

## Configuration and metrics

The defaults are a 15-second warmup, a 1-minute measurement, and
concurrency 1 and 32. Set `DEXBENCH_DURATION`, `DEXBENCH_WARMUP`,
`DEXBENCH_CONCURRENCY` (comma-separated), `DEXBENCH_WORKERS`, `DEXBENCH_STEPS`,
`DEXBENCH_SCENARIOS`, `DEXBENCH_TARGETS`, `DEXBENCH_RECOVERY_MB`, or
`DEXBENCH_OUT` to override them. `DEXBENCH_TARGETS` accepts only `zd`. Before
each build and step, the scripts wait until the one-minute load average is
below `DEXBENCH_MAX_LOAD`, which defaults to the core count.

S1 starts and completes a one-Step Flow. S2 runs ten sequential SYNC Steps; the report shows whole-Flow latency and per-Step latency, which is
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
GetFlowState call per wake. Only
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
target with empty state. The script starts `dexcli dev` with a new ZD data directory. The Attribute Store tables are truncated before every
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

The root is the `dexcli dev` process, so server-zd and s3mock are included.
When a PostgreSQL Attribute Store is configured, sampling includes its container.

### Result metadata

ZD stores its log and blobs in the embedded s3mock. Each JSON records the release versions.

Each JSON contains configuration, release and SDK identity, the Git commit,
machine details, attempts, successes, measured errors and unmeasured warmup or
cleanup errors, throughput over the actual elapsed time, exact latency
percentiles, per-interval CPU and RSS samples, and ZD object-store and commit
metrics when available. S3 requests per minute are reported per operation
(GET, PUT, LIST, DELETE) and per object kind. For historical result sets containing both targets, `report.md` also shows
ZD/Temporal throughput and latency ratios for each scenario and concurrency.
