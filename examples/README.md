# Examples

Language-specific sample applications for Dex. Each tree organizes Flows into
three categories:

| Category | Purpose | HTTP prefix |
|----------|---------|-------------|
| **products** | Real-world business scenarios | `/products/<kebab-name>/...` |
| **patterns** | Durable workflow design patterns | `/patterns/<kebab-name>/...` |
| **primitives** | One minimal example per Dex primitive | `/primitives/<kebab-name>/...` |

Languages with HTTP controllers expose routes under these prefixes. Cron
schedule and some Worker-only examples have no HTTP surface.

Go-only [Dataset Deal](go/products/dataset-deal/) and Python-only
[AI Agent Email](python/ai-agent-email/) have their own UIs; they are not on
the shared playground.

| Path | Language |
|------|----------|
| [go/](go/) | Go |
| [java/](java/) | Java |
| [python/](python/) | Python (async) |
| [python/sync-python/](python/sync-python/) | Python (sync) |
| [rust/](rust/) | Rust |
| [typescript/](typescript/) | TypeScript |

Each tree has setup, env overrides, and verify steps. CI workflows are
`.github/workflows/examples-*-ci.yml`.

The shared [Entity Store setup](entity-store/) adds PostgreSQL for the
user-profile projection example implemented by all five language applications.

## Typical local loop

1. Start Dex (`dexcli dev`). Flow service listens on `8801`; Dex Web is
   typically `8802`.
2. Start **one** language example server (commands below).
3. Start the [playground](playground/).
4. Open the playground, then Dex Web to inspect runs.

Only one language backend can bind HTTP `8080` / Worker `8803` at a time.
Python sync uses `8081` / `8804`, so it can run alongside another language.

| Service | Default |
|---------|---------|
| Example HTTP | `8080` (Python sync `8081`) |
| Example Worker | `8803` (Python sync `8804`) |
| Dex flow service | `8801` |
| Dex Web | `8802` (`dexcli dev`) |
| Playground | `3333` |

## Start a language server

Start Dex, then one of the following. Commands match each language README.

**Go** — from `examples/go/` ([README](go/README.md)):

```bash
dexcli dev --temporal-db-filename /tmp/dex-examples.db
make bins
./dex-samples
```

Dataset Deal also needs PostgreSQL; see the Go README.

**Java** — from `examples/java/` ([README](java/README.md)):

```bash
dexcli dev --temporal-db-filename /tmp/dex-examples.db
./gradlew bootRun
```

**Python async** — from `examples/python/` ([README](python/README.md)):

```bash
dexcli dev --temporal-db-filename /tmp/dex-examples-python.db
uv sync --locked
uv run --frozen python main.py
```

**Python sync** — from `examples/python/` ([README](python/sync-python/README.md)):

```bash
dexcli dev --temporal-db-filename /tmp/dex-examples-python.db
uv sync --locked
uv run --frozen python sync-python/main.py
```

**TypeScript** — from `examples/typescript/` ([README](typescript/README.md)):

```bash
dexcli dev --temporal-db-filename /tmp/dex-examples.db
npm install
npm start
```

**Rust** — from `examples/rust/` ([README](rust/README.md)):

```bash
dexcli dev --temporal-db-filename /tmp/dex-examples-rust.db
cargo run --locked
```

## Playground

[playground/](playground/) is a static page that calls every shared product,
pattern, and primitive HTTP controller.

```bash
cd playground
./start.sh --port 3333 --backend http://127.0.0.1:8080 --dex-web http://127.0.0.1:8802
```

Open [http://127.0.0.1:3333](http://127.0.0.1:3333). For Python sync, pass
`--backend http://127.0.0.1:8081` (or change the URL in the page header). See
[playground/README.md](playground/README.md).
