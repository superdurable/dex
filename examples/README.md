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

Each language README lists env overrides for those addresses. Entity Store
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

The Go [Deal DSL](go/products/deal-dsl/) application and Python-only
[AI Agent](python/ai-agent/) have their own UIs; they are not on
the shared playground. Deal DSL Flow implementations also run in Java, Python,
TypeScript, and Rust. The AI Agent UI reads the session snapshot and live events
through the Dex Agents browser stream helpers.

# Directory organization

Each language tree groups Flows the same way. HTTP controllers use these
prefixes (some Worker-only examples have no HTTP surface):

| Category | Purpose | HTTP prefix |
|----------|---------|-------------|
| **products** | Real-world business scenarios | `/products/<kebab-name>/...` |
| **patterns** | Durable workflow design patterns | `/patterns/<kebab-name>/...` |
| **primitives** | One minimal example per Dex primitive | `/primitives/<kebab-name>/...` |

Shared primitives are step, attribute, channel, timer, rpc, subflow, and
client-apis.

| Path | Role |
|------|------|
| [go/](go/) | Go Worker + HTTP (`8080` / `8803`) |
| [go/codec-server/](go/codec-server/) | Deployable codec server for encrypted payloads in Dex Web and dexcli (not a Worker) |
| [go/agent-harnesses/](go/agent-harnesses/) | Order support agent on CloudWeGo Eino with Dex Agents: Worker + HTTP (`8082`) and the Agent Quick Start |
| [java/](java/) | Java Worker + HTTP (`8080` / `8803`) |
| [python/](python/) | Python async Worker + HTTP (`8080` / `8803`) |
| [python/sync-python/](python/sync-python/) | Python sync Worker + HTTP (`8081` / `8804`) |
| [python/codec-server/](python/codec-server/) | Deployable codec server for encrypted payloads in Dex Web and dexcli (not a Worker) |
| [typescript/](typescript/) | TypeScript Worker + HTTP (`8080` / `8803`) |
| [typescript/codec-server/](typescript/codec-server/) | Deployable codec server for encrypted payloads in Dex Web and dexcli (not a Worker) |
| [rust/](rust/) | Rust Worker + HTTP (`8080` / `8803`) |
| [playground/](playground/) | Shared static UI for the HTTP controllers |
| [entity-store/](entity-store/) | PostgreSQL + Attribute Store YAML for the user-profile pattern |

Language-specific extras live next to those trees: the Go
[Deal DSL](go/products/deal-dsl/) UI, PostgreSQL catalog, and
`dex-deal-dsl` binary; and the Python
[AI Agent](python/ai-agent/) UI. The Deal DSL interpreter itself is
implemented across all five language SDK examples.

Each language README has setup, env overrides, and verify steps. CI workflows
are `.github/workflows/examples-*-ci.yml`.

# Cross-SDK payload encryption test

[`run-payload-encryption-interop-tests.sh`](run-payload-encryption-interop-tests.sh)
checks that Go, Python, and TypeScript read each other's encrypted values. It
starts one Dex server and writes one key file with `dexcli codec-server keygen`.
It then runs one Worker per SDK. Each Worker serves the same Flow under its own
Flow type, and each SDK's Client starts and reads all three. The Go test also
reads and writes through the dexcli codec server and the three example codec
servers. It checks their authentication and CORS too.

The test code lives in [go/interop/](go/interop/),
[python/tests/interop/](python/tests/interop/), and
[typescript/test/interop/](typescript/test/interop/). It uses the same released
SDK versions as the other examples. Install the dependencies the script lists,
then run it from the repository root:

```bash
./examples/run-payload-encryption-interop-tests.sh
```

CI runs it in `.github/workflows/payload-encryption-interop-ci.yml`.
