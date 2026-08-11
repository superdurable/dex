# Blob Cache Go

`blob-cache-go` is an independently versioned disk blob-cache module shared by
Dex components. The [blobcache package](blobcache/README.md) uses Ristretto
TinyLFU admission and SampledLFU eviction while keeping payloads on disk.

Dex Server and the Go SDK do not yet import this module. Release it with a
path-style tag such as `blob-cache-go/v0.1.0` before adding those dependencies.

Run its tests from the repository root:

```text
make -C blob-cache-go tests
```
