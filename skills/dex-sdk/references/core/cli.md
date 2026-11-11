# CLI (dexcli) and local development

dexcli is the Dex command-line tool. Its output is JSON first, so scripts and agents can parse it. It talks to the same Server API as Workers and Clients.

## Commands

| Command | Use |
| --- | --- |
| `dexcli dev` | Start a local Dex: server-zd and Dex Web. The release package bundles the Server. |
| `dexcli flow` | Inspect, search, watch, and operate Flows: `inspect`, `search`, `summary`, `state`, `history`, and operator actions. |
| `dexcli api` | Call any public API method. |
| `dexcli web` | Serve Dex Web against an existing Server, for example over an SSH tunnel. |
| `dexcli visualize` | Draw the Flow Definition Graph from Flow source. Dex Web's canvas uses it. |
| `dexcli version check` | Check protocol compatibility with a Server before deploying a Worker. |
| `dexcli admin` | Describe or step down the active Server process. |

Flags, connection, TLS, and authentication settings are in the [CLI reference](https://docs.superdurable.io/reference/cli/).

## Local development with dexcli dev

```bash
brew install superdurable/tap/dexcli
dexcli dev
```

- Use the Server and Dex Web addresses that `dexcli dev` prints. Another local process can change the ports, so pass the printed Server address to the application instead of assuming a default.
- State persists in a data directory across restarts unless you choose another directory.
- A Server configuration override file can change local settings such as retention, Streams, or search. An Attribute Store configuration file enables Attribute Store sync locally.
- Strict local durability flushes every write to disk and is slow on some hosts. A short-lived test server that needs no host-crash safety can select process-crash-safe durability. Keep strict for data you need. See the [CLI reference](https://docs.superdurable.io/reference/cli/#start-locally).
- The Stream Store is enabled by default in `dexcli dev`.

### Show the Flow while you build

For a Go, Python, or TypeScript Flow, run `dexcli visualize SOURCE` once the shape is explicit. As soon as the first graph renders:

1. Write it to a rendering directory.
2. Start a long-lived `dexcli dev` for the user with that rendering directory.
3. Share the Dex Web URL before you continue.
4. Keep it running while you implement, and run tests on separate isolated stacks.

The visualizer reads Flow source without running it. Java and Rust source is not supported. Full management metadata (Indexed Attribute presentation, Summary and Display, Actions) comes only from Go Flow definitions. Python and TypeScript Steps carry `dex:group` and `dex:explanation` comments directly above each Step class; a missing or malformed directive is a blocking analyzer error. The rules are in the [Flow visualization reference](https://docs.superdurable.io/reference/web/flow-visualization).

## Inspect a Flow

Diagnosis is read-only. Use bounded commands and skip payloads you do not need:

```bash
dexcli flow summary <flow-id> --no-hydrate
dexcli flow state <flow-id> --no-hydrate
dexcli flow history <flow-id> --no-hydrate
dexcli flow inspect <flow-id> --all-history
dexcli flow search --query 'FlowType = "OrderFlow" AND ExecutionStatus = "Running"'
```

- `--no-hydrate` skips payload contents that are large or sensitive.
- CLI success fields such as `updated` or `skipped` acknowledge acceptance only.
- Mutating commands (stop, time travel, skip a Timer, invoke, publish) need user authorization. Follow [operator recovery](error-handling.md#operator-recovery).

## Self-check

- Does the application use the Server address that `dexcli dev` printed?
- Was the graph visualized and shared in Dex Web before implementation continued?
- Do tests run on isolated stacks, not on the user's long-lived dev server?
