# dexzd-object-store

## Purpose

Object storage interface used by server-zd, with S3 and db9 implementations and a fault-injecting in-memory implementation for tests.

## S3 configuration

`S3ObjectStoreConfig` selects the endpoint, region, bucket, relative key prefix, path-style addressing, credentials, and per-request timeout. Credentials come from the AWS default provider chain or explicitly supplied static keys. Static keys are intended for local S3-compatible endpoints. Static keys may include a session token for temporary credentials. Anonymous credentials send unsigned requests, for a local emulator or a bucket that allows anonymous access. Both the multipart threshold and part size default to 8 MiB; the part size must be at least 5 MiB.

S3 reads retry transient service and transport failures up to three attempts. Writes do not retry inside the SDK. A write that times out or receives a server error may already have committed, so the store reports `OutcomeUnknown`; callers read the key and compare its content before retrying. In particular, retrying a conditional write after its own earlier success can produce `PreconditionFailed`. Multipart uploads apply the condition on completion and abort unfinished uploads after failures.

HTTP 409 `ConditionalRequestConflict` retries twice with bounded backoff. Each multipart retry aborts the previous upload and starts a new one. A persistent conflict is `Transient`; HTTP 412 and `If-Match` on a missing key are `PreconditionFailed`.

Cancellation can interrupt a multipart upload before the abort request runs. Configure the bucket's `AbortIncompleteMultipartUpload` lifecycle rule to remove abandoned uploads.

`head` checks bucket existence after a 404 to distinguish a missing key from a missing bucket. This adds one request to the missing-key path.

File downloads write to a temporary file beside the target and rename after the full body arrives. Errors and cancellation remove the temporary file without publishing a partial target.

`startup_self_check` verifies create-only and ETag replacement behavior in the configured bucket, then deletes its probe object. Start the server only after it succeeds.

## Testing

The shared conformance suite exercises the in-memory implementation and an in-process s3local in strict, process-crash-safe, and relaxed durability modes. It needs no cloud credentials or external service.

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
