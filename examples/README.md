# Examples

Runnable Dex samples you can start on your machine. Each language application
hosts a Worker plus an HTTP controller so you can start Flows, publish to
Channels, and call RPCs locally.

You only need **two small steps**:

1. Start Dex **and** one language example server.
2. Start the examples playground, pointed at the example-server HTTP port
   and the Dex web UI port printed in step 1.

## Step 1 — Dex and one example server

In one terminal, start Dex. In another, start **one** language application.
`dexcli dev` starts a Dex server with a web UI. Ports are dynamic; use the
addresses it prints.

Pick a language. Commands match that language's README.

**Go** — from [`go/`](go/) ([README](go/README.md)):

```bash
dexcli dev
make bins
./dex-samples
```

**Java** — from [`java/`](java/) ([README](java/README.md)):

```bash
dexcli dev
./gradlew bootRun
```

**Python (async)** — from [`python/`](python/) ([README](python/README.md)):

```bash
dexcli dev
uv sync --locked
uv run --frozen python main.py
```

**Python (sync)** — from [`python/`](python/) ([README](python/sync-python/README.md)):

```bash
dexcli dev
uv sync --locked
uv run --frozen python sync-python/main.py
```

**TypeScript** — from [`typescript/`](typescript/) ([README](typescript/README.md)):

```bash
dexcli dev
npm install
npm start
```

**Rust** — from [`rust/`](rust/) ([README](rust/README.md)):

```bash
dexcli dev
cargo run --locked
```

Default example-server ports:

| Service | Default |
|---------|---------|
| Example HTTP | `8080` (Python sync `8081`) |
| Example Worker | `8803` (Python sync `8804`) |

Dex ports are whatever `dexcli dev` prints.

Each language README lists env overrides for those addresses. Dataset Deal
(Go) also needs PostgreSQL; see the [Go README](go/README.md). Entity Store
needs PostgreSQL plus `dexcli dev --attribute-store-config`; see
[entity-store/](entity-store/).

## Step 2 — Examples playground Web UI

The playground Web UI is **optional but recommended**. From
[`playground/`](playground/) ([README](playground/README.md)), point the page at
the example HTTP port and the Dex web UI port from step 1:

```bash
./start.sh --port 3333 --example-server http://127.0.0.1:8080 --dex-web http://127.0.0.1:8802
```

Replace `8802` with the web UI port printed by `dexcli dev`. For Python sync,
pass `--example-server http://127.0.0.1:8081`. `./start.sh` with no flags defaults
the example server to `8080` and the playground to `3333`; still pass `--dex-web`
with the printed port.

The script opens a browser tab to [http://127.0.0.1:3333](http://127.0.0.1:3333).
You can also change the example server and Dex web UI URLs in the page header.

Go-only [Dataset Deal](go/products/dataset-deal/) and Python-only
[AI Agent Email](python/ai-agent-email/) have their own UIs; they are not on
the shared playground.

# Directory organization

Each language tree groups Flows the same way. HTTP controllers use these
prefixes (cron schedule and some Worker-only examples have no HTTP surface):

| Category | Purpose | HTTP prefix |
|----------|---------|-------------|
| **products** | Real-world business scenarios | `/products/<kebab-name>/...` |
| **patterns** | Durable workflow design patterns | `/patterns/<kebab-name>/...` |
| **primitives** | One minimal example per Dex primitive | `/primitives/<kebab-name>/...` |

Shared primitives are step, attribute, channel, timer, rpc, subflow, and
client-apis. Java currently ships the step primitive.

| Path | Role |
|------|------|
| [go/](go/) | Go Worker + HTTP (`8080` / `8803`) |
| [java/](java/) | Java Worker + HTTP (`8080` / `8803`) |
| [python/](python/) | Python async Worker + HTTP (`8080` / `8803`) |
| [python/sync-python/](python/sync-python/) | Python sync Worker + HTTP (`8081` / `8804`) |
| [typescript/](typescript/) | TypeScript Worker + HTTP (`8080` / `8803`) |
| [rust/](rust/) | Rust Worker + HTTP (`8080` / `8803`) |
| [playground/](playground/) | Shared static UI for the HTTP controllers |
| [entity-store/](entity-store/) | PostgreSQL + Attribute Store YAML for the user-profile pattern |

Language-specific extras live next to those trees: Go
[Dataset Deal](go/products/dataset-deal/) (own UI and PostgreSQL), Python
[AI Agent Email](python/ai-agent-email/) (own UI).

Each language README has setup, env overrides, and verify steps. CI workflows
are `.github/workflows/examples-*-ci.yml`.
