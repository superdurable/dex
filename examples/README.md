# Examples

Runnable Dex samples you can start on your machine. Each language application
hosts a Worker plus an HTTP controller so you can start Flows, publish to
Channels, and call RPCs locally.

You only need **two small steps**:

1. Start Dex **and** one language example server.
2. Start the examples playground, pointed at the Dex Web and example-server
   ports from step 1.

## Step 1 — Dex and one example server

In one terminal, start Dex. In another, start **one** language application.
`dexcli dev` listens on flow service `8801` and Dex Web `8802`. It stores
local Temporal state in `$HOME/.dex/dev/<temporal-port>/dex.sqlite.db`; you do
not need `--temporal-db-filename`.

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

Default ports:

| Service | Default |
|---------|---------|
| Example HTTP | `8080` (Python sync `8081`) |
| Example Worker | `8803` (Python sync `8804`) |
| Dex flow service | `8801` |
| Dex Web | `8802` (`dexcli dev`) |

Only one language backend can bind HTTP `8080` / Worker `8803` at a time.
Python sync uses `8081` / `8804`, so it can run alongside another language.

Each language README lists env overrides for those addresses. Dataset Deal
(Go) also needs PostgreSQL; see the [Go README](go/README.md). Entity Store
needs PostgreSQL plus `dexcli dev --attribute-store-config`; see
[entity-store/](entity-store/).

## Step 2 — Examples playground

From [`playground/`](playground/) ([README](playground/README.md)), point the
page at the example HTTP port and Dex Web port from step 1:

```bash
./start.sh --port 3333 --backend http://127.0.0.1:8080 --dex-web http://127.0.0.1:8802
```

For Python sync, pass `--backend http://127.0.0.1:8081`. `./start.sh` with no
flags uses those same defaults (`8080` / `8802` / playground `3333`).

Open [http://127.0.0.1:3333](http://127.0.0.1:3333). You can also change the
backend and Dex Web URLs in the page header. Use Dex Web at
[http://127.0.0.1:8802](http://127.0.0.1:8802) to inspect runs.

Go-only [Dataset Deal](go/products/dataset-deal/) and Python-only
[AI Agent Email](python/ai-agent-email/) have their own UIs; they are not on
the shared playground.

## Directory organization

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
