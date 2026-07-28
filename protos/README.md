# Dex IDL (`protos/`)

Protobuf + gRPC interface between Dex SDKs and the Dex server.

- Source: [`dex.proto`](dex.proto)
- Rename catalog: [`../docs/design/idl-renames.md`](../docs/design/idl-renames.md)
- License: MIT ([`LICENSE`](LICENSE))

## Services

- **FlowService** — hosted by the server; SDKs call these RPCs
- **WorkerService** — hosted by the worker; the server calls `WaitFor`, `Execute`, and `WorkerRpc`

## Codegen

Regenerate checked-in stubs into server and SDK trees:

```bash
make -C protos proto
```

| Output | Replaces |
|--------|----------|
| `server/gen/dexpb/` | `server/gen/dexpb/` |
| `sdk-go/gen/dexpb/` | `sdk-go/gen/dexpb/` |
| `sdk-java/src/main/java/io/superdurable/gen/` | OpenAPI `build/generated` |
| `sdk-python/dex/dexpb/` | `sdk-python/dex/dex_api/` |

`make -C server idl-code-gen` and `make -C sdk-go idl-code-gen` delegate to `make -C protos proto`.
