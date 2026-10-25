# s3mock

## Purpose

Local-disk object store with S3 versioning, conditional writes, and listing semantics for s3mock.

## Design

Metadata lives in `metadata.redb`; its `store_format` table records the store format version
described in the [workspace README](../../README.md#store-format). Each object version is one data file under
`data/<bucket incarnation>/<shard>/<ULID>`; the file ends with a footer that repeats its metadata.
A PutObject, or a CopyObject reading its source version, streams into `tmp/`, appends the footer,
renames the file into place, and then commits metadata in one redb write transaction. Startup removes leftover temporary files, and
`Store::collect_orphan_data_files` removes data files that no committed metadata references.
Credential records share the redb database and survive server restarts. Only the server exposes
credential management; store reads include signing secrets so callers must not serialize them in
responses or logs.
