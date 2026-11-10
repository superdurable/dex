# Dex Core Semantics (Step 0 of the expressiveness proof)

Status: revision 9 (after three adversarial reviews, the owner decisions of
2026-11-09 on the review of the Lean statements, the owner decision of
2026-11-10 on unforgeable handles, and its completion for the environment's
input, a wording fix after the review of the safety proof, and the failure
model of Section 11; see [Review log](#10-review-log)). The Lean development
is in `formal/lean/`. It formalizes revision 8. Revision 9 leaves every
failure-free rule unchanged.

Base commit: `9b37482ed` on `main`. Every `path:line` in Sections 0 to 10 is
relative to the repository root at that commit, except where a citation is
marked "at `55721e878`". Section 11 and the revision-9 changes in Sections
1.3, 3.4, 4.4, 4.5, and 7 cite commit `55721e878` on `main`.

## 0. Purpose and conventions

This document fixes the formal semantics of the Dex core programming model.
A later Lean development will use it to state one theorem:

> Every well-formed program of a small concurrent imperative language **L**
> translates into a Dex Flow that is trace-equivalent to it, assuming no
> failures.

Sections 1 to 10 assume no failures: no crashes, retries, or redelivery.
Section 11 adds a failure model for a fragment of Dex, and states a second
theorem: failures change no observable behavior except that external
effects may repeat (see [Failures](#11-failures)).

[owner, 2026-11-09] The theorem covers **well-formed** L programs only. A
program is well formed when no run of it, under any input and any schedule,
has a thread that calls `wait`, `signal`, or `broadcast` without holding the
condition variable's mutex, or calls `unlock` on a mutex it does not hold
(Section 8.1). A program that can do either is outside the theorem's domain,
as a program with a data race is outside a theorem about data-race-free
programs.

Sources of truth, in order:

1. The server-zd apply state machine, `server-zd/crates/dexzd-apply/`. Apply is
   a pure function of state and command (`server-zd/crates/dexzd-apply/src/lib.rs:9-15`).
2. The server-zd engine, `server-zd/crates/dexzd-engine/`, where the semantics
   needs state that apply does not hold (Section 3.3).
3. The wire protocol, `protos/dex.proto`, and the persistent formats in
   `server-zd/proto/dexzd/v1/`.
4. Concept docs in `docs/content/concepts/`, and the Go SDK in `sdk-go/dex/`,
   for names only.

When the docs and the code disagree, the code wins. Section 7 records each
disagreement. One rule deliberately follows a fix that is not merged yet; it
is marked **follows #1450** (Section 4.7, Open question 3).

Markers used in this document:

- **[code]** The claim cites the implementing code.
- **[assumption]** The claim is a modeling choice or an inference without
  direct code evidence. Appendix A lists every one.
- **[decision]** The claim is a choice between defensible options. The
  alternative is named.
- **[owner]** The repository owner decided it.

Notation:

- `f` is a Flow ID, `e` a Step execution ID, `T` a Step type, `c` a Channel
  name, `k` an Attribute key, `r` an RPC name, and `v` a value.
- `m ∈ {W, E}` is a Step method: WaitFor or Execute.
- `⊥` means absent. `xs ++ ys` concatenates lists. `M[k ↦ v]` updates a map.
- `p(f)` is the partition of Flow `f`. `t_p` is the clock of partition `p` in
  microseconds. `seq` is the run sequence number; every committed command of a
  run takes the next `seq`.

## 1. Scope

### 1.1 In the core calculus

| Construct | Why it is in the core |
| --- | --- |
| Flow run, start Step, Flow close and result | The unit of execution and of observation. |
| Step execution with optional WaitFor and required Execute | The only place user code runs, apart from RPCs. |
| Step input and Step execution locals | Per-execution data passed between WaitFor and Execute. |
| StepDecision: GoTo/GoToMany, GracefulComplete, ForceComplete, ForceFail, DeadEnd, ForceCompleteIfChannelsEmpty, CancelSteps, CancelSiblingSteps | Control flow. `protos/dex.proto:1589-1619`. |
| Waits: Timer, Channel (bounds `at_least`/`at_most`), SubFlow, combined by AllOf, AnyOf, AnyCombinationOf; empty wait | Blocking and synchronization. `protos/dex.proto:1621-1699`. |
| Attributes (key-value store of one run), including AttributeMap entries | Shared mutable state. AttributeMap entries are Attributes with keys `map/instance` (`server-zd/crates/dexzd-apply/src/steps/attempt_snapshot.rs:145-158`). |
| Attribute locks on Step methods and RPCs | Mutual exclusion. |
| Channels (FIFO queues of one run), including ChannelMap instances | Messages, external input, and synchronization. |
| RPCs (client-invoked handlers that read and write the run and may start or cancel Steps) | Specified here, but outside the core theorem's admissible environment [owner] (Section 5.3). |
| SubFlows (child Flows started by a wait, result delivered to the parent) | Calls across Flows. |
| Client inputs: StartFlow, PublishToChannel, SetAttributes, DeleteChannelMessage, StopFlow, SkipTimer | The environment's inputs. Only StartFlow and PublishToChannel are admissible for the theorem (Section 5.3). |
| Per-partition clocks and timer firing | Time. |

### 1.2 Derived (expressible in the core, kept out of the rules)

| Construct | Treatment |
| --- | --- |
| `Until(cond)` | Equals `AllOf(cond)` (`sdk-go/dex/condition.go:40-43`). |
| `SkipWaitImmediately` and Steps without WaitFor | An empty wait, or `skip_wait_for`; both go straight to Execute (Section 4.2). |
| ChannelMap and AttributeMap | Families of Channels and Attributes keyed by a string. Dynamic names are what the L translation needs for per-instance queues. |

### 1.3 Excluded

| Construct | Reason |
| --- | --- |
| Flow timeout (all policies) and the timeout handler Step | Not derivable from user Steps. FAIL closes the Flow directly as Failed with FLOW_TIMEOUT, CANCEL closes it as Canceled, and HANDLER creates the system Step `sys:timeout_handler-1` (`server-zd/crates/dexzd-apply/src/command_handlers/step_commands.rs:211-301`). SkipTimer has its own Flow-timeout branch (`server-zd/crates/dexzd-apply/src/steps/wait_conditions/timer_skip.rs:38-42`, `timer_skip.rs:61-81`). The theorem's programs and environment set no Flow timeout (`flow_timeout_seconds = 0`), so no Flow timeout timer is ever armed (`server-zd/crates/dexzd-apply/src/command_handlers/flow_start.rs:249-260`). |
| UpdateFlowConfig | Changes the run's FlowConfig, including the Worker target and Step durability (`server-zd/crates/dexzd-apply/src/command_handlers/client_writes.rs:366-413`, routed at `server-zd/crates/dexzd-apply/src/command_handlers/mod.rs:416-418`). The run config is fixed for the theorem: no UpdateFlowConfig input. |
| Stream | Not in apply at all. It is in server memory only, lossy by capacity, and a Step write is not rolled back (`docs/content/concepts/primitives/stream.mdx:29-39`). It has no durable semantics to translate into. |
| Retries, backoff timers, failure policies (`PROCEED_ON_FAILURE`, `PROCEED_TO_CONFIGURED_STEP`), heartbeats, method timeouts, attempt return | Failure handling. Out of scope for Sections 1 to 10. Section 11 adds them for the fragment it covers, under the hypotheses H1 to H4 (Section 11.1). |
| Durability SYNC/ASYNC | Changes only what survives a crash. [assumption] In a failure-free run both produce the same committed log, because the speculative overlay is checked byte for byte against the committed outcome (`server-zd/crates/dexzd-engine/src/flow_coordination/registry.rs:326-330`). Section 11 covers SYNC only (D2). |
| Time travel (ResetFlow), history, history offload, search projection, Attribute Sync, hard-delete Attributes, permission mappings, `record_events` | Operations, storage, and projections. Attribute Sync only adds stutter before a natural close (Section 4.9). |
| Repeated request IDs | Not only a retry concern. Two mechanisms exist, and they differ. SetAttributes, DeleteChannelMessage, and RPCs use a per-run request table: apply replays the recorded reply for a repeated request ID with the same content, and rejects one with different content, even without failures (`client_writes.rs:41-65`, `client_writes.rs:138-147`, `client_writes.rs:260-268`, `server-zd/crates/dexzd-apply/src/command_handlers/rpc_committed.rs:58-74`). The table keeps at most a fixed number of entries per run and evicts the oldest; it has no time limit (at `55721e878`: `client_writes.rs:80-95`, `server-zd/crates/dexzd-apply/src/state_access/run_rows.rs:353-376`). StartFlow does not use that table. A StartFlow whose Flow ID already has a run is rejected with ALREADY_EXISTS under the default reuse policy. Only when it sets `ignore_already_started_error` and carries the request ID that started the latest run does it return that run instead. That StartFlow is still a log entry, but it writes no run row and takes no run `seq` (at `55721e878`: `flow_start.rs:110-131`; Section 11.3, [START-RETRY]). PublishToChannel carries no request ID. [assumption] Every modeled input carries a globally fresh request ID, or none. Section 11.3 adds client retries. |
| Flow ID reuse policies and run IDs | [assumption] Every L program run starts a fresh Flow ID. SubFlow IDs are the exception; see Open question 13. |
| Retention and run purge after close | (Added in revision 9; citations at `55721e878`.) Closing a run arms a retention timer, by default 7 days after the close (`server-zd/crates/dexzd-apply/src/command_handlers/flow_close.rs:400-424`, `server-zd/crates/dexzd-engine/src/flow_coordination/flow_coordination_config.rs:111`). When it fires, the run is marked purging (`server-zd/crates/dexzd-apply/src/cross_flow/run_purge.rs:73-98`), and a later RunPurge command removes the run from the Flow directory and deletes all its rows (`run_purge.rs:216-228`). [assumption] The model keeps a closed run in `R` forever and has no rule for either step. This changes no event under `E_adm`. The only later inputs of `E_adm` are publications. While the run is purging, apply rejects one with NOT_FOUND and FLOW_NOT_EXISTS (`run_cursor.rs:114-119`, `run_cursor.rs:386-392`), as for any closed run (`client_writes.rs:213-214`). After the purge the engine finds no run and rejects it with the same code and sub-status before any command is proposed (`server-zd/crates/dexzd-engine/src/flow_coordination/read_operations/state_rows.rs:122-126`, `server-zd/crates/dexzd-engine/src/flow_coordination/flow_coordination_error.rs:104-110`). Only the message text differs, and the model's event `rejected(pub(f, [(c, v)]), NOT_FOUND)` does not carry it. The purge also frees the Flow ID, but `E_adm` has only one start. |
| Resource limits (`max_value_bytes`, `max_run_state_bytes`, `max_active_step_executions`, `max_child_flow_result_bytes`) | Hitting a limit turns into a rejection or an attempt failure (`server-zd/crates/dexzd-apply/src/command_handlers/mod.rs:226-280`). [assumption] Limits are never hit. |
| Worker response validation | An invalid response becomes a non-retryable attempt failure. Here it is a well-formedness constraint on user code (Section 2.5). |

## 2. Abstract syntax

### 2.1 Programs

A Dex program is one Flow definition plus the Flow definitions its SubFlows
name.

```text
Program   P   ::= { FlowType ↦ FlowDef }
FlowDef   F   ::= ⟨ steps : StepType ↦ StepDef,  rpcs : RpcName ↦ RpcDef ⟩
StepDef   σd  ::= ⟨ waitFor : WaitForFn?,  execute : ExecuteFn ⟩
RpcDef    ρd  ::= ⟨ handler : RpcFn ⟩
```

Step options travel with each Step movement, not with the Step definition, in
the wire format (`protos/dex.proto:1611-1619`); the server reads them only
from there (`server-zd/crates/dexzd-apply/src/steps/step_creation.rs:73`).
[assumption] The SDKs fill them from the Step definition plus per-movement
overrides ("A StepDecision can override the options for one Step execution",
`docs/content/concepts/step/index.mdx`, StepOptions). The core keeps only
these options (`protos/dex.proto:452-475`):

```text
Opts  o  ::= ⟨ skipWaitFor : Bool,
               lockKeys   : {W ↦ [Key], E ↦ [Key]},
               loadMaps   : {W ↦ [Sel], E ↦ [Sel]},     -- AttributeMap selections
               loadChans  : {W ↦ [Sel], E ↦ [Sel]} ⟩    -- Channels whose messages are loaded
```

### 2.2 User code

User code is opaque. Each handler is a total function. [decision] Handlers
emit external effects but receive no external input; every external input to a
Flow arrives through a client input (Section 5). The alternative, handlers
that read external services, needs an oracle in the trace and is left to the
L side.

```text
WaitForFn : Ctx × Val × Snap               → Fx* × WaitResp
ExecuteFn : Ctx × Val × Snap × Locals × CR → Fx* × ExecResp
RpcFn     : Ctx × Val × Snap               → Fx* × RpcResp

Ctx      ::= ⟨ flowId, stepExecutionId, fromStepExecutionId, ... ⟩   -- also ctxA (Section 11.1, H3)
Snap     ::= ⟨ attrs : Key ⇀ Val,  sizes : Chan ⇀ ℕ⁺,  loaded : Chan ⇀ [Msg] ⟩
Effects  ::= ⟨ writes : [(Key, Val | null)],
               deletes : [(Chan, MsgId)],
               pubs    : [(Chan, Val)] ⟩
WaitResp ::= ⟨ Effects, locals : [(LKey, Val | null)], wait : Wait ⟩
ExecResp ::= ⟨ Effects, locals : [(LKey, Val | null)], decision : Decision ⟩
RpcResp  ::= ⟨ Effects, output : Val?, rpcDecision : RpcDecision? ⟩
```

Wire forms: `protos/dex.proto:1501-1511` (WaitFor), `protos/dex.proto:1546-1556`
(Execute), `protos/dex.proto:1579-1587` (RPC). Each `Fx` is one external effect,
such as an HTTP call to another system. It is observable (Section 5).

### 2.3 Waits

```text
Wait   W  ::= ε                                         -- empty: Execute at once
           |  ⟨ kind : All | Any | AnyComb,
                timers : [⟨id?, d⟩],                    -- d in whole seconds
                chans  : [⟨id?, c, lo?, hi?⟩],
                subs   : [⟨id?, FlowType, StepType, Val, Opts⟩],
                combos : [[CondId]] ⟩                   -- AnyComb only
```

Channel bounds (`server-zd/crates/dexzd-apply/src/steps/wait_conditions/condition_plan.rs:139-153`):

| SDK form | `lo` | `hi` | Proceeds when | Consumes |
| --- | --- | --- | --- | --- |
| ForOne (`lo = hi = ⊥`) | 1 | 1 | ≥ 1 queued | exactly 1 |
| ForN(n) (`sdk-go/dex/channel.go:124-127`) | n | n | ≥ n queued | exactly n |
| AtLeast(n) | n | ∞ | ≥ n queued | all queued |
| AtMost(n) | 0 | n | at once | up to n |
| AtLeastAtMost(a, b) | a | b | ≥ a queued | up to b |

### 2.4 Decisions

```text
Decision D ::= ⟨ moves : [Move],  close : Close?,
                 cancel : [StepType],  cancelSib : [StepType] ⟩
Move       ::= ⟨ T, v?, o ⟩
Close      ::= GC(v?)            -- GracefulComplete
            |  FC(v?)            -- ForceComplete
            |  FF(s?)            -- ForceFail, s a string
            |  DE                -- DeadEnd
            |  FCE([Chan], v?)   -- ForceCompleteIfChannelsEmpty

RpcDecision ::= ⟨ moves : [Move], cancel : [StepType] ⟩
```

`protos/dex.proto:1589-1619`.

### 2.5 Well-formedness of user code results

The engine validates every Worker response before it commits. A response that
fails validation becomes a non-retryable attempt failure, which is out of
scope. So the semantics requires every handler result to be well formed:

- A decision has at least one move or a close
  (`server-zd/crates/dexzd-request-validation/src/worker.rs:195-200`).
- GC, FC, FF, and DE have no moves. FCE has at least one move and at least one
  distinct Channel (`worker.rs:262-310`).
- FF's value is a string. DE has no value (`worker.rs:311-333`).
- An RPC decision has no close and no sibling cancel (`worker.rs:117-136`).
- SubFlow conditions pass `validate_child_start_conditions`
  (`server-zd/crates/dexzd-apply/src/cross_flow/outbox.rs:213`). Otherwise the
  Flow fails with INVALID_USER_FLOW_CODE
  (`server-zd/crates/dexzd-apply/src/steps/step_results.rs:879-901`).

## 3. Configurations

### 3.1 Step executions

```text
StepRec s ::= ⟨ T, n, in : Val?, o : Opts, from : String,
                φ : Phase, inv : Inv?, w : WaitState?, cr : CR?, loc : LKey ⇀ Val ⟩
Phase   φ ::= WF | WT | EX | CO | CA          -- WaitFor, Waiting, Execute, Completed, Canceled
Inv       ::= ⟨ m, ip : P | R, ps : ℕ, keys : [Key], snap : Snap? ⟩   -- P pending, R running
WaitState ::= ⟨ W, fireAt : [t], tdone : [Bool], childIds : [FlowId], sws : ℕ ⟩
CR        ::= ⟨ chans : [⟨id, done, c, [Msg]⟩], timers : [⟨id, done⟩], subs : [FlowResult] ⟩
```

- `e = T-n`, where `n` is a per-type counter of the run
  (`server-zd/crates/dexzd-apply/src/steps/step_creation.rs:40-52`).
- `from` is the creating Step execution, `__start__` for the start Step, or
  `__rpc/<r>` for a Step that RPC `r` started
  (`server-zd/crates/dexzd-apply/src/steps/mod.rs:48`,
  `server-zd/crates/dexzd-apply/src/command_handlers/rpc_committed.rs:148`).
- A Step is **active** when `φ ∈ {WF, WT, EX}`
  (`server-zd/crates/dexzd-apply/src/steps/step_decisions.rs:179-184`).
- The FAILED phase exists (`server-zd/proto/dexzd/v1/state.proto:367-379`) but
  only failures reach it.
- `sws` is the `seq` at which the wait was registered. It orders waiters.

Persistent forms: `StepExecutionRecord` (`state.proto:382-416`),
`StepInvocationRecord` (`state.proto:429-458`), `WaitStateRecord`
(`state.proto:488-498`), `ConditionResultsRecord` (`state.proto:501-506`).

### 3.2 One run (apply state)

```text
Run ρ ::= ⟨ st   : Running | Closed(status),          -- status ∈ {Completed, Failed, Canceled, Terminated}
            pc   : Closing?,                           -- pending close
            gc   : Bool,                               -- graceful completion requested
            outs : [(T, e, Val)],                      -- collected Flow outputs
            S    : StepExecId ⇀ StepRec,
            cnt  : StepType → ℕ,
            A    : Key ⇀ Val,                          -- Attributes
            Q    : Chan → [Msg],  Msg ::= ⟨q, id, v⟩   -- pending messages, ascending q
            K    : Chan → ℕ,                           -- claimed message counts
            L    : Key → ⟨h : InvKey?, ws : [⟨InvKey, ps⟩]⟩   -- Step locks
            Tm   : TimerId ⇀ t,                        -- armed timers and fire times
            SFR  : FlowId ⇀ FlowResult,                -- received child results
            Ob   : [OutboxMsg],                        -- committed cross-Flow messages
            seq  : ℕ ⟩
InvKey ::= (e, m)
```

| Component | Persistent row | Code |
| --- | --- | --- |
| `st`, `pc`, `gc`, `outs` | `RunRecord.status`, `.pending_close`, `.is_graceful_completion_requested`, `.close.results` | `state.proto:97-218`, `state.proto:255-276` |
| `A` | `AttributeRecord` | `state.proto:334-340` |
| `Q`, `K` | `ChannelMessageRecord` keyed by channel and enqueue seq; `ChannelMetaRecord.pending_count`, `.claimed_count` | `state.proto:342-355` |
| `L` | `LockRecord` | `state.proto:528-539` |
| `Tm` | `TimerRecord` | `state.proto:542-545` |
| `SFR` | `SubFlowResultRecord` | `state.proto:601-607` |
| `Ob` | `OutboxRecord` | `state.proto:558-570` |

Enqueue seq `q = seq · 10⁶ + i` for the `i`-th publication of a command
(`server-zd/crates/dexzd-apply/src/command_handlers/flow_data.rs:79`,
`flow_data.rs:530-540`). So `Q[c]` is in commit order, and within one command
in list order.

### 3.3 Engine state that apply does not hold

Apply alone does not determine the semantics. The engine keeps four more
pieces of state, and the rules need them. [code] for each:

```text
Engine η ::= ⟨ RL  : (f, Key) ⇀ RpcId,       -- RPC locks, in memory, never logged
               Rq  : RpcId ⇀ RpcRec,          -- in-flight RPCs
               Tx  : f → ℕ,                   -- admitted transactional RPCs in flight
               Θ   : TaskId ⇀ Task ⟩          -- attempts handed to a Worker
RpcRec ::= ⟨ f, r, v, keys, tx : Bool, admitted : Bool,
             phase : Called | Snapped(Snap) | Ran(Fx*, RpcResp) | Done(Result) ⟩
Task   ::= ⟨ f, e, m, input, snap, locals, cr, phase : Started | Running(Fx*, Resp) | Returned(Resp) ⟩
```

- `RL`: `server-zd/crates/dexzd-engine/src/flow_coordination/rpc_locks.rs:20-51`.
  The comment says "never logged". A lock guard releases its keys when it is
  dropped (`server-zd/crates/dexzd-engine/src/flow_coordination/registry.rs:833-846`).
- `Tx` counts admitted transactional RPCs; the admission guard decrements it
  when dropped (`registry.rs:809-817`). `Tx` drives
  `FlowCommand.is_transactional_rpc_in_flight`, which decides whether a natural
  close is deferred
  (`server-zd/crates/dexzd-apply/src/command_handlers/flow_close.rs:281-292`).
  The engine sets the stamp only on a command that would close the run, and
  counts the admitted transactional RPCs other than the one this command
  commits (`registry.rs:593-615`). The rules write that count as `Tx⁻[f]`:
  `Tx[f]` minus one when the command is the commit of an admitted
  transactional RPC, else `Tx[f]`.
- The timer service decides when `TimersFired` is proposed
  (`server-zd/crates/dexzd-engine/src/effects/timer_service.rs:116-205`).
- The dispatcher decides which startable pending invocation is handed to a
  Worker next (`registry.rs:297-322`).

### 3.4 System

```text
Σ ::= ⟨ τc : Partition → ℕ,       -- per-partition clocks t_p
        P  : Program,
        R  : FlowId ⇀ Run,
        η  : Engine,
        N  : multiset OutboxMsg ⟩  -- cross-Flow messages in transit
```

- [code] There is no global clock. The owner stamps every command with its
  wall clock, strictly increasing per partition, and apply uses only that time
  (`server-zd/proto/dexzd/v1/command.proto:39-40`,
  `server-zd/crates/dexzd-apply/src/lib.rs:11-12`). A Flow and its SubFlows can
  be in different partitions, so their timestamps come from different clocks.
- [assumption] One Flow ID has one run. Run IDs are elided.
- [code] The run attempt is not merely elided. Apply checks every run
  command's `expected_run_attempt` against the run's current attempt and
  rejects a mismatch (at `55721e878`:
  `server-zd/crates/dexzd-apply/src/command_handlers/run_cursor.rs:120-130`;
  a value of 0 skips the check). Only time travel changes the current
  attempt, and time travel is excluded (Section 1.3). So the current attempt
  stays 1, the check never fails, and the rules leave it out. The run attempt
  is a different counter from the attempt number of a Step method, which
  Section 11.2 adds.

## 4. Operational semantics

### 4.1 Shape of the rules

A transition is `Σ --ℓ⃗--> Σ'`, where `ℓ⃗ ∈ Obs*` is the **ordered sequence**
of observable events the transition produces (Section 5). The empty sequence
is written `τ`. A commit can produce more than one event; for example StopFlow
produces its input event and then the close event.

Every rule marked **(commit)** is one apply command. It is atomic: apply
computes all its writes in one transaction, and a rejected command writes
nothing (`server-zd/crates/dexzd-apply/src/command_handlers/mod.rs:90-300`).
Each commit of Flow `f` stamps `now = t_{p(f)}` and takes `seq := seq + 1` for
its run. [assumption] `t_{p(f)}` strictly increases between two commits of
partition `p(f)`; partitions advance independently.

Rules not marked commit are engine or Worker steps. They change only `η`, `N`,
or the clocks.

The conclusions call deterministic meta-functions. Section 4.2 defines them.
Each meta-function runs inside the commit that calls it and in the order
written. A meta-function that emits an event appends it to the transition's
event sequence.

### 4.2 Meta-functions (deterministic, inside one commit)

All functions act on the run `ρ` of the command's Flow `f`.

**`new(T, v, o, from)`** creates a Step execution.
`server-zd/crates/dexzd-apply/src/steps/step_creation.rs:34-115`.

```text
n := cnt[T] + 1;  cnt[T] := n;  e := "T-n"
φ := if o.skipWaitFor then EX else WF
S[e] := ⟨T, n, v, o, from, φ, ⊥, ⊥, ⊥, ∅⟩
pend(e, if φ = EX then E else W)
```

**`pend(e, m)`** makes method `m` pending and queues it on its lock keys.
`server-zd/crates/dexzd-apply/src/steps/invocations.rs:45-138`.

```text
keys := sort(dedup(S[e].o.lockKeys[m]))
for k in keys: if L[k].h ≠ (e,m) then L[k].ws := L[k].ws ++ [((e,m), seq)]
S[e].inv := ⟨m, P, seq, keys, ⊥⟩
```

**`snapshot(e, m)`** is what the attempt reads. It is computed once, when the
attempt starts, after its locks are taken.
`server-zd/crates/dexzd-apply/src/steps/attempt_snapshot.rs:62-143`.

```text
attrs  := { k ↦ A[k] | k has no '/', or k matches a selection in o.loadMaps[m] }
sizes  := { c ↦ |Q[c]| | |Q[c]| > 0 }
loaded := { c ↦ Q[c] | c ∈ o.loadChans[m], prefix selections expanded }
```

The Worker request adds the Step input, the locals, and, for Execute, the
condition results (`server-zd/crates/dexzd-engine/src/effects/method_request.rs:449-545`).

**`release(e)`** ends the current attempt of `e`. It releases every lock the
attempt holds and removes its queue entries.
`server-zd/crates/dexzd-apply/src/steps/invocations.rs:314-390`.

```text
for k in S[e].inv.keys:
  if L[k].h = (e,m) then L[k].h := ⊥
  L[k].ws := L[k].ws without (e,m)
if S[e].inv.ip = R and the end is a cancel: send cancel to the Worker   (env, not state)
```

**`effects(e?, E, locals)`** applies a handler's data effects, in this order.
`server-zd/crates/dexzd-apply/src/command_handlers/flow_data.rs:600-675`.

```text
for (k, v) in E.writes:  A[k] := v  (null deletes k)               -- flow_data.rs:106-230
for (c, id) in E.deletes: remove the pending message id from Q[c]
                          if present; skip it otherwise             -- flow_data.rs:463-481
for (c, v) in E.pubs:    Q[c] := Q[c] ++ [⟨seq·10⁶+i, fresh id, v⟩] -- flow_data.rs:522-583
merge locals into S[e].loc (null deletes)                           -- flow_data.rs:624-663
wake(channels published to)                                         -- flow_data.rs:664-669
```

Writes are blind: they do not compare with the snapshot. Later writes in the
list win.

**`wake(C)`** re-evaluates every Step that waits on a Channel in `C`, earliest
waiter first.
`server-zd/crates/dexzd-apply/src/steps/wait_conditions/mod.rs:140-164`.

```text
for (sws, e) in sort({ (S[e].w.sws, e) | e waits on some c ∈ C }):   -- lexicographic
  eval(e)
```

**`eval(e)`** fires a satisfied wait.
`server-zd/crates/dexzd-apply/src/steps/wait_conditions/mod.rs:335-402`.

```text
if S[e].φ ≠ WT: return
π := plan(S[e].w.W, pending = (c ↦ |Q[c]|), tdone, subsDone = (child ↦ child ∈ SFR))
if π = ⊥: return
unregister(e)                                   -- waiter rows, unfired timers
for (j, count) in π:  msgs_j := first count of Q[c_j];  Q[c_j] := rest;  K[c_j] += count
S[e].cr := ⟨per Channel condition: msgs_j or "not done";  per timer: tdone;  per SubFlow: SFR or RUNNING⟩
S[e].φ := EX;  pend(e, E)
```

Consumed messages leave `Q` at once and are counted in `K` until the Step ends
(`wait_conditions/mod.rs:407-460`).

**`plan(W, progress)`** picks the first satisfied candidate and its exact
consumption. `server-zd/crates/dexzd-apply/src/steps/wait_conditions/condition_plan.rs:34-205`.

```text
candidates(All)     = [ all conditions ]
candidates(Any)     = [ each timer ] ++ [ each channel ] ++ [ each SubFlow ]   -- declaration order
candidates(AnyComb) = [ each combination ], each member ID resolved to the LAST declaration
                      within the FIRST kind that has that ID, kinds tried in the order
                      timer, Channel, SubFlow                                  -- condition_plan.rs:103-129
feasible(cand) ⇔ every timer of cand is done ∧ every SubFlow of cand has a result
                 ∧ per Channel name, the sum of the minimums of cand's conditions on it ≤ pending
consume(cand)  = reserve each minimum first; then, in declaration order, add extras up to each maximum
plan = consume(first feasible candidate), or ⊥
```

An empty wait is satisfied at once with no consumption (`condition_plan.rs:38-43`).

**`register(e, W)`** stores a wait after WaitFor.
`server-zd/crates/dexzd-apply/src/steps/wait_conditions/mod.rs:51-136`.

```text
if W = ε or W has no condition:
  S[e].cr := empty results;  S[e].φ := EX;  pend(e, E);  return
childIds := [ "SubFlow:" ++ f ++ "-" ++ e ++ "-" ++ i | i ∈ indices(W.subs) ]   -- mod.rs:645-647
for each SubFlow condition i: Ob += ChildStart(childIds[i], W.subs[i], parent = (f, e, i))  -- outbox.rs:37-171
for each timer i: Tm[wait(e, i)] := now + d_i · 10⁶                            -- mod.rs:89-108
for each distinct Channel c of W: add a waiter row (c, e) with sws := seq       -- mod.rs:109-122
S[e].φ := WT;  S[e].w := ⟨W, fireAt, tdone = all false, childIds, seq⟩
eval(e)
```

A zero-second timer is armed, not completed. It completes only through
[TIMER-FIRE]. Fire times use the clock of the partition of `f`.

**`finish(e, φ')`** ends a Step as CO or CA.
`server-zd/crates/dexzd-apply/src/steps/step_decisions.rs:277-323`.

```text
release(e) with a cancel end;  S[e].inv := ⊥
drop S[e].in and S[e].loc
unregister(e);  for each claimed message of S[e].cr on c: K[c] -= 1     -- messages are discarded, not re-queued
S[e].φ := φ'
```

The message release is in `wait_conditions/mod.rs:250-281`.

**`cancelTypes(Tg, Ts, from)`** cancels Steps by type.
`server-zd/crates/dexzd-apply/src/steps/step_decisions.rs:251-273`.

```text
for e' with S[e'] active:
  if S[e'].T ∈ Tg  or  (S[e'].T ∈ Ts and S[e'].from = from):  finish(e', CA)
```

**`decide(e, D)`** applies an Execute decision.
`server-zd/crates/dexzd-apply/src/steps/step_decisions.rs:35-152`.

```text
finish(e, CO)                                                       -- :44
if D.cancel ∪ D.cancelSib ≠ ∅: cancelTypes(D.cancel, D.cancelSib, S[e].from)  -- :45-53
goNext := true
case D.close of
  FCE(cs, v): if ∀c ∈ cs: |Q[c]| = 0 ∧ K[c] = 0:                    -- :64-77
                collect(e, v);  close(Completed);  return
              -- otherwise fall through and take the moves
  GC(v):      collect(e, v);  gc := true;  goNext := false          -- :78-84
  FC(v):      collect(e, v);  close(Completed);  return             -- :85-91
  FF(s):      close(Failed(STEP_DECISION_FAILING_FLOW, s));  return -- :92-119
  DE:         goNext := false                                       -- :120
if goNext: for (T', v', o') in D.moves: new(T', v', o', e)          -- :132-149
gcheck()                                                            -- :150
```

**`collect(e, v)`** appends `(S[e].T, e, v)` to `outs` when `v` is not null
(`step_decisions.rs:186-222`).

**`gcheck()`** completes the Flow when graceful completion was requested and no
user Step is active. The timeout handler does not count.
`server-zd/crates/dexzd-apply/src/steps/step_decisions.rs:156-173`.

```text
if gc ∧ st = Running ∧ ¬∃e. S[e] active ∧ e ≠ sys:timeout_handler-1:  close(Completed)
```

**`close(κ)`** is a natural close.
`server-zd/crates/dexzd-apply/src/command_handlers/flow_close.rs:178-197`.

```text
if st ≠ Running or pc ≠ ⊥: return
for e active, ordered by (created seq, e): finish(e, CA)            -- flow_close.rs:307-340
if Tx⁻[f] > 0 (or Attribute Sync entries exist):                     -- flow_close.rs:281-292
  pc := κ;  gc := false;  return                                    -- deferred; st stays Running
record(κ)
```

**`closeNow(κ)`** is StopFlow's close. It cancels every active Step and records
at once, discarding a pending close
(`flow_close.rs:200-212`).

**`record(κ)`** records the close and emits the observable close event.
`server-zd/crates/dexzd-apply/src/command_handlers/flow_close.rs:343-406`.

```text
st := Closed(κ.status);  gc := false
emit closed(f, κ.status, κ.error, outs)                            -- outs as collected, for every status
if ρ has a parent: Ob += ChildCompleted(f, result = ⟨κ.status, outs, κ.error⟩)   -- outbox.rs:401-488
```

### 4.3 Flow start

**[FLOW-START]** (commit) Events `[start(f, T₀, v₀, A₀)]`.

```math
\frac{f \notin \mathrm{dom}(R)}
     {\Sigma \xrightarrow{\;[start(f,T_0,v_0,A_0)]\;} \Sigma[R(f) \mapsto \rho_0]}
```

`ρ₀` is the empty run with `st = Running`, then `A := A₀` applied as writes,
then `new(T₀, v₀, o₀, "__start__")` when `T₀` is not empty.
`server-zd/crates/dexzd-apply/src/command_handlers/flow_start.rs:191-247`.
Without a start Step the Flow runs with no Step and waits for RPCs or a close
(`flow_start.rs:204-205`). No Flow timeout is set (Section 1.3).

[owner, 2026-11-09, kept as taken] The options `o₀` of the start Step are not
part of the event `start(f, T₀, v₀, A₀)`. Under `E_adm` (Section 5.3),
`o₀.skipWaitFor` is true exactly when the definition of `T₀` has no WaitFor,
as the SDKs fill it (Section 2.1), and `o₀` selects no AttributeMap and no
Channel.

### 4.4 Step methods

A Step method is **three** transitions: Start, Run, Complete. Other
transitions can interleave between them. This is the central fact of the
semantics (Section 6).

**[STEP-START]** (commit, `StepAttemptStarted`) Events `τ`.

```math
\frac{S(e).inv = \langle m, P, ps, keys, \bot\rangle \quad
      \forall k \in keys.\; (f,k) \notin \mathrm{dom}(\eta.RL) \;\wedge\;
      \big(L[k].h = (e,m) \;\vee\; (L[k].h = \bot \wedge \mathrm{head}(L[k].ws) = (e,m))\big)}
     {\Sigma \xrightarrow{\tau} \Sigma'}
```

Effect:

```text
for k in keys: L[k].ws := L[k].ws without (e,m);  L[k].h := (e,m)
snap := snapshot(e, m);  S[e].inv := ⟨m, R, ps, keys, snap⟩
Θ[fresh task] := ⟨f, e, m, S[e].in, snap, S[e].loc, S[e].cr, Started⟩
```

- Apply checks the lock queue: `server-zd/crates/dexzd-apply/src/steps/invocations.rs:227-245`,
  `server-zd/crates/dexzd-apply/src/steps/step_results.rs:156-209`.
- The engine also checks the RPC locks before proposing, under the Flow's lock:
  `server-zd/crates/dexzd-engine/src/flow_coordination/rpc_locks.rs:85-125`,
  `registry.rs:297-322`.
- Any startable pending invocation may start. [assumption] The order is
  nondeterministic; Worker availability adds no other constraint. Fairness is
  stated separately (Section 5.5).

**[WORKER-EFFECT]** (Worker) Events `[fx(f, (e, m), a)]`.
A task in phase `Started` computes `(fx⃗, resp) := U_{T,m}(ctx, input, snap, locals, cr)`
and becomes `Running(fx⃗, resp)`. Each step of this rule emits the head of
`fx⃗`. When `fx⃗` is empty the task becomes `Returned(resp)`.

The Worker runs outside apply. No state of `ρ` changes. The Worker receives
the request the engine builds from the task
(`server-zd/crates/dexzd-engine/src/effects/method_request.rs:449-545`) and
answers on the streams `InvokeWaitForMethodOutput` and
`InvokeExecuteMethodOutput` (`protos/dex.proto:1523-1529`,
`protos/dex.proto:1558-1564`).

**[WORKER-ABANDON]** (Worker) Events `τ`. A task whose invocation was canceled
(Section 4.2 `release` with a cancel end) may be dropped at any point, before
or after any of its effects. The cancel commit emits the effect that tells the
Worker to stop (`server-zd/crates/dexzd-apply/src/steps/invocations.rs:376-381`).
Cancellation is cooperative
(`docs/reference/go/flows-and-steps/step-options.mdx:282-294`). Until it is
dropped, a canceled task may keep taking [WORKER-EFFECT] steps, also after its
Flow closed.

**[STEP-COMPLETE-WAITFOR]** (commit, `StepMethodCompleted`) Events `τ`, or
`[closed(...)]` when the Flow closes.

Premise: the task is `Returned(⟨E, locals, W⟩)` and `S[e].inv = ⟨W, R, …⟩`
is the invocation that started this task. Otherwise the command is ignored
and the result is lost
(`server-zd/crates/dexzd-apply/src/steps/step_results.rs:857-872`).

The code's fence is more precise than "the same task". It requires the
Step execution, the method, the method's attempt number, the phase Running,
and the task ID to match (at `55721e878`: `step_results.rs:83-128`,
`step_results.rs:857-872`). Without failures, an invocation has one attempt
and one task, so the two premises select the same results. `Θ` here has no
attempt number and no Backoff phase. Section 11.2 adds both, and
[RESULT-STALE] (Section 11.3) states the full fence.

```text
release(e) with a finish end;  S[e].inv := ⊥             -- step_results.rs:915-925
effects(e, E, locals)                                    -- step_results.rs:927-946
register(e, W)                                           -- step_results.rs:952-958
```

**[STEP-COMPLETE-EXECUTE]** (commit, `StepMethodCompleted`) Events `τ` or
`[closed(...)]`.

Premise as above with `ExecResp ⟨E, locals, D⟩` and method `E`.

```text
release(e) with a finish end;  S[e].inv := ⊥
effects(e, E, locals)                                    -- step_results.rs:960-979
decide(e, D)                                             -- step_results.rs:980-987
```

Note the order: the data effects, including `wake` of Channel waiters, come
before the decision's cancellations and close (Open question 7).

**[STEP-COMPLETE-STALE]** (Worker) Events `τ`. A `Returned` task whose
invocation is no longer running is dropped. Apply ignores the command
(`step_results.rs:863-872`).

### 4.5 Time and timers

**[TICK]** (environment) Events `τ` (or `[tick(p, δ)]` in the timed variant of
Section 5.6).

```math
\frac{\delta > 0}{\tau c \xrightarrow{\tau} \tau c[p \mapsto \tau c(p)+\delta]}
```

Apply never reads a clock; every command carries its partition owner's wall
clock time, strictly increasing per partition
(`server-zd/proto/dexzd/v1/command.proto:39-40`,
`server-zd/crates/dexzd-apply/src/lib.rs:11-12`).

**[TIMER-FIRE]** (commit, `TimersFired`) Events `τ` or `[closed(...)]`.

```math
\frac{\mathit{Due}_f = \{\, i \in \mathrm{dom}(Tm) \mid Tm(i) \le t_{p(f)} \,\} \ne \emptyset
      \qquad D = \text{a non-empty prefix of } \mathit{Due}_f \text{ in index order}}
     {\Sigma \xrightarrow{\tau} \Sigma'}
```

Index order is `(fire time, protobuf bytes of the TimerId)`
(`server-zd/crates/dexzd-keyspace/src/row_keys.rs:303-314`). For each `i` in
`D`, in that order (`server-zd/crates/dexzd-apply/src/command_handlers/step_commands.rs:148-207`):

```text
if i ∉ Tm: skip                                  -- disarmed by an earlier timer of this command
remove i from Tm
case i of wait(e, j):  if S[e].φ = WT ∧ ¬tdone[j]:  tdone[j] := true;  eval(e);  gcheck()
                                                     -- wait_conditions/mod.rs:167-188
```

Why a prefix, and not any subset (Review log, finding 3):

- The timer service scans the committed timer index in order, up to a load
  limit, and fires every due row of a run in one command
  (`server-zd/crates/dexzd-engine/src/effects/timer_service.rs:116-205`,
  `timer_service.rs:208-251`). The load limit cuts the scan, which gives a
  prefix.
- The service skips a row that is in its retry delay
  (`timer_service.rs:123-128`, `timer_service.rs:144-146`). A row enters the
  retry delay only after the service fired it
  (`timer_service.rs:158-184`), and firing waits until the TimersFired command
  commits (`timer_service.rs:283-287`,
  `server-zd/crates/dexzd-engine/src/effects/mod.rs:427-455`). A committed
  TimersFired disarms every timer it names that still exists
  (`step_commands.rs:156-163`). So, without failures, a row in its retry delay
  has already fired, and skipping it never lets a later timer of the same run
  overtake an unfired earlier one. The retry delay only guards against reading
  the index before the commit is visible, and against failed proposals.
- [assumption] So a prefix is exact for failure-free runs. (Revision 9:
  this is false in one window, see Open question 26. The rule is
  unchanged.) The "any non-empty
  subset" abstraction would admit runs the code cannot produce: with Steps A
  and B waiting on `AllOf(Timer, c.ForOne())`, A's timer earlier, both due,
  and one message on `c`, the code always gives the message to A; a subset
  could fire B's timer alone first and give it to B. For an equivalence
  theorem an over-approximation of Dex is not safe in the L ⊆ Dex direction.
- The prefix keeps the AnyOf facts of Open question 12: a due timer still loses
  to a message that commits before [TIMER-FIRE], because firing is late.

Other facts:

- The timer service never fires before its partition's clock reaches the fire
  time (`timer_service.rs:9-11`).
- Apply does not enforce the premise `Tm(i) ≤ t_{p(f)}`. It fires every
  timer the command names that is still armed, whatever its fire time (at
  `55721e878`: `step_commands.rs:147-207`). The premise is a property of the
  engine. The timer service proposes only rows whose fire time is at or
  before the wall clock it read (at `55721e878`: `timer_service.rs:117`,
  `timer_service.rs:131`, `timer_service.rs:147`). The command's stamp is at
  least the wall clock at stamping (at `55721e878`:
  `server-zd/crates/dexzd-engine/src/commit/command_committer.rs:141-144`).
  Without failures, this rule assumes that the wall clock does not step
  backward between those two reads. Section 11.1, D6, states this
  assumption.
- Retry backoff timers share the timer index with wait timers. Without
  failures no retry timer is ever armed. Section 11.3 treats a TimersFired
  that names both kinds.
- Firing lateness is unbounded. Any other commit may come between a timer
  becoming due and [TIMER-FIRE].

**[SKIP-TIMER]** (commit) Events `[skip(f, e, j)]`. When `S[e].φ = WT` and
timer `j` is not done: mark it done, disarm it, `eval(e)`, `gcheck()`.
Otherwise the input is rejected with INVALID_ARGUMENT and nothing changes
(`timer_skip.rs:83-110`). The Flow-timeout branch (`timer_skip.rs:38-42`) is
excluded with Flow timeouts (Section 1.3).

### 4.6 Client inputs

Each accepted client input is one commit whose event sequence starts with its
input event. A rejected input changes nothing and produces the single event
`[rejected(input, err)]`.

**[PUBLISH]** Events `[pub(f, [(c, v)])]`. Requires `st = Running`, else
rejected with NOT_FOUND. The engine gives every message a fresh ID. Apply
appends the messages in order and calls `wake` on the changed Channels.
`server-zd/crates/dexzd-apply/src/command_handlers/client_writes.rs:206-251`.
A publication during a pending close is accepted: the handler checks only
`st = Running` (`client_writes.rs:213-215`). PublishToChannel carries no
request ID.

**[SET-ATTRIBUTES]** Events `[set(f, [(k, v)])]`. Requires `st = Running`.
Writes in order. No Step wakes: no wait condition reads Attributes. Attribute
locks are not consulted (Open question 8).
`client_writes.rs:131-204`.

**[DELETE-MESSAGE]** Events `[del(f, c, id)]`. Requires `st = Running`, no
pending close, and the message pending in `Q[c]`; otherwise rejected with
NOT_FOUND or FAILED_PRECONDITION. Claimed messages cannot be deleted.
`client_writes.rs:253-320`.

**[STOP]** Events `[stop(f, kind), closed(f, ...)]`. Requires `st = Running`,
else rejected with NOT_FOUND. `closeNow(κ)` with κ = Canceled (CANCEL or
unspecified), Terminated (TERMINATE), or Failed(CLIENT_API_FAILING_FLOW)
(FAIL). It closes even during a pending close and with transactional RPCs in
flight.
`client_writes.rs:322-362`, `flow_close.rs:43-58`.

For SetAttributes, DeleteChannelMessage, StartFlow, and RPCs, a request ID
must be fresh (Section 1.3).

### 4.7 RPCs

An RPC is a client call that runs a handler on a Worker. It takes these
transitions: Call, Snap, Run, Commit, and always a final Finish that returns
the result and drops the RPC's guards.
`server-zd/crates/dexzd-engine/src/flow_coordination/client_operations/rpc_invocation.rs:62-302`.

Let `tx := request.is_transactional ∨ keys ≠ ∅` (`rpc_invocation.rs:73`).

**[RPC-CALL]** (engine) Events `[call(f, r, v, id)]`. Adds `Rq[id] := ⟨f, r, v,
keys, tx, admitted = false, Called⟩`. If `tx`:

```text
if st ≠ Running:       Rq[id].phase := Done(NOT_FOUND)            -- rpc_invocation.rs:170-178, :405-418
else if pc ≠ ⊥:        Rq[id].phase := Done(FAILED_PRECONDITION)  -- registry.rs:254-255
else:                  Tx[f] += 1;  Rq[id].admitted := true       -- rpc_invocation.rs:179-191, registry.rs:227-262
```

**[RPC-SNAP]** (engine) Events `τ`. Phase `Called`.

```text
if keys ≠ ∅:
  if ∃k ∈ keys. (f,k) ∈ dom(RL) ∨ L[k].h ≠ ⊥:  Rq[id].phase := Done(ABORTED);  return   -- registry.rs:735-763
  RL[(f,k)] := id for k ∈ keys
  if st ≠ Running:  Rq[id].phase := Done(NOT_FOUND);  return       -- rpc_invocation.rs:204-211
Rq[id].phase := Snapped(snapshot of the committed run with the RPC's load selections)
                                                                    -- rpc_invocation.rs:202-221, :464
```

- A queued (pending) Step waiter does **not** block an RPC. Only a holder does
  (`rpc_locks.rs:63-82`).
- The real engine takes the locks, waits for a sync barrier, and then reads
  the snapshot (`rpc_invocation.rs:192-221`). [assumption] Fusing lock and
  snapshot into one transition gives the same traces. Between the two real
  steps no Step can take these keys, and holders are only released. So the
  same commits can occur on both sides, and ABORTED is possible in the same
  schedules.
- A non-locking RPC reads the committed state at any point; no barrier.

**[RPC-RUN]** (Worker) Events `[fx(f, rpc:id, a)]` per step. As
[WORKER-EFFECT], with `U_r(ctx, v, snap)`. Ends in `Ran(fx⃗ = [], resp)`. The
engine calls the Worker and validates its response (`rpc_invocation.rs:234-271`).

**[RPC-READONLY]** (engine) Events `τ`. When phase is `Ran(resp)`, `¬tx`, and
`resp` has no effects (no writes, deletions, publications, record events,
moves, or cancels): `Rq[id].phase := Done(ok(resp.output))`. Nothing commits;
the run is unchanged (`rpc_invocation.rs:272-286`, `rpc_invocation.rs:630-643`).

**[RPC-COMMIT]** (commit, `RpcCommitted`) Events `τ` or `[closed(...)]`. When
phase is `Ran(resp)` and `tx` or `resp` has effects.

```text
if st ≠ Running:  Rq[id].phase := Done(NOT_FOUND);  return       -- rpc_committed.rs:75-79 (the handler already ran)
if tx ∧ some deletion names no pending message:
                  Rq[id].phase := Done(NOT_FOUND);  return       -- rpc_committed.rs:91-103
A, Q: writes, then deletions, then publications                  -- rpc_committed.rs:104-121
wake(published Channels)                                         -- rpc_committed.rs:122-127
if pc = ⊥ and resp.rpcDecision present:                          -- rpc_committed.rs:136-162
  cancelTypes(rpcDecision.cancel, [], "")
  for (T, v, o) in rpcDecision.moves: new(T, v, o, "__rpc/" ++ r)
gcheck()                                                         -- follows #1450, not yet merged
if pc ≠ ⊥ and Tx⁻[f] = 0: record(pc)                             -- rpc_committed.rs:225
Rq[id].phase := Done(ok(output))
```

- **follows #1450, not yet merged.** [owner] The `gcheck()` line models the
  fixed behavior of issue
  [#1450](https://github.com/superdurable/dex/issues/1450): the RPC commit runs
  the graceful-completion check after its cancellations and new Steps. The
  code at `9b37482ed` does not call it (Open question 3).
- RPC decisions cannot close the Flow or cancel siblings (`rpc_committed.rs:84-90`).
- A transactional commit carries `expected_run_attempt`, which matters only
  with time travel (`rpc_invocation.rs:375-378`).

**[RPC-FINISH]** (engine) Events `[ret(id, result)]`. Enabled when
`Rq[id].phase = Done(result)`, whichever path set it: rejection at Call,
ABORTED or NOT_FOUND at Snap, read-only return, rejected commit, or successful
commit.

```text
for k with RL[(f,k)] = id:  delete RL[(f,k)]                     -- RpcLockGuard drop, registry.rs:838-846
if Rq[id].admitted:  Tx[f] -= 1                                  -- TransactionalRpcGuard drop, registry.rs:813-817
delete Rq[id]
```

- The engine drops both guards on every exit path, success or error, because
  they are scoped values (`rpc_invocation.rs:179-200`,
  `rpc_invocation.rs:287-301`).
- After this step, a pending close that waited for this RPC can be recorded by
  [PENDING-CLOSE-CHECK] (Section 4.9).

### 4.8 SubFlows and cross-Flow delivery

Outbox messages written by a commit move to the network `N`, then a separate
commit at the receiving Flow consumes them. Delivery is at least once with
deduplication. [assumption] Failure-free delivery is exactly once, after an
arbitrary delay, in any order
(`server-zd/crates/dexzd-engine/src/effects/outbox_delivery.rs:9-11`).

The child Flow may be in another partition. Its commits use its own
partition's clock, and its timers fire by that clock. Nothing in the parent's
rules reads the child's clock: the child result reaches the parent only
through [DELIVER-CHILD-DONE], which the parent stamps with its own clock.

**[OUTBOX-SEND]** (engine) Events `τ`. Move a committed message from
`R(f).Ob` to `N` (`server-zd/crates/dexzd-engine/src/effects/outbox_delivery.rs:25-28`).
The receiver's commit later acknowledges it, and the sender stops redelivering
(`server-zd/proto/dexzd/v1/command.proto:461-464`).

**[DELIVER-CHILD-START]** (commit at child, `InboxMessage`) Events `τ`.
`server-zd/crates/dexzd-apply/src/cross_flow/inbox.rs:62-208`.

```text
f_c := child Flow ID
if no run of f_c exists:  start the child run like [FLOW-START] with parent = (f, e, i)
else if the reuse policy attaches (default: latest run is Running or Completed):   -- inbox.rs:123-140
  add parent to the child's attached parents
  if the child is closed: Ob_child += ChildCompleted(...) at once                 -- inbox.rs:210-239
else: start a new child run (ID reuse)
```

The child's start event and close event are Dex events of `f_c`. The
projection of Section 5.3 hides them.

**[DELIVER-CHILD-DONE]** (commit at parent, `InboxMessage`) Events `τ` or
`[closed(...)]`.
`server-zd/crates/dexzd-apply/src/cross_flow/inbox.rs:241-382`.

```text
SFR[f_c] := result                                     -- stored even if the parent is closed
if st = Running:
  for e with S[e].w waiting on f_c, ordered by sws:  eval(e)    -- wait_conditions/mod.rs:192-245
  gcheck()
```

A result for a Step that no longer waits on that child is stored and never
read.

### 4.9 Pending close

**[PENDING-CLOSE-CHECK]** (commit) Events `[closed(...)]`. When `pc ≠ ⊥` and
`Tx[f] = 0`: `record(pc)`.
`server-zd/crates/dexzd-apply/src/command_handlers/flow_close.rs:234-273`.
The engine proposes it after the last admitted transactional RPC drops its
guard (`server-zd/crates/dexzd-engine/src/effects/mod.rs:974-976`,
`server-zd/crates/dexzd-engine/src/effects/pending_close_check.rs:24`).

While `pc ≠ ⊥`, the Flow status is still RUNNING, every Step is already
canceled, transactional RPCs are not admitted, and committing RPCs start no
Steps. Attribute Sync, which is out of scope, would add the same deferral.
In the admissible environment (Section 5.3) there are no RPCs, so `pc` is
never set.

## 5. Observable events, projection, and equivalence

### 5.1 Dex event alphabet

`Obs` is the set of events the rules produce. Everything else is `τ`.

| Direction | Event | Produced by |
| --- | --- | --- |
| In | `start(f, T, v, A)` | [FLOW-START] |
| In | `pub(f, [(c, v)])` | [PUBLISH] |
| In | `set(f, [(k, v)])` | [SET-ATTRIBUTES] |
| In | `del(f, c, id)` | [DELETE-MESSAGE] |
| In | `skip(f, e, j)` | [SKIP-TIMER] |
| In | `stop(f, kind)` | [STOP] |
| In | `call(f, r, v, id)` | [RPC-CALL] |
| Out | `rejected(input, err)` | a rejected client input |
| Out | `ret(id, result)` | [RPC-FINISH] |
| Out | `fx(f, src, a)` | [WORKER-EFFECT], [RPC-RUN]; `src` is `(e, m)` or `rpc:id` |
| Out | `closed(f, status, error, outs)` | `record` |

[decision] Reads are excluded. GetAttributes, GetFlowState, history, and
GetChannelMessages are not in `Obs`. Including them makes every intermediate
Attribute write observable, which is a far stronger theorem that the
translation would rarely satisfy.

### 5.2 Traces

A run of the system is a finite or infinite sequence of transitions from an
initial `Σ₀` with `R = ∅`. Its trace is the concatenation of the event
sequences of its transitions.

### 5.3 Admissible environment and projection to L

`Obs` contains raw identifiers (Flow IDs, Step execution IDs, method sources,
RPC IDs) and client operations that L has no counterpart for. The theorem is
stated over a projection, under a restricted environment.

**Admissible environment `E_adm`** (the only inputs the theorem allows). An
admissible input word is a finite or infinite sequence of **attempted client
requests**, in commit order, accepted and rejected alike. Each request in the
word is attempted exactly once, in the word's order, and its commit produces
exactly one input-side event: the input event when apply accepts it, or
`rejected(request, err)` when apply rejects it (Section 4.6). The word has
this shape:

- It **begins** with exactly one StartFlow request `start(f, T₀, v, ∅)`, with
  a fresh Flow ID `f`, the program's entry Step `T₀`, and the L program's
  input `v`. No Flow timeout, no UpdateFlowConfig, no initial Attributes. No
  other StartFlow follows. Its Flow ID is fresh, so it is always accepted.
- **Every later** request is a PublishToChannel request `pub(f, [(c, v)])`
  where `c` is one of the program's **external** Channels (the translations of
  L's external input channels, Section 8), each with one message. While the
  root Flow is RUNNING it is accepted and produces `pub(f, [(c, v)])`; after
  the close it is rejected with NOT_FOUND and produces
  `rejected(pub(f, [(c, v)]), NOT_FOUND)`
  (`server-zd/crates/dexzd-apply/src/command_handlers/client_writes.rs:206-215`).
  Both are outcomes of the same request in `w`.
- No publication precedes the start: one would be rejected with NOT_FOUND
  (`server-zd/crates/dexzd-apply/src/command_handlers/run_cursor.rs:111-113`)
  and would project to a `refused` before `begin`, which L has no
  counterpart for.
- No `set`, `del`, `skip`, `stop`, or `call`. [owner] RPC entry points are a
  future extension and are outside the core theorem; every external input of
  an L program maps to Channel publications, which never meet RPC lock
  conflicts.
- Every request ID is fresh (Section 1.3).
- [owner, 2026-11-09, kept as taken] The start input and every published
  message are encoded L values `enc(v)`. `π` decodes them: it maps
  `start(f, T₀, enc(v), ∅)` to `begin(v)`, `pub(f, [(c, enc(v))])` to
  `in(c, v)`, and the rejection to `refused(c, v)`. A publication of any other
  Dex value to an external Channel has no L counterpart and is not
  admissible.
- [owner, completing the decision of 2026-11-10] The environment sends plain
  data only. The start input and every published message are encoded L
  **literals**: null, a Boolean, an integer, or a string (Section 8.1,
  Handles and literals). A request that carries a thread, mutex, or condition
  variable handle is not admissible, so no handle enters a program from
  outside.

**Projection `π : Obs → Obs_L ∪ {ε}`**, where `Obs_L` is L's observable
alphabet (Section 8.1):

| Dex event | L event |
| --- | --- |
| `start(f, T₀, v, ∅)` for the root Flow | `begin(v)` |
| `pub(f, [(c, v)])`, `c` external, root Flow | `in(c, v)` |
| `rejected(pub(f, [(c, v)]), NOT_FOUND)` | `refused(c, v)` |
| `fx(f', src, a)` for the root Flow or any of its SubFlows | `out(a)` (the source is dropped) |
| `closed(f, Completed, _, [(T, e, ⟦exit: x⟧)])` for the root Flow | `exit(x)` |
| `closed(f, Failed, (STEP_DECISION_FAILING_FLOW, msg), _)` for the root Flow | `fail(msg)` |
| every other event (child Flow starts and closes, internal Channels) | `ε` |

Encoding of exit values and failure messages:

- FC drops a null output: `collect` appends nothing for an absent or null
  value (`server-zd/crates/dexzd-apply/src/steps/step_decisions.rs:186-196`).
  So the translation never passes an L value to FC directly. It passes the
  **tagged** value `⟦exit: x⟧ = {"exit": enc(x)}`, which is never null, also
  when `x` is L's null. The translation uses neither GC nor FCE, so the root
  Flow's `outs` is exactly that one FC output, and `π` decodes `x` from it.
- FF stores no output. Its string input becomes the close message, and its
  error type is STEP_DECISION_FAILING_FLOW (`step_decisions.rs:92-119`). The
  translation always passes `fail(s)` as `FF(s)` with `s` a string (empty
  allowed), so `π` reads `msg = s` from the structured error. No other failed
  close (another error type, Canceled, Terminated) can happen under `E_adm`
  without failures: there is no StopFlow, no Flow timeout, and handler results
  are well formed (Section 2.5).

### 5.4 Equivalence: safety

For an L program `S` and its translation `⟦S⟧`, the **safety** statement is
weak trace equality of finite traces under the projection:

```math
\pi\big(\mathrm{FinTr}_{E_{adm}}(⟦S⟧)\big) \;=\; \mathrm{FinTr}(S)
```

where `FinTr_{E_adm}` takes the finite traces of runs whose inputs are drawn
from `E_adm`, `π` is applied event by event with `ε` erased, and `FinTr(S)` is
L's set of finite traces over `Obs_L`. [decision] Untimed by default.
[owner, 2026-11-09] `S` ranges over well-formed L programs (Sections 0 and
8.1).

Finite-trace equality alone cannot tell a Flow that ends with DeadEnd and stays
RUNNING from one whose close is never scheduled by an unfair scheduler. The
liveness statement in Section 5.5 closes that gap.

### 5.5 Equivalence: termination and divergence (fair maximal traces)

Maximal traces are defined **relative to a fixed admissible input word** `w`
(Section 5.3). A run **for `w`** is an infinite run whose client-request
commits, accepted or rejected, are exactly the requests of `w`, each once and
in order. ([TICK] is always enabled, so no maximal run is finite.) [TICK] steps are time passing, not progress: they
carry no event in the untimed trace, and the classification below counts only
the other transitions, which this section calls **progress steps** (inputs,
commits, and engine and Worker steps).

A run for `w` is **fair** when every condition below holds. [assumption]
These are the fairness assumptions the theorem needs; the rules themselves are
unfair.

- **F0 Requests arrive.** Every request of `w` is eventually attempted, in
  order, and committed as accepted or rejected.
- **F1 Time diverges.** In an infinite run, every partition clock exceeds every
  bound.
- **F2 Timers fire.** A timer that stays armed and due is eventually fired by
  [TIMER-FIRE].
- **F3 Steps start.** A pending invocation that stays startable (premise of
  [STEP-START]) eventually starts. In `E_adm` there are no RPC locks, and a
  Step lock queue only shrinks at its head, so a startable invocation stays
  startable until it starts or is canceled; weak fairness suffices.
- **F4 Workers settle.** Every task eventually leaves `Θ`. A task of a
  still-running invocation emits all its effects and returns. A task of a
  canceled invocation either emits all its effects and returns, or is dropped
  by [WORKER-ABANDON] after some prefix of its effects.
- **F5 Results commit.** Every `Returned` task is eventually completed by a
  Complete rule or dropped by [STEP-COMPLETE-STALE].
  [owner, 2026-11-09, kept as taken] A task stays in `Θ` until a Complete
  rule commits its result or a rule drops it. So F4 reads: every task
  eventually becomes `Returned` or is dropped; and F5 reads: every `Returned`
  task eventually leaves `Θ`.
- **F6 Messages move.** Every outbox message is eventually sent and delivered;
  [PENDING-CLOSE-CHECK] and [RPC-FINISH] eventually run when enabled.

Because time keeps passing (F1), every fair run is infinite. It is classified
by its progress steps, and its **maximal trace** is its whole untimed trace,
with a marker:

- `✓` — finitely many progress steps, and the root Flow is closed. By F4 and
  F5, every task has left `Θ` by then, so the trace already contains every
  effect a canceled Worker emits after the close. A `✓` trace is therefore
  never a proper prefix of another fair trace for the same `w`.
- `δ` — finitely many progress steps, and the root Flow is RUNNING. From some
  point on only [TICK] happens: no Step is startable, no task is in flight,
  no timer is armed (F2 would fire it), and `w` is used up. A program whose
  threads all end with DeadEnd, while the Flow stays idle
  (`step_decisions.rs:120`, `flow_start.rs:204-205`), lands here.
- `∞` — infinitely many progress steps. An infinite run of only [TICK] after
  a DeadEnd or after the close is `δ` or `✓`, never `∞`.

With an infinite `w`, every fair run has infinitely many inputs and is `∞`;
the theorem then compares the infinite traces themselves.

The **liveness** statement is equality of fair maximal traces with markers,
for every admissible input word:

```math
\forall w \in E_{adm}.\;\;
\pi\big(\mathrm{MaxTr}^{fair}_{w}(⟦S⟧)\big) \;=\; \mathrm{MaxTr}^{fair}_{w}(S)
```

[owner, 2026-11-09] As in Section 5.4, `S` ranges over well-formed L programs
(Sections 0 and 8.1).

L must define the same three outcomes (normal exit or failure, deadlock, and
divergence) the same way: relative to the input word, counting its own
actions (including timer `fire`, Section 8.1) as progress and idle time as
not, with `✓` only after every issued emit has delivered or been dropped. The theorem states safety (5.4) and
liveness (5.5) together. [decision] The alternative, weak bisimilarity, is
stronger than needed and would distinguish where a choice is made.

[owner, 2026-11-09, kept as taken] A fair L run satisfies the L forms of F0
to F6:

- **F0** and **F1** as above, for L's requests and L's clock.
- **F2.** An armed timer that stays due is eventually fired.
- **F3.** A thread whose next action can begin eventually begins it. This is
  the L form of a pending invocation: before the program's start action,
  before the begin of a statement, and after a wait was decided.
- **F4.** An issued emit eventually delivers its `out(a)`, or the program
  ends first. After the end, every issued emit is eventually delivered or
  dropped.
- **F5.** A thread that began an action eventually completes it.
- **F6** holds trivially: L has no cross-program messages.

### 5.6 Timed variant

Make `tick(p, δ)` visible and stamp every event with its partition's clock.
Then a fired timer's Execute and its effects carry a time at least the fire
time, and nothing else bounds them from above. Two complications: events of
Flows in different partitions carry times from different clocks
(Open question 22), and L's sleep durations must be whole seconds
(Open question 12).

## 6. Atomicity and interleaving

### 6.1 Atomic units

| Unit | Atomic? | Code |
| --- | --- | --- |
| One apply command (any rule marked commit) | Yes. All writes or none. | `command_handlers/mod.rs:90-300` |
| A Step method (Start, Run, Complete) | **No.** Reads come from the snapshot at Start. Writes, publications, and the decision commit at Complete. | `step_results.rs:156-209`, `step_results.rs:857-994` |
| The writes and decision of one Step method | Yes. One commit. | `step_results.rs:960-987` |
| An RPC with effects (Snap, Run, Commit, Finish) | **No.** Snapshot first, commit later, return last. | `rpc_invocation.rs:202-301` |
| A read-only, non-transactional RPC | No commit at all. | `rpc_invocation.rs:272-286` |
| A Worker's external effects | Not part of any commit. They happen before the commit, and may happen with no commit, also after the Flow closed. | Section 4.4 |
| Timers that fire together | One command for a prefix of the due timers of one run. | `timer_service.rs:116-205` |
| A SubFlow start, and a SubFlow completion report | Each is two commits (sender's outbox, receiver's inbox) and a delivery between them. | `outbox.rs:37-171`, `inbox.rs:62-382` |

The docs say "Each Step method commits atomically"
(`docs/content/concepts/flow/index.mdx:30`) and "Dex commits its Attribute
writes and Channel publications together with its result"
(`docs/content/concepts/step/index.mdx`, Guarantees). Both are true of the
writes. Neither makes the read-modify-write atomic (Open question 1).

### 6.2 Order of commits

- Commands of one partition are totally ordered by the partition log, so the
  commands of one run are too. Apply sees them in that order and every command
  reads the state its predecessors wrote.
- Commands of different partitions are not ordered with each other, and their
  timestamps come from different clocks. Commands of different Flows are
  related only through cross-Flow delivery. [assumption] Different Flows
  interleave arbitrarily.
- Within one commit the order is fixed by the meta-functions. The key orders
  are:
  - Execute: data effects, then `wake`, then finish of the deciding Step, then
    cancellations, then the close decision, then new Steps, then `gcheck`.
  - WaitFor: data effects, then `wake` of other waiters, then registration and
    one evaluation of the new wait.
  - RPC: writes, deletions, publications, `wake`, cancellations, new Steps,
    `gcheck` (follows #1450).

### 6.3 Which interleavings are possible

Between Start and Complete of a Step method, any enabled transition can occur:
other Steps' Starts and Completes, RPC commits, client inputs, timer firings,
and deliveries. In particular:

1. Two Steps whose Execute reads and writes the same Attribute can both start
   before either completes. The second commit overwrites the first (lost
   update). Locks on both prevent it.
2. An RPC can snapshot before a Step's commit and commit after it, or the
   reverse. Locks on both serialize them; an RPC never waits for a lock, it
   gets ABORTED.
3. A Channel message published by a client between two waiters' registrations
   goes to whichever waiter's evaluation first finds its condition feasible.
4. A timer that is due can lose an AnyOf race to a message committed after
   the timer's fire time, because firing is late.
5. A canceled Step's Worker can still emit effects after the cancel commits,
   and after the Flow closes.

The rules are unfair: the dispatcher may delay a startable invocation, the
timer service a due timer, and delivery a message, each without bound. Section
5.5 states the fairness the liveness statement assumes.

## 7. Open semantic questions

Each item gives what the code does, why it matters for the translation
theorem, and the stance this document takes. Items 1 to 13 cover the topics
the task named; the rest were found on the way or in review.

1. **A Step method is not a read-modify-write transaction.**
   - Code: the snapshot is computed at Start, after locks
     (`attempt_snapshot.rs:9-10`, `step_results.rs:188-195`). Writes are
     applied blindly at Complete (`flow_data.rs:188-230`). Nothing checks
     that the read values are still current.
   - Docs: "Each Step method commits atomically"
     (`docs/content/concepts/flow/index.mdx:30`). True for the writes only.
   - Matters: an L assignment `x := x + 1` in two threads, translated to two
     Executes without locks, loses an update.
   - Stance: three transitions per method (Section 4.4). Section 8 puts at
     most one shared access in each Step method: a read linearizes at Start, a
     write at Complete. No Attribute locks are needed.

2. **GracefulComplete while other Steps are active.**
   - Code: GC collects the output, sets `gc`, and takes no moves
     (`step_decisions.rs:78-84`). Nothing else reads `gc` except `gcheck`
     (`grep is_graceful_completion_requested`: `step_decisions.rs:82,161`,
     `flow_close.rs:190,352`, time travel only otherwise). So other active
     Steps keep running and may GoTo new Steps; RPCs may start new Steps. The
     Flow completes the first time `gcheck` sees no active user Step. Several
     GC decisions each add an output. A later FC adds its output and closes at
     once; a later FF closes as Failed and keeps the earlier outputs. The
     timeout handler does not count as active (`step_decisions.rs:164-168`).
   - Matters: only for an L "exit when all threads end" construct. Section 8
     maps exit to FC and never uses GC.
   - Stance: as coded.

3. **Canceling Steps from an RPC skips the graceful-completion check.**
   - Code at `9b37482ed`: `apply_rpc_committed` calls `cancel_steps_by_type`
     (`rpc_committed.rs:139-147`) and never calls `complete_gracefully_if_ready`.
     The only callers of `gcheck` are `step_decisions.rs:150`,
     `wait_conditions/mod.rs:187`, `wait_conditions/mod.rs:244`, and
     `timer_skip.rs:50`. When GC was requested and an RPC cancels the last
     active Step, the Flow stays RUNNING.
   - Docs: "GracefulComplete completes the Flow once no other Step is active"
     (`docs/content/concepts/step/index.mdx`, StepDecision section).
   - **Resolved [owner], option A:** a bug, tracked as
     [#1450](https://github.com/superdurable/dex/issues/1450), to be fixed in
     server-zd. [RPC-COMMIT] models the fixed behavior: `gcheck()` after the
     RPC's cancellations and new Steps. It is marked "follows #1450, not yet
     merged". RPCs are outside the core theorem anyway (Section 5.3).

4. **Several Steps waiting on one Channel: who consumes.**
   - Code: when a Channel changes, its waiters are evaluated one after another
     in `(sws, StepExecutionId)` order, so an earlier waiter sees the queue
     first (`wait_conditions/mod.rs:140-164`). A new wait is evaluated alone
     when it registers (`wait_conditions/mod.rs:133`). A waiter whose minimum
     is not met is skipped. The `StepExecutionId` tie-break is string order,
     so `T-10 < T-9`; ties need equal `sws`, which [assumption] does not
     happen without time travel because each command registers at most one
     wait.
   - Docs: "One wait consumes a message, once"
     (`docs/content/concepts/primitives/channel.mdx:30-31`). True.
   - Matters: with ForN waiters this is not FIFO-fair: a ForN(3) waiter can
     starve while later ForOne waiters take every message. With ForOne waiters
     only, it is FIFO by registration: a message present when a waiter
     registers means no earlier waiter could take it. Section 8 uses only
     ForOne on shared Channels and relies on this FIFO order.
   - Stance: as coded.

5. **ForN and the other Channel bounds.**
   - Code: ForN(n) is `lo = hi = n` (`sdk-go/dex/channel.go:124-127`). It waits
     for `n` pending messages and takes exactly the oldest `n`. AtLeast(n)
     takes every pending message. AtMost(n) is satisfied at once, even with an
     empty queue. Claimed messages do not count as pending
     (`wait_conditions/mod.rs:361-365`). Within one candidate, minimums on the
     same Channel are reserved before any extra
     (`condition_plan.rs:155-205`). AnyOf picks the first feasible candidate in
     the order timers, Channels, SubFlows, and consumes only its own messages
     (`condition_plan.rs:72-86`; docs agree,
     `docs/reference/go/flows-and-steps/waits.mdx`). In AnyCombinationOf, a
     duplicated condition ID resolves to the last declaration within the first
     kind that has the ID, kinds tried in the order timer, Channel, SubFlow
     (`condition_plan.rs:103-129`).
   - Matters: L's `recv(ch)` is ForOne. A batch receive maps to ForN.
   - Stance: as coded.

6. **Consumed messages are lost when the consuming Step ends without Execute
   committing.**
   - Code: `finish` drops the Step's condition results and only decrements
     `K` (`wait_conditions/mod.rs:250-281`). `close` cancels Steps with
     "consumed messages discarded" (`flow_close.rs:172-174`).
   - Matters: a Channel-token mutex loses its token if the Step that consumed
     it is canceled before its Execute commits; the mutex then stays locked
     forever (Review log, finding 8).
   - Stance: as coded. Section 8 never cancels Steps: L cancellation is
     cooperative, and the only Step cancellation in a translated program is
     the Flow close, after which no Step runs.

7. **A Step's own publications are delivered before its decision runs.**
   - Code: `effects` ends with `wake` (`flow_data.rs:664-669`) before
     `decide` (`step_results.rs:960-987`). So "publish to `c` and cancel type
     `B`" can let a `B` Step consume the message and then be canceled in the
     same commit; the message is lost (item 6). "Publish and ForceComplete"
     does the same.
   - Matters: the translation of "send then cancel" or "send then exit".
   - Stance: as coded. The translation never combines a publication with
     CancelSteps, and a publication in the same commit as FC is unobservable
     after the close.

8. **Attribute lock acquisition: Step vs RPC.**
   - Code, Step: a pending method queues FIFO on each key by `seq`, at
     `pend` time, even when its keys are free (`invocations.rs:59-79`). It
     starts only as the first waiter of every key, with no RPC holding any key
     (`invocations.rs:227-245`, `rpc_locks.rs:85-125`). It holds the keys for
     that one method call and releases them at Complete or cancel
     (`invocations.rs:314-342`).
   - Code, RPC: an RPC with lock keys fails at once with ABORTED if any key is
     held by a Step or another RPC (`registry.rs:735-763`). Queued Step
     waiters do not block it (`rpc_locks.rs:63-82`), so RPCs can overtake
     queued Steps without bound.
   - RPC locks are engine memory, never logged (`rpc_locks.rs:20`).
   - Locks are advisory: SetAttributes, Steps without the key in their
     options, and RPCs without lock keys ignore them.
   - WaitFor's locks are released before the Step waits. No single Step holds
     a lock across a wait.
   - Docs agree (`docs/reference/server/runtime-data.mdx:439-452`).
   - Matters: an L mutex held across a blocking operation cannot be an
     Attribute lock.
   - Stance: as coded; Section 8 uses a Channel token for L mutexes and no
     Attribute locks.

9. **Canceling other Steps: cooperative, and what is committed.**
   - Code: cancel ends the Step at once (`finish(e, CA)`). The running attempt
     gets a cancel request (`invocations.rs:376-381`), and its later result is
     ignored (`step_results.rs:863-872`). So none of its writes, publications,
     or decision commit. Everything an earlier method of the Step committed,
     such as WaitFor's writes, stays. Its claimed messages are lost (item 6).
     Its child SubFlows keep running (`flow_close.rs:172-174`). Its Worker may
     keep running and emit external effects ([WORKER-ABANDON]).
   - Selection is by Step type for the whole run, or by type among Steps with
     the same `from`. Pending and waiting Steps are canceled too. The Steps a
     decision creates are made after its cancellations, so they are never
     canceled by it (`step_decisions.rs:45-53` before `:132-149`). The SDK says
     the same (`sdk-go/dex/decision.go:124-130`).
   - All Steps that one RPC name started share `from = __rpc/<r>`, so they are
     siblings across RPC invocations. RPCs cannot use sibling cancel.
   - Matters: an effect emitted inside an Execute can be followed by a cancel
     before the Execute commits; this includes StopFlow and a competing Step's
     close (`client_writes.rs:322-355`). Section 8 gives L the same
     pre-commit effect semantics.
   - Stance: as coded. Section 8 uses no CancelSteps.

10. **DeadEnd and runs with no active Step.**
    - Code: DE takes no moves and closes nothing (`step_decisions.rs:120`). A
      run with no active Step and no GC request stays RUNNING forever, also
      when it started without a start Step (`flow_start.rs:204-205`). Docs
      agree (`docs/reference/go/flows-and-steps/step-decisions.mdx`, "A Flow
      can have no active Steps and still keep running").
    - Matters: L program termination must map to an explicit FC. A thread that
      ends maps to DE. A program stuck with every thread ended is the `δ`
      outcome of Section 5.5.
    - Stance: as coded.

11. **RPC vs Step ordering.**
    - Code: an RPC reads a snapshot of committed state (Section 4.7) and
      commits later; any commit can come between. A read-only, non-transactional
      RPC never commits and leaves no history. An RPC whose run closed in
      between ran its handler, and its commit is rejected (`rpc_committed.rs:75-79`).
      During a pending close an RPC commit still writes Attributes and
      publishes, but starts no Steps (`rpc_committed.rs:133-138`). Step
      decisions from RPCs cannot close the Flow.
    - **Resolved [owner], option A:** ABORTED is an observable RPC result
      (`ret(id, ABORTED)`). RPC entry points are a future extension and are out
      of the core theorem; every external input of an L program maps to Channel
      publications, which never meet RPC lock conflicts (Section 5.3).

12. **Timer firing order.**
    - Code: due timers of one run fire in one command, in `(fire time, encoded
      TimerId bytes)` order (`row_keys.rs:303-314`, `timer_service.rs:116-205`).
      The service fires a prefix of the due timers; Section 4.5 explains why
      its retry delay does not break the prefix in failure-free runs.
      The fire time is the WaitFor commit time plus whole seconds
      (`wait_conditions/mod.rs:89-98`). [assumption, from protobuf encoding]
      For two wait timers with equal fire times, the bytes compare first by
      the encoded length of the `WaitConditionTimerId`, then by the Step
      execution ID. That length depends on both the ID length and the timer
      index: index 0 is omitted in proto3, and indexes 1 to 127 add two
      bytes. So `T-9` sorts before `T-10`, and with IDs of equal length an
      index-0 timer sorts before another Step's index-1 timer whatever the ID
      bytes. A zero-second timer still needs a [TIMER-FIRE].
    - Firing is late by an unbounded amount, so a due timer can lose an AnyOf
      race to a later message (Section 6.3).
    - In one evaluation a timer and a message cannot both become ready,
      because apply evaluates on every change (`condition_plan.rs:217-218`).
    - SDK: "positive fractions round up to whole seconds"
      (`sdk-go/dex/condition.go:75`).
    - Matters: L `sleep(d)` with `d` not a whole number of seconds sleeps at
      least `⌈d⌉` in Dex. Untimed traces are unaffected. A timed theorem needs
      whole-second durations in L.
    - Stance: untimed by default (Section 5.4). L gets an explicit late
      `fire` action, so races between a timer and a message or cancel are
      decided by firing order on both sides, and L fires timers in the same
      `(deadline, timer number)` order that Dex's index gives translated
      programs (Sections 8.1, 8.2).

13. **SubFlow completion delivery and child identity.**
    - Code: the child result travels through the child's outbox and a
      delivery commit (Section 4.8). It completes the parent's wait only when
      the parent is RUNNING and the Step still waits on that child
      (`inbox.rs:367-375`, `wait_conditions/mod.rs:216-245`); otherwise it is
      stored and ignored.
    - The child Flow ID is `SubFlow:<parent Flow ID>-<StepExecutionId>-<index>`
      (`wait_conditions/mod.rs:645-647`). Step counters restart in every run,
      so a later run of the same parent Flow ID reproduces the same child IDs.
      With the default reuse policy the parent then attaches to the earlier
      child if it is RUNNING or COMPLETED (`inbox.rs:123-140`) and, if it is
      closed, receives the old result at once (`inbox.rs:230-232`). The docs
      describe this as intended for retries
      (`docs/content/concepts/primitives/subflow.mdx:24-28`).
    - Canceling the parent Step, or closing the parent, does not stop the
      child.
    - Matters: L recursion through SubFlows is sound only when every parent
      Flow ID is fresh. Section 8 implements calls inline, without SubFlows.
    - Stance: [assumption] fresh Flow IDs (Section 1.3).

14. **ForceCompleteIfChannelsEmpty when not empty.**
    - Code: it falls through and takes the moves (`step_decisions.rs:64-77`).
      Claimed messages count as non-empty until their Step ends. Docs agree
      (`docs/reference/go/flows-and-steps/step-decisions.mdx`).
    - Stance: as coded.

15. **A failed Flow's result keeps earlier outputs.**
    - Code: FF collects nothing, but `record` reports `outs` for every status
      (`flow_close.rs:359-370`), and the child report includes them
      (`outbox.rs:447-485`). So GC by one Step and then FF by another gives a
      FAILED result with the GC output.
    - Matters: the `closed` event's `outs`. The projection drops `outs` for a
      failed root Flow (Section 5.3), and Section 8 never uses GC.
    - Stance: as coded.

16. **A natural close can be deferred by transactional RPCs.**
    - Code: with `Tx⁻[f] > 0`, `close` cancels every Step but leaves the
      status RUNNING with `pc` set; the close is recorded when the last such
      RPC commits or, after its guard drops, a PendingCloseCheck runs
      (`flow_close.rs:178-197`, `flow_close.rs:234-273`). StopFlow does not
      wait.
    - Matters: not in `E_adm`, which has no RPCs.
    - Stance: modeled ([RPC-FINISH], [PENDING-CLOSE-CHECK]).

17. **Dispatch order and fairness.**
    - Code: the engine may start any startable pending invocation; the
      DispatchQueue orders waiting attempts by pending time
      (`server-zd/crates/dexzd-apply/src/steps/dispatch_queue.rs:33-75`), but
      Worker polling decides when.
    - Matters: liveness of the translation, not safety.
    - Stance: nondeterministic in the rules; fairness F3 in Section 5.5.

18. **Worker effects can happen with no commit, and after the close.**
    - Code: a canceled Step's or a rejected RPC's handler may have emitted
      effects; its result is dropped (`step_results.rs:863-872`,
      `rpc_committed.rs:75-79`). The Worker of a Step canceled by a Flow close
      may still emit effects after the `closed` event.
    - Matters: L's `emit` must allow the same (Section 8.1).
    - Stance: [WORKER-ABANDON] and [STEP-COMPLETE-STALE] model it.

19. **Exceptions in user code are failures.**
    - Code: a handler that returns an error is an attempt failure, then retry,
      then a failure policy (`step_results.rs:258-472`). All of that is out of
      scope.
    - Matters: L exceptions must be compiled to explicit control flow inside
      handlers and decisions, never to Worker errors.
    - Stance: [decision] exceptions are data (Section 8).

20. **Channel deletion is best-effort for Steps and silent.**
    - Code: a Step's or non-transactional RPC's deletion of a missing message is
      skipped (`flow_data.rs:463-481`). A transactional RPC's deletion of a
      missing message rejects the whole RPC with NOT_FOUND
      (`rpc_committed.rs:91-103`). A client deletion of a missing message is
      NOT_FOUND (`client_writes.rs:277-293`).
    - Matters: little for L, which has no "delete by ID".
    - Stance: as coded.

21. **Publication during a pending close.**
    - Code: `apply_channel_publish` checks only `st = Running`
      (`client_writes.rs:213-215`); during a pending close the status is still
      RUNNING, so the publication is accepted into a run whose Steps are all
      canceled.
    - Matters: not in `E_adm`, where `pc` is never set.
    - Stance: as coded. Docs say nothing.

22. **There is no global clock.** (New in revision 2.)
    - Code: timestamps strictly increase per partition only
      (`command.proto:39-40`), and apply reads only each command's own
      timestamp (`lib.rs:9-15`). A SubFlow can live in another partition.
    - Matters: untimed traces are unaffected, because nothing compares
      timestamps across partitions: a child's result reaches its parent only
      through a delivery commit stamped by the parent's partition. A timed
      theorem must either relate the clocks ([assumption] on one node they
      read one wall clock, so their skew is bounded) or keep times per
      partition.
    - Stance: per-partition clocks (Section 3.4).

23. **Repeated request IDs change behavior without any failure.** (New.)
    - Code: a repeated request ID replays the recorded reply, or is rejected
      when its content differs (`client_writes.rs:41-65`,
      `rpc_committed.rs:58-74`, `flow_start.rs:115`).
    - Stance: fresh request IDs in `E_adm` (Section 1.3).

24. **Flow timeouts and UpdateFlowConfig are not derived constructs.** (New.)
    - Code: a Flow timeout closes the Flow directly with FLOW_TIMEOUT or
      CANCELED (`step_commands.rs:281-298`); UpdateFlowConfig changes the
      Worker target and Step durability mid-run (`client_writes.rs:366-413`).
    - Stance: both excluded from the theorem (Section 1.3).

25. **Facts above that failures change.** (New in revision 9.) Each is
    stated for failure-free runs above and is correct there. Section 11
    gives the rule with failures. Citations at `55721e878`.
    - Item 8 says a Step holds its lock keys "for that one method call". A
      method call can span several attempts. The keys stay held through every
      retry and through the Backoff wait between attempts
      (`server-zd/crates/dexzd-apply/src/steps/invocations.rs:11-12`,
      `invocations.rs:329-342`). So an RPC on those keys gets ABORTED during
      the whole wait. The comment on `lock_keys` in `state.proto:439`, "held
      only while RUNNING", is out of date; the code wins.
    - Items 9 and 18: a canceled Step whose method was in Backoff leaves its
      retry timer armed. Cancel and Flow close do not disarm it
      (`server-zd/crates/dexzd-apply/src/steps/step_decisions.rs:367-412`,
      `server-zd/crates/dexzd-apply/src/command_handlers/flow_close.rs:307-341`).
      When it fires, the command commits and does nothing else
      (`step_results.rs:816-838`, `step_commands.rs:161-170`).
    - Item 23: the per-run request table is bounded, evicts the oldest
      entries, and has no time limit (`client_writes.rs:80-95`,
      `server-zd/crates/dexzd-apply/src/state_access/run_rows.rs:353-376`).
      A request ID is recognized only within one run. StopFlow has no
      request ID. A StopFlow without a run ID targets the newest run of its
      Flow ID when it is proposed
      (`server-zd/crates/dexzd-engine/src/flow_coordination/client_operations/mod.rs:100-123`,
      `server-zd/crates/dexzd-engine/src/flow_coordination/read_operations/state_rows.rs:113-129`).
      So a late retry after Flow ID reuse stops the new run.
    - Section 4.4 says a Step method is three transitions. A recovered
      server does not hand a started attempt out again. It commits a
      failure of that attempt, which no failure-free rule has
      ([RECOVER-INTERRUPT], Section 11.3).
    - Heartbeat and method timeouts live in engine memory only. A crash
      loses them, and recovery fails the attempt instead of restarting the
      timers
      (`server-zd/crates/dexzd-engine/src/effects/dispatch/attempt_deadlines.rs:34-54`,
      `server-zd/crates/dexzd-engine/src/effects/dispatch/method_attempt.rs:638-711`).
    - Drift unrelated to failures, noted while checking: at `55721e878`, a
      PublishToChannel during a pending close is rejected
      (`server-zd/crates/dexzd-apply/src/command_handlers/client_writes.rs:216-219`).
      Section 4.6 and item 21 describe `9b37482ed`, where it is accepted.
      Both are outside `E_adm`, where `pc` is never set, so the rule is left
      as it is.
    - The simulation crate `dexzd-simulation` checks that a recovered store
      equals a twin that never crashed. It is not evidence that retries
      repeat only external effects, and it does not exercise client retries
      of PublishToChannel. [assumption, from the plan's survey; not
      re-verified in revision 9.]

26. **[TIMER-FIRE] is a prefix at scan time, not at apply time.** (New in
    revision 9. Found by the adversarial review of revision 9; citations at
    `55721e878`.) This gap exists without any failure.
    - Code: the timer service scans the committed, applied state
      (`server-zd/crates/dexzd-engine/src/effects/timer_service.rs:214-223`)
      and proposes TimersFired with the timer IDs it scanned
      (`timer_service.rs:253-287`). The registry puts a command in the log
      before it is applied to the committed store
      (`server-zd/crates/dexzd-engine/src/flow_coordination/registry.rs:619-635`).
      Apply fires exactly the listed timers that are still armed. It does not
      check that they are a prefix of the due timers at apply time
      (`server-zd/crates/dexzd-apply/src/command_handlers/step_commands.rs:156-181`).
    - Scenario: Step A has a timer due at 95. Step B's WaitFor completion is
      in the log with stamp 90 but not yet applied. When applied, it arms a
      timer due at 90 plus the timer's whole seconds, for example 90 with a
      zero-second timer
      (`server-zd/crates/dexzd-apply/src/steps/wait_conditions/mod.rs:89-108`).
      At wall clock 100 the timer service scans, sees only A, and proposes
      TimersFired[A]. The log applies B's completion first, then
      TimersFired[A]. A fires while B's earlier-due timer is still armed. The
      clock assumption D6 holds throughout.
    - So Appendix A, item 7, "exact for failure-free runs", is false in this
      window. Finding 3 of revision 2 considered the retry delay only.
    - Consequence: the model of revision 8 under-approximates real Dex for
      timer order. A real run in this window has no counterpart in the model.
      The expressiveness theorem does not cover such runs: it speaks about
      the model's runs.
    - **[owner, 2026-11-10] The server is not changed.** No distributed
      system guarantees precise timer firing, so the window stays.
    - **[owner, 2026-11-10] OD1, option A.** Both the expressiveness proof
      and the failure proof keep the prefix rule [TIMER-FIRE] of Section 4.5,
      as an explicit modeling idealization. Reason: no distributed system
      guarantees the order of timers due at almost the same time. So neither
      theorem covers a run in which such timers fire out of due order.
    - Stance: the rule of Section 4.5 is **not** changed. Section 11.3 keeps
      [TIMER-FIRE-SCAN] as the documented code behavior; neither theorem
      uses it.

## 8. Sketch: mapping L onto the core

Not a proof. Each choice names the open questions it depends on.

### 8.1 L, as the mapping requires it

L has threads with local variables, shared variables with sequentially
consistent single reads and writes, `seq`, `if`, `while`, procedure calls with
recursion, `fork`/`join`, mutexes, condition variables, `sleep(d)`,
`select { recv(c) → s | after(d) → s }`, `recv(c)` on external channels,
`emit(a)`, `throw`/`try`, `cancel(t)`, `exit(v)`, and `fail(s)`.

L's observable alphabet `Obs_L` is `begin(v)`, `in(c, v)`, `refused(c, v)`,
`out(a)`, `exit(v)`, and `fail(s)`.

The mapping is exact only if L adopts these semantics. Each matches what Dex
does; they are L design decisions for the owner.

- **Atomic actions.** Each single read or write of a shared variable, each
  `emit` issue, and each synchronization operation is one atomic action.
  Local computation is invisible. (Item 1.)
- **Mutex.** Threads blocked in `lock(m)` acquire `m` in FIFO order of
  blocking. `lock` is not a cancellation point. (Item 4.)
- **Condition variable.** `wait(cv, m)` requires `m` held; it atomically
  releases `m` and appends the thread to `cv`'s FIFO queue. `signal(cv)` and
  `broadcast(cv)` require `m` held; `signal` wakes the head of the queue,
  `broadcast` wakes all. No spurious wakeups. A woken thread re-locks `m`
  (FIFO with other lockers). `wait` is not a cancellation point. (Items 1, 4.)
  [owner, 2026-11-09, kept as taken] A condition variable belongs to one
  mutex, fixed by its identity. That mutex is the `m` that `signal(cv)` and
  `broadcast(cv)` require. `wait(cv, m)` with any other mutex is a type error
  and halts the thread (see Halting).
- **Well-formed programs.** [owner, 2026-11-09] The requirements above define
  the theorem's domain. A program is **well formed** when no run of it, under
  any input and any schedule, has a thread about to call `wait(cv, m)`,
  `signal(cv)`, or `broadcast(cv)` without holding `m`, or about to call
  `unlock(m)` on a mutex it does not hold. L gives no behavior to a program
  that is not well formed, and the theorem does not cover it. This is a
  property of the source program under L's semantics, like data-race freedom;
  the translation plays no part in it.
- **Cancellation is cooperative and deferred.** `cancel(t)` sets `t`'s flag.
  `t` observes it at a cancellation point (`sleep`, `recv`, `select`, `join`)
  only if that point would block; `t` then ends without releasing anything it
  holds. A canceled thread that holds a mutex leaves it locked, as in POSIX
  without cleanup handlers. (Items 6, 9.)
  [owner, 2026-11-09, kept as taken] A thread that ends this way has
  ended like any other: `join(t)` on it completes. In Dex its last Step
  publishes to `join/t` before it decides DeadEnd (Section 8.3).
- **Emit is asynchronous.** `emit(a)` is issued as one atomic action; the
  `out(a)` event happens later, after any interleaving of other threads, and
  the thread continues only after `out(a)`. If the program exits between the
  issue and `out(a)`, `out(a)` may still happen after `exit` or never happen.
  (Items 9, 18.)
- **Exit.** `exit(v)`, or the return of `main`, ends the program at once:
  every other thread stops, except that issued emits may still deliver. A
  thread that ends otherwise just ends. A program whose threads all end
  without `exit` is stuck forever. `exit(v)` may carry any value, including
  null; `fail(s)` carries a string. (Item 10.)
  [owner, 2026-11-09, kept as taken] `fail(s)` ends the program the same
  way as `exit(v)`: every thread stops, an issued emit may still deliver or
  never deliver, and input that arrives afterwards is `refused`.
- **External input.** Each external channel is a FIFO queue. Threads blocked
  in `recv(c)` on it are served in FIFO order of blocking. Input that arrives
  after exit is `refused`. (Items 4, 21.)
- **Clock and timing model.** [owner, 2026-11-09] L has an internal clock in
  microseconds. It advances by idle ticks. L has a **fixed timing model**:
  each kind of statement takes a fixed number of **stamped actions**, and the
  clock strictly increases between any two stamped actions, as Dex commit
  timestamps strictly increase within a partition (`command.proto:39-40`).
  The counts are the numbers of Dex commits of the translation (Section 8.2),
  so an L run and its Dex run can carry the same timestamps. A translated
  program uses no SubFlows, so it lives in one partition.

  | L step | Stamped actions | Where its atomic action happens |
  | --- | --- | --- |
  | `begin(v)` | 1, then 2 more before `main`'s first statement | — |
  | `seq`, `skip` | 0 | — |
  | A statement that does not block: local assignment, shared read, shared write, `if`, each test of `while`, call, return, `fork`, new mutex, `unlock`, `signal`, `broadcast`, `emit`, `throw`, entering `try`, `cancel`, `exit`, `fail`. Also the end of a `try` body and the end of a procedure body. | 2: begin, complete | A shared read at begin. An `emit` is issued at begin. Every other atomic action at complete. |
  | A blocking statement: `lock`, `recv`, `join` | 4: begin the wait, register the wait, begin the continuation, complete it | At register |
  | `wait(cv, m)` | 10: 2 to release `m` and join `cv`'s queue, 4 to wait for the signal, 4 to re-lock `m` | At the first complete, and at the two registers |
  | `sleep(d)`, `select` with `after(d)` | 6: 2 for Enter, then 4 as a blocking statement, whose register is Arm | Enter at the first complete; Arm at register |
  | `in(c, v)` while the program runs | 1 | — |
  | `fire` | 1 for a batch of `k ≥ 1` consecutive `fire` actions | — |
  | `out(a)`; the delivery or drop of an issued emit after the end; `refused(c, v)` | 0 | — |

  A thread that observes its cancel flag ends at the complete of that
  statement's continuation. A statement that halts its thread (see Halting)
  takes the actions of its first part: 4 for `lock`, `recv`, and `join`, 2
  for any other statement.

  Why a fixed model is needed: a deadline is the Arm timestamp plus whole
  seconds. Two runs can carry the same deadlines only if the same number of
  stamped steps lies between any two Arms. With different counts on the two
  sides, the order of two deadlines can differ once enough steps lie between
  their Arms, and a `select` makes that order observable.

  Rationale [owner]: real programs cannot rely on timing races. A program
  whose outcomes do not depend on the order of two deadlines means the same
  under this timing model as under any other. The theorem adopts this one
  model, the one aligned with Dex, which makes the equivalence exact without
  narrowing what the theorem covers in practice. It is like counting one time
  unit per instruction in algorithm analysis. The theorem does not say that a
  program is equivalent to itself under another cost model: as the paragraph
  above shows, other counts can reorder two deadlines and change the outcome
  of a `select`.
- **Timers fire late, in order, by an explicit action.** A timed operation,
  `sleep(d)` or `select` with an `after(d)` branch, takes two actions. *Enter*
  gives it the next **timer number** `n` from one program-wide counter.
  *Arm*, later, sets its deadline to `now + d` and blocks the thread; other
  actions may come between Enter and Arm. The timer completes only by the
  internal action `fire`, which:
  - is enabled at any time no earlier than the deadline, and possibly much
    later (fairness: a timer that stays due is eventually fired);
  - fires only the **least** due timer in the order `(deadline, n)`. So the
    timers of one program fire in deadline order, and timers with equal
    deadlines fire in timer-number order. A later-due timer never fires while
    an earlier-due one is still armed and due.
  - shares its clock stamp with the `fire` actions that follow it with
    nothing in between (the timing model above), as one [TIMER-FIRE] commit
    fires a prefix of the due timers.
  A timer is never complete at the moment it is armed, even for `d = 0`. A
  blocked timed operation is decided by whichever happens first: its `fire`,
  the arrival of its message, or a cancel; the loser's timer is disarmed. So a
  message or cancel that arrives after the deadline but before `fire` wins.
  This mirrors [TIMER-FIRE], which fires an ordered prefix of a run's due
  timers only when the timer service gets to it
  (`server-zd/crates/dexzd-engine/src/effects/timer_service.rs:116-155`), and
  the per-change evaluation of a waiting Step
  (`server-zd/crates/dexzd-apply/src/steps/wait_conditions/mod.rs:166-188`,
  `wait_conditions/mod.rs:140-164`). Section 8.2 shows that Dex's order is
  exactly `(deadline, n)` for translated programs. Durations are whole
  seconds. (Item 12.)
- **Join.** Each thread is joined at most once. [owner, 2026-11-09, kept as taken]
  A thread that ends leaves one join token. A `join(t)` takes the token if it
  is there; otherwise it observes its own cancel flag, and otherwise it
  blocks. Threads blocked in `join(t)` are served in FIFO order of blocking.
  So a second `join(t)` blocks forever, unless a cancel ends it; and so does a
  `join` of a thread that never ends.
- **`select`.** [owner, 2026-11-09, kept as taken] `select` has exactly one
  `recv` branch and one `after` branch. A `select` without `after` is not a
  separate construct: it is `recv(c)` followed by the branch (Section 8.3).
- **Handles and literals.** [owner, 2026-11-10] L literals are only null,
  Boolean, integer, and string. Handles cannot be forged:
  - A mutex handle comes from only two places: `newMutex`, and the
    static-mutex expression, which names one of the program's statically
    known mutexes by its index.
  - A thread handle comes only from `fork`. So no thread holds its own
    handle, and no thread holds the handle of `main`: a thread cannot cancel
    or join itself, and `main` is never canceled.
  - A condition variable handle is formed from a mutex handle and an index.

  - The environment sends plain data only [owner, completing the decision
    of 2026-11-10]: the program input of `begin(v)` and every message of
    `in(c, v)` and `refused(c, v)` are literals (Section 5.3). So a lock
    handle can only be created by `newMutex` or named by the static-mutex
    expression, and thread handles are likewise unforgeable: no handle
    reaches a program through its input.

  So a thread can name a created mutex only after `newMutex` created it. A
  static-mutex expression whose index is beyond the program's statically
  known mutexes names a mutex that never exists; `lock` on it blocks forever,
  in L and in Dex.
- **Halting.** [owner, 2026-11-09] L has no static types. A statement whose
  operand has the wrong type halts its thread: a non-Boolean condition, a
  `lock` of a value that is not a mutex, a call of an unknown procedure or
  with the wrong number of arguments, a negative duration, and the like. An
  uncaught exception whose value is a string `s` fails the program with
  `fail(s)`, in any thread; an uncaught exception with a non-string value
  halts its thread. A halted thread takes no further action. It does not end,
  so it is never joinable, and it releases nothing it holds. In Dex its Step
  decides DeadEnd and publishes nothing.

### 8.2 General scheme

Compile each L thread to continuation-passing Steps.

- A program point is a Step type. Thread-local variables, the thread ID, and
  the call stack are the Step input, passed by GoTo.
- **One Step per statement.** [owner, 2026-11-09] Each L statement is its own
  Step, whose Execute takes two commits ([STEP-START] and
  [STEP-COMPLETE-EXECUTE]). A blocking statement's Step has a WaitFor, which
  adds two commits. `seq` and `skip` are not Steps. `sleep` and `select` run a
  Step whose Execute only GoTos `Timed`, and then the `Timed` Step.
  `wait(cv, m)` runs three Steps: the release, the wait for the signal, and
  the re-lock. The start Step runs before `main`'s first statement. These
  commit counts are L's timing model (Section 8.1).
- Shared variables are Attributes. Each Step method performs **at most one**
  shared access: a read is taken from the Execute snapshot at Start; a write
  is committed at Complete. So a read linearizes at [STEP-START] and a write
  at [STEP-COMPLETE-EXECUTE]. No Attribute locks are used. (Item 1.)
- The waiter list of a condition variable is the AttributeMap instance
  `waiters/cv`. A snapshot holds an AttributeMap instance only when the Step
  selects it (Section 4.2, `snapshot`). So the Step of a `wait(cv, m)`,
  `signal(cv)`, or `broadcast(cv)` declares the exact instance `waiters/cv`
  in its Execute load selection (`execute_load_attribute_map_instances`,
  `protos/dex.proto:472`). The Step that moves to it sets that option; it
  knows `cv` from the thread state it passes on. Shared variables are plain
  Attributes, with keys without `/`, and need no selection.
- Each `emit` is the only effect of its own Execute. The issue is
  [STEP-START], `out(a)` is [WORKER-EFFECT], and the thread continues at
  [STEP-COMPLETE-EXECUTE].
- **One Step type for every timed operation.** Every `sleep` and every
  `select` with an `after` branch runs in one generic Step type, `Timed`,
  whose input carries the duration, the Channels to wait on, and the
  continuation. Its WaitFor returns a wait with exactly one timer, at index 0,
  and its Execute dispatches on the result and GoTos the continuation. Then:
  - L's *Enter* is the commit that creates the `Timed` Step, and the timer
    number `n` is its Step number: the ID is `Timed-n`, from the per-type
    counter in creation order (`server-zd/crates/dexzd-apply/src/steps/step_creation.rs:40-52`).
  - L's *Arm* is the WaitFor completion commit, which sets the fire time to
    its own timestamp plus `d` seconds (`wait_conditions/mod.rs:89-108`). So
    the L deadline and the Dex fire time are equal.
  - Dex fires due timers of the run in index order `(fire time, encoded
    TimerId bytes)` (`server-zd/crates/dexzd-keyspace/src/row_keys.rs:303-314`).
    All the program's timers are wait timers of one run, of run attempt 1, at
    index 0, with IDs `Timed-n`. Their encoded TimerIds are
    `08 01 12 ℓ 0a len(id) id`, with the index field omitted because it is 0
    and `ℓ = len(id) + 2`. Bytes compare first by `ℓ`, so fewer digits come
    first, and then, for equal length, by the decimal digits of `n`. For
    `n < 2³²` (the Step number is a `uint32`, `state.proto:385`), `ℓ < 128`
    fits one varint byte, and the byte order is exactly numeric order on `n`.
    [assumption, from the protobuf encoding rules] So for equal fire times
    Dex's order is timer-number order, as in L.
  - [owner, 2026-11-09] The model idealizes Step numbers and timer numbers as
    unbounded natural numbers, ordered numerically. The real limit is fewer
    than 2³² Step executions of one type per run. The numeric timer-ID order
    is exact within that limit, and the theorem says nothing about a run that
    exceeds it.
  - [TIMER-FIRE] fires a non-empty prefix of the due timers in that order
    (Section 4.5), processing them one after another in one commit. L's
    one-at-a-time `fire` of the least due timer produces the same states:
    a prefix of size `k` is `k` consecutive `fire` actions with nothing in
    between, and each L `fire` is a prefix of size 1. The `k` actions share
    one clock stamp, as the prefix is one commit (Section 8.1, timing model).
- The translation never uses CancelSteps, CancelSiblingSteps, GracefulComplete,
  RPCs, SubFlows, or Attribute locks. The only Step cancellation is the Flow
  close, after which nothing observable happens except effects already in
  flight, which L's emit semantics allows. (Items 3, 6, 7, 9, 13.)
- The start Step publishes one token to each statically known mutex Channel
  before it starts `main`.

### 8.3 Mapping table

| L construct | Dex mapping | Depends on |
| --- | --- | --- |
| Sequence, `if` | `seq` and `skip` are not Steps. `if` is a Step that GoTos the first Step of the taken branch. (Section 8.2, one Step per statement.) | — |
| `while` | A Step that GoTos itself or the loop exit. | — |
| Shared read / write | One Execute per access: read from the snapshot, or write one Attribute. | 1 |
| Call and recursion | Push the return point on an explicit stack in the Step input. No SubFlow. | — |
| `fork t` | GoToMany: the parent's continuation and `t`'s first Step, with a fresh thread ID derived from the parent's ID and a counter in the input. | — |
| `join(t)` | `t`'s last Step publishes to the Channel `join/t` and decides DE. The joiner waits `AnyOf(join/t.ForOne(), cancel/self.ForOne())`. | 4, 5 |
| `exit(v)` / return of `main` | FC(`{"exit": enc(v)}`): a tagged value, never null, so `collect` keeps it (Section 5.3). | 10, 15 |
| `fail(s)` / uncaught exception with a string value `s` | FF(s) with `s` a string; the close message is `s` (Section 5.3). An uncaught exception with any other value halts its thread (Section 8.1, Halting). | 15 |
| Thread end | DE. | 10 |
| `lock(m)` | WaitFor `Until(m.ForOne())`: a ForOne waiter on `m`'s token Channel. FIFO by registration. | 4, 6 |
| `unlock(m)` | Execute publishes one token to `m`. | — |
| New mutex | Execute publishes the first token. | — |
| `wait(cv, m)` | One Execute, under `m`: append the waiter ID `w` to the Attribute `waiters/cv` and publish `m`'s token. Its Step selects `waiters/cv` in its Execute load selection (Section 8.2). Then a Step that waits `Until(cv/w.ForOne())` on the per-waiter Channel instance `cv/w`, then `lock(m)` again. | 1, 4 |
| `signal(cv)` | One Execute, under `m`: if `waiters/cv` is not empty, remove its head `h` and publish to `cv/h`. Its Step selects `waiters/cv` as `wait` does. | 1 |
| `broadcast(cv)` | As `signal`, for every waiter in order. | 1 |
| `sleep(d)` | GoTo a `Timed` Step (Enter); its WaitFor returns `AnyOf(Timer(d), cancel/self.ForOne())` (Arm). | 12 |
| `select { recv(c) → s₁ \| after(d) → s₂ }` | GoTo a `Timed` Step; its WaitFor returns `AnyOf(Timer(d), c.ForOne(), cancel/self.ForOne())`. Only the winner consumes. A `select` without `after` uses an ordinary Step. | 5, 12 |
| `recv(c)` on an external channel | WaitFor `AnyOf(c.ForOne(), cancel/self.ForOne())`. The environment's `pub` fills `c`. | 4, 5 |
| `emit(a)` | An Execute whose only effect is `a`. | 9, 18 |
| `throw` / `try` | The handler stack lives in the Step input. `throw` is a GoTo to the innermost handler's Step. No Worker errors. | 19 |
| `cancel(t)` | One Execute publishes to `cancel/t`. At a cancellation point, a wait that cannot complete at once is won by `cancel/t`, and the thread ends: its Step publishes to `join/t` and decides DE, as any thread end does. | 4, 5 |

Why the synchronization encodings are sound under these L semantics:

- **Mutex.** The token Channel has only ForOne waiters, so `wake` serves them
  in registration order, and a token present at registration means no waiter
  was blocked (item 4). The token is consumed only by a Step that is never
  canceled before its Execute commits, except by the Flow close, after which
  the mutex is unobservable (item 6). This answers finding 8 by restricting
  Dex-side cancellation, not by a recovery protocol; the restriction holds
  because the translation emits no CancelSteps and `E_adm` has no `stop` or
  RPCs.
- **Condition variable.** `waiters/cv` is read and written only under `m`, so
  its one-access-per-method rule is relaxed here: the Execute that reads and
  writes it holds `m`, and no other thread touches it. Per-waiter Channels
  `cv/w` make the signal reach exactly the dequeued waiter, even if it has
  not registered its wait yet, because the message stays queued. No waiter is
  ever canceled while waiting (`wait` is not a cancellation point), so the
  waiter list never goes stale. This answers finding 10 without spurious
  wakeups.
- **Cancellation.** The cancel Channel `cancel/t` is a per-thread instance;
  AnyOf consumes only from the winning condition (item 5), so a cancel never
  eats an input message or a token, and a message never eats a cancel. A
  leftover `cancel/t` message after `t` ends is never read.
- **AnyOf at registration.** A timer is never done when a wait registers, so
  at registration the Channel conditions decide in declaration order: the
  operation's own Channel before `cancel/self`. This matches "observed only if
  the point would block". Later, each change re-evaluates the wait, and a timer
  and a message never become ready in the same evaluation (item 12).

Constructs whose mapping still depends on an open question: shared accesses
(1), FIFO wake-up order (4), timed `sleep` (12, 22), and external effects
after exit (18).

## 9. Future work

- `server-zd/crates/dexzd-simulation` runs deterministic simulation tests of
  the engine against simulated object storage, clocks, Workers, and crashes.
  An optional check for Section 11: export the `fx` and commit events of a
  simulated run and test that the trace lies in the repetition closure of a
  failure-free trace.
- RPC entry points for L, with ABORTED as an observable result [owner].
- The Lean statement needs the projection of Section 5.3, the two
  equivalences of Sections 5.4 and 5.5, and the fairness assumptions F0 to F6.
- Failures beyond the fragment of Section 11 (D1):
  - **SubFlows under failures.** Cross-Flow messages are delivered at least
    once and deduplicated by the receiver. The model needs rules for
    outbox redelivery and inbox deduplication
    (`server-zd/crates/dexzd-engine/src/effects/outbox_delivery.rs:9-11`).
    This extension is the next one to do.
  - **RPCs under crashes.** RPC locks and in-flight RPCs live in engine
    memory, and a crash loses them. A crash before the RPC's commit lets a
    client retry run the handler again. The RPC request table answers a
    retry with the same request ID after the commit, until the entry is
    evicted. The extension needs a hypothesis that a retry comes before
    eviction (Section 7, item 25). The Go SDK's InvokeRPC mints a new
    request ID on every call and offers no option to pass one (at
    `55721e878`: `sdk-go/dex/client.go:1765`, `sdk-go/dex/options.go:553-561`).
    [owner, 2026-11-10] The SDKs will get an optional caller-supplied request
    ID ([#1519](https://github.com/superdurable/dex/issues/1519)).
  - **An ASYNC variant.** Under ASYNC a Worker can receive a start that a
    crash then rolls back, and the same attempt number runs again from a
    fresh snapshot (D2). A weaker theorem would have to allow different
    effect lists per attempt.
  - **StopFlow, SetAttributes, DeleteChannelMessage, and SkipTimer under
    retries.** They are outside `E_adm`. StopFlow has no request ID and
    resolves an empty run ID to the newest run (Section 7, item 25).
    [owner, 2026-11-10] The SDKs will accept an optional run ID for
    StopFlow ([#1520](https://github.com/superdurable/dex/issues/1520)).

## 10. Review log

Revision 2 answers an adversarial review of revision 1 and two owner
decisions. Each finding was checked against the code before the spec changed.

| # | Finding | Resolution |
| --- | --- | --- |
| Owner Q3 | Model RPC cancellation with the graceful-completion check. | Accepted. [RPC-COMMIT] runs `gcheck()` after cancellations and new Steps, marked "follows #1450, not yet merged"; Open question 3 resolved. |
| Owner Q11 | ABORTED is observable; RPC entry points out of the core theorem. | Accepted. `ret(id, ABORTED)` in `Obs`; RPCs excluded from `E_adm` (5.3); Open question 11 resolved; L external input is Channel publications only (8). |
| 1 | RPC ABORT and early NOT_FOUND paths never released RL or decremented Tx. | Confirmed: the guards are scoped values and drop on every exit (`registry.rs:809-846`, `rpc_invocation.rs:179-200`, `:287-301`). Added [RPC-FINISH] for every path; Call, Snap, Read-only, and Commit now only set `Done(result)`. [PENDING-CLOSE-CHECK] runs after the guard drops (4.7, 4.9). |
| 2 | Finite weak traces cannot tell DeadEnd-and-RUNNING from an unscheduled close. | Accepted. Added fair maximal traces with markers `✓`, `δ`, `∞`, and fairness assumptions F1 to F6 (5.5), alongside the safety statement (5.4). Revision 3 added F0, so the current list is F0 to F6. |
| 3 | [TIMER-FIRE] allows only a prefix; retry delay can let a later timer fire first. | **Rejected for failure-free runs; the spec keeps the prefix and explains why.** A row enters the retry delay only after its TimersFired command committed (`timer_service.rs:158-184`, `:283-287`; `effects/mod.rs:427-455`), and that commit disarmed it (`step_commands.rs:156-163`). So skipping it never lets a later timer overtake an unfired one. A subset abstraction would add runs the code cannot produce (example in 4.5), which is unsafe for the L ⊆ Dex direction. AnyOf outcomes are unchanged. |
| 4 | One label per transition cannot express StopFlow or an RPC commit that closes. | Accepted. Transitions carry an ordered event sequence (4.1); [STOP] emits `[stop, closed]`; RPC return is its own step [RPC-FINISH]. |
| 5 | `Obs` exposes raw IDs and client operations L lacks. | Accepted. Added the admissible environment `E_adm` and the projection `π` to `Obs_L` (5.3). |
| 6 | A Flow timeout is not a user Timer Step that ForceFails. | Confirmed (`step_commands.rs:211-301`, `timer_skip.rs:38-81`). Moved from Derived to Excluded; no Flow timeout in programs or environment (1.3); Open question 24. |
| 7 | Request deduplication applies without failures. | Confirmed (`client_writes.rs:41-65`, `:138-147`, `:260-268`; `rpc_committed.rs:58-74`). Fresh request IDs required (1.3, 4.6); Open question 23. |
| 8 | Canceling a Step that consumed a mutex token loses the token. | Confirmed (`wait_conditions/mod.rs:374-460`, `:250-281`). Chose the simplest sound option: L cancellation is cooperative, `lock` is not a cancellation point, and the translation never cancels Steps; only the Flow close does, after which the mutex is unobservable (8.1, 8.3). |
| 9 | "Emit only when no longer cancelable" cannot be enforced in Execute. | Confirmed (`client_writes.rs:322-355`, `step_results.rs:863-872`, `invocations.rs:375-381`). L's `emit` is asynchronous: issue, then `out(a)`, with exit possible in between and `out(a)` possible after exit (8.1). |
| 10 | Condition-variable waiter count goes stale on cancel. | Accepted. Replaced the count with a FIFO waiter list and per-waiter Channels, and made `wait` not a cancellation point, so no waiter is canceled while waiting; no spurious wakeups needed (8.1, 8.3). |
| 11 | No global clock. | Confirmed (`command.proto:35-40`, `lib.rs:9-15`). Per-partition clocks (3.4, 4.1, 4.5); cross-partition SubFlows discussed in 4.8; Open question 22. |
| 12 | Duplicated condition IDs resolve within the first matching kind. | Confirmed (`condition_plan.rs:103-129`). Wording fixed in `plan` (4.2) and Open question 5. |
| 13 | UpdateFlowConfig has no rule. | Confirmed (`command_handlers/mod.rs:416-418`, `client_writes.rs:366-413`). Run config fixed for the theorem; excluded (1.3). |

Revision 3 answers the re-review of revision 2. The re-review accepted the
rejection of finding 3 for failure-free runs and verified the fixes for
findings 1 and 4 to 13. Its five new findings, all checked against the code:

| # | Finding | Resolution |
| --- | --- | --- |
| R2-1 | F1 lets an idle run of only [TICK] after DeadEnd or after the close count as `∞`. | Confirmed: an idle RUNNING Flow with no active Step does nothing (`step_decisions.rs:120`, `flow_start.rs:204-205`). Maximal traces are now relative to a fixed input word `w`, [TICK] is not a progress step, and the marker is decided by the number of progress steps: `∞` needs infinitely many (5.5). Added F0 (inputs arrive). |
| R2-2 | `✓` is assigned at the close, but a canceled Worker may still emit afterwards. | Confirmed (`invocations.rs:376-381` only requests cancellation; `step_results.rs:863-872` drops only the result). `✓` now requires finitely many progress steps, and F4 makes every task, canceled or not, leave `Θ`; so a `✓` trace contains every post-close effect and is never a proper prefix of another fair trace. Matches L's asynchronous emit (5.5, 8.1). |
| R2-3 | `E_adm` does not put the start first. | Confirmed: a publication with no run is rejected with NOT_FOUND (`run_cursor.rs:111-113`). An admissible input word now begins with the unique start, and every later input is a publication (5.3). |
| R2-4 | `dec` is undefined for `exit(null)`; `fail(err)` does not extract the message. | Confirmed: `collect` drops absent and null outputs (`step_decisions.rs:186-196`); FF stores its string input as the message with STEP_DECISION_FAILING_FLOW (`step_decisions.rs:92-119`). Exit values are encoded as the tagged non-null value `{"exit": enc(v)}`, and `π` maps a failed close to `fail(msg)` read from that structured error (5.3, 8.3). |
| R2-5 | L timers fire exactly at the deadline, but Dex fires arbitrarily late. | Confirmed (`timer_service.rs:116-155`; `wait_conditions/mod.rs:167-188`). L now has an explicit nondeterministic `fire` action, enabled no earlier than the deadline, and blocked operations are decided by the order of `fire`, message arrival, and cancel (8.1, Open question 12). |

Revision 4 answers the third review of revision 3. Its two findings, both
checked against the code:

| # | Finding | Resolution |
| --- | --- | --- |
| R3-1 | The input word counts only accepted input events; a publication after the close produces only `rejected(...)`, so an L run that exits and then refuses an input has no matching Dex run. | Confirmed: a publication to a closed run is rejected with NOT_FOUND and emits no `pub` event (`client_writes.rs:206-215`). The input word is now a sequence of attempted requests, accepted and rejected alike; each request occurs exactly once, in order, and its commit produces either the input event or `rejected(...)` (5.3). Runs for `w` and F0 are restated over request commits (5.5). |
| R3-2 | Independent late `fire` actions in L let a later-due timer fire before an earlier-due one, which Dex's ordered prefix forbids. | Confirmed (`timer_service.rs:140-155`, `row_keys.rs:303-314`, `wait_conditions/mod.rs:166-188`). L's `fire` now fires only the least due timer in the order `(deadline, timer number)`, the timer number coming from a program-wide counter at Enter, and L's clock strictly increases between actions (8.1). The translation runs every timed operation in one Step type `Timed` with one timer at index 0, so the timer number is the Step number, the deadline is the fire time, and the encoded-TimerId tie-break is numeric order on the Step number; a Dex prefix of size `k` equals `k` consecutive L `fire` actions (8.2). |
| R3-3 | The revision-2 log row still says "F1 to F6". | Fixed: that row now notes that revision 3 added F0, so the current list is F0 to F6. |

Revision 5 records the owner decisions of 2026-11-09 on the review of the
Lean statements (`formal/lean/`, commit `cc123891a`), and the two model
findings of that review.

| # | Point | Decision or resolution |
| --- | --- | --- |
| L1 | Cost model. L's clock "strictly increases between any two L actions", but the spec did not fix how many actions a statement takes. Trace equality then fails for long runs: the order of two deadlines depends on the number of stamped steps between their Arms. One [TIMER-FIRE] commit of `k` timers also needed `k` clock values in L. | **Owner decision, 2026-11-09.** L has a fixed timing model, aligned with the Dex commits of the translation, and a batched `fire` shares one stamp. Section 8.1 tabulates the counts and gives the rationale: real programs cannot rely on timing races, so a fixed timing model leaves the meaning of correct programs unchanged. Section 8.2 fixes one Step per statement, and the "Sequence, `if`" row of Section 8.3 follows it. Appendix A, item 29. |
| L2 | Well-formed programs. The spec stated "`wait(cv, m)` requires `m` held" and the like without a behavior for a program that breaks it; `unlock` of a mutex not held was not covered. | **Owner decision, 2026-11-09.** The theorem covers well-formed programs only. Stated in Sections 0, 5.4, 5.5, and 8.1; Appendix A, item 28. The analogy is a theorem about data-race-free programs. |
| L3 | Unbounded counters. The numeric timer-ID order holds for Step numbers below 2³². | **Owner decision, 2026-11-09.** Step numbers and timer numbers are idealized as unbounded natural numbers. The real limit is fewer than 2³² Step executions of one type per run; the order is exact within it. Section 8.2; Appendix A, item 27. |
| L4 | Uncaught exceptions with a non-string value, and dynamic type errors, had no stated behavior. | **Owner decision, 2026-11-09.** Each halts its thread. Section 8.1, Halting; Section 8.3, `fail` row; Appendix A, item 30. |
| R4-1 | The Lean model loaded every Attribute into a snapshot, but `snapshot` (Section 4.2) excludes keys with `/` unless `loadMaps` selects them, and the translation reads `waiters/cv`. | Confirmed (`attempt_snapshot.rs:145-158`). The Lean model now applies the selection, and the translation declares the exact instance `waiters/cv` in the Execute options of the Steps that read it. Sections 8.2 and 8.3 say so. |
| R4-2 | The Lean syntax of L exposed the runtime-only `park` as a source statement. | Fixed in Lean: source statements and runtime instructions are separate types, so the theorem ranges over programs built from the constructs of Section 8.1 only. No spec change. |

The same review listed nine more choices that the Lean model had taken
where the spec was silent. Each is **kept as taken, owner accepted by not
objecting, 2026-11-09**, and is now written into the spec.

| # | Choice | Status and place |
| --- | --- | --- |
| K3 | A condition variable belongs to one mutex, fixed by its identity; that is the `m` `signal(cv)` and `broadcast(cv)` require. `wait(cv, m)` with another mutex is a type error. | Kept as taken, owner accepted by not objecting, 2026-11-09. Section 8.1, Condition variable; Appendix A, item 31. |
| K4 | A thread that ends by observing its cancel flag is joinable; its last Step publishes to `join/t`. | Kept as taken, owner accepted by not objecting, 2026-11-09. Section 8.1, Cancellation; Section 8.3, `cancel` row; Appendix A, item 32. |
| K5 | A thread that ends leaves one join token; a second `join(t)` blocks forever. | Kept as taken, owner accepted by not objecting, 2026-11-09. Section 8.1, Join; Appendix A, item 33. |
| K8 | `fail(s)` ends the program as `exit(v)` does: issued emits may still deliver, and later input is `refused`. | Kept as taken, owner accepted by not objecting, 2026-11-09. Section 8.1, Exit; Appendix A, item 34. |
| K9 | Under `E_adm`, the start input and every published message are encoded L values. | Kept as taken, owner accepted by not objecting, 2026-11-09. Section 5.3; Appendix A, item 35. |
| K11 | The start Step's `skipWaitFor` is read from its definition; it selects no AttributeMap and no Channel. | Kept as taken, owner accepted by not objecting, 2026-11-09. Section 4.3; Appendix A, item 36. |
| K13 | A task stays in `Θ` until its result commits or it is dropped, which fixes the reading of F4 and F5. | Kept as taken, owner accepted by not objecting, 2026-11-09. Section 5.5; Appendix A, item 37. |
| K14 | L's fair runs are given by the L forms of F0 to F6. | Kept as taken, owner accepted by not objecting, 2026-11-09. Section 5.5; Appendix A, item 38. |
| K15 | `select` has one `recv` branch and one `after` branch; without `after` it is `recv(c)` followed by the branch. | Kept as taken, owner accepted by not objecting, 2026-11-09. Section 8.1, `select`; Appendix A, item 39. |

The numbers K3 to K15 are the point numbers of "Points for the owner" in
`formal/lean/README.md`.

Revision 6 records one owner decision of 2026-11-10. The proof of the safety
theorem found the point (Lean commit `56057f7a7`).

| # | Point | Decision or resolution |
| --- | --- | --- |
| L5 | Forged mutex handles. A literal could be any value, so a program could write the handle of a mutex before `newMutex` created it. A thread that locked it blocked forever in L, because creating a mutex does not serve blocked lockers; in Dex the first token that `newMutex` publishes wakes the waiting Step. For `locker(): lock(<forged handle>); emit(1)` and `main: fork locker(); sleep(0); newMutex(); recv(9)`, L had the fair maximal trace `[begin(0)]` with `δ`, and every fair run of the Flow emits `out(1)`. | **Owner decision, 2026-11-10.** Mutex handles are unforgeable: L literals are only null, Boolean, integer, and string, and a mutex handle comes only from `newMutex` or the static-mutex expression. The same rule is applied to thread handles, which come only from `fork`: it follows from the restriction on literals at no cost, and it rules out handles of threads that do not exist. Section 8.1, Handles and literals; Appendix A, item 40. |

Revision 7 records the completion of that decision for the environment's
input. The proof of the safety theorem found the point (Lean commit
`83cbcc202`).

| # | Point | Decision or resolution |
| --- | --- | --- |
| L6 | Forged handles through the input. After L5 no expression could forge a handle, but the program input and every external message were arbitrary L values, and an L value can be a mutex handle. The counterexample of L5 came back with the forged handle as the program input: `locker(h): lock(h); emit(1)` and `main(x): fork locker(x); sleep(0); newMutex(); recv(9)`, started with `x` the handle of the mutex `main` creates. | **Owner decision, completing that of 2026-11-10.** The environment sends plain data only: the start input and every external message are literals (null, Boolean, integer, string), and `E_adm` admits only encoded literals. So a lock handle can only be created by `newMutex` or named by the static-mutex expression, and thread handles are likewise unforgeable. Sections 5.3 and 8.1; Appendix A, item 41. Row K9 is narrowed accordingly: "encoded L values" there now means encoded literals. |

Revision 8 answers the review of the safety proof (Stage 2a of the Lean
development). It changes no definition.

| # | Finding | Resolution |
| --- | --- | --- |
| S1 | The rationale of L1 said that any fixed timing model leaves the meaning of a correct program unchanged. The paragraph before it says that different counts can reorder two deadlines and change a `select`. | **Reworded.** The rationale now speaks of the one timing model the theorem adopts, and says that the theorem does not imply equivalence under another cost model. Section 8.1, "Clock and timing model". |

Revision 9 adds the failure model of Section 11 for issue
[#1514](https://github.com/superdurable/dex/issues/1514), after the owner
decisions D1 to D8 of 2026-11-10. **The failure-free fragment is unchanged.**
No failure-free rule changes its premise, effect, or events. So the
expressiveness proof in `formal/lean/`, which formalizes revision 8, stays
valid. The changes to Sections 1.3, 3.4, 4.4, and 4.5 are clarifications of
the code behind unchanged rules. Every new citation is at `55721e878`.

| # | Point | Resolution |
| --- | --- | --- |
| F1 | Section 1.3 listed StartFlow among the inputs that the request table deduplicates. | **Clarified.** StartFlow deduplicates through the Flow directory's start request ID, and only with `ignore_already_started_error`; the request table is bounded and per run. Section 1.3. |
| F2 | Section 3.4 said that run attempts are elided. | **Clarified.** Apply checks `expected_run_attempt` on every run command. Only time travel changes the run attempt, so the check never fails in the theorem's runs. Section 3.4. |
| F3 | The premise of [STEP-COMPLETE-WAITFOR] said "the same task". | **Clarified.** The code's fence is Step execution, method, attempt number, phase Running, and task ID. It selects the same results without failures. Section 4.4; [RESULT-STALE] in Section 11.3. |
| F4 | Section 4.5 read as if apply checked that a timer is due. | **Clarified.** Apply fires whatever the command names; the timer service and the command stamp make it due, under the clock assumption D6. Section 4.5. |
| F5 | Facts of Section 7 that failures change: locks held through Backoff, retry timers left armed after a cancel, the bounded request table, StopFlow's run resolution, the interrupt commit, and timeouts kept only in memory. | **Recorded** as Open question 25. |
| F6 | The failure model itself. | **Added** as Section 11: decisions, state, rules, events, theorem statements, and the abstraction map. Appendix A, items 42 to 70. Section 9 lists the failures left for later. |
| F7 | The plan stated safety as "`t` is a failure trace if and only if `t` is a repetition of a failure-free trace". Not every repetition is reachable: an attempt emits its effects in order from the first. | **Stated as two inclusions.** Forward: erasing repeated `fx` from a DexF trace gives a Dex trace. Backward: every Dex trace is a DexF trace. The end-to-end corollary follows the same shape. Section 11.5; Appendix A, item 58. |
| F8 | The clarification of Section 4.5 makes explicit that the failure-free [TIMER-FIRE] is faithful to the code only under the clock assumption D6. | **Recorded.** The rule is unchanged, so the Lean proof is unaffected. Appendix A, item 47. |

The adversarial review of revision 9 found six more points. Each was checked
against the code at `55721e878`. The review of crashes, recovery, and
takeover found nothing.

| # | Finding | Resolution |
| --- | --- | --- |
| F9 | [RESULT-STALE] required Running and a task ID of every StepAttemptFailed. A before-start failure has no task ID and matches a Pending attempt. | **Fixed.** The fence is split by command kind; "only older logs have an empty task ID" is limited to started-attempt commands. A before-start failure is proposed before any task ID or frame exists, so it emitted nothing (`attempt_handout.rs:222-235`, `:363-364`, `:610-629`). Section 11.3. |
| F10 | Infinitely many legal [START-RETRY] requests made DexF give `∞` where Dex gives `δ` or `✓`. | **Fixed** [decision]: a [START-RETRY] is not a progress step, like [TICK]. The liveness statement says so. Section 11.5; Appendix A, item 59. |
| F11 | [TIMER-FIRE]'s prefix holds at scan time, not at apply time, also without failures. | **Recorded, rule unchanged.** Open question 26 gives the scenario; Appendix A, item 7, points to it. Section 11.3 states the code's rule [TIMER-FIRE-SCAN] and leaves the choice for the failure proof open (OD1; Appendix A, item 60). |
| F12 | "A StartFlow retry commits nothing" was wrong: it is a log entry with a stamp. | **Fixed.** Log commit and run `seq` are kept apart (Sections 1.3, 11.1, 11.3). |
| F13 | One order-preserving relabeling cannot cover `seq` values that erased commands wrote. | **Fixed.** Section 11.6 lists each `seq`-derived field and says whether α maps it by `ζ`, restores it from a ghost, or ignores it. |
| F14 | α used the unstored fact "some attempt has returned"; I3 and I7 were not invariants. | **Fixed.** The virtual task's state is the ghost pair `h`, `v` (Section 11.2). I3 and I7 are weakened. Section 11.6. |

The re-review of these fixes found four more points, all accepted and
checked against the code at `55721e878`.

| # | Finding | Resolution |
| --- | --- | --- |
| F15 | α was not a function of the configuration: `ζ` needs to know which past commands were normal. Two runs, one with a normal [STEP-START] and one with an [ATTEMPT-HEARTBEAT] at the same `seq`, reach equal states but need different `ζ`. | **Fixed.** `ζ` and a counter `n` of applied normal commands are ghost variables of `Σ_F`, updated only by normal commands. α is defined from them. Sections 11.2 and 11.6. |
| F16 | The run `seq` and `next_run_seq` were both restored as `ζ` of the last normal command, which is off by one. | **Fixed.** They are `n` and `n + 1` (`run_cursor.rs:153-156`). Section 11.6. |
| F17 | The `seq` table missed `AttributeRecord.written_seq`, `SearchDocumentRecord.version`, `HistoryRecord.seq`, and the checkpoint `seq` fields. | **Fixed.** Added with evidence, together with hard-delete, outbox, inbox, and attempt-record `seq` fields. The table now says which modules were searched and which were not audited. Section 11.6. |
| F18 | "Attempt 1 has no snapshot yet" confused the attempt number with whether the invocation ever started: a returned attempt is Pending with attempt 1 and keeps its snapshot. | **Fixed.** The text now speaks of an invocation that has never started (`step_results.rs:138-145`, `step_results.rs:509-530`). Section 11.3. |

A further re-review found two definitional gaps, both accepted and checked
against the code at `55721e878`.

| # | Finding | Resolution |
| --- | --- | --- |
| F19 | The command classes were incomplete. StepAttemptQueued is applied but never finishes its run cursor, so it takes no run `seq`; "every applied command takes a run `seq`" was false. | **Fixed.** Section 11.2 has four classes: normal, failure, bookkeeping (applied, no run `seq`), and ignored or rejected. A search for handlers that skip `RunCursor::finish` found StepAttemptQueued and eight commands of excluded features; all are listed. Bookkeeping commands leave `n` and `ζ` unchanged and are stutters. |
| F20 | The `seq` table could not be made exhaustive: it missed Attribute Sync outbox entries, the search index value, and history window fields, and it cited `attribute_sync.rs:151`, which compares an outbox entry's `written_seq`, not the Attribute's. | **Fixed.** The table is inverted. It lists only the `seq`-derived values of the failure-free model and how α restores each. Every other persisted field has no counterpart in the model and is outside α by construction, sound for the same reason as the failure-free model. The completeness claim and the wrong citation are removed. Section 11.6. |

The next re-review found three more points, all accepted and checked against
the code at `55721e878`. The owner also decided four points on 2026-11-10.

| # | Point | Resolution |
| --- | --- | --- |
| F21 | The run `seq` criterion was "writes the run record". HistorySegmentUploaded writes the run record through `reconcile_run_storage` but takes no run `seq` (`segment_upload.rs:46`, `:87`, `:104`; `storage_accounting.rs:30`). A leftover sentence in Section 11.3 said every applied command takes a run `seq`, which StepAttemptQueued contradicts. | **Fixed.** The criterion is whether the command advances the persisted `next_run_seq`. Both sentences are corrected. Sections 11.2 and 11.3. |
| F22 | Every rejected command was a stutter, which loses `rejected(pub(...), NOT_FOUND)` for a Publish after the close (`client_writes.rs:213-214`, `command_handlers/mod.rs:292-294`). | **Fixed.** A rejected client input of the word maps to the same Dex rejection with the same event. Only internal ignored or rejected commands are silent. [START-RETRY] stays silent: its reply is a success, and `w↓` deletes it. Section 11.2; Appendix A, item 64. |
| F23 | `ps₀` was described as the raw `seq` of the first pend in some places. `pending_since_seq` stores the raw `seq` (`invocations.rs:114`), and the Dex value is its normal index. | **Fixed.** `ps₀(e, m) := ζ(s)` for the `seq` `s` of the first pend, used in Sections 11.2, 11.3, and 11.6, with the example. |
| Owner | PublishToChannel gets no request ID. The SDKs get an optional RPC request ID (#1519) and an optional StopFlow run ID (#1520). The server keeps its timer behavior, because no distributed system guarantees precise timer firing. | **Recorded.** Section 11.1 (D7), Section 9, Open question 26; Appendix A, items 61 to 63. OD1 stays open. |

The next re-review found one blocker, accepted and checked against the code
at `55721e878`. The owner also decided OD1 on 2026-11-10.

| # | Point | Resolution |
| --- | --- | --- |
| F24 | Retention and purge after the close were not modeled. A close arms a retention timer (`flow_close.rs:400-424`); its TimersFired takes a run `seq` and marks the run purging (`step_commands.rs:156-175`, `:201-206`; `run_purge.rs:73-98`); RunPurge deletes the directory entry and every run row (`run_purge.rs:216-228`). A StartFlow retry after the purge starts a second run. | **Fixed.** Section 1.3 adds the [assumption] that the model keeps a closed run forever; this changes no event under `E_adm`, because a publication is rejected with NOT_FOUND and FLOW_NOT_EXISTS before and after the purge, only the message text differs (`run_cursor.rs:114-119`, `:386-392`; `state_rows.rs:122-126`; `flow_coordination_error.rs:104-110`). Section 11.2 adds a sixth class, post-close housekeeping, mapped to stutters with a ghost copy `Rc` of the purged run. Hypothesis HP bounds [START-RETRY] to before the purge (Section 11.3), and the theorem assumes it (Section 11.5). The input word now includes requests the engine rejects before any proposal (D7). Appendix A, items 65 to 67. |
| F25 | OD1, which timer rule the failure proof uses. | **Owner decision, 2026-11-10: option A.** Both theorems keep the prefix rule [TIMER-FIRE] of Section 4.5 as an explicit modeling idealization, because no distributed system guarantees the order of timers due at almost the same time. [TIMER-FIRE-SCAN] stays documented as the code's behavior and is used by neither theorem. Section 11.3, Open question 26; Appendix A, items 7 and 60. |

The final review of the whole section found two blockers and two major
points, all accepted and checked against the code at `55721e878`.

| # | Finding | Resolution |
| --- | --- | --- |
| F26 | HP said a retry "reaches the server" before RunPurge commits. A RunPurge can be in the log but not yet durable when the retry arrives, and the retry is then ordered after it (`registry.rs:619-635`, `:651-654`). | **Fixed.** HP is a predicate on runs: in the committed log, every [START-RETRY] command is ordered before the RunPurge of its run. It is not a property of the input word. Section 11.3; Appendix A, item 66. |
| F27 | The word and "run for `w`" still required every request to commit, but after a purge the engine rejects a legal Publish with no log command (`client_operations/mod.rs:286-296`, `state_rows.rs:122-126`). | **Fixed.** One definition, the outcome word: requests in the order of their final outcomes (accepted commit, apply rejection, or engine rejection). New rule [ENGINE-REJECT] with no log entry and no stamp, in the rejected-input class, mapped to Dex's apply rejection with the same event. Sections 11.1, 11.2, 11.3, 11.5, and 11.6; Appendix A, item 48. |
| F28 | α built a virtual task only for an invocation holding a snapshot. A close clears the invocation and releases the snapshot (`flow_close.rs:323-328`, `step_decisions.rs:373-374`, `invocations.rs:344-381`), while a real task may still emit its first effect. | **Fixed.** A ghost `par` keeps `U`'s arguments from the first start. A canceled invocation keeps a virtual task, built from `par`, `v`, and `h`, while any real task of it is in `Θ` or `Z`; it leaves with the last one. This matches Dex's canceled tasks (Section 4.4, F4, F5). Section 11.6. |
| F29 | `Σ_F` had no up or down state, yet rules, fairness, and `δ` depend on it. | **Fixed.** `Σ_F` has `up`. Every commit and engine rule needs it; Worker rules and [TICK] do not. [CRASH] and [RECOVER] flip it (`registry.rs:774-778`, `node_start.rs:450-455`). With FF and FR the server is eventually up for good; F3, F5, and `δ` say how they treat a down server. Sections 11.2, 11.3, and 11.5. |

A last pass found one blocker and three major points, all accepted and
checked against the code at `55721e878`.

| # | Finding | Resolution |
| --- | --- | --- |
| F30 | Internal stutters still counted as progress, and nothing bounded them: a stale StepMethodCompleted replayed forever (`step_results.rs:863-872`) gave `∞` in DexF where Dex gives `✓`. | **Fixed** [decision]: one uniform rule. A DexF step is a progress step if and only if its image under the transition map is a Dex progress step. The per-class items are now consequences. The theorem says nothing about internal livelock of the engine. Section 11.5; Appendix A, items 59 and 67. |
| F31 | "Every engine rule requires `up`" contradicted [RECOVER], which requires `¬up`. | **Fixed.** [RECOVER] is exempt. Section 11.2. |
| F32 | [ENGINE-REJECT] lacked the premise that the request is targeted; StartFlow is always proposed (`client_operations/mod.rs:72-86`). A "committed word" was left in Section 11.5. | **Fixed.** Premise added, with citations; the leftover now says outcome word. Sections 11.3 and 11.5. |
| F33 | Retries were defined through the rule [START-RETRY] having fired. | **Fixed.** A retry is a StartFlow request with the request ID of the word's first StartFlow and `ignore_already_started_error`. `w↓` removes those requests; HP orders their commands before the original run's RunPurge and holds trivially without one. Under HP each retry takes [START-RETRY]; without it, a retry after the purge starts a new run (`flow_start.rs:91-124`, `:139-155`; `run_purge.rs:216-227`). Section 11.3; Appendix A, item 66. |
| F34 | The retry definition also matched the word's first StartFlow, which sets `ignore_already_started_error` and has its own request ID, so `w↓` deleted the real start. | **Fixed.** A retry is a StartFlow request at a position after the first one, with the first one's request ID. `w↓`, HP, and Appendix A, item 66, use this definition. Section 11.3. |

## 11. Failures

Sections 1 to 10 describe runs without failures. This section adds failures
for the fragment of decision D1, and states the theorem that failures are
transparent: they change no observable event except that external effects
may repeat. Every citation in this section is at commit `55721e878`. A path
that starts with a crate name, such as `dexzd-apply/`, is under
`server-zd/crates/`. A bare file name repeats the last full path given for
that file. The decisions D1 to D8
are the owner's, of 2026-11-10, for issue
[#1514](https://github.com/superdurable/dex/issues/1514).

The model is called **DexF** below. It is the system of Sections 3 and 4
plus the state of Section 11.2 and the rules of Section 11.3. The
failure-free system of Sections 3 and 4 is called **Dex**.

### 11.1 Scope and decisions

**D1, the fragment.** [owner] The theorem covers the fragment of Dex that the
Lean model of revision 8 covers: one Flow in one partition, the environment
`E_adm` of Section 5.3 (extended by client retries in Section 11.3), and no
RPCs, SubFlows, StopFlow, SetAttributes, DeleteChannelMessage, SkipTimer,
CancelSteps, GracefulComplete, or Attribute locks (`formal/lean/README.md`,
"What the Dex model leaves out"). The rules below are written for the
general Step method, with lock effects where the code has them. Notes say
where a lock effect matters outside the fragment. RPCs and SubFlows under
failures are future work (Section 9).

**D2, durability.** [owner] Only SYNC Step methods are covered. ASYNC is
excluded.

- [code] A SYNC attempt reaches its Worker only after its start commits
  (`dexzd-engine/src/effects/dispatch/method_attempt.rs:184-192`). An ASYNC
  attempt is sent at once (`method_attempt.rs:193-196`).
- [code] So under ASYNC a crash can lose a start that a Worker already
  received. The same attempt number then runs again and consumes no retry
  (`docs/reference/server/high-availability.mdx:41-43`). Its new start takes
  a new snapshot (`dexzd-apply/src/steps/step_results.rs:188-195`), so its
  effect list may differ. The argument of Section 11.4 then fails. This is
  the real ASYNC behavior, not a gap in the proof.
- [assumption] Every invocation's pinned durability is SYNC. The default is
  SYNC when neither the Step options nor the Flow config set it
  (`dexzd-apply/src/steps/invocations.rs:93-97`, `invocations.rs:184-206`).

**D3, zombie Workers.** [owner] A Worker attempt can keep running after the
server has given it up. Such an attempt is a **zombie**. It may emit
external effects at any later time, also after a newer attempt of the same
method started.

- [code] A server crash does not reach the Worker process.
- [code] On a method or heartbeat timeout the engine cancels the dispatch
  session and fails the attempt
  (`method_attempt.rs:657-666`). Cancellation is cooperative (Section 4.4,
  [WORKER-ABANDON]).
- [code] A process that lost its lease keeps carrying the frames of tasks
  that Workers already run until it stops
  (`docs/reference/server/high-availability.mdx:147-154`).

**D4, features that are not transparent.** [owner] Four features change
observable behavior beyond repeated effects. Each stays in the model, as a
rule or as part of the syntax. The theorem assumes each one away as an
explicit hypothesis:

- **H1, no exhaustion.** A run contains no [ATTEMPT-EXHAUSTED] transition
  (Section 11.3). Exhaustion closes the Flow as Failed, which no
  failure-free run can do.
- **H2, no failure policies.** No Step option of the program sets the
  WaitFor policy ProceedOnFailure or the Execute policy
  ProceedToConfiguredStep. Both change control flow and the input of a later
  method (`step_results.rs:377-456`).
- **H3, user code is insensitive to attempts.** The Step method functions `U`
  receive a context `Ctx` that includes the attempt-varying fields
  `ctxA = ⟨attempt, firstAttemptTimestamp, recoveryError,
  lastHeartbeatValue⟩` (`dexzd-engine/src/effects/method_request.rs:464-479`).
  H3 says that `U`'s effects and result do not depend on `ctxA`. Hard-delete
  Attribute values are also re-read on each attempt
  (`method_attempt.rs:591-613`); Section 1.3 already excludes hard-delete
  Attributes.
- **H4, no non-retryable failures.** No attempt failure is non-retryable. A
  non-retryable failure exhausts at once
  (`dexzd-apply/src/steps/retry_backoff.rs:78-80`). Section 2.5 already
  requires well-formed results, and Section 1.3 requires that limits are
  never hit; together they rule out the invalid-response and size failures.

**D5, finitely many failures.** No system makes progress under infinitely
many crashes. So the liveness statement assumes that a run has finitely many
failure transitions (Section 11.5). This is a necessary assumption, not a
choice.

**D6, wall clock.** [owner] Assumption: **on one node, the wall clock does
not step backward between the timer service's due check and the stamping of
the TimersFired command that it proposes.** Why this suffices:

- The timer service reads the wall clock `w₁` and proposes only timers with
  fire time `≤ w₁` (`dexzd-engine/src/effects/timer_service.rs:117`,
  `timer_service.rs:131`, `timer_service.rs:147`).
- The committer stamps the command with `max(w₂, watermark + 1) ≥ w₂`, where
  `w₂` is the wall clock at stamping
  (`dexzd-engine/src/commit/command_committer.rs:141-144`).
- If `w₂ ≥ w₁`, the stamp is at least the fire time. The model's time is the
  stamp, so the premise `Tm(i) ≤ t_{p(f)}` of [TIMER-FIRE] holds.
- Both reads happen on one node. So the assumption needs no clock agreement
  between hosts. After a takeover to another host, stamps still strictly
  increase, because the new owner seeds its watermark from the largest
  applied timestamp (`dexzd-engine/src/engine_node/node_start.rs:408`,
  `command_committer.rs:89-94`).
- Firing early in physical time, when a host's clock runs ahead of real time,
  is outside the model. The model speaks only of stamps.
- Apply does not check that a timer is due (Section 4.5). The owner decided
  not to change apply and not to open an issue for it.

Late firing needs no assumption: a down period is one large [TICK], and
Section 4.5 already allows unbounded lateness.

**D7, network failures.** [owner]

- **The input word under failures.** It is the sequence of client
  requests, in the order of their final outcomes. A final outcome is one of
  three: an accepted commit, a rejection by apply, or a rejection by the
  engine before any proposal ([ENGINE-REJECT], Section 11.3). This
  definition is used everywhere below, under the name **outcome word**. A
  request lost before any outcome is invisible to the theorem.
- PublishToChannel has no request ID (`protos/dex.proto:650-654`). A client
  that sends it again after a lost response has sent a second request: a
  second input of the word, with its own `pub` event. This is outside
  failure transparency. The server is not changed.
- StartFlow retries are covered: a retry that carries the start's request ID
  and `ignore_already_started_error` writes no run state and produces no
  event. It is still a log entry that advances the partition clock
  ([START-RETRY], Section 11.3).
- Duplicate Worker results are covered by the fence ([RESULT-STALE]).
- An application that needs exactly-once publication can publish from an RPC
  handler, because a repeated RPC request ID with the same content returns
  the recorded reply (`dexzd-apply/src/command_handlers/rpc_committed.rs:58-75`).
  [owner, 2026-11-10] This route is **planned, not yet available**. The
  caller must reuse one request ID, and today the SDKs do not let it pass
  one: the Go SDK's InvokeRPC mints a new request ID on every call
  (`sdk-go/dex/client.go:1765`, `sdk-go/dex/options.go:553-561`). The owner
  decided that the SDKs will get an optional caller-supplied request ID
  ([#1519](https://github.com/superdurable/dex/issues/1519)). Until it ships,
  no SDK exposes one. Two more cautions apply, outside this theorem. A crash
  before the RPC commits lets the handler run again. An RPC on a locked key
  gets ABORTED.
- [owner, 2026-11-10] PublishToChannel will not get a request ID. This
  confirms the decision above.
- [owner, 2026-11-10] The SDKs will accept an optional run ID for StopFlow
  ([#1520](https://github.com/superdurable/dex/issues/1520)), so that a
  retry can target the run it meant to stop (Open question 25). This is a
  note only: StopFlow is outside `E_adm`.
- The per-run request table is bounded and evicts its oldest entries
  (Section 1.3). The fragment does not use it: StartFlow deduplicates
  through the Flow directory, and PublishToChannel has no request ID. So the
  theorem needs no hypothesis about eviction. The RPC extension will need
  one.

**D8, takeover.** [owner] A standby takeover is in scope. The model treats
it as [CRASH], then [RECOVER], with an overlap window of the old owner
(Section 11.3, "Takeover"). Multi-node partition ownership is excluded,
because it does not exist: "a cluster runs one node"
(`docs/reference/server/configuration.mdx:60`). The theorem describes one
partition log with one owner at a time. A crash or a takeover may replace
the owner.

### 11.2 The state extension

DexF adds four pieces of state to `Σ` of Section 3.4.

```text
Inv   ::= ⟨ m, ip : P | R | B, a : ℕ⁺, ps, keys, snap : Snap?, tid : TaskId? ⟩
                                       -- B is Backoff; a is the attempt number
Task  ::= ⟨ f, e, m, a, tid, input, snap, locals, cr, ctxA,
            phase : Started | Running(Fx*, Resp) | Returned(Resp),
            j : ℕ ⟩                    -- j: effects emitted so far (ghost)
Σ_F   ::= ⟨ τc, P, R, η, N, Z, I, h, v, ps₀, n, ζ, Rc, par, up ⟩
up : Bool                              -- the server is serving
Z : TaskId ⇀ Task                      -- zombie tasks: Workers the server gave up
I : set of (e, m, a, tid)              -- interrupted attempts, engine memory
h : (e, m) ⇀ ℕ                         -- ghost: largest effect index any attempt emitted
v : (e, m) ⇀ NotRun | Ran(Fx*, Resp) | Returned(Resp)
                                       -- ghost: furthest phase any attempt reached
ps₀ : (e, m) ⇀ ℕ                       -- ghost: ζ(s), s the run seq of the invocation's first pend
n : ℕ                                  -- ghost: number of applied normal commands of the run
ζ : ℕ ⇀ ℕ                              -- ghost: real seq of each applied normal command ↦ its index
Rc : FlowId ⇀ Run                      -- ghost: a run as RunPurge found it
par : (e, m) ⇀ ⟨input, snap, locals, cr⟩  -- ghost: the arguments of U at the first start
```

- `up` is whether the server serves. Every commit rule, every engine rule
  except [RECOVER], and [ENGINE-REJECT] require `up`. [RECOVER] requires
  `¬up`. Worker rules ([WORKER-EFFECT],
  [ZOMBIE-EFFECT], [WORKER-ABANDON]) and [TICK] do not: Workers and clocks
  run outside the server. [CRASH] sets `up := false` and [RECOVER] sets
  `up := true`. The code has the same split: the registry refuses work once
  halted (`dexzd-engine/src/flow_coordination/registry.rs:774-778`), and a
  starting node accepts commands only after `start_serving`, before it
  rebuilds (`dexzd-engine/src/engine_node/node_start.rs:450-455`,
  `node_start.rs:459-465`). A crash may leave every other component
  unchanged, for example on a quiescent Flow with `Θ`, `Z`, and `I` empty;
  `up` is what disables commits then and enables [RECOVER].
- `Inv` extends Section 3.1. `a` is the method's attempt number. In Backoff
  it is already the next attempt's number. `tid` is the task ID of the
  running attempt
  (`server-zd/proto/dexzd/v1/state.proto:419-458`).
- `snap` may be present while `ip = P` or `ip = B`. That is the **retained
  snapshot** of a retry. Without failures `snap` is present exactly when
  `ip = R`.
- `Tm` (Section 3.2) also holds **retry timers** `retry(e, m, a)`, armed when
  attempt `a − 1` fails (`step_results.rs:741-756`).
- `h`, `v`, `ps₀`, `n`, `ζ`, `Rc`, and `par` are history variables. No rule reads them;
  only α does. Under H2 each pair `(e, m)` has at most one invocation in its
  life, so they need no reset.
- **Command classes.** Every command in the log is a log commit with a
  stamp (Section 11.3). Apply's outcome puts it in one of six classes. The
  criterion for the run `seq` is whether the command **advances the
  persisted `next_run_seq`**, not whether it writes the run record. A cursor
  opened with `RunCursor::open` bumps `next_run_seq` in memory
  (`dexzd-apply/src/command_handlers/run_cursor.rs:153-156`); the bump
  persists only if the command then writes the run record from that cursor.
  A cursor opened with `open_without_seq` keeps `next_run_seq`
  (`run_cursor.rs:91-99`), even when the command writes the run record. For
  example, HistorySegmentUploaded opens its cursor that way
  (`dexzd-apply/src/history_offload/segment_upload.rs:46`), changes the
  record (`:87`), and writes it through `reconcile_run_storage`
  (`:104`, `dexzd-apply/src/values/storage_accounting.rs:30`), yet takes no
  run `seq`.

  | Class | Run `seq` | α | Commands |
  | --- | --- | --- | --- |
  | **Normal** | taken | a Dex commit with the same events | FlowStart, ChannelPublish, a first [STEP-START], a Complete rule, a TimersFired that fires a wait timer |
  | **Failure** | taken | stutter | a [STEP-START] that reuses a snapshot, [ATTEMPT-LOST], [RECOVER-INTERRUPT], [ATTEMPT-RETURNED], [ATTEMPT-HEARTBEAT], a TimersFired that fires only retry timers or stale timers |
  | **Bookkeeping** | not taken | stutter | applied, but does not advance `next_run_seq`: see below |
  | **Rejected client input** | not taken | the Dex rejection of the same request, with the same event `rejected(input, err)` | a request of the word that apply rejects with an error, such as a PublishToChannel after the root Flow closed; also [ENGINE-REJECT] (Section 11.3), which is not a log command at all |
  | **Internal ignored or rejected** | not taken | stutter | [RESULT-STALE], [START-RETRY], a TimersFired whose timers are all gone (`dexzd-apply/src/command_handlers/step_commands.rs:194-196`), a stale StepAttemptQueued |
  | **Post-close housekeeping** | taken by the retention TimersFired; not by RunPurge | stutter, with `Rc` (see below) | the retention TimersFired, RunPurge |

  **Rejected client inputs are observable.** A PublishToChannel to a closed
  run is rejected with NOT_FOUND
  (`dexzd-apply/src/command_handlers/client_writes.rs:213-214`), and a
  rejection writes nothing (`command_handlers/mod.rs:292-294`). Sections 4.6
  and 5.3 give it the event `rejected(pub(f, [(c, v)]), NOT_FOUND)`, which
  `π` maps to `refused(c, v)`. So it is not a stutter: α maps it to the same
  Dex transition, with `n` and `ζ` unchanged on both sides.

  **[START-RETRY] stays silent.** It differs from a rejected Publish in two
  ways. Its reply is a success that returns the existing run
  (`flow_start.rs:116-124`), not an error, so it is not a rejection in the
  sense of Section 4.6. And `w↓` deletes it (Section 11.5), so the Dex word
  has no request for it to match. A rejected Publish stays in `w↓`.

  **Bookkeeping commands** do not advance `next_run_seq`. They were found by
  searching `dexzd-apply` at `55721e878` for handlers that build their
  output without `RunCursor::finish`; each was then checked for how it opens
  its cursor:
  - StepAttemptQueued (`dexzd-apply/src/steps/dispatch_queue.rs:106-164`).
    It opens a cursor (`:111`) but never writes the run record; it writes
    only a DispatchQueue index row (`:150-157`). So after the start
    `next_run_seq` stays 2. The fragment's
    engine proposes it whenever no Worker takes a pending attempt at once
    (`attempt_handout.rs:362`, `attempt_handout.rs:381-392`). It is in the
    fragment, and it is a stutter: dispatch order is nondeterministic in the
    model (F3).
  - SetSearchProjectionMode and SearchOutboxAck
    (`dexzd-apply/src/projections/search_outbox.rs:155`), SetHistoryOffloadMode
    (`dexzd-apply/src/history_offload/offload_mode.rs:101`),
    HistorySegmentUploaded (`dexzd-apply/src/history_offload/segment_upload.rs:105`),
    HistorySegmentObjectsDeleted (`dexzd-apply/src/history_offload/segment_expiry.rs:230`),
    TimeTravelStage (`dexzd-apply/src/history/time_travel/staging.rs:108`),
    ValueRevive (`dexzd-apply/src/history_offload/value_revive.rs:84`),
    and HistoryLocalCleanup (`dexzd-apply/src/history/local_cleanup.rs:112`).
    The search and offload-mode commands are partition commands with no run
    cursor. The history, time-travel, and revive commands open their cursor
    with `open_without_seq` (`segment_upload.rs:46`,
    `dexzd-apply/src/history_offload/segment_expiry.rs:184`,
    `dexzd-apply/src/history/time_travel/staging.rs:55`,
    `dexzd-apply/src/history_offload/value_revive.rs:44`,
    `dexzd-apply/src/history/local_cleanup.rs:92`); some of them still write
    the run record. They serve search, history, and time travel, which
    Section 1.3 excludes. Background work may still propose them during a run
    of the fragment; each is a stutter.
  - RunPurge is not here: it is post-close housekeeping (below).

  **Post-close housekeeping.** In the fragment the engine proposes two run
  commands only after the root Flow closed. A search of the effects that a
  close emits found no others: the only other close effect,
  `PendingCloseHeldByRpcs` (`dexzd-apply/src/command_handlers/flow_close.rs:304`),
  needs a pending close, which `E_adm` never sets (Section 4.9).
  - **The retention TimersFired.** Every close arms a retention timer
    (`flow_close.rs:400-424`). The timer service proposes it like any timer
    (`dexzd-engine/src/effects/timer_service.rs:253-287`). Apply skips the
    run-attempt check for it, marks the run purging, and asks for a purge
    (`step_commands.rs:156-175`, `dexzd-apply/src/cross_flow/run_purge.rs:73-98`),
    or postpones the timer while outbox entries or sealed history segments
    remain (`run_purge.rs:79-84`, `run_purge.rs:237-252`). It finishes its
    cursor, so it **takes a run `seq`** (`step_commands.rs:201-206`). It can
    share a command only with retry timers left after the close, because the
    close disarmed every wait timer (Section 4.2, `finish`).
  - **RunPurge.** The engine proposes it after it deleted the run's objects
    (`dexzd-engine/src/effects/object_deletions.rs:110-136`). It removes the
    run from the Flow directory and deletes every run row
    (`run_purge.rs:216-228`). It takes no run `seq`: the run is gone.

  Both map to stutters. That is sound only because nothing in the fragment
  reads the closed run afterwards, except two inputs. A publication is
  rejected with the same event before and after the purge (Section 1.3,
  "Retention and run purge after close"). A StartFlow retry is deduplicated
  only before RunPurge commits, and hypothesis HP (Section 11.3,
  [START-RETRY]) excludes a later one. To keep α a function of the
  configuration, `Σ_F` keeps a ghost copy `Rc(f)` of the run as RunPurge
  found it, and α uses `Rc(f)` for `R(f)` after the purge.

  The routing of these commands is at
  `dexzd-apply/src/command_handlers/mod.rs:116-170`. Step commands and
  TimersFired finish their cursor
  (`dexzd-apply/src/command_handlers/step_commands.rs:80-89`,
  `step_commands.rs:201-206`).
- Every applied normal command with run `seq` `s` sets `n := n + 1` and
  `ζ[s] := n`. Every command of the other five classes leaves `n` and `ζ`
  unchanged.
- `ps₀(e, m) := ζ(s)`, where `s` is the run `seq` of the command whose
  `pend` first made `(e, m)` pending. That command is always normal: a first
  pend happens only when a Step is created, when a wait fires, or when a
  WaitFor completes, and the failure path that pends a new method is
  excluded by H2. `ps₀` is set in the same command that sets `ζ[s]`, and
  retries never change it. The code's `pending_since_seq` holds the raw `s`
  (`invocations.rs:114`). Example: `seq` 1 is the start, 2 the first WaitFor
  start, 3 a failure, 4 the retry timer, 5 the start that reuses the
  snapshot, 6 the WaitFor completion that registers an empty wait and pends
  Execute. Execute's first pend has raw `seq` 6, but its command is the
  third normal command, so `ps₀ = ζ(6) = 3`. So `ζ` is strictly increasing on its domain. It is part of the
  configuration, because the configuration alone cannot tell which past
  commands were normal: two runs can reach the same `R`, `Θ`, `Z`, `I`, `h`,
  `v`, `ps₀`, and clocks, one through a normal [STEP-START] and one through
  an [ATTEMPT-HEARTBEAT] at the same `seq`, and need different relabelings
  afterwards (`step_results.rs:211-255`, `invocations.rs:274-280`,
  `step_decisions.rs:373-401`).
- `v(e, m)` is monotone. It starts at `NotRun` when the invocation is first
  pended. It becomes `Ran(fx⃗, resp)` when some attempt first runs the
  handler, and `Returned(resp)` when some attempt first returns. It never
  moves back, even when the attempt that advanced it fails, is dropped, or
  its result is ignored. It is well defined because every attempt computes
  the same `fx⃗` and `resp` (Section 11.4, H3). Together with `h`, it is the
  state of the virtual task of Section 11.6, and it depends on no live
  task.

**What survives a crash.** The durable point of a commit is its log batch in
object storage, written with a create-only condition
(`dexzd-log/src/commit_pipeline.rs:523-578`). Recovery replays the same
stamped commands through the same applier as live commits
(`dexzd-engine/src/commit/partition_applier.rs:624-639`). Apply takes its
time from the command stamp and is not in replay mode
(`dexzd-apply/src/command_handlers/command_scope.rs:45-56`); replay mode is
for time travel only (`command_scope.rs:24-25`). So recovery is the same
function on the same command stream. It is not a special apply mode.

| State | After [CRASH] |
| --- | --- |
| Apply state `R`, including `Inv`, locks `L`, timers `Tm`, request tables, and the Flow directory | Kept (in the log; the local store is rebuilt from it) |
| Clocks `τc` | Kept: stamps keep increasing (D6) |
| Zombie tasks `Z` | Kept: Workers run outside the server |
| Tasks `Θ` | Lost; every task moves to `Z` |
| RPC state `RL`, `Rq`, `Tx` | Lost (outside the fragment) |
| Timer service memory, its retry delays (`timer_service.rs:123-127`, `timer_service.rs:176-184`) | Lost |
| Dispatcher memory, method and heartbeat deadlines (`dexzd-engine/src/effects/dispatch/attempt_deadlines.rs:34-54`) | Lost |
| Interrupted set `I` | Rebuilt by [RECOVER] |

### 11.3 The failure rules

Rules are in the style of Section 4. Two notions are kept apart below. A
**log commit** is a command durably in the partition log; it carries a
stamp, and the stamp advances the partition clock, whether apply accepts the
command or not (`partition_applier.rs:624-639`). A command takes the next
run `seq` only when it advances the persisted `next_run_seq` (the command
classes of Section 11.2). Not every applied command does: StepAttemptQueued
is applied and takes none (`dexzd-apply/src/steps/dispatch_queue.rs:150-163`).
A command that apply ignores or rejects writes no run row and takes no run
`seq`. In Dex, a log commit that changes no model state is covered by
[TICK]; a rejected client input keeps its event (Section 11.2).

**Unchanged rules.** [FLOW-START], [TICK], [PUBLISH], [WORKER-ABANDON], the
meta-functions of Section 4.2, and the RPC and SubFlow rules keep their
premises, effects, and events. [STEP-START], [WORKER-EFFECT], the Complete
rules, and [TIMER-FIRE] are **extended**: each extension is a no-op on the
states that failure-free runs reach.

**[STEP-START], extended** (commit, `StepAttemptStarted`). Events `τ`.

- Premise as in Section 4.4, with `ip = P` and any attempt number `a`.
- Effect: `snap := S[e].inv.snap` when it is present, else
  `snapshot(e, m)` (`step_results.rs:188-195`). Then `ip := R`, and `tid` is
  a fresh task ID that the engine minted
  (`dexzd-engine/src/effects/dispatch/attempt_handout.rs:230-235`,
  `dexzd-apply/src/steps/invocations.rs:249-296`).
- `Θ[tid] := ⟨f, e, m, a, tid, S[e].in, snap, S[e].loc, S[e].cr, ctxA,
  Started, 0⟩`.
- Without failures `snap` is never present at Start, so this is the rule of
  Section 4.4.

**[WORKER-EFFECT], extended** (Worker) Events `[fx(f, (e, m), x)ʲ]`. As in
Section 4.4, for a task in `Θ`. The `j`-th effect of a task carries the ghost
index `j`. The task's counter becomes `j`, and `h(e, m) := max(h(e, m), j)`.
When a task computes `(fx⃗, resp)`, `v(e, m)` becomes `Ran(fx⃗, resp)` unless
it is already further. When a task becomes `Returned(resp)`, `v(e, m)`
becomes `Returned(resp)`. The invocation's first `pend` sets
`ps₀(e, m) := ζ(s)` once (Section 11.2). The first [STEP-START] that computes
a snapshot sets `par(e, m) := ⟨S[e].in, snap, S[e].loc, S[e].cr⟩` once. Every
later attempt runs `U` on the same arguments up to `ctxA` (Section 11.4), so
`par` stays valid after a cancel releases the snapshot.

**[ZOMBIE-EFFECT]** (Worker) Events `[fx(f, (e, m), x)ʲ]`. The same rule for a
task in `Z`. A zombie may also be dropped at any point, as [WORKER-ABANDON]
drops a canceled task.

**Complete rules, extended** ([STEP-COMPLETE-WAITFOR] and
[STEP-COMPLETE-EXECUTE], commit, `StepMethodCompleted`). The task may be in
`Θ` or in `Z`. The premise is the fence of [RESULT-STALE]. The effect is that
of Section 4.4. The task leaves `Θ` or `Z`. A zombie can pass the fence only
while its invocation is still running with its task ID: after [CRASH] and
before its [RECOVER-INTERRUPT]. [decision] The model allows this order. The
code suggests that it cannot happen, because the old owner's commits all
precede the new owner's recovery (Takeover, below). Allowing it is a safe
over-approximation: the commit is an ordinary completion.

**[RESULT-STALE]** (log commit of a fenced command) Events `τ`. The fence
depends on the command kind:

- **Started-attempt commands.** StepMethodCompleted, StepAttemptReturned,
  StepAttemptHeartbeat, and a StepAttemptFailed without `is_before_start`
  apply only when the Step execution exists, its current invocation has the
  command's method and attempt number, its phase is Running, and its task ID
  equals the command's task ID (`step_results.rs:83-128`,
  `step_results.rs:147-153`, `step_results.rs:857-872`).
- **Before-start failures.** A StepAttemptFailed with `is_before_start`
  carries no task ID. It applies only when the current invocation has the
  command's method and attempt number and its phase is Pending
  (`step_results.rs:130-146`). Apply ignores a before-start failure that
  carries a task ID (`step_results.rs:264-269`).

Otherwise apply ignores the command. The outcome is `Ignored`: the command is
a log entry, but it writes no run row and takes no run `seq`
(`step_results.rs:49-54`). A returned Worker task whose command is ignored
leaves `Θ` or `Z`. This rule subsumes [STEP-COMPLETE-STALE].

- For a started-attempt command, the task ID check is skipped when the
  command's task ID is empty (`step_results.rs:105-128`). Only logs of older
  binaries carry an empty task ID on such a command. The engine mints every
  task ID (`attempt_handout.rs:230-235`) and panics before it proposes an
  empty one (`method_attempt.rs:970-979`). A Worker never supplies the
  fence: the engine fills it from its own memory, and a Worker frame reaches
  an attempt only through the stream's current assignment
  (`dexzd-worker-dispatch/src/worker_registry/worker_protocol.rs:236-241`).
  A StepAttemptReturned without a task ID never applies
  (`step_results.rs:481-486`). [assumption] Every logged started-attempt
  command carries a non-empty task ID.
- The empty task ID of a before-start failure is by design, not a
  compatibility path. The engine proposes one only when matching refuses a
  pending attempt as invalid, inside the slot reservation
  (`attempt_handout.rs:363-364`, `attempt_handout.rs:610-629`). The
  reservation comes before the task ID is minted and before the start is
  proposed (`attempt_handout.rs:222-235`, `attempt_handout.rs:257`). So no
  task frame was sent, no Worker ran the attempt, and no effect was emitted.
  What decides the snapshot is whether the invocation has ever started, not
  the attempt number. The fence checks only the phase Pending, the method,
  and the attempt number (`step_results.rs:138-145`). An invocation that has
  never started has no snapshot yet. One that started before keeps its
  retained snapshot, unchanged, also when it is Pending with attempt 1
  after a [ATTEMPT-RETURNED] (`step_results.rs:509-530`). The failure moves the invocation to Backoff
  ([ATTEMPT-LOST]); under α it is a stutter (Section 11.6).
- Duplicate proposals are stale by this rule. The engine proposes a Step
  command again only after an `Unavailable` error
  (`dexzd-engine/src/effects/mod.rs:427-456`). The first commit ends the
  Running phase, so a second copy is ignored.

**[ATTEMPT-LOST]** (commit, `StepAttemptFailed`) Events `τ`. Nondeterministic.
Premise: `S[e].inv = ⟨m, ip, a, …⟩` with `ip = R` and task ID `t`, or
`ip = P` (a before-start failure, with the fence of [RESULT-STALE] for that
kind). The retry decision is
RetryAt (`dexzd-apply/src/steps/retry_backoff.rs:70-121`; its other outcome
is [ATTEMPT-EXHAUSTED]).

```text
for k in keys:  if L[k].h = (e,m) then keep it            -- locks retained
                L[k].ws := L[k].ws without (e,m)
snap kept, pinned for attempt a+1                          -- invocations.rs:344-373
ip := B;  a := a + 1;  tid := ⊥
Tm[retry(e, m, a)] := now + backoff                         -- step_results.rs:349-360
if t ∈ dom(Θ): Z[t] := Θ[t];  delete Θ[t]                   -- the Worker may continue
```

Code: `step_results.rs:258-360`, `invocations.rs:314-390`.

- **Sources.** The rule stands for every way the engine fails an attempt: a
  Worker error (`attempt_deadlines.rs:191-211`), a method timeout (default 2
  hours) or a heartbeat timeout (default 60 seconds)
  (`attempt_deadlines.rs:22-23`, `method_attempt.rs:657-666`), a Worker
  disconnect (`attempt_deadlines.rs:174-180`), a declined reserved slot
  (`attempt_deadlines.rs:159-165`), a server-side error
  (`method_attempt.rs:129-154`), a result that arrives after the method
  deadline (`method_attempt.rs:842-848`), and a task that matching refuses
  before any start (`attempt_handout.rs:363-364`, `attempt_handout.rs:610-629`).
- **Over-approximation.** The model does not describe detection. It lets the
  attempt fail at any time. [decision] This is sound for the safety
  theorem: the theorem's content is the inclusion of DexF traces in Dex
  traces plus repetitions, and more DexF runs only make that inclusion
  harder to prove. Liveness needs only that failures are finitely many (D5).
- **Heartbeats.** No SDK heartbeats automatically. The Go, Python, and Rust
  SDKs send one only from an explicit call (`sdk-go/dex/invocation.go:176-188`,
  `sdk-python/dex/_invocation_context.py:176-191`,
  `sdk-python/dex/step.py:60-76`, `sdk-rust/crates/dex-sdk/src/context.rs:308-340`).
  The Java SDK documents that "Dex sends no automatic heartbeat"
  (`sdk-java/src/main/java/io/superdurable/dex/StepOptions.java:203`), and the
  TypeScript SDK asks for explicit heartbeats
  (`sdk-typescript/src/step.ts:147`). Any
  output frame, heartbeat or Stream write, resets the heartbeat deadline
  (`method_attempt.rs:692-693`). So a handler that runs longer than the
  heartbeat timeout without either loses its attempt, with no crash at all.
- **Before the first start.** A before-start failure of an invocation that
  has never started leaves no snapshot and no task: no effect was emitted. It removes the invocation
  from its lock queues, and [RETRY-FIRE] queues it again at the tail
  (`invocations.rs:339-340`, `invocations.rs:70-77`). No failure-free run
  reorders a lock queue. The fragment has no Attribute locks, so the proof
  is unaffected. Open point for a fragment with locks (Section 11.6).

**[ATTEMPT-EXHAUSTED]** (commit, `StepAttemptFailed`) Events
`[closed(f, Failed, (type, text), outs)]`. Premise as [ATTEMPT-LOST], with the
retry decision Exhausted: the failure is non-retryable, the attempt number
reached the policy's maximum, or the next retry would pass the total
duration (default 4 hours) (`retry_backoff.rs:78-86`, `retry_backoff.rs:115-119`,
`retry_backoff.rs:15`). Without a failure policy the Step becomes FAILED and
the Flow closes as Failed with WORKER_API_FAIL or INTERNAL
(`step_results.rs:457-468`, `step_results.rs:550-584`). With a policy (H2) the
Step proceeds instead (`step_results.rs:377-456`). H1 excludes this rule from
the theorem's runs.

**[ATTEMPT-RETURNED]** (commit, `StepAttemptReturned`) Events `τ`. Premise:
`ip = R` with task ID `t`, and the fence of [RESULT-STALE]. Effect: `ip := P`,
`tid := ⊥`. The attempt number, snapshot, locks, and queue position stay
(`step_results.rs:474-531`). No retry is consumed. If `t ∈ dom(Θ)`, it moves
to `Z`.

- Sources: the Worker rejects the task as an unknown Step or for capacity,
  or the frame was not delivered (`attempt_deadlines.rs:151-173`,
  `method_attempt.rs:257-262`, `method_attempt.rs:331-370`).
- The protocol says the Worker never ran the attempt. A Worker that rejects
  after it sent output breaks the protocol
  (`worker_protocol.rs:294-300`), yet its rejection still reaches the
  attempt. [decision] The rule does not assume that the task emitted no
  effect. A task that did is a zombie like any other.

**[ATTEMPT-HEARTBEAT]** (commit, `StepAttemptHeartbeat`) Events `τ`. Premise:
the fence of [RESULT-STALE]. Effect: store the checkpoint value on the
invocation (`step_results.rs:211-256`). The engine proposes heartbeats in
batches, and may lose one (`method_attempt.rs:713-735`). Heartbeats also
happen in failure-free runs whenever user code records one. Sections 1 to 10
leave them out (Section 1.3). They change only `lastHeartbeatValue`, which
H3 makes invisible to `U`.

**[RETRY-FIRE]**, part of [TIMER-FIRE], extended (commit, `TimersFired`).
Events `τ`, or `[closed(...)]` as in Section 4.5.

- The command names `D`, a non-empty prefix of the run's due timers in index
  order `(fire time, encoded TimerId)`, as in Section 4.5. Wait timers and
  retry timers are interleaved in that order
  (`timer_service.rs:140-156`). Each timer of `D`, in order, is disarmed if
  armed and then handled (`dexzd-apply/src/command_handlers/step_commands.rs:147-207`):
  - `wait(e, j)`: as in Section 4.5.
  - `retry(e, m, a)`: if `S[e].inv = ⟨m, B, a, …⟩`, then `pend` again:
    `ip := P`, `ps := seq`, and queue on each lock key it does not hold. The
    retained snapshot stays (`step_results.rs:815-855`,
    `invocations.rs:57-138`, `invocations.rs:123-125`). Otherwise nothing.
- **A stale retry timer still commits.** A cancel or a Flow close does not
  disarm a retry timer (Open question 25). When it fires, `disarm` finds the
  row, so the command applies, takes a `seq`, and records history, while the
  handler does nothing (`step_commands.rs:161-170`,
  `step_commands.rs:194-206`, `step_results.rs:831-838`). In the fragment
  the only cancel is the Flow close, so a stale retry timer exists only
  after the close. Its commit maps to a stutter, so it is not a progress
  step (Section 11.5).
- **Failures add nothing to the timer-order gap.** A row enters the timer
  service's retry delay only after its TimersFired command committed: the
  proposal returns only after the commit, or fails when the node stops
  (`timer_service.rs:165-184`, `timer_service.rs:282-286`). A crash empties
  the retry delays, which only removes skips.
- **The rule the code implements.** The prefix is taken at scan time, not
  at apply time (Open question 26). Precisely:

  ```text
  [TIMER-FIRE-SCAN]  The command names D. There is an earlier applied state ρ'
  of this run on the same path, and a wall clock w ≤ t_{p(f)} of the scan,
  such that D is a non-empty prefix, in index order, of
  { i ∈ dom(Tm in ρ') | Tm(i) ≤ w }.
  Apply, in order: for i in D: if i ∉ Tm: skip; else handle i as above.
  ```

  Between `ρ'` and the apply, other commands of the run may apply. They may
  disarm timers of `D`, which are then skipped, and they may arm timers due
  before members of `D`, which are then not fired by this command.
- **OD1, which timer rule the failure proof uses. Decided [owner,
  2026-11-10]: option A.** Both theorems use the prefix rule [TIMER-FIRE] of
  Section 4.5 as an explicit modeling idealization. [TIMER-FIRE-SCAN] stays
  documented as the code's behavior and is used by neither theorem. The
  options as they were put:
  - **Option A, keep the prefix rule of Section 4.5 on both sides.** The
    failure theorem then composes with the expressiveness theorem as it
    stands. Cost: like that theorem, it says nothing about real runs in the
    window of Open question 26.
  - **Option B, use [TIMER-FIRE-SCAN] on both sides.** The failure theorem
    then covers those runs. Cost: the Dex side is no longer the model of
    revision 8. The end-to-end corollary needs the expressiveness theorem
    redone for [TIMER-FIRE-SCAN], and L's `fire` order (least due timer
    first, Section 8.1) would no longer match. Under failures nothing else
    changes: the mapping below works for either rule, because it compares
    DexF with a Dex that uses the same rule.
  - "The timer rule" in Section 11 therefore means [TIMER-FIRE].
- **Mapping.** The wait timers of `D` form a prefix of the current due wait
  timers, because `D` is a prefix of all due timers and one index order
  covers both kinds. Section 11.6 maps the command to [TIMER-FIRE] of that
  sub-list, or to a stutter when it is empty. A retry handler
  touches neither wait state nor Channels, so its position among the wait
  timers does not change their effects.
- D6 is needed for wait timers only. A retry timer that fires early changes
  timing, which the untimed theorem does not observe.

**[CRASH]** (environment) Events `τ`. Premise: `up`. Effect: `up := false`;
every task of `Θ` moves to `Z`, and the rest of `η` is lost (Section 11.2).
`R`, `Tm`, `τc`, and `Z` stay. No commit happens until [RECOVER].

- A commit is atomic at its log batch: it happened or it did not.
- [assumption] A log write with an unknown outcome is a crash. The pipeline
  fails every open proposal and stops (`commit_pipeline.rs:484-493`), and the
  client path halts the registry (`dexzd-engine/src/flow_coordination/client_operations/mod.rs:326-352`).
  Replay then decides whether the batch was committed.

**[RECOVER]** (engine) Events `τ`. Premise: `¬up`. Effect: `up := true`, and
the partition rebuild runs
(`dexzd-engine/src/effects/partition_rebuild.rs:91-160`,
`node_start.rs:450-467`):

```text
for each invocation of R:
  ip = P:  the dispatcher may start it again (F3)
  ip = B:  nothing; its retry timer is in Tm
  ip = R with task ID t:  I := I ∪ {(e, m, a, t)}
the timer service starts again from the committed timer index
```

- A Running attempt is **not** handed out again: [STEP-START] needs
  `ip = P`. The server cannot know whether the Worker ran it, because a SYNC
  frame is sent only after the start commits. So it counts the attempt as
  failed.

**[RECOVER-INTERRUPT]** (commit, `StepAttemptFailed` of kind
`STARTED_ATTEMPT_INTERRUPTED`) Events `τ`. Premise: `(e, m, a, t) ∈ I`.
Effect: remove it from `I`. If the fence of [RESULT-STALE] holds for `t`, the
effect is that of [ATTEMPT-LOST]: the attempt consumes a retry, keeps its
snapshot and locks, and goes to Backoff. Otherwise the command is ignored.

- Code: `attempt_handout.rs:663-717` (the kind at `:703`, the fence at
  `:707`); `dexzd-engine/src/effects/dispatch/dispatch_scheduler.rs:202-218`.
  The command has no `is_before_start` and no `is_not_run`, so it runs the
  same `apply_attempt_failed` as any failure, and records the progress
  NOT_COMPLETED (`invocations.rs:150-161`).
- **Not atomic.** [RECOVER] reads the state once. Each interrupt is then a
  separate commit, spawned on its own task. Any transition may come between
  [RECOVER] and an interrupt, including a cancel of the Step, a Flow close,
  or a commit of the zombie task's result. The first of these to commit wins
  through the fence, and the interrupt or the result is ignored.
- [code] A running attempt with an empty task ID is logged and not failed
  (`attempt_handout.rs:679-689`). It would then stay Running. The assumption
  of non-empty task IDs excludes it.

**Takeover.** A standby takeover is [CRASH] of the old owner, then [RECOVER]
of the new one. The old owner overlaps the new one, but the committed log
has one writer at a time:

- [code] Each batch is an object at its own `seq`, written create-only
  (`commit_pipeline.rs:523-567`). The new owner writes an EpochStart batch at
  the first free `seq`. Replay voids every batch of an older epoch after an
  EpochStart (`dexzd-engine/src/recovery/log_replay.rs:137-145`,
  `log_replay.rs:182-220`, `dexzd-log/src/stream_tail_reader.rs:22-61`).
- So the model places the old owner's [CRASH] at the EpochStart. Its commits
  before the EpochStart are ordinary commits before [CRASH]. Its commits
  after it are void and never happened. Two owners never write the same
  `seq`, and no old-epoch batch counts after the EpochStart.
- [code] After its lease lapses, the old owner hands Workers no new task. Its
  open PollWorkerTask streams still carry frames of tasks that Workers run,
  until it stops (`docs/reference/server/high-availability.mdx:147-154`).
  These tasks are zombies.
- [code] A result of such a task cannot reach the new owner. A Worker frame
  whose task ID is not the stream's current assignment is dropped
  (`worker_protocol.rs:236-241`), and the new owner never assigned it. If the
  result did reach apply, the fence would drop it.
- [code] A cold standby replays the log like a restart. A hot standby has
  applied the log already and replays only the last batches; it runs no
  Steps or timers, so it has no `Θ`
  (`docs/reference/server/high-availability.mdx:82-85`,
  `high-availability.mdx:107-109`). Both start through the same node start:
  log replay, watermark seeding, and partition rebuild
  (`node_start.rs:385-408`, `node_start.rs:459-465`). So a takeover uses the
  same interrupt kind and the same retry accounting as a restart. The
  documented "NOT_COMPLETED" (`high-availability.mdx:40-41`) is the recorded
  attempt progress, not a different failure kind.

**[START-RETRY]** (client, StartFlow) Events `τ`. A StartFlow request for a
Flow ID that already has a run, with `ignore_already_started_error` set and
the request ID that started the latest run. Under the default reuse policy
the start conflicts. Apply returns the latest run as a successful reply
(`dexzd-apply/src/command_handlers/flow_start.rs:91-125`). This holds also
after the run closed, and any number of times, **but only while the Flow
directory still holds that run**. RunPurge removes the run from the
directory (`run_purge.rs:216-220`). A StartFlow after that finds no run, so
the conflict check passes (`flow_start.rs:91-99`) and the request creates and
starts a new run (`flow_start.rs:139-190`, `flow_start.rs:305-308`). That is
a second start, and later a second close, which `E_adm` does not admit. A
retry while the run is only marked purging still finds the directory entry
and is deduplicated.

- **HP, retries come before the purge.** A predicate on runs: in the
  committed log, the command of every retry (a later StartFlow request of
  the word that carries the request ID of its first StartFlow, defined
  below) is ordered before the RunPurge of the original run. HP holds trivially in a
  run with no RunPurge. It is a condition on the execution, not a property
  of the input word. Arriving at the server first is not
  enough. The registry submits a command to the log before its outcome is
  durable (`dexzd-engine/src/flow_coordination/registry.rs:619-635`,
  `registry.rs:651-654`). So a RunPurge can be ahead in the log while it is
  not yet durable, and a retry that arrives then is ordered after it. Apply
  then sees an empty directory and starts a second run
  (`run_purge.rs:216-227`, `flow_start.rs:91-116`, `flow_start.rs:139-155`,
  `flow_start.rs:305-308`). By default the purge comes at least 7 days after
  the close (Section 1.3).

- **Log commit versus run `seq`.** The engine always proposes the FlowStart
  and waits for its commit
  (`dexzd-engine/src/flow_coordination/client_operations/mod.rs:83-86`,
  `client_operations/mod.rs:317-323`). So a deduplicated retry is a durable
  log entry. Its stamp advances the partition's last command timestamp
  (`partition_applier.rs:638-639`). Apply's outcome is `Rejected`, so it
  writes no run row and takes no run `seq` (`flow_start.rs:117-124`). In the
  model it changes only the partition clock. Dex covers that clock step
  with [TICK]. The Go SDK documents this retry and
lets the caller fix the request ID (`sdk-go/dex/client.go:510-512`,
`client.go:553`, `client.go:745-747`).

- Without `ignore_already_started_error`, or with another request ID, the
  request is rejected with ALREADY_EXISTS (`flow_start.rs:126-130`). That
  produces `rejected(...)`, which `E_adm` does not admit.
- **StartFlow retries, by request identity.** A **retry** is a StartFlow
  request at a position **after** the word's first StartFlow, whose request
  ID equals the first one's, with `ignore_already_started_error` set. The
  first StartFlow itself is never a retry. This defines retries by the
  request, not by the rule that applies to it.
- **`E_adm` with retries.** The input word is the outcome word (D7). Its
  first StartFlow is the start of Section 5.3, and it is the first request
  of the word. Any number of retries may follow anywhere in the word. Every
  other request is a PublishToChannel as in Section 5.3. The start uses the
  default reuse policy and sets `ignore_already_started_error`. Two rules of
  the failure-free sections are relaxed, only for retries: Section 5.3's "no
  other StartFlow follows", and the requirement that every request ID is
  fresh (Sections 1.3, 4.6, and 5.3). Every other request ID stays fresh.
- **What a retry does.** Under HP, every retry takes [START-RETRY]. Without
  HP, a retry ordered after the RunPurge finds an empty directory and
  creates and starts a new run (`flow_start.rs:91-124`,
  `flow_start.rs:139-155`, `run_purge.rs:216-227`). Admissibility does not
  depend on which of the two happens; HP, a hypothesis on runs, rules out
  the second.
- A client's re-sent PublishToChannel is a new request of the word (D7). The
  rule [PUBLISH] is unchanged.

**[ENGINE-REJECT]** (engine, client input) Events `[rejected(input, err)]`.
Premise: the server is up; the request is a **targeted** request, one that
names an existing run (PublishToChannel in the fragment); and its Flow ID
has no run in the Flow directory. StartFlow is never engine-rejected this
way: the engine always proposes it
(`dexzd-engine/src/flow_coordination/client_operations/mod.rs:72-86`). Only
targeted requests resolve their run before proposing
(`client_operations/mod.rs:286-296`,
`dexzd-engine/src/flow_coordination/read_operations/state_rows.rs:119-126`). In the fragment this happens only after RunPurge (Section
1.3). Effect: none. There is no log entry and no stamp, so the partition
clock does not move. The engine resolves the target run while it builds the
command, and returns NOT_FOUND with FLOW_NOT_EXISTS before anything is
proposed (`dexzd-engine/src/flow_coordination/client_operations/mod.rs:286-296`,
`dexzd-engine/src/flow_coordination/read_operations/state_rows.rs:122-126`,
`dexzd-engine/src/flow_coordination/flow_coordination_error.rs:104-110`). The
event is the one apply's rejection of the same request produces (Section 1.3),
and α maps this rule to that Dex rejection.

### 11.4 Observable events under failure

- `fx` carries the ghost index: `fx(f, (e, m), x)ʲ` is the `j`-th effect of
  some attempt of `(e, m)`. The projection to real labels erases `j`.
- Every failure transition is `τ`: [CRASH], [RECOVER],
  [RECOVER-INTERRUPT], [ATTEMPT-LOST], [ATTEMPT-RETURNED],
  [ATTEMPT-HEARTBEAT], [RESULT-STALE], [START-RETRY], and a [RETRY-FIRE]
  that fires no wait timer. [ATTEMPT-EXHAUSTED] emits `closed(...)`, and H1
  excludes it.

**Repeated `fx`, with the ghost index.** An event `fx(f, src, x)ʲ` of a trace
is **repeated** when an earlier event of the same trace has the same source
`src` and the same index `j`.

**Repeated `fx`, real labels.** The theorem uses this form. A trace `t` is a
**repetition** of a trace `u` when `t` is `u` with events inserted, and each
inserted event equals, in source and label, an event of `u` that comes
before it in `t`.

**First occurrences come in order 1, 2, 3, ….** For each `(e, m)`:

1. Every attempt of `(e, m)` computes the same effect list `fx⃗(e, m)` and
   the same response. `U` is a function (Section 2.2). Its arguments are
   equal across attempts:
   - the snapshot is retained from the first start on every retry path
     (Section 11.6, invariant I2);
   - the Step input does not change while a method is live, and the locals
     and condition results change only when a method of the Step completes;
   - `ctxA` may differ, and H3 makes `U` ignore it.
2. An attempt emits its effects in list order, the `j`-th only after the
   `(j−1)`-th ([WORKER-EFFECT]).
3. So when index `j` first appears in the trace, some attempt has emitted
   indexes `1` to `j − 1` before, and each of them appeared already. The
   first occurrences are `fx⃗(e, m)[1], fx⃗(e, m)[2], …` in order. They are the
   events of one virtual task of Dex. Every other `fx` event of `(e, m)` is
   repeated, and it equals the first occurrence of its index in source and
   label.

So the real-label form holds with the first occurrences as `u`'s events.
Two different indexes may carry equal labels. That does not matter: the
inserted event equals an earlier event of `u`.

### 11.5 Theorem statements

`Tr_free(P)` is the set of projected traces of Dex (Sections 5.4 and 5.5).
`Tr_fail(P)` is the same for DexF, over runs that satisfy H1 to H4, D2, D6,
and the non-empty task ID assumption, with inputs from `E_adm` extended by
[START-RETRY] (Section 11.3) and runs that satisfy HP.

**Lemma (no failures).** `Tr_free ⊆ Tr_fail`. Every Dex run is a DexF run
with no failure transition. This is not literally trivial, because DexF
carries more state: attempt numbers, task IDs, `Z`, `I`, and `h`. The lemma
must show that the extended rules agree with Dex on states that no failure
reached.

**Word mapping.** A DexF outcome word may contain retries (Section 11.3),
and a Dex word of `E_adm` does not. For an outcome word `w`, write `w↓` for
the Dex word obtained by deleting its retries. The first StartFlow is not a
retry, so `w↓` keeps it. Under HP each deleted request took [START-RETRY]
and produced no event.

**Erasure.** For a DexF trace `t`, `erase(t)` removes every repeated `fx`
(the ghost form of Section 11.4). Then `t` is a repetition of `erase(t)`.

**Safety, forward.** For every program `P` of the fragment and every finite
DexF trace `t` for an outcome word `w`, `erase(t)` is a finite Dex trace for
`w↓`. Hence `t` is a repetition of a finite Dex trace.

**Safety, backward.** Only the Lemma: every Dex trace is a DexF trace. The
converse of the forward statement is false. Not every repetition is
reachable. An attempt runs `U` from the start and emits its effects in
order, so index `j` can never have more copies than index `j − 1`. For an
effect list `[a, b, c]`, one extra `c` without extra `a` and `b` before it is
a repetition that no DexF run produces. [decision] The theorem states the two
inclusions, not an equality. (The translation of Section 8.2 gives each
Execute at most one effect, so equality may hold for translated programs.
That would need its own construction and is not claimed.)

**Liveness.** Fairness for DexF replaces F2 to F5 of Section 5.5 and adds FF,
FR, and FI:

- **FF, finitely many failures.** A run has finitely many [CRASH],
  [ATTEMPT-LOST], and [ATTEMPT-RETURNED] transitions.
- **FR, the server comes back.** Every [CRASH] is eventually followed by
  [RECOVER].
- **FI, interrupts commit.** Every element of `I` eventually takes
  [RECOVER-INTERRUPT].
- **F2, timers fire.** A wait timer or retry timer that stays armed and due
  is eventually fired.
- **F3, Steps start.** As in Section 5.5: a pending invocation that stays
  startable while `up` eventually starts.
- **The server is eventually up for good.** By FF there are finitely many
  [CRASH] transitions, and by FR each is followed by [RECOVER]. So every
  fair run has a point after which `up` holds forever. F3, F5, and FI
  constrain the run only from there; a down period only delays them.
- **F4, Workers settle.** Every task of `Θ` eventually becomes `Returned`,
  leaves `Θ` by a failure rule, or is dropped by [WORKER-ABANDON]. Every
  zombie task eventually emits all its effects and returns, or is dropped
  after some prefix of them.
- **F5, results commit.** Every `Returned` task of `Θ` or `Z` eventually
  leaves by a Complete rule or by [RESULT-STALE]. The commit needs `up`; a
  zombie whose result can no longer reach any server is dropped instead
  (F4).

**Progress steps.** [decision] One uniform rule: a DexF step is a
**progress step** if and only if its image under the transition map of
Section 11.6 is a Dex progress step, that is, a non-empty Dex step other
than [TICK]. The DexF marker is defined with this rule, exactly as Section
5.5 defines the Dex marker. Consequences:

- Every step that maps to a stutter is not a progress step: every failure
  rule, [RESULT-STALE], [START-RETRY], bookkeeping, post-close housekeeping,
  and repeated `fx`. In particular, infinitely many retries, which apply
  answers any number of times (`flow_start.rs:99-124`), do not make a run
  `∞`.
- So the theorem says nothing about internal livelock of the engine. An
  infinite run whose only steps other than [TICK] are internal stutters is
  classified by its Dex image, as `✓` or `δ`. For example, a stale
  StepMethodCompleted replayed forever after a normal completion is ignored
  each time (`step_results.rs:863-872`). The real engine stops resubmitting
  once a proposal succeeds (`dexzd-engine/src/effects/mod.rs:427-456`), but
  the fairness assumptions do not forbid such a run. Internal livelock is
  outside the observable model.

Markers:

- `✓` needs the root Flow closed and finitely many progress steps. By F4 and
  F5, every zombie task has settled by then. So a `✓` trace already contains
  every repeated effect that a zombie emits after the close, and is never a
  proper prefix of another fair trace.
- `δ` needs finitely many progress steps and the root Flow RUNNING. A Backoff
  invocation is not quiescent: its retry timer is armed, and F2 fires it. An
  element of `I` is not quiescent either (FI). So `δ` also needs no armed
  timer of either kind, no task in `Θ` or `Z`, an empty `I`, and `up` from
  some point on. FF and FR rule out a fair run that stays down forever, so
  `δ` never stands for a server that is down.

Statement: under FF, FR, FI, F0 to F6 as above, H1 to H4, and HP, for every
admissible outcome word `w`, the set of `erase(t)` over the fair maximal
traces `t` of DexF for `w` equals the set of fair maximal traces of Dex for
`w↓`, markers included. A DexF run **for `w`** gives every request of `w`,
retries included, its final outcome in the order of `w`: an accepted
commit, a rejection by apply, or [ENGINE-REJECT]. F0 applies to all of them. Its marker is decided by
the progress steps defined above, and the Dex marker by Section 5.5 for
`w↓`. The inclusion from Dex to DexF again comes from the Lemma: a fair Dex
run for `w↓`, with the retries of `w` inserted where `w` puts them, is a fair
DexF run for `w` with no failure and the same marker. By FF
there are finitely many attempts and each emits finitely many effects, so a
fair DexF trace has finitely many repeated events.

**End-to-end corollary.** Compose with the expressiveness theorem (Sections
5.4 and 5.5). For every well-formed L program `S`, under the fairness and
hypotheses above:

- every L trace of `S` is in `π(Tr_fail(⟦S⟧))`;
- every trace in `π(Tr_fail(⟦S⟧))` is an L trace of `S` with inserted
  `out(x)` events, each equal in value to an earlier `out(x)`.

On the L side the second statement is weaker than on the Dex side. `π` drops
the source of an `fx`. So an inserted `out(x)` is only known to equal some
earlier `out(x)` with the same value, not to repeat a particular `emit`.

**What the theorem does not say.**

- That an idempotent effect leaves the world unchanged. Idempotent does not
  mean commutative. A zombie's late `set x = 1` can overwrite a later
  `set x = 2`, though each call is idempotent. Exactly-once in effect needs
  more: idempotency keys, commutative effects, or effects fenced by attempt.
- Anything about physical time. A timer fires no earlier than its fire time
  in stamps (D6); a host clock that runs ahead can fire it early in real
  time.
- Anything about a client that re-sends PublishToChannel after a lost
  response. That is a second request (D7).
- Anything about a StartFlow retry that arrives after the run was purged.
  It starts a new run (HP).
- Anything about ASYNC Step methods (D2).
- Anything about runs that exhaust retries, use failure policies, read
  attempt-varying context, or meet non-retryable failures (H1 to H4). A
  long outage can break H1: under the default total retry duration of 4
  hours, an interrupt after a longer outage exhausts the method
  (`retry_backoff.rs:15`, `retry_backoff.rs:115-119`).
- Anything about RPCs, SubFlows, or StopFlow under failures (D1).

### 11.6 The abstraction map α

`α` maps a DexF state to a Dex state. The proof shows that every failure
transition maps to a stutter, every other transition maps to a Dex
transition with the same events, and the first occurrence of each `fx` index
maps to a Dex [WORKER-EFFECT].

**What α forgets.**

- Attempt numbers, task IDs, heartbeat checkpoints, last failures, and retry
  bookkeeping of every invocation.
- Every retry timer in `Tm`.
- `Z`, `I`, and the timer service memory.
- `up`, which Dex does not have.
- The ghosts `h`, `v`, `ps₀`, `n`, `ζ`, `Rc`, and `par`, after it has used them to build the
  virtual tasks and to restore `ps` below.

**Invocations.** α decides by whether the invocation holds a snapshot, not
by its phase. For each invocation `(e, m)`:

- An invocation **with** a snapshot (`ip = R`, or `ip = B` or `ip = P` with a
  retained snapshot) maps to `ip = R` with that snapshot, and to one
  **virtual task** in `Θ`. The virtual task's state comes from the ghost
  variables only, never from a live task:
  - `v(e, m) = NotRun`: phase `Started`;
  - `v(e, m) = Ran(fx⃗, resp)`: phase `Running(fx⃗[h(e, m)+1..], resp)`, having
    emitted `fx⃗[1..h(e, m)]`;
  - `v(e, m) = Returned(resp)`: phase `Returned(resp)`; then `h(e, m)` is the
    length of `fx⃗`.

  Two DexF states with equal `R` and empty `Z` may still map to different
  virtual tasks. Example: in one run an attempt with an empty effect list
  returned, and its result was then ignored after a failure; in the other
  the attempt failed before it ran the handler. The ghost `v` tells them
  apart (`Returned` versus `NotRun`), as it must, because Dex is in a
  different state in the two cases.
- An invocation **without** a snapshot (`ip = P` or `ip = B`) maps to
  `ip = P`, and `h(e, m) = 0`. It never started: either it is waiting for its
  first start, or it failed before any start and is in Backoff or pending
  again with `a ≥ 2`. No attempt of it emitted an effect.
- **An invocation that a cancel or a close ended**, while some real task of
  `(e, m)` is still in `Θ` or `Z`. The cancel cleared the invocation
  (`dexzd-apply/src/command_handlers/flow_close.rs:323-328`,
  `dexzd-apply/src/steps/step_decisions.rs:373-374`), released its
  snapshot (`invocations.rs:344-373`), and only told the running Worker to
  stop (`invocations.rs:376-381`). So `R` no longer shows it, yet a real task
  may still emit, also its first effect. α then keeps a virtual task in `Θ`
  for `(e, m)`, built from the ghosts alone: its arguments are `par(e, m)`,
  and its phase comes from `v(e, m)` and `h(e, m)` as above. This is exactly
  a canceled task of Dex: Section 4.4 keeps such a task in `Θ` until it is
  dropped by [WORKER-ABANDON], and lets it take [WORKER-EFFECT] steps
  meanwhile, also after the Flow closed.
  - The virtual task leaves `Θ` when the last real task of `(e, m)` leaves
    `Θ` and `Z`. If that task's result is ignored and `v(e, m)` is
    `Returned`, the Dex step is [STEP-COMPLETE-STALE]; otherwise it is
    [WORKER-ABANDON].
  - If no real task of `(e, m)` is left at the cancel commit, as for an
    invocation in Backoff with no zombie, the virtual task is abandoned right
    after that commit.
  - This matches F4 and F5 of Section 5.5. DexF's F4 and F5 make every real
    task settle, so the virtual task leaves `Θ`: either after all its
    effects and a stale return, or after a prefix of them.
- **An invocation whose result committed.** Then `v(e, m)` is `Returned` and
  `h(e, m)` is the length of `fx⃗(e, m)`. Every effect a remaining zombie
  emits has an index at most `h(e, m)`, so it is repeated and a stutter. No
  virtual task is needed.

This is sound because a retrying invocation looks to apply exactly like a
running one. It keeps its snapshot from the first start
(`invocations.rs:344-373`, `invocations.rs:123-125`,
`step_results.rs:188-195`). It keeps its lock keys through Backoff
(`invocations.rs:329-342`). Its input, locals, and condition results do not
change. So Dex has a reachable state with that invocation running.

**Snapshot retention on every path.** The zombie argument of Section 11.4
rests on this fact. In the fragment, which has no Attribute locks, it is the
only retained state that does work. Every retry path keeps the snapshot:

| Path | Code | Snapshot |
| --- | --- | --- |
| Ordinary failure | `step_results.rs:344-358`, `invocations.rs:344-373` | moved to attempt `a + 1` |
| Restart interrupt | `attempt_handout.rs:673-717`, then the same `apply_attempt_failed` | moved to attempt `a + 1` |
| Takeover interrupt | same node start and rebuild as a restart (`node_start.rs:459-465`, `partition_rebuild.rs:141-148`) | moved to attempt `a + 1` |
| Returned attempt | `step_results.rs:474-531` | kept; attempt number unchanged |
| Retry timer fires | `invocations.rs:123-125` | kept while the method is the same |
| Next start | `step_results.rs:188-195` | reused |

A fresh snapshot is taken only on these paths: the first start; a start
after a failure before any start, which emitted nothing; an ASYNC start lost
in a crash (D2); and a method switch, which is a new invocation, not a retry
(`invocations.rs:80-92`).

**`seq`-derived values.** Applied failure commands take run `seq` values.
Dex has no such commands. So `R` and `α(R)` are not equal field by field, and
no single relabeling covers every field: a value written by an erased
command has no Dex counterpart. Example: `seq` 1 is the start, 2 the first
[STEP-START], 3 an [ATTEMPT-LOST], 4 the [RETRY-FIRE], 5 the start that
reuses the snapshot, 6 the completion. After 4 the invocation's
`pending_since_seq` is 4 (`step_results.rs:844-852`,
`invocations.rs:109-125`). The Dex run has commits 1, 2, and 3 for DexF's 1,
2, and 6, and its invocation keeps the `ps` of its first pend.

So α is defined on the state of the failure-free model, not on every field
the code persists. It names only the `seq`-derived values that **exist in
the model** (Sections 3.1, 3.2, and 4.2), and says how it restores each from
the ghosts `n`, `ζ`, and `ps₀` of Section 11.2. `ζ` maps the run `seq` of the
`k`-th applied normal command to `k`, and it is strictly order-preserving.

| Value in the model | Code field | Written by | α |
| --- | --- | --- | --- |
| The run's `seq` (Section 3.2) | the cursor's `seq`; `next_run_seq` is one more (`run_cursor.rs:153-156`) | every command that finishes its cursor | `n`; the code's `next_run_seq` corresponds to `n + 1`. With only the start applied, `n = 1` and `next_run_seq = 2` |
| `sws` of a wait (Section 3.1) | `started_waiting_seq` (`dexzd-apply/src/steps/wait_conditions/mod.rs:119`, `wait_conditions/mod.rs:130`) | wait registration in a WaitFor completion, a normal command | `ζ` |
| Enqueue `q = seq · 10⁶ + i` of a message (Section 3.2) | `flow_data.rs:79`, `flow_data.rs:540` | publications of completions and of clients, normal commands; failure commands publish nothing | `ζ(seq) · 10⁶ + i` |
| `ps` of an invocation (Section 3.1) | `pending_since_seq` (`invocations.rs:114`) | `pend` in normal commands, and the retry part of [RETRY-FIRE] (`step_results.rs:844-852`) | `ps₀(e, m)`; retries never change it |
| `ps` of a lock waiter in `L[k].ws` (Section 3.2) | `invocations.rs:73-76` | `pend`, including a re-queue after a before-start failure | not in the Lean fragment, which has no Attribute locks; outside it, `ps₀` for an invocation that started, and the open point at the end of this section for one that never started |
| The created `seq` that orders the cancels of a close (Section 4.2, `close`) | `created_seq` (`dexzd-apply/src/steps/step_creation.rs:91`) | Step creation, only in normal commands; the failure path that creates a Step is excluded by H2 (`step_results.rs:407-416`) | `ζ` |

**Every other persisted field is outside α by construction.** It has no
counterpart in the model, so α has nothing to map it to. Examples are the
search projection, history and its checkpoints, Attribute Sync, hard-delete
objects, time travel, retention, and storage accounting (all excluded in
Section 1.3); the outbox and inbox (SubFlows, outside the fragment by D1);
the request table (fresh request IDs, Section 1.3); the dispatch queue index
(dispatch order is nondeterministic, F3); and value IDs and `completed_seq`,
which the model does not keep (Section 3.2 stores values directly). This is
sound for the same reason the failure-free model is: the failure-free rules
in the fragment never read these fields, and Section 1.3 records why each
feature that reads them is excluded. α adds no new abstraction here; it
inherits the one Sections 1 to 4 already make. So this section makes no
claim to list every `seq`-derived field the code writes.

In the fragment, `ps` of an invocation orders only lock queues, which the
fragment lacks, and dispatch order is nondeterministic in the model (F3). So
α may also ignore it there. The ghost copy keeps α exact outside the
fragment.

[assumption, to check in Stage 1] Apply reads the `ζ`-mapped values only to
compare them: waiter order, queue order, and the cancel order at a close
(`flow_close.rs:315-318`). Value IDs are not in the model, but they reach the
Worker as value references (`method_request.rs:551-593`). So the model also
needs that the SDK resolves a reference to its value before user code sees
it: then `U` never sees an ID, and α preserves every argument of `U`.

Other values:

- **Timestamps.** α keeps the stamp of every normal commit. Failure commits
  use stamps between them, and Dex covers that time with [TICK]. So wait
  timer fire times are equal on both sides.
- **Channel message IDs** are UUIDs the engine assigns per result
  (`dexzd-engine/src/effects/dispatch/worker_result.rs:81-88`). Only one
  result per invocation commits, so a bijection of IDs suffices. No event of
  `E_adm` shows them.
- **Step IDs** are equal. Failure commits never create Steps under H2, so the
  counters `cnt` are equal.

**Invariants that `Related` needs.** Stage 1 must state them before any
proof (plan Section 4.2), and the inductiveness fuzz must test them.

- **I1, one committer.** Each invocation commits at most one result. After
  it, `inv = ⊥`, and every remaining task of that invocation is stale.
- **I2, one effect list.** Every attempt of `(e, m)`, in `Θ` or `Z`, computes
  `U` on the same arguments up to `ctxA`. So all attempts have the same effect
  list and response (H3).
- **I3, prefixes.** Every task of `(e, m)` in `Θ` or `Z` has emitted a
  prefix of `fx⃗(e, m)` of length at most `h(e, m)`. A task that returns has
  emitted the whole list. Every task's phase is at most `v(e, m)`. No live
  task needs to witness `h` or `v`: the ghosts keep them after the task
  that reached them is gone.
- **I4, retained state.** An invocation that holds a snapshot holds its lock
  keys, and its snapshot is the snapshot of its first start. An invocation
  without a snapshot has never started, holds no lock key, and has
  `h(e, m) = 0`.
- **I5, same apply state.** `α(R)` equals the Dex apply state on every
  component of the model, with the `seq`-derived values restored as the
  table above says. Fields without a counterpart in the model are outside
  α.
- **I6, retry timers.** Each armed retry timer belongs to a Backoff
  invocation with that attempt number, or it is stale and its firing is a
  no-op.
- **I7, interrupts.** Each element `(e, m, a, t)` of `I` named an invocation
  that was Running with task ID `t` in the durable state that the last
  [RECOVER] read (`partition_rebuild.rs:112-148`), and `t ∉ dom(Θ)`. Nothing
  more holds: the zombie task `t` may have been dropped, and the invocation
  may have moved on, in which case the interrupt is stale.

**Transition map.**

| DexF transition | Dex transitions |
| --- | --- |
| [STEP-START] of an invocation without a snapshot (it computes one) | [STEP-START] |
| [STEP-START] that reuses a retained snapshot | stutter |
| A task runs the handler while `v(e, m) = NotRun` | the virtual task runs the handler |
| A task runs the handler while `v(e, m)` is further | stutter |
| A task returns while `v(e, m)` is not `Returned` | the virtual task returns |
| A task returns while `v(e, m)` is `Returned` | stutter |
| [WORKER-EFFECT] or [ZOMBIE-EFFECT] with `j > h(e, m)` (so `j = h + 1`) | [WORKER-EFFECT] of the virtual task |
| The same with `j ≤ h(e, m)` | stutter; the event is repeated |
| Complete rule of a task in `Θ` or `Z` | the Complete rule of the virtual task |
| [TIMER-FIRE] / [RETRY-FIRE] | [TIMER-FIRE] on the wait-timer sub-list (OD1), or stutter |
| The retry part of [RETRY-FIRE]: `B` to `P`, with or without a snapshot | stutter (`R` to `R`, or `P` to `P`) |
| [ATTEMPT-LOST] before the first start (`P` to `B`, no snapshot) | stutter (`P` to `P`) |
| Cancel or close of a Step whose attempts are all gone | the same commit, then [WORKER-ABANDON] of the virtual task |
| Cancel or close of a Step with a real task left in `Θ` or `Z` | the same commit; the virtual task stays, built from `par`, `v`, and `h` |
| The last real task of a canceled invocation leaves `Θ` and `Z` | [WORKER-ABANDON], or [STEP-COMPLETE-STALE] when `v` is `Returned` and the result was ignored |
| A rejected client input of the word, such as a PublishToChannel after the close | the same Dex rejection, with the event `rejected(input, err)` |
| [ENGINE-REJECT] of a PublishToChannel after RunPurge | Dex's apply rejection of the same request, with the same event |
| A bookkeeping command, such as StepAttemptQueued | stutter |
| Post-close housekeeping: the retention TimersFired, RunPurge | stutter; after RunPurge, α reads the run from `Rc` |
| [CRASH], [RECOVER], [RECOVER-INTERRUPT], [ATTEMPT-LOST], [ATTEMPT-RETURNED], [ATTEMPT-HEARTBEAT], [RESULT-STALE], [START-RETRY] | stutter |

A canceled invocation's virtual task follows Dex's rules for a canceled task:
it may keep emitting effects, and it is abandoned once no real task of it is
left (the canceled-invocation case above).

**Open point outside the fragment.** A failure before the first start
moves the invocation to the tail of its lock queues (Section 11.3). No Dex
run does that. A version of the theorem with Attribute locks needs either
an argument that the new position is reachable in Dex, or one more
hypothesis.

## Appendix A. Assumptions and decisions in one list

1. User code is a total function with output-only external effects (2.2).
2. Durability does not change failure-free behavior (1.3).
3. One run per Flow ID; fresh Flow IDs (1.3, 3.4).
4. Resource limits are never hit (1.3).
5. Per-partition clocks; `t_p` strictly increases between commits of
   partition `p`; partitions advance independently (3.4, 4.1).
6. Dispatch order is nondeterministic; the rules are unfair (4.4, 6.3).
7. [TIMER-FIRE] fires a non-empty prefix of the due timers; exact for
   failure-free runs (4.5). **False in one window**: a command applied
   before TimersFired can arm an earlier-due timer that the scan did not
   see. See Open question 26. [owner, 2026-11-10] Kept as an explicit
   modeling idealization by both theorems (OD1, option A).
8. Fusing RPC lock acquisition and snapshot preserves traces (4.7).
9. Cross-Flow delivery is exactly once, in any order, after any delay (4.8).
10. Different Flows interleave arbitrarily (6.2).
11. Equal `sws` among waiters of one Channel does not occur without time
    travel (Open question 4).
12. TimerId byte order as derived from protobuf encoding (Open question 12).
13. Reads are not observable (5.1).
14. Equivalence is safety (finite weak traces) plus liveness (fair maximal
    traces with markers, per admissible input word, [TICK] not counted as
    progress), untimed, under `E_adm` and `π` (5.3-5.5).
15. Fairness assumptions F0 to F6 (5.5).
16. ABORTED is an observable RPC outcome [owner] (Open question 11).
17. RPCs, SetAttributes, DeleteChannelMessage, SkipTimer, StopFlow,
    UpdateFlowConfig, and Flow timeouts are outside `E_adm` (1.3, 5.3).
18. Every modeled request ID is fresh (1.3).
19. L exceptions are data, never Worker errors (Open question 19).
20. The SDKs fill per-movement Step options from the Step definition (2.1).
21. [RPC-COMMIT] follows the #1450 fix, not the code at `9b37482ed` [owner]
    (Open question 3).
22. On one node, partition clocks read one wall clock with bounded skew; only
    a timed theorem needs it (Open question 22).
23. L adopts the semantics of Section 8.1 (FIFO mutex and condition variable,
    cooperative cancellation, asynchronous emit, exit stops all threads,
    explicit late timer `fire` of the least due timer in `(deadline, timer
    number)` order, a clock that strictly increases between stamped actions,
    item 29).
24. An admissible input word is a sequence of attempted requests, accepted or
    rejected, each exactly once and in order; it begins with the unique start,
    and every later request is a publication to an external Channel (5.3).
25. The translation encodes exit values as `{"exit": enc(v)}` and failure
    messages as FF's string input (5.3).
26. Every timed L operation runs in the single Step type `Timed`, with one
    timer at index 0, so Dex's TimerId tie-break is numeric order on the Step
    number; derived from the protobuf encoding rules (8.2).
27. Step numbers and timer numbers are unbounded natural numbers in the
    model. The real limit is fewer than 2³² Step executions of one type per
    run; the numeric timer-ID order is exact within that limit [owner] (8.2).
28. The theorem covers well-formed L programs only: no thread calls `wait`,
    `signal`, or `broadcast` without holding the mutex, or `unlock` on a
    mutex it does not hold [owner] (0, 5.4, 5.5, 8.1).
29. L has a fixed timing model: each kind of statement takes a fixed number
    of stamped actions, equal to the number of Dex commits of its
    translation, and a batch of consecutive `fire` actions shares one stamp
    [owner] (8.1, 8.2).
30. A dynamic type error halts its thread, and so does an uncaught exception
    with a non-string value [owner] (8.1).
31. A condition variable belongs to one mutex, fixed by its identity;
    `wait(cv, m)` with another mutex is a type error [owner, kept as taken]
    (8.1).
32. A thread that ends by observing its cancel flag is joinable: its last
    Step publishes to `join/t` [owner, kept as taken] (8.1, 8.3).
33. A thread that ends leaves one join token; a second `join(t)` blocks
    forever [owner, kept as taken] (8.1).
34. `fail(s)` ends the program as `exit(v)` does: issued emits may still
    deliver, and later input is `refused` [owner, kept as taken] (8.1).
35. Under `E_adm`, the start input and every published message are encoded L
    values; other Dex values on an external Channel are not admissible
    [owner, kept as taken] (5.3).
36. Under `E_adm`, the start Step's `skipWaitFor` is read from its definition,
    and it selects no AttributeMap and no Channel [owner, kept as taken]
    (4.3).
37. A task stays in `Θ` until its result commits or it is dropped; F4 is
    "becomes `Returned` or is dropped", F5 is "a `Returned` task leaves `Θ`"
    [owner, kept as taken] (5.5).
38. A fair L run satisfies the L forms of F0 to F6 [owner, kept as taken]
    (5.5).
39. `select` has one `recv` branch and one `after` branch; without `after`
    it is `recv(c)` followed by the branch [owner, kept as taken] (8.1).
40. L literals are only null, Boolean, integer, and string. Mutex handles
    come only from `newMutex` and the static-mutex expression; thread handles
    come only from `fork` [owner, 2026-11-10] (8.1).
41. The environment sends plain data only: the program input and every
    external message are literals, and `E_adm` admits only encoded literals
    [owner, completing the decision of 2026-11-10] (5.3, 8.1).
42. The failure theorem covers the fragment of the Lean model of revision 8:
    one Flow, `E_adm` with StartFlow retries, no RPCs, SubFlows, StopFlow,
    or Attribute locks [owner, D1, 2026-11-10] (11.1).
43. Only SYNC Step methods; every invocation's pinned durability is SYNC
    [owner, D2, 2026-11-10] (11.1).
44. Zombie Workers are in the model: an abandoned attempt may keep emitting
    effects [owner, D3, 2026-11-10] (11.1).
45. Hypotheses H1 to H4: no exhaustion, no failure policies, user code
    ignores `ctxA`, no non-retryable failures. The features stay in the
    model; the theorem assumes them away [owner, D4, 2026-11-10] (11.1).
46. Liveness assumes finitely many failures (FF), that the server comes back
    (FR), and that every interrupt commits (FI) (D5, 11.5).
47. On one node, the wall clock does not step backward between the timer
    service's due check and the stamping of its TimersFired command [owner,
    D6, 2026-11-10] (4.5, 11.1). The failure-free [TIMER-FIRE] rests on it
    too.
48. The input word under failures is the outcome word: client requests in
    the order of their final outcomes (accepted commit, apply rejection, or
    [ENGINE-REJECT]). A
    re-sent PublishToChannel is a second request. A StartFlow retry carries
    the start's request ID and `ignore_already_started_error`, under the
    default reuse policy; it is the only exception to item 18 [owner, D7,
    2026-11-10] (11.1, 11.3).
49. A standby takeover is [CRASH], [RECOVER], and an overlap of the old
    owner; one owner writes the log at a time; there is no multi-node
    partition ownership [owner, D8, 2026-11-10] (11.1, 11.3).
50. Every logged fenced Step command carries a non-empty task ID (11.3).
51. Failure transitions are nondeterministic and unconditioned, an
    over-approximation of failure detection [decision] (11.3).
52. A result of an interrupted task may commit before its interrupt; the
    model allows both orders [decision] (11.3).
53. A log write with an unknown outcome is a crash (11.3).
54. [ATTEMPT-RETURNED] does not assume that the task emitted nothing; such a
    task is a zombie [decision] (11.3).
55. Apply reads `seq`-derived values only to compare them, and value IDs
    only for equality, so `Related` may relabel them by an order-preserving
    map [assumption, to check in Stage 1] (11.6).
56. The lock-queue move after a failure before the first start matters only
    with Attribute locks, which the fragment does not have [decision]
    (11.3, 11.6).
57. `dexzd-simulation` does not check that retries repeat only external
    effects [assumption, from the plan's survey, not re-verified] (7,
    item 25).
58. Safety is stated as two inclusions, not an equality: erasing repeated
    `fx` from a DexF trace gives a Dex trace, and every Dex trace is a DexF
    trace [decision] (11.5).
59. A DexF step is a progress step if and only if its image under α is a
    Dex progress step. So the theorem says nothing about internal livelock
    of the engine [decision] (11.5).
60. Both theorems use the prefix rule [TIMER-FIRE] of Section 4.5 as an
    explicit modeling idealization; the code's [TIMER-FIRE-SCAN] is
    documented and used by neither. They do not cover runs where timers due
    at almost the same time fire out of due order [owner, 2026-11-10, OD1
    option A] (11.3; Open question 26).
61. The server keeps its timer behavior: no distributed system guarantees
    precise timer firing [owner, 2026-11-10] (Open question 26).
62. The SDKs will get an optional caller-supplied RPC request ID (#1519);
    until it ships, the RPC route to exactly-once publication is not
    available. PublishToChannel gets no request ID [owner, 2026-11-10]
    (11.1, D7).
63. The SDKs will accept an optional run ID for StopFlow (#1520); StopFlow
    stays outside `E_adm` [owner, 2026-11-10] (11.1, 9).
64. A rejected client input of the word keeps its event `rejected(input,
    err)` under α; only internal ignored or rejected commands are silent
    (11.2).
65. The model keeps a closed run in `R` forever and has no rule for
    retention or purge. Under `E_adm` this changes no event: a publication
    is rejected with NOT_FOUND and FLOW_NOT_EXISTS before and after the
    purge (1.3).
66. HP, a predicate on runs: in the committed log, the command of every
    retry, a StartFlow request after the word's first StartFlow with the
    same request ID, is ordered before the RunPurge of the original run. It holds
    trivially without a RunPurge. It is not a property of the input word
    (11.3, 11.5).
67. F2 does not apply to the retention timer. That post-close
    housekeeping and [START-RETRY] are not progress steps follows from item
    59 (11.2, 11.5).
68. [ENGINE-REJECT]: after RunPurge the engine rejects a request with
    NOT_FOUND before any proposal; it has no log entry and no stamp, and it
    produces the same event as apply's rejection (11.3).
69. A canceled invocation keeps a virtual task, built from the ghost `par`
    and from `v` and `h`, while any real task of it remains (11.6).
70. `Σ_F` records whether the server is up; commit and engine rules need
    it, and a fair run is eventually up for good (11.2, 11.5).
