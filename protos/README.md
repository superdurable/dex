# Dex IDL (`protos/`)

Protobuf + gRPC interface between Dex SDKs and the Dex server.

- Source: [`dex.proto`](dex.proto)
- Rename catalog: [`../docs/design/idl-renames.md`](../docs/design/idl-renames.md)
- License: MIT ([`LICENSE`](LICENSE))

## Services

- **FlowService** — hosted by the server; SDKs call these RPCs
- **WorkerService** — hosted by the worker; the server calls `WaitFor`, `Execute`, and `WorkerRpc`

## Worker targets

`WorkerTarget.address` is a plaintext gRPC target. Set `is_headless_address` for a
`host:port` whose DNS records represent individual WorkerService endpoints.

Set `FlowConfig.worker_target` when starting a flow. `UpdateFlowConfig` can
change the target for subsequent WorkerService calls while the flow is running.

## Step close decisions

`StepDecision.close_decision` explicitly ends a step thread or flow. Graceful
completion waits for other active steps; force completion and force failure end
the flow immediately; dead end ends only the current step thread.

`FORCE_COMPLETE_ON_CHANNELS_EMPTY` atomically completes when every named channel
is empty. It requires unique, non-empty `conditional_channel_names` and at least
one `next_steps` fallback. Other close types cannot include channel names or
next steps. `FORCE_FAIL` accepts only a string `close_input`, and `DEAD_END`
accepts no input.

## Search flows

`SearchFlows` returns each execution's flow ID, run ID, and all search
attributes supplied by the backend visibility API. Search attribute values use
the `Value` oneof; the response does not expose backend index types.

Temporal type metadata preserves numeric types. Cadence visibility payloads do
not include index types, so Dex infers numbers from JSON: integral JSON numbers
become `int_value`, and other numbers become `double_value`.

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
