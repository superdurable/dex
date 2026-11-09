# server-zd

server-zd is a Dex Server written in Rust whose only runtime dependency is object storage (S3 or
an S3-compatible store). The local disk is only a cache: every acknowledged write is durable in
object storage, and a node rebuilds its local state from snapshots and the command log.

See the [Server reference](https://docs.superdurable.io/reference/server/) for configuration,
operations, metrics, authentication, search, and Attribute Store synchronization.

## Layout

| Crate | Role |
| --- | --- |
| `dexzd-protocol` | Generated protobuf and gRPC code for the public protocol and internal formats |
| `dexzd-keyspace` | Identifiers, partition hashing, key encodings, value references |
| `dexzd-clock` | Wall-clock abstraction for real and simulated time |
| `dexzd-metrics` | Fixed, process-local OpenMetrics registry and recording handles |
| `dexzd-object-store` | Object storage interface, S3 implementation, fault-injecting test implementation |
| `dexzd-visibility-query` | Visibility query parser and type checker |
| `dexzd-history-segment` | Object format of history segments: encoder and ranged decoders |
| `dexzd-log` | Command log batches and the conditional-write commit protocol |
| `dexzd-cluster-metadata` | Cluster object, partition manifest, node leases |
| `dexzd-apply` | Deterministic state machine that applies commands |
| `dexzd-stream-store` | In-memory Stream store |
| `dexzd-storage` | Local RocksDB state store and snapshots |
| `dexzd-worker-dispatch` | Pull Worker protocol server |
| `dexzd-postgres-connection` | PostgreSQL connection string parsing and TLS connections for Attribute Store and search |
| `dexzd-search` | SearchFlows backends |
| `dexzd-engine` | Commit pipeline, Flow coordination, effects, recovery |
| `dexzd-api` | FlowService and AdminService gRPC handlers and public API authentication |
| `dexzd-server` | The `dex-server-zd` executable |
| `dexzd-simulation` | Deterministic simulation tests |
| `dexzd-e2e-tests` | End-to-end tests over the real gRPC protocol |

Crate layers are registered in `script/rust-crate-layers.json`; a crate may depend only on crates
in the same or a lower layer.

## Release binaries

Each published `server-zd/vX.Y.Z` GitHub Release includes native `dex-server-zd` archives for
Linux amd64, Linux arm64, macOS amd64, and macOS arm64:

| Platform | Archive |
| --- | --- |
| Linux amd64 | `dex-server-zd_vX.Y.Z_linux_amd64.tar.gz` |
| Linux arm64 | `dex-server-zd_vX.Y.Z_linux_arm64.tar.gz` |
| macOS amd64 | `dex-server-zd_vX.Y.Z_darwin_amd64.tar.gz` |
| macOS arm64 | `dex-server-zd_vX.Y.Z_darwin_arm64.tar.gz` |

Each archive contains `dex-server-zd`, `LICENSE.md`, `LEGACY_NOTICES.md`, and `LICENSES/`.
The Release also includes `checksums.txt` with the SHA-256 hash of each archive. Verify the
downloaded archive against that file before extracting it. Run
`sha256sum --ignore-missing -c checksums.txt` on Linux or
`shasum -a 256 --ignore-missing -c checksums.txt` on macOS from the download directory.

Linux archives require glibc 2.28 or newer. They run on Debian 10+, Ubuntu 20.04+, and
RHEL 8+ without a newer system libstdc++.

## Running

Use `dex-server-zd serve` for an external object store, `dex-server-zd local` for one node
with an embedded object store, or `dexcli dev` for local application development. See the
[configuration reference](https://docs.superdurable.io/reference/server/configuration) for
commands, flags, and settings.

## Development

```bash
cd server-zd
cargo test -p <crate>        # iterate on one crate
make fmt-check clippy test   # what CI runs
make build-server            # builds target/release/dex-server-zd
```

Local tests use only the in-process s3local (`../s3local`) or the in-memory object store. They need
no docker, MinIO, or Temporal.

### Object storage compatibility

The [MinIO nightly](../.github/workflows/server-zd-minio-nightly.yml) checks the coollabsio
`RELEASE.2025-10-15T17-29-55Z` build (image digest
`sha256:69b55a1c1c5dc285ce04db96689f5b2102317fc77a50680a1874ca6efd1c87f9`). The
server-zd object-store and commit-protocol tests, the `serve` warm and cold restart test, and
S3 conformance tests for server-zd's object storage contract are required. The remaining S3
conformance tests are informational; the workflow uploads their result summary. This is the
MinIO release tracked for compatibility; each additional release needs its own matrix entry and
passing required tests. The workflow also supports manual runs.

The full S3 conformance suite exercises features beyond server-zd's object storage contract.
The pinned MinIO release has shown these deviations from the suite's S3 expectations:

| S3 behavior | Observed MinIO behavior | server-zd use |
| --- | --- | --- |
| Recreating an owned bucket in `us-east-1` | Returns `BucketAlreadyOwnedByYou`: "Your previous request to create the named bucket succeeded and you already own it." | No; the bucket is provisioned before server startup |
| Lifecycle rule for aborting incomplete multipart uploads | Reads `DaysAfterInitiation` as "left: None" instead of "right: Some(2)"; expiration days round-trip | No lifecycle API use |
| Versioned batch delete | No listed marker satisfies the run's assertion, "entry.is_latest() == Some(true)" | No bucket versioning use |
| Multipart upload listing with prefix and markers | Returns "left: 0" where the test expects "right: 1" upload | No multipart upload listing use |
| Checksummed multipart part numbering | A SHA-256 upload completed from part 2; the test logged "called `Result::unwrap_err()` on an `Ok` value" | No; server-zd uploads CRC32 parts consecutively from part 1 |
| Multipart full-object checksum type | A COMPOSITE checksum-type header on a FULL_OBJECT upload was accepted; the test logged "called `Result::unwrap_err()` on an `Ok` value" after a wrong full-object CRC32 value was rejected | No checksum-type header; server-zd uses CRC32 part checksums |
| Reading a delete marker version | Fails "S3 reports the delete marker's Last-Modified on 405" | No bucket versioning use |

The encrypted multipart conformance fixtures need a test KMS. The workflow configures one for
each run; an upload initiation failure without it does not measure multipart completion behavior.

### Persistent format testdata

`testdata/v<N>` holds a recorded log, snapshot, cluster metadata, and committed state.
`cargo test -p dexzd-engine --test format_compat` checks that the current code decodes every
version and replays it to the recorded state. A pinned digest of every file fails the test when
a byte changes. Before server-zd 1.0, regenerate `v1` with its generator when apply results
change and update its pin. From 1.0 on, recorded versions are immutable. The scripted workload
must also reproduce the newest version byte for byte, so a later change to the bytes the engine
writes for it needs a new version.

From server-zd 1.0 on, add the next version in the change that does any of the following:

- changes `proto/dexzd/`, the key layout, the object storage layout, or the snapshot format;
- adds a command kind, a state table, an index kind, or an object kind;
- adds a behavior version;
- prepares a release whose persisted data differs from the newest version.

`testdata/README.md` describes the generator and the steps.

## Rules specific to server-zd

- `dexzd-apply` must be deterministic. It never reads clocks, random numbers, configuration, the
  environment, files, or the network, and it never iterates a hash map. Its `clippy.toml` bans the
  common offenders.
- After 1.0, a change to the result of applying an already committed command needs a new behavior
  version with a named predicate. Before 1.0, regenerate `v1` instead.
- Persistent formats (`proto/dexzd/`, key layout, object storage layout, snapshots) change only
  additively, following the same rules as `protos/dex.proto`.
