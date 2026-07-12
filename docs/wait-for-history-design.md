# WaitForHistoryEvent Design

`WaitForHistoryEvent` is a long-poll RPC on `RunsService` that lets a client
wait for a run to make progress — a specific history event to become readable,
or the run to close — without hot-looping `OpsService.GetHistoryEvents`. It
backs two SDK features: `WaitForHistoryEvent` ("wait until this run reaches
event N") and `WaitForRunComplete` ("wait until this run finishes", returning
the terminal status directly — no per-event `GetRun` polling).

It is modeled directly on Cadence's `GetWorkflowExecutionHistory` long
poll (`service/history/engine/engineimpl/poll_mutable_state.go`
`longPollForEventID`): subscribe to a per-run notification channel, re-read
the authoritative state to close the subscribe race, then `select` on the
channel versus the caller's deadline.

Unlike Cadence's handler, there is **no server-side timeout cap and no
request-level `timeout_ms` field.** The wait is bounded **solely by the
caller's own RPC context deadline** — the handler blocks on `ctx` directly and,
on forward, propagates the same `ctx` unchanged to the shard owner. A caller
that wants to wait indefinitely (e.g. `WaitForRunComplete` with
`context.Background()`) gets exactly that; a caller with a 5s deadline blocks
at most 5s. This mirrors `MatchingServiceConfig.LongPollDefaultTimeout`'s
*intent* (bound the block) without its server-side cap — see "Why no
server-side cap or safety buffer" below.

The authoritative read is a **single `HistoryStore.GetLatestEvent`** — no engine
and no RunStore. Everything the response needs (tip, closed, terminal status)
comes from the latest history event. A consequence: an unknown or not-yet-started
run is not an error — it simply has no latest event, so the call blocks until the
timeout, and a client may start waiting **before** the run exists.

## API

```proto
rpc WaitForHistoryEvent(WaitForHistoryEventRequest) returns (WaitForHistoryEventResponse);

message WaitForHistoryEventRequest {
  string namespace = 1;
  string run_id = 2;
  reserved 4;                     // was timeout_ms; the ctx deadline bounds the wait
  oneof condition {               // exactly one must be set
    int64 until_event_id = 5;     // wake when the latest event id reaches this
    bool  until_run_stop = 6;     // wake only when the run closes (terminal)
  }
}

message WaitForHistoryEventResponse {
  int64 latest_event_id = 1;   // highest inserted (readable) event id at return
  int32 run_status = 2;        // terminal RunStatus (0..5) when closed; -1 (invalid) otherwise
}
```

**Conditions.** Exactly one `condition` must be set (else `InvalidArgument`):

- `until_event_id` — return when `latest_event_id >= until_event_id`
  **or** the run closes. The closed short-circuit mirrors Cadence's
  `!IsWorkflowRunning` branch: a closed run's `RunStop` is its last event, so a
  caller waiting for an id beyond it must return instead of hanging.
- `until_run_stop` — return **only** when the run closes. A mere event advance
  does not wake it. This is the efficient primitive behind `WaitForRunComplete`:
  the response's `run_status` gives the terminal outcome without a `GetRun`.
  A non-terminal wake reports `run_status = -1` (`RunStatusInvalid`), since `0`
  is `Pending` — a real status — and cannot double as "not closed".

There is **no `timeout_ms`** — the caller's context deadline is the only bound.
In both modes, the deadline elapsing first returns `codes.DeadlineExceeded`
(derived from `ctx.Err()` via `status.FromContextError`), meaning "the
condition had not been met by your deadline" — the caller decides whether and
how to retry with a fresh deadline. See "Why no server-side cap or safety
buffer" below for why this differs from Cadence's empty-result-on-timeout and
from Matching's `PollForRun`.

`latest_event_id` is the highest **inserted** (readable-via-`GetHistoryEvents`)
event id, not the allocated high-water on the run row — see §"Insert-bound
notifier" below.

## Why it lives on RunsService (not OpsService)

History rows are written on the **shard owner** by the per-shard OpsFIFO
batch reader, and the wakeup notifier is a process-local component fed by
that reader. A random `RunsService` instance may not own the run's shard
(`shard = crc32(runID) % numShards`), so the handler forwards to the owner
via the same `tryForward` / `RemoteClient` routing used by `StartRun`,
`StopRun`, and the `ProcessStep*` RPCs — but unlike those RPCs' fixed 10s
forward timeout, `WaitForHistoryEvent` forwards with the **caller's ctx
unchanged**: the owner blocks on the same deadline the caller set, and a
forward-transport failure surfaces directly to the caller as an error (no
extra timeout layer to reconcile).

## Insert-bound notifier

Unlike Cadence — which persists events and mutable state atomically in the
history service — DEX appends history events under the run's CAS but inserts
the history **rows asynchronously** via the OpsFIFO batch reader (~one
`OpsBatchReadDelay` later). To give strict read-your-writes semantics
(a returned `latest_event_id` is always readable), the notifier is bound to
the **insert**, not the CAS:

- Producer: `taskprocessor.OpsBatchReader.writeHistoryBatch`, after
  `BatchInsertHistory` succeeds, hands the inserted `[]HistoryEvent` to
  `historynotify.Notifier.NotifyEventsWritten(events)` (via the
  `historynotify.EventNotifier` interface). The notifier owns the derivation:
  per-`(namespace, runID)` it takes the max `EventID` as the tip and, if any
  event is a `RunStop`, marks the run closed with its terminal `RunStatus`.
- Consumer: `RunsServiceHandler.WaitForHistoryEvent` calls
  `Notifier.Subscribe(ctx, req)`, which registers the waiter and then folds a
  single authoritative `GetLatestEvent` read through `NotifyEventsWritten` — so
  an already-satisfied condition (e.g. the run already closed) fires without
  waiting for a fresh OpsFIFO insert.

Both run on the shard owner, so a process-local notifier suffices — there is
no cross-node pub/sub. Every terminal transition appends a `RunStop` history
event (`RunEngine.StopRun` / completion / failure) carrying the run status, so
a blocked waiter — including a `run_stop` one — is always woken by the
`RunStop` insert with `closed=true` and the terminal status, without an extra
`GetRun`.

## Control flow (`server/internal/api/runs_service.go`)

The notifier owns the wait condition; the handler never re-checks or loops:

1. Validate that a `condition` is set (else `InvalidArgument`).
2. Map `runID → shardID`; if not local, `tryForward` to the owner, propagating
   the caller's `ctx` unchanged (see "Why no server-side cap" below).
3. On the owner, `historyNotifier.Subscribe(ctx, req)`. Subscribe registers the
   waiter **first** (with the request's condition), then does the authoritative
   read: a single `HistoryStore.GetLatestEvent(ns, runID)` — **no engine and no
   RunStore** — folded in via `NotifyEventsWritten`. Registering before the read
   means a concurrent insert cannot slip between them. The notifier derives the
   tip (`EventID`), whether the latest event is the **RunStop event**, and the
   terminal `run_status` (`RunStop.RunStatus`). A run with no events (unknown /
   not yet started) reads `nil` → the wait simply blocks, **no error**, until
   events start landing. This also covers the cold-owner/failover case purely
   from history.
4. Fast path: a non-blocking `select` on `sub.WaitUntilConditionMet()`; if the
   authoritative read already satisfied the condition, return immediately —
   preferred over an already-expired ctx.
5. `select` on `sub.WaitUntilConditionMet()` versus `ctx.Done()` directly — no
   loop, no re-check, no intermediate budget. The notifier delivers the
   `Result` on that channel and closes it exactly when the subscription's
   condition holds; the handler builds the response (`latest_event_id`,
   `run_status`) directly from it. `ctx.Done()` first → return
   `status.FromContextError(ctx.Err()).Err()` (`codes.DeadlineExceeded` or
   `codes.Canceled`).

The `historynotify` package is small: a mutex-guarded map keyed by
`namespace/runID`, each per-run `runNotifier` holding the latest event and the
set of live subscriptions. Each subscription carries its own condition and a
buffered `resultCh` the notifier sends the `Result` on (then closes) when that
condition holds; the entry is dropped when its last subscription closes, so the
map does not grow unbounded.

## Server-side timeout cap (without busy-spin risk)

`WaitForHistoryMaxTimeout` (default 60s) caps how long a single
`WaitForHistoryEvent` RPC blocks on the server: the effective wait is
`min(caller_deadline, cap)`, and the cap alone when the caller supplied no
deadline. It bounds **subscription lifetime per RPC** regardless of the
caller's deadline, so a caller with a very long (or `context.Background()`)
deadline cannot pin a subscription past the cap.

A single RPC therefore blocks at most `WaitForHistoryMaxTimeout`. A caller that
wants to wait longer than one window **re-issues** — each re-issue is a fresh
Subscribe with the caller's remaining budget. This is a deliberate contract:
`WaitForRunComplete` is single-shot, so a caller (or test) waiting on a run
that may take longer than the cap loops on `codes.DeadlineExceeded` while its
own ctx still has budget (see the cluster test's `waitForRunCompleteWithinCtx`).
Because the cap produces a plain `DeadlineExceeded` (not a distinct re-issue
signal), the re-issue is driven by the caller's own remaining budget — when
that budget is gone, the caller stops. No busy-spin.

The critical design difference from Cadence's `LongPollExpirationInterval` +
`longPollCompletionBuffer` (and our own Matching's `LongPollDefaultTimeout` +
`LongPollSafetyBuffer`):

- **Cadence / Matching's `PollForRun`**: the cap produces a **special signal**
  (`OutOfRange` in our earlier design, an empty result in Cadence/Matching).
  The caller interprets it as "no event yet" and **re-issues immediately**.
  This is safe for Matching because the worker supplies a fresh deadline each
  iteration (`context.WithTimeout(w.rootCtx, ...)` in `pollForRunOnce`), so
  there's always budget left. But for a `WaitForHistoryEvent` caller near its
  own deadline, re-issuing on the cap's signal produces busy-spin.
- **Our design**: the cap just produces a **deadline (via
  `context.WithTimeout(ctx, maxTimeout)`)**, same as the caller's deadline. If
  the server cap expires first, the caller gets `codes.DeadlineExceeded` just
  as if their own deadline fired. The caller sees "your deadline or the server
  cap expired" — **no special signal, no invitation to re-issue.** A
  thoughtful caller (one using the deadline as the true bound on work) won't
  re-issue. An oblivious caller that re-issues anyway will hit their own
  deadline next and stop. No busy-spin.

So the cap is there for **server hygiene (bound goroutine lifetime)**, not for
**client behavior guidance**. The `codes.DeadlineExceeded` that both deadlines
produce is neutral: it reports expiry without direction.

## Configuration

`RunServiceConfig` (`server/config/run_service.go`):

- `WaitForHistoryMaxTimeout` (default 60s) — server-side cap on how long a
  single RPC blocks. The effective wait is `min(caller_deadline, cap)`, and the
  cap alone when the caller has no deadline. Bounds subscription lifetime per
  RPC; a caller wanting to wait longer re-issues. Expiry returns
  `codes.DeadlineExceeded`.

## Observability

The handler currently emits **only `DEBUG` logs** — the wait is client-driven
and potentially high-frequency, so tenant-triggered volume must not inflate
WARN/ERROR. Logged at DEBUG: forwarding to the shard owner, and the caller's
deadline being reached, each tagged with run id and namespace.

Dedicated metrics (latency, `immediate | signaled | timeout` result counter,
in-flight-waiter gauge) are **not yet wired up**.

## Tests

- Integration (`internal/integration/waitforhistory/`, one full-server boot
  with a live OpsFIFO): `by_id` immediate / blocked-then-signaled / blocks
  until the caller's deadline (`DeadlineExceeded`) / closed short-circuit /
  concurrent; `run_stop` blocks past event advances and wakes on close /
  immediate when already closed; missing-`condition` InvalidArgument; an
  unknown run blocks until the caller's deadline; and a wait started
  **before** the run exists wakes on its RunStart insert.
- Cross-node forwarding
  (`internal/integration/cluster/cluster_test.go`,
  `TestCluster_WaitForHistoryEvent_ForwardsToOwner`): the wait issued on the
  non-owner node forwards to the owner and is woken by the owner's insert.
- SDK E2E (`internal/integration/sdke2e/sdk_e2e_wait_for_history_test.go`):
  `Client.WaitForHistoryEvent` then `Client.WaitForRunComplete` against a
  worker-driven run through to close.
- Unit (`internal/historynotify/notifier_test.go`): by-id met by tip; by-id met
  by close before the tip; a late joiner wakes immediately if the run's
  existing tip already satisfies its condition (register-time check, not just
  the trailing fold); `run_stop` ignores a non-terminal advance and wakes on
  close; Subscribe seeds from the store; an already-closed run satisfies at
  Subscribe; a batch spanning multiple runs; notify with no subscriber is a
  no-op; the entry is dropped when the last waiter leaves; keys isolated by
  namespace and run.
- Unit (`sdk-go/dex/backoff_test.go`): `callWithRetry` stops retrying once
  ctx's remaining time drops below `retryMinRemainingBudget` (returns the last
  error, not `ctx.Err()`); retries normally with an ample deadline; a
  non-retryable error returns on the first attempt; a ctx with no deadline
  never trips the remaining-budget guard.

## SDK

- `Client.WaitForHistoryEvent(ctx, runID, expectedEventID) (int64, error)`
  — `until_event_id` mode; returns the latest event id, or
  `codes.DeadlineExceeded` if `ctx` expires first. There is no `timeout`
  parameter; the caller's `ctx` is the only bound, single-shot (no retry loop
  inside the SDK — a caller wanting to keep waiting re-issues with a fresh
  ctx).
- `Client.WaitForRunComplete(ctx, runID) (int32, error)` — a thin pass-through
  to `RawClient.WaitForRunStop` (`until_run_stop` mode): blocks until the run
  closes or `ctx` expires, then returns the terminal status or
  `codes.DeadlineExceeded`. No `GetRun` polling, no retry loop.

Both raw methods (`RawClient.WaitForHistoryEvent`, `RawClient.WaitForRunStop`)
retry transient server errors (`Unavailable`, `Internal`, `DeadlineExceeded`,
`Aborted`, `ResourceExhausted`) via `callWithRetry`, but **stop retrying once
`ctx`'s remaining time drops below `retryMinRemainingBudget`** — a retry
with less budget than that has no realistic chance of completing (especially
for a long-poll RPC, whose own attempt can take seconds), so `callWithRetry`
returns the last error immediately instead of burning the remaining ctx on a
doomed attempt. This means a `WaitForHistoryEvent`/`WaitForRunComplete` call
whose *transport* was flaky right up to the deadline can still surface as
`codes.Unavailable` (the last attempt's real error) rather than
`codes.DeadlineExceeded` — both signal "didn't complete in time," and the
caller's next step is the same either way: re-issue with a fresh ctx.
