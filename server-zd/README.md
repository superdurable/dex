# server-zd

server-zd is a Dex Server written in Rust whose only runtime dependency is object storage (S3 or
an S3-compatible store). The local disk is only a cache: every acknowledged write is durable in
object storage, and a node rebuilds its local state from snapshots and the command log.

It serves the same `protos/dex.proto` FlowService and pull-mode WorkerTaskService as the Go Server,
so the Dex SDKs work without changes. It differs from the Go Server in a few documented ways (for
example, it supports only pull Workers).

StopFlow closes the run as soon as it commits. A later write to that run, such as SetAttributes,
returns NOT_FOUND. Since v0.1.5, SetAttributes has one exception on a closed run that is not yet
purged: it is accepted when the request has at least one write and every write sets a currently
stored hard-delete Attribute to null, and the request omits the Action permission mappings field.
The erase deletes the stored values and leaves the run closed. Value writes, ordinary erases, and
mixed requests still return NOT_FOUND. The Go Server accepts writes while a canceled or failed run
winds down, and rejects every write after the run closes. Write what a Flow needs before stopping
it. StopFlow and Terminate never wait for Attribute Store synchronization.

From v0.1.8, a transactional or locking InvokeRPC on a closed run returns NOT_FOUND and
FLOW_NOT_EXISTS before calling a Worker. A retry with a previously committed request ID returns its
recorded reply. Other RPCs still run their handlers on a closed, unpurged run, so they can read it,
and any write they return is rejected. The Go Server behaves the same way.

See [RPC request IDs](crates/dexzd-engine/README.md#rpc-request-ids) for concurrent calls,
Worker timeouts, and retry limits.

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
| `dexzd-postgres-connection` | PostgreSQL connection string parsing and TLS connections for Attribute Store and search |
| `dexzd-search` | SearchFlows backends |
| `dexzd-engine` | Commit pipeline, Flow coordination, effects, recovery |
| `dexzd-api` | FlowService gRPC handlers and public API authentication |
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

## Compatibility

Released Go SDK integration suites were run unchanged against the released macOS arm64 binary.
Each run used pull Workers and `dexcli dev -engine zd` from `cli-v1.16.0`. It followed the steps
of main's `sdk-go/run-e2e-tests.sh` with `DEX_INTEG_ENGINE=zd`.

| server-zd | Go SDK | Top-level tests | Subtests |
| --- | --- | --- | --- |
| `server-zd/v0.1.1` | `sdk-go/v1.16.0` | 53 passed, 4 skipped, 0 failed | 14 passed, 5 skipped, 0 failed |
| `server-zd/v0.1.1` | `sdk-go/v1.15.0` | 49 passed, 1 skipped, 7 failed | 14 passed, 0 skipped, 5 failed |

Each SDK CI workflow (for example [Go SDK CI](../.github/workflows/sdk-go-ci.yml)) runs its Go,
Rust, Python, TypeScript, or Java SDK integration suite with pull Workers against server-zd on
every relevant pull request and main push, including server-zd changes.

Every `sdk-go/v1.15.0` failure is a difference listed below. Its one skip is a push-only test
that the suite skips with pull Workers. Both versions also passed the two pull Worker tests that
run against a real server.

The `sdk-go/v1.15.0` suite predates server-zd, so it expects Go Server behavior in these areas.
The `sdk-go/v1.16.0` suite skips unsupported cases or asserts server-zd behavior. The released
`sdk-go/v1.17.0`, `sdk-go/v1.20.0`, and `sdk-go/v1.21.0` suites run the cancellation cases with
Go Server expectations and fail against server-zd versions that support Step failure policies. The
server-zd cancellation expectations below apply from the next Go SDK release containing this change.

| Area | server-zd behavior | Affected tests |
| --- | --- | --- |
| Step failure policies | Through `server-zd/v0.1.1`, proceed-on-failure and proceed-to-configured-Step policies are rejected with `InvalidArgument`. A Worker Step decision that sets one fails the attempt. Later versions run them like the Go Server. See ASYNC Step cancellation and Canceled Step handlers below for related engine differences. | `TestProceedOnWaitForFailureFlow`, `TestStepRecovery`, and five `TestStepCancellation` scenarios |
| Flow retry policies | StartFlow with a Flow retry policy returns `InvalidArgument`. | `TestBasicFlow` |
| TriggerContinueAsNew | Returns `Unimplemented`. | `TestActionPermissionProjectionContinueAsNew`, `TestSubFlowPartialResultsSurviveContinueAsNewWithoutRestart` |
| ASYNC Step heartbeats | Heartbeat checkpoints are kept from the first attempt. | `TestAsyncLocalHeartbeatIsIgnoredAndStreamsAreRetained` |
| ASYNC Step cancellation | An ASYNC Step has no local phase. Its handler runs once on server-zd; the Go Server runs a local invocation and then a remote one. | `TestStepCancellation` (`local-execute`, `local-timeout-fallback`) |
| Canceled Step handlers | server-zd sends `cancel_task`, so a pull Worker cancels a running handler context even if the handler records no heartbeat. On the Go Server, a running handler observes cancellation when it records a heartbeat or writes a Stream frame. This also applies to the ASYNC `local-execute` case on server-zd. | `TestStepCancellation` (`no-heartbeat`, `local-execute`) |
| Push Workers | Not supported. | `TestMessageSizeHydrationSplitsBlobBatchesAboveLimit` (push-only) |
| Step timeout failure text | A Step that exhausts its retries after a timeout fails with `method timeout of <seconds>s expired`, `no heartbeat within <seconds>s`, or `retry policy total duration expired`. The Go Server reports a Temporal timeout string. | None |
| Value size limit | A Worker result with a value larger than `max_value_bytes` fails the attempt without retry and uses `WORKER_API_FAIL`. Its text is `a value of <n> bytes exceeds the <max> byte limit` or `value of <n> bytes exceeds the maximum of <max> bytes`. A Step attempt that exceeds `max_run_state_bytes` likewise fails without retry and closes the Flow with `WORKER_API_FAIL`; its text is `run state of <n> bytes exceeds the maximum of <max> bytes`. The Go Server has no per-run value size limit, so no matching error type exists. | None |
| Worker request size | A Step attempt whose request exceeds the Worker message limit fails with `INTERNAL` and `Worker request of <n> bytes exceeds the <max> byte message limit`. The Go Server also uses `INTERNAL` for the pull dispatch send failure, though its error text differs. | None |
| Value references | A Worker result that echoes a value reference naming no value of the run fails the attempt without retry, with `INTERNAL` and `value reference "<reference>" does not name a value of this run`. The Go Server has no value references, so no matching failure exists. | None |
| Rejected apply result | A Worker result for the wrong running method fails with `INTERNAL`. The Go Server does not have an equivalent apply-time method check; its pull dispatch rejects a wrong final frame earlier. | None |
| Unproduced failure kinds | `UNSUPPORTED_FEATURE` and `UNSPECIFIED` have no engine producer. A committed command with either kind keeps `WORKER_API_FAIL`; the Go Server has no matching failure to classify. | None |
| Server-side dispatch failures | `matching server is closing`, `server error: <error>`, and `dispatch ended without an outcome` are server-zd texts with `INTERNAL`. The Go Server reports its own internal error text, which depends on where the failure happens. | None |
| SubFlow options in WaitFor results | server-zd checks a SubFlow condition's start options and effective Flow configuration when it validates the WaitFor result. The attempt fails with that check's text, such as `server-zd does not support flow_start_delay_seconds > 0`. The Go Server checks the Flow configuration when it starts the SubFlow. | None |
| Multiple invalid Attribute writes in one Worker Step result | server-zd validates each write completely before checking the next. The Go Server scans all writes for internal blob IDs before checking hard-delete values. When an earlier write has an invalid hard-delete value and a later write has an internal blob ID, server-zd reports `hard-delete attribute "<key>" must be a string, an object, or null`; the Go Server reports `worker response must not contain internal_blob_id arms`. | None |

Apart from the listed differences, an exhausted Step failure closes the Flow with the Go Server's
failure message and error type. server-zd gives a recovery Step the same text as its error detail.

The [released Go SDK compatibility workflow](../.github/workflows/server-zd-sdk-go-compatibility.yml)
repeats this check after each server-zd release. It runs the newest `sdk-go` release's suite
against the Linux amd64 binary. A manual run can choose any server-zd tag and any `sdk-go` tag
from `sdk-go/v1.16.0` on. The newest-release check remains red until a Go SDK release includes
the cancellation expectations above.

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
| `dexzd_startup_read_retries_total` | counter | — | Transient object storage reads retried during startup |
| `dexzd_recovery_last_replayed_bytes` | gauge | — | Uncompressed command record bytes replayed by the last recovery |
| `dexzd_local_storage_bytes` | gauge | — | Local SST and WAL bytes, sampled at scrape time |
| `dexzd_timer_lag_seconds` | gauge | — | Age of the oldest due timer not yet fired, 0 when none |
| `dexzd_matching_queued_tasks` | gauge | — | Worker tasks waiting for a poll across all Flow types |
| `dexzd_matching_worker_streams` | gauge | — | Open Worker poll streams |
| `dexzd_api_request_latency_seconds` | histogram, buckets 0.005, 0.025, 0.1, 0.5, 2.5 | `rpc_class`: flow_write, flow_read, search, worker_respond | Completed unary call latency; Worker poll streams are excluded |
| `dexzd_step_attempt_failures_total` | counter | `durability`: sync, async; `failure_kind`: worker_error, method_timeout, heartbeat_timeout, dispatch_error, invalid_response, unsupported_feature, value_too_large, request_too_large, retry_deadline | Failed Step method attempts |
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
| `dexzd_search_projection_coalesced_changes_total` | counter | — | Queued changes superseded by newer documents or deletes |
| `dexzd_search_projection_dropped_updates_total` | counter | — | Intermediate Running updates dropped above a soft queue limit |
| `dexzd_search_projection_rebuild_requests_total` | counter | — | Newly pending partition rebuild requests after admission failure or unreadable committed documents |
| `dexzd_search_skipped_documents_total` | counter | — | Committed documents omitted because their indexed values are unsupported |

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
write-ahead log and up to four uncompacted level-0 files per column family, each about
`localStorage.writeBufferBytes` (64 MiB by default). A compaction
after a bulk replay returns the directory to about the live state size.

When the local store is corrupt, is missing files, has an incompatible format, or cannot be
updated from the retained log, the node renames the directory to the sibling `<directory>.discarded-<uuidv7>` and
rebuilds it. Other open errors, such as a RocksDB lock that another process holds or a permission
error, fail startup and leave the directory in place. If the store itself cannot be read, stop the
node, move or delete the directory, and restart to rebuild it from object storage. After every successful start, the node
deletes all but the newest `localStorage.retainedSetAsideStoreCount` set-aside stores (default 1;
0 keeps none) and logs each deleted path; the copy this start set aside always counts as the
newest, and a failed deletion only logs a warning. After a successful start, the parent volume
holds at most that many old copies. A start that sets the store aside holds one more until it
succeeds, and starts that fail after setting it aside each add a copy until one succeeds.

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
The startup conditional-write probe also creates and deletes a key under that prefix. A signal
during the probe may leave that key behind. No unconditioned bucket access or bucket
administration action is required. The bucket may be unversioned: create-only and replacement
writes use `If-None-Match` and `If-Match` with opaque
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
   conditional-write self-check. The self-check does not use startup read retries.
3. The node acquires the lease of `cluster.nodeId` for `lease.duration`. A lease that another
   process still holds is awaited until it expires, or fails startup when `lease.failIfHeld` is
   true. `dev` takes a held lease over at once instead; see [dev](#dev). Transient or
   unknown-outcome reads of the cluster, manifest, and lease objects are retried during bootstrap
   and acquisition.
4. The node restores `localStorage.directory` from the latest snapshot and the command log. The
   directory is a cache; an empty one is rebuilt from object storage. Replay keeps up to
   `recovery.readAheadBatches` log reads in flight, holding at most `recovery.readAheadBytes` of
   log objects or one larger batch, and applies batches in log order as they arrive. After a
   replay of at least `localStorage.compactAfterReplayBytes`, the local store is compacted in the
   background while the node serves.
   Transient or unknown-outcome object-storage reads are retried with jittered exponential backoff.
   Each read gets its own `recovery.startupReadRetryBudget` wall-clock window (default 30 seconds),
   starting at its first transient or unknown-outcome failure. Backoff and later read calls count
   toward that window. Concurrent reads do not share a budget, and a later read gets a new window.
   Permanent errors fail startup immediately. Lease loss or shutdown can stop retries sooner;
   lease renewal continues during recovery. The window bounds each read, not the whole startup, so
   startup after lease acquisition has no overall time limit. That is acceptable because a
   persistent outage still fails startup within one read's window, and renewal keeps the lease
   while slow reads make progress.
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
  synced values once it recovers. A later unsynced write does not replace a pending synced value.
  Entries still pending `syncRetryPolicy.totalDuration` after they were
  first marked are dropped and counted in `dexzd_attribute_sync_dropped_entries_total`.
- server-zd v0.1.3 closes a Flow without waiting for its synced Attributes, so rows can land
  after it completes. server-zd v0.1.4 or later waits, like the Go Server. A natural close (a
  Step decision that completes or fails the Flow, exhausted Step retries, or the Flow timeout)
  cancels the Steps and disarms the Flow timeout at once. The run then stays RUNNING with a
  pending close until no outbox entry, for any store, remains. GetFlowSummary and SearchFlows
  show RUNNING meanwhile, and WaitForFlow returns only after the rows are written or dropped.
- A natural close in v0.1.4 or later also waits, with or without an Attribute Store, for every
  transactional or locking RPC that was admitted before it, as the Go Server waits for its
  in-flight updates. The owner stamps such a closing command, apply keeps the close pending, and
  the owner records it with a PendingCloseCheck command once those RPCs commit, fail, or time
  out. A new owner after a restart or takeover has no admitted RPC and records the close during
  recovery. Waits and non-transactional RPCs do not hold a close.
- During that cleanup, SetAttributes, PublishToChannel, and non-transactional RPC results are
  accepted, and their synced writes extend the wait. They start no Step. Transactional or locking
  RPCs, WaitForStepCompletion, WaitForAttribute, and DeleteChannelMessage calls made during
  cleanup fail with FAILED_PRECONDITION and FLOW_CLOSING, as the Go Server rejects them. A wait
  admitted earlier still returns a match it sees, and a transactional RPC
  admitted earlier still commits; its synced writes join the outbox before the close.
- `syncRetryPolicy.totalDuration` bounds each pending entry's retry window, not the whole close.
  During a PostgreSQL outage a natural close waits until its entries are dropped, up to that long
  per entry, and each write accepted during cleanup adds an entry with its own window, which
  extends the wait.
- StopFlow (CANCEL or FAIL), Terminate, and ID reuse termination close the run at once, even
  during cleanup; the stop discards a pending close. The run's remaining entries keep syncing
  after the close. ResetFlow replaces a pending close with the state of the reset point. When a
  reset restores an unsynced Attribute over a synced one, its store row is deleted.
- Entries of a store that is no longer configured, including when the `attributeStore` section
  is gone, are dropped at once with an error log and counted in
  `dexzd_attribute_sync_dropped_entries_total`, so their runs' closes can finish.
- Failed writes are retried with the `syncRetryPolicy` backoff until `totalDuration`, like the Go
  Server. A connection failure or other failure of the whole store backs off every run of that
  store. A failure caused by the written values, such as a constraint violation or a value too
  long for its column, backs off only that run, and other Flows keep syncing. So does a transaction
  that times out, deadlocks, or waits too long for a row lock. After a timeout, that run's retries
  use half as many statements per transaction, down to one. One statement writes all of the run's
  plain Attributes and stays in one transaction, so the main row is never partly updated. Each
  AttributeMap instance is one statement, so a timed-out run's instances can land in different
  transactions. Each transaction that commits is acknowledged, so a retry writes only the rest. When three runs of a store time out with no
  commit in between, the whole store backs off. The retry after a store backoff starts with other
  runs than the one that failed. A newer run of the same Flow ID waits for the older run so rows
  keep run order.
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
`syncAttemptTimeout` or a quarter of `lease.duration`, whichever is smaller. A quarter of
`lease.duration` is also the lease margin. A write starts only while more than the transaction
timeout plus the margin of lease validity remains. Its client deadline therefore passes before the
margin begins. A COMMIT sent just before the deadline finishes within the server-side
`statement_timeout`, which equals the transaction timeout, so it still lands before the lease ends.
The timeout plus the margin is at most half of `lease.duration`, less than the two thirds of
validity left when a renewal starts, so writes rarely wait for a renewal. With the default
10-second lease, a transaction lasts at most 2.5 seconds.

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
- The connection string accepts `sslmode` (`disable`, `allow`, `prefer`, `require`, `verify-ca`,
  `verify-full`), `sslrootcert` (a PEM CA file or `system`), `sslsni` (`0` or `1`),
  `sslnegotiation` (`postgres` or `direct`), and `connect_timeout` in seconds. Direct negotiation
  requires PostgreSQL 17 or later. For example,
  `postgres://role:pw@host:5432/db?sslmode=require&sslnegotiation=direct` opens TLS before the
  startup message. `sslmode=require` without `sslrootcert` encrypts the connection but does not
  verify the server certificate. With a PEM file in `sslrootcert`, `require` verifies the
  certificate chain against that file, but not the host name. `sslmode=verify-ca` verifies the
  chain against the PEM file in `sslrootcert`, or against the system roots when `sslrootcert` is
  absent. It never verifies the host name. `sslmode=verify-full` verifies the chain and the host
  name, against the PEM file in `sslrootcert` or against the system roots when `sslrootcert` is
  absent. `sslrootcert=system` uses the system roots and forces `verify-full`, whatever `sslmode`
  says. Without `sslnegotiation=direct`, `prefer` and `allow` do not verify the certificate, even
  with a PEM file in `sslrootcert`. `sslmode=allow` with `sslnegotiation=direct` is invalid.
  Without TLS parameters, the connection remains plaintext for compatibility with existing
  deployments. Explicit `sslmode=prefer` first attempts TLS without certificate verification;
  previously it remained plaintext. With `sslnegotiation=direct`, `sslmode=prefer` or a missing
  `sslmode` becomes `require`, and the `require` rules above apply, so TLS is mandatory and there
  is no plaintext fallback.
  The connect timeout covers the TLS handshake and authentication, including reconnects.
- A `search.paradeDbSchema` other than `public` is applied as the `search_path` connection option.
  A transaction pooler must pass that startup option through; otherwise keep the tables in `public`.
- A background task copies each committed partition frame's search documents to ParadeDB. Commits
  never wait for it. When ParadeDB is unavailable, the task retries. The queue coalesces complete
  documents per run, sheds intermediate Running updates above its soft limits, and keeps starts,
  deletes, and terminal documents up to its hard limits. Admission beyond a hard limit or a
  diverged stored position rebuilds the partition from committed local state. See the
  [queue policy and defaults](crates/dexzd-search/README.md#sql-visibility-projection).
- On startup, every partition whose ParadeDB position differs from the local store is rebuilt.
- SearchFlows waits up to `search.readYourWritesWait` until frames committed before the request are
  searchable, then returns Unavailable on timeout. While shedding, dropped intermediate updates
  can leave a Running document stale even after the wait succeeds; its next update or close catches it up.
- One ParadeDB schema belongs to one Dex cluster.

On SIGTERM or SIGINT the node stops within 14.5 seconds. Health reports NOT_SERVING, Workers are
told to reconnect, long polls close, and open calls get 4 seconds to finish. Then effects and the
engine stop, and the node releases its lease. The process exits 0, and a replacement process with
the same `cluster.nodeId` acquires the lease at once. When object storage rejects the release, or
the release does not finish within 2 seconds, the process exits 1 and the lease stays held until it
expires.

A lost lease, a stopped commit pipeline, or a halted Flow coordinator stops the node, and the process
exits 1. Restart it. A signal before the node acquires its lease cancels startup and exits 0. If
the outcome of the node's lease write is still unknown, that write is not released, and a
replacement may wait for it to expire. If the node already holds its lease, a signal stops startup
at once and releases the lease. The process then exits 0, or exits 1 when the release fails. If
startup finishes before it observes the signal, the node runs graceful shutdown, with the same
14.5-second total and exit status as a signal received while serving. A failed startup releases its
lease and exits 1.

The node serves only within its local lease validity. The deadline is `lease.duration` after the
last successful renewal was sent, on the local monotonic clock. Renewal starts one third of
`lease.duration` after the last success. Each attempt has its own timeout of one sixth of
`lease.duration`, at least 1 second and at most 5 seconds, while another attempt still fits after
it. The last attempt may use all the remaining time. A renewal must complete 100 ms before the
deadline. A write that lands after its attempt timed out still counts: a later attempt finds
it, and validity counts from when it was sent. A transient failure, such as a 5xx response, a
throttling response, a lost connection, or a timed-out attempt, is retried after a jittered
backoff of 200 ms, doubling up to 2 seconds. Retries continue until 100 ms before the deadline.
Then the lease is lost. A conditional write lost to another holder, or a
permanent storage error, stops the node at once.

After the deadline passes without a renewal, the node accepts no new commands and acknowledges no
commits. A watchdog thread that does not depend on the async runtime enforces the deadline. It
reports NOT_SERVING for every health name at the deadline, and the graceful lease-lost path exits
the process with status 1. If the process still runs 2 seconds after the deadline, for example
because the async runtime stalled or every object storage call hangs, the watchdog aborts it with
SIGABRT. The abort skips exit handlers, which would wait for local storage background work; the
local store recovers from its write-ahead log. A successor can take the lease over 1 second after
its recorded expiry, and its conditional writes fence the command log. Until the process ends, a
partly stalled node may still answer reads from its local state.

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
Health turns NOT_SERVING when the lease validity deadline passes without a renewal. The process
then exits 1, or is aborted with SIGABRT within 2 more seconds, so a probe never sees a stalled node
as SERVING for long.
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

`dev.durabilityMode` selects how the embedded s3mock stores acknowledged writes:

| Mode | Survives | Cost |
| --- | --- | --- |
| `processCrashSafe` (default) | A process kill: SIGKILL, an OOM kill, or a deleted Pod. A host crash or power loss can lose recent writes. | Close to `relaxed`. Every write reaches the files before the reply, without fsync. |
| `strict` | A process kill, a host crash, and power loss. | Every write is flushed with fsync. On macOS each flush is a full device flush, so a Flow takes several times longer. |
| `relaxed` | Only a graceful stop. | Writes stay in memory until `dev` stops gracefully. |

After a process kill in the default or `strict` mode, the next `dev` start on the same data
directory recovers every acknowledged Flow. A relaxed object store rolls back after an abrupt exit,
while the local store under `<data-dir>/store` keeps newer state. Every later start then fails, in
any mode, with an error that names the data directory. Delete the whole data directory to start
empty.

Upgrading a data directory from an earlier release follows these rules:

- A directory that an earlier release stopped gracefully keeps working in every mode.
- A directory that an earlier release left after an abrupt exit is refused in the default and
  `relaxed` modes, because those releases defaulted to `relaxed` and may have rolled back.
- With `dev.durabilityMode: strict`, the same directory is accepted, and the upgrade is logged at
  info level. Strict releases fsynced every commit, so the configuration is trusted. Set it only when
  every earlier run of the directory used `strict`.

After the first start of a release with this rule, the directory is stamped, and any mode works.

`dev` defaults `lease.duration` to 30 seconds, unless the `--config` file sets it; `serve` defaults
it to 10 seconds. A `dev` node has no standby, so the longer lease costs no failover time and lets
the node ride out longer host stalls.

`dev` holds an exclusive lock on `<data-dir>/dev.lock` for its whole run, so a second `dev` process
on the same data directory fails to start. While `dev` holds the lock, no other process can write
its embedded object store. A lease that the store still shows as held therefore belongs to a
stopped process, so `dev` takes it over at once, ignoring `lease.failIfHeld`. A restart never waits
for the lease, even after a crash. The takeover raises the node epoch, as any takeover does. On a
filesystem that cannot lock files, `dev` logs a warning and waits for a held lease to expire, up to
31 seconds.

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

### PostgreSQL visibility

`search.backend: postgres` uses PostgreSQL 15+ with no extensions. Configure
`postgresConnectionString` (or `DEX_ZD_POSTGRES_CONNECTION_STRING`),
`postgresConnectionPoolSize` (default 10), and `postgresSchema` (default public).
TLS and `readYourWritesWait` work as for ParadeDB. A database schema records its
dialect and refuses the other backend; switch to a new database or schema to
rebuild automatically from committed state. ParadeDB version 3 upgrades without
clearing documents.

Each tenant receives ten Keyword slots and three each of Int, Datetime, Double,
Bool, KeywordArray, and Text, in registration order. Full types reject new
registrations, and an over-capacity registry prevents startup. The append-only
registry has no delete API. Indexed Keyword values and KeywordArray elements
are limited to 1024 UTF-8 bytes; ingress rejects larger writes, and projection
records previously committed oversized documents as skipped. Attributes use
JSONB until backfill finishes. Explicit StartTime/CloseTime ordering sorts all
matches; negative predicates may not use an index.
