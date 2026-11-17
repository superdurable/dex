# Adaptive pull Worker slot streams

- **Status:** proposal. Nothing in this document is decided. The owner decides the items in [Pending decisions](#pending-decisions).
- **Tracking issue:** [#1989](https://github.com/superdurable/dex/issues/1989) and its 2026-11-17 comment.
- **Code read:** `origin/main` at `35ee8dcdccf64fb9db38d8b0cc0a7aacefa29d4f` (2026-11-17). Every `path:line` below refers to that commit.
- **Scope:** design only. It proposes no product code in this change.

## Summary

Today a pull Worker opens one reserved-slot stream per task slot at start. The stream count stays at `MaxConcurrentTasks` whether the Worker is idle or busy. This document proposes that each SDK vary the number of open Step-and-RPC slot streams between a floor and the `MaxConcurrentTasks` ceiling:

- The Worker keeps at least one idle Step-and-RPC stream while it is below the ceiling. When a hand-out takes its last idle stream, it doubles its open stream count, with one growth batch in flight at a time.
- Every shrink window, the Worker closes half of the idle surplus above the window's peak busy count plus 25% headroom. It closes with the existing clean half-close.
- Reserved RPC-only slots stay fixed.

The recommendation is Option A: SDK-only, with no protocol or server change. It keeps the #1253 attempt semantics unchanged. The server already tells the Worker what it needs to know: a hand-out that takes the last idle stream. Option B, a server hint, is a later, optional addition. Option C, multiplexing slots over a few streams, removes more server cost. It does so by rewriting the hand-out, half-close, and failure semantics of #1253, so it is held back until a benchmark shows that busy streams, not idle ones, limit the server.

A Worker with its floor equal to its ceiling behaves exactly as today. Phase 1 ships the mechanism opt-in, with that equality as the default. A later phase changes the defaults to a floor of the CPU count and a ceiling of 100. The default change from the CPU count to 100 must not ship before adaptive streams. See [Rollout](#rollout-and-api-changes).

## Background

### The reserved-slot protocol today

[#1253](https://github.com/superdurable/dex/pull/1253) made each pull stream one Worker slot. It also made the server commit `StepAttemptStarted` when it hands a task to an idle slot.

- **One assignment per stream.** `WorkerState` holds one `current: Option<Assignment>` (`server-zd/crates/dexzd-worker-dispatch/src/worker_registry.rs:131-140`). A reserved-slot stream that declines for capacity breaks the protocol, and the server ends it (`server-zd/crates/dexzd-worker-dispatch/src/worker_registry/worker_protocol.rs:325-334`; `protos/dex.proto:281-285`).
- **Registration.** The first frame must be `Register` (`worker_protocol.rs:56-69`). The server checks the node limit `matching.maxStreams` and the per-app-Worker limit `matching.maxConcurrentTasksCap`. Either limit answers `RESOURCE_EXHAUSTED` (`worker_protocol.rs:98-109`). The acknowledgement reports `max_concurrent_tasks_per_app_worker`, which counts RPC-only slots too (`worker_protocol.rs:129-141`; `protos/dex.proto:334-339`). The stream becomes idle right after the acknowledgement is queued (`worker_protocol.rs:149`).
- **Queues per stream.** A reserved-slot stream serves its sticky queue (`app_worker_id`) and every registered Flow type (`server-zd/crates/dexzd-worker-dispatch/src/worker_registry/worker_streams.rs:70-83`). It is inserted into each of those `1 + F` queues (`worker_protocol.rs:116-125`).
- **Idle order.** Idle streams carry one global, increasing position (`server-zd/crates/dexzd-worker-dispatch/src/worker_registry/stream_registry.rs:55-65`). `pick_worker` walks a queue's idle streams in that order and takes the first eligible one, unless same-Flow affinity prefers another app Worker (`worker_registry.rs:791-826`). So the stream idle longest is picked first, across all Workers in the queue.
- **RPC before Step, then backlog.** A newly idle stream wakes the oldest RPC waiter, else the oldest Step waiter, and otherwise claims durable backlog (`stream_registry.rs:150-200`). RPC waiters get the stream promised (`stream_registry.rs:233-236`).
- **Step dispatch.** A Step first checks for qualified backlog. If the queue already has backlog that a connected Worker can run, the Step goes straight to the durable task queue (`server-zd/crates/dexzd-worker-dispatch/src/worker_registry/step_reservation.rs:162-164`). Otherwise it waits for the sticky Worker for up to `matching.stickyWait` (5 s), then for any Worker of the Flow type for up to `matching.syncMatchWait` (200 ms), then enqueues (`step_reservation.rs:165-211`; defaults in `server-zd/crates/dexzd-worker-dispatch/src/matching_config.rs:44-59`). The sticky wait ends early only when the sticky queue has no streams or the sticky Worker declined the type (`step_reservation.rs:254-259`).
- **RPC dispatch.** An RPC never queues. It waits in memory, sticky first, until `matching.dispatchWait` (10 s) ends (`worker_registry.rs:350-420`).
- **Backlog Claims.** Matching keeps one head per partition and queue. A head is claimable only while no Claim of that partition and queue is in flight (`server-zd/crates/dexzd-worker-dispatch/src/task_backlog.rs:87-95, 241-260`). A Claim takes an idle stream and holds it while the partition owner answers (`backlog_matching.rs:497-531`). The owner's reply names the next row (`task_backlog.rs:47-55`). So each partition drains one queue one Claim round trip at a time. Backlog drain parallelism is the number of partitions with backlog for the queue, not the number of idle streams.
- **Declined-type cache.** A decline of an unknown Step type or RPC name is cached per app Worker (`worker_streams.rs:259-269, 280-289`). The entries are removed when the app Worker's last stream disconnects (`worker_registry.rs:941-950`).
- **Clean half-close.** When a Worker half-closes a reserved-slot stream, the server stops sending on it. A sent task with no output is returned as `NOT_DELIVERED` and dispatched again with the same attempt number. A reset within 200 ms after the half-close counts as a disconnect instead (`worker_streams.rs:118-148`; `worker_protocol.rs:194-197`; `protos/dex.proto:185-197`).
- **Keepalive.** Every `matching.pingInterval` (20 s) the server sends a `Ping` to an idle stream and an empty response to a busy one (`worker_protocol.rs:182-201, 212-234`). HTTP/2 keepalive is per connection, every 20 s with a 10 s timeout (`server-zd/crates/dexzd-server/src/server_node/grpc_server.rs:38-39, 149-151`).

### Server cost of one idle stream

Each registered stream holds the following, whether idle or busy:

- hyper and tonic HTTP/2 stream state and the request decoder;
- one tokio reader task (`worker_protocol.rs:152-161`);
- an unbounded response channel and a `watch` close channel (`worker_protocol.rs:70-71`);
- an `Arc<WorkerStream>` with its state mutex (`worker_protocol.rs:72-91`);
- registry entries: `streams`, `worker_stream_ids`, and membership in `1 + F` queues (`worker_protocol.rs:111-125`);
- idle-order entries in each of those queues while idle (`stream_registry.rs:55-65`);
- `step_workers` entries for each backlog Flow type (`server-zd/crates/dexzd-worker-dispatch/src/worker_registry/backlog_matching.rs:118-131`);
- a Ping every 20 s and the Worker's Pong. At 100,000 idle streams that is 5,000 Pings and 5,000 Pongs per second.

Correction to the issue text: the `mpsc::channel(32)` at `worker_streams.rs:174` is created per assignment, when a task frame is sent. An idle stream does not hold it.

Idle streams also lengthen matching scans. `pick_worker` skips promised, excluded, RPC-only, and declined streams one by one (`worker_registry.rs:800-818`). `match_backlog_queue` walks idle streams the same way (`backlog_matching.rs` `match_backlog_queue`, starting at line 467). Hypothesis: with many idle streams from a Worker that declined a type, these scans cost matching CPU under the registry lock. No benchmark measures this.

The server sets no HTTP/2 `max_concurrent_streams` (`grpc_server.rs:149-151`). tonic 0.14.6 passes its default `None` to hyper, which replaces hyper's default of 200 (tonic `src/transport/server/mod.rs:128, 812`; hyper 1.12.0 `src/proto/h2/server.rs:69`, `src/server/conn/http2.rs:215-217`). So one HTTP/2 connection carries every slot stream of a Worker without a per-connection limit.

The node limit is `matching.maxStreams` = 100,000, and the per-app-Worker limit is `matching.maxConcurrentTasksCap` = 256 (`matching_config.rs:28-31, 52-53`).

### The pull Worker in each SDK

All five SDKs open every slot stream at start and keep the count fixed. None has a stream metric.

| | Go | Python | Java | TypeScript | Rust |
| --- | --- | --- | --- | --- | --- |
| Slot option and default | `MaxConcurrentTasks`, `runtime.NumCPU()` (`sdk-go/dex/worker_pull.go:110-115`) | `max_concurrent_tasks`, `os.cpu_count()` (`sdk-python/dex/_pull_worker.py:138`) | `maxConcurrentTasks`, `availableProcessors()` (`sdk-java/src/main/java/io/superdurable/dex/PullWorkerOptions.java:46-47`) | `maxConcurrentTasks`, `availableParallelism()` (`sdk-typescript/src/pull-worker.ts:124`) | `max_concurrent_tasks`, `available_parallelism()` (`sdk-rust/crates/dex-sdk/src/worker_pull.rs:176-186`) |
| Reserved RPC slots | inside the total; 1 when total ≥ 2 (`worker_pull.go:116-124`) | same (`_pull_worker.py:139-143`) | same; the first N slots (`PullWorkerOptions.java:49-53`, `PullWorker.java:455-459`) | same; the last N slots (`pull-worker.ts:125-128, 227-228`) | same; the first N slots (`worker_pull.rs:187-202, 350-359`) |
| Stream creation | incremental `reconcile` per slot kind (`worker_pull.go:312-334`) | incremental `_reconcile` (`_pull_worker.py:232-253`) | only when `streams.isEmpty()` (`PullWorker.java:454-463`) | only when `streams.size == 0` (`pull-worker.ts:225-243`) | once in `open_slots` (`worker_pull.rs:343-388`) |
| Cap check | first ack over the cap fails start; later only warns (`worker_pull.go:391-410`) | same (`_pull_worker.py:267-288`) | same (`PullWorker.java:632-652`) | same (`pull-worker.ts:312-325`) | same (`worker_pull.rs:810-838`) |
| Reconnect backoff | 100 ms doubling to 5 s, plus up to half as jitter (`worker_pull.go:523-541`) | same (`_pull_worker.py:512-521`) | same (`PullWorker.java:467-469, 1126`) | same (`pull-worker.ts:676-677`) | same (`sdk-rust/crates/dex-sdk/src/worker_pull/reconnect.rs:60-72`) |
| `RESOURCE_EXHAUSTED` | permanent: 60 s retry and a recorded permanent error (`worker_pull.go:552-554`) | permanent (`_pull_worker.py:50`) | permanent (`PullWorker.java:385`) | permanent (`pull-worker.ts:668, 691`) | permanent (`worker_pull.rs:1095-1104`) |

"Same" means the same rule as the Go column. Every SDK keeps a deprecated, no-op idle-streams-per-queue option. Readiness waits for one registered stream of each slot kind in Go, Python, Java, and Rust. In TypeScript, readiness returns after the first registered slot (`pull-worker.ts:218-223`). Each slot runs its handler concurrently with its stream loop: goroutines in Go, asyncio tasks with a thread pool in Python, a fixed handler pool in Java, promises in TypeScript, and tokio tasks in Rust. The Python and Java handler pools are sized by the slot count (`sdk-python/dex/worker.py:128-131`; `sdk-java/src/main/java/io/superdurable/dex/Worker.java:415-431`). These SDK facts come from a survey of the files at the commit above.

The unmerged branch `sdk/worker-slot-defaults` (commit `b782e79c3`, WIP) changes the default to 100 in Go, Python, and Java. Before the first acknowledgement it opens one stream. It clamps an unset default to the server cap with an info log. That branch is on hold. Today's default stays the CPU count.

### History

- **Before SDK 1.35**, Workers kept idle streams per queue and opened more on demand.
- [#740](https://github.com/superdurable/dex/issues/740): surplus idle streams never closed after load dropped. Each queue kept its historical peak. With 15 Flow types and 256 slots, a Worker could hold 4,112 streams instead of 272. [#741](https://github.com/superdurable/dex/pull/741) closed surplus streams with the half-close path in all five SDKs. Its constraints apply here too. Readiness must not change. A Worker never closes a busy stream, its last idle stream, or a stream with an assignment in flight. Stream churn must stay low.
- [#1253](https://github.com/superdurable/dex/pull/1253) replaced that model with one reserved-slot stream per slot and attempt start at hand-out.
- `docs/design/worker-pull-mode.md:7, 19` still describes the pre-#1253 model. Its stream formula is stale.

### Benchmark evidence

- No benchmark measures idle-stream cost (#1989). The 4.5 GiB RSS at C=12,288 on c7gd.4xlarge was measured under full load (`benchmark/results/2026-11-09-aws-s3-c7gd-4xlarge-v0.1.23/06-final-trials-c48/report.md:7-9`). It includes RocksDB and caches, so it is not a per-stream cost.
- `dexbench` runs one pull Worker per connection and gives each one C divided by the connection count as `MaxConcurrentTasks` (`benchmark/dexbench/runner.go:100-116`; `benchmark/README.md:97`).
- **S2 tail at 256 slots per Worker.** At C=4,096 with 16 connections, which is 256 slots per Worker, S2 had a p99 of 5,475 ms. With 32 connections, 128 slots per Worker, it had 725 ms (`benchmark/README.md:73`; `benchmark/results/2026-11-09-aws-s3-c7gd-4xlarge-v0.1.23/README.md:42-50`). Server metrics put 0.9% of Steps between 2.5 s and 10 s schedule-to-start. The stickyWait hypothesis in that README is that a Step became ready before its Worker released the previous attempt's slot and then waited the 5 s `matching.stickyWait`. It is untested.
- [#1118](https://github.com/superdurable/dex/issues/1118): above about 2,000 concurrent Flows, throughput collapsed while CPU was unsaturated. Doubling the Worker slots did not help, so slot count was not the cause there.
- [#1266](https://github.com/superdurable/dex/issues/1266) asks for burst, `syncMatchWait`, and backlog-drain benchmarks. No results are committed.
- [#821](https://github.com/superdurable/dex/issues/821) and #1118 put a SYNC Step commit at roughly 25 to 50 ms on object storage. This document uses that range as a hypothesis for commit latency.

## Requirements

1. Server cost of idle Workers follows actual concurrency. An idle Worker holds a small, bounded number of streams.
2. `MaxConcurrentTasks` keeps its meaning: the most tasks the Worker runs at once, including reserved RPC slots.
3. The #1253 semantics stay unchanged. The server starts an attempt at hand-out. A slot is ready only while idle. The Worker runs every task it receives. A half-close returns an undelivered task as `NOT_DELIVERED` without spending a retry.
4. A Worker reaches its ceiling quickly under a burst or a backlog. The ramp-up adds bounded schedule-to-start latency.
5. Open streams return toward the floor after load drops. They never leak the way #740 did.
6. Released SDKs and released servers keep working in every combination.
7. All five SDKs behave the same: same defaults, same thresholds, and the same stream trajectory for the same load.
8. Readiness, the declined-type cache, RPC reservation, sticky dispatch, and graceful stop keep their current behavior.

## Non-goals

- Changing matching order, sticky dispatch, or `syncMatchWait`.
- Scaling Worker processes. That remains the job of an external controller using the task queue metrics.
- Throttling by CPU or memory inside the Worker. `MaxConcurrentTasks` stays the only concurrency limit.
- Fixing the S2 tail at the ceiling. See [Sticky dispatch](#sticky-dispatch).
- Adaptive reserved RPC slots.

## Options

### Option A: SDK-only adaptive streams from local signals

**Mechanism.** Each SDK varies the number of open Step-and-RPC slot streams between a floor and the ceiling. It uses only what it sees on its own streams.

- *All slots busy.* A task frame takes the Worker's last idle Step-and-RPC stream. The server only sends a task to an idle stream, so this is exact.
- *A freed slot receives a task at once.* When a stream finishes, the server re-idles it (`worker_streams.rs:271-277`; `worker_registry.rs:893-916`). It wakes a waiter, or else starts a Claim if a head is claimable (`stream_registry.rs:196-199`). The server picks the longest-idle stream first, so a just-freed stream receives a task quickly only when the Worker has no older idle stream. That is the same event as "all busy" happening again. This document therefore folds the two signals into one trigger: the last idle stream is taken.

How close this is to "the queue has demand" differs by path. These are hypotheses for the benchmarks:
- **In-memory waiters:** RPCs, and Steps inside the sticky wait or `syncMatchWait`. Every waiter takes an idle stream at once, so demand above the open streams empties the idle set and triggers growth.
- **Durable backlog.** Matching runs at most one Claim per partition and queue (see [Background](#the-reserved-slot-protocol-today)). Backlog can sit while the Worker still has idle streams, and the Worker then does not grow. That is the right outcome, because more streams would not drain a Claim-bound queue faster. The Worker grows only when the Claims in flight, plus the waiters, use up its idle streams.
- **Several Workers.** The signal also depends on how idle streams are spread across them. See [Fairness](#multi-worker-fairness-and-mixed-fleets).

Shrink uses the existing clean half-close of an idle stream.

**Protocol changes.** None.

**Compatibility.** Released servers accept any number of streams up to their caps, so a new SDK works against every protocol-4 server. Released SDKs are unaffected. A Worker whose floor equals its ceiling behaves exactly as today.

**Server cost.** Streams equal busy slots plus at most the floor or the growth overshoot. In steady state that is busy plus about 25% headroom. Each growth or shrink costs one registration or one disconnect, which are the existing paths.

**Failure modes.**
- Ramp-up latency for bursts larger than the open streams. See [Ramp-up latency](#ramp-up-latency).
- Oscillation under periodic load. See [Oscillation](#oscillation).
- A shrink half-close that races a hand-out returns the task as `NOT_DELIVERED`. That costs a committed `StepAttemptReturned` and a re-dispatch, and it spends no retry.
- A leak if the SDK reopens a stream it closed on purpose (#740 class).
- The SDK cannot see schedule-to-start. `Context` carries `first_attempt_timestamp` but no schedule time (`protos/dex.proto:469-484`). A is blind to queue age.

### Option B: Option A plus a server backlog or schedule-to-start hint

**Mechanism.** Option A, plus a server hint on each task frame. The SDK grows harder when the hint shows queuing. It also reports schedule-to-start in its own metrics.

**Protocol changes.** An additive field on `PollWorkerTask` (`protos/dex.proto:347-361`), for example one of:
- `google.protobuf.Duration schedule_to_start = 6`. The server computes it at send time from the schedule time it already uses for `dexzd_step_schedule_to_start_latency_seconds` (`worker_registry.rs:869-880`). A server-side duration avoids clock skew.
- `bool is_from_task_queue = 6`, set when the task came from a backlog Claim.

A cheap depth hint is not available. Matching keeps one claimable head per partition and queue, not a count (`server-zd/crates/dexzd-worker-dispatch/src/task_backlog.rs:87-95, 184-195`). Matching does know whether a queue has backlog and the oldest pending age (`task_backlog.rs:421-434`; `backlog_matching.rs:394-430`).

**Compatibility.** proto3 additive. Released SDKs ignore the field. A new SDK against an older server sees no value and falls back to A. The SDK can detect support from the field's presence or from a protocol version bump.

**Server cost.** A Duration on each Step task frame. No extra lock work: the schedule time is already at hand at send time.

**Failure modes.** Those of A. Also: the hint describes the task already sent, so it arrives one hand-out late. It also adds a field that all five SDKs must interpret identically.

### Option C: decouple slots from streams

**Mechanism.** A Worker keeps a few streams, for example one per Worker or one per CPU. Each stream registers a slot capacity. The server counts free capacity per stream and hands out several tasks on one stream, distinguished by `task_id`, which every frame already carries (`protos/dex.proto:266-268, 286-288, 348-350, 364-366`).

**Protocol changes.** Large:
- a registration field for slot capacity and a capability flag;
- a frame or field to change capacity at run time;
- per-task `NOT_DELIVERED` on a stream that has several tasks in flight;
- per-task cancellation, which exists (`PollWorkerTaskCancelTask`);
- a new protocol version.

**Server changes.** Large:
- `WorkerState.current` becomes a map;
- the idle order becomes capacity-weighted;
- `pick_worker`, `claim_backlog_for`, `finish_assignment`, `half_close`, and `disconnect` change;
- the RPC promise logic changes.

These are the paths that #1253 hardened.

**Compatibility.** Released SDKs keep one-slot streams, so the server must support both models indefinitely.

**Server cost.** The lowest. Streams per Worker are constant, idle or busy.

**Failure modes.**
- **Blast radius.** A stream reset fails every running attempt on it as `NOT_COMPLETED`, and each one spends a retry. Today a reset costs one attempt.
- **Head-of-line blocking.** HTTP/2 flow control is per stream, so one task's large output or stream writes can delay the frames of others.
- The half-close race becomes per task.

### Option D: status quo with fixed slots

**Mechanism.** No change. Raise the default later only as far as the stream budget allows.

**Server cost.** `MaxConcurrentTasks` streams per Worker at all times. Rolling deploys double that while old and new Workers overlap. A failover re-registers every stream at once.

**Failure modes.** A default of 100 makes 1,000 idle Workers fill `matching.maxStreams`. See [Budgeting](#budgeting-maxconcurrenttaskscap-and-maxstreams).

### Comparison

| | A: SDK adaptive | B: A plus hint | C: multiplexed slots | D: fixed |
| --- | --- | --- | --- | --- |
| Protocol change | none | one additive field | large, new version | none |
| Server change | none | small | large, in #1253 paths | none |
| #1253 semantics | unchanged | unchanged | per-task rework | unchanged |
| Idle server cost | floor per Worker | floor per Worker | a few streams per Worker | ceiling per Worker |
| Busy server cost | one stream per busy slot | same | a few streams per Worker | ceiling per Worker |
| Burst latency | ramp of log2(ceiling / floor) rounds | same, faster growth possible | none | none |
| Reset blast radius | one attempt | one attempt | all tasks on the stream | one attempt |
| Five-SDK work | moderate | moderate plus hint | large | none |

**Recommendation:** Option A now. Keep B as an optional later phase if the burst benchmark shows A's ramp or overshoot is a problem, or if per-Worker schedule-to-start is wanted. Revisit C only if the idle-stream benchmark shows that busy streams, not idle ones, limit a node.

## Recommended design (Option A)

### Terms

Per Worker, for Step-and-RPC slot streams only:

- `E`, the effective ceiling: `min(MaxConcurrentTasks, reported cap) - R`. `R` is the reserved RPC slot count. The reported cap is `max_concurrent_tasks_per_app_worker`, where zero means none.
- `L`, the floor.
- `S`, the open streams: registered or registering, and not closing.
- `P`, the registering streams: opened, with no acknowledgement yet.
- `B`, the busy streams: a task frame received and no final frame sent yet.
- `I`, the idle streams: `S - P - B`.

The rules below apply only to Step-and-RPC streams. RPC-only streams stay at `R`.

### Floor and ceiling defaults

- **Ceiling.** `MaxConcurrentTasks` keeps its meaning and its current default, the CPU count. Phase 2 changes the default to 100 (see [Rollout](#rollout-and-api-changes)).
- **Floor.** A new option, proposed as `MinOpenSlots` (pending decision D7). It counts Step-and-RPC streams and must satisfy `1 ≤ L ≤ MaxConcurrentTasks - R`.
  - **Phase 1 default:** `MaxConcurrentTasks - R`, so the Worker stays fixed and identical to today. Adaptation is opt-in by setting a lower floor.
  - **Phase 2 default:** `min(MaxConcurrentTasks - R, max(1, CPU count - R))`. On a machine with at least 2 CPUs, a Worker with the default ceiling then still holds exactly today's default stream count while idle. Only the capacity above the CPU count adapts.
  - With 1 CPU, today's Worker holds 1 stream and no RPC slot. With a ceiling of 100, `R` becomes 1, so the Worker holds 2 streams.
- **Why the floor is at least 1:**
  - Readiness needs one registered Step-and-RPC stream (`docs/reference/server/workers.mdx:20-26`).
  - The declined-type cache entries of an app Worker are dropped when its last stream disconnects (`worker_registry.rs:941-950`). A Worker that scaled to zero would silently reset its exclusions.
  - The sticky search ends at once when the sticky queue has no streams (`step_reservation.rs:257`; `worker_registry.rs:956-958`). A Worker at zero would also lose sticky dispatch.

### Grow rule

**Trigger.** A task frame arrives on a Step-and-RPC stream. It leaves `I = 0` with `P = 0` and `S < E`.

**Step.** The Worker opens `min(E - S, max(1, S))` new streams. That doubles `S`, so the growth is geometric.

**Cooldown.** No new batch starts while `P > 0`. Growth therefore runs at the speed of one registration round trip, with no timer constant to tune. A batch is complete when each of its streams has been acknowledged or has failed.

**Continuation.** When the last stream of a batch registers and the trigger condition already holds again, the Worker grows at once. A new stream becomes idle on registration and wakes a waiter or starts a Claim (`worker_protocol.rs:149`; `stream_registry.rs:196-199`). Under in-memory demand, growth therefore repeats until supply exceeds demand or `S = E`. Under durable backlog alone, growth stops once the streams outnumber the Claims that can run at once. Hypothesis, to be measured by the `burst` benchmark.

**Thresholds considered.**
- A low-water mark, such as growing when `I < S/4`, grows before tasks have to wait. It also keeps more idle streams.
- The "freed slot received a task within T ms" signal duplicates the trigger above (see [Option A](#option-a-sdk-only-adaptive-streams-from-local-signals)). Its T would have to cover a commit for SYNC claims, so it is not used.

The trigger and the step are pending decision D5.

**Opening a stream.**
- A new stream runs the same registration as today.
- Before the first acknowledgement of a Worker's life, it opens one Step-and-RPC stream and the `R` RPC-only streams. It reads the cap from that acknowledgement, then opens up to `L`. The on-hold branch uses the same pattern.
- Java, TypeScript, and Rust must turn their one-shot stream creation into an incremental reconcile, as Go and Python already have.

### Shrink rule

**Window.** The Worker tracks `peakBusy`, the highest `B` seen during the shrink window `W`, proposed as 30 s. A growth batch restarts the window, so the Worker never shrinks right after it grows.

**Target.** At the end of a window, `target = max(L, peakBusy + headroom)`, where `headroom = max(1, ceil(peakBusy / 4))`.

**Step.** When `S > target`, the Worker closes `ceil((S - target) / 2)` idle streams. A Worker at rest therefore decays to its floor in about `log2(E - L)` windows: about 7 windows, or 3.5 minutes, from 100 to 4.

**Hysteresis.** Growth fires at `I = 0`. Shrink keeps 25% headroom above the window's peak. So a load that varies within 25% of its peak neither grows nor shrinks. Closing half the surplus per window damps the response further.

**Which streams.** The Worker closes the idle streams that became idle most recently. The server hands work to the longest-idle stream first (`stream_registry.rs:55-65`; `worker_registry.rs:800`). The newest idle streams are the least likely to be picked during the close, which keeps the `NOT_DELIVERED` race rare.

**How.** The existing clean half-close of an idle stream, as on stop:
1. The Worker marks the stream closing.
2. It drops any task that arrives after that decision, sending no frame for it (`protos/dex.proto:189-196`).
3. It keeps reading until the server ends the stream.

A dropped Step returns as `NOT_DELIVERED` with the same attempt number and no retry spent (`worker_streams.rs:118-148`). An RPC handles a half-close in one of two ways:
- If the frame was not sent, the RPC dispatch keeps searching (`worker_registry.rs:499-505`).
- If the half-close arrives before any output, the RPC dispatch excludes that stream and keeps searching until its dispatch wait ends (`worker_registry.rs:564-568, 661-664`).

**Safety rules.**
- Never close a busy stream.
- Never close a registering stream.
- Never close the last idle stream.
- Never go below `L`.
- Never close RPC-only streams.

### Reserved RPC slots

- `R` RPC-only streams stay open from start to stop. They are counted inside `MaxConcurrentTasks` as today.
- They never trigger growth and are never shrunk.
- An RPC that takes a Step-and-RPC stream counts toward `B` like a Step.
- RPC waiters are served before Step waiters (`stream_registry.rs:150-172`), so RPC demand on the shared slots also drives growth.
- An RPC never queues: it waits in memory up to `matching.dispatchWait`. A Worker below its ceiling then grows within one round trip after the RPC takes the last idle stream, well inside that wait.

### Registration failures and the cap

**A growth stream rejected with `RESOURCE_EXHAUSTED`.** This means the per-Worker cap or the node limit was reached (`worker_protocol.rs:98-109`). The rejection is soft. The Worker:
- discards the stream and does not retry it;
- sets the effective ceiling to the streams currently registered until the next shrink window;
- logs once at info level and increments a counter;
- does not record a permanent error in the registration state, change readiness, return `PullRegistrationError` (`docs/reference/server/workers.mdx:35-43`), or enter the 60 s permanent-error retry (`worker_pull.go:552-554` and the other SDKs).

**A rejection of a floor stream or the first stream** keeps today's handling.

**The server lowers the cap.** Every acknowledgement reports the cap. When a later acknowledgement reports a lower cap, the Worker lowers `E` and lets the shrink rule close the surplus. It never resets busy streams. The first-acknowledgement check stays unchanged: an explicit `MaxConcurrentTasks` above the cap fails start. Clamping an unset default is pending decision D4.

### Reconnect, server restart, and failover

**A stream that the server or the network ends.** Today every stream reconnects with backoff (`worker_pull.go:523-541`). With adaptive streams, a reconnect supervisor reopens a lost stream only while `S` is below `max(L, B + 1)`. Above that, it drops the stream. A server-directed `Reconnect` on a busy stream still waits for the task to finish (`worker_protocol.rs:236-244`).

**A whole-server restart or failover.** Every stream breaks. The Worker reopens `L` Step-and-RPC streams and `R` RPC-only streams with today's jittered backoff. The grow rule then takes it to the needed level. The re-registration burst after a failover is therefore the sum of floors, not the sum of ceilings.

**Attempts interrupted by the failover.** They fail as `NOT_COMPLETED`, as today. A handler still running locally keeps its slot counted in `B` until it returns, so `S + local busy ≤ E` holds.

**Never reopen a stream closed on purpose.** The supervisor must not reopen a stream that the Worker closed for shrink or stop. This is the #740 leak class. Tests in each SDK cover it.

### Multi-Worker fairness and mixed fleets

The server picks the longest-idle stream across all Workers of a queue (`stream_registry.rs:55-65`; `worker_registry.rs:800-826`).

- **Without backlog.** Work spreads in proportion to idle streams. A Worker that receives more work runs out of idle streams and grows. A Worker with surplus shrinks. Hypothesis: busy streams converge toward an even split between Workers with equal ceilings. Exact balance is not a goal. Each Worker stays within its own ceiling.
- **Under backlog.** A Worker's freed and new streams start Claims while heads are claimable. The number of Claims that can run at once bounds this (one per partition and queue). Workers grow only while those Claims, together with the waiters, use up their idle streams. A Worker that started earlier holds more streams and may take a larger share. Hypothesis, measured by the `fairness` benchmark.
- **Mixed fleets during a rollout.** Old fixed Workers always hold idle streams, so they attract most of the tasks. New adaptive Workers rarely see "all busy" and stay at their floor. When the old Workers stop, their share moves to the new ones, which ramp from their floors under load. The ramp costs a few round trips (see [Ramp-up latency](#ramp-up-latency)). Graceful stop already half-closes the old Workers' idle streams first. The benchmark plan measures this case.
- **Slow handlers.** A Worker whose handlers slow down, for example from CPU saturation, keeps its streams busy and grows toward its ceiling. Fixed slots already behave this way at the ceiling, so adaptation adds no new risk here. Resource-based throttling is a non-goal.

### Sticky dispatch

A sticky Step waits up to `matching.stickyWait` (5 s) while the sticky Worker has any connected stream, even when all of them are busy (`step_reservation.rs:170-191, 254-259`).

- **Below the ceiling, adaptation bounds that wait.** The hand-out that took the last idle stream triggers growth. The new stream re-idles at registration and wakes the sticky waiter (`worker_protocol.rs:149`; `worker_registry.rs:893-916`). The wait shrinks from up to 5 s to about one registration round trip.
- **At the ceiling, adaptation changes nothing.** The S2 tail case ran at 256 slots per Worker, which was the ceiling. A matching-side change would address it, such as ending the sticky wait early when the sticky Worker has no idle slot and is at its cap. That change is separate work and out of scope here. It is listed in [Pending decisions](#pending-decisions) (D11) for the owner.

### Budgeting `maxConcurrentTasksCap` and `maxStreams`

`matching.maxConcurrentTasksCap` still bounds each Worker's streams on a node. A ceiling above it is reached only up to the cap, and the excess growth is rejected softly. `matching.maxStreams` still bounds the node.

**What changes.** Operators budget streams for real concurrency plus floors, instead of the sum of ceilings. A node limit reached under peak load now costs capacity, not Worker start failures.

**Arithmetic with `maxStreams` = 100,000:**

| Policy per Worker | Idle streams | Idle Workers that fit | With full rolling overlap |
| --- | --- | --- | --- |
| Fixed at today's default, CPU count 8 | 8 | 12,500 | 6,250 |
| Fixed at 100 | 100 | 1,000 | 500 |
| Adaptive, floor 8, ceiling 100 | 8 at rest | 12,500 | 6,250 at rest; peak adds busy streams only |

**Rolling deploys.**
- Old and new Workers overlap. With fixed slots the stream count doubles.
- With adaptive streams, new Workers start at their floor, and old Workers stop by half-closing idle streams first. The overlap costs about the real concurrency plus two floors per Worker pair.
- The peak is still bounded by the sum of ceilings. An operator who must guarantee every Worker its full ceiling still sizes `maxStreams` for that.

**Failover.** Re-registration is the sum of floors, followed by growth.

### Observability

**New SDK surface.** Every SDK adds the same set, pending decision D12:
- An accessor on the pull Worker that returns a snapshot: `open`, `registering`, `busy`, `idle`, `floor`, `effectiveCeiling`, `rpcOnlyOpen`, and the counters below. Today no SDK has pull metrics, so an accessor fits all five SDKs without choosing a metrics library.
- Counters: `streamsGrown`, `streamsShrunk`, `growRejectedAtCap`, and `shrinkRaceDrops`. The last one counts tasks dropped because they arrived after a shrink decision.
- Debug logs for each batch: grow with `from`, `to`, and `reason`, and shrink with `from`, `to`, and `peakBusy`. One info log when the effective ceiling is clamped by the cap or by a rejection.

**Existing server metrics answer the operator questions without a new family:**
- `dexzd_matching_worker_streams` (gauge, basic; `server-zd/crates/dexzd-metrics/src/lib.rs:415-419`) follows real concurrency plus floors.
- `dexzd_step_schedule_to_start_latency_seconds` (histogram, advanced; `lib.rs:490-494`) shows ramp cost. Its buckets are 5 ms, 25 ms, 100 ms, 500 ms, 2.5 s, and 10 s (`server-zd/crates/dexzd-metrics/src/histogram.rs:18`). They cannot separate a 5 s sticky wait from other waits between 2.5 s and 10 s. Finer buckets are pending decision D9.
- `dexzd_worker_stream_events_total{event="closed"}` (verbose; `server-zd/crates/dexzd-metrics/src/worker_stream.rs:18-25`) counts churn. Opens equal closes plus the gauge change.
- `dexzd_step_attempts_returned_total{reason="not_delivered"}` counts shrink races.
- `dexzd_step_task_queue_entries` and `dexzd_step_task_queue_oldest_age_seconds` show backlog during a ramp.

**Phase 1 adds no server metric.** If one is needed later, the rule is a new label value on an existing family, such as `event="registered"`.

### Keeping five SDKs identical

- **One specification.** The rules above are written as one state machine (below). Every SDK implements that machine with the same constants. The constants are SDK-private: only `MinOpenSlots` is public.
- **Same timers.** Every SDK runs one Worker-level timer for the shrink window. Growth has no timer.
- **Same stream selection.** RPC-only streams are distinguished by kind, not by slot number. This removes today's first-N versus last-N difference, which has no effect on the server.
- **Conformance tests.**
  - The Rust `cross_sdk` suite and each SDK's integration suite run the same load script against a real server.
  - They assert the stream count trajectory read from `dexzd_matching_worker_streams` and from the SDK snapshot.
  - They check the trajectory at fixed checkpoints, with tolerance for scheduling jitter.

```text
on task_frame(stream):                      # Step-and-RPC stream
    B += 1; I -= 1
    maybe_grow()

on final_frame_sent(stream):
    B -= 1; I += 1

on registered(stream):
    P -= 1; I += 1
    E = min(MaxConcurrentTasks, reported_cap or inf) - R
    if P == 0: maybe_grow()

on registration_rejected(stream, RESOURCE_EXHAUSTED) and stream is growth:
    P -= 1; S -= 1
    E = registered_count(); ceiling_clamped_until = window_end
    if P == 0: maybe_grow()

maybe_grow():
    if I == 0 and P == 0 and S < E:
        n = min(E - S, max(1, S))
        open n streams; S += n; P += n
        restart_window()

every W:
    target = max(L, peakBusy + max(1, ceil(peakBusy / 4)))
    if S > target:
        close ceil((S - target) / 2) idle streams, newest idle first,
              never the last idle stream, never below L
    peakBusy = B
```

## Risks

### Oscillation

- **A load period shorter than `W`.** The window's peak keeps the streams, so nothing oscillates.
- **A period longer than `W`.** The Worker shrinks during the trough at half the surplus per window and regrows at the next peak.
- **Bound per cycle.** Closes are at most `S - L`, and regrowth is at most `log2(E / L)` rounds. Each close may cost one `NOT_DELIVERED` return, which is rare given the newest-idle selection.
- **A load that varies within 25% of its peak** neither grows nor shrinks.
- **Verification.** The periodic-load benchmark measures this. The tests assert at most one shrink per window.

### #740-class leaks

The #740 bug kept every queue at its historical peak. The invariants that prevent the same class here:

1. `S ≤ E` at all times, and `S ≥ L` after the first registration.
2. Every stream is in exactly one state: registering, idle, busy, or closing. A stream leaves `S` when it starts closing.
3. The reconnect supervisor never reopens a closing stream or a stream closed on purpose.
4. A lost stream is reopened only while `S < max(L, B + 1)`.
5. After load stops, `S` reaches `L` within `ceil(log2(E - L + 1)) + 1` windows.

Each SDK's integration tests check invariants 3 and 5 against a real server. The server is unchanged, so no server-side leak is introduced.

### Ramp-up latency

**Rounds.** From `L` to `E`, growth takes `ceil(log2(E / L))` rounds:
- 4 rounds from 8 to 99 (a ceiling of 100 with one RPC slot);
- 7 rounds from 2 to 255.

**One round.** It is the time from the hand-out that empties `I` until the new streams can take tasks:
- 0.5 RTT for the task frame to reach the Worker;
- 0.5 RTT for the new stream's HEADERS and Register to reach the server;
- registration under the registry lock, then the server hands a waiting task out at once (`worker_protocol.rs:92-149`);
- 0.5 RTT for that task frame to reach the Worker.

**Estimates.** These are hypotheses until the burst benchmark runs. RTT is assumed at 1 ms in a region and 50 ms across regions. Commit latency is assumed at 25 to 50 ms (#821, #1118).

| Case | Round time | Ramp 8 to 99 | Ramp 2 to 255 |
| --- | --- | --- | --- |
| ASYNC Step or RPC, in-memory waiter, same region | about 1.5 RTT ≈ 2 ms | ≈ 8 ms | ≈ 14 ms |
| Same, cross region | ≈ 75 ms | ≈ 0.3 s | ≈ 0.5 s |
| SYNC Step, in-memory waiter: the task frame waits for the `StepAttemptStarted` commit (#1253) | ≈ 1.5 RTT + commit ≈ 30 to 50 ms | ≈ 0.1 to 0.2 s | ≈ 0.2 to 0.35 s |
| Step from durable backlog: Claim to the partition owner, commit, then the frame (`backlog_matching.rs:24-36`) | Claim-bound, not stream-bound: each partition and queue hands out one row per Claim round trip, about 1.5 RTT + commit ≈ 30 to 60 ms | growth happens only while concurrent Claims use up the idle streams | same |

**Side effects.**
- During a SYNC ramp, Steps that miss the 200 ms `syncMatchWait` are enqueued and cost one extra commit each. Once a queue has backlog, new Steps enqueue directly (`step_reservation.rs:162-164`). So a slow ramp moves a burst onto the queued path. The benchmark measures that share.
- A burst no larger than the idle streams sees no ramp. Phase 2 keeps today's default stream count as the floor, so bursts up to the CPU count behave as today.

## Benchmark plan

The scenarios extend `dexbench` in `benchmark/dexbench`:

- add scenario names in `isKnownScenario` (`benchmark/dexbench/cmd/dexbench/main.go:316-323`);
- pass a new `-min-open-slots` flag into the `PullWorkerOptions` in `openConnection` (`benchmark/dexbench/runner.go:113-116`);
- add an open-loop load scheduler beside the closed-loop `runWindow` (`runner.go:373`).

Each point records:
- `/metrics` before and after, as today;
- the series of `dexzd_matching_worker_streams` at 1 s intervals;
- the SDK snapshot of every Worker;
- server RSS and CPU.

| Scenario | Setup | Measures | Answers |
| --- | --- | --- | --- |
| `idle-slots` (#1989 item 1) | Fixed slots, total 10k, 50k, and 100k streams. 256 per Worker within the default cap, so 40, 196, and 391 Workers. F = 1 and F = 16 Flow types. A light probe of 50 Steps/s. Then a server restart and a standby failover. | RSS and CPU per idle stream, and the Ping and Pong rate. Probe schedule-to-start p50 and p99. Time until the stream gauge returns to N. | The cost per idle stream, which sizes the floor and the gain of A over D. Whether C is needed. |
| `burst` | Workers at their floor, `L` = 8, `E` = 99, no load. Then an open-loop burst of 1x, 4x, and 12x `L` per Worker. ASYNC and SYNC. Repeated with fixed slots at 99. | Schedule-to-start p50, p99, and max. Time to reach the needed stream count. Share of Steps on the queued path. `StepAttemptQueued` count. | Ramp cost against the estimates above, and whether B is needed. |
| `fairness` | 8 adaptive Workers with equal ceilings. Open-loop load at 25%, 50%, 90%, and 110% of total capacity. Then 4 fixed and 4 adaptive. Then a rolling replacement of fixed Workers by adaptive ones. | Per-Worker task share and busy streams, which needs per-Worker counters in `dexbench`. Schedule-to-start during the replacement. | Fairness and the mixed-fleet ramp. |
| `periodic` | Square-wave and sine load with periods of 10 s, 60 s, and 5 min, between 10% and 90% of capacity. | Stream count over time. `dexzd_worker_stream_events_total{event="closed"}` rate. `dexzd_step_attempts_returned_total{reason="not_delivered"}`. p99. | Oscillation and shrink-race cost. |
| S2 at C=4,096, 16 connections | Fixed against adaptive, at 256 per Worker. | p99 and the schedule-to-start buckets. | Confirms that adaptation does not fix the at-ceiling sticky tail. |

Results go under `benchmark/results/` with the server tunables, Worker options, commit, and hardware of each point.

## Rollout and API changes

**Phase 0: measure.** Add `idle-slots` to `dexbench` and commit results (#1989 item 1). The results inform D2 and D8. No product change.

**Phase 1: opt-in adaptive streams, an SDK patch release in all five SDKs.**
- Add `MinOpenSlots`, an optional field that defaults to `MaxConcurrentTasks - R`. With that default, behavior is identical to today. A lower value enables adaptation.
- Make stream creation incremental in Java, TypeScript, and Rust.
- Implement the state machine, soft growth rejection, the reconnect rules, the snapshot accessor, and the logs.
- Doc comments carry "Since 1.NN.P" with the next patch version.
- `MaxConcurrentTasks` keeps its meaning and default. The deprecated idle-streams-per-queue options stay no-ops. This is an additive change, so it fits a patch release under the repository's release rules.

**Phase 2: adaptive by default, together with the new default ceiling.**
- The floor default becomes `min(MaxConcurrentTasks - R, max(1, CPU count - R))`.
- The `MaxConcurrentTasks` default becomes 100, and an unset default is clamped to the server cap.
- These two changes ship in the same SDK release, after phase 0 and the phase 1 benchmarks.
- Changing defaults is a behavior change under Beta compatibility, so it needs the owner's explicit authorization (D2, D3).
- Migration: an application that needs every slot open at all times sets `MinOpenSlots` equal to its slot count.

**The default of 100 relative to adaptive streams:**
- *Before:* each idle Worker holds 100 fixed streams, so 1,000 Workers fill `maxStreams`, and 500 with rolling overlap. This document recommends against it.
- *With:* the floor keeps idle cost at today's level, and the higher ceiling serves bursts. Recommended.
- *After:* safe, but the higher ceiling waits a release.

See D3.

**Phase 3, optional: Option B.**
- The `schedule_to_start` field on `PollWorkerTask`, a server release, and SDK use of the field.
- Only if the `burst` or `fairness` results show a need, or if per-Worker schedule-to-start is wanted.

**Not planned: Option C.** Reconsider it only if `idle-slots` shows that busy streams limit a node.

## Pending decisions

Nothing here is decided. Each row has a recommendation for the owner.

| # | Decision | Scenario | Options | Cost | Recommendation |
| --- | --- | --- | --- | --- | --- |
| D1 | Which option | Server cost should follow real concurrency | A; A plus B; C; D | A: SDK work only. B: plus a protocol field and a server release. C: protocol and server rework of #1253 paths. D: none, but no headroom for a higher default | A now; B later if benchmarks need it; C only on evidence |
| D2 | Opt-in or default-on, and the default floor | Existing Workers set or inherit a slot count | Opt-in first (floor = ceiling), then default floor = CPU count; default-on at once; always opt-in | Opt-in delays the server saving. Default-on changes burst behavior for explicit large slot counts without notice | Opt-in in phase 1. Default floor `min(ceiling, CPU count)` in phase 2 with owner authorization |
| D3 | When the default ceiling moves from CPU count to 100 | Raising the default without raising server cost | Before, with, or after adaptive default-on | Before: 1,000 idle Workers fill `maxStreams`. With: one coordinated release. After: the higher ceiling waits a release | With phase 2; never before |
| D4 | A ceiling above the server cap | Default 100 against a server with a lower cap | Fail start (today); clamp unset defaults only; clamp all values with a warning | Fail: breaks Workers on low-cap servers when the default rises. Clamp all: hides a misconfiguration | Clamp unset defaults with an info log; keep failing for explicit values |
| D5 | Grow trigger and step | A burst arrives at a Worker at its floor | `I = 0` and doubling; a low-water mark (`I < S/4`); 1.5x step; linear +1 | Low-water: more idle streams. 1.5x: about 1.7 times the rounds. Linear: E rounds | `I = 0`, doubling, one batch in flight |
| D6 | Shrink window and step | Load drops or cycles | `W` of 30 s, 60 s, or 5 min; close half the surplus, or all of it; headroom of 25% or a fixed count | A short `W` and closing everything cause churn. A long `W` delays the saving | `W` = 30 s, half the surplus, 25% headroom (minimum 1) |
| D7 | Public option name and shape | Users opt in or pin fixed slots | `MinOpenSlots` integer; `MinIdleSlots`; a mode enum `Fixed` or `Adaptive` | An enum cannot express a custom floor. "Idle" is wrong after growth | `MinOpenSlots` integer, the same idiom in each SDK |
| D8 | Server hint (Option B) now or later | The SDK cannot see queue age | Ship `schedule_to_start` now; later on evidence; never | Now: a server release and five SDK changes before the data | Later, decided from the `burst` and `fairness` results |
| D9 | Finer schedule-to-start buckets | 2.5 s to 10 s hides the 5 s sticky wait | Add buckets such as 1 s and 5 s; keep them | Changing the buckets of a released family changes its series and dashboards | Keep them in phase 1. Revisit with the S2 investigation |
| D10 | Adaptive reserved RPC slots | RPC-only slots sit idle without RPCs | Keep fixed; make them adaptive too | Adaptive RPC slots defeat their purpose: RPC latency isolation | Keep fixed |
| D11 | The at-ceiling sticky wait | S2 tail at 256 slots per Worker | Separate matching change, such as ending the sticky wait when the sticky Worker is at its cap with no idle slot; leave as is | A matching change to sticky semantics, outside this design | Track it as a separate server-zd investigation |
| D12 | SDK observability surface | Operators need open, busy, and idle per Worker | Snapshot accessor plus logs; native metrics per SDK; logs only | Native metrics need a metrics library choice per SDK | Snapshot accessor plus debug logs; native metrics later |
| D13 | TypeScript readiness difference | TypeScript is ready after one slot of any kind; the others wait for each kind | Align TypeScript in phase 1; leave it | Aligning it changes TypeScript readiness timing | Align it in phase 1, since phase 1 already reworks TypeScript stream creation |

### D1: Which option

The owner's goal is that server cost follows real concurrency, so the slot ceiling can be high.
- **A** reaches that goal with no protocol or server change. The server already tells the Worker the moment its last idle slot is taken.
- **B** adds what A cannot see, queue age.
  - A grows whenever in-memory waiters use up the idle streams.
  - Durable backlog drains one Claim per partition and queue at a time, so extra streams would not drain it faster. A not growing there loses little. This is a hypothesis for the `burst` benchmark.
  - So B's main gains are faster growth and per-Worker schedule-to-start.
- **C** removes even the cost of busy streams. It does so by changing hand-out, half-close, and reset handling, which #1253 made correct. A reset would then fail every running attempt on the stream as `NOT_COMPLETED` and spend their retries.
- **D** keeps fixed cost. A default of 100 would then limit a node to 1,000 idle Workers.

Recommendation: A, with B and C held behind benchmark evidence.

### D2: Opt-in or default-on, and the default floor

Today a Worker holds `MaxConcurrentTasks` streams at all times.
- If adaptation were on by default at once, a Worker that sets 256 slots would start at a lower floor. Bursts would then see a ramp of a few round trips.
- Opt-in in phase 1 keeps every existing Worker identical: the default floor equals the ceiling.
- In phase 2, a default floor of the CPU count keeps a Worker with today's default exactly as it is now. It only stops idle cost from growing with a higher ceiling.

Changing defaults needs the owner's explicit authorization under Beta compatibility.

Recommendation: opt-in now, then the CPU-count floor default in phase 2.

### D3: When the default ceiling moves from CPU count to 100

The default of 100 is planned. With fixed slots, every Worker would hold 100 streams while idle:
- 1,000 Workers reach `matching.maxStreams` = 100,000;
- during a rolling deploy's overlap, 500 Workers reach it;
- a failover re-registers all of them at once.

With adaptive streams and a CPU-count floor, idle cost stays at today's level.

The options:
- shipping 100 before adaptive streams takes on the fixed cost;
- shipping it with phase 2 makes one release carry both default changes;
- shipping it after phase 2 is safe but later.

Recommendation: with phase 2, never before.

### D4: A ceiling above the server cap

Today, a first acknowledgement whose cap is below `MaxConcurrentTasks` fails start. A later lower cap only warns.
- With a default of 100 and an operator's cap below 100, failing start would break Workers that set nothing.
- Clamping an unset default, as the on-hold branch does, avoids that. It still reports an explicit misconfiguration.
- Clamping every value also hides a deliberate setting.

Recommendation: clamp unset defaults with an info log, and keep failing start for explicit values.

### D5: Grow trigger and step

A Worker at its floor receives a burst.
- `I = 0` with doubling and one batch in flight reaches the ceiling in `log2(E / L)` round trips. It needs no timer constant.
- A low-water mark grows earlier but keeps about a quarter of the streams idle.
- A 1.5x step overshoots less but needs about 1.7 times the rounds.
- Linear growth needs E rounds and is too slow for bursts.

Recommendation: `I = 0`, doubling, one batch in flight. Confirm with the `burst` and `periodic` benchmarks.

### D6: Shrink window and step

After load drops, surplus streams should close without churn under cycling load.
- A 30 s window with half the surplus closed and 25% headroom reaches the floor in a few minutes.
- A shorter window, or closing all the surplus, reacts faster but churns streams under periodic load. Each close can cost a `NOT_DELIVERED` return.
- A longer window delays the saving.

Recommendation: 30 s, half the surplus, 25% headroom with a minimum of 1. Confirm with the `periodic` benchmark.

### D7: Public option name and shape

Users need to opt in during phase 1, and to pin fixed slots after phase 2.
- An integer floor does both.
- A mode enum cannot express a custom floor.
- "Idle" is wrong for the floor, because the floor counts streams that may be busy.

Proposed names: `MinOpenSlots` in Go, `min_open_slots` in Python and Rust, and `minOpenSlots` in Java and TypeScript. Validation: `1 ≤ MinOpenSlots ≤ MaxConcurrentTasks - ReservedRPCTasks`. As with the slot count, zero means the default, except in Rust, which uses `Option`.

Recommendation: the `MinOpenSlots` integer.

### D8: Server hint now or later

The SDK cannot compute schedule-to-start. Task frames carry no schedule time (`protos/dex.proto:469-484`).
- A `schedule_to_start` Duration on `PollWorkerTask` would let the Worker grow on queue age and report it.
- It needs a server release and five SDK changes before data shows A is insufficient.

Recommendation: decide after the `burst` and `fairness` results.

### D9: Finer schedule-to-start buckets

The 2.5 s to 10 s bucket of `dexzd_step_schedule_to_start_latency_seconds` cannot separate the 5 s sticky wait from other waits. Changing the buckets of a released family changes its series.

Recommendation: keep them in phase 1. Revisit them with D11's investigation.

### D10: Adaptive reserved RPC slots

RPC-only slots exist so that RPCs still run while Steps hold every other slot. Adapting them would bring back the ramp delay for RPCs, which never queue. There are only a few of them, one by default.

Recommendation: keep them fixed.

### D11: The at-ceiling sticky wait

The S2 tail at 256 slots per Worker fits the stickyWait hypothesis: a Step waits for a sticky Worker whose slots are all busy, at its ceiling. Adaptive streams cannot help at the ceiling. A matching change could, such as ending the sticky wait when the sticky Worker has no idle slot and is at its cap. That changes sticky semantics and is outside this design.

Recommendation: a separate server-zd investigation. The owner decides whether to track it.

### D12: SDK observability surface

No SDK has pull Worker metrics today. Operators need open, busy, idle, and ceiling per Worker, plus grow and shrink counts.
- A snapshot accessor and debug logs fit all five SDKs the same way.
- Native metrics need a metrics library choice per SDK.

Recommendation: a snapshot accessor plus debug logs now.

### D13: TypeScript readiness difference

TypeScript reports ready after the first registered slot of any kind (`sdk-typescript/src/pull-worker.ts:218-223`). The other SDKs wait for one of each slot kind. Phase 1 reworks TypeScript stream creation anyway, and identical behavior is a requirement. Aligning it changes TypeScript readiness timing slightly.

Recommendation: align it in phase 1 and note it in the TypeScript change log.

## Tests

The repository default is integration and E2E tests against a real server. Each scenario runs in the five SDK integration suites:
- Go pull integration and e2e;
- Python integration;
- Java integration;
- TypeScript integration;
- Rust `integ`.

The Rust `cross_sdk` suite checks that the five SDKs produce the same trajectories. The server is unchanged in phases 1 and 2. Its existing suites, `server-zd/crates/dexzd-worker-dispatch/tests/reserved_slots.rs` and `tests/pull_worker.rs`, keep covering the half-close and hand-out paths. A test reads the stream count from `dexzd_matching_worker_streams` on the server metrics endpoint. The SDK tests need a test-only setting for a short shrink window, such as 1 s, named with the `_for_test` or equivalent convention of each SDK.

1. **Fixed mode is unchanged.** With the floor equal to the ceiling, the stream count stays at the ceiling through load and idle periods. This guards requirement 8 and the phase 1 default.
2. **Idle Worker at the floor.** An adaptive Worker holds exactly `L + R` streams after start, and readiness holds.
3. **Burst growth.** A burst of `N` concurrent long-running ASYNC Steps, with `L < N ≤ E`, grows streams to at least `N` and at most `min(E, 2N)`. A burst of `N` concurrent RPCs gives the same result. Every task completes, and no Step spends a retry. This covers the grow rule and the overshoot bound. A SYNC variant checks only that streams grow above `L` and stay at most `min(E, 2N)`. Some SYNC Steps may take the Claim-bound backlog path.
4. **Return to the floor.** After load stops, the count reaches `L + R` within the bound of invariant 5. It does not rise again over three more windows, which guards against the #740 leak.
5. **Shrink race.** Steps are dispatched continuously while the Worker shrinks. Every Step completes with attempt number 1 and no retry spent. Any race appears as `dexzd_step_attempts_returned_total{reason="not_delivered"}`. This guards requirement 3.
6. **Soft rejection at the cap.** With `matching.maxConcurrentTasksCap` below the ceiling, growth stops at the cap. Readiness stays true. No `PullRegistrationError` appears. Registration attempts stay bounded, with no tight loop.
7. **Cap lowered on reconnect.** After a server restart with a lower cap, the Worker keeps running and converges to the new cap. Busy tasks finish.
8. **Failover reopens the floor.** With the standby failover harness (`server-zd/crates/dexzd-server/tests/serve_process/standby_failover/`), the Worker re-registers `L + R` streams, not `E + R`. It regrows under the queued backlog.
9. **Reserved RPC slots stay fixed.** RPC-only streams stay at `R` through growth and shrink. An RPC succeeds while every Step slot is busy.
10. **Bounded sticky wait below the ceiling.** A sequential Step chain on a Worker whose open slots are all busy, below its ceiling, shows schedule-to-start well under 1 s. Today it can wait up to the 5 s `stickyWait`.
11. **A stream closed on purpose is not reopened.** After a shrink, the closed streams stay closed for longer than two maximum reconnect backoffs.
12. **Graceful stop with adaptive streams.** Stop during growth half-closes idle and registering streams, lets busy ones finish, and returns undelivered tasks without spending a retry.
13. **Mixed fleet E2E.** A fixed Worker and an adaptive Worker serve one queue. Stopping the fixed one moves its load to the adaptive one, which grows. No Step fails. This uses the server-zd E2E harness (`server-zd/crates/dexzd-e2e-tests`).

## Documentation

- **Developer reference.** `docs/reference/server/workers.mdx`, Capacity and shutdown, lines 248-269: replace "The stream count equals the task limit and does not change with load" with the floor and ceiling model, the soft cap behavior, and the budgeting guidance. Also update the `matching.maxStreams` guidance at line 399.
- **SDK references.** The five pages `docs/reference/{go,python,java,typescript,rust}/workers.mdx`: the new option, its defaults, and the snapshot accessor, each with "Since 1.NN.P".
- **SDK doc comments.** `MinOpenSlots` and the snapshot accessor in each SDK, with a complete example and version markers. The `MaxConcurrentTasks` comments change from "one stream per slot" to "the most concurrent tasks; streams vary between the floor and this value".
- **Change logs.** One fragment per SDK target (`go`, `python`, `java`, `typescript`, `rust`), section Added in phase 1 and Changed in phase 2. A `server` fragment only if Option B ships.
- **Stable layer.** This changes a Component's behavior, so docs and skills change in the same pull request:
  - `docs/content/components/application.mdx:446` and its zh-Hans copy say "A Worker opens one stream for each task slot";
  - `skills/dex-sdk/references/core/application.md:63` says the same;
  - the skills change needs a fragment and the owner's confirmation of its bump level;
  - `skills/doc-map.yaml` keeps both sides linked.
- **Contributor docs.** Update the stale stream formula in `docs/design/worker-pull-mode.md:7, 19`. Update `benchmark/README.md` for the new scenarios.
- **Agents reference.** `docs/reference/agents/workers.mdx:33-35` explains the 256 cap. Update it if agents Workers inherit the new defaults.

## UI/UX

N/A: no Dex Web change. The stream counts are visible through server metrics and the SDK snapshot.
