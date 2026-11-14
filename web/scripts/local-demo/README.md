# Dex Web local demo kit

Developer tool. It starts one local Dex with sample runs, so you can look at
Dex Web from the current checkout. It is not an application sample; those live
in `examples/`.

## Run

From the repository root:

```bash
web/scripts/local-demo/run.sh
```

The script builds Dex Web and `dexcli` from this checkout, builds the Go
programs, starts everything in the background, waits for readiness, and prints
the links. Press Ctrl+C, or close the terminal, to stop everything it started.
From another terminal, `web/scripts/local-demo/run.sh stop` does the same. It
stops only the processes it started, which it records in
`$TMPDIR/dex-web-local-demo-<uid>/web-<web port>.pid`. When the script itself
was killed, `run.sh stop` still stops the processes recorded there.

Requirements: Go, Node.js 22+, `curl`, and a `dex-server-zd` binary.

## What it starts

| Process | What it shows in Dex Web |
| --- | --- |
| `dexcli dev` on Dex port 8811 and Web port 8812 | Dex Web itself. `-flow-rendering-dir` holds two Flow Definition Graphs: `customer-refund-agentic.json` from `docs/src/data/flow-definitions/`, and `demo-agent.json`, the agent session graph with Flow type `DemoAgent`. |
| Go examples server (`examples/go/cmd/server`) on 8095 | Every example Flow. The script starts one agentic customer refund run. Its Design tab draws the refund graph, and the run waits at a manager approval. |
| `agent-session` Worker | Flow type `DemoAgent`. A scripted model answers without a network call. The script starts one session with two turns; the second turn calls the `lookup_order` tool. Open its Conversation tab. |
| `many-events` Worker | One run of `TimelineDensityFlow` that loops 1100 rounds, which records well over 1000 history events. Open its Timeline to check density and scrolling. |

The script derives `demo-agent.json` from
`agents/python/core/src/dex_agents/session_flow_graph.json`. It fills the
`<flow-type>` placeholder with `DemoAgent` and the Action permission with
`dex.agents.action`. Both graph files are written to the run directory, not to
the repository.

Each run uses new Flow IDs with a timestamp suffix. Without
`DEX_DEMO_RUN_DIR`, each run also gets a new run directory, so server-zd starts
empty. With `DEX_DEMO_RUN_DIR`, a later run reuses that directory's server-zd
state and Dex Web also lists the earlier runs.

The script deletes the run directory's `bin/` on exit and keeps the logs, graphs,
BlobCaches, and server-zd state. These can reach a few hundred MB. Delete old run
directories with `rm -rf "${TMPDIR:-/tmp}"/dex-web-local-demo.*` while no kit is
running.

## Settings

| Environment variable | Default | Meaning |
| --- | --- | --- |
| `DEX_DEMO_DEX_PORT` | `8811` | Dex FlowService port. |
| `DEX_DEMO_WEB_PORT` | `8812` | Dex Web port. It also names the `run.sh stop` PID file. |
| `DEX_DEMO_EXAMPLES_PORT` | `8095` | Go examples HTTP port. |
| `DEX_DEMO_RUN_DIR` | new `mktemp -d` directory | Logs, binaries, graphs, and server-zd state. |
| `DEX_DEMO_READY_TIMEOUT_SECONDS` | `180` | How long each readiness wait may take. |
| `DEX_DEMO_SKIP_WEB_BUILD` | empty | `1` reuses an existing `web/assets/dist` instead of running `npm run build`. |
| `DEXCLI` | empty | An installed `dexcli` to use instead of building one, such as `DEXCLI=$(command -v dexcli)`. Dex Web then comes from that build, not this checkout. |
| `DEX_SERVER_ZD_BINARY` | see below | The `dex-server-zd` binary. |

Without `DEX_SERVER_ZD_BINARY`, the script uses `dex-server-zd` next to
`dexcli`, then on `PATH`, then `server-zd/target/release/dex-server-zd`.

The script checks all three ports before it builds anything. A busy port then
fails in seconds instead of after the build. `dexcli dev` would also refuse a
busy explicit port, but the examples server starts only later.

The Web build runs `npm ci` when `web/node_modules` is missing or older than
`web/package-lock.json`.

## Run a piece by hand

The Go programs form their own module. Build it without the repository
`go.work`:

```bash
cd web/scripts/local-demo
GOWORK=off go run ./agent-session -session-id my-session
GOWORK=off go run ./many-events -flow-id my-run -rounds 1100
```

Both read `DEX_FLOW_SERVICE_ADDRESS`, default `127.0.0.1:8811`, and keep
serving as a Worker until Ctrl+C. `-blob-cache-dir` chooses their BlobCache
directory; without it they use a temporary directory and delete it on exit.
Web CI runs `go vet` on this module.
