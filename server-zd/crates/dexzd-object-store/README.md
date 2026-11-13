# dexzd-object-store

## Purpose

Object storage interface used by server-zd, with S3, db9, Azure Blob Storage, and Google Cloud Storage implementations and a fault-injecting in-memory implementation for tests.

## S3 configuration

`S3ObjectStoreConfig` selects the endpoint, region, bucket, relative key prefix, path-style addressing, credentials, and per-request timeout. Credentials come from the AWS default provider chain or explicitly supplied static keys. Static keys are intended for local S3-compatible endpoints. Static keys may include a session token for temporary credentials. Anonymous credentials send unsigned requests, for a local emulator or a bucket that allows anonymous access. Both the multipart threshold and part size default to 8 MiB; the part size must be at least 5 MiB.

S3 reads retry transient service and transport failures up to three attempts. Writes do not retry inside the SDK. A write that times out or receives a server error may already have committed, so the store reports `OutcomeUnknown`; callers read the key and compare its content before retrying. In particular, retrying a conditional write after its own earlier success can produce `PreconditionFailed`. Multipart uploads apply the condition on completion and abort unfinished uploads after failures.

HTTP 409 `ConditionalRequestConflict` retries twice with bounded backoff. Each multipart retry aborts the previous upload and starts a new one. A persistent conflict is `Transient`; HTTP 412 and `If-Match` on a missing key are `PreconditionFailed`.

Cancellation can interrupt a multipart upload before the abort request runs. Configure the bucket's `AbortIncompleteMultipartUpload` lifecycle rule to remove abandoned uploads.

`head` checks bucket existence after a 404 to distinguish a missing key from a missing bucket. This adds one request to the missing-key path.

File downloads write to a temporary file beside the target and rename after the full body arrives. Errors and cancellation remove the temporary file without publishing a partial target.

`startup_self_check` verifies create-only and ETag replacement behavior in the configured bucket, then deletes its probe object. Start the server only after it succeeds. The check fails when it overruns `STARTUP_SELF_CHECK_DEADLINE` (5 minutes). With cleanup it holds its probe object for at most `STARTUP_PROBE_MAXIMUM_LIFETIME`.

## Mutable object keys and create-only stores

server-zd writes `ReplaceIfEtagMatches` only on mutable object keys: `manifest`, `registry/attribute-indexes`, and keys under `lease/`, `probe/`, and `registry/flow-definitions/`. `is_mutable_object_key` and the key constants are their single definition. Register a key there before replacing it.

`AliyunOssObjectStore` wraps a store whose only conditional write is create-only, such as Aliyun OSS. Every other key passes through unchanged and rejects `ReplaceIfEtagMatches` with a permanent error. `supports_replace_if_etag_matches_on_every_key` then returns false; wrappers forward the answer.

A mutable key `K` is stored as create-only versions at `K.v/<u64::MAX - generation>`, zero-padded to 20 digits, so a one-key listing returns the newest version. ETags are `"g<generation>-<version ID>"`. A delete writes a tombstone version.

The mapping is injective. A mutable key may contain `.` but never `.v/`, so a Flow type such as `Order_Flow-v1.2` keeps its key. The 20 digits hold no `.v/`, so the last `.v/` of a version key is its separator and determines `K`. The version prefixes of two different mutable keys never overlap, since one key would have to contain `.v/`. A plain key is rejected when the text before any of its `.v/` is a mutable key, so no plain key lies under a version prefix. Flow definition keys are `registry/flow-definitions/<tenant ID>/types/<segment>`: tenant IDs are decimal, and segments percent-encode `/`.

Each version body starts with a 38-byte header, format 1, followed by the caller's payload. A tombstone has no payload.

| Bytes | Field |
| --- | --- |
| 0-3 | Magic `DXZV` |
| 4 | Format, 1 |
| 5 | Flags: bit 0 tombstone, bit 1 predecessor present; other bits zero |
| 6-21 | Version ID, a random 128-bit big-endian integer |
| 22-37 | Predecessor version ID, big-endian; zero without a predecessor |

A read lists the newest version, then reads that physical key. The read must return the listed object: its physical ETag and size must match the listing, or the read lists again. Reclamation and a stale writer can replace a listed version with a resurrected object at the same key, and only this check keeps such an object out of reads. Physical ETags tell objects apart because every body holds a random version ID. Get, head, range reads, file downloads, and listings all use this read. A listing whose prefix may include mutable keys shows each such key once, with its newest version, and hides deleted keys. This slow listing path reads every object under the prefix after `start_after` on each page, because `K.v/...` sorts after `K-x` while `K` sorts before it.

A replacement reads the newest version this way and requires it to be the ETag's version. It then creates the next generation and confirms it. The version is written when it is still the newest, or when the next generation names it as predecessor. Otherwise a stale writer recreated a reclaimed generation: the version is deleted and the write reports `PreconditionFailed`. When the next generation is already gone too, the outcome is unknown. Failures after the create are `OutcomeUnknown`.

This is linearizable because a resurrected object is never the newest. A generation is reclaimed only after two newer ones exist, and the newest generation is never deleted. Reads therefore never return a resurrected object or its ETag. Every predecessor ID comes from a read of the newest version, so a successor names only a version that was the newest, which is a real write.

After a confirmed write of generation `n`, a background task deletes generations `n - 2` and below whose successor was written at least `MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION` (one hour) before `n`, by the store's own timestamps. It keeps tombstones. Each pass lists every version still kept, so a key gets at most one pass every 5 minutes; requests in between are dropped, and the next pass deletes what they would have. A node lease renewed every 3.3 seconds therefore keeps about 1,200 versions, and its renewals add about 2 listings, one batch delete, and one header read per reclaimed version every 5 minutes.

The hour also keeps replacements safe if a listing lags behind writes. A lagging listing can hide the newest generation `g`, so a writer creates `g` again; the create-only write conflicts and the replacement fails. Only a lag longer than the age could show a reclaimed generation as the newest and let a writer recreate it. `confirm_created_version` and reclamation count each listing that misses the version this process just created or confirmed in `dexzd_object_store_versioned_listing_stale_total`, by `site`, and log a warning with the key and both generations.

Startup probe keys belong to one process, so deleting one removes every version. `remove_stale_startup_probes` removes probe keys whose newest object is older than 24 hours by the store's clock. It reads that clock by writing and deleting a clock probe object of its own; when another sweep removes that object first, the sweep skips and removes nothing. A running check holds its probe for at most `STARTUP_PROBE_MAXIMUM_LIFETIME`. A process suspended during a check, by host sleep or `SIGSTOP`, can hold it longer: after more than 24 hours the sweep may remove the probe, and the resumed check then fails or passes on a new probe. Restart the server when that check fails. Probe keys are separate from leases and the manifest, so this never affects fencing, and the sweep deletes only keys under `probe/`. Each read and write of a mutable key adds one listing; a replacement also reads one version header. Flow definition reads therefore cost a listing and a GET, and listing definitions takes the slow listing path. A version key is `VERSION_OBJECT_KEY_SUFFIX_BYTES` (23) bytes longer than its logical key.

The inner store must create atomically and list consistently right after each write. The layout differs from every other provider, so data cannot move between them.

## Azure Blob Storage and Google Cloud Storage

`AzureGcsObjectStore` serves both clouds through the `object_store` crate. Both
clouds support create-only and compare-and-swap writes natively, so the object
layout matches S3. Azure uses `If-None-Match: *` and `If-Match: <ETag>`; GCS
uses `x-goog-if-generation-match` on its XML API.

Callers see one ETag per object version. On Azure it is the quoted ETag. On GCS
it is the quoted generation, which identifies each write of the object's data;
GCS ETags give no usable version identity, and multipart objects have no MD5
ETag. Writes, reads, and listings return the same token. A replacement ETag that
no read returned, including GCS generation 0 or a non-canonical number, fails
its condition without a request. GCS listings use the JSON API, which reports
generations; the XML listing does not.

Each store builds two `object_store` clients that share credentials. Reads retry
up to three attempts. Writes, copies, and deletes never retry inside
`object_store`, which would otherwise retry 5xx and 429 responses even for
conditional writes. The HTTP transport never follows redirects, because a
followed redirect would repeat a write below those settings. A connector wrapper
exposes the response status that `object_store` hides.

| Response | Write, copy, or delete | Read |
| --- | --- | --- |
| HTTP 412, Azure 409 `BlobAlreadyExists`, or 404 on a replacement | `PreconditionFailed` | - |
| HTTP 429 | retried three times, after 1, 2, and 4 seconds, then `Transient` | `Transient` |
| 5xx, 408, timeout, dropped connection | `OutcomeUnknown` | `Transient` after retries |
| Redirect (3xx) | `OutcomeUnknown` | `Permanent` |
| Refused connection | `Transient` | `Transient` |
| Azure copy still pending | see below | `Transient` |
| Azure copy failed or aborted | `Corrupt` | `Corrupt` |
| 401, 403, missing container or bucket, other 4xx | `Permanent` | `Permanent` |

Deleting a missing object succeeds. A GCS HEAD response has no body, so a HEAD
cannot tell a missing bucket from a missing object and returns `None`; the
startup self check fails on a missing bucket instead.

Bodies and files up to 16 MiB use one request. Multipart uploads use 8 MiB
parts, and the request timeout applies to each part. A failed multipart
completion keeps its own outcome: an abort only cleans up, and its failure is
logged. After a completion with an unknown outcome, no abort is sent.

A larger create-only write uploads in parts to `staging/<millis>-<uuid>`, copies
it to the key only if the key is absent, and deletes the staging object. When
Azure reports the copy pending, the store polls the key for up to a minute. A
finished copy is a create; a failed copy reports `Corrupt`; a copy still
pending reports `OutcomeUnknown` and keeps the staging object.
`delete_abandoned_staging_objects` deletes staging objects that the store last
modified over an hour ago on GCS, or over 15 days ago on Azure, where a pending
copy may read its source for up to two weeks. The server calls it at startup,
and the store repeats it at most hourly after a staged create.

`object_store` has no conditional multipart upload, so a replacement by ETag of
a file over 16 MiB fails with a permanent error. A replacement of an in-memory
body of any size uses one request.

Listings check that keys ascend and stay inside the prefix, and fail rather than
sort a page. Azure listings read a blob named exactly the prefix with a head
request, because `object_store` drops it from listings.

GCS application default credentials support `service_account` and
`authorized_user` files and the metadata server. `object_store` does not support
`external_account` or `impersonated_service_account` files.

## Testing

The shared conformance suite exercises the in-memory implementation and an in-process s3local in strict, process-crash-safe, and relaxed durability modes. It needs no cloud credentials or external service.

`aliyun_oss_conformance` and `aliyun_oss_versioned_cas` run `AliyunOssObjectStore` over an in-memory store that rejects `ReplaceIfEtagMatches`. They cover racing writers, stale writers after reclamation and resurrection, superseded writers, injected faults, tombstones, probe removal, and Flow definition keys. The racing histories must fit a sequential compare-and-swap order in real time. One conformance test also runs the adapter over `S3ObjectStore` against s3local's Aliyun OSS dialect, with content and opaque ETags.

Azure Blob Storage and GCS error mapping runs against a scripted local HTTP server in the same suite.

The conformance suite runs against real Azure and GCS accounts only manually. Run it after changing that backend, after upgrading `object_store`, and before a release. Each run writes under a unique prefix and deletes it afterwards. Leave `DEX_GCS_SERVICE_ACCOUNT_PATH` empty to use application default credentials.

```bash
export DEX_AZURE_ACCOUNT=<account> DEX_AZURE_ACCESS_KEY=<key> DEX_AZURE_CONTAINER=<container>
export DEX_GCS_BUCKET=<bucket> DEX_GCS_SERVICE_ACCOUNT_PATH=<key file, or empty>
cargo test --manifest-path server-zd/Cargo.toml -p dexzd-object-store --test conformance \
    azure_gcs_cloud -- --ignored --nocapture
```

## db9 SQL provider

`Db9ObjectStore` uses the PostgreSQL wire protocol directly, with no S3 gateway.
It creates `dex_object_metadata` and `dex_object_chunks` in the connection's
search path. Use a dedicated database or schema and keep the search path stable.
The SQL user needs DDL permission for startup and SELECT/INSERT/UPDATE/DELETE on
these tables. Bucket and prefix provide logical isolation, not access control.

Each write locks or creates the metadata row, replaces 256 KiB chunks, updates
size and modification time, and commits in one database transaction. Create-only
uses `ON CONFLICT DO NOTHING`; ETag replacement uses an atomic conditional UPDATE.
A new random ETag is assigned on every successful write, including identical
content and recreation after deletion. Readers hold `FOR SHARE` on metadata
until every requested chunk has been read. Deletes acquire the metadata lock
before removing chunks. Missing-key deletes never delete chunks belonging to a
concurrent new object. Metadata and payload must not be modified outside this
provider.

The backend must provide atomic durable transactions, unique primary keys,
row-level locking, read-your-writes, and strongly consistent reads. A successful
SQL COMMIT is the durability boundary. Distributed replication and availability
are provided by the database deployment; the provider does not add replication.
Do not use a read replica, an eventually consistent SQL endpoint, or transaction
pooling that cannot retain one transaction on a single backend connection.

File uploads and downloads use bounded chunks. Whole-object `get` returns the
entire body in memory as required by `ObjectStore`; range reads fetch only the
intersecting chunks. Files are published through `TemporaryDownloadFile` after
all reads succeed. Large uploads are single transactions: database transaction
size/duration limits still apply, and readers/writers on that key can block.
There is no object throughput parity claim with S3.

Connections are bounded and reused, and reconnect after transport closure.
Each SQL request has a deadline. Write transport failures and timeouts return
`OutcomeUnknown`, not a false precondition failure; callers must resolve the
outcome. Deadlocks and serialization failures are transient. SQL error messages
expose SQLSTATE only, never credentials or object contents. Dropping a store
aborts its owned connection drivers. Cancellation drops unfinished transactions,
which enqueue rollback through tokio-postgres.

`Db9ObjectStoreConfig` accepts the connection string, logical bucket, key prefix,
connection count (default 8, range 1-64), and per-request timeout (default 10 s).
TLS uses the shared PostgreSQL connection implementation. Use `sslmode=verify-full`
and trusted roots for remote endpoints; there is no automatic plaintext fallback
from a configured verified connection.

### Running the database-backed tests

Supply `DEX_ZD_DB9_CONNECTION_STRING` through your secret manager or environment.
The explicit feature requires a reachable database; missing credentials fail the
tests rather than skipping them. Test namespaces are unique. The suite includes
the same object-store conformance checks as S3, independent-client create/CAS
races, ETag ABA prevention, chunk-boundary reads, namespace isolation, rollback
on failed upload, and concurrent read/write consistency.

```bash
make -C server-zd db9ObjectStoreTests
```

The provider speaks the PostgreSQL wire protocol, so the same tests run against
PostgreSQL. Server ZD CI adds this suite against PostgreSQL 16 to a pull request
only while it carries the `db9-tests` label; adding the label starts that suite.
PostgreSQL checks the provider code, not db9's own locking and isolation.
