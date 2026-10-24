# server-zd

server-zd is a Dex Server written in Rust whose only runtime dependency is object storage (S3 or
an S3-compatible store). The local disk is only a cache: every acknowledged write is durable in
object storage, and a node rebuilds its local state from snapshots and the command log.

It serves the same `protos/dex.proto` FlowService and pull-mode WorkerTaskService as the Go Server,
so the Dex SDKs work without changes. It differs from the Go Server in a few documented ways (for
example, it supports only pull Workers).

## Layout

| Crate | Role |
| --- | --- |
| `dexzd-protocol` | Generated protobuf and gRPC code for the public protocol and internal formats |
| `dexzd-keyspace` | Identifiers, partition hashing, key encodings, value references |
| `dexzd-clock` | Wall-clock abstraction for real and simulated time |
| `dexzd-metrics` | Fixed, process-local OpenMetrics registry and recording handles |
| `dexzd-object-store` | Object storage interface, S3 implementation, fault-injecting test implementation |
| `dexzd-visibility-query` | Visibility query parser and type checker |
| `dexzd-log` | Command log batches and the conditional-write commit protocol |
| `dexzd-cluster-metadata` | Cluster object, partition manifest, node leases |
| `dexzd-apply` | Deterministic state machine that applies commands |
| `dexzd-stream-store` | In-memory Stream store |
| `dexzd-storage` | Local RocksDB state store and snapshots |
| `dexzd-worker-dispatch` | Pull Worker protocol server |
| `dexzd-search` | SearchFlows backends |
| `dexzd-engine` | Commit pipeline, Flow runtime, effects, recovery |
| `dexzd-api` | FlowService gRPC handlers and public API authentication |
| `dexzd-server` | The `dex-server-zd` executable |
| `dexzd-simulation` | Deterministic simulation tests |
| `dexzd-e2e-tests` | End-to-end tests over the real gRPC protocol |

Crate layers are registered in `script/rust-crate-layers.json`; a crate may depend only on crates
in the same or a lower layer.

## Release binaries

Each published `server-zd/vX.Y.Z` GitHub Release includes native `dex-server-zd` archives for
Linux amd64, Linux arm64, and macOS arm64:

| Platform | Archive |
| --- | --- |
| Linux amd64 | `dex-server-zd_vX.Y.Z_linux_amd64.tar.gz` |
| Linux arm64 | `dex-server-zd_vX.Y.Z_linux_arm64.tar.gz` |
| macOS arm64 | `dex-server-zd_vX.Y.Z_darwin_arm64.tar.gz` |

Each archive contains `dex-server-zd`, `LICENSE.md`, `LEGACY_NOTICES.md`, and `LICENSES/`.
The Release also includes `checksums.txt` with the SHA-256 hash of each archive. Verify the
downloaded archive against that file before extracting it. Run
`sha256sum --ignore-missing -c checksums.txt` on Linux or
`shasum -a 256 --ignore-missing -c checksums.txt` on macOS from the download directory.

Linux archives require glibc 2.28 or newer. They run on Debian 10+, Ubuntu 20.04+, and
RHEL 8+ without a newer system libstdc++.

## Compatibility

Released Go SDK integration suites were run unchanged against the released macOS arm64 binary.
Each run used pull Workers and `dexcli dev -engine zd` from `cli-v1.16.0`. It followed the steps
of main's `sdk-go/run-e2e-tests.sh` with `DEX_INTEG_ENGINE=zd`.

| server-zd | Go SDK | Top-level tests | Subtests |
| --- | --- | --- | --- |
| `server-zd/v0.1.1` | `sdk-go/v1.16.0` | 53 passed, 4 skipped, 0 failed | 14 passed, 5 skipped, 0 failed |
| `server-zd/v0.1.1` | `sdk-go/v1.15.0` | 49 passed, 1 skipped, 7 failed | 14 passed, 0 skipped, 5 failed |

Every `sdk-go/v1.15.0` failure is a difference listed below. Its one skip is a push-only test
that the suite skips with pull Workers. Both versions also passed the two pull Worker tests that
run against a real server.

The `sdk-go/v1.15.0` suite predates server-zd, so it expects Go Server behavior in these areas.
From `sdk-go/v1.16.0` on, the suite skips these tests on server-zd or asserts server-zd behavior:

| Area | server-zd behavior | Affected tests |
| --- | --- | --- |
| Step failure policies | Proceed-on-failure and proceed-to-configured-Step policies are rejected with `InvalidArgument`. A Worker Step decision that sets one fails the attempt. | `TestProceedOnWaitForFailureFlow`, `TestStepRecovery`, and five `TestStepCancellation` scenarios |
| Flow retry policies | StartFlow with a Flow retry policy returns `InvalidArgument`. | `TestBasicFlow` |
| TriggerContinueAsNew | Returns `Unimplemented`. | `TestActionPermissionProjectionContinueAsNew`, `TestSubFlowPartialResultsSurviveContinueAsNewWithoutRestart` |
| ASYNC Step heartbeats | Heartbeat checkpoints are kept from the first attempt. | `TestAsyncLocalHeartbeatIsIgnoredAndStreamsAreRetained` |
| Push Workers | Not supported. | `TestMessageSizeHydrationSplitsBlobBatchesAboveLimit` (push-only) |

The [released Go SDK compatibility workflow](../.github/workflows/server-zd-sdk-go-compatibility.yml)
repeats this check after each server-zd release. It runs the newest `sdk-go` release's suite
against the Linux amd64 binary. A manual run can choose any server-zd tag and any `sdk-go` tag
from `sdk-go/v1.16.0` on.

## Metrics

Set `metrics.listen` to a bind address such as `127.0.0.1:9090` to serve plain HTTP `GET /metrics` on a listener separate from `api.listen`. The default empty string disables it. The endpoint is unauthenticated; never expose it publicly. Other paths return 404. The response uses OpenMetrics text with `application/openmetrics-text; version=1.0.0; charset=utf-8`. Metrics are available while a node is recovering or standing by.

Every label value is from a closed set. No series has a Flow, Flow type, partition, tenant, or key label.

| Name | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `dexzd_build_info` | gauge, always 1 | `version` | Binary version |
| `dexzd_behavior_version` | gauge | — | Cluster's current version barrier used by new commands |
| `dexzd_node_role` | gauge, 1 for current role and 0 otherwise | `role`: starting, standby, recovering, serving, draining | Lifecycle state |
| `dexzd_lease_epoch` | gauge | — | Owner epoch held by this node, 0 when none |
| `dexzd_lease_renewals_total` | counter | `outcome`: ok, error | Lease renewal attempts |
| `dexzd_api_requests_total` | counter | `rpc_class`: flow_write, flow_read, search, worker_poll, worker_respond; `code`: gRPC status code in snake_case | Public API calls by final status; unary calls at completion, Worker streams once at close, unauthenticated calls included, health checks excluded |
| `dexzd_commit_batches_total` | counter | — | Durably committed batches |
| `dexzd_commit_records_total` | counter | — | Committed commands |
| `dexzd_commit_bytes_total` | counter | — | Committed batch bytes |
| `dexzd_commit_latency_seconds` | histogram, buckets 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5 | — | Batch seal to durable acknowledgement |
| `dexzd_object_store_requests_total` | counter | `operation`: put, get, list, delete; `object_kind`: log, snapshot, manifest, lease, other | Object storage requests; multipart upload counts once as put |
| `dexzd_object_store_request_errors_total` | counter | `operation`: put, get, list, delete | Failed requests after SDK retries |
| `dexzd_object_store_put_bytes_total` | counter | — | Uploaded bytes |
| `dexzd_snapshots_total` | counter | `outcome`: ok, error | Snapshot attempts |
| `dexzd_snapshot_last_duration_seconds` | gauge | — | Duration of the last successful snapshot |
| `dexzd_snapshot_last_bytes` | gauge | — | Size of the last successful snapshot |
| `dexzd_recovery_last_duration_seconds` | gauge | — | Duration of the last completed recovery or takeover replay |
| `dexzd_recovery_last_replayed_bytes` | gauge | — | Uncompressed command record bytes replayed by the last recovery |
| `dexzd_local_storage_bytes` | gauge | — | Local SST and WAL bytes, sampled at scrape time |
| `dexzd_timer_lag_seconds` | gauge | — | Age of the oldest due timer not yet fired, 0 when none |
| `dexzd_matching_queued_tasks` | gauge | — | Worker tasks waiting for a poll across all Flow types |
| `dexzd_matching_worker_streams` | gauge | — | Open Worker poll streams |
| `dexzd_api_request_latency_seconds` | histogram, buckets 0.005, 0.025, 0.1, 0.5, 2.5 | `rpc_class`: flow_write, flow_read, search, worker_respond | Completed unary call latency; Worker poll streams are excluded |
| `dexzd_step_attempt_failures_total` | counter | `durability`: sync, async; `failure_kind`: worker_error, method_timeout, heartbeat_timeout, dispatch_error, invalid_response, unsupported_feature, value_too_large, request_too_large | Failed Step method attempts |
| `dexzd_step_retries_exhausted_total` | counter | `durability`: sync, async | Step executions that failed after their last allowed attempt |
| `dexzd_flow_closes_total` | counter | `close_status`: completed, failed, terminated, canceled | Closed Flow runs |
| `dexzd_flow_end_to_end_latency_seconds` | histogram, buckets 1, 10, 60, 600, 3600, 21600, 86400 | — | Run start to close using committed timestamps |
| `dexzd_step_schedule_to_start_latency_seconds` | histogram, buckets 0.005, 0.025, 0.1, 0.5, 2.5, 10 | — | Committed SYNC Step task becoming dispatchable to Worker delivery; ASYNC speculative dispatches have no committed schedule time |
| `dexzd_worker_stream_events_total` | counter | `event`: delivered, closed | Task deliveries and persistent Worker stream closes |
| `dexzd_attribute_sync_backlog_entries` | gauge | — | Attribute Sync entries waiting to be written to an Attribute Store, as of the projector's last scan |
| `dexzd_attribute_sync_oldest_pending_age_seconds` | gauge | — | Age of the oldest pending Attribute Sync entry's retry window, 0 when none |
| `dexzd_attribute_sync_write_failures_total` | counter | `failure_kind`: retryable, non_retryable | Failed Attribute Store write transactions; non-retryable failures drop their entries |
| `dexzd_attribute_sync_dropped_entries_total` | counter | — | Attribute Sync entries dropped after `syncRetryPolicy` was exhausted or the store rejected them |
| `dexzd_attribute_sync_fenced_writes_total` | counter | — | Attribute Store writes rejected because a newer owner raised the fencing token |

Worker polling uses a persistent stream, so there is no empty poll outcome. The stream event metric counts each task delivery and each stream close.
Worker results return on that same stream. There is no separate Worker response RPC, so `worker_respond` remains a zero latency series.
Long-poll waits are included in `flow_read` latency, which can exceed the highest finite bucket.

On Linux, the scrape also includes `process_resident_memory_bytes`, `process_cpu_seconds_total`, `process_open_fds`, and `process_start_time_seconds`. These process metrics are absent on other platforms.

## Running

`dex-server-zd` has two commands: `serve` for a Server node on external object storage and `dev`
for local development.

### serve

```bash
dex-server-zd serve --config /etc/dex/dex-server-zd.yaml
```

`serve` reads every setting from one YAML file. `config/dex-server-zd.example.yaml` lists every key
with its default. Set at least `cluster.nodeId`, `objectStore.bucket`, and
`localStorage.directory`. Unknown keys and a `dev` section with non-default values fail startup.
An invalid value fails startup with the YAML path of the rejected field.

`localStorage.directory` needs room for the live state plus `localStorage.maxWalBytes` of RocksDB
write-ahead log and up to four uncompacted 64 MiB level-0 files per column family. A compaction
after a bulk replay returns the directory to about the live state size.

Object storage uses `objectStore.endpoint` (empty for AWS endpoint resolution), `region`, `bucket`,
`prefix`, and `forcePathStyle`. `objectStore.hardDeleteStore` optionally sends hard-delete values
to a separate destination with the same keys; without it they use the primary bucket and prefix.
`credentialsSource` selects the credentials of each destination:

| Value | Credentials |
| --- | --- |
| `defaultChain` (default) | The AWS SDK default credential provider chain |
| `environment` | `DEX_ZD_OBJECT_STORE_ACCESS_KEY_ID`, `DEX_ZD_OBJECT_STORE_SECRET_ACCESS_KEY`, and the optional `DEX_ZD_OBJECT_STORE_SESSION_TOKEN`; the hard-delete store reads the `DEX_ZD_HARD_DELETE_STORE_*` variables with the same suffixes |
| `anonymous` | Unsigned requests, for a local emulator or a bucket that allows anonymous access |

For a shared AWS S3 bucket, set a nonempty `objectStore.prefix` and grant the Server role only
these S3 actions. Replace `BUCKET` and `PREFIX/` with the bucket and configured prefix; include the
trailing slash in the policy. Apply the same policy to `objectStore.hardDeleteStore` when configured.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "s3:ListBucket",
      "Resource": "arn:aws:s3:::BUCKET",
      "Condition": { "StringLike": { "s3:prefix": "PREFIX/*" } }
    },
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload"],
      "Resource": "arn:aws:s3:::BUCKET/PREFIX/*"
    }
  ]
}
```

The Server lists only under its configured prefix, including when checking a missing object.
The `s3:ListBucket` condition must use `StringLike` with `PREFIX/*`. An exact-prefix
`StringEquals` grant or no `s3:ListBucket` grant is unsupported: a missing object may return
403, and the Server cannot use a prefixed listing to distinguish it from a denied object.
With the `StringLike` grant above, AWS S3 returns 404 for missing-object reads; narrower grants
can return 403. The Server handles either response when the prefixed listing is allowed.
The startup conditional-write probe also creates and deletes a key under that prefix. No
unconditioned bucket access or bucket administration action is required. The bucket may be
unversioned: create-only and replacement writes use `If-None-Match` and `If-Match` with opaque
ETags, not object versions or MD5 digests. The S3 client sends no server-side encryption headers,
so the bucket's default SSE-KMS configuration applies. For a customer-managed KMS key, grant the
role `kms:GenerateDataKey` and `kms:Decrypt` on that key, and allow those actions in its key policy.
Configure a bucket lifecycle rule to `AbortIncompleteMultipartUpload` after 1–7 days. A crash can
leave an incomplete upload, and a prefix-confined role cannot list or clean up all abandoned
multipart uploads.

Startup proceeds in this order:

1. The node binds `api.listen`. gRPC health Check and Watch report NOT_SERVING for `""`,
   `dex.FlowService`, and `dex.WorkerTaskService`; FlowService and WorkerTaskService calls return
   Unavailable while startup continues.
2. Every S3 destination verifies a prefixed child listing. Every destination then passes a
   conditional-write self-check.
3. The node acquires the lease of `cluster.nodeId` for `lease.duration`. A lease that another
   process still holds is awaited until it expires, or fails startup when `lease.failIfHeld` is
   true.
4. The node restores `localStorage.directory` from the latest snapshot and the command log. The
   directory is a cache; an empty one is rebuilt from object storage. Replay keeps up to
   `recovery.readAheadBatches` log reads in flight, holding at most `recovery.readAheadBytes` of
   log objects or one larger batch, and applies batches in log order as they arrive. After a
   replay of at least `localStorage.compactAfterReplayBytes`, the local store is compacted in the
   background while the node serves.
5. After recovery and rebuild finish, health switches to SERVING and the API serves requests.
   Startup failure closes the listener.

### Attribute Store synchronization

The `attributeStore` section uses the Go Server's keys and table contract. Attributes written with
SyncToAttributeStore by a Flow whose FlowConfig lists a configured store name are written to that
store's PostgreSQL tables. These differences from the Go Server apply:

- Only `type: postgres` is supported. Other types fail startup with a configuration error; keep
  those stores on the Go Server.
- Synchronization is asynchronous. Step, RPC, and SetAttributes commits do not wait for the table,
  and there is no 7-second synchronous phase. Rows normally appear within a second of the commit.
  While a store is unavailable, Flows keep running and the backlog is written with the latest
  values once it recovers. Entries still pending `syncRetryPolicy.totalDuration` after they were
  first marked are dropped and counted in `dexzd_attribute_sync_dropped_entries_total`.
- Failed writes are retried with the `syncRetryPolicy` backoff until `totalDuration`, like the Go
  Server. A connection failure, timeout, or other failure of the whole store backs off every run of
  that store. A failure caused by the written values, such as a constraint violation or a value
  too long for its column, backs off only that run, and other Flows keep syncing. A newer run of
  the same Flow ID waits for the older run so rows keep run order.
- After time travel, every row the run synced is rewritten with the state after the time travel.
- Each database needs a Dex fencing table, named by `fencingTableName`
  (default `dexzd_attribute_sync_fencing`). One table serves every store and cluster in the
  database. Create it as the table owner, then grant `SELECT`, `INSERT`, and `UPDATE` on it to the
  Dex role. dex-server-zd never creates or alters tables, and startup fails with this statement when
  the table is missing:

  ```sql
  CREATE TABLE dexzd_attribute_sync_fencing (store_name text NOT NULL, cluster_id text NOT NULL,
    scope text NOT NULL, fencing_token bigint NOT NULL, updated_at timestamptz NOT NULL,
    PRIMARY KEY (store_name, cluster_id, scope));
  ```

  Rows of permanently stopped clusters may be deleted; never delete the rows of a running cluster.

Startup connects to every store and checks its tables; any failure fails startup. A store's `dsn`
may be left empty and supplied in `DEX_ZD_ATTRIBUTE_STORE_DSN_<NAME>`, where `NAME` is the store
name in upper case with other characters replaced by `_`. A write transaction lasts at most
`syncAttemptTimeout` or one eighth of `lease.duration`, whichever is smaller, and starts only while
the lease has room for it, so writes always end while the node owns its partitions.

### Snapshot uploads after ownership changes

The first snapshot after every takeover or restart uploads every file in the local checkpoint to
a fresh lineage. It cannot reuse objects from the previous lineage, even when the local RocksDB
directory is reused. Expect uploads approximately equal to the full checkpoint size, plus manifest
data. Transfer time is at least checkpoint bytes divided by effective upload throughput, plus
checkpoint and per-file request time. For example, 10 GiB at 100 MiB/s takes at least about 102
seconds to transfer. Later snapshots from the same owner can reuse unchanged files.

`search.backend: builtin` serves SearchFlows from committed local search documents.

`search.backend: paradedb` serves SearchFlows from ParadeDB:

- Before the log is replayed, the node opens `search.paradeDbConnectionPoolSize` connections to
  `search.paradeDbConnectionString` and creates or upgrades the search schema. It tries three
  times, then fails startup. The first startup needs a role that may create the `pg_search`
  extension, unless the extension is already installed.
- A `search.paradeDbSchema` other than `public` is applied as the `search_path` connection option.
  A transaction pooler must pass that startup option through; otherwise keep the tables in `public`.
- A background task copies each committed partition frame's search documents to ParadeDB. Commits
  never wait for it. When ParadeDB is unavailable, the task retries. When it falls behind its queue
  or its stored position diverges, it rebuilds the partition from committed local state.
- On startup, every partition whose ParadeDB position differs from the local store is rebuilt.
- SearchFlows waits up to `search.readYourWritesWait` until frames committed before the request are
  searchable, then returns Unavailable.
- One ParadeDB schema belongs to one Dex cluster.

On SIGTERM or SIGINT the node stops within 14.5 seconds. Health reports NOT_SERVING, Workers are
told to reconnect, long polls close, and open calls get 4 seconds to finish. Then effects and the
engine stop, and the node releases its lease. The process exits 0, and a replacement process with
the same `cluster.nodeId` acquires the lease at once. When object storage rejects the release, or
the release does not finish within 2 seconds, the process exits 1 and the lease stays held until it
expires.

A lost lease, a stopped commit pipeline, or a halted Flow runtime stops the node, and the process
exits 1. Restart it. A signal before the node acquires its lease cancels startup and exits 0. If
the node already holds its lease, startup finishes before graceful shutdown begins. That shutdown
has the same 14.5-second total and exit status as a signal received while serving. A failed startup
releases its lease and exits 1.

Logs go to standard error. `RUST_LOG` filters them; the default level is `info`.

### Authentication

`auth.authenticators` turns on public API authentication. It is empty by default, which leaves
the API open. The section uses the Go Server's `auth` keys, so an existing Go Server section
loads unchanged:

```yaml
auth:
  authenticators:          # tried in order
    - kind: jwt
      issuer: https://issuer.example.com     # exact iss claim
      audiences: ["dex"]                     # aud must name at least one
      jwksURL: ""                            # empty uses OIDC discovery from the issuer
      allowInsecureJWKSURL: false            # server-zd only
      caFile: ""                             # PEM bundle added to the system roots
    - kind: staticTokens
      file: /etc/dex-server-zd/tokens.yaml
```

As in the Go Server, a Go Server key that belongs to the other kind is ignored, such as `file` on a
`jwt` entry. `allowInsecureJWKSURL` applies only to `jwt`. An unknown key fails startup.

Every FlowService and WorkerTaskService call needs an `authorization: Bearer <credential>`
header. gRPC health needs no credential. A call to a service that server-zd does not serve answers
UNIMPLEMENTED, as in the Go Server. A rejected call ends with UNAUTHENTICATED. Its
`ServiceErrorResponse` detail has sub-status `UNCATEGORIZED` and one of two details: "a bearer
credential is required" or "the credential is not valid". These match the Go Server. Credentials
are never logged; rejection reasons are logged at debug level.

Authenticators are tried in order:

- A `staticTokens` entry accepts a credential whose SHA-256 hash its file lists.
- The first `jwt` entry whose `issuer` equals the token's unverified `iss` claim decides alone. If
  its checks fail, later entries are not tried.

A JWT is accepted when all of these hold:

- Its `alg` is RS256, RS384, RS512, PS256, PS384, PS512, ES256, ES384, ES512, or EdDSA (Ed25519).
- Its `kid` names exactly one public signing key. If the key has an `alg`, it must match the
  token's.
- The signature verifies. RSA keys need 2048 to 8192 bits.
- `iss` matches `issuer` exactly, and `aud` names an entry of `audiences`.
- `exp` is present, and `exp`, `nbf`, and `iat` hold with a 30-second clock skew leeway.

The issuer must be an HTTPS URL, or plain HTTP on `localhost` or a loopback IP. Keys come from
`jwksURL`, or from the `jwks_uri` of `<issuer>/.well-known/openid-configuration`. A discovered
document must name the same issuer, and its `jwks_uri` follows the same HTTPS rule. `jwksURL`
follows it too, unless `allowInsecureJWKSURL` is true. That flag permits a plain `http://` key
server on a trusted network, such as one inside the cluster. It needs `jwksURL` and relaxes
nothing else. HTTPS requests trust the system roots plus `caFile`, so a container image needs CA
certificates. Redirects are not followed, and proxy environment variables are ignored.

The key set follows the Go Server's limits:

- Startup fetches nothing, so an unreachable issuer does not fail startup. The first JWT fetches
  the keys.
- Keys refresh at most once a minute. Every request that arrives while that refresh is due or
  running waits for it. A removed or replaced key therefore stops verifying with that refresh.
- A token with an unknown `kid` triggers a refresh at most every ten seconds. Only requests for
  unknown keys wait for that refresh.
- A request waits at most five seconds, which also bounds each refresh. Then the current keys
  decide.
- A failed refresh keeps the previous keys.
- Errors and logs show key set and discovery URLs without their userinfo, query, or fragment.
  The same applies to the issuer that a discovery document names.

A static token file lists hashes, never plaintext tokens:

```yaml
tokens:
  - sha256: 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08
```

Compute a hash with `printf %s "$TOKEN" | sha256sum`. The file must exist and be valid at
startup, or startup fails. Changes take effect within a second. A malformed update keeps the last
valid content, while an empty or missing file revokes every token.

A credential is checked once, when a call starts. This matches the Go Server. A pull Worker stream
that opened with a valid credential stays open after its JWT expires or its static token is
revoked. The Worker needs a currently valid credential when it reconnects, for example after the
server asks it to.

`api.listen` serves plaintext gRPC, so bearer credentials cross the network in clear text.
Terminate TLS in front of the server, or restrict the network to trusted callers.

### Container image

The `dex-server-zd` image is published for `linux/amd64` and `linux/arm64`. Its entrypoint is
`/usr/local/bin/dex-server-zd`. By default it runs `serve --config /etc/dex-server-zd/config.yaml`;
mount a production config at that path. The image includes
`/etc/dex-server-zd/dex-server-zd.example.yaml` as a reference, not as its active config.
Mount a writable directory for `localStorage.directory` and set that directory in the config.
The process runs as UID 10001 and GID 10001. Make the data volume writable by that identity,
for example by setting Kubernetes `fsGroup: 10001` or changing the directory ownership to
`10001:10001`.

Port 8801 serves the API and `grpc.health.v1` on `api.listen`. Kubernetes gRPC probes can call
that service directly. Health is NOT_SERVING during startup and SERVING after recovery completes.
If the object store startup check fails, the container exits with an error. Docker and Kubernetes
send SIGTERM directly to the process; it stops accepting work, releases its lease, and normally
exits 0 within 14.5 seconds.

Release tags `server-zd/vX.Y.Z` publish `dex-server-zd:X.Y.Z` and `dex-server-zd:latest`.
The manually dispatched main-image workflow builds its selected ref and publishes
`dex-server-zd:main` and `dex-server-zd:main-<short-sha>` without changing `latest`.

### dev

```bash
dex-server-zd dev --data-dir ./zd-data [--listen 127.0.0.1:8801] [--log-dir <dir>] [--config <overrides.yaml>] [--attribute-store-config <dex.yaml>]
```

`dev` runs one node with an embedded s3mock whose data lives under `<data-dir>/s3`, and keeps local
state under `<data-dir>/store`. `dexcli dev -engine zd` starts it with these flags. It fixes the
node ID to `dev`, anonymous credentials, the embedded endpoint, and the `--listen` address, and it
rejects `objectStore.hardDeleteStore`. With `--log-dir`, logs also go to
`<log-dir>/dex-server-zd.log`. A `search.backend: paradedb` override uses ParadeDB as `serve` does.
`--attribute-store-config` reads only the `attributeStore` section of a Go Server configuration
file, which must define at least one store; the `--config` file must not also set `attributeStore`.

## Stable command-line interface

These commands and flags are part of the Beta compatibility interface. Applications can invoke
them without depending on the implementation of the server:

| Command | Flag | Meaning |
| --- | --- | --- |
| `serve` | `--config <file>` | Required YAML configuration file for a node using external object storage. |
| `dev` | `--data-dir <dir>` | Required directory for embedded object storage and local state. |
| `dev` | `--listen <address>` | Plaintext gRPC listen address as `IP:port`; host names are rejected; defaults to `127.0.0.1:8801`. |
| `dev` | `--config <file>` | Optional YAML configuration overrides. |
| `dev` | `--log-dir <dir>` | Optional directory that also receives `dex-server-zd.log`. |
| `dev` | `--attribute-store-config <file>` | Optional Go Server YAML file whose `attributeStore` section configures Attribute synchronization. |
| Both | `--version` | Prints `dex-server-zd X.Y.Z`. |

SIGTERM stops `dev` and exits with status 0.

## Development

```bash
cd server-zd
cargo test -p <crate>        # iterate on one crate
make fmt-check clippy test   # what CI runs
make build-server            # builds target/release/dex-server-zd
```

Local tests use only the in-process s3mock (`../s3mock`) or the in-memory object store. They need
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
