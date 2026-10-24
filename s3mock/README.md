# s3mock

## Purpose

An S3-compatible object store on local disk, so Dex development and tests need no MinIO or cloud account.

## Crates

| Crate | Purpose |
| --- | --- |
| [`s3mock`](crates/s3mock/) | The store: redb metadata plus one self-describing data file per object version. |
| [`s3mock-server`](crates/s3mock-server/) | The `s3mock` binary and library: path-style S3 over HTTP/1.1. |
| [`s3mock-conformance`](crates/s3mock-conformance/) | Conformance tests that run against s3mock or real S3 through `aws-sdk-s3`. |

## Run

```bash
make run ARGS="--data-directory /tmp/s3mock-data --listen-address 127.0.0.1:9000"
```

Every flag also reads an `S3MOCK_*` environment variable; the control flags are listed below.
Point an S3 client at `http://127.0.0.1:9000` with path-style addressing
and any access key. Request signatures are not verified by default.

## Releases

The manual `S3mock release` workflow runs from `main` with a semantic `version` matching
the workspace version in `s3mock/Cargo.toml`. It creates a
new `s3mock/v<version>` tag and GitHub release only after native Linux amd64 and arm64 binaries
and images pass smoke tests. The workflow rejects an existing versioned tag. The release contains binary
archives and SHA-256 checksums; its notes give the digest of the multi-architecture
`docker.io/superdurable/s3mock:<version>` image. The workflow has no automatic trigger.
GitHub-enforced locking of published tags and assets requires a separate repository release
immutability setting.
Before publishing the GitHub release, the workflow checks that the image digest can be read
without Docker Hub credentials. If Docker Hub creates the repository as private, make it public
and rerun the workflow on the same commit and version. The existing image tag is reused only
when its recorded source commit matches.

The image runs as user 10001. It listens on `0.0.0.0:9000` and stores data in the
`/var/lib/s3mock` volume. Every command-line flag can be set through its `S3MOCK_*` environment
variable, including `S3MOCK_LIFECYCLE_SCAN_INTERVAL_SECONDS`. For example:

```bash
docker run --rm -p 9000:9000 -v s3mock-data:/var/lib/s3mock \
  -e S3MOCK_SIGNATURE_VERIFICATION=strict \
  docker.io/superdurable/s3mock:<version>
```

Lifecycle rules run every `--lifecycle-scan-interval-seconds` (default 60; `0` disables).
A pass removes everything due at the lifecycle clock, so expiration happens within one interval
of its due time instead of on S3's daily schedule. Tests advance the lifecycle clock instead of
waiting days; dates, tag and size filters, and transitions return `501 NotImplemented`.

`--durability strict` (the default) flushes every acknowledged write with fsync, so it survives a
crash. `--durability relaxed` skips fsync. It is much faster, but a crash may lose acknowledged
writes, so do not use it for crash-recovery tests.

## Store format

The `store_format` table in `metadata.redb` records the store format version of a data directory.
This release reads and writes version 1. A directory created before the version was recorded is
treated as version 1 and stamped when it opens. A directory with any other version fails to open,
and its records stay unchanged. The error names the found version, the supported version, and the
directory. To recover, run an s3mock release that supports that version, or delete the data
directory to start empty. Deleting it removes every stored object and credential.

Compatibility covers upgrades only. Keep metadata records additive: give every new field a serde
default, and never rename, remove, or reinterpret a field. Never change the name, key, or value
types of the `store_format` table, because older releases read it to refuse newer directories.
Any incompatible change, including a redb major upgrade that changes its file format, must
increment `CURRENT_STORE_FORMAT_VERSION`. The same change must either migrate or refuse the
previous version, and must add recorded testdata for the new version.

`crates/s3mock/tests/testdata/store_format_v1/` holds a store that s3mock 0.1.0 wrote. Its
`manifest.json` records the writer, and `expected_contents.json` records what the store must read
back. The `store_format` test opens a copy and compares every bucket, version, delete marker,
upload, part, credential, CORS rule, lifecycle rule, and Object Lock setting. It also opens a copy
without the `store_format` table, as written before the version existed. Never edit or regenerate a
recorded directory. To record a new version, run the ignored generator with a new output
directory:

```bash
S3MOCK_STORE_FORMAT_OUTPUT="$PWD/crates/s3mock/tests/testdata/store_format_v2" \
  cargo test -p s3mock --test store_format -- --ignored --exact generator::generate_recorded_store
```

## Supported S3 API

| Area | Operations |
| --- | --- |
| Buckets | CreateBucket, HeadBucket, DeleteBucket (ends in-progress uploads), ListBuckets, GetBucketLocation |
| Versioning | GetBucketVersioning, PutBucketVersioning, ListObjectVersions, version-addressed GET, HEAD, and DELETE |
| Objects | PutObject, GetObject, HeadObject, DeleteObject |
| Listing | ListObjectsV2 with prefix, delimiter, max-keys, continuation-token, start-after, and encoding-type |
| Conditions | PutObject `If-None-Match: *` and `If-Match`; GET and HEAD `If-Match`, `If-None-Match`, `If-Modified-Since`, and `If-Unmodified-Since` |
| Integrity | `Content-MD5`; CRC32, CRC32C, CRC64NVME, SHA1, and SHA256 checksums in headers or aws-chunked trailers |
| Ranges | One `Range: bytes=` range per request |
| Multipart | CreateMultipartUpload, UploadPart, CompleteMultipartUpload (with `If-None-Match` and `If-Match`), AbortMultipartUpload, ListParts, ListMultipartUploads |
| Batch delete | DeleteObjects with Quiet mode and per-key results |
| Object Lock | Get/PutObjectLockConfiguration and CreateBucket with Object Lock; retention is stored, never enforced |
| Lifecycle | Put/Get/DeleteBucketLifecycle with prefix filters: `Expiration` `Days` and `ExpiredObjectDeleteMarker`, `NoncurrentVersionExpiration` (with `NewerNoncurrentVersions`), and `AbortIncompleteMultipartUpload` |
| CORS | PutBucketCors, GetBucketCors, DeleteBucketCors, preflight OPTIONS, and cross-origin object requests |

Other operations and subresources return `501 NotImplemented`.

## Test

```bash
make test
S3MOCK_CONFORMANCE_TARGET=aws make test
```

The second command runs the conformance suite against real S3; all 45 tests passed against AWS S3
in us-east-1 on 2026-10-23. AWS answered 500 InternalError when a checksummed multipart upload
listed part 2 without part 1; s3mock returns the documented 400 InvalidPartOrder instead. Run it on
demand, for example before relying on a newly emulated S3 behavior, with local AWS credentials:

```bash
AWS_PROFILE=<profile> AWS_REGION=us-east-1 S3MOCK_CONFORMANCE_TARGET=aws make test
```

It creates and deletes buckets named `s3mock-conformance-<ULID>`; set
`S3MOCK_CONFORMANCE_BUCKET_LOG=<file>` to record them so leftovers from failed tests can be deleted.
The `S3mock AWS conformance` workflow can run the same suite manually once the repository variable
`S3MOCK_CONFORMANCE_AWS_ROLE_ARN` names an IAM role that GitHub OIDC can assume; it has no schedule.

## Testing controls

All controls are opt-in and local to one server process. They are independent of the store's
persisted objects. `s3mock_server::start_with_config` accepts the same `ServerConfig` used by the
binary; `start` uses explicit defaults and ignores `S3MOCK_*` environment variables.

| Flag | Environment variable | Default | Meaning |
| --- | --- | --- | --- |
| `--fault-seed` | `S3MOCK_FAULT_SEED` | `0` | Seed for deterministic fault selection. |
| `--counter-prefix-depth` | `S3MOCK_COUNTER_PREFIX_DEPTH` | `1` | Slash-separated key components included in prefix counters; `0` groups all keys. |
| `--request-log-capacity` | `S3MOCK_REQUEST_LOG_CAPACITY` | `1024` | Recent completed requests retained; `0` disables retention. |
| `--etag-mode` | `S3MOCK_ETAG_MODE` | `md5` | `md5` or `opaque`. |
| `--signature-verification` | `S3MOCK_SIGNATURE_VERIFICATION` | `off` | `off` or `strict`. |
| `--control-authentication` | `S3MOCK_CONTROL_AUTHENTICATION` | `off` | `root` requires root SigV4 signatures on every test control when signature verification is strict. |
| `--access-key-id` | `S3MOCK_ACCESS_KEY_ID` | `s3mock` | Root access key; the only identity that can manage credentials. |
| `--secret-access-key` | `S3MOCK_SECRET_ACCESS_KEY` | `s3mock` | Root signing secret. |

Opaque ETags identify a committed version rather than its MD5. Repeated HEAD/GET requests and
server restarts return the same ETag for that version. Writing identical bytes creates a different
opaque ETag. Use the returned ETag in `If-Match`; use checksum headers to compare content.

Strict mode verifies SigV4 header and presigned query authentication, SHA256 payload hashes, `UNSIGNED-PAYLOAD`,
`STREAMING-UNSIGNED-PAYLOAD-TRAILER`, and chained signatures for
`STREAMING-AWS4-HMAC-SHA256-PAYLOAD` and `STREAMING-AWS4-HMAC-SHA256-PAYLOAD-TRAILER`.
The latter also requires a valid trailer signature covering the declared trailers. Invalid signatures
return `403 SignatureDoesNotMatch`; an unknown access key returns `403 InvalidAccessKeyId`,
and requests without a valid authorization header return `403 AccessDenied`.
Presigned URLs must include `X-Amz-Algorithm`, `X-Amz-Credential`, `X-Amz-Date`,
`X-Amz-Expires`, `X-Amz-SignedHeaders`, and `X-Amz-Signature`. Their signature uses
`UNSIGNED-PAYLOAD`, and expiry must be between 1 and 604800 seconds. An expired URL returns
`403 AccessDenied`. Presigned requests use the same credential status and policy checks as
header-signed requests. Signature timestamps must match the credential date and use the
YYYYMMDDTHHMMSSZ format. The region must be `us-east-1`.
All `x-amz-*` headers must be signed except `x-amz-content-sha256`, which is already bound by the
canonical payload hash. Malformed streaming framing returns `400 IncompleteBody`; clock freshness and session token issuance are not simulated. Only use test credentials.
Strict mode spools the encoded body to an anonymous temporary file under the store’s `tmp/` directory
and verifies it before any S3
handler runs, then streams it to the handler. Memory use stays bounded, but uploads require extra
local disk space and I/O. Encoded request bodies are limited to 6 GiB. Bodiless requests avoid spooling.
Off mode keeps the original streaming upload path and accepts any access key.

Strict mode recognizes the root key and durable scoped credentials. The root key has full S3 access.
Each scoped credential has an IAM policy and can be enabled or disabled. An unknown or disabled key returns
`403 InvalidAccessKeyId`. An unmatched action or resource returns `403 AccessDenied`; an explicit
`Deny` overrides every `Allow`. Policies support `Version: "2012-10-17"`, one `Statement` or an array,
`Allow` and `Deny`, case-insensitive exact `s3:<Action>` or `s3:*`, bucket and object ARNs, and `*` and `?` in object
key patterns. `arn:aws:s3:::*` matches all buckets and objects. A `Condition` is supported only for
`s3:ListBucket` or `s3:ListBucketVersions`: `StringEquals` or `StringLike` on `s3:prefix`.
Every other condition key or operator, principals, negated actions or resources, and unknown
fields and unsupported S3 actions are rejected when the credential is created. A statement may
have an optional `Sid`. Access key IDs contain only letters, digits, `.`, `_`, and `-`. An absent
prefix does not satisfy a prefix condition, even one that matches an empty prefix. HeadBucket has
no prefix. GET and HEAD for a missing key return 404 when `s3:ListBucket` permits listing with
`s3:prefix` equal to that key; otherwise they return `403 AccessDenied`.

### Strict-mode IAM actions

| S3 operation | Required action | Resource |
| --- | --- | --- |
| ListBuckets | `s3:ListAllMyBuckets` | `arn:aws:s3:::*` |
| CreateBucket | `s3:CreateBucket` | Bucket |
| HeadBucket, ListObjectsV2 | `s3:ListBucket` | Bucket |
| DeleteBucket | `s3:DeleteBucket` | Bucket |
| GetBucketLocation | `s3:GetBucketLocation` | Bucket |
| GetBucketVersioning | `s3:GetBucketVersioning` | Bucket |
| PutBucketVersioning | `s3:PutBucketVersioning` | Bucket |
| GetBucketCors | `s3:GetBucketCORS` | Bucket |
| PutBucketCors, DeleteBucketCors | `s3:PutBucketCORS` | Bucket |
| GetBucketLifecycleConfiguration | `s3:GetLifecycleConfiguration` | Bucket |
| PutBucketLifecycleConfiguration, DeleteBucketLifecycle | `s3:PutLifecycleConfiguration` | Bucket |
| GetObjectLockConfiguration | `s3:GetBucketObjectLockConfiguration` | Bucket |
| PutObjectLockConfiguration | `s3:PutBucketObjectLockConfiguration` | Bucket |
| ListMultipartUploads | `s3:ListBucketMultipartUploads` | Bucket |
| ListObjectVersions | `s3:ListBucketVersions` | Bucket |
| PutObject, CreateMultipartUpload, UploadPart, CompleteMultipartUpload | `s3:PutObject` | Object |
| GetObject, HeadObject | `s3:GetObject`; with `versionId`, `s3:GetObjectVersion` | Object |
| DeleteObject | `s3:DeleteObject`; with `versionId`, `s3:DeleteObjectVersion` | Object |
| DeleteObjects | `s3:DeleteObject` per key; with `VersionId`, `s3:DeleteObjectVersion` per key | Object |
| AbortMultipartUpload | `s3:AbortMultipartUpload` | Object |
| ListParts | `s3:ListMultipartUploadParts` | Object |

For a CreateBucket request that enables Object Lock, the bucket policy must also allow
`s3:PutBucketObjectLockConfiguration` and `s3:PutBucketVersioning`. Bucket resources have the
form `arn:aws:s3:::<bucket>`; object resources have the form `arn:aws:s3:::<bucket>/<key>`.
The [AWS S3 permissions reference](https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-with-s3-policy-actions.html)
defines these action names.

### Bucket CORS

PutBucketCors stores S3 XML rules per bucket; GetBucketCors returns them, and DeleteBucketCors
removes them. Rules survive restarts. A rule specifies AllowedOrigin and AllowedMethod entries,
and may specify AllowedHeader, ExposeHeader, MaxAgeSeconds, and ID. S3 allows one `*` in an origin
or allowed-header pattern. Requested headers match case-insensitively. The first rule matching the
origin, method, and every requested header controls the response.
An AllowedOrigin of `*` returns `Access-Control-Allow-Origin: *` without
`Access-Control-Allow-Credentials`.

OPTIONS preflights need no signature. A matching preflight or actual request with `Origin` receives
the S3 `Access-Control-Allow-*`, `Access-Control-Expose-Headers`, `Access-Control-Max-Age`, and
`Vary` headers supplied by its rule. A preflight with an unmatched origin, method, or requested
header, or a bucket without CORS configuration, returns 403. An actual request with no matching
rule runs normally without CORS headers; the browser then blocks access to its response. Requests
without `Origin` keep their existing behavior.
For browser presigned PUT, GET, and HEAD, one rule can allow a single origin, the methods `PUT`,
`GET`, and `HEAD`, headers `content-type`, `if-none-match`, `x-amz-checksum-sha256`, and
`x-amz-meta-sha256`, expose `ETag`, and set MaxAgeSeconds to 600.

### Admin HTTP API

The same listener serves JSON endpoints under `/_s3mock/`. Bucket names cannot start with `_`.
Credential requests must carry a root-key SigV4 header signature in strict mode; off mode rejects
them. Other test controls remain unauthenticated by default in both modes. Set
`--control-authentication root` to require a root signature on all test controls in strict mode.
Admin requests
bypass fault injection and do not affect S3 counters or logs.
Keep the listener on loopback when using these test controls. Successful calls return HTTP 200;
invalid rule documents return HTTP 400 with `{"error":"..."}` and preserve existing rules.
Unknown admin paths or methods return HTTP 404 JSON.

| Method and path | Request | Response |
| --- | --- | --- |
| `PUT /_s3mock/faults` | `{"rules":[...]}` | Replaces all rules atomically and restarts the seeded sequence; returns `rules` and `seed`. |
| `GET /_s3mock/faults` | None | `{"rules":[...],"seed":0}`. |
| `DELETE /_s3mock/faults` | None | Clears rules and restarts the sequence; `{"rules":[]}`. |
| `GET /_s3mock/counters` | None | `{"prefix_depth":1,"counters":{"by_operation":{...},"by_prefix":{...}}}`. |
| `DELETE /_s3mock/counters` | None | Resets counters only; `{"reset":true}`. In-flight requests are counted when they finish. |
| `GET /_s3mock/requests` | None | `{"capacity":1024,"requests":[...]}` in completion order, oldest first. |
| `POST /_s3mock/gc` | None | Runs orphan collection; `{"examined_files":0,"removed_files":0}`. |
| `GET /_s3mock/lifecycle/clock` | None | Returns the lifecycle time and its offset from wall-clock time; `{"now_unix_ms":0,"offset_ms":0}`. |
| `POST /_s3mock/lifecycle/advance` | `{"milliseconds":86400000}` | Moves the lifecycle clock forward (never backward) and returns the clock. |
| `POST /_s3mock/lifecycle/run` | None | Applies lifecycle rules once at the lifecycle time; returns `expired_current_versions`, `removed_delete_markers`, `expired_noncurrent_versions`, and `aborted_uploads`. |
| `POST /_s3mock/credentials` | `{"access_key_id":"key","secret":"write-only","parent":"owner","description":"reader","policy":{...},"status":"enabled"}` | Creates a credential; `409 CredentialAlreadyExists` never overwrites an existing key. Returns the record without `secret`. |
| `GET /_s3mock/credentials/{accessKeyId}` | None | Returns the record without `secret`, or `404 NoSuchCredential`. |
| `PATCH /_s3mock/credentials/{accessKeyId}` | `{"status":"enabled"}` or `{"status":"disabled"}` | Changes only the status and returns the record without `secret`. |
| `GET /_s3mock/credentials?parent=<parent>` | None | Returns `{"credentials":[...]}` for that opaque owner, without secrets. |
| `DELETE /_s3mock/credentials/{accessKeyId}` | None | Deletes a credential, or returns `404 NoSuchCredential`. |

GC uses the store's safety boundary: it examines data files older than the store's open time, so
an upload in flight cannot lose its data file. It does not advance a clock or expire live objects.

A rule has optional `operation`, `bucket`, `key_prefix`, and `has_conditional_header` match fields,
an optional `probability` from 0 through 1 (default 1), and one required `action`. Match fields
combine with AND. Omitted fields match any value. Operation names use S3 API spelling, such as
`PutObject`, `GetObject`, or `HeadObject`. Conditional headers mean `If-Match`, `If-None-Match`, `If-Modified-Since`, or `If-Unmodified-Since`.
Unknown operation names and action fields are rejected. Rules run in order; each matching rule consumes a seeded random draw, and the first selected rule
wins. Replacing the rules reproduces a sequence for the same matching request arrival order;
concurrent scheduling can change that order. An SDK retry is a separate request and draw.

| Action JSON | Behavior |
| --- | --- |
| `{"type":"commit_then_drop"}` | Requires `operation` to be `PutObject`, `UploadPart`, or `CompleteMultipartUpload`. A successful store commit closes the connection without a response; an unsuccessful condition still returns its normal error. |
| `{"type":"delay","milliseconds":100}` | Waits before invoking the S3 handler. Maximum 600000 milliseconds (ten minutes); deleting faults releases sleeping requests. |
| `{"type":"error","status":500}` | Returns `InternalError` without invoking the store. Status 403 returns `AccessDenied`; 503 returns `SlowDown`; 409 returns `ConditionalRequestConflict`. Other statuses are rejected. |
| `{"type":"missing_key_access_denied"}` | Requires `GetObject` or `HeadObject`. Changes only a missing key's `NoSuchKey` response to 403 `AccessDenied`; existing keys and missing buckets retain their normal responses. |
| `{"type":"fail_before_commit"}` | Returns `500 InternalError` without invoking the store. |
| `{"type":"corrupt_body"}` | Requires `operation: "GetObject"`. For successful 200 or 206 responses, flips the first returned byte and preserves the original headers, including checksums and ETag. Empty bodies have no byte to flip. |
| `{"type":"truncate_response"}` | Requires `GetObject`, `ListBuckets`, `ListObjectsV2`, `ListObjectVersions`, `ListParts`, or `ListMultipartUploads`. Sends part of a successful response body, then terminates the stream with an error. |

For example, in off mode test an ambiguous conditional write with automatic SDK retries disabled:

```bash
curl -X PUT http://127.0.0.1:9000/_s3mock/faults \
  -H 'content-type: application/json' \
  -d '{"rules":[{"operation":"PutObject","bucket":"example","key_prefix":"pending/","has_conditional_header":true,"action":{"type":"commit_then_drop"}}]}'
curl http://127.0.0.1:9000/_s3mock/requests
curl -X DELETE http://127.0.0.1:9000/_s3mock/faults
```

The first PUT commits but fails at the client. A retry with `If-None-Match: *` receives 412;
reading the object returns the committed content.

Each counter contains `requests`, `bytes_received`, and `bytes_sent`. `by_operation` maps API
names to counters. `by_prefix` maps bucket names to prefix-to-counter maps. Body bytes count
encoded request bytes consumed and response bytes yielded to HTTP, excluding headers. They are
not transport acknowledgments. A response truncated by a fault counts only its yielded prefix.
Requests are recorded when the response body is released; clients should finish reading before
inspecting counters. Error responses count too. Prefix groups are created only for existing buckets and supported operations.
The first 1024 bucket/prefix groups are retained until reset; new groups fold into
`by_prefix["<overflow>"]["<overflow>"]`. Operation counters are unaffected by this cap.

Every S3 request also emits one structured tracing event with `operation`, `bucket`, `key`,
`if_match`, `if_none_match`, `if_modified_since`, `if_unmodified_since`, `status`, `duration_ms`,
`bytes_received`, `bytes_sent`, and
`injected_fault`. The same fields appear in the recent request ring. Status 0 means the server
intentionally dropped a committed response or truncated a response before yielding any body bytes. No authorization header or signing secret is logged.

The integration suite exercises all fault actions through `aws-sdk-s3`, seeded replay, admin
validation and resets, counters and bounded logs, opaque ETag conditions, SDK memory/file
uploads in strict mode, presigned GET and PUT, and signed chunk/trailer tampering through raw HTTP.
