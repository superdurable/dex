# Adaptive pull Worker slot streams

- **Status:** accepted, with all decisions made. The owner decided D1 to D13 and the review follow-ups F3 to F6 in [Decisions](#decisions) on 2026-11-17. F5 supersedes F1 and F2.
- **Tracking issue:** [#1989](https://github.com/superdurable/dex/issues/1989) and its 2026-11-17 comment.
- **Code read:** `origin/main` at `35ee8dcdccf64fb9db38d8b0cc0a7aacefa29d4f` (2026-11-17). Every `path:line` below refers to that commit.
- **Scope:** design only. It proposes no product code in this change.

## Summary

Today a pull Worker opens one reserved-slot stream per task slot at start. The stream count stays at `MaxConcurrentTasks` whether the Worker is idle or busy. This document proposes that each SDK vary the number of open Step-and-RPC slot streams between a floor and the `MaxConcurrentTasks` ceiling:

- The Worker keeps at least one idle Step-and-RPC stream while it is below the ceiling. When a hand-out takes its last idle stream, it doubles its open stream count, with one growth batch in flight at a time.
- Every shrink window, the Worker closes half of the idle surplus above the window's peak busy count plus 25% headroom. It closes with the existing clean half-close.
- Reserved RPC-only slots stay fixed.

The owner chose Option A: SDK-only, with no protocol or server change. It keeps the #1253 attempt semantics unchanged. The server already tells the Worker what it needs to know: a hand-out that takes the last idle stream. Option B, a server hint, is a later, optional addition. Option C, multiplexing slots over a few streams, removes more server cost. It does so by rewriting the hand-out, half-close, and failure semantics of #1253, so it is held back until a benchmark shows that busy streams, not idle ones, limit the server.

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
- **Backlog Claims.**
  - Matching keeps one head per partition and queue. A head is claimable only while no Claim of that partition and queue is in flight (`server-zd/crates/dexzd-worker-dispatch/src/task_backlog.rs:87-95, 241-260`).
  - A Claim takes an idle stream and holds it while the partition owner answers (`backlog_matching.rs:497-531`). The owner's reply names the next row (`task_backlog.rs:47-55`).
  - The reply ends the Claim and makes the next row claimable. Matching then starts the next Claim at once on another idle stream (`task_backlog.rs:265-287`; `backlog_matching.rs:228-242`). The task the first Claim started keeps running on its stream.
  - So one partition and queue limits the *start rate* to one task per Claim round trip, not the number of running tasks. By Little's law, a single partition and queue keeps about `handler duration / Claim round trip` tasks running. Short handlers keep few streams busy. Long handlers fill as many streams as the Worker has.
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

The unmerged branch `sdk/worker-slot-defaults` (commit `b782e79c3`, WIP) changes the default to 100 in Go, Python, and Java. Before the first acknowledgement it opens one stream. It clamps an unset default to the server cap with an info log. Decision D4 raises that log to WARN. That branch is on hold. Today's default stays the CPU count.

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
- **Durable backlog.** Matching keeps at most one Claim in flight per partition and queue, but started tasks keep running (see [Background](#the-reserved-slot-protocol-today)). The busy streams a backlog needs are about the Claim rate times the handler duration.
  - With long handlers, every Claim adds one busy stream, so the backlog uses up the idle streams and the Worker grows.
  - With handlers shorter than a Claim round trip, few streams are busy at a time. Backlog can then sit while the Worker still has idle streams, and the Worker does not grow. That outcome is right in this case, because the Claim rate limits the drain and more streams would not drain it faster.
  - The single-partition `backlog-one-partition` benchmark checks both cases.
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

**Decision (D1):** Option A now. Keep B as an optional later phase if the burst benchmark shows A's ramp or overshoot is a problem, or if per-Worker schedule-to-start is wanted. Revisit C only if the idle-stream benchmark shows that busy streams, not idle ones, limit a node.

## Design (Option A)

### Configuration shape

`MaxConcurrentTasks` is the ceiling and `MinOpenSlots` is the floor. Both count all slot streams, including reserved RPC slots. A Worker is adaptive when its floor is below its ceiling, and fixed when they are equal.

```go
// Fixed: every slot keeps a stream. This is the phase 1 default.
dex.PullWorkerOptions{MaxConcurrentTasks: 64}

// Adaptive: between 8 and 64 open slot streams.
dex.PullWorkerOptions{MaxConcurrentTasks: 64, MinOpenSlots: 8}

// Phase 2: the default floor is min(ceiling, CPU count), so this Worker is adaptive.
dex.PullWorkerOptions{MaxConcurrentTasks: 64}

// Phase 2: setting the floor equal to the ceiling keeps it fixed.
dex.PullWorkerOptions{MaxConcurrentTasks: 64, MinOpenSlots: 64}
```

### Terms

Per Worker, for Step-and-RPC slot streams only:

- `T`, the total ceiling. The reported cap is `max_concurrent_tasks_per_app_worker`, where zero means none.
  - In fixed mode, `T` is an explicit `MaxConcurrentTasks`, or an unset default clamped to the reported cap (D4).
  - In adaptive mode, `T` is `MaxConcurrentTasks`, explicit or default, clamped to the reported cap (F6).
- `C`, the total capacity: `min(T, reported cap)`, from the last acknowledgement. It counts Step-and-RPC and RPC-only slots.
- `R`, the configured reserved RPC slot count, derived from `T` (see [Floor and ceiling defaults](#floor-and-ceiling-defaults)). `R_eff` is the RPC-only stream count the Worker may hold: `R`, or `max(0, C - 1)` when `R + 1` exceeds `C` (F5).
- `Sr` and `Hr`, the open RPC-only streams and the orphaned RPC-only handlers.
- `E`, the hard Step-and-RPC capacity: `max(0, C - max(R_eff, Sr + Hr))`. RPC-only handlers above `R_eff`, busy or orphaned, take Step capacity until they return. A soft rejection never lowers `E`.
- `G`, the soft growth target: `E`, or lower after a growth batch with a soft rejection (see [Registration failures](#registration-failures-and-the-cap)). `G ≤ E` always holds, and growth never goes above `G`.
- `L`, the Step-and-RPC floor: `MinOpenSlots - R`. `L_eff = min(L, E)` is the effective floor. Both `E` and `L_eff` are never negative.
- `S`, the open Step-and-RPC streams: registered or registering, and not closing.
- `P`, the registering streams: opened, with no acknowledgement yet.
- `B`, the busy streams: a task frame received and no final frame sent yet.
- `I`, the idle streams: `S - P - B`.
- `H`, the orphaned handlers: Step-and-RPC handlers still running locally after their stream ended, such as after a disconnect or failover. A slot is free only when its handler has returned.

The capacity invariant is `S + H + Sr + Hr ≤ C`, and so `S + H ≤ E`. A handler that runs without a stream still uses a slot, so the Worker never opens a stream for it.

The invariant has one transient exception (F5). An acknowledgement may reveal that the cap dropped. Local usage may then exceed the new `C`, but only through handlers and streams that started before the Worker learned the new cap.
- While usage exceeds `C`, the Worker opens no new stream. It computes how many streams it may open as `max(0, …)`, never a negative count.
- Usage converges as handlers return and surplus streams half-close: RPC-only surplus first, then Step-and-RPC streams above `C - R_eff`.

The rules below apply to Step-and-RPC streams unless they name RPC-only streams. RPC-only streams stay at `R_eff`. An orphaned RPC-only handler blocks the reopening of its RPC-only stream the same way.

### Floor and ceiling defaults

- **Ceiling.** `MaxConcurrentTasks` keeps its meaning and its current default, the CPU count. Phase 2 changes the default to 100 (see [Rollout](#rollout-and-api-changes)).
- **Clamping the ceiling.** The first acknowledgement reports the cap.
  - **Fixed mode:** an explicit `MaxConcurrentTasks` above the cap fails start, as today. An unset default above the cap is clamped with a WARN log (D4).
  - **Adaptive mode:** `MaxConcurrentTasks` above the cap is clamped to the cap with a WARN log that names both values, whether it is explicit or the default (F6). In adaptive mode the ceiling is only a growth limit, and users often set it high to mean "as many as allowed".
- **Defaults are derived after the clamp.** `matching.maxConcurrentTasksCap` may be as low as 1 (`server-zd/crates/dexzd-server/src/config/validation.rs:571-579`), and the cap counts RPC-only streams. So when the ceiling is clamped, the Worker computes the defaults from the clamped `T`, not from the unclamped value:
  - `R` defaults to 1 when `T ≥ 2`, else 0. This is today's rule (`sdk-go/dex/worker_pull.go:116-123`) applied to `T`.
  - `MinOpenSlots` defaults from `T`, as below.
  - An explicit `ReservedRPCTasks` with `R ≥ T` fails start with an error that names `R`, `T`, and the cap. An explicit `MinOpenSlots` above `T` fails start the same way.
  - Example, fixed mode with an unset ceiling: cap 1 with the default ceiling of 100 gives `T` = 1, `R` = 0, `E` = 1, and `L` = 1. The Worker runs one Step-and-RPC stream and logs the clamp at WARN.
- **Floor.** A new option, `MinOpenSlots` (decision D7). It counts all slot streams, like `MaxConcurrentTasks`, and must satisfy `R + 1 ≤ MinOpenSlots ≤ MaxConcurrentTasks`. The Step-and-RPC floor is `L = MinOpenSlots - R`.
  - **Phase 1 default:** `MaxConcurrentTasks`, so the Worker stays fixed and identical to today. Adaptation is opt-in by setting a lower floor.
  - **Phase 2 default:** `min(T, max(R + 1, CPU count))`. On a machine with at least 2 CPUs, a Worker with the default ceiling then still holds exactly today's default stream count while idle. Only the capacity above the CPU count adapts. A user who sets only `MaxConcurrentTasks` becomes adaptive. Setting `MinOpenSlots` equal to `MaxConcurrentTasks` keeps the Worker fixed.
  - With 1 CPU, today's Worker holds 1 stream and no RPC slot. With a ceiling of 100, `R` becomes 1, so the Worker holds 2 streams.
- **Why the floor is at least 1:**
  - Readiness needs one registered Step-and-RPC stream (`docs/reference/server/workers.mdx:20-26`).
  - The declined-type cache entries of an app Worker are dropped when its last stream disconnects (`worker_registry.rs:941-950`). A Worker that scaled to zero would silently reset its exclusions.
  - The sticky search ends at once when the sticky queue has no streams (`step_reservation.rs:257`; `worker_registry.rs:956-958`). A Worker at zero would also lose sticky dispatch.

### Grow rule

**Trigger.** A task frame arrives on a Step-and-RPC stream. It leaves `I = 0` with `P = 0` and `S + H < G`, where `G ≤ E`.

**Step.** The Worker opens `min(G - S - H, max(1, S))` new streams. That doubles `S`, so the growth is geometric.

**Cooldown.** No new batch starts while `P > 0`. Growth therefore runs at the speed of one registration round trip, with no timer constant to tune. A batch is complete when each of its streams has been acknowledged or has failed.

**Continuation.** When the last stream of a batch registers and the trigger condition already holds again, the Worker grows at once. A new stream becomes idle on registration and wakes a waiter or starts a Claim (`worker_protocol.rs:149`; `stream_registry.rs:196-199`). Under in-memory demand, growth therefore repeats until supply exceeds demand or `S = E`. Under durable backlog, growth repeats while started tasks hold streams longer than the Claims take to refill them, that is, until `S` exceeds about `Claim rate × handler duration` or reaches `E`. Hypothesis, measured by the `burst` and `backlog-one-partition` benchmarks.

**Thresholds considered.**
- A low-water mark, such as growing when `I < S/4`, grows before tasks have to wait. It also keeps more idle streams.
- The "freed slot received a task within T ms" signal duplicates the trigger above (see [Option A](#option-a-sdk-only-adaptive-streams-from-local-signals)). Its T would have to cover a commit for SYNC claims, so it is not used.

The trigger and the step are decision D5.

**Opening a stream.**
- A new stream runs the same registration as today.
- **Fixed mode with an explicit `MaxConcurrentTasks`:** at start, the Worker opens all `R + L` streams in parallel, as today (`sdk-go/dex/worker_pull.go:314-334`; `sdk-java/src/main/java/io/superdurable/dex/PullWorker.java:454-463`). An explicit ceiling above the cap still fails at the first acknowledgement, so it needs no probe. This keeps phase 1 startup and burst behavior identical to today.
- **Fixed mode with an unset ceiling that may be clamped** (from phase 2): the Worker first opens one Step-and-RPC stream and waits for its acknowledgement (F4). It computes `T`, `R`, `E`, and `L` from the reported cap, then opens the `R` RPC-only streams and the other `L - 1` Step-and-RPC streams in parallel. This adds one round trip to startup only in this case, and it keeps a cap of 1 from rejecting the RPC-only stream. The on-hold branch uses the same probe.
- **Adaptive mode, with the ceiling set or not:** the Worker starts at the floor. It opens its `L` Step-and-RPC floor streams in parallel. It opens the RPC-only streams after the first acknowledgement, with `R` derived from the clamped `T`. Step-and-RPC capacity comes first, as in F5, so a cap of 1 leaves the one slot to a Step-and-RPC stream. The ceiling is clamped at that acknowledgement (F6) and only limits growth, so adaptive mode needs no cap probe.
  - When the cap is below `L`, the extra floor streams get `RESOURCE_EXHAUSTED`. The Worker keeps running on the registered ones, because `L_eff = min(L, E)` and another stream of the kind is registered. That is today's rule for a rejection while another stream of the same kind stays registered (`docs/reference/server/workers.mdx:64-68`). The rejection is recorded as today and not retried, because the stream is above `L_eff`. With `StopOnPermanentStreamRejection` set, a rejection that arrives before any acknowledgement can still stop start, as it does today when the slot count exceeds the cap.
- Java, TypeScript, and Rust must turn their one-shot stream creation into an incremental reconcile, as Go and Python already have.
- TypeScript readiness changes to one registered stream of each slot kind, as decision D13 authorizes (`sdk-typescript/src/pull-worker.ts:218-223`). It is the only phase 1 behavior change in fixed mode, and the TypeScript change log records it.

### Shrink rule

**Window.** The Worker tracks `peakBusy`, the highest `B` seen during the shrink window `W` of 30 s (decision D6). A growth batch restarts the window, so the Worker never shrinks right after it grows.

**Target.** At the end of a window, `target = min(E, max(L_eff, peakBusy + headroom))`, where `headroom = max(1, ceil(peakBusy / 4))`.

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
- Never close the last idle stream, unless `S + H > C - R_eff` after a cap drop.
- Never go below `L_eff`.
- Never close RPC-only streams, except when a lowered cap no longer fits them (see [Registration failures](#registration-failures-and-the-cap)).

**Busy streams above the target.** After a lowered cap, a busy stream of a kind that is in surplus half-closes right after its final frame instead of staying idle. RPC-only streams are in surplus when `Sr + Hr > R_eff`. Step-and-RPC streams are in surplus when `S + H > C - R_eff`. The protocol allows this, because the final output comes before the half-close (`protos/dex.proto:195`). A task the server sends in between returns as `NOT_DELIVERED`. So usage converges to the new cap as busy streams finish, without any reset.

### Reserved RPC slots

- `R_eff` RPC-only streams stay open from start to stop. That is `R` unless a cap drop lowers it (F5). They are counted inside `MaxConcurrentTasks` as today.
- They never trigger growth and are never shrunk.
- An RPC that takes a Step-and-RPC stream counts toward `B` like a Step.
- RPC waiters are served before Step waiters (`stream_registry.rs:150-172`), so RPC demand on the shared slots also drives growth.
- An RPC never queues: it waits in memory up to `matching.dispatchWait`. A Worker below its ceiling then grows within one round trip after the RPC takes the last idle stream, well inside that wait.

### Registration failures and the cap

**A growth stream rejected with `RESOURCE_EXHAUSTED`.** This means the per-Worker cap or the node limit was reached (`worker_protocol.rs:98-109`). A growth batch sends several Registers at once. The server checks each one against the streams registered at that moment (`worker_protocol.rs:98-149`), so some streams of a batch may be accepted after another one is rejected. The rejection is soft. The Worker:
- discards the rejected stream, removes it from `S` and `P`, and does not retry it at once;
- keeps the hard capacity `E` unchanged, because a soft rejection says nothing about the Worker's own slots;
- settles the batch only when the whole batch is done (`P = 0`). If any stream of the batch was rejected, it sets the soft growth target `G = S + H` from the streams that did register, and marks the target clamped until the end of the current shrink window;
- at the end of that window, restores `G = E` and calls the grow check again;
- makes at most one growth batch per window while rejections continue, so retries stay bounded at one batch per 30 s (F3);
- logs once at info level per clamped window and increments a counter;
- does not record a permanent error in the registration state, change readiness, return `PullRegistrationError` (`docs/reference/server/workers.mdx:35-43`), or enter the 60 s permanent-error retry (`worker_pull.go:552-554` and the other SDKs).

**Which rejections are soft (F5).** Today a `RESOURCE_EXHAUSTED` registration is a permanent stream error in every SDK. It is recorded in the registration state, and it can make **Start** return `PullRegistrationError` under `StopOnPermanentStreamRejection` (`sdk-go/dex/worker_pull.go:438-446, 552-555`; `sdk-python/dex/_pull_worker.py:45-50, 467-482`; `sdk-java/src/main/java/io/superdurable/dex/PullWorker.java:354-385`; `sdk-typescript/src/pull-worker.ts:686-703`; `sdk-rust/crates/dex-sdk/src/worker_pull.rs:281-283, 446-454, 1096-1104`; `docs/reference/server/workers.mdx:34-43, 64-68`). This design softens it only when all of these hold:
- The Worker is in adaptive mode: `MinOpenSlots` below `MaxConcurrentTasks`.
- The stream has not received `Registered`.
- The stream is either (a) a growth stream above the floor, or (b) an adaptive reconnect registration made after every required slot kind has reached initial readiness.

Every other rejection keeps today's permanent classification and retry semantics. That covers fixed mode, and the first registration of each required slot kind, such as an RPC-only stream before initial readiness.

A soft rejection is never recorded as a permanent error, so readiness and `StopOnPermanentStreamRejection` are unaffected. The Worker retries the stream only after another registered stream closes, or at the next window. It never retries on the rejected stream's own end (see the state machine).

A cap drop is the case that needs (b). An RPC-only stream can re-register first and take the only slot of a cap of 1. The Step-and-RPC stream then gets `RESOURCE_EXHAUSTED` (`worker_protocol.rs:98-149`). In adaptive mode it registers once the surplus RPC-only stream closes. In fixed mode it keeps today's behavior.

**The server lowers the cap.** Every acknowledgement reports the cap. When an acknowledgement reports a lower cap (F5):
- The Worker recomputes `C`, `R_eff`, `E`, and `L_eff`. It logs at WARN when `R + 1` exceeds the new cap and `R_eff` drops below `R`.
- Usage above the new `C` is the transient exception of the capacity invariant. The Worker opens no stream while it lasts.
- Step-and-RPC capacity comes first. RPC-only streams above `R_eff` half-close: at once when idle, after their final frame when busy. They are not reopened. Orphaned RPC-only handlers above `R_eff` count against `C` until they return.
- Then Step-and-RPC streams above `C - R_eff` half-close the same way. The shrink rule closes other idle surplus. The Worker never resets a busy stream.
- The first-acknowledgement check stays unchanged in fixed mode: an explicit `MaxConcurrentTasks` above the cap fails start. In adaptive mode it is clamped instead (F6).

**After a reconnect**, the Worker uses the last known cap. Operators rarely lower the cap, and the server enforces it at registration anyway. A lower cap is learned from the next acknowledgement and handled as above.

**A ceiling above the cap at the first acknowledgement (D4 and F6).** The Worker clamps `T` to the cap, derives `R` and the default floor from the clamped `T` (see [Floor and ceiling defaults](#floor-and-ceiling-defaults)), logs the clamp at WARN level naming both values, and keeps running, when:
- the Worker is in fixed mode and `MaxConcurrentTasks` is unset (D4); or
- the Worker is in adaptive mode, with an explicit or default `MaxConcurrentTasks` (F6).

In fixed mode, an explicit `MaxConcurrentTasks` above the cap fails start, as today.

### Reconnect, server restart, and failover

**A stream that the server or the network ends.** Today every stream reconnects with backoff (`worker_pull.go:523-541`).
- If the stream was busy, its handler may keep running. It moves from `B` to `H` until it returns. Not every SDK can stop it: the Python executor path does not cancel a sync handler (`sdk-python/dex/_pull_worker.py:624-634, 968-971`).
- A reconnect supervisor reopens a lost stream only while total usage is below `C`, `S + H < E`, and `S < max(L_eff, B + 1)`. Otherwise it drops the stream. A slot whose handler is still running is reopened only after the handler returns. The same holds for RPC-only streams, with `Sr + Hr < R_eff`.
- A server-directed `Reconnect` on a busy stream still waits for the task to finish (`worker_protocol.rs:236-244`).

**A whole-server restart or failover.** Every stream breaks. Up to `C` handlers may still run locally as `H` and `Hr`.
- **Last known cap.** The Worker computes free capacity from the last known `C`: `max(0, C - (S + H + Sr + Hr))`.
- **Order.** Free capacity goes first to one Step-and-RPC stream if none is open. Then it goes to RPC-only streams up to `max(0, R_eff - Sr - Hr)`, then to Step-and-RPC streams up to `max(0, min(L_eff, E - H) - S)`, and finally to growth. Streams open with today's jittered backoff. The same order applies whenever an orphaned handler returns.
- **No early registration.** Every newly registered stream accepts a task at once. So the Worker never registers a stream for a slot that a running handler still holds. While `H + Hr` fills the capacity, it holds no stream at all.
- **Growth.** The grow rule then takes the Worker to the needed level, still bounded by `S + H ≤ E` and `G`. The re-registration burst after a failover is therefore at most the sum of floors, not the sum of ceilings.
- **A lower cap after the failover.** The first acknowledgement reveals it, and the Worker converges as described in [Registration failures](#registration-failures-and-the-cap).
- **Protocol.** The reserved-slot protocol has no way to register a stream that refuses tasks: a NO_CAPACITY rejection on a reserved slot is a protocol error (`worker_protocol.rs:325-334`). Delaying the registration is the only protocol-safe choice (F5).
- **Readiness keeps today's semantics.** Once every required slot kind has registered, the readiness wait keeps returning success through reconnects (`sdk-go/dex/worker_pull.go:338-343, 416-424`; `sdk-python/dex/pull_registration.py:90-94, 205-210`; `docs/reference/server/workers.mdx:42-43`). A delayed registration does not change that. The snapshot shows the gap instead. `open` is 0 and `orphanedHandlers` is above 0 while the Worker waits for a free slot (see [Observability](#observability)).

**Attempts interrupted by the failover.** They fail as `NOT_COMPLETED`, as today. A handler that is still running counts in `H` or `Hr` until it returns. So the capacity invariant holds through the reconnect, apart from its transient exception after a cap drop (F5).

**Never reopen a stream closed on purpose.** The supervisor must not reopen a stream that the Worker closed for shrink or stop. This is the #740 leak class. Tests in each SDK cover it.

### Multi-Worker fairness and mixed fleets

The server picks the longest-idle stream across all Workers of a queue (`stream_registry.rs:55-65`; `worker_registry.rs:800-826`).

- **Without backlog.** Work spreads in proportion to idle streams. A Worker that receives more work runs out of idle streams and grows. A Worker with surplus shrinks. Hypothesis: busy streams converge toward an even split between Workers with equal ceilings. Exact balance is not a goal. Each Worker stays within its own ceiling.
- **Under backlog.** A Worker's freed and new streams start Claims while heads are claimable. Each partition and queue starts one task per Claim round trip, and started tasks keep their streams. Workers grow while those running tasks and the waiters use up their idle streams. A Worker that started earlier holds more streams and may take a larger share. Hypothesis, measured by the `fairness` benchmark.
- **Mixed fleets during a rollout.** Old fixed Workers always hold idle streams, so they attract most of the tasks. New adaptive Workers rarely see "all busy" and stay at their floor. When the old Workers stop, their share moves to the new ones, which ramp from their floors under load. The ramp costs a few round trips (see [Ramp-up latency](#ramp-up-latency)). Graceful stop already half-closes the old Workers' idle streams first. The benchmark plan measures this case.
- **Slow handlers.** A Worker whose handlers slow down, for example from CPU saturation, keeps its streams busy and grows toward its ceiling. Fixed slots already behave this way at the ceiling, so adaptation adds no new risk here. Resource-based throttling is a non-goal.

### Sticky dispatch

A sticky Step waits up to `matching.stickyWait` (5 s) while the sticky Worker has any connected stream, even when all of them are busy (`step_reservation.rs:170-191, 254-259`).

- **Below the ceiling, adaptation usually shortens that wait.** The hand-out that took the last idle stream triggers growth. A new stream becomes idle at registration (`worker_protocol.rs:149`; `worker_registry.rs:893-916`). It then serves the oldest RPC waiter first, then the oldest Step waiter of any queue it serves, and only then backlog (`stream_registry.rs:150-199`).
  - Without competing waiters, the sticky waiter is expected to get a new stream within about one registration round trip, instead of waiting up to 5 s.
  - With competing RPC waiters or older Step waiters, they take the new streams first. The sticky waiter may need several growth rounds, and it can still wait up to `stickyWait`. This is an expectation, not a guarantee.
- **At the ceiling, adaptation changes nothing.** The S2 tail case ran at 256 slots per Worker, which was the ceiling. A matching-side change would address it, such as ending the sticky wait early when the sticky Worker has no idle slot and is at its cap. That change is separate work and out of scope here. It is tracked in [#1992](https://github.com/superdurable/dex/issues/1992) (decision D11).

### Budgeting `maxConcurrentTasksCap` and `maxStreams`

`matching.maxConcurrentTasksCap` still bounds each Worker's streams on a node. In fixed mode, an explicit ceiling above it fails at the first acknowledgement, and an unset ceiling is clamped to it (D4). In adaptive mode, any ceiling above it is clamped to it (F6), so a high ceiling means "as many as the server allows". `matching.maxStreams` still bounds the node. When the node is full, an adaptive Worker's growth is rejected softly.

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

**Failover.** Re-registration is at most the sum of floors, limited further by orphaned handlers, followed by growth.

### Observability

**New SDK surface.** Every SDK adds the same set (decision D12):
- An accessor on the pull Worker that returns a snapshot with these fields:
  - `open`, `registering`, `busy`, and `idle` Step-and-RPC streams;
  - `orphanedHandlers` (`H`) and `orphanedRpcOnlyHandlers` (`Hr`): handlers still running after their stream ended;
  - `floor`, `effectiveCeiling` (`E`), `growthTarget` (`G`), and `totalCapacity` (`C`);
  - `rpcOnlyOpen` (`Sr`) and `rpcOnlySlots` (`R_eff`);
  - the counters below.

  `open` of 0 with `orphanedHandlers` above 0 means the Worker holds no live Step stream while it waits for a free slot. Readiness keeps today's semantics and does not report this. Today no SDK has pull metrics, so an accessor fits all five SDKs without choosing a metrics library.
- Counters: `streamsGrown`, `streamsShrunk`, `growRejectedAtCap`, and `shrinkRaceDrops`. The last one counts tasks dropped because they arrived after a shrink decision.
- Debug logs for each batch: grow with `from`, `to`, and `reason`, and shrink with `from`, `to`, and `peakBusy`. A WARN log when the ceiling is clamped to the server cap: an unset default in fixed mode (D4), or any ceiling in adaptive mode (F6). It names the configured value and the cap. One info log when a soft rejection clamps `growthTarget`.

**Existing server metrics answer the operator questions without a new family:**
- `dexzd_matching_worker_streams` (gauge, basic; `server-zd/crates/dexzd-metrics/src/lib.rs:415-419`) follows real concurrency plus floors.
- `dexzd_step_schedule_to_start_latency_seconds` (histogram, advanced; `lib.rs:490-494`) shows ramp cost. Its buckets are 5 ms, 25 ms, 100 ms, 500 ms, 2.5 s, and 10 s (`server-zd/crates/dexzd-metrics/src/histogram.rs:18`). They cannot separate a 5 s sticky wait from other waits between 2.5 s and 10 s. Decision D9 keeps these buckets in phase 1.
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

Only `maybe_reopen` and `maybe_grow` open streams. Both return at once while the retry latch `is_reopen_deferred` is set. Both open only within free capacity: `maybe_reopen` from `C - (S + H + Sr + Hr)`, and `maybe_grow` below `G ≤ E`. Every end of a stream, whether a disconnect, a failover, a completed half-close, or a rejected Register, goes through `stream_ended` once. Only an end of a stream that received `Registered` clears the latch.

```text
on task_frame(stream):                      # Step-and-RPC stream
    B += 1; I -= 1
    maybe_grow()

on final_frame_sent(stream):               # Step-and-RPC stream
    B -= 1; I += 1
    if S + H > max(0, C - R_eff):           # surplus after a lowered cap
        half_close(stream)
        recompute_capacity(); maybe_reopen()

on rpc_only_final_frame_sent(stream):
    if Sr + Hr > R_eff:                     # surplus after a cap drop
        half_close(stream)
        recompute_capacity(); maybe_reopen()

half_close(stream):                         # every deliberate close goes through here
    send END_STREAM; stream.is_removed = true   # its stream_ended changes no count
    if stream is RPC-only: Sr -= 1 else: S -= 1; I -= 1

on stream_ended(stream):                    # the only end event: disconnect, failover,
                                            # a completed half-close, or a rejected Register
    if stream.has_ended: return             # one stream may report lost and closed
    stream.has_ended = true
    if not stream.is_removed:               # not yet taken out of S or Sr
        stream.is_removed = true
        if stream is RPC-only:
            Sr -= 1; if its handler still runs: Hr += 1
        else:
            S -= 1
            if stream was registering: P -= 1
            elif stream was busy and its handler still runs: B -= 1; H += 1
            elif stream was idle: I -= 1
    if stream received Registered:          # it held capacity; a rejected one never did
        is_reopen_deferred = false
    recompute_capacity()                    # C stays the last known cap
    maybe_reopen(); maybe_grow()
    if P == 0: settle_growth_batch()

on orphaned_handler_returned(kind):
    if kind is RPC-only: Hr -= 1 else: H -= 1
    recompute_capacity(); maybe_reopen()

on registered(stream):                      # either stream kind
    if stream is Step-and-RPC: P -= 1; I += 1
    if this is the first acknowledgement:
        if is_adaptive or MaxConcurrentTasks is unset:  # F6, D4
            T = min(MaxConcurrentTasks or default, reported_cap or inf)
            derive R and MinOpenSlots defaults from T; L = MinOpenSlots - R
            if T was clamped: log WARN with the configured value and the cap
        elif MaxConcurrentTasks > reported_cap: fail start   # fixed, explicit
        has_first_ack = true                # adaptive RPC-only streams may open now
    C = min(T, reported_cap or inf)
    R_eff = R if R + 1 <= C else max(0, C - 1)
    close_surplus()
    recompute_capacity(); maybe_reopen()
    if P == 0: settle_growth_batch()

on registration_rejected(stream, RESOURCE_EXHAUSTED):
    if not is_soft_rejection(stream):
        today's permanent handling; return  # fixed mode, first registration per kind
    stream.is_removed = true                # its stream_ended event changes no count
    if stream is RPC-only: Sr -= 1
    else: P -= 1; S -= 1
    if stream is growth: has_batch_rejection = true
    is_reopen_deferred = true               # no tight retry loop
    recompute_capacity()
    if P == 0: settle_growth_batch()        # cannot reopen: the latch is set

is_soft_rejection(stream):                  # F5
    return MinOpenSlots < MaxConcurrentTasks    # adaptive mode only
       and stream never received Registered
       and (stream is growth above the floor
            or (stream is a reconnect and every slot kind reached initial readiness))

close_surplus():                            # Step-and-RPC capacity comes first
    while Sr + Hr > R_eff and an RPC-only stream is idle:
        half_close(that stream)
    while S + H > max(0, C - R_eff) and a Step-and-RPC stream is idle:
        half_close(that stream)
    # busy surplus streams close after their final frame (events above)

recompute_capacity():
    E = max(0, C - max(R_eff, Sr + Hr))     # RPC-only overflow takes Step capacity
    L_eff = min(L, E)
    G = min(G, E) if is_growth_target_clamped else E

maybe_reopen():                             # counts are clamped at zero
    if is_reopen_deferred: return
    free = max(0, C - (S + H + Sr + Hr))    # zero during the transient exception
    if S == 0 and free > 0:
        open 1 Step-and-RPC stream; S += 1; P += 1; free -= 1
    if has_first_ack or not is_adaptive:    # adaptive start: Step floor first
        n = min(free, max(0, R_eff - Sr - Hr))
        open n RPC-only streams; Sr += n; free -= n
    n = min(free, max(0, min(L_eff, E - H) - S))
    open n Step-and-RPC streams; S += n; P += n

settle_growth_batch():                      # only when the whole batch is done
    if has_batch_rejection:
        G = min(E, S + H); is_growth_target_clamped = true
        has_batch_rejection = false
    maybe_grow()

maybe_grow():                               # S + H < G ≤ E also keeps total usage below C
    if is_reopen_deferred: return           # every opening path respects the latch
    if I == 0 and P == 0 and S + H < G
            and not (is_growth_target_clamped and has_grown_this_window):
        n = min(G - S - H, max(1, S))
        open n streams; S += n; P += n
        has_grown_this_window = true
        restart_window()

every W:
    is_growth_target_clamped = false
    has_grown_this_window = false
    is_reopen_deferred = false
    recompute_capacity()                    # restores G = E
    maybe_reopen()
    target = min(E, max(L_eff, peakBusy + max(1, ceil(peakBusy / 4))))
    if S > target:
        half_close ceil((S - target) / 2) idle streams, newest idle first,
              never the last idle stream, never below L_eff
    peakBusy = B
    maybe_grow()
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

1. `S + H + Sr + Hr ≤ C` and `S + H ≤ E` at all times, with one transient exception (F5). After an acknowledgement reveals a cap drop, handlers and streams started before the Worker learned it may exceed the new capacity. During it the Worker opens no stream, and usage converges as handlers return and surplus streams half-close. `S ≥ L_eff` after the first registration, except while orphaned handlers fill the capacity.
2. Every stream is in exactly one state: registering, idle, busy, or closing. A stream leaves `S` when it starts closing.
3. The reconnect supervisor never reopens a closing stream or a stream closed on purpose.
4. A lost stream is reopened only while `S + H < E` and `S < max(L_eff, B + 1)`.
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
| Step from durable backlog: Claim to the partition owner, commit, then the frame (`backlog_matching.rs:24-36`) | Each partition and queue starts one task per Claim round trip, about 1.5 RTT + commit ≈ 30 to 60 ms. Started tasks keep running, so busy streams grow by one per Claim while handlers outlast that round trip | With long handlers, about the same as SYNC with an in-memory waiter, once enough partitions and Claims are in flight. With short handlers, the Worker needs few streams and does not grow | same |

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
| `backlog-one-partition` | One partition, or Flows that all map to one partition, if `dexbench` gains a way to pin it. Queue 1,000 Steps of one type with no Worker. Then start one adaptive Worker, `L` = 8, `E` = 99. Run twice: handlers of 10 ms, shorter than a Claim round trip, and handlers of 2 s. | Claims per second, busy and open streams over time, drain time, and schedule-to-start. Fixed slots at 99 as the baseline. | Whether started tasks fill the streams and trigger growth with long handlers, and whether short handlers stay Claim-bound with no growth. |
| S2 at C=4,096, 16 connections | Fixed against adaptive, at 256 per Worker. | p99 and the schedule-to-start buckets. | Confirms that adaptation does not fix the at-ceiling sticky tail. |

Results go under `benchmark/results/` with the server tunables, Worker options, commit, and hardware of each point.

## Rollout and API changes

**Phase 0: measure.** Add `idle-slots` to `dexbench` and commit results (#1989 item 1). The results size the phase 2 floor and feed the D8 evidence check. No product change.

**Phase 1: opt-in adaptive streams, an SDK patch release in all five SDKs.**
- Add `MinOpenSlots`, an optional field that defaults to `MaxConcurrentTasks`. With that default, behavior is identical to today. A lower value enables adaptation.
- In adaptive mode, a ceiling above the server cap is clamped with a WARN log instead of failing start (F6). Fixed mode keeps today's failure for an explicit value.
- Make stream creation incremental in Java, TypeScript, and Rust.
- Implement the state machine, the reconnect rules, the snapshot accessor, and the logs. Soft rejection applies in adaptive mode only (F5). Fixed mode keeps today's permanent classification of `RESOURCE_EXHAUSTED`.
- Doc comments carry "Since 1.NN.P" with the next patch version.
- `MaxConcurrentTasks` keeps its meaning and default. The deprecated idle-streams-per-queue options stay no-ops. This is an additive change, so it fits a patch release under the repository's release rules.
- Fixed mode keeps today's parallel startup. The only fixed-mode change is TypeScript readiness, which D13 authorizes.
- From this phase on, the stream count can vary, so the texts that say a Worker opens one stream per slot become wrong. The phase 1 pull requests update them together with the behavior change. Line numbers are from `origin/main` at `6eb9f59d3`:
  - `skills/dex-sdk/references/core/application.md:63` and the "Idle slots still hold Server streams" checklist item at line 76, with a skills fragment;
  - `docs/content/components/application.mdx:446` and its zh-Hans counterpart, `docs/i18n/zh-Hans/docusaurus-plugin-content-docs/current/components/application.mdx:398`;
  - `docs/reference/server/workers.mdx` lines 249, 262, 330-375, and 456 (see [Documentation](#documentation));
  - `docs/design/worker-pull-mode.md:7, 19`.
- Phase 2 updates the default values in the same pages.

**Phase 2: adaptive by default, together with the new default ceiling.**
- The `MinOpenSlots` default becomes `min(MaxConcurrentTasks, max(R + 1, CPU count))`. A Worker that sets only `MaxConcurrentTasks` is therefore adaptive. Setting `MinOpenSlots` equal to `MaxConcurrentTasks` keeps it fixed.
- The `MaxConcurrentTasks` default becomes 100. A ceiling above the server cap is clamped: an unset default in fixed mode (D4), and any value in adaptive mode (F6).
- These two changes ship in the same SDK release, after phase 0 and the phase 1 benchmarks.
- Changing defaults is a behavior change under Beta compatibility. The owner authorized these two default changes in D2 and D3 on 2026-11-17.
- Each clamp logs at WARN (D4, F6).
- Migration: an application that needs every slot open at all times sets `MinOpenSlots` equal to its slot count.

**The default of 100 relative to adaptive streams:**
- *Before:* each idle Worker holds 100 fixed streams, so 1,000 Workers fill `maxStreams`, and 500 with rolling overlap. Rejected.
- *With:* the floor keeps idle cost at today's level, and the higher ceiling serves bursts. Chosen.
- *After:* safe, but the higher ceiling waits a release.

See D3.

**Phase 3, optional: Option B.**
- The `schedule_to_start` field on `PollWorkerTask`, a server release, and SDK use of the field.
- Only if the `burst` or `fairness` results show a need, or if per-Worker schedule-to-start is wanted.

**Not planned: Option C.** Reconsider it only if `idle-slots` shows that busy streams limit a node.

## Decisions

The owner decided every row on 2026-11-17. The options and costs stay here for the record.

| # | Decision | Scenario | Options | Cost | Decided (owner, 2026-11-17) |
| --- | --- | --- | --- | --- | --- |
| D1 | Which option | Server cost should follow real concurrency | A; A plus B; C; D | A: SDK work only. B: plus a protocol field and a server release. C: protocol and server rework of #1253 paths. D: none, but no headroom for a higher default | A now; B later if benchmarks need it; C only on evidence |
| D2 | Opt-in or default-on, and the default floor | Existing Workers set or inherit a slot count | Opt-in first (floor = ceiling), then default floor = CPU count; default-on at once; always opt-in | Opt-in delays the server saving. Default-on changes burst behavior for explicit large slot counts without notice | Opt-in in phase 1. Default floor `min(ceiling, CPU count)` in phase 2 with owner authorization |
| D3 | When the default ceiling moves from CPU count to 100 | Raising the default without raising server cost | Before, with, or after adaptive default-on | Before: 1,000 idle Workers fill `maxStreams`. With: one coordinated release. After: the higher ceiling waits a release | With phase 2; never before |
| D4 | A ceiling above the server cap | Default 100 against a server with a lower cap | Fail start (today); clamp unset defaults only; clamp all values with a warning | Fail: breaks Workers on low-cap servers when the default rises. Clamp all: hides a misconfiguration | Clamp unset defaults with a WARN log; keep failing for explicit values. Amended by F6: this applies to fixed mode; adaptive mode clamps explicit values too |
| D5 | Grow trigger and step | A burst arrives at a Worker at its floor | `I = 0` and doubling; a low-water mark (`I < S/4`); 1.5x step; linear +1 | Low-water: more idle streams. 1.5x: about 1.7 times the rounds. Linear: E rounds | `I = 0`, doubling, one batch in flight |
| D6 | Shrink window and step | Load drops or cycles | `W` of 30 s, 60 s, or 5 min; close half the surplus, or all of it; headroom of 25% or a fixed count | A short `W` and closing everything cause churn. A long `W` delays the saving | `W` = 30 s, half the surplus, 25% headroom (minimum 1) |
| D7 | Public option name and shape | Users opt in or pin fixed slots | `MinOpenSlots` integer; `MinIdleSlots`; a mode enum `Fixed` or `Adaptive` | An enum cannot express a custom floor. "Idle" is wrong after growth | `MinOpenSlots` integer, the same idiom in each SDK |
| D8 | Server hint (Option B) now or later | The SDK cannot see queue age | Ship `schedule_to_start` now; later on evidence; never | Now: a server release and five SDK changes before the data | Later, decided from the `burst` and `fairness` results |
| D9 | Finer schedule-to-start buckets | 2.5 s to 10 s hides the 5 s sticky wait | Add buckets such as 1 s and 5 s; keep them | Changing the buckets of a released family changes its series and dashboards | Keep them in phase 1. Revisit with the S2 investigation |
| D10 | Adaptive reserved RPC slots | RPC-only slots sit idle without RPCs | Keep fixed; make them adaptive too | Adaptive RPC slots defeat their purpose: RPC latency isolation | Keep fixed |
| D11 | The at-ceiling sticky wait | S2 tail at 256 slots per Worker | Separate matching change, such as ending the sticky wait when the sticky Worker is at its cap with no idle slot; leave as is | A matching change to sticky semantics, outside this design | A separate server-zd investigation, tracked in [#1992](https://github.com/superdurable/dex/issues/1992) |
| D12 | SDK observability surface | Operators need open, busy, and idle per Worker | Snapshot accessor plus logs; native metrics per SDK; logs only | Native metrics need a metrics library choice per SDK | Snapshot accessor plus debug logs; native metrics later |
| D13 | TypeScript readiness difference | TypeScript is ready after one slot of any kind; the others wait for each kind | Align TypeScript in phase 1; leave it | Aligning it changes TypeScript readiness timing | Align it in phase 1, since phase 1 already reworks TypeScript stream creation |

### D1: Which option

The owner's goal is that server cost follows real concurrency, so the slot ceiling can be high.
- **A** reaches that goal with no protocol or server change. The server already tells the Worker the moment its last idle slot is taken.
- **B** adds what A cannot see, queue age.
  - A grows whenever in-memory waiters use up the idle streams.
  - Under durable backlog, one Claim per partition and queue is in flight at a time, but started tasks keep running. A grows whenever those running tasks use up its idle streams, which long handlers do. With handlers shorter than a Claim round trip, the Claim rate limits the drain and extra streams would not help. This is a hypothesis for the `burst` and `backlog-one-partition` benchmarks.
  - So B's main gains are faster growth and per-Worker schedule-to-start.
- **C** removes even the cost of busy streams. It does so by changing hand-out, half-close, and reset handling, which #1253 made correct. A reset would then fail every running attempt on the stream as `NOT_COMPLETED` and spend their retries.
- **D** keeps fixed cost. A default of 100 would then limit a node to 1,000 idle Workers.

Decided (owner, 2026-11-17): A, with B and C held behind benchmark evidence.

### D2: Opt-in or default-on, and the default floor

Today a Worker holds `MaxConcurrentTasks` streams at all times.
- If adaptation were on by default at once, a Worker that sets 256 slots would start at a lower floor. Bursts would then see a ramp of a few round trips.
- Opt-in in phase 1 keeps every existing Worker identical: the default floor equals the ceiling.
- In phase 2, a default floor of the CPU count keeps a Worker with today's default exactly as it is now. It only stops idle cost from growing with a higher ceiling.

Changing defaults needs the owner's explicit authorization under Beta compatibility. This decision gives that authorization for the phase 2 floor default.

Decided (owner, 2026-11-17): opt-in now, then the CPU-count floor default in phase 2.

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

Decided (owner, 2026-11-17): with phase 2, never before.

### D4: A ceiling above the server cap

Today, a first acknowledgement whose cap is below `MaxConcurrentTasks` fails start. A later lower cap only warns.
- With a default of 100 and an operator's cap below 100, failing start would break Workers that set nothing.
- Clamping an unset default, as the on-hold branch does, avoids that. It still reports an explicit misconfiguration.
- Clamping every value also hides a deliberate setting.

Decided (owner, 2026-11-17): clamp unset defaults to the server cap with a WARN log that names both values, and keep failing start for explicit values above the cap. WARN, not info, because the Worker then runs with less capacity than its default.

Amended by F6 (owner, 2026-11-17): the text above now applies to fixed mode. In adaptive mode, an explicit value above the cap is also clamped with a WARN log. See F6.

### D5: Grow trigger and step

A Worker at its floor receives a burst.
- `I = 0` with doubling and one batch in flight reaches the ceiling in `log2(E / L)` round trips. It needs no timer constant.
- A low-water mark grows earlier but keeps about a quarter of the streams idle.
- A 1.5x step overshoots less but needs about 1.7 times the rounds.
- Linear growth needs E rounds and is too slow for bursts.

Decided (owner, 2026-11-17): `I = 0`, doubling, one batch in flight. Confirm with the `burst` and `periodic` benchmarks.

### D6: Shrink window and step

After load drops, surplus streams should close without churn under cycling load.
- A 30 s window with half the surplus closed and 25% headroom reaches the floor in a few minutes.
- A shorter window, or closing all the surplus, reacts faster but churns streams under periodic load. Each close can cost a `NOT_DELIVERED` return.
- A longer window delays the saving.

Decided (owner, 2026-11-17): 30 s, half the surplus, 25% headroom with a minimum of 1. Confirm with the `periodic` benchmark.

### D7: Public option name and shape

Users need to opt in during phase 1, and to pin fixed slots after phase 2.
- An integer floor does both.
- A mode enum cannot express a custom floor.
- "Idle" is wrong for the floor, because the floor counts streams that may be busy.

Names: `MinOpenSlots` in Go, `min_open_slots` in Python and Rust, and `minOpenSlots` in Java and TypeScript. Validation: `ReservedRPCTasks + 1 ≤ MinOpenSlots ≤ MaxConcurrentTasks`. `MinOpenSlots` counts all slot streams, in the same unit as `MaxConcurrentTasks` (see [Configuration shape](#configuration-shape)). As with the slot count, zero means the default, except in Rust, which uses `Option`.

Decided (owner, 2026-11-17): the `MinOpenSlots` integer.

### D8: Server hint now or later

The SDK cannot compute schedule-to-start. Task frames carry no schedule time (`protos/dex.proto:469-484`).
- A `schedule_to_start` Duration on `PollWorkerTask` would let the Worker grow on queue age and report it.
- It needs a server release and five SDK changes before data shows A is insufficient.

Decided (owner, 2026-11-17): later. Option B ships only if the `burst` and `fairness` results show a need.

### D9: Finer schedule-to-start buckets

The 2.5 s to 10 s bucket of `dexzd_step_schedule_to_start_latency_seconds` cannot separate the 5 s sticky wait from other waits. Changing the buckets of a released family changes its series.

Decided (owner, 2026-11-17): keep them in phase 1. Revisit them with D11's investigation.

### D10: Adaptive reserved RPC slots

RPC-only slots exist so that RPCs still run while Steps hold every other slot. Adapting them would bring back the ramp delay for RPCs, which never queue. There are only a few of them, one by default.

Decided (owner, 2026-11-17): keep them fixed.

### D11: The at-ceiling sticky wait

The S2 tail at 256 slots per Worker fits the stickyWait hypothesis: a Step waits for a sticky Worker whose slots are all busy, at its ceiling. Adaptive streams cannot help at the ceiling. A matching change could, such as ending the sticky wait when the sticky Worker has no idle slot and is at its cap. That changes sticky semantics and is outside this design.

Decided (owner, 2026-11-17): a separate server-zd investigation, tracked in [#1992](https://github.com/superdurable/dex/issues/1992).

### D12: SDK observability surface

No SDK has pull Worker metrics today. Operators need open, busy, idle, and ceiling per Worker, plus grow and shrink counts.
- A snapshot accessor and debug logs fit all five SDKs the same way.
- Native metrics need a metrics library choice per SDK.

Decided (owner, 2026-11-17): a snapshot accessor plus debug logs now.

### D13: TypeScript readiness difference

TypeScript reports ready after the first registered slot of any kind (`sdk-typescript/src/pull-worker.ts:218-223`). The other SDKs wait for one of each slot kind. Phase 1 reworks TypeScript stream creation anyway, and identical behavior is a requirement. Aligning it changes TypeScript readiness timing slightly.

Decided (owner, 2026-11-17): align it in phase 1 and note it in the TypeScript change log.

### Follow-ups from review

The reviews of this document needed new choices. The owner decided F3 to F6 on 2026-11-17. F1 and F2 are superseded by F5 and kept only for the record.

- **F1: superseded by F5.** It proposed a Step-and-RPC probe after every reconnect. The review showed that the probe could take a task before the Worker read the new cap, so it did not keep the bound. F5 replaces it.
- **F2: superseded by F5.** Its delayed registration and its readiness rule are now part of F5.
- **F3: retry bound after a soft rejection.** Decided (owner, 2026-11-17). At most one growth batch per 30 s shrink window while rejections continue. The batch settles when all its Registers are done. The soft target `G` then becomes the registered `S + H`, and it returns to the hard capacity `E` at the end of each window. `E` itself never drops on a soft rejection. Alternatives: a separate backoff, or no retry until the next acknowledgement.
- **F4: the startup probe only for fixed mode with an unset ceiling.** Decided (owner, 2026-11-17). One Step-and-RPC stream is opened first only in fixed mode with an unset ceiling that may be clamped. Fixed mode with an explicit ceiling keeps today's parallel startup. Adaptive mode starts at the floor whether the ceiling is set or not (F6), and needs no probe. Alternative: probe always, which adds one round trip to every Worker start.
- **F5: reconnect, cap drops, and orphaned handlers.** Decided (owner, 2026-11-17).
  - **Reconnect.** The Worker uses the last known cap. It opens a stream only while `S + H + Sr + Hr < C` and never registers a stream for a slot that a running handler still holds. A reserved slot cannot refuse a task, so registering early would break the bound.
  - **Order of freed capacity.** One Step-and-RPC stream if none is open, then RPC-only streams up to `R_eff`, then the Step floor, then growth.
  - **An acknowledgement that reveals a lower cap.** `R_eff` becomes `max(0, C - 1)` when `R + 1` exceeds `C`, with a WARN log. Local usage may exceed the new cap transiently, but only through handlers and streams that started before the Worker learned it. While it does, the Worker opens no stream. Usage converges as handlers return and surplus streams half-close: RPC-only surplus first, then Step-and-RPC streams above `C - R_eff`. A busy surplus stream half-closes after its final frame. No stream is reset.
  - **Soft rejection, adaptive mode only.** `RESOURCE_EXHAUSTED` is soft only in adaptive mode (`MinOpenSlots` below the ceiling), and only for a stream that has not received `Registered`. It must also be either a growth stream above the floor, or a reconnect registration after every required slot kind has reached initial readiness. Fixed mode, and the first registration of each required slot kind, keep today's permanent classification and retry semantics. A soft-rejected stream is retried only after another registered stream closes, or at the next window. Its own end does not trigger the retry.
  - **Readiness.** The readiness wait keeps today's semantics. The snapshot shows `open` = 0 and `orphanedHandlers` > 0 while the Worker waits for a free slot.
  - **Alternatives:** a probe stream before other streams after each reconnect, which still races a hand-out; or a protocol addition for a stream that accepts no tasks yet.
- **F6: an explicit ceiling above the cap in adaptive mode.** Decided (owner, 2026-11-17).
  - In adaptive mode (`MinOpenSlots` below `MaxConcurrentTasks`), an explicit `MaxConcurrentTasks` above the server cap does not fail. It is clamped to the cap at the first acknowledgement, with a WARN log that names both values.
  - In fixed mode, an explicit value above the cap still fails at the first acknowledgement, as D4 says.
  - Reason: in adaptive mode the ceiling is only a growth limit, and users often set it high to mean "as many as allowed".
  - Consequence: adaptive mode starts at the floor whether the ceiling is set or not. It opens the RPC-only streams after the first acknowledgement, so Step-and-RPC capacity comes first. The F4 cap probe applies only to fixed mode with an unset ceiling.
  - D4 is amended to point here.

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
3. **Burst growth.** A burst of `N` concurrent long-running ASYNC Steps, with `L < N ≤ E`, grows streams to at least `N` and at most `min(E, 2N)`. A burst of `N` concurrent RPCs may use the RPC-only slots too, so it asserts `S + R ≥ min(N, E + R)` and `S ≤ min(E, 2N)`. Every task completes, and no Step spends a retry. This covers the grow rule and the overshoot bound. A SYNC variant checks only that streams grow above `L` and stay at most `min(E, 2N)`. Some SYNC Steps may take the backlog path, where each partition and queue starts one task per Claim round trip.
4. **Return to the floor.** After load stops, the count reaches `L + R` within the bound of invariant 5. It does not rise again over three more windows, which guards against the #740 leak.
5. **Shrink race.** Steps are dispatched continuously while the Worker shrinks. Every Step completes with attempt number 1 and no retry spent. Any race appears as `dexzd_step_attempts_returned_total{reason="not_delivered"}`. This guards requirement 3.
6. **Soft rejection at the node limit.** An initial cap below an explicit ceiling fails at the first acknowledgement, and an unset ceiling is clamped, so the per-Worker cap cannot produce a growth rejection. The test therefore fills `matching.maxStreams` instead:
   - Set `matching.maxStreams` to 20 and `matching.maxConcurrentTasksCap` to 20 or less, because validation requires `maxStreams ≥ maxConcurrentTasksCap` (`server-zd/crates/dexzd-server/src/config/validation.rs:571-579`).
   - Fill most of the node limit with a fixed Worker, for example 14 streams.
   - Start an adaptive Worker with a valid explicit ceiling, for example `MaxConcurrentTasks` = 12 with `R` = 1 and `L` = 2. Its ceiling fits the cap but exceeds the 6 streams left.
   - Drive it with long handlers so that it grows into the limit.
   - Assert that growth stops at the room left, readiness stays true, and no `PullRegistrationError` appears.
   - Assert that registration attempts stay at most one batch per shrink window.
   - Stop the fixed Worker. Assert that the adaptive Worker grows again after the next window.
7. **Cap lowered on reconnect.** The Worker runs in adaptive mode, with `MinOpenSlots` below the ceiling. After a server restart with a lower cap, the Worker keeps running. Surplus busy streams half-close after their final frame, idle surplus closes, and `S` converges to `min(E, target)`. The test covers three cases: a cap still above `L`, a cap below `L` (so `L_eff = E`), and a cap below `R + 1` (F5). The restart itself interrupts the streams that were open. The test asserts only that cap convergence causes no extra reset.
8. **Failover reopens the floor.** With the standby failover harness (`server-zd/crates/dexzd-server/tests/serve_process/standby_failover/`):
   - Queue `4 × L` Steps of 1 s ASYNC handlers before the failover, spread over at least two partitions. A 1 s handler is far longer than a Claim round trip, so Claims keep the started tasks running and fill the streams.
   - Assert that the Worker re-registers at most `L + R` streams first, not `E + R`.
   - Assert that it then regrows above `L` while the queue drains.
9. **Reserved RPC slots stay fixed.** RPC-only streams stay at `R` through growth and shrink. An RPC succeeds while every Step slot is busy.
10. **Shorter sticky wait below the ceiling, without contention.** A sequential Step chain on a Worker whose open slots are all busy, below its ceiling, with no other waiters, shows schedule-to-start well under 1 s. Today it can wait up to the 5 s `stickyWait`. A second case adds competing RPC waiters and older Step waiters on the same Worker. It asserts only that every waiter is served, that the sticky wait stays within `stickyWait`, and that no Step spends a retry.
11. **A stream closed on purpose is not reopened.** After a shrink, the closed streams stay closed for longer than two maximum reconnect backoffs.
12. **Graceful stop with adaptive streams.** Stop during growth half-closes idle and registering streams, lets busy ones finish, and returns undelivered tasks without spending a retry.
13. **Mixed fleet E2E.** A fixed Worker and an adaptive Worker serve one queue. Stopping the fixed one moves its load to the adaptive one, which grows. No Step fails. This uses the server-zd E2E harness (`server-zd/crates/dexzd-e2e-tests`).
14. **Cap of 1.** Against `matching.maxConcurrentTasksCap = 1`:
    - Fixed mode with an unset ceiling: the Worker probes first (F4). It starts with `T` = 1, `R` = 0, and one Step-and-RPC stream, and logs the clamp at WARN. It runs Steps and RPCs.
    - Adaptive mode with `MinOpenSlots` = 1 and the default `R`, with the ceiling set or not: the Worker opens one Step-and-RPC stream. At the first acknowledgement it clamps `T` to 1 with a WARN log and derives `R` = 0, so it opens no RPC-only stream. It runs Steps and RPCs.
    - With an explicit `ReservedRPCTasks` of 1, in either mode, start fails with an error that names `R`, `T`, and the cap.
    - Fixed mode with an explicit `MaxConcurrentTasks` of 2: start fails as today.
15. **Failover with orphaned handlers.** Start `E` handlers that ignore cancellation for 3 s: Python sync handlers on the executor path, and blocking handlers in the other SDKs. Then fail the server over.
    - Read `open`, `rpcOnlyOpen`, `orphanedHandlers`, and `orphanedRpcOnlyHandlers` from the SDK snapshot.
    - With the cap unchanged, assert that `open + orphanedHandlers + rpcOnlyOpen + orphanedRpcOnlyHandlers ≤ totalCapacity` at every sample.
    - Assert that the Worker registers no stream while the orphaned handlers fill the capacity.
    - Assert that, as handlers return one by one, freed capacity goes to a Step-and-RPC stream first, then to RPC-only streams, then to the floor.
    - Assert that the readiness wait keeps returning success throughout.
    - Repeat with the failover landing while a growth batch is registering, and while a shrink half-close is in flight.
    - Repeat with orphaned RPC-only handlers.
    - Repeat with a restart that lowers the cap below the orphaned handler count. This case checks the transient exception (F5), so it asserts convergence, not a strict bound. Usage may exceed the new `totalCapacity` only through handlers and streams that started before the first acknowledgement with the new cap. Assert that the Worker sends no new Register after that acknowledgement until usage falls below the new cap. Registers sent earlier under the old cap may still be acknowledged; let them settle. Assert that usage reaches at most the new cap once the old handlers return.
17. **Cap drop to 1 across a reconnect.** A Worker with `R` = 1 runs against cap 4 and has reached initial readiness. Restart the server with cap 1. A test proxy between the Worker and the server forces the reconnect order: it holds the Step-and-RPC Register until the RPC-only stream is acknowledged, so the Step-and-RPC Register really gets `RESOURCE_EXHAUSTED`. The test runs in two modes with separate assertions.
    - **17a, adaptive mode** (`MinOpenSlots` below the ceiling), with busy RPC-only and Step handlers during the restart:
      - Usage may exceed 1 for a while. The test asserts convergence, not a strict bound, during that window.
      - Assert that the rejection is soft. Readiness stays successful, and no `PullRegistrationError` appears, even with `StopOnPermanentStreamRejection` set.
      - Assert that `rpcOnlySlots` becomes 0 and that the RPC-only stream half-closes, after its final frame when busy.
      - Assert that the Step-and-RPC stream registers after that close, and that within a bounded time the Worker holds exactly one Step-and-RPC stream with usage at most 1.
      - Assert that Steps and RPCs keep running on that stream.
    - **17b, fixed mode** (`MinOpenSlots` equal to the ceiling):
      - Assert today's behavior. The rejection is a permanent error in the Step-and-RPC slot kind's registration state.
      - With `StopOnPermanentStreamRejection` set, **Start** returns `PullRegistrationError`, as documented (`docs/reference/server/workers.mdx:64-68`).
      - Without it, the Worker retries with today's permanent-error backoff.
18. **Growth batch partly rejected.** With the node limit setup of test 6, make a batch where some Registers succeed and some are rejected. Assert that `growthTarget` equals the streams that registered plus `orphanedHandlers`. Assert that `effectiveCeiling` stays unchanged, and that `growthTarget` returns to `effectiveCeiling` at the next window.
19. **Floor retries at the node limit.**
    - **Setup.** Set `matching.maxStreams` to 20 and `matching.maxConcurrentTasksCap` to 20. Start an adaptive Worker with `MaxConcurrentTasks` = 12, `R` = 1, and `L` = 2, with no load, so its `S` sits exactly at the floor of 2. A test-only shrink window of 1 s keeps the run short.
    - **Competitor.** The test runs its own raw `PollWorkerTask` client under another app Worker ID. It fills the other 17 node slots. It re-registers into any freed slot in a tight loop, so it wins every freed slot against the Worker's jittered backoff of 100 ms or more.
    - **Trigger.** A test proxy between the Worker and the server resets one of the Worker's two floor streams, after readiness. The competitor takes the freed slot. The Worker's reconnect Register is then rejected, and the rejection is soft, rule (b).
    - **Counting.** The proxy counts the Worker's Register frames for 3 windows. Close one of the competitor's streams once during that time. The competitor takes its slot again at once.
    - **Assertions.**
      - At most one Register per window.
      - The competitor's close triggers no extra Register. The latch clears only when one of this Worker's own registered streams ends.
      - No Register follows the rejected stream's own end at once.
      - Readiness stays successful.
      - `open` stays at 1 until the competitor stops, and then returns to 2 within one window.
20. **Explicit ceiling above the cap (F6).** Use the default `matching.maxConcurrentTasksCap` of 256.
    - **20a, adaptive mode:** `MaxConcurrentTasks` = 300 and `MinOpenSlots` = 8. Assert that the Worker starts and becomes ready. Assert that it logs one WARN naming 300 and 256, and that `totalCapacity` is 256. Drive it with 400 long-running concurrent ASYNC Steps. Assert that open streams, `open + rpcOnlyOpen`, reach at most 256, and that no registration is rejected for the per-Worker cap.
    - **20b, fixed mode:** `MaxConcurrentTasks` = 300 with `MinOpenSlots` unset in phase 1, or equal to 300. Assert that start fails at the first acknowledgement with today's error, which names both values.
16. **Fixed-mode startup is parallel.** With the floor equal to the ceiling, every stream is opened before the first acknowledgement arrives, as today. This guards the phase 1 "no behavior change" claim.

## Documentation

- **Developer reference.** `docs/reference/server/workers.mdx`. These line numbers are from `origin/main` at `6eb9f59d3c8f7774024be49bb1c9b3f52a51b57f`, which added the slot-count section after the commit this document read:
  - Capacity and shutdown, lines 248-269: replace "A Worker opens one long-lived stream for each slot" (line 249) and "The stream count equals the task limit and does not change with load" (line 262) with the floor and ceiling model and the soft cap behavior.
  - "Choose the slot count", lines 330-375: line 332 says each task slot is one stream. The "Idle slots cost the server" bullet (lines 359-360) costs idle slots by the ceiling. The "Rolling deploys" bullet (lines 364-365) budgets `matching.maxStreams` by the sum of slots. Rewrite them for adaptive streams: idle cost follows the floor, and the budget is real concurrency plus floors.
  - The `matching.maxStreams` row at line 456, "Estimate it by total slots".
- **SDK references.** The five pages `docs/reference/{go,python,java,typescript,rust}/workers.mdx`: the new option, its defaults, and the snapshot accessor, each with "Since 1.NN.P".
- **SDK doc comments.** `MinOpenSlots` and the snapshot accessor in each SDK, with a complete example and version markers. The `MaxConcurrentTasks` comments change from "one stream per slot" to "the most concurrent tasks; streams vary between the floor and this value".
- **Change logs.** One fragment per SDK target (`go`, `python`, `java`, `typescript`, `rust`), section Added in phase 1 and Changed in phase 2. A `server` fragment only if Option B ships.
- **Stable layer.** This changes a Component's behavior, so docs and skills change in the same pull request:
  - `docs/content/components/application.mdx:446` and its zh-Hans copy (`docs/i18n/zh-Hans/docusaurus-plugin-content-docs/current/components/application.mdx:398`) say "A Worker opens one stream for each task slot";
  - `skills/dex-sdk/references/core/application.md:63` says the same, and its checklist item at line 76 says idle slots still hold Server streams (line numbers from `origin/main` at `6eb9f59d3`);
  - the skills change needs a fragment and the owner's confirmation of its bump level;
  - `skills/doc-map.yaml` keeps both sides linked.
- **Contributor docs.** Update the stale stream formula in `docs/design/worker-pull-mode.md:7, 19`. Update `benchmark/README.md` for the new scenarios.
- **Agents reference.** `docs/reference/agents/workers.mdx:33-35` explains the 256 cap. Update it if agents Workers inherit the new defaults.

## UI/UX

N/A: no Dex Web change. The stream counts are visible through server metrics and the SDK snapshot.
