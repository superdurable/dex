# Dex Core Semantics (Step 0 of the expressiveness proof)

Status: revision 6 (after three adversarial reviews, the owner decisions of
2026-11-09 on the review of the Lean statements, and the owner decision of
2026-11-10 on unforgeable handles; see [Review log](#10-review-log)). The
Lean development is in `formal/lean/`.

Base commit: `9b37482ed` on `main`. Every `path:line` below is relative to the
repository root at that commit.

## 0. Purpose and conventions

This document fixes the formal semantics of the Dex core programming model.
A later Lean development will use it to state one theorem:

> Every well-formed program of a small concurrent imperative language **L**
> translates into a Dex Flow that is trace-equivalent to it, assuming no
> failures.

Failures, crashes, retries, and redelivery are out of scope. A separate,
later proposition covers them (see [Future work](#9-future-work)).

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
| Retries, backoff timers, failure policies (`PROCEED_ON_FAILURE`, `PROCEED_TO_CONFIGURED_STEP`), heartbeats, method timeouts, attempt return | Failure handling. Out of scope by the task statement. |
| Durability SYNC/ASYNC | Changes only what survives a crash. [assumption] In a failure-free run both produce the same committed log, because the speculative overlay is checked byte for byte against the committed outcome (`server-zd/crates/dexzd-engine/src/flow_coordination/registry.rs:326-330`). |
| Time travel (ResetFlow), history, history offload, search projection, Attribute Sync, hard-delete Attributes, permission mappings, `record_events` | Operations, storage, and projections. Attribute Sync only adds stutter before a natural close (Section 4.9). |
| Repeated request IDs | Not only a retry concern: apply replays the recorded reply for a repeated request ID with the same content, and rejects one with different content, even without failures (`client_writes.rs:41-65`, `client_writes.rs:138-147`, `client_writes.rs:260-268`, `server-zd/crates/dexzd-apply/src/command_handlers/rpc_committed.rs:58-74`; StartFlow matches its request ID at `flow_start.rs:115`). [assumption] Every modeled input carries a globally fresh request ID, or none. |
| Flow ID reuse policies and run IDs | [assumption] Every L program run starts a fresh Flow ID. SubFlow IDs are the exception; see Open question 13. |
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

Ctx      ::= ⟨ flowId, stepExecutionId, fromStepExecutionId, ... ⟩
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
- [assumption] One Flow ID has one run. Run IDs and attempts are elided.

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
with the same task. Otherwise the command is ignored and the result is lost
(`server-zd/crates/dexzd-apply/src/steps/step_results.rs:857-872`).

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
- [assumption] So a prefix is exact for failure-free runs. The "any non-empty
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

  Rationale [owner]: real programs cannot rely on timing races, so any fixed
  timing model leaves the meaning of correct programs unchanged. Choosing the
  model aligned with Dex makes the equivalence exact without narrowing what
  the theorem covers in practice. It is like counting one time unit per
  instruction in algorithm analysis.
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
  the engine against simulated object storage, clocks, Workers, and crashes;
  it is relevant to the later crash-transparency proposition.
- RPC entry points for L, with ABORTED as an observable result [owner].
- The Lean statement needs the projection of Section 5.3, the two
  equivalences of Sections 5.4 and 5.5, and the fairness assumptions F0 to F6.

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

## Appendix A. Assumptions and decisions in one list

1. User code is a total function with output-only external effects (2.2).
2. Durability does not change failure-free behavior (1.3).
3. One run per Flow ID; fresh Flow IDs (1.3, 3.4).
4. Resource limits are never hit (1.3).
5. Per-partition clocks; `t_p` strictly increases between commits of
   partition `p`; partitions advance independently (3.4, 4.1).
6. Dispatch order is nondeterministic; the rules are unfair (4.4, 6.3).
7. [TIMER-FIRE] fires a non-empty prefix of the due timers; exact for
   failure-free runs (4.5).
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
