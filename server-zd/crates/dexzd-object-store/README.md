# dexzd-object-store

## Purpose

Object storage interface used by server-zd, with an S3 implementation and a fault-injecting in-memory implementation for tests.

## S3 configuration

`S3ObjectStoreConfig` selects the endpoint, region, bucket, relative key prefix, path-style addressing, credentials, and per-request timeout. Credentials come from the AWS default provider chain or explicitly supplied static keys. Static keys are intended for local S3-compatible endpoints. Static keys may include a session token for temporary credentials. Anonymous credentials send unsigned requests, for a local emulator or a bucket that allows anonymous access. Both the multipart threshold and part size default to 8 MiB; the part size must be at least 5 MiB.

S3 reads retry transient service and transport failures up to three attempts. Writes do not retry inside the SDK. A write that times out or receives a server error may already have committed, so the store reports `OutcomeUnknown`; callers read the key and compare its content before retrying. In particular, retrying a conditional write after its own earlier success can produce `PreconditionFailed`. Multipart uploads apply the condition on completion and abort unfinished uploads after failures.

HTTP 409 `ConditionalRequestConflict` retries twice with bounded backoff. Each multipart retry aborts the previous upload and starts a new one. A persistent write conflict is `Transient`; HTTP 412 and write `If-Match` on a missing key are `PreconditionFailed`. Conditional deletes map HTTP 404 to `Deleted` and HTTP 409/412 to `PreconditionFailed`; callers re-read before retrying.

Cancellation can interrupt a multipart upload before the abort request runs. Configure the bucket's `AbortIncompleteMultipartUpload` lifecycle rule to remove abandoned uploads.

`head` checks bucket existence after a 404 to distinguish a missing key from a missing bucket. This adds one request to the missing-key path.

File downloads write to a temporary file beside the target and rename after the full body arrives. Errors and cancellation remove the temporary file without publishing a partial target.

`startup_self_check` verifies create-only and ETag replacement behavior in the configured bucket, then deletes its probe object. Start the server only after it succeeds.

## Testing

The shared conformance suite exercises the in-memory implementation and an in-process s3mock in strict, process-crash-safe, and relaxed durability modes. It needs no cloud credentials or external service.

## Search-pending objects

The SQL projection stores one compact protobuf object at
`search-pending/v1/partition-<decimal partition>` under the configured store prefix.
Format version 1 contains fields: format version (tag 1), cluster ID (2), owner
node epoch (3), partition ID (4), repeated pending run records (5), and the stable rebuild-reason mask (6).
Legacy unversioned partition keys remain readable and are updated in place. Unknown
keys and path/payload versions are skipped and counted, so future formats do not stop startup.
The run-record layout is documented in [dexzd-keyspace](../dexzd-keyspace/README.md#local-search-pending-sidecar).
Only identities, Flow hashes, priorities, and revisions are stored, not documents.

Changed partition lists sync in batches (defaults: one second, 64 partitions), with at most eight concurrent object operations.
Dirty partitions rotate across batches so hot partitions cannot starve other pending lists.
New owners load objects before projecting and replace them with their owner epoch.
ETag compare-and-swap protects writes. `delete_if_etag_matches` protects empty-list
removal from concurrent replacements; implementations without conditional deletion fail
without deleting. A newer owner epoch fences stale writers. Empty pending lists without rebuild markers remove
their objects. Markers survive owner changes in these objects. Local SQL checkpoints remain held until shed intents reach object storage.

Sync snapshots revisions while holding the local mutex, releases it for network I/O,
and clears dirty state only if the revision still matches. Scans page through 256 entries.
A sync still serializes and rewrites the complete partition object: memory and upload cost
are proportional to its pending list. Multiple drops and completions coalesce into at most
one rewrite per partition per regular sync interval. Takeover and explicit rebuilding may
sync immediately. New owners rewrite loaded objects under their epoch before continuing.
