# Dex Rust SDK

[![Rust SDK CI](https://github.com/superdurable/dex/actions/workflows/sdk-rust-ci.yml/badge.svg?branch=main)](https://github.com/superdurable/dex/actions/workflows/sdk-rust-ci.yml)

This workspace contains the Rust SDK runtime and the shared DXBC BlobCache.
Other language SDKs do not use the Rust Worker runtime.

The crates are:

- `dex-blob-cache`: transport-neutral, Go-compatible disk cache.
- `dex-blob-cache-jni`: Java 8-compatible binding containing only cache APIs.
- `dex-core`: Rust invocation dispatch, completion routing, and shutdown.
- `dex-runtime`: tonic WorkerService transport for the Rust SDK.
- `dex-protocol`: generated Rust protobuf and gRPC protocol.

The architecture is defined in
[Multi-language Rust SDK Core](../docs/design/multi-language-rust-sdk-core.md).

## Blob cache

The shared cache keeps opaque payload bytes on disk and uses Stretto only for
metadata admission and eviction:

```rust
use dex_blob_cache::{BlobCache, BlobCacheConfig};

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
bounded blocking executor. Concurrent reads are supported; mutations and close
coordinate inside the cache. An orderly `close` does not delete committed
files, so they can be reused after restart; use `delete_all` before `close` for
ephemeral storage. The cache is not authoritative: policy rejection, a miss,
or a cache error must fall back to fresh server data.

Build the Java cache binding as an optimized native library:

```bash
cargo build --release -p dex-blob-cache-jni --locked
```

## Development

Format and test the workspace:

```bash
make fmt
make lint
make test
```

## License

[Super Durable Source License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
