# Dex Rust SDK Core

This workspace contains the shared native Core for Dex language SDKs.
Java is supported through a dedicated Java 8-compatible JNI bridge.

The first implemented crate is:

- `dex-core`: bounded invocation dispatch, polling, completion routing, and
  shutdown.

The architecture is defined in
[Multi-language Rust SDK Core](../docs/design/multi-language-rust-sdk-core.md).

## Development

Format and test the workspace:

```bash
make fmt
make lint
make test
```

Core is not yet connected to `WorkerService`. The next implementation phase
adds the internal protobuf protocol and tonic gRPC adapter.

## License

Apache License 2.0.
