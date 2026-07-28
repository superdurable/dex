# Phase 4 — Interpreter rewrite (careful execution)

Detailed execution plan for Phase 4 of the server rewrite. Parent plan:
[`server-rewrite-plan.md`](server-rewrite-plan.md).

Phase 4 is the largest remaining slice. **Pacing:** work in the 5 review slices
below; stop after each for review before continuing. Files in S2/S4/S5 share the
same Go package, so S1–S4 are stacked review checkpoints, not independently
mergeable green commits. Do not land them separately or start Phase 5 until S5
makes the whole interpreter package build and the Phase 4 checkpoint passes.

### Interpreter migration constraint

- Use commit `d5f06a2b51a1b878ce1329aa5d4f1d945ea09c27` as the structural baseline
  for `server/service/interpreter/`.
- Preserve the existing main loop, coroutine ownership, type layout, methods,
  fields, variables, and comments unless protobuf or a listed feature requires a
  change.
- Apply only the concept renames in [`idl-renames.md`](../idl-renames.md).
  Do not introduce an `interpreterRuntime`, orchestration state machine, child
  workflow contexts, or a deferred result queue.
- Exception: retain `channel.Plan` / `channel.MatchPlan` as the replacement for
  `deciderTrigger`; it is required for multi-value and shared-channel matching.

```mermaid
flowchart TB
  S1[S1_Interfaces_Config_Version]
  S2[S2_Stores_Channel_Attr_Step]
  S3[S3_Providers_Timers]
  S4[S4_Orchestration_workflowImpl]
  S5[S5_Updates_Queries_CAN]
  S1 --> S2 --> S3 --> S4 --> S5
```

## Highest-risk invariants (do not regress)

- Channel matching is **two-phase** (pure plan → atomically commit). Never eager
  `Retrieve` before the winning trigger is known
  ([PR #600](https://github.com/indeedeng/iwf/pull/600) behavior only).
  Consumption is a **single-threaded central commit** after winner selection —
  condition threads only `Await` peek predicates, they never consume (fixes the
  current per-thread `Retrieve` at `workflowImpl.go:768`/`:806`).
- Channel messages enter through declared system signals and worker results.
  Arbitrary signal names are not discovered or consumed.
- Attributes are always fully loaded; locks use explicit RPC or step API lock keys.
- Memo is never attribute persistence. No interpreter path reads or upserts
  attributes through Temporal/Cadence Memo.
- Attribute locking covers each worker API read-modify-write window. New worker
  activities that read the full attribute set do not start while a lock is held;
  the originating activity/signal coroutine waits and commits its whole result after
  unlock.
- **Sync-update waits yield to continue-as-new.** WaitForStep/WaitForAttribute
  return an internal `CAN_PREEMPTED` outcome when the threshold becomes true. The
  API/UnifiedClient retries against the current run while preserving the original
  caller context and absolute deadline. CAN drain is never stalled for the full
  wait duration. Locking InvokeRPC is allowed to finish its bounded activity and
  holds the CAN inflight counter until it releases its locks.
- WaitForStep / WaitForAttribute **and locking `InvokeRPC`** (non-empty
  `lock_attribute_keys`) are Temporal sync-updates only; on Cadence the API returns
  `codes.Unimplemented`. Non-locking `InvokeRPC` still works on Cadence via
  signal+query.
- CAN dump uses deterministic proto marshal + checksum; no JSON pages.
- Deterministic marshal does not sort repeated fields constructed from maps.
  Snapshot/build helpers sort every map-derived repeated collection explicitly.
- Activity calls are single top-level `*dexpb.*` arg/result (no
  `backendType, req, ...` multi-arg).
- Any map iteration that feeds workflow decisions uses `DeterministicKeys`
  where ordering matters; `channel.Plan` winner selection and commit iterate ordered
  `repeated` condition slices, never the channel map.
- Interpreter/activities have no mutable package dependency globals: delete
  `env.SetSharedEnv`, `env.Get*`, and the activity-provider registry;
  constructor-inject dependencies.
- Complete/fail/CAN use the baseline workflow-loop flags and precedence defined
  in S4/S5.

**Parent-plan alignment:** the parent Phase 4 summary follows this plan: long waits
are preempted and transparently retried instead of blocking CAN; locking InvokeRPC
remains bounded and drains before CAN.

### Entry prerequisite — remove memo-as-attribute plumbing outside Phase 4

- Before S1, Phase 1 API/config/const cleanup must be complete: StartFlow no longer writes
  `UseMemoForDataAttributesKey` or copies attributes into Memo; GetAttributes no
  longer calls `GetUseMemoForDataAttributes` or reads attributes from
  `response.Memos`; `Interpreter.FailAtMemoIncompatibility` and
  `service.UseMemoForDataAttributesKey` are deleted.
- Phase 2/2.5 has no `InterpreterWorkflowInput.UseMemoForDataAttributes`; do not add a
  replacement proto field (`dex.proto` intentionally contains no memo field).
- Preserve only legitimate backend Memo behavior: `WorkerTargetMemoKey`, workflow
  request-id idempotency, and client-side memo encode/decode/encryption helpers for
  those system keys.

---

## S1 — Contracts, config, version reset

**Files:**
[`interfaces/interfaces.go`](../../../server/service/interpreter/interfaces/interfaces.go)
(+ regenerate mock),
[`config/workflowConfiger.go`](../../../server/service/interpreter/config/workflowConfiger.go)
→ `flowConfiger.go` / `FlowConfiger`,
[`globalVersioner.go`](../../../server/service/interpreter/globalVersioner.go),
[`service/interfaces.go`](../../../server/service/interfaces.go) (delete
superseded Go payload structs; keep `BasicInfo` / helpers).

- Drop all `dexpb` from `WorkflowProvider` / `TimerProcessor` / `ActivityOptions`.
- Retype to `dexpb` (`RetryPolicy`, `TimerCondition`, attributes).
- Delete `WorkflowProvider.UpsertMemo`, both Temporal/Cadence implementations, the
  generated mock method, and timer/provider test stubs. Do not replace it: the only
  caller was memo-as-attribute storage.
- Put update-handler registration directly on `WorkflowProvider`. Temporal registers
  handlers; Cadence returns nil because the API rejects its unsupported calls.
  Use three concrete typed methods rather than
  `interface{}`/`proto.Message` dispatch:
  - InvokeRPC (`InvokeRPCRequest` → `InvokeRpcUpdateResult`)
  - WaitForStepCompletion (`WaitForStepCompletionRequest` → `...Response`)
  - WaitForAttribute (`WaitForAttributeRequest` → `google.protobuf.Empty`)
- Keep timeout mechanics out of handler registration. Long waits capture one workflow
  start time and use common `WorkflowProvider.Await` with a predicate checking
  `Now() > start + timeout`, match, terminal, and CAN. This creates no durable
  timeout timer; timeout alone need not wake an otherwise idle workflow.
- Replace `ExecuteActivity(..., optimize bool, ...)` with
  `ExecuteActivity(..., StepDurability, ...)`. Reject `UNSPECIFIED` at this boundary.
  Every call still passes one proto input and result (enforced in S4); no variadic
  interpreter activity arguments remain. Step wait/execute calls use resolved
  durability; blob cleanup is fixed `SYNC`, while CAN dump directly uses
  `ExecuteLocalActivity`.
- Keep `ExecuteLocalActivity` as a separate forced-local operation. Temporal sync
  update handlers call it directly; they never route through durability dispatch or
  fallback to a regular activity.
- Keep workflow registration names in `service/const.go`; Temporal and Cadence must
  reference the same `Interpreter` and `BlobStoreCleanup` constants.
- Keep application-error creation and detection on `WorkflowProvider`.
- `FlowConfiger`: use `*dexpb.FlowConfig`; retain the ownership-transferred input
  directly and panic on a nil constructor argument. Never retain the mutable
  `config.DefaultWorkflowConfig` pointer. FlowConfig scalar fields retain presence,
  so StartFlow `flow_config_override` and `UpdateFlowConfig` apply only present
  fields. Callers must not mutate a message after transferring ownership. An absent
  start override uses the configured server default.
  Define zero values explicitly: threshold 0 disables automatic CAN, page size 0 uses
  `DefaultContinueAsNewPageSizeInBytes`, durability `UNSPECIFIED` resolves to
  `SYNC`, and active-step mode `UNSPECIFIED` resolves to
  `ENABLED_FOR_STEPS_WITH_WAIT_FOR`. Reject negative threshold/page size and unknown
  enum numbers. Resolve durability separately for wait and execute:
  1. use the matching `StepOptions` override when non-`UNSPECIFIED`;
  2. otherwise use `FlowConfig.step_durability`;
  3. otherwise default to `SYNC` (the old `optimize_activity=false` behavior).
  `SYNC` executes a regular activity. `ASYNC` first executes a local activity; if its
  `Future.Get` returns a Go error, retry once as a regular activity and return that
  result. An `InterpreterError` inside a successful activity response is a business
  result and does not trigger fallback. Delete
  `optimize_timer` / old activity-optimize bools that conflict with always-greedy
  timers.
- `GlobalVersioner`: keep mechanism; set
  `MaxOfAllVersions = StartingVersionV1`; delete all `IsAfterVersionOf*` gates
  and call sites as they are touched (finish leftover gates in S4/S5).
  Always-latest behavior.

**Review gate:** interfaces and config subpackages compile in isolation; config
replacement/default/durability tests are green; mocks are regenerated;
`globalVersioner.go` retains only the v1 marker/hook; no call site adds a compatibility
alias. The root interpreter package may remain broken until S5 because all five
slices share it.

---

## S2 — Leaf stores (channel, attributes, step queue/counter/output)

**Files:** `InternalChannel.go` → `channelStore.go`,
[`persistence.go`](../../../server/service/interpreter/persistence.go),
[`outputCollector.go`](../../../server/service/interpreter/outputCollector.go),
`stateRequest*.go` → `stepRequest*.go`,
`stateExecutionCounter.go` → `stepExecutionCounter.go`,
[`channel/plan.go`](../../../server/service/interpreter/channel/plan.go).

### Channel store

- `receivedData map[string][]*dexpb.Value`.
- Keep the store API small: publish/peek/snapshot plus
  `CommitMatch(*channel.MatchPlan)`. Planning is pure and never writes reservation
  state into the store. Commit verifies the planned counts against the current queue,
  consumes FIFO, and cannot yield.
- `GetInfos() map[string]*dexpb.ChannelInfo`.
- Delete single-message `Retrieve` / version-gated paths.

### Decider / matching

- Input: `*dexpb.WaitingCondition`.
- Normalize AtLeast/AtMost per Phase 0 (Exact N / OneToAll / ZeroToAll /
  defaults).
- Put normalization and planning in a pure `interpreter/channel` subpackage so its
  table tests run before the root interpreter package is green.
- The channel package accepts activity-validated conditions. Keep Worker response
  validation at the activity boundary; do not expose it from the channel package.
- Use timer/channel declaration indexes as internal identity. `condition_id` is only
  required to resolve ANY_COMBINATION references and is copied unchanged to results.
- Implement ALL / ANY / ANY_COMBINATION with this exact algorithm:
  1. Snapshot available counts; do not retain pointers into channel queues.
  2. Build candidate sets. ALL has one set containing every condition. ANY has one
     candidate per condition in canonical order: timer declaration order followed by
     channel declaration order. ANY_COMBINATION tries combinations in declaration
     order and allocates their referenced conditions in original condition order.
     A candidate is eligible only when all referenced timer conditions have already
     fired or been skipped; ZeroToAll channel conditions are immediately eligible.
  3. For each candidate, reserve every channel condition's normalized `at_least`
     first. The candidate is infeasible if the sum of minima for a shared channel
     exceeds its available count.
  4. In a second declaration-order pass, allocate remaining messages up to each
     condition's positive `at_most`; an omitted/zero `at_most` consumes all remaining
     capacity. This prevents ZeroToAll/OneToAll from stealing another condition's
     required minimum.
  5. Select the first feasible candidate and return a `MatchPlan` containing only
     exact channel consume counts keyed by channel-condition declaration index.
     A winning zero-consumption channel still has a `Consume` entry. Timer-only
     candidates have an empty plan. Planning does not mutate queues.
  6. The step coroutine commits that plan once without a workflow yield. A count
     mismatch at commit is a trusted invariant failure because no yield can occur
     between plan and commit. A channel result receives the planned FIFO values.
     Consumed channels are `COMPLETED`; other channels are `WAITING`. Every timer
     reports its observed processor state: fired/skipped maps to `COMPLETED`, while
     pending/canceled maps to `WAITING`.
- Output: `*dexpb.ConditionResults` with `ChannelResult.values`.
- Provide the commit as a single call the step coroutine invokes from one thread
  (S4); the per-condition wait threads only peek, never consume.

### Persistence

- One `map[string]*dexpb.Value`. `AttributeWrite` is transient: process its current
  `IndexConfig` into the backend update, then discard it. Persistence, queries, and
  CAN retain only key/value pairs. Ownership of retained values transfers on ingress;
  readers must not mutate them. Do not defensively clone on ingress/egress.
- Inject the shared `interfaces.WorkflowProvider` directly. Do not add a local
  search-attribute-only provider interface.
- Delete LoadingPolicy / DA↔SA split and the full memo-as-storage path:
  `PersistenceManager.useMemo`, the `useMemo` parameters on new/rebuild constructors,
  and the `ProcessUpsertDataAttribute` branch that builds a memo map and calls
  `UpsertMemo`. The unified batch method updates only persistence and indexed state.
- A missing `AttributeWrite.value` is invalid. Explicit null deletes persistence;
  deleting an absent persistence key is successful. When that same write has enabled
  `IndexConfig`, send nil for its current backend `index_key`; otherwise indexed
  state is untouched. Reject empty and duplicate logical keys before checking locks.
  Never recover old index metadata from persistence or the provider.
- `InterpreterWorkflowInput.init_attributes` and `ContinueAsNewDump.attributes` are
  `KV`, not `AttributeWrite`. `NewPersistenceManager` initializes new and CAN-restored
  runs from ownership-transferred `[]*dexpb.KV` and rejects invalid or duplicate
  entries.
- The mutation entrypoint is
  `ApplyAttributeWrites(ctx, writes) (applied bool, err error)`.
  Validate the whole batch and build indexed updates only from each current write.
  Send one backend upsert, then commit the persistence values. A mapper/backend error
  leaves the persistence map unchanged. Process enabled `IndexConfig` even when the
  stored value is unchanged. Do not track or validate backend index-key ownership;
  repeated writes targeting the same backend key resolve in declaration order.
- The validator checks `CanLockKeys(keys)`. The handler calls
  `LoadAttributes(ctx, keys)`, which awaits all keys becoming unlocked, locks them,
  and returns all attributes. `UnlockKeys(keys)` releases them. Locks are run-local
  and never enter the CAN dump.
- The single attribute batch method returns `applied=false` without mutation if any
  written key is locked.
  The existing step or signal coroutine waits for all attribute locks to release,
  then applies the **entire** originating result without another workflow yield.
  It must not expose half of that result early. Starting a Wait/Execute worker
  activity is deferred while any attribute lock exists because those requests carry
  the full attribute snapshot. Read-only queries remain non-locking.
- **GetAttributes query handler** (`queryHandler.go`) retyped here alongside the store:
  serve `GetAttributesQueryRequest` → proto response (feeds the `GetAttributes` RPC).
  Queries return retained immutable values, perform no hydration/activity/yield, and
  sort/deduplicate requested keys before constructing repeated output. `all_keys=true`
  ignores `keys`, matching the public proto contract; missing keys are omitted.

### Step request / counter / output

- Rename types/files to Step; no aliases.
- `StepRequest` contains exactly one owned `*StepMovement` start or
  `*StepExecutionResumeInfo`. `RebuildStepRequestQueue` appends start entries in
  repeated-field order, then resume-map entries by sorted execution id. `TakeAll`
  transfers the current queue slice and resets it without cloning proto messages.
- `StepExecutionCounter` creates per-step-type ids as `<stepType>-<startedCount>`,
  retains the proto counter maps across CAN, counts only new starts toward the
  run/restored total, and updates the sorted active-step search value according to
  `FlowConfig.active_step_search_mode`. A backend error commits no local count change.
- `OutputCollector` owns the two StartFlow whitelist sets, a
  `byExecutionID` completion map, a `firstExecutionIDByStepType` selection map, and
  stable ordered results.
- When a whitelisted step type starts for the first time, bind that type to its
  monotonic execution id. Later executions cannot replace it even if they finish
  earlier. An explicitly whitelisted execution id remains independently registered.
- On completion, retain only registered targets. Always store a
  `StepCompletionOutput` marker, even when `completed_step_output` is absent. Lookup
  distinguishes “not completed” from “completed with nil output”.
- CAN `step_outputs` contains the bounded retained markers. Rebuild the execution-id
  index and type selection from retained markers plus in-flight
  `StepExecutionResumeInfo`; emit final results by parsed execution-number suffix,
  then execution id as the deterministic tie-break.

**Review gate:** `make -C server phase4MatchingTests phase4S2Tests` is green; touched
store files are free of `dexpb`/`compatibility`; every state-changing persistence
method handles mapper/backend errors. Root-package tests run at S5 after the stacked
migration compiles.

---

## S3 — Providers + timers + worker wiring

**Files:**
[`temporal/workflowProvider.go`](../../../server/service/interpreter/temporal/workflowProvider.go),
[`cadence/workflowProvider.go`](../../../server/service/interpreter/cadence/workflowProvider.go),
[`timers/`](../../../server/service/interpreter/timers/),
[`temporal/worker.go`](../../../server/service/interpreter/temporal/worker.go),
[`cadence/worker.go`](../../../server/service/interpreter/cadence/worker.go),
[`activityImpl.go`](../../../server/service/interpreter/activityImpl.go), and delete
[`env/env.go`](../../../server/service/interpreter/env/env.go).

- Temporal implements the three typed registrations on `WorkflowProvider`.
  Long waits use common `Await`/`Now` without a durable timeout timer. Cadence
  registrations are no-ops; the API rejects unsupported calls before dialing.
- Replace package globals with constructor-owned objects:
  - `Activities` contains the backend `ActivityProvider`, worker pool, internal
    client, blob store, UnifiedClient, captured event handler, and pointers to the
    exact config sections it needs. Register receiver methods on the backend worker;
    receiver methods never read the mutable `event.Handle` package variable.
  - `InterpreterWorker` owns immutable worker config, DataConverter, and startup/
    shutdown dependencies. Constructors panic on nil required dependencies.
  - Remove `env.SetSharedEnv`, every `env.Get*`, `activityProviderRegistry`, and init
    registration. Validate activity input `backend_type` matches the worker backend.
  - Do not pass `config.Config`. `Activities` receives `*config.ApiConfig`,
    `*config.ExternalStorageConfig`, and `*config.InterpreterActivityConfig`.
    Backend worker constructors receive their own `*config.TemporalConfig` or
    `*config.CadenceConfig`, plus `*config.Interpreter` only where fields outside the
    backend section are actually consumed.
  - Remove memo-as-storage-only `memoEncryption` / memo-converter interpreter-worker
    parameters. Legitimate system Memo conversion remains owned by the backend API
    client/bootstrap DataConverter path.
- Activity outputs obey one contract: success sets `response` only; an expected
  non-retryable worker/API failure sets `InterpreterError` only; both/neither is an
  invariant violation at the workflow call site when the activity Go error is nil.
  Transient infrastructure failures return a Go activity error so backend retry
  policy applies. Never return a successful proto error envelope and a Go error
  together.
- `InvokeWaitForMethod` validates the untrusted Worker response inside the activity:
  condition entries are non-nil; non-empty condition ids are globally unique;
  ALL/ANY allow omitted ids; ANY_COMBINATION requires every condition id. Channel
  names are non-empty; timer durations and channel bounds are non-negative; Worker
  timers do not set the server-owned absolute timestamp. ALL/ANY reject combinations;
  ANY_COMBINATION requires a non-empty combination list whose entries contain
  declared, unique ids. Bounds satisfy the Phase 0 ordering rules. A
  violation returns `InterpreterError(InvalidArgument)` with a nil Go error, so it
  neither triggers ASYNC fallback nor reaches workflow code as a raw validation
  error.
- Always greedy timers: keep `greedyTimerProcessor` + scheduler; delete
  `simpleTimerProcessor`, `optimize_timer`, and selection branches.
- Define stable scheduler ordering by `(firing timestamp, step_execution_id,
  timer-condition declaration index)` so equal deadlines never depend on pointer/map
  order. A losing ANY/combination removes its pending timer; fired/skipped results
  retain their status for `ConditionResults`; stale skip signals remain in CAN until
  successfully matched.
- Port scheduler tests without `time.Sleep`: equal deadlines, skip-before-register,
  skip-vs-fire, loser cancellation, and restore/reschedule after CAN.
- Retype timer APIs to `TimerCondition` / `InternalTimerStatus` proto enums.
- Immediately after a successful WaitFor activity output is validated, convert each
  `TimerCondition.duration_seconds` in place to
  `firing_unix_timestamp_seconds = provider.Now(ctx).Unix() + duration`, then clear
  the relative duration. Workers must return a zero absolute field. CAN carries the
  normalized `WaitingCondition`; restore uses its absolute timestamps and
  `completed_timer_conditions` (`InternalTimerStatus`), never recomputing duration.
  Reject missing absolute timestamps and `UNSPECIFIED` statuses as trusted
  corruption. Do not rerun the Worker-response validator on a restored normalized
  condition; restore has a separate trusted-snapshot validator for absolute fields.
- Retype **GetCurrentTimerInfos** and **GetScheduledGreedyTimerTimes** query handlers
  to proto here. They are read-only and construct repeated results in stable order.
- Temporal workers inherit the Phase 2.5 DataConverter from their configured
  client. Set it explicitly on Cadence worker Options. Client/worker codec chains
  match; search attributes continue through the backend-native mapper.
- Confirm WaitForStateCompletion workflow is deleted and unregistered (Temporal
  already dropped registration in Phase 3 — remove remaining defs).

**Review gate:** provider/worker diffs contain no global dependency setter/registry
and no default DataConverter call. Backend registration names are explicit and
stable. Compilation completes at S5 because provider packages import the stacked
root interpreter package.

---

## S4 — Orchestration (`workflowImpl` + signalReceiver)

**Files:**
[`workflowImpl.go`](../../../server/service/interpreter/workflowImpl.go),
[`signalReceiver.go`](../../../server/service/interpreter/signalReceiver.go),
entrypoints
[`temporal/workflow.go`](../../../server/service/interpreter/temporal/workflow.go)
/
[`cadence/workflow.go`](../../../server/service/interpreter/cadence/workflow.go).

- Keep the monolithic `InterpreterImpl` loop and its existing local state from the
  baseline commit. Retype that structure in place; do not replace it with a runtime
  object or a new state machine. Extract only callbacks that must become methods
  under the repository's stateful-closure rule.
- Interpreter input/output = `*dexpb.InterpreterWorkflowInput/Output`.
- Initial and CAN-restore construction of `PersistenceManager` takes no
  `input.UseMemoForDataAttributes` argument; that field has no proto replacement.
- Condition wait threads (rework of `workflowImpl.go:740-828`): each thread `Await`s a
  **peek** predicate only (`matched || commandReqDoneOrCanceled`), and **must not**
  `Retrieve`. After the central `Await` on `IsDeciderTriggerConditionMet(...)` selects
  the trigger, call the decider's single commit (S2) once, from this one thread, to
  consume the winners' messages. This removes the eager per-thread `Retrieve`
  (`:768`/`:806`) and makes CAN preservation trivial (nothing half-consumed at
  snapshot time — the store round-trips via `channel_received`).
- Activity calls: `InvokeWaitForMethod` / `InvokeExecuteMethod` / etc. with
  single proto activity messages (include `BackendType` inside message). Resolve
  wait/execute durability independently, validate exactly one of response/error, and
  translate `InterpreterError` without JSON wrapping. Every returned Go error is
  handled; no `_ =` persistence/event mutation remains. For a successful WaitFor
  response, first validate that every Worker-produced timer has a zero absolute
  field, then immediately call
  `timers.NormalizeTimerConditionsFromActivityOutput(provider.Now(ctx), waitingCondition)`
  before any workflow yield, timer registration, result publication, or snapshot
  construction. This is the only relative-to-absolute conversion point.
- Apply each worker response atomically from its existing step coroutine. If an
  attribute key is locked, that coroutine waits for unlock before applying
  attributes, channel publications, and next-step enqueueing together. Record
  events remain in the activity result or system-signal history payload. Do not add
  a FIFO commit queue. Do not dispatch a new full-attribute worker request while any
  lock is held.
- Keep the existing per-step cancellation bool used by condition waiters. Backend
  child contexts are unnecessary. Once its trigger, failure, or CAN wins, set the
  bool and remove pending timers so predicates wake. An in-flight
  activity finishes normally; discard its result after a terminal request. CAN
  awaits thread-count convergence without `time.Sleep`.
- System signals: keep RPC/config/fail/skip-timer/trigger-CAN. Signal handlers
  update the existing request flags; the main workflow loop performs terminal exits.
- `SkipTimer` requires `timer_condition_id` or optional
  `timer_condition_index`. Query current timer infos and require a pending match
  before sending its system signal; condition ID wins when both are provided.
- On failure, set the existing error variable. Cooperative waits observe it and the
  main loop returns the accumulated outputs with that error.
- Send each `PublishToChannel` batch in one `ExecuteRpcSignalRequest`, then
  publish its ordered messages into channel store. Drain pending RPC signals
  before a CAN/conditional-close snapshot.
- Port conditional close completely: validate close type/channel names; drain pending
  PublishToChannel messages before testing emptiness; graceful close waits for
  step queue/in-flight executions, while force close returns immediately. Apply
  `close_input` without fabricating a new step execution.
- Drop `compatibility.*` and all remaining LoadingPolicy / dual start-API /
  waitForKey paths.
- Finish deleting leftover `IsAfterVersionOf*` call sites (always-latest).

**Review gate:** no per-condition `Retrieve`, no stateful orchestration closure, and
no new step can start after a terminal request. The root package is expected to
compile only after S5 migrates updater/query/CAN files.

---

## S5 — Sync updates, CAN proto, InternalService dump hook

**Files:**
[`workflowUpdater.go`](../../../server/service/interpreter/workflowUpdater.go)
(expand/rename),
[`continueAsNewer.go`](../../../server/service/interpreter/continueAsNewer.go),
[`queryHandler.go`](../../../server/service/interpreter/queryHandler.go), API
[`InternalService`](../../../server/service/api/) dump handler if still stubbed.

### InvokeRPC update

- Full attributes; no LoadingPolicy. Empty `lock_attribute_keys` keeps the existing
  non-locking signal+query path on both backends.
- The API owns a WorkerService connection pool built from
  `InterpreterActivityConfig`. The non-locking path queries `PrepareRpc`, invokes
  WorkerService directly, signals mutations as one `ExecuteRpcSignalRequest`, and
  closes the pool during API shutdown.
- A non-empty key list requires Temporal synchronous updates. Cadence returns
  `codes.Unimplemented` at the API before query/signal/worker calls.
- Validator: reject empty keys, normalize duplicates, sort keys, and read the lock
  table only. It cannot mutate/yield. Any contended key returns
  `RPC_ACQUIRE_LOCK_FAILURE`/`Aborted`.
- Handler: increment CAN inflight, call `LoadAttributes` to await, lock, and load,
  and defer conditional unlock plus inflight decrement. Pass full immutable
  attributes and channel infos into the worker request only after lock acquisition;
  do not issue a separate `PrepareRpc` query for the locking path.
- Invoke the worker with `ExecuteLocalActivity` regardless of
  `FlowConfig.step_durability`. Sync Update must not route through
  `ExecuteActivity` or fallback to a regular activity. After validating the complete
  worker result, unlock and immediately commit attributes, channels, and next steps
  without a workflow yield. Record events remain in the Local Activity marker. Mark
  the locks released so deferred cleanup does not unlock twice. Worker/activity
  error, cancellation, terminal request, and conversion error all release exactly
  once.
- Validate that every returned `upsert_attributes` key belongs to the normalized
  `lock_attribute_keys` set. An out-of-set write is an invalid worker response and
  applies none of the result. This prevents two disjoint locking RPCs from
  cross-writing each other's keys while both hold locks. If a terminal request becomes
  visible while the activity is running, discard all returned side effects, release,
  and return `FailedPrecondition`.
- Compute one workflow-side RPC budget from positive `timeout_seconds` capped by
  `Api.EffectiveMaxWaitSeconds()`; zero uses the cap. Use it for the activity
  start-to-close/retry envelope so an accepted update remains bounded after caller
  cancellation. Locking RPC finishes before CAN and, unlike long-poll waits, is not
  reissued across runs.

### Wait update lifecycle and CAN preemption

- The API computes one absolute effective deadline from caller context,
  `wait_time_seconds`, and `Api.MaxWaitSeconds`; reject a negative request duration.
  Every retry passes only the remaining duration, rounded up to a whole second so a
  positive remainder never becomes an immediate check. The API context still
  enforces the exact sub-second deadline, and CAN retries never reset the user's wait
  budget.
- Define a private Temporal application-error type such as
  `DEX_CAN_PREEMPTED`. UnifiedClient/API consumes it internally: retry
  `SynchronousUpdateWorkflow` against the main workflow without pinning a run
  (`runID=""` only at the backend client boundary) using bounded context-aware
  backoff until the new run accepts it or the original context/deadline ends. The
  WaitForStepCompletion request remains run-ID-free. Retry only this exact sentinel;
  use a cancelable timer capped by the remaining deadline, not `time.Sleep`, and
  return all other errors immediately. Never expose this sentinel as the gRPC
  response.
- Each handler validates before yielding, increments CAN inflight, and defers
  decrement. It checks the current value once, then waits on
  `match || hasTerminalRequest(...) || IsThresholdMet()`.
- Outcome order after Await returns: a real match wins; terminal returns
  `FailedPrecondition`; timeout returns `DeadlineExceeded` +
  `LONG_POLL_TIME_OUT`; CAN threshold returns `DEX_CAN_PREEMPTED`. This order avoids
  losing a completion/attribute write that became visible in the same workflow task
  as CAN.
- Zero timeout performs one immediate check and never installs a timer. Caller
  cancellation is returned as `Canceled`; an already accepted handler still exits
  through its bounded deadline, terminal request, or CAN predicate.
- Cadence implements no handlers. The API returns `codes.Unimplemented` before
  dialing.

### WaitForStepCompletion

- Require exactly one oneof target. A target absent from the corresponding StartFlow
  retention whitelist returns `FailedPrecondition` immediately; it must not consume
  the full timeout.
- Check `OutputCollector` before Await. Execution-id lookup is exact. Step-type lookup
  waits for the execution id selected when that type first starts, not whichever
  execution happens to finish first.
- Return the retained `StepCompletionOutput` marker even when its output field is
  absent. CAN preemption/retry uses the restored indexes and still needs no run id.

### WaitForAttribute

- Validate condition/equal/key/value before inflight registration. Missing value is
  invalid; explicit null matches an absent key. Reject internal blob-id arms in the
  requested value at the external trust boundary.
- The initial implementation supports only inline stored values: string, object,
  integer, double, bool, null/missing. If the current stored value is an internal
  blob-id arm, return `FailedPrecondition`; do not start a hydration activity.
- Compute a per-attempt workflow deadline once from the remaining duration. Check the
  current value before `Await`, then use a pure predicate that reads the current
  store value and checks deadline, terminal, and CAN state. It creates no durable
  timeout timer and may observe timeout only after another workflow event. Compare
  exact scalar arms; compare objects by encoding and serialized payload bytes.
- TODO: design deterministic hydration for blob-backed attributes without losing
  concurrent writes during the activity yield.

### Queries

- Complete the Phase 2.5 inventory: GetAttributes (S2), current/scheduled timer
  queries (S3), PrepareRpc, DebugDump, and CAN page query.
- Every query takes/returns a concrete proto pointer, is read-only, and performs no
  activity, hydration, lock mutation, or workflow yield. Sort map-derived repeated
  fields. `PrepareRpc` normalizes lock keys but only reports current state.

### Continue-as-new

1. When the threshold wins after terminal-priority reconciliation, stop starting
   steps. Long wait updates observe the threshold and return `CAN_PREEMPTED`; bounded
   locking RPCs finish. Drain step/system threads, then drain declared system
   signals. Re-check fail/complete; a terminal request wins and cancels CAN.
2. Copy the latest `FlowConfig` into continue-as-new input. Assert no held lock,
   uncommitted `MatchPlan`, inflight update, or live step thread remains.
3. `GetSnapshot()` builds a fresh `*dexpb.ContinueAsNewDump`. Sort attribute keys and
   every map-derived repeated field; retain FIFO queue order and monotonic output
   order. Use `proto.MarshalOptions{Deterministic:true}` and a hex checksum over the
   full canonical bytes.
4. Page 0 establishes positive `total_pages` (empty bytes still produce one page)
   and checksum. Each later response must echo requested page number, total pages,
   and checksum. Resolve a non-positive page size to the existing default. Reject a
   negative/out-of-range page number or a page size above `Api.GrpcMaxMessageBytes -
   continueAsNewPageEnvelopeHeadroomBytes`, where the named headroom constant is
   1024 bytes.
5. `LoadInternalsFromPreviousRun` concatenates pages and `proto.Unmarshal`s once.
   A cross-page checksum mismatch resets the accumulated data and restarts at page
   0, preserving the baseline behavior. Each per-page Local Activity keeps its
   configured retry policy for transport failures.
6. Restore channel FIFO data, step queue/resume info, counters, retained completion
   indexes, attribute KVs, timer/stale-skip state, and latest config.
   Reject duplicate keys and invalid `UNSPECIFIED` internal enums as trusted snapshot
   corruption.

- Wire `InternalService.DumpFlowForContinueAsNew` to the typed CAN page query if still
  stubbed. Reuse the injected internal client; validate response identity and close
  it through worker shutdown ownership.
- Retain the baseline `inMemoryContinueAsNewMonitor` diagnostics. It may affect logs
  only and must not influence workflow decisions. Do not replace it with a durable
  timeout timer.

**Exit (Phase 4 checkpoint):**

```bash
GOWORK=off go -C server build ./service/...
make -C server unitTests 2>&1 | tee /tmp/test-phase4-unit.log
make copyright-check 2>&1 | tee /tmp/test-phase4-copyright.log
git diff --check

if rg -n 'gen/dexpb|service/common/compatibility' \
  server/service/interpreter --glob '*.go'; then
  exit 1
fi

if rg -n 'env\.Get|SetSharedEnv|RegisterActivityProvider' \
  server/service/interpreter --glob '*.go'; then
  exit 1
fi

if rg -n 'useMemoFor(DataAttributes|DAs)|UseMemoForDataAttributes|UpsertMemo|\buseMemo\b' \
  server/service/interpreter --glob '*.go'; then
  exit 1
fi

if rg -n 'time\.Now\(' \
  server/service/interpreter/{workflowImpl.go,workflowUpdater.go,signalReceiver.go}; then
  exit 1
fi
```

Manually inspect every touched Go error return; remove `_ = f()`, `value, _ := f()`,
and `if err == nil { ... }` paths without an explicit failure branch. Confirm no
workflow decision depends on unsorted map iteration. Full integration remains Phase 5.

---

## Tests

- **S1 config:** ownership transfer, full-replacement config update,
  each documented zero default, invalid negative values/unknown enums, and
  wait-vs-execute durability override precedence.
- **Memo removal:** no replacement behavior test. The structural checkpoint must
  prove the provider method, persistence flag/arguments, workflow-input plumbing, and
  test stubs are gone. Phase 5 deletes old memo-as-attribute scenarios while keeping
  WorkerTarget/request-id Memo and encryption coverage.
- Add narrow `phase4MatchingTests` and `phase4S2Tests` Make targets during S2 so the
  pure matching/store code can run while the stacked root package is temporarily
  broken.
- **S2 matching:** Exact N, OneToAll, ZeroToAll, omitted vs explicit zero,
  shared-channel ALL/ANY/combination, competing minima, ZeroToAll beside Exact N,
  multiple feasible combinations, optional ids for ALL/ANY, declaration-index
  identity, deterministic tie-break, FIFO results, and plan-without-commit/
  no-premature-consume.
- **S2 persistence/output:** null deletes existing vs absent key using its current
  `IndexConfig`, duplicate logical keys, shared backend index keys, same-value index
  updates, backend-error rollback, ownership/no-clone behavior, sorted KV query/restore output,
  all-or-none lock acquisition, locked batch rejection without partial effects,
  non-yielding whole-result commit after unlock, nil completion marker, explicit-id
  lookup, first-started type selection, CAN index rebuild, stable queue restore, and
  active-step counter modes.
- **S3:** Temporal and Cadence `ASYNC` execution falls back from a Local Activity Go
  error to one regular activity, while `SYNC` and successful Local Activity results
  execute once. Activity validation covers negative bounds/timer duration, nil
  entries, optional ids for ALL/ANY, required ids for ANY_COMBINATION,
  duplicate/unknown ids, invalid combinations, and Worker absolute-field rejection.
  Also cover timer equal-deadline ordering,
  skip-before-register, skip-vs-fire, loser cancellation, immediate
  duration-to-deadline normalization, CAN restore, corrupt unnormalized restore
  rejection, and worker/client DataConverter configuration. No `time.Sleep`; use
  deterministic workflow test time/Eventually.
- **S5 focused unit edges:** WaitForAttribute inline comparison arms and stored-blob
  rejection; update await outcome precedence; locking-RPC deadline after caller
  cancellation; locking InvokeRPC always uses one Local Activity regardless of flow
  durability and never falls back; locking-RPC out-of-set write rejection and
  terminal-during-activity; CAN canonical bytes, empty/split pages, invalid page
  bounds, checksum/total-page mismatch, three-restart exhaustion, and corrupt
  snapshot rejection. These are isolated workflow/codec edges that integration
  cannot exercise reliably.
- Keep restored `activityImpl_test.go`. Do not add a broad mocked interpreter suite;
  Phase 5 integration covers Temporal/Cadence lifecycle, Complete/Fail/CAN races,
  update retry across CAN, lock contention with already-inflight and newly-ready
  activities/SetAttributes, whole-result ordering after unlock, conditional close,
  and exact application-error type/details in failure events.

## Documentation

- Update [`docs/design/idl-renames.md`](../idl-renames.md) only if Phase 4
  introduces new internal names beyond the existing Internal serializable types
  table.
- Keep the parent plan's Phase 4 summary synchronized with this plan's
  CAN-preempt/retry behavior, Temporal-only update handling, first-started step-type
  selection, fail-fast locking, and Cadence locking-RPC `Unimplemented` decision.
- Note in [`server/CONTRIBUTING.md`](../../../server/CONTRIBUTING.md):
  interpreter DataConverter configuration/codec chain must match API client;
  WaitForStep/Attribute and locking RPC are Temporal-only; activities/workflows use
  constructor injection and no package-global runtime environment; Memo is reserved
  for worker-target/request-id metadata and never attribute storage.

## UI/UX

- N/A: no in-repo web UI.

---

## Explicitly out of this phase

- Porting `server/integ` (Phase 5).
- Replay history recapture (Phase 6).
- SDK migration (deliberately outside this server-rewrite phase).
- FlowID-sticky worker LB (deferred).
- Lazy attribute loading (deferred; keep eager hydrate).

## Substack checklist

| ID | Scope | Status |
|----|-------|--------|
| S1 | common contracts + delete UpsertMemo + typed update registration + durability resolution + shared workflow names + FlowConfiger + GlobalVersioner v1; regenerate mocks | implemented and reviewed; legacy call sites migrate in S4/S5 |
| S2 | pure MatchPlan/central commit + unified non-Memo persistence/owner locks + step queue/counter/retention indexes + GetAttributes | implemented; isolated matching/store gates green, root call-site migration waits for S4/S5 |
| S3 | constructor-owned Activities/Workers; delete env/registry and memo-storage wiring; Temporal update adapter; greedy timers + timer queries; matching DataConverter config | implemented; isolated gates green, root integration waits for S4/S5 |
| S4 | baseline InterpreterImpl loop + remove input memo flag + single-proto/durability activity calls + system/channel signals + conditional close | implemented; service and server entrypoint build |
| S5 | locking/non-locking InvokeRPC + Wait updates/CAN retry + all queries + canonical CAN paging/restore + InternalService + full Phase 4 gates | implemented; focused gates and full unit suite green |
