# Dex - Durable Execution, Re-defined.

**Dead Simple. More Power.**

Traditional databases persist only data. Durable Execution persists both data and actions. On top of that, Super Durable synchronizes persisted data with your existing databases and data storage—unifying your persistence architecture.

This project is a fork of [indeedeng/iwf](https://github.com/indeedeng/iwf) (Indeed Workflow Framework). It combines that family of repos into one tree under [superdurable/dex](https://github.com/superdurable/dex), preserving git history under each directory.

## Quick start

```bash
brew install superdurable/tap/dexcli
dexcli dev --open
```

Open Dex Web at [http://127.0.0.1:8901](http://127.0.0.1:8901). This starts
local Temporal and its Web UI automatically. Connect to an existing Temporal
server instead with `dexcli dev --temporal-address localhost:7233`.

See [cli/README.md](cli/README.md) for ports, persistence, and all flags.

## Releases

Versions are per-component. Tag with a prefix (for example `server-v1.0.0`, `sdk-python-v0.12.0`, `sdk-java-v2.11.1`, `sdk-go/v1.2.3`). Details: [CONTRIBUTING.md — Releases](CONTRIBUTING.md#releases-monorepo-tags).

## Licensing

Multiple licenses apply by directory. See root [LICENSE](LICENSE) and each package's own LICENSE file.
