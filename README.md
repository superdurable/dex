# Dex - Durable Execution, Re-defined.

**Dead Simple. More Power.**

Traditional databases persist only data. Durable Execution persists both data and actions. On top of that, Super Durable synchronizes persisted data with your existing databases and data storage—unifying your persistence architecture.

<img width="676" height="607" alt="arch" src="https://github.com/user-attachments/assets/720e38a8-b151-4251-aa8a-5b62ae64a7f4" />


## Quick start

```bash
brew install superdurable/tap/dexcli
dexcli dev --open
```

Open Dex Web at [http://127.0.0.1:8802](http://127.0.0.1:8802). This starts
local Temporal and its Web UI automatically. Connect to an existing Temporal
server instead with `dexcli dev --temporal-address localhost:7233`. Dex step
inputs and large values persist by default in `$HOME/.dex/blobs`.

See [cli/README.md](cli/README.md) for ports, persistence, and all flags.

## Releases

Versions are per-component. Tag with a prefix (for example `server-v1.0.0`, `sdk-python/v0.12.0`, `sdk-java/v2.11.1`, `sdk-go/v1.2.3`). Details: [CONTRIBUTING.md — Releases](CONTRIBUTING.md#releases-monorepo-tags).

## Licensing

Super Durable changes to the product, SDKs, protocol, CLI, and web components
use the [Super Durable Source License 1.0](LICENSE). Production use is free up
to US$10 million in consolidated annual revenue; after first exceeding the
threshold, continued production use requires a subscription within 90 days.
Competitive Dex products and hosted replacements always require a commercial
license.

Applications may bundle SDK and protocol components subject to the
developer/operator revenue rule. End users that only run the application do
not need a separate subscription. Legacy code retains its original license;
`docs/` and `examples/` are excluded from the relicensing. See
[LICENSING.md](LICENSING.md) and [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
