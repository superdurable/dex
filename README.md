# Dex — monorepo

Workflow-as-code orchestration: server, protobuf IDL, and SDKs/samples for Go,
Java, Python, and Rust.

This project is a fork of [indeedeng/iwf](https://github.com/indeedeng/iwf) (Indeed Workflow Framework). It combines that family of repos into one tree under [superdurable/dex](https://github.com/superdurable/dex), preserving git history under each directory.

## Layout

| Path | Contents |
|------|----------|
| [server/](server/) | Dex server (Temporal/Cadence backend) |
| [protos/](protos/) | Protobuf IDL ([`dex.proto`](protos/dex.proto); renames in [`docs/design/idl-renames.md`](docs/design/idl-renames.md)) |
| [docs/](docs/) | Docs: [`design/`](docs/design/), [`case-study/`](docs/case-study/), [`wiki/`](docs/wiki/) (start at [README.md](docs/README.md)) |
| [cli/](cli/) | `dexcli` local development environment |
| [web/](web/) | Dex Web console |
| [sdk-go/](sdk-go/) | Go SDK |
| [examples/go/](examples/go/) | Go examples |
| [sdk-java/](sdk-java/) | Java SDK |
| [examples/java/](examples/java/) | Java examples |
| [sdk-python/](sdk-python/) | Python SDK |
| [examples/python/](examples/python/) | Python examples |
| [sdk-rust/](sdk-rust/) | Shared Rust SDK Core |

Go SDK + samples use root [`go.work`](go.work). Build the server separately (`cd server && go build ./...`) to avoid a Cadence/Temporal `genproto` workspace conflict.

## Quick start

```bash
brew install dexcli
dexcli dev
```

Open Dex Web at [http://127.0.0.1:8901](http://127.0.0.1:8901). This starts
local Temporal and its Web UI automatically. Connect to an existing Temporal
server instead with `dexcli dev --temporal-address localhost:7233`.

See [cli/README.md](cli/README.md) for ports, persistence, and all flags.

## Docker server

All-in-one Docker (from upstream lite image):

```bash
docker pull superdurable/dex-server-lite:latest && \
  docker run -p 8801:8801 -p 7233:7233 -p 8233:8233 \
  -e AUTO_FIX_WORKER_URL=host.docker.internal \
  --add-host host.docker.internal:host-gateway \
  -it superdurable/dex-server-lite:latest
```

Or build/run from this repo:

```bash
cd server
docker pull superdurable/dex-server:latest && docker compose -f ./docker-compose/docker-compose.yml up
```

- Dex service: http://localhost:8801/
- Temporal Web UI: http://localhost:8233/
- Temporal: `localhost:7233`

See [server/README.md](server/README.md) and [CONTRIBUTING.md](CONTRIBUTING.md) for details.

See [web/README.md](web/README.md) for frontend development.

## Releases

Versions are per-component. Tag with a prefix (for example `server-v1.0.0`, `sdk-python-v0.12.0`, `sdk-java-v2.11.1`, `sdk-go/v1.2.3`). Details: [CONTRIBUTING.md — Releases](CONTRIBUTING.md#releases-monorepo-tags).

## Licensing

Multiple licenses apply by directory. See root [LICENSE](LICENSE) and each package's own LICENSE file.
