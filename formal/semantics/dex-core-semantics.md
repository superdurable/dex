# Dex Core Semantics (Step 0 of the expressiveness proof)

Status: draft for owner review. No Lean yet.

Base commit: `9b37482ed` on `main`. Every `path:line` below is relative to the
repository root at that commit.

## 0. Purpose and conventions

This document fixes the formal semantics of the Dex core programming model.
A later Lean development will use it to state one theorem:

> Every program of a small concurrent imperative language **L** translates into
> a Dex Flow that is trace-equivalent to it, assuming no failures.

Failures, crashes, retries, and redelivery are out of scope. A separate,
later proposition covers them (see [Future work](#9-future-work)).

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
disagreement.

Markers used in this document:

- **[code]** The claim cites the implementing code.
- **[assumption]** The claim is a modeling choice or an inference without
  direct code evidence. Each one is listed again in Section 7.
- **[decision]** The claim is a choice between defensible options. The
  alternative is named.

Notation:

- `f` is a Flow ID, `e` a Step execution ID, `T` a Step type, `c` a Channel
  name, `k` an Attribute key, `r` an RPC name, and `v` a value.
- `m ∈ {W, E}` is a Step method: WaitFor or Execute.
- `⊥` means absent. `xs ++ ys` concatenates lists. `M[k ↦ v]` updates a map.
- `t` is the global clock in microseconds. `seq` is the run sequence number;
  every committed command of a run takes the next `seq`.

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
| RPCs (client-invoked handlers that read and write the run and may start or cancel Steps) | Synchronous external interaction. |
| SubFlows (child Flows started by a wait, result delivered to the parent) | Calls across Flows. |
| Client inputs: StartFlow, PublishToChannel, SetAttributes, DeleteChannelMessage, StopFlow | The environment's inputs. |
| The clock and timer firing | Time. |

### 1.2 Derived (expressible in the core, kept out of the rules)

| Construct | Treatment |
| --- | --- |
| `Until(cond)` | Equals `AllOf(cond)` (`sdk-go/dex/condition.go:40-43`). |
| `SkipWaitImmediately` and Steps without WaitFor | An empty wait, or `skip_wait_for`; both go straight to Execute (Section 4.2). |
| Flow timeout with policy FAIL or CANCEL | A Flow-level timer whose firing closes the Flow (`server-zd/crates/dexzd-apply/src/command_handlers/step_commands.rs:211-301`). Equivalent to a dedicated Step that waits on a Timer and then ForceFails. Kept out to keep rules small. |
| Flow timeout with policy HANDLER | Creates the Step `sys:timeout_handler-1` (`step_commands.rs:222-279`). Excluded from the rules for the same reason. It matters only for Open question 2. |
| SkipTimer (client completes a pending timer early) | A client input that marks one timer completed (`server-zd/crates/dexzd-apply/src/steps/wait_conditions/timer_skip.rs:31-110`). Modeled as rule [SKIP-TIMER] for completeness. |
| ChannelMap and AttributeMap | Families of Channels and Attributes keyed by a string. Dynamic names are what the L translation needs for per-instance queues. |

### 1.3 Excluded

| Construct | Reason |
| --- | --- |
| Stream | Not in apply at all. It is in server memory only, lossy by capacity, and a Step write is not rolled back (`docs/content/concepts/primitives/stream.mdx:29-39`). It has no durable semantics to translate into. |
| Retries, backoff timers, failure policies (`PROCEED_ON_FAILURE`, `PROCEED_TO_CONFIGURED_STEP`), heartbeats, method timeouts, attempt return | Failure handling. Out of scope by the task statement. |
| Durability SYNC/ASYNC | Changes only what survives a crash. [assumption] In a failure-free run both produce the same committed log, because the speculative overlay is checked byte for byte against the committed outcome (`server-zd/crates/dexzd-engine/src/flow_coordination/registry.rs:326-330`). |
| Time travel (ResetFlow), history, history offload, search projection, Attribute Sync, hard-delete Attributes, permission mappings, `record_events` | Operations, storage, and projections. Attribute Sync only adds stutter before a natural close (Section 4.9). |
| Request deduplication (`request_id`) | Matters only for client retries, which are failures. |
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
these options
(`protos/dex.proto:452-475`):

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
RpcRec ::= ⟨ f, r, v, keys, tx : Bool, phase : Called | Snapped(Snap) | Ran(Fx*, RpcResp) ⟩
Task   ::= ⟨ f, e, m, input, snap, locals, cr, phase : Started | Running(Fx*, Resp) | Returned(Resp) ⟩
```

- `RL`: `server-zd/crates/dexzd-engine/src/flow_coordination/rpc_locks.rs:20-51`.
  The comment says "never logged".
- `Tx` drives `FlowCommand.is_transactional_rpc_in_flight`, which decides
  whether a natural close is deferred
  (`server-zd/crates/dexzd-apply/src/command_handlers/flow_close.rs:281-292`).
  The engine sets the stamp only on a command that would close the run, and
  counts the admitted transactional RPCs other than the one this command
  commits (`server-zd/crates/dexzd-engine/src/flow_coordination/registry.rs:593-615`).
  The rules write that count as `Tx⁻[f]`: `Tx[f]` minus one when the command is
  the commit of an admitted transactional RPC, else `Tx[f]`.
- The timer service decides when `TimersFired` is proposed
  (`server-zd/crates/dexzd-engine/src/effects/timer_service.rs:116-205`).
- The dispatcher decides which startable pending invocation is handed to a
  Worker next (`registry.rs:297-322`).

### 3.4 System

```text
Σ ::= ⟨ t : ℕ,  P : Program,
        R : FlowId ⇀ Run,
        η : Engine,
        N : multiset OutboxMsg ⟩     -- cross-Flow messages in transit
```

[assumption] One Flow ID has one run. Run IDs and attempts are elided.

## 4. Operational semantics

### 4.1 Shape of the rules

A transition is `Σ --ℓ--> Σ'`. The label `ℓ` is either an observable event
(Section 5) or `τ`.

Every rule marked **(commit)** is one apply command. It is atomic: apply
computes all its writes in one transaction, and a rejected command writes
nothing (`server-zd/crates/dexzd-apply/src/command_handlers/mod.rs:90-300`).
Each commit stamps `now = t` and takes `seq := seq + 1` for its run. The
owner's clock is strictly increasing per partition
(`server-zd/proto/dexzd/v1/command.proto:39-40`). [assumption] One global
clock, and `t` strictly increases between two commits of the same run.

Rules not marked commit are engine or Worker steps. They change only `η`, `N`,
or `t`.

The conclusions call deterministic meta-functions. Section 4.2 defines them.
Each meta-function runs inside the commit that calls it and in the order
written.

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
candidates(AnyComb) = [ each combination ], members resolved by ID:
                      timers first, then channels, then SubFlows; a duplicated ID → its last declaration
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
[TIMER-FIRE].

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

**[FLOW-START]** (commit) Label `start(f, T₀, v₀, A₀)`.

```math
\frac{f \notin \mathrm{dom}(R)}
     {\Sigma \xrightarrow{\;start(f,T_0,v_0,A_0)\;} \Sigma[R(f) \mapsto \rho_0]}
```

`ρ₀` is the empty run with `st = Running`, then `A := A₀` applied as writes,
then `new(T₀, v₀, o₀, "__start__")` when `T₀` is not empty.
`server-zd/crates/dexzd-apply/src/command_handlers/flow_start.rs:191-247`.
Without a start Step the Flow runs with no Step and waits for RPCs or a close
(`flow_start.rs:204-205`).

### 4.4 Step methods

A Step method is **three** transitions: Start, Run, Complete. Other
transitions can interleave between them. This is the central fact of the
semantics (Section 6).

**[STEP-START]** (commit, `StepAttemptStarted`) Label `τ`.

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
  nondeterministic; Worker availability adds no other constraint. No fairness
  is assumed in the rules (Section 6.3).

**[WORKER-EFFECT]** (Worker) Label `fx(f, e, m, a)`.
A task in phase `Started` computes `(fx⃗, resp) := U_{T,m}(ctx, input, snap, locals, cr)`
and becomes `Running(fx⃗, resp)`. Each step of this rule emits the head of
`fx⃗`. When `fx⃗` is empty the task becomes `Returned(resp)`.

The Worker runs outside apply. No state of `ρ` changes. The Worker receives
the request the engine builds from the task
(`server-zd/crates/dexzd-engine/src/effects/method_request.rs:449-545`) and
answers on the streams `InvokeWaitForMethodOutput` and
`InvokeExecuteMethodOutput` (`protos/dex.proto:1523-1529`,
`protos/dex.proto:1558-1564`).

**[WORKER-ABANDON]** (Worker) Label `τ`. A task whose invocation was canceled
(Section 4.2 `release` with a cancel end) may be dropped at any point, before
or after any of its effects. The cancel commit emits the effect that tells the
Worker to stop (`server-zd/crates/dexzd-apply/src/steps/invocations.rs:376-381`).
Cancellation is cooperative
(`docs/reference/go/flows-and-steps/step-options.mdx:282-294`).

**[STEP-COMPLETE-WAITFOR]** (commit, `StepMethodCompleted`) Label `τ`, or a
`closed` label when the Flow closes.

Premise: the task is `Returned(⟨E, locals, W⟩)` and `S[e].inv = ⟨W, R, …⟩`
with the same task. Otherwise the command is ignored and the result is lost
(`server-zd/crates/dexzd-apply/src/steps/step_results.rs:857-872`).

```text
release(e) with a finish end;  S[e].inv := ⊥             -- step_results.rs:915-925
effects(e, E, locals)                                    -- step_results.rs:927-946
register(e, W)                                           -- step_results.rs:952-958
```

**[STEP-COMPLETE-EXECUTE]** (commit, `StepMethodCompleted`) Label `τ` or `closed`.

Premise as above with `ExecResp ⟨E, locals, D⟩` and method `E`.

```text
release(e) with a finish end;  S[e].inv := ⊥
effects(e, E, locals)                                    -- step_results.rs:960-979
decide(e, D)                                             -- step_results.rs:980-987
```

Note the order: the data effects, including `wake` of Channel waiters, come
before the decision's cancellations and close (Open question 7).

**[STEP-COMPLETE-STALE]** (Worker) Label `τ`. A `Returned` task whose
invocation is no longer running is dropped. Apply ignores the command
(`step_results.rs:863-872`).

### 4.5 Time and timers

**[TICK]** (environment) Label `τ` (or `tick(δ)` in the timed variant of
Section 5.3).

```math
\frac{\delta > 0}{\langle t, \dots\rangle \xrightarrow{\tau} \langle t+\delta, \dots\rangle}
```

Apply never reads a clock; every command carries the owner's wall-clock time,
strictly increasing per partition (`server-zd/proto/dexzd/v1/command.proto:39-40`,
`server-zd/crates/dexzd-apply/src/lib.rs:11-12`).

**[TIMER-FIRE]** (commit, `TimersFired`) Label `τ` or `closed`.

```math
\frac{\mathit{Due} = \{\, i \in \mathrm{dom}(Tm) \mid Tm(i) \le t \,\} \ne \emptyset
      \qquad D = \text{a non-empty prefix of } \mathit{Due} \text{ in index order}}
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

- The timer service never fires before the owner's clock reaches the fire
  time, and fires all due timers of a run in one command
  (`server-zd/crates/dexzd-engine/src/effects/timer_service.rs:9-11`,
  `timer_service.rs:116-205`).
- [assumption] The prefix (rather than all due timers) over-approximates the
  service's load limit and per-key retry delay.
- Firing lateness is unbounded. Any other commit may come between a timer
  becoming due and [TIMER-FIRE].

**[SKIP-TIMER]** (commit) Label `skip(f, e, j)`. When `S[e].φ = WT` and timer
`j` is not done: mark it done, disarm it, `eval(e)`, `gcheck()`. Otherwise the
input is rejected with INVALID_ARGUMENT and nothing changes
(`timer_skip.rs:31-110`).

### 4.6 Client inputs

Each client input is one commit and one observable input label. Rejections
change nothing and return an error to the client; the error reply is an
observable output `reply(f, err)`.

**[PUBLISH]** Label `pub(f, [(c, v)])`. Requires `st = Running`, else NOT_FOUND.
The engine gives every message a fresh ID. Apply appends the messages in order
and calls `wake` on the changed Channels.
`server-zd/crates/dexzd-apply/src/command_handlers/client_writes.rs:206-251`.
A publication during a pending close is accepted: the handler checks only
`st = Running` (`client_writes.rs:213-215`).

**[SET-ATTRIBUTES]** Label `set(f, [(k, v)])`. Requires `st = Running`. Writes
in order. No Step wakes: no wait condition reads Attributes. Attribute locks
are not consulted (Open question 8).
`client_writes.rs:131-204`.

**[DELETE-MESSAGE]** Label `del(f, c, id)`. Requires `st = Running`, no pending
close, and the message pending in `Q[c]`; otherwise NOT_FOUND or
FAILED_PRECONDITION. Claimed messages cannot be deleted.
`client_writes.rs:253-320`.

**[STOP]** Label `stop(f, kind)`. Requires `st = Running`, else NOT_FOUND.
`closeNow(κ)` with κ = Canceled (CANCEL or unspecified), Terminated
(TERMINATE), or Failed(CLIENT_API_FAILING_FLOW) (FAIL). It closes even during a
pending close and with transactional RPCs in flight.
`client_writes.rs:322-362`, `flow_close.rs:43-58`.

### 4.7 RPCs

An RPC is a client call that runs a handler on a Worker. It has up to four
transitions: Call, Snap, Run, and Commit-and-Return.
`server-zd/crates/dexzd-engine/src/flow_coordination/client_operations/rpc_invocation.rs:62-302`.

Let `tx := request.is_transactional ∨ keys ≠ ∅` (`rpc_invocation.rs:73`).

**[RPC-CALL]** (engine) Label `call(f, r, v, id)`. Adds `Rq[id] := ⟨f, r, v,
keys, tx, Called⟩`. If `tx`, it is admitted only while `st = Running` and
`pc = ⊥`; admission increments `Tx[f]`. Otherwise it returns NOT_FOUND or
FAILED_PRECONDITION at once (`rpc_invocation.rs:170-191`,
`registry.rs:227-262`).

**[RPC-SNAP]** (engine) Label `τ`.

```text
if keys ≠ ∅:
  if ∃k ∈ keys. (f,k) ∈ dom(RL) ∨ L[k].h ≠ ⊥:  emit ret(id, ABORTED);  drop Rq[id]   -- [RPC-ABORT]
  RL[(f,k)] := id for k ∈ keys                                            -- registry.rs:735-763
Rq[id].phase := Snapped(snapshot of the committed run with the RPC's load selections)
                                                                          -- rpc_invocation.rs:202-221, 464
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

**[RPC-RUN]** (Worker) Labels `fx(f, rpc:id, a)`. As [WORKER-EFFECT], with
`U_r(ctx, v, snap)`. Ends in `Ran(fx⃗, resp)`. The engine calls the Worker and
validates its response (`rpc_invocation.rs:234-271`).

**[RPC-RETURN-READONLY]** (engine) Label `ret(id, output)`. When `¬tx` and
`resp` has no effects (no writes, deletions, publications, record events,
moves, or cancels). Nothing commits; the run is unchanged
(`rpc_invocation.rs:272-286`, `rpc_invocation.rs:630-643`).

**[RPC-COMMIT]** (commit, `RpcCommitted`) Label `ret(id, output)` and maybe
`closed`.

```text
if st ≠ Running:  emit ret(id, NOT_FOUND);  release RL of id;  return   -- rpc_committed.rs:75-79 (the handler already ran)
if tx ∧ some deletion names no pending message:  emit ret(id, NOT_FOUND); return   -- rpc_committed.rs:91-103
A, Q: writes, then deletions, then publications                          -- rpc_committed.rs:104-121
wake(published Channels)                                                 -- rpc_committed.rs:122-127
if pc = ⊥ and resp.rpcDecision present:                                  -- rpc_committed.rs:136-162
  cancelTypes(rpcDecision.cancel, [], "")
  for (T, v, o) in rpcDecision.moves: new(T, v, o, "__rpc/" ++ r)
if pc ≠ ⊥ and Tx⁻[f] = 0 (no other transactional RPC): record(pc)      -- rpc_committed.rs:225
emit ret(id, output);  release RL of id;  Tx[f] -= 1 if tx               -- rpc_invocation.rs:287-301
```

- RPC decisions cannot close the Flow or cancel siblings (`rpc_committed.rs:84-90`).
- `gcheck()` is **not** called after an RPC's cancellations (Open question 3).
- A transactional commit carries `expected_run_attempt`, which matters only
  with time travel (`rpc_invocation.rs:375-378`).

### 4.8 SubFlows and cross-Flow delivery

Outbox messages written by a commit move to the network `N`, then a separate
commit at the receiving Flow consumes them. Delivery is at least once with
deduplication. [assumption] Failure-free delivery is exactly once, after an
arbitrary delay, in any order
(`server-zd/crates/dexzd-engine/src/effects/outbox_delivery.rs:9-11`).

**[OUTBOX-SEND]** (engine) Label `τ`. Move a committed message from
`R(f).Ob` to `N` (`server-zd/crates/dexzd-engine/src/effects/outbox_delivery.rs:25-28`).
The receiver's commit later acknowledges it, and the sender stops redelivering
(`server-zd/proto/dexzd/v1/command.proto:461-464`).

**[DELIVER-CHILD-START]** (commit at child, `InboxMessage`) Label `τ`.
`server-zd/crates/dexzd-apply/src/cross_flow/inbox.rs:62-208`.

```text
f_c := child Flow ID
if no run of f_c exists:  start the child run like [FLOW-START] with parent = (f, e, i)
else if the reuse policy attaches (default: latest run is Running or Completed):   -- inbox.rs:123-140
  add parent to the child's attached parents
  if the child is closed: Ob_child += ChildCompleted(...) at once                 -- inbox.rs:210-239
else: start a new child run (ID reuse)
```

**[DELIVER-CHILD-DONE]** (commit at parent, `InboxMessage`) Label `τ` or
`closed`.
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

**[PENDING-CLOSE-CHECK]** (commit) Label `closed(...)`. When `pc ≠ ⊥` and
`Tx⁻[f] = 0`: `record(pc)`.
`server-zd/crates/dexzd-apply/src/command_handlers/flow_close.rs:234-273`.

While `pc ≠ ⊥`, the Flow status is still RUNNING, every Step is already
canceled, transactional RPCs are not admitted, and committing RPCs start no
Steps. Attribute Sync, which is out of scope, would add the same deferral.

## 5. Observable events and traces

### 5.1 Alphabet

The trace is stated over this alphabet `Obs`. Everything else is `τ`.

| Direction | Event | Produced by |
| --- | --- | --- |
| In | `start(f, T, v, A)` | [FLOW-START] |
| In | `pub(f, [(c, v)])` | [PUBLISH] |
| In | `set(f, [(k, v)])` | [SET-ATTRIBUTES] |
| In | `del(f, c, id)` | [DELETE-MESSAGE] |
| In | `skip(f, e, j)` | [SKIP-TIMER] |
| In | `stop(f, kind)` | [STOP] |
| In | `call(f, r, v, id)` | [RPC-CALL] |
| Out | `ret(id, output \| error)` | [RPC-RETURN-READONLY], [RPC-COMMIT], [RPC-SNAP] (ABORTED), [RPC-CALL] (rejection) |
| Out | `reply(f, error)` | a rejected client input |
| Out | `fx(f, src, a)` | [WORKER-EFFECT], [RPC-RUN]; `src` is `(e, m)` or `rpc:id` |
| Out | `closed(f, status, error, outs)` | `record` |

[decision] Reads are excluded. GetAttributes, GetFlowState, history, and
GetChannelMessages are not in `Obs`. Including them makes every intermediate
Attribute write observable, which is a far stronger theorem that the
translation would rarely satisfy. The closed result `outs` is observable,
including outputs collected before a ForceFail (Open question 15).

### 5.2 Traces and equivalence

A run of the system is a finite or infinite sequence of transitions from an
initial `Σ₀` with `R = ∅`. Its trace is the sequence of its labels with `τ`
erased.

[decision] The default equivalence is **weak trace equivalence over `Obs`,
untimed**: two systems are equivalent when their sets of finite traces are
equal, with the environment's inputs drawn from the same set. The alternatives
are weak bisimilarity (strictly stronger; it would distinguish where a choice
is made) and a fair-trace variant (needed for liveness statements). Both are
left for the Lean statement.

### 5.3 Timed variant

Make `tick(δ)` visible and stamp every label with `t`. Then a fired timer's
Execute and its effects carry `t ≥` fire time, and nothing else bounds them
from above. Open question 12 explains why a timed theorem needs L's sleep
durations in whole seconds.

## 6. Atomicity and interleaving

### 6.1 Atomic units

| Unit | Atomic? | Code |
| --- | --- | --- |
| One apply command (any rule marked commit) | Yes. All writes or none. | `command_handlers/mod.rs:90-300` |
| A Step method (Start, Run, Complete) | **No.** Reads come from the snapshot at Start. Writes, publications, and the decision commit at Complete. | `step_results.rs:156-209`, `step_results.rs:857-994` |
| The writes and decision of one Step method | Yes. One commit. | `step_results.rs:960-987` |
| An RPC with effects (Snap, Run, Commit) | **No.** Snapshot first, commit later. | `rpc_invocation.rs:202-298` |
| A read-only, non-transactional RPC | No commit at all. | `rpc_invocation.rs:272-286` |
| A Worker's external effects | Not part of any commit. They happen before the commit, and may happen with no commit. | Section 4.4 |
| Timers that fire together | One command for a prefix of the due timers of one run. | `timer_service.rs:116-205` |
| A SubFlow start, and a SubFlow completion report | Each is two commits (sender's outbox, receiver's inbox) and a delivery between them. | `outbox.rs:37-171`, `inbox.rs:62-382` |

The docs say "Each Step method commits atomically"
(`docs/content/concepts/flow/index.mdx:30`) and "Dex commits its Attribute
writes and Channel publications together with its result"
(`docs/content/concepts/step/index.mdx`, Guarantees). Both are true of the
writes. Neither makes the read-modify-write atomic (Open question 1).

### 6.2 Order of commits

- Commands of one run are totally ordered by the partition log. Apply sees
  them in that order and every command reads the state its predecessors wrote.
- Commands of different Flows are ordered only through cross-Flow delivery.
  [assumption] Different Flows interleave arbitrarily.
- Within one commit the order is fixed by the meta-functions. The key orders
  are:
  - Execute: data effects, then `wake`, then finish of the deciding Step, then
    cancellations, then the close decision, then new Steps, then `gcheck`.
  - WaitFor: data effects, then `wake` of other waiters, then registration and
    one evaluation of the new wait.
  - RPC: writes, deletions, publications, `wake`, cancellations, new Steps.

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
5. A canceled Step's Worker can still emit effects after the cancel commits.

No fairness is built into the rules. The dispatcher may delay a startable
invocation, the timer service may delay a due timer, and delivery may delay a
message, each without bound. [assumption] A liveness theorem needs weak
fairness for [STEP-START], [WORKER-EFFECT], the Complete rules,
[TIMER-FIRE], [OUTBOX-SEND], and the delivery rules.

## 7. Open semantic questions

Each item gives what the code does, why it matters for the translation
theorem, and the stance this document takes. Items 1 to 13 cover the topics
the task named; the rest were found on the way.

1. **A Step method is not a read-modify-write transaction.**
   - Code: the snapshot is computed at Start, after locks
     (`attempt_snapshot.rs:9-10`, `step_results.rs:188-195`). Writes are
     applied blindly at Complete (`flow_data.rs:188-230`). Nothing checks
     that the read values are still current.
   - Docs: "Each Step method commits atomically"
     (`docs/content/concepts/flow/index.mdx:30`). True for the writes only.
   - Matters: an L assignment `x := x + 1` in two threads, translated to two
     Executes without locks, loses an update. The translation must lock every
     shared key a step reads and writes, or use a Channel-token mutex.
   - Stance: three transitions per method (Section 4.4).

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
   - Matters: joining threads by "the last thread to finish completes the
     program" works only if `gcheck` runs after every way a Step can end. See
     item 3.
   - Stance: as coded.

3. **Canceling Steps from an RPC skips the graceful-completion check.**
   - Code: `apply_rpc_committed` calls `cancel_steps_by_type`
     (`rpc_committed.rs:139-147`) and never calls `complete_gracefully_if_ready`.
     The only callers of `gcheck` are `step_decisions.rs:150`,
     `wait_conditions/mod.rs:187`, `wait_conditions/mod.rs:244`, and
     `timer_skip.rs:50`. So when GC was requested and an RPC cancels the last
     active Step, the Flow stays RUNNING until another `gcheck` caller runs,
     which may be never.
   - Docs: "GracefulComplete completes the Flow once no other Step is active"
     (`docs/content/concepts/step/index.mdx`, StepDecision section).
     Disagreement. No apply test covers this case (searched
     `server-zd/crates/dexzd-apply/tests/` for graceful tests).
   - Matters: an L `cancel(t)` delivered by RPC, when `t` is the last running
     thread, must still terminate the program.
   - Stance: the rules follow the code. The translation must not cancel the
     last Step from an RPC after GC, or must follow it with a Step that runs
     `gcheck`. The owner should decide whether this is a bug.

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
   - Matters: not FIFO-fair. A ForN(3) waiter can starve while later ForOne
     waiters take every message. A mutex built on a Channel token is
     mutual-exclusive but not first-come-first-served.
   - Stance: as coded; L's mutex and channel semantics must allow unfair
     wake-up, or the translation must add its own queue.

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
     `docs/reference/go/flows-and-steps/waits.mdx`).
   - Matters: L's `recv(ch)` is ForOne. A batch receive maps to ForN.
   - Stance: as coded.

6. **Consumed messages are lost when the consuming Step ends without Execute
   committing.**
   - Code: `finish` drops the Step's condition results and only decrements
     `K` (`wait_conditions/mod.rs:250-281`). `close` cancels Steps with
     "consumed messages discarded" (`flow_close.rs:172-174`).
   - Matters: L's `select` plus cancellation: if a thread receives a message
     and is then canceled before it acts on it, Dex drops the message. L
     semantics where a received message is already "owned" by the receiver
     agree; L semantics with rollback do not.
   - Stance: as coded.

7. **A Step's own publications are delivered before its decision runs.**
   - Code: `effects` ends with `wake` (`flow_data.rs:664-669`) before
     `decide` (`step_results.rs:960-987`). So "publish to `c` and cancel type
     `B`" can let a `B` Step consume the message and then be canceled in the
     same commit; the message is lost (item 6). "Publish and ForceComplete"
     does the same.
   - Matters: the translation of "send then cancel" or "send then exit".
   - Stance: as coded. The translation should not rely on a message reaching
     a Step it cancels in the same decision.

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
     Attribute lock. An L lock request from an RPC can only "try-lock".
   - Stance: as coded; Section 8 uses a Channel token for L mutexes.

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
   - Matters: L `cancel(t)` targets one thread. Cancel-by-type cancels every
     thread whose current Step has that type.
   - Stance: as coded. Section 8 prefers a cooperative flag.

10. **DeadEnd and runs with no active Step.**
    - Code: DE takes no moves and closes nothing (`step_decisions.rs:120`). A
      run with no active Step and no GC request stays RUNNING forever, also
      when it started without a start Step (`flow_start.rs:204-205`). Docs
      agree (`docs/reference/go/flows-and-steps/step-decisions.mdx`, "A Flow
      can have no active Steps and still keep running").
    - Matters: L program termination must map to an explicit FC or GC. A
      thread that ends maps to DE.
    - Stance: as coded.

11. **RPC vs Step ordering.**
    - Code: an RPC reads a snapshot of committed state (Section 4.7) and
      commits later; any commit can come between. A read-only, non-transactional
      RPC never commits and leaves no history. An RPC whose run closed in
      between ran its handler, and its commit is rejected (`rpc_committed.rs:75-79`).
      During a pending close an RPC commit still writes Attributes and
      publishes, but starts no Steps (`rpc_committed.rs:133-138`). Step
      decisions from RPCs cannot close the Flow.
    - Matters: an L external call that reads and updates shared state must be
      a locking RPC to be atomic, and then can fail with ABORTED, which L has
      no counterpart for unless L's call may fail.
    - Stance: as coded. [decision] Treat ABORTED as an observable outcome; the
      translation of an L entry point retries it, which is invisible only if
      `ret(ABORTED)` is hidden. The owner must decide whether the L side
      observes it.

12. **Timer firing order.**
    - Code: due timers of one run fire in one command, in `(fire time, encoded
      TimerId bytes)` order (`row_keys.rs:303-314`, `timer_service.rs:116-205`).
      The fire time is the WaitFor commit time plus whole seconds
      (`wait_conditions/mod.rs:89-98`). [assumption, from protobuf encoding]
      For two wait timers with equal fire times, the bytes compare first by
      the encoded length of the `WaitConditionTimerId`, then by the Step
      execution ID. That length depends on both the ID length and the timer
      index: index 0 is omitted in proto3, and indexes 1 to 127 add two
      bytes. So `T-9` sorts before `T-10`, and with IDs of equal length an
      index-0 timer sorts before another Step's index-1 timer whatever the ID
      bytes. A wait timer sorts before a Flow timeout timer of the same
      attempt (field 2 vs field 4). A zero-second timer still needs a
      [TIMER-FIRE].
    - Firing is late by an unbounded amount, so a due timer can lose an AnyOf
      race to a later message (Section 6.3).
    - In one evaluation a timer and a message cannot both become ready,
      because apply evaluates on every change (`condition_plan.rs:217-218`).
    - SDK: "positive fractions round up to whole seconds"
      (`sdk-go/dex/condition.go:75`).
    - Matters: L `sleep(d)` with `d` not a whole number of seconds sleeps at
      least `⌈d⌉` in Dex. Untimed traces are unaffected. A timed theorem needs
      whole-second durations in L.
    - Stance: untimed by default (Section 5.2).

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
      Flow ID is fresh. Structured concurrency needs explicit child
      cancellation, which the core does not provide from the parent.
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
    - Matters: the `closed` label's `outs` component. L must say what a failed
      program outputs.
    - Stance: as coded.

16. **A natural close can be deferred by transactional RPCs.**
    - Code: with `Tx⁻[f] > 0`, `close` cancels every Step but leaves the status
      RUNNING with `pc` set; the close is recorded when the last such RPC
      commits or a PendingCloseCheck runs (`flow_close.rs:178-197`,
      `flow_close.rs:234-273`). StopFlow does not wait.
    - Matters: between the decision and the `closed` label, a committing RPC
      can still write Attributes and publish. Observable through `ret`.
    - Stance: modeled ([PENDING-CLOSE-CHECK]).

17. **Dispatch order and fairness.**
    - Code: the engine may start any startable pending invocation; the
      DispatchQueue orders waiting attempts by pending time
      (`server-zd/crates/dexzd-apply/src/steps/dispatch_queue.rs:33-75`), but
      Worker polling decides when.
    - Matters: liveness of the translation, not safety.
    - Stance: nondeterministic, no fairness in the rules (Section 6.3).

18. **Worker effects can happen with no commit.**
    - Code: a canceled Step's or a rejected RPC's handler may have emitted
      effects; its result is dropped (`step_results.rs:863-872`,
      `rpc_committed.rs:75-79`).
    - Matters: trace equivalence must allow `fx` events whose state changes
      never commit, or the translation must emit external effects only from
      code whose commit cannot be preempted.
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
    - Stance: as coded. Docs say nothing.

## 8. Sketch: mapping L onto the core

Not a proof. Each line names the open questions the mapping depends on.

L is assumed to have: threads with local variables, shared variables with
sequentially consistent single accesses, `seq`, `if`, `while`, procedure
calls with recursion, `fork`/`join`, mutexes, condition variables,
`sleep(d)`, `select { recv(ch) → s | after(d) → s }`, `recv(ch)` on external
channels, `emit(a)` external effects, `throw`/`try`, and `cancel(thread)`.

**General scheme.** Compile each L thread to continuation-passing Steps.

- A program point is a Step type. Thread-local variables and the call stack
  are the Step input, passed by GoTo.
- Shared variables are Attributes.
- One L atomic action that touches shared state is one Execute, with Execute
  locks on every key it reads or writes. The lock keys are per Step movement,
  so they can depend on runtime data. Depends on 1 and 8.
- Purely local code between two shared accesses runs inside one Execute.

| L construct | Dex mapping | Depends on |
| --- | --- | --- |
| Sequence, `if` | Code inside one Execute, or GoTo between blocks. | — |
| `while` | A Step that GoTos itself or the loop exit. | — |
| Call and recursion | (a) Inline: push the return point on an explicit stack in the Step input. No SubFlow needed. (b) SubFlow per call: the child result returns via the wait. (a) is preferred. | (b): 13 |
| `fork` | GoToMany: one move for the parent's continuation, one for the child's first Step. | — |
| `join(t)` | `t`'s last Step publishes to a per-thread Channel instance; the joiner waits ForOne on it. | 4, 6 |
| Program end | `main`'s last Step decides FC (ends at once) or GC (ends when every thread has ended). Other ended threads decide DE. | 2, 3, 10, 15 |
| Mutex `lock(m)` / `unlock(m)` | A Channel `m` holding one token. Lock = WaitFor `Until(m.ForOne())`; unlock = publish a token. Not FIFO-fair. Attribute locks cannot span a wait. | 4, 6, 8 |
| Condition variable `wait(cv, m)` | Under the mutex, increment an Attribute `waiters[cv]` and publish `m`'s token in one Execute; GoTo a Step whose WaitFor waits on `cv`; then re-lock `m`. `signal` publishes to `cv` only when `waiters[cv] > 0`, and decrements it. Channels keep messages, so a signal between unlock and wait registration is not lost. | 1, 4 |
| `sleep(d)` | WaitFor `Until(Timer(⌈d⌉))`. | 12 |
| `select { recv(ch) → s₁ \| after(d) → s₂ }` | WaitFor `AnyOf(ch.ForOne(), Timer(d))`. Only the winner consumes. Execute branches on the condition results. | 5, 12 |
| External `recv(ch)` | A Channel that only the client publishes to (`pub`). | 4 |
| `emit(a)` | An external effect in an Execute. Emit only after the thread can no longer be canceled before its commit, or accept effects without commits. | 9, 18 |
| `throw` / `try` | The handler stack lives in the Step input. `throw` is a GoTo to the innermost handler's Step. No Worker errors. | 19 |
| `cancel(t)` | Preferred: a cooperative flag Attribute `cancelled[t]` plus a wake-up message on `t`'s wait Channel; every Step of `t` checks the flag. Alternative: CancelSteps by per-thread Step types; loses claimed messages and leaves child SubFlows running. | 3, 6, 7, 9 |
| An external entry point that updates state (L handler called from outside) | A locking RPC. ABORTED must be visible or retried. | 8, 11 |

Constructs whose mapping depends on an open question: join and program end
(2, 3), mutex fairness (4), `select` with cancel (6, 7), cancellation (3, 9),
recursion through SubFlows (13), timed `sleep` (12), external effects (18),
and RPC entry points (11).

## 9. Future work

- `server-zd/crates/dexzd-simulation` runs deterministic simulation tests of
  the engine against simulated object storage, clocks, Workers, and crashes;
  it is relevant to the later crash-transparency proposition.
- The Lean statement needs the equivalence choice of Section 5.2 and the
  fairness assumptions of Section 6.3.

## Appendix A. Assumptions and decisions in one list

1. User code is a total function with output-only external effects (2.2).
2. Durability does not change failure-free behavior (1.3).
3. One run per Flow ID; fresh Flow IDs (1.3, 3.4).
4. Resource limits are never hit (1.3).
5. One global clock; `t` strictly increases between commits of one run (4.1).
6. Dispatch order is nondeterministic; no fairness in the rules (4.4, 6.3).
7. [TIMER-FIRE] fires a non-empty prefix of the due timers (4.5).
8. Fusing RPC lock acquisition and snapshot preserves traces (4.7).
9. Cross-Flow delivery is exactly once, in any order, after any delay (4.8).
10. Different Flows interleave arbitrarily (6.2).
11. Equal `sws` among waiters of one Channel does not occur without time
    travel (Open question 4).
12. TimerId byte order as derived from protobuf encoding (Open question 12).
13. Reads are not observable; the equivalence is weak, untimed trace
    equivalence (5.1, 5.2).
14. ABORTED is an observable RPC outcome (Open question 11).
15. L exceptions are data, never Worker errors (Open question 19).
16. The SDKs fill per-movement Step options from the Step definition (2.1).
17. A liveness theorem needs weak fairness for Step start, Worker progress,
    Step completion, timer firing, and cross-Flow delivery (6.3).
