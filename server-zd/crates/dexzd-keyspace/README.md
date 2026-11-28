# dexzd-keyspace

## Purpose

Identifiers, partition hashing, RocksDB key encodings, and value references shared by every server-zd crate.

## Embedding application key layout versions

`EMBEDDING_APPLICATION_KEY_LAYOUT_VERSIONS` reserves versions from `0x8000_0000` through `u32::MAX` for applications that embed server-zd. server-zd's own key layout versions always stay below that range. Its existing key layout equality check therefore rejects application clusters, including in already released binaries.
