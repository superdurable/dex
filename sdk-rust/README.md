# Dex Rust SDK Core

[![Rust SDK CI](https://github.com/superdurable/dex/actions/workflows/sdk-rust-ci.yml/badge.svg?branch=main)](https://github.com/superdurable/dex/actions/workflows/sdk-rust-ci.yml)

This workspace contains the shared native Core for Dex language SDKs.
Java is supported through a dedicated Java 8-compatible JNI bridge.

The first implemented crate is:

- `dex-core`: bounded invocation dispatch, polling, completion routing, and
  shutdown.
- `dex-core::BlobCache`: shared disk-backed blob caching for all language SDKs
  except the independent Go SDK.

The architecture is defined in
[Multi-language Rust SDK Core](../docs/design/multi-language-rust-sdk-core.md).

## Blob cache

The shared cache keeps opaque payload bytes on disk and uses Stretto only for
metadata admission and eviction:

```rust
use dex_core::{BlobCache, BlobCacheConfig};

let config = BlobCacheConfig::new("./blob-cache", 256 * 1024 * 1024, 10_000)?;
let cache = BlobCache::open(config)?;
let retained = cache.put("server-minted-blob-id", b"opaque bytes")?;
let cached_payload = cache.get("server-minted-blob-id")?;
if !retained || cached_payload.is_none() {
    // Keep using the freshly loaded bytes.
}
cache.close()?;
```

The calls are synchronous. Event-loop bridges must dispatch them through a
bounded blocking executor. An orderly `close` does not delete committed files,
so they can be reused after restart; use `delete_all` before `close` for
ephemeral storage. The cache is not authoritative: policy rejection, a miss,
or a cache error must fall back to fresh server data.

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

[Super Durable Source License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
