# Dex server benchmark

`dexbench` compares server-zd and the Go Server with the same Go SDK, Worker
configuration, machine, warmup, measurement window, and concurrency. It runs one
server at a time. Quick mode uses local development servers and gives a rough
comparison. Prod mode uses a dedicated EC2 host, PostgreSQL-backed Temporal,
and real S3. Use prod results for performance conclusions.

Each run writes one JSON file per scenario, target, and concurrency under
`benchmark/results/<YYYY-MM-DD>-<mode>-<host>/`, plus `report.md` and `host.txt`.
These results are tracked in Git. The temporary server data and logs are removed
when the script exits.

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

Install the newest server-zd release binary in the same directory. Select the
asset for the host architecture (`amd64` or `arm64`).

```bash
zd_tag="$(curl -fsSL 'https://api.github.com/repos/superdurable/dex/releases?per_page=100' | jq -r '.[] | select(.draft == false and .prerelease == false and (.tag_name | startswith("server-zd/v"))) | .tag_name' | sort -V | tail -1)"
zd_version="${zd_tag#server-zd/}"
curl -fsSL "https://github.com/superdurable/dex/releases/download/$zd_tag/dex-server-zd_${zd_version}_linux_amd64.tar.gz" -o /tmp/dex-server-zd.tar.gz
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
```

For a short local smoke of all scenarios on ZD, use:

```bash
DEXBENCH_TARGETS=zd DEXBENCH_DURATION=10 DEXBENCH_WARMUP=1 \
  DEXBENCH_CONCURRENCY=2 DEXBENCH_RECOVERY_MB=1 benchmark/run.sh quick
```

## Prod mode: dedicated EC2 host

The coordinator prepares and reaches the host through Systems Manager Session
Manager. The host needs an instance role with access to the dedicated bucket,
and a mounted local NVMe directory. No SSH port or static AWS keys are needed.
On the host, from the Dex checkout:

```bash
benchmark/prod/host-setup.sh
export DEXBENCH_S3_BUCKET=<dedicated-bucket>
export DEXBENCH_S3_PREFIX=<dedicated-prefix>
export DEXBENCH_S3_REGION=<region>
export DEXBENCH_NVME_DIR=<mounted-local-nvme-directory>
benchmark/prod/run-prod.sh
```

`host-setup.sh` installs Docker Compose, Go, the Temporal CLI, and the newest
`server-zd/v*` and `server/v*` releases. It downloads Linux release assets;
when a Go Server release has no binary asset, it builds that release tag from
source. The prod script starts Temporal and PostgreSQL from
`server/docker-compose/`, runs the standalone Go Server, then runs
`dex-server-zd serve` on real S3. Both targets use a PostgreSQL Attribute
Store for the synchronized S7 variant. ZD state lives on local NVMe. The
script removes its Docker containers and local temporary state on exit; the
coordinator removes the benchmark bucket and host afterward.

## Configuration and metrics

The defaults are a 30-second warmup and a 3-minute measurement at concurrency
1, 8, 32, and 128. Set `DEXBENCH_DURATION`, `DEXBENCH_WARMUP`,
`DEXBENCH_CONCURRENCY` (comma-separated), `DEXBENCH_WORKERS`, `DEXBENCH_STEPS`,
`DEXBENCH_SCENARIOS`, `DEXBENCH_TARGETS`, `DEXBENCH_RECOVERY_MB`, or
`DEXBENCH_OUT` to override them. `DEXBENCH_TARGETS=zd` runs only ZD.

S1 starts and completes a one-Step Flow. S2 and S3 run ten sequential SYNC or
ASYNC Steps. S4 measures plain and Attribute-locked RPCs. S5 compares a read-only
RPC with direct FlowService GetAttributes through the generated gRPC client.
S6 measures Channel publication to Step execution. S7 measures external
SetAttributes writes with synchronization disabled and, when a PostgreSQL
Attribute Store is configured, enabled. S8 runs only on ZD: it writes each
configured log payload size, restarts without the local state, and measures
until FlowService becomes ready. S8 records the replayed byte count reported
by the server as well as the target payload size.

Each JSON contains configuration, release and SDK identity, the Git commit,
machine details, attempts, successes and errors, throughput, exact latency
percentiles, one-second CPU and RSS samples, and ZD object-store and commit
metrics when available. On prod, Go Server resource samples include Temporal
and both PostgreSQL containers. `report.md` shows both targets side by side
with ZD/Temporal throughput and latency ratios for each scenario and
concurrency.
