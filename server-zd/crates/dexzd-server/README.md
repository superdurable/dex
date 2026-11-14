# dexzd-server

## Purpose

The dex-server-zd executable: typed configuration, startup, shutdown, the serve mode for external object storage, and the dev mode with an embedded S3 emulator.

## Manual Aliyun OSS tests

`tests/serve_process/aliyun_oss_real_bucket.rs` checks `objectStore.provider: aliyunOSS`
against a real Aliyun OSS bucket. CI never runs these tests. Run them after changing aliyunOSS
code, after upgrading aws-sdk, and before a release.

The bucket must never have had versioning enabled. Each test writes under a new prefix,
`dexzd-manual-test/<unix-nanos>/`, and deletes every object under it at the end, also when a
check fails. From `server-zd/`:

```text
DEX_OSS_ENDPOINT=https://s3.oss-cn-hangzhou.aliyuncs.com \
DEX_OSS_REGION=cn-hangzhou \
DEX_OSS_BUCKET=<bucket> \
DEX_OSS_ACCESS_KEY_ID=<access key ID> \
DEX_OSS_SECRET_ACCESS_KEY=<secret access key> \
cargo test -p dexzd-server --test serve_process real_aliyun_oss -- --ignored --nocapture --test-threads=1
```

With `--ignored`, a missing variable fails the test and names every missing variable.

## Manual Tencent COS tests

`tests/serve_process/tencent_cos_real_bucket.rs` checks `objectStore.provider: tencentCOS`
against a real Tencent COS bucket. CI never runs these tests. Run them after changing tencentCOS
or aliyunOSS code, after upgrading aws-sdk, and before a release.

Use a single-AZ bucket whose versioning was never enabled. The checks cover the bucket checks,
concurrent appends, the versioned compare-and-swap, a 20 MiB append and multipart upload, batch
deletes, and a serve process restart. Each test writes under a new prefix and deletes it at the
end, as the Aliyun OSS tests do. From `server-zd/`:

```text
DEX_COS_ENDPOINT=https://cos.ap-guangzhou.myqcloud.com \
DEX_COS_REGION=ap-guangzhou \
DEX_COS_BUCKET=<bucket-appid> \
DEX_COS_ACCESS_KEY_ID=<SecretId> \
DEX_COS_SECRET_ACCESS_KEY=<SecretKey> \
cargo test -p dexzd-server --test serve_process real_tencent_cos -- --ignored --nocapture --test-threads=1
```

## Manual object storage soak

`tests/serve_process/object_store_soak.rs` runs an active and a hot standby `serve` process of
one node ID on one prefix for hours, behind a router that sends clients to whichever one serves.
CI never runs it. Run it on a real bucket before a release of the tencentCOS or aliyunOSS code,
and for a few minutes on s3local after changing the soak.

The load starts chain Flows (Steps), timer Flows, and counter Flows (RPCs and locked
Attributes), with a reconnecting Worker. Vault Flows that no Worker runs get 64 KiB Attributes,
16 KiB hard-delete values, and Channel messages. Every few minutes a seeded schedule injects one
fault, in shuffled rounds of every kind:

- SIGKILL of the active; the hot standby takes over, and the killed slot restarts as standby;
- SIGTERM of the active, which releases its lease, then the same restart;
- a cold restart of both processes on wiped local directories, which must restore a snapshot
  when one exists;
- a SIGKILL of both processes, then the same cold restart once their lease expired. The first
  process starts as a cold standby, so its recovery restores the latest periodic snapshot and
  must replay the log written since it;
- stopping the oldest vault Flows and erasing their hard-delete values.

After each fault the counters and vault Flows must read back every acknowledged write. A cold
restart must restore a snapshot when one exists. When the fault interval exceeds the snapshot
interval by at least a minute, every cold restart must restore one, and the soak warns at the
start otherwise.

At the end, the acknowledged run of every chain and timer Flow must complete with its expected
output. Each chain Step must run under exactly one Step execution ID; its handler may run again
after a crash, and the summary counts those re-executions. Each timer Step must run only after
its timer fired. Each counter must hold every acknowledged increment once, and every resent
increment must end acknowledged. Every hard-delete object must be gone.

The router samples each process's readiness every 25 ms and fails the test when two processes
served at once. Sampling can miss a short overlap; the node lease and create-only log writes are
what fence one. With HEAD probing (tencentCOS, or aliyunOSS with `versionDiscovery: head`),
every versioned key except startup probes must hold consecutive first versions that name their
predecessors, with an anchor once generation 1 is gone. The manifest and node lease keys must be
listed, and a failed HEAD fails the check. A progress line every 30 seconds and the final summary report the faults,
takeover times, commit latency, `dexzd_object_store_versioned_listing_stale_total` by site
(`newest_version_lookup` grows on COS), and the objects by area. Any violation fails the test.
The soak restarts a process that exits outside an injected fault as standby, as a container
supervisor would restart it, and keeps checking every invariant; the counters and vault Flows
must read back every acknowledged write after each such exit. A process may stop by design, for
example after `commit pipeline failed: object storage did not accept a log batch before its
deadline` during an object storage stall. The summary lists a start that failed after it took the
lease as a FAILED START, and every other exit as an UNPLANNED EXIT with its stop reason: the last
`dex-server-zd:` line of its log, else its last ERROR line. A stop on a late batch of a killed or
exited process is counted apart. Three failed starts of one slot in a row fail the test, and so
do more than three unplanned exits and failed starts of one slot within ten minutes.

The progress line and the summary count hot standby restarts ("the hot standby restarts after an
error"), takeover retries ("the takeover failed before the node lease"), and WARN and ERROR lines
across every process log. The summary also lists the 15 most frequent WARN and ERROR patterns, with
numbers and IDs replaced. These counts only report by default; set
`DEX_SOAK_MAX_STANDBY_RESTARTS` or `DEX_SOAK_MAX_TAKEOVER_RETRIES` to fail the test above a limit.

Each run writes under a new prefix, `dexzd-soak/<unix-nanos>/`, and deletes every object under
it at the end, also after a failure. Cleanup trusts only two listings five seconds apart that
agree, and fails when objects remain or a listing fails. On tencentCOS targets, the cleanup
starts after both processes stopped and lists with the AmazonS3 dialect, which shows the 0-byte
residues of interrupted appends that the tencentCOS dialect reads as absent. It reports how many
empty residues it deleted. Set `DEX_SOAK_KEEP_OBJECTS=1` to keep the objects; the soak then
reports how many stay, and how many of them are empty residues. Process logs
stay in `DEX_SOAK_LOG_DIR`, by default a new directory under the system temporary directory that
the soak prints.

| Variable | Meaning |
| --- | --- |
| `DEX_SOAK_TARGET` | `tencentCOS` or `aliyunOSS` for a real bucket; `s3local-tencentCOS`, whose listings repeat for two seconds and whose appends show a 200 ms empty placeholder one time in five and are interrupted one time in 2000, or `s3local-aliyunOSS` for an emulator in the test |
| `DEX_SOAK_VERSION_DISCOVERY` | `list` (default) or `head`, for aliyunOSS targets |
| `DEX_SOAK_DURATION` | How long the load runs, such as `2h` (default), `30m`, or `1h30m` |
| `DEX_SOAK_FAULT_INTERVAL` | Time between faults; defaults to a sixth of the duration, from 30 seconds to 4 minutes |
| `DEX_SOAK_SEED` | The fault schedule's seed; defaults to the start time, and the soak prints it |
| `DEX_SOAK_SNAPSHOT_INTERVAL` | `snapshot.interval` of both processes; defaults to `60s`. A longer interval, such as the server default of `10m`, leaves a longer log to replay after a crash |
| `DEX_SOAK_LEASE_DURATION` | `lease.duration` of both processes; defaults to the server default of `30s`. A short lease from outside the bucket's region can expire during slow requests and stop the active process |
| `DEX_SOAK_MAX_STANDBY_RESTARTS` | Unset (default) only reports hot standby restarts; a count fails the test once the restarts over all processes exceed it |
| `DEX_SOAK_MAX_TAKEOVER_RETRIES` | Unset (default) only reports takeover retries; a count fails the test once the retries over all processes exceed it |
| `DEX_SOAK_LOG_STALL_INTERVAL` | s3local targets only: how often to hold log batch writes for 15 seconds, outside the fault schedule, so the serving process exits as an UNPLANNED EXIT. Unset by default |
| `DEX_COS_*`, `DEX_OSS_*` | The bucket variables of the manual tests above, for real targets |

From `server-zd/`, a two-hour soak on Tencent COS:

```text
DEX_SOAK_TARGET=tencentCOS \
DEX_COS_ENDPOINT=https://cos.ap-guangzhou.myqcloud.com \
DEX_COS_REGION=ap-guangzhou \
DEX_COS_BUCKET=<bucket-appid> \
DEX_COS_ACCESS_KEY_ID=<SecretId> \
DEX_COS_SECRET_ACCESS_KEY=<SecretKey> \
cargo test -p dexzd-server --test serve_process object_store_soak -- --ignored --nocapture
```

The summary lists the log replay of each recovery: the applied seq it started after, the
batches it replayed, and how long the recovery took. A serving process takes its first snapshot
one interval after it starts, so the log to replay after a crash grows with the interval, and
across faults when the interval is longer than the time between them. For a backlog of several
thousand batches on a real bucket, add for example `DEX_SOAK_SNAPSHOT_INTERVAL=5m` to the
command above.

A five-minute smoke run on the emulator:

```text
DEX_SOAK_TARGET=s3local-tencentCOS DEX_SOAK_DURATION=5m \
cargo test -p dexzd-server --test serve_process object_store_soak -- --ignored --nocapture
```

On `s3local-tencentCOS`, the summary counts the append placeholders and interrupted appends that
the emulator's request log showed, a lower bound when the log overflows between samples. Writers
retry interrupted appends; one that stops a process anyway counts as an UNPLANNED EXIT or a FAILED
START, which the run absorbs.

Add `DEX_SOAK_LOG_STALL_INTERVAL=2m` to exercise unplanned exits: each stall must end in one
UNPLANNED EXIT whose reason names the log batch deadline, and the run must still pass.

Run-level retention purges are not part of the soak: `retention.closedFlowDuration` applies to
every Flow of a node, so it would also purge the Flows that the final check reads.
