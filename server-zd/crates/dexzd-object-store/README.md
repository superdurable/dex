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

`AliyunOssObjectStore` wraps a store whose only conditional write is create-only, such as Aliyun OSS or Tencent COS. Every other key passes through unchanged and rejects `ReplaceIfEtagMatches` with a permanent error. `supports_replace_if_etag_matches_on_every_key` then returns false; wrappers forward the answer.

A mutable key `K` is stored as create-only versions at `K.v/<u64::MAX - generation>`, zero-padded to 20 digits, so a one-key listing returns the newest version. ETags are `"g<generation>-<version ID>"`. A delete writes a tombstone version.

The mapping is injective. A mutable key may contain `.` but never `.v/`, so a Flow type such as `Order_Flow-v1.2` keeps its key. The 20 digits, and the suffix `anchor` of the anchor object that HEAD probing adds, hold no `.v/`, so the last `.v/` of a version key is its separator and determines `K`. The version prefixes of two different mutable keys never overlap, since one key would have to contain `.v/`. A plain key is rejected when the text before any of its `.v/` is a mutable key, so no plain key lies under a version prefix. Flow definition keys are `registry/flow-definitions/<tenant ID>/types/<segment>`: tenant IDs are decimal, and segments percent-encode `/`.

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

After a confirmed write of generation `n`, a background task deletes generations `n - 2` and below whose successor was written at least the minimum age before `n`, by the store's own timestamps. Production passes `MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION` (10 minutes) to `AliyunOssObjectStore::new`, which rejects ages below one second; tests and simulations pass 60 seconds. It keeps tombstones. A node lease renewed every 3.3 seconds keeps about 180 versions, and each renewal's reclamation lists them in one page; the versions of a key's last 10 minutes stay until its next write.

The age also keeps replacements safe if a listing lags behind writes. A lagging listing can hide the newest generation `g`, so a writer creates `g` again; the create-only write conflicts and the replacement fails. Only a lag as long as the age could show a reclaimed generation as the newest and let a writer recreate it. This holds once every process sharing the bucket uses the age; a process of an older release, during a rolling upgrade or after a rollback, still reclaims after 60 seconds. `dexzd_object_store_versioned_listing_stale_total` counts, by `site`, each listing that misses the version this process just created (`confirm_created_version`) or confirmed (`reclamation`), and each event logs a warning with the key and both generations. Reclamation first lists the newest version: when it is at least two generations newer, that writer may have reclaimed the version, so nothing is counted.

Startup probe keys belong to one process, so deleting one removes every version. `remove_stale_startup_probes` removes probe keys whose newest object is older than 24 hours by the store's clock. It reads that clock by writing and deleting a clock probe object of its own; when another sweep removes that object first, the sweep skips and removes nothing. A running check holds its probe for at most `STARTUP_PROBE_MAXIMUM_LIFETIME`. A process suspended during a check, by host sleep or `SIGSTOP`, can hold it longer: after more than 24 hours the sweep may remove the probe, and the resumed check then fails or passes on a new probe. Restart the server when that check fails. Probe keys are separate from leases and the manifest, so this never affects fencing, and the sweep deletes only keys under `probe/`. Each read and write of a mutable key adds one listing; a replacement also reads one version header. Flow definition reads therefore cost a listing and a GET, and listing definitions takes the slow listing path. A version key is `VERSION_OBJECT_KEY_SUFFIX_BYTES` (23) bytes longer than its logical key.

The inner store must create atomically. With `VersionDiscovery::Listing`, the default of `AliyunOssObjectStore::new`, it must also list consistently right after each write. The paragraphs above describe that discovery, which the server uses for Aliyun OSS. The layout differs from every other provider, so data cannot move between them.

### HEAD probing

`AliyunOssObjectStore::with_version_discovery` with `VersionDiscovery::HeadProbing` serves Tencent COS, whose listings can repeat a listing from seconds earlier. It needs strongly consistent HEAD, GET, create-only PUT, and DELETE of single objects. A read that returns a version, and the outcome of a conditional write, never depend on a listing or on any clock. Listings still matter in three ways. A stale listing adds requests, and a read retries from a listing, so a listing that stays stale through 16 attempts makes the read fail with `Transient`. A logical `list` of mutable keys finds them through physical listings, so it can miss a key that a stale listing misses. And the startup check below finds older versions only through listings. The minimum age, measured by store timestamps, only decides how long superseded versions stay. Every process that shares a prefix must use the same discovery, because listing discovery reclaims in any order and keeps no anchor.

HEAD probing starts only on an empty prefix. This is a precondition that the operator keeps. Versions that listing discovery wrote can have gaps and lack the anchor, so a HEAD-probing read could take a remaining old generation, or a missing generation 1, for the newest state.

`check_version_discovery_marker` checks that at startup, on a best-effort basis, before the adapter writes a version. A HEAD-probing store creates the small marker `version-discovery/head` (`HEAD_PROBING_MARKER_OBJECT_KEY`), a plain create-only key that is never deleted. A listing store refuses a prefix with the marker. A HEAD-probing store refuses a prefix without the marker that holds a version under any mutable key except startup probes, found by one-key listings. Before it refuses, it reads the marker again: a HEAD-probing node that started at the same time may have marked the prefix and written versions after the first read. The check is best effort. It reliably finds a prefix with versions only where listings show every write, as Aliyun OSS listings did in every test; a stale listing can look empty. It records no mode beyond the marker. The server runs the check for every aliyunOSS and tencentCOS store. It cannot see a node that is already running, and releases before 0.1.31 skip it, so a prefix that uses HEAD probing needs every node on 0.1.31 or later with HEAD probing, started after all listing nodes stopped.

Terms. The *first version* of generation `g` is the first object ever stored at its key. Any later object at that key is *recreated*: a stale writer created it after reclamation deleted the first version. Version IDs are random and each body is written once. So an object seen present and later read with the same physical ETag, or with the same version ID, was present the whole time in between.

Invariants:

1. A writer creates generation `g ≥ 2` only after a read confirmed the first version of `g - 1` as the newest, and names its ID as predecessor. Every object at `g`, first or recreated, therefore names the first version of `g - 1`. This holds by induction over reads, each of which the argument below shows correct.
2. Reclamation deletes generation `k` only after a confirmed write of `k + 2` or later, and only after it saw generation `k - 1` absent, by a HEAD or by its own earlier deletion of `k - 1`. Before it deletes generation 1, it creates the anchor `K.v/anchor`, a small object that is never deleted. Neither the anchor nor the marker is empty, because Tencent COS cannot create an empty object exclusively. A writer deletes only an object it proved recreated. So the first versions that remain form one consecutive range of generations that ends at the newest, the newest two generations remain once they exist, and every recreated object lies below that range.
3. While the anchor is absent, generation 1 was never deleted.

Startup probe keys are outside these invariants. Only their creating process uses them, a delete removes all of their versions, and the stale probe sweep removes them by listing.

A read starts from a lower bound `L`. It is the version this process last confirmed (its hint), else the newest version of a one-key listing, else 0. The read HEADs `L + 1`, `L + 2`, and so on, until generation `N + 1` is missing. It then GETs `N` and requires the object that showed `N` present before that HEAD: the same physical ETag, and for a hint also the same version ID. So `N` was present when `N + 1` was missing. Last, it checks that `N` holds a first version. When `N` is the hint itself, it already does. Otherwise, for `N ≥ 2`, generation `N - 1` must hold the version that `N` names as its predecessor. For `N = 1`, the anchor must be absent. A recreated `N` fails this check: by invariant 1 it names the first version of `N - 1`, which reclamation deleted before the first version of `N`. A first version that was present while the next generation was missing was the newest at that moment, because reclamation never deletes a generation above a remaining first version. That moment linearizes the read. When probing from 0 finds generation 1 missing, the read checks the anchor after that HEAD. An absent anchor proves the key absent, by invariant 3. A present anchor means generation 1 was reclaimed. Every failed check retries from a listing without the hint, and after 16 attempts the read fails with `Transient`.

Four interleavings need care:

- (a) Reclamation deletes a listed `L` before the read's HEAD requests. Probing passes `L` when a newer generation exists. Otherwise the GET of `L` finds no object or another one, and the read retries.
- (b) Reclamation deletes a generation during probing. Probing goes past it, or the candidate's GET or predecessor check fails and the read retries. A candidate's predecessor goes only after the candidate is no longer one of the newest two.
- (c) A recreated object can sit below the first versions with a gap above it. Probing stops in that gap only with the recreated object as its candidate, and its predecessor check fails. Probing passes over a recreated object that has a present generation above it.
- (d) A candidate counts only when the GET returns the object whose presence the read saw before the next generation was missing. A recreated object has a new version ID and physical ETag, so it never passes for an object seen earlier.

A replacement reads the newest version this way and requires the ETag's generation and version ID; a create requires no version or a tombstone. The writer then creates generation `g` and confirms its object `X`:

1. For `g ≥ 2`, generation `g - 1` still holds the predecessor that the writer read. For `g = 1`, the anchor is absent. Either way no object at `g` was ever deleted, by invariants 2 and 3. The create found the key empty, so `X` is the first version: `Written`.
2. Otherwise, generation `g + 1` names `X` as its predecessor: `Written`. When it names another version, `X` is recreated, by invariant 1: the writer deletes `X` and reports `PreconditionFailed`.
3. Otherwise, `g + 1` is missing too. The missing predecessor or the present anchor shows that reclamation started below `g`, after a confirmed write of `g + 1` or later. So the first version of `g + 1` existed and is gone now, and reclamation deleted it only after the first version of `g`. So when generation `g` still holds `X`, `X` is recreated: the writer deletes it and reports `PreconditionFailed`. When `X` is gone, the outcome is unknown.

A failed request during the confirmation reports `OutcomeUnknown`. Deleting a recreated `X` by key is safe, because no first version can return to that key.

After a confirmed write of generation `c`, a background pass HEADs `c` for its write time and lists the versions older than `c`. It walks up from the oldest listed version to `c - 2`. It deletes generation `k` when `k`'s successor was written at least the minimum age before `c`, by the store's timestamps, and `k - 1` is gone. Superseded tombstones go too: only the newest two generations serve reads and confirmations. Each request deletes one object, and the pass stops at the first failure or at the first version it keeps. A failed, interrupted, or crashed pass therefore leaves the remaining versions consecutive, and the anchor exists before generation 1 goes. The process remembers the highest generation it deleted for each key. Every first version up to it is gone for good, so an object that a listing still shows there can only be a stale writer's recreated version whose own cleanup failed. The pass HEADs it and deletes it when it is there: it lies below the consecutive versions, so no read returns it, and deleting it opens no gap. A stale writer that has not confirmed that version yet then reports `OutcomeUnknown` instead of `PreconditionFailed`, which is still correct for a write that never took effect. The production age of 10 minutes keeps confirmations and reads from finding a neighbor reclaimed, so they rarely report `OutcomeUnknown` or retry.

No clock takes part in a decision. Reads and confirmations compare only which objects exist and which IDs they hold, in the order the requests ran. The minimum age changes only which versions a pass keeps, and every deletion keeps the invariants whatever the age, so a slow host clock, a frozen VM, or a store clock that jumps ahead cannot make a read or confirmation wrong.

A read with a hint costs a HEAD of the next generation and a GET; right after this process's own write, it also HEADs its version for the write time. A read from a listing adds the listing and a GET of the predecessor's header. A write adds the create and one confirmation read, and reclamation adds a HEAD, a listing, and one DELETE for each deleted version. A listing whose first version is not the newest, or is gone, is counted at `newest_version_lookup`; another process's write between the listing and the HEAD requests counts too. Probe keys are deleted by key, one at a time from generation 1 up to the newest. A failed probe delete keeps the newest version and the hint, so a retry by the same process finds it and deletes the rest.

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

`aliyun_oss_conformance` and `aliyun_oss_versioned_cas` run `AliyunOssObjectStore` over an in-memory store that rejects `ReplaceIfEtagMatches`. `aliyun_oss_head_probing` and `aliyun_oss_head_probing_adversarial` run HEAD probing there, over listings that repeat for seconds or forever, with a store clock that tests advance on their own or that runs a thousand times faster than the processes' clocks, and with processes frozen in the middle of a write. They cover racing writers, stale writers after reclamation and resurrection, superseded writers, injected faults, tombstones, probe removal, and Flow definition keys. The racing histories must fit a sequential compare-and-swap order in real time. One conformance test also runs the adapter over `S3ObjectStore` against s3local's Aliyun OSS dialect, with content and opaque ETags, and another against its Tencent COS dialect.

The Tencent COS dialect of `S3ObjectStore` creates objects with a signed AppendObject at position 0. COS shows an empty appendable object as soon as it receives the headers of an append to a missing key, and keeps it after an interrupted append; it checks the append position only at commit. So the dialect reads every 0-byte object as absent: GET, ranged GET, `get_to_file`, and HEAD return `None`, and `list` omits such entries. `list` keeps paging until it fills `max_keys`, so a one-key listing never comes back empty while non-empty objects follow; every page keeps only keys after its cursor, and an untruncated page without such a key ends the listing with no cursor. A truncated page without such a key is a replay of the COS listing cache: it is requested again with backoff for `S3ObjectStoreConfig::stale_listing_retry_window` (`TENCENT_COS_STALE_LISTING_BOUND`, 5 seconds), and then the listing fails with `Transient` rather than end early. So every returned cursor is after the caller's `start_after`, caller loops end, and no listing ends before a later page. A ranged GET that answers 416 while HEAD shows bytes in range, as a placeholder whose append committed does, is read again up to three times and then fails as `Transient`, so it never returns empty bytes for those bytes. Every write of an empty body fails with `Permanent` before any request, so no written object reads as absent. A create over an empty object succeeds, and concurrent creates still have one winner. The store never deletes an empty object it hides: a delete could race with the commit of the append that showed it. An interrupted create at a log seq or version generation is replaced by the next create there; one at a key that is never written again stays as a 0-byte object. The conformance suite runs it against s3local's Tencent COS dialect with strict signature checks: shared cases, racing creators, empty and oversized bodies, ambiguous appends, and the bucket checks. `tencent_cos_empty_objects` uses s3local's `append_placeholder` and `interrupted_append` faults to cover appends in flight and interrupted, one-key and stale listings driven to their end or to `Transient`, repeated 416 answers, and HEAD-probing reads and lease renewals past an empty generation. `tencent_cos_bucket_checks` covers the check's timeouts and cleanup failures.

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
