# Dex expressiveness theorem in Lean 4

This directory holds a Lean 4 development of one theorem:

> Every well-formed program of a small concurrent imperative language **L**
> translates into a Dex Flow that is trace-equivalent to it, assuming no
> failures.

The semantics it formalizes is
[`formal/semantics/dex-core-semantics.md`](../semantics/dex-core-semantics.md),
revision 5. "Spec" below means that document.

## Status: Stage 2a in progress, stopped on an open issue

Stage 2a proves the safety theorem. Eight of the twelve `sorry` of Stage 1
are proved, and `safety_of_isWellFormed` depends on exactly two: the step
simulations. Work on those found that a program could forge a mutex handle;
the owner decided on 2026-11-10 that handles are unforgeable. See
[Decided in Stage 2a](#decided-in-stage-2a-handles-are-unforgeable). The same
forged handle can still arrive through the program input or an external
message. See
[Open issue found in Stage 2a](#open-issue-found-in-stage-2a-forged-handles-through-the-input).

### Stage 1, revised after the first review

Stage 1 is the model and the statements. The two main theorems are stated and
reduced to four inclusion lemmas. Those lemmas, and eight more that the proof
plan needs, end in `sorry`. Stage 2 is the proofs. It starts after the owner
and a reviewer confirm that the statements match the spec.

The first review (of commit `cc123891a`) returned "fix before Stage 2". This
revision applies the owner decisions of 2026-11-09 (spec Review log, L1 to L4)
and the review's two model findings (R4-1, R4-2). See
[Points for the owner](#points-for-the-owner) for the status of each point.

`lake build` succeeds. Every `sorry` is in `DexExpressiveness/Simulation.lean`
and is listed in [The `sorry` inventory](#the-sorry-inventory).

## Build

```bash
# Once: install elan, the Lean toolchain manager, into ~/.elan (no sudo).
curl -sSf https://raw.githubusercontent.com/leanprover/elan/master/elan-init.sh \
  | sh -s -- -y --default-toolchain none --no-modify-path
export PATH="$HOME/.elan/bin:$PATH"

cd formal/lean
lake build
```

The first build downloads the pinned toolchain. A clean build takes about 30
seconds, of which about 10 are the lockstep checks.

| Tool | Version |
| --- | --- |
| Lean | 4.35.0 (`leanprover/lean4:v4.35.0`, commit `eb6cafb7290e`), pinned in `lean-toolchain` |
| Lake | 5.0.0 (ships with Lean 4.35.0) |
| elan | 4.2.4 |
| Dependencies | none: Lean core only, no Mathlib, no Batteries |

Build output goes to `formal/lean/.lake/`, which `.gitignore` and
`.dockerignore` exclude. `.lean` files are outside the license-header policy:
`formal/` is not an included prefix in `script/licenseheaders/policy.json`,
and `make copyright-check` passes.

## Layout

| File | Content |
| --- | --- |
| `DexExpressiveness/Core/Assoc.lean` | Association-list maps, stable sort. |
| `DexExpressiveness/Core/TransitionSystem.lean` | Runs, finite traces, fair maximal traces, markers. Shared by both sides. |
| `DexExpressiveness/Dex/Syntax.lean` | Dex abstract syntax (spec §2). |
| `DexExpressiveness/Dex/Config.lean` | Dex configurations (spec §3). |
| `DexExpressiveness/Dex/MetaFunctions.lean` | The meta-functions of one commit (spec §4.2). |
| `DexExpressiveness/Dex/Rules.lean` | The event alphabet and the transition rules (spec §4.3 to §4.6, §5.1). |
| `DexExpressiveness/Dex/Observation.lean` | The Dex system, `E_adm`, fairness F1 to F6 (spec §5.2 to §5.5). |
| `DexExpressiveness/Dex/RunLemmas.lean` | Proved: lookup after update, sorting, and the shape of `eval`. |
| `DexExpressiveness/Dex/WaitLemmas.lean` | Proved: `wake` serves the earliest waiter; AnyOf and timers at registration. |
| `DexExpressiveness/Dex/RunInvariant.lean` | Proved: the run invariant, kept by every meta-function of spec §4.2. |
| `DexExpressiveness/Invariants.lean` | Proved: the invariant of every reachable configuration of a translated Flow, and the three invariants of spec §7 item 4 and Appendix A, items 11 and 26. |
| `DexExpressiveness/L/Syntax.lean` | L syntax and its event alphabet (spec §8.1). |
| `DexExpressiveness/L/ThreadLocal.lean` | What one statement does to its own thread, and the atomic action it asks for. |
| `DexExpressiveness/L/Semantics.lean` | L small-step semantics, L fairness, `Program.IsWellFormed` (spec §8.1). |
| `DexExpressiveness/Translation/Flow.lean` | The translation `⟦·⟧` (spec §8.2, §8.3). |
| `DexExpressiveness/Translation/Projection.lean` | The projection `π`, the request encoding, `E_adm` of a translated program (spec §5.3). |
| `DexExpressiveness/TraceSets.lean` | The four trace sets the theorem compares. |
| `DexExpressiveness/Theorems.lean` | **The main statements**: `safety_of_isWellFormed`, `liveness_of_isWellFormed`, `expressiveness_of_isWellFormed`. |
| `DexExpressiveness/Simulation.lean` | The candidate simulation relation, the two step simulations, and the two liveness inclusions. Every `sorry` is here. |
| `DexExpressiveness/FiniteTraces.lean` | Proved from the step simulations: the two finite-trace inclusions that give safety. |
| `DexExpressiveness/Properties.lean` | Proved facts. No `sorry`. |
| `DexExpressiveness/Examples/*.lean` | Concrete runs checked in the Lean kernel, and lockstep checks. No `sorry`. |

A reviewer who checks the statements against the spec reads, in order:
`Core/TransitionSystem.lean`, the five `Dex/` files, the three `L/` files, the
two `Translation/` files, `TraceSets.lean`, and `Theorems.lean`.

## The two main statements

From `DexExpressiveness/Theorems.lean`:

```lean
theorem safety_of_isWellFormed (program : Program) (isWellFormed : program.IsWellFormed)
    (trace : List Event) :
    IsProjectedDexFiniteTrace program trace ↔ IsLFiniteTrace program trace

theorem liveness_of_isWellFormed (program : Program) (isWellFormed : program.IsWellFormed)
    (word : Sequence L.Request) (isAdmissible : L.IsAdmissibleWord word)
    (trace : Sequence Event) (marker : Marker) :
    IsProjectedDexFairMaximalTrace program word trace marker ↔
      IsLFairMaximalTrace program word trace marker
```

The scope, from `DexExpressiveness/L/Semantics.lean`:

```lean
def Thread.BreaksMutexRequirement (config : Config) (thread : Thread) : Prop :=
  thread.phase = .ready ∧ thread.timer = none ∧
  ∃ mutex, requiredMutex thread.core thread.code = some mutex ∧
    config.mutexOwners.get? mutex ≠ some (some thread.core.tid)

def BreaksMutexRequirement (config : Config) : Prop :=
  ∃ thread, thread ∈ config.threads ∧ thread.BreaksMutexRequirement config

def Program.IsWellFormed (program : Program) : Prop :=
  ∀ config, (system program).Reachable config → ¬ BreaksMutexRequirement config
```

`requiredMutex` (`L/ThreadLocal.lean`) reads the requirement off the head
statement: `m` for `unlock(m)`, and `cv`'s mutex for `wait(cv, m)`,
`signal(cv)`, and `broadcast(cv)`.

with, from `DexExpressiveness/TraceSets.lean`:

```lean
def IsProjectedDexFiniteTrace (program : Program) (trace : List Event) : Prop :=
  (Dex.system ⟦program⟧).IsFiniteTrace environment.IsAdmissiblePrefix project trace

def IsLFiniteTrace (program : Program) (trace : List Event) : Prop :=
  (L.system program).IsFiniteTrace (fun _ => True) some trace

def IsProjectedDexFairMaximalTrace (program : Program) (word : Sequence L.Request)
    (trace : Sequence Event) (marker : Marker) : Prop :=
  (Dex.system ⟦program⟧).IsFairMaximalTrace Dex.IsFair project
    (word.map encodeRequest) trace marker

def IsLFairMaximalTrace (program : Program) (word : Sequence L.Request)
    (trace : Sequence Event) (marker : Marker) : Prop :=
  (L.system program).IsFairMaximalTrace L.IsFair some word trace marker
```

How they read against the spec:

- A set equality `A = B` of the spec is written `∀ t, t ∈ A ↔ t ∈ B`. Lean
  core has no set type.
- `safety_of_isWellFormed` is spec §5.4: `π(FinTr_{E_adm}(⟦S⟧)) = FinTr(S)`.
- `liveness_of_isWellFormed` is spec §5.5:
  `∀ w ∈ E_adm. π(MaxTr^fair_w(⟦S⟧)) = MaxTr^fair_w(S)`.
  The word is quantified in L form and encoded for the Dex side.
  `isAdmissibleWord_encode` and `isAdmissibleWord_decode` (proved) show that
  the encoded admissible L words are exactly the words of `E_adm`.
- A trace is a `Sequence Event`, finite or infinite. A marker is `terminated`
  (`✓`), `deadlocked` (`δ`), or `divergent` (`∞`).
- Both statements range over well-formed programs (spec §0, §5.4, §5.5,
  §8.1): `program.IsWellFormed`. It is a property of the L program under L's
  semantics, quantified over every input and schedule, like data-race
  freedom. The translation plays no part in it. The theorem names carry the
  scope.
- `Program` is built from source statements only. The runtime instruction
  `park` is not a `Stmt`, so no program contains it.

## Spec section to Lean

| Spec | Lean file | Definitions |
| --- | --- | --- |
| §0 Notation `M[k ↦ v]`, `⊥` | `Core/Assoc.lean` | `AssocList.get?`, `set`, `erase` |
| §2.1 Programs, `Opts` | `Dex/Syntax.lean` | `Signature` (with `MapSelection`, `isMapInstanceKey`, `isSelectedBy`), `FlowDef`, `StepDef`, `StepOptions` (`skipWaitFor`, `loadMaps`) |
| §2.2 User code | `Dex/Syntax.lean` | `WaitForFn`, `ExecuteFn`, `Context`, `Snapshot`, `Effects`, `WaitForResponse`, `ExecuteResponse` |
| §2.3 Waits | `Dex/Syntax.lean` | `Wait`, `WaitKind`, `Wait.empty` |
| §2.4 Decisions | `Dex/Syntax.lean` | `Decision`, `Move`, `Close` |
| §2.5 Well-formedness | `Dex/Syntax.lean`, `Properties.lean` | `Decision.IsWellFormed`; proved for `⟦S⟧` in `translate_execute_isWellFormed` |
| §3.1 Step executions | `Dex/Syntax.lean`, `Dex/Config.lean` | `StepExecId`, `Phase`, `Invocation`, `WaitState`, `StepRecord`, `ConditionResults` |
| §3.2 One run | `Dex/Config.lean` | `Run`, `RunStatus`, `CloseStatus`, `TimerId` |
| §3.3 Engine state | `Dex/Config.lean` | `Task`, `TaskPhase` (`Θ` only) |
| §3.4 System | `Dex/Config.lean` | `Config`, `Config.initial` |
| §4.1 Shape of the rules | `Dex/Rules.lean` | `step`, `Action`, `Config.commit`, `Config.canCommit` |
| §4.2 Meta-functions | `Dex/MetaFunctions.lean` | `Run.newStep`, `plan`, `planAnyChannel`, `consumeMessages`, `evalStep`, `waitersOn`, `wake`, `applyEffects`, `registerWait`, `finishStep`, `collectOutput`, `closeRun`, `applyDecision`, `isLoadedKey`, `snapshot`; `record`'s event is `Config.closeEvents` |
| §4.3 [FLOW-START] | `Dex/Rules.lean` | `step`, case `.request (.startFlow ..)` |
| §4.4 Step methods | `Dex/Rules.lean` | `step`, cases `.stepStart`, `.workerRun`, `.workerEffect`, `.workerReturn`, `.workerAbandon`, `.stepComplete`, `.stepCompleteStale`; `Task.compute` |
| §4.5 [TICK], [TIMER-FIRE] | `Dex/Rules.lean`, `Dex/MetaFunctions.lean` | `step`, cases `.tick`, `.timerFire`; `Run.dueTimers`, `Run.fireTimer`, `Run.isTimerEntryAtMost` |
| §4.6 [PUBLISH], rejection | `Dex/Rules.lean` | `step`, case `.request (.publish ..)` |
| §4.6 other inputs, §4.7, §4.8, §4.9 | not modeled | excluded by spec §5.3 and §8.2 |
| §5.1 Event alphabet | `Dex/Rules.lean` | `Obs` |
| §5.2 Traces | `Core/TransitionSystem.lean`, `Dex/Observation.lean` | `TransitionSystem.exec`, `IsRun`, `eventsAt`, `IsConcatenation`; `Dex.system` |
| §5.3 `E_adm` | `Dex/Rules.lean`, `Dex/Observation.lean`, `Translation/Projection.lean` | `Dex.Request`, `Dex.Environment`, `IsAdmissibleWord`, `IsAdmissiblePrefix`; `Translation.environment`, `encodeRequest`; `L.IsAdmissibleWord` |
| §5.3 Projection `π` | `Translation/Projection.lean` | `project` |
| §5.4 Safety | `Core/TransitionSystem.lean`, `TraceSets.lean`, `Theorems.lean` | `IsFiniteTrace`; `IsProjectedDexFiniteTrace`, `IsLFiniteTrace`; `safety_of_isWellFormed` |
| §5.5 Runs for `w`, markers | `Core/TransitionSystem.lean` | `IsRunFor`, `RequestsArrive` (F0), `HasFinitelyManyProgressSteps`, `Marker`, `HasMarker`, `IsFairMaximalTrace` |
| §5.5 Fairness, Dex | `Dex/Observation.lean` | `Fairness.TimeDiverges` (F1), `TimersFire` (F2), `StepsStart` (F3), `WorkersSettle` (F4), `ResultsCommit` (F5), `MessagesMove` (F6), `Dex.IsFair` |
| §5.5 Fairness, L | `L/Semantics.lean` | `Fairness.TimeDiverges` (F1), `TimersFire` (F2), `ThreadsBegin` (F3), `EmitsSettle` (F4), `ActionsComplete` (F5), `MessagesMove` (F6), `L.IsFair` |
| §5.5 Liveness | `TraceSets.lean`, `Theorems.lean` | `IsProjectedDexFairMaximalTrace`, `IsLFairMaximalTrace`; `liveness_of_isWellFormed` |
| §5.6 Timed variant | not modeled | the theorem is untimed |
| §6 Atomicity | `Dex/Rules.lean` | one commit is `Config.commit`; a Step method is `.stepStart`, the Worker actions, `.stepComplete` |
| §8.1 L | `L/Syntax.lean`, `L/ThreadLocal.lean`, `L/Semantics.lean` | see the next table |
| §8.2 General scheme | `Translation/Flow.lean` | `StepType`, `Chan`, `Key`, `DexVal`, `signature`, `startStepDef`, `pointStepDef`, `timedStepDef`, `translate` (`⟦·⟧`) |
| §8.3 Mapping table | `Translation/Flow.lean` | `encodeIntent` (one case per row), `encodeWaitRequest`, `decodeBlockedOutcome`, `decodeTimedOutcome` |

### Spec §8.1, bullet by bullet

| Spec §8.1 | Lean (`L/Semantics.lean` unless noted) |
| --- | --- |
| Constructs | `Stmt`, `Expr`, `Val`, `Program` (`L/Syntax.lean`); the runtime `Instr` (`L/ThreadLocal.lean`) |
| Handles and literals | `Literal`, `Expr.lit`, `Expr.staticMutex` (`L/Syntax.lean`); `evalExpr_mutex_origin`, `evalExpr_thread_origin` (`Properties.lean`) |
| `Obs_L` | `Event` (`L/Syntax.lean`) |
| Atomic actions | `Intent` and `completeStatement` (`L/ThreadLocal.lean`); `Config.applyIntent` |
| Mutex | `Config.registerWait` case `lock`; `Config.releaseMutex` |
| Condition variable | `applyIntent` cases `cvWait`, `signal`, `broadcast`; `Config.wakeCvWaiter`; the runtime instruction `Instr.park` |
| Well-formed programs | `requiredMutex` (`L/ThreadLocal.lean`); `Thread.BreaksMutexRequirement`, `BreaksMutexRequirement`, `Program.IsWellFormed` |
| Cancellation | `applyIntent` case `cancel`; `Config.cancelBlockedThread`; the check order in `Config.registerWait` |
| Emit | `Phase.emitting`, `Phase.emitted`; `Label.lateDeliver`, `Label.lateDrop`; `Config.closeProgram` |
| Exit | `applyIntent` cases `exit`, `fail`, `endThread`; `returnValue` (`L/ThreadLocal.lean`) |
| External input | `Config.deliverInput`; `step` case `.request (.input ..)` |
| Clock and timing model | `Config.clock`, `lastStampAt`, `canStamp`, `stamp`; `Label.tick`; the phases of `Phase` and `stepThread`, which take the stamped actions the spec tabulates |
| Timers | `Thread.timer` (Enter); `registerWait` (Arm); `Config.leastDueTimerThread`, `Config.fireTimers`, `Label.fire` |
| Join | `Config.joinable`; `registerWait` case `join`; `applyIntent` case `endThread` |
| `select` | `Stmt.select`; `timedChan`, `resumeTimed` (`L/ThreadLocal.lean`) |
| Halting | `Intent.halt`, `Phase.halted`; the `halted` branches of `completeStatement`, `resumeBlocked`, `resumeTimed` (`L/ThreadLocal.lean`) |

### Spec Appendix A, assumption by assumption

| # | Where it lives |
| --- | --- |
| 1 | Handlers are Lean functions: `WaitForFn`, `ExecuteFn`. |
| 2, 4 | Not modeled: no failures, no limits. |
| 3 | `Config.run : Option Run`; a start is enabled only when there is no run. |
| 5 | `Config.canCommit`, `Config.lastCommitAt`; one partition. |
| 6 | `Action.stepStart` may start any pending invocation. |
| 7 | `Action.timerFire count` fires a non-empty prefix of `Run.dueTimers`. |
| 8, 9, 10, 16, 21, 22 | Not applicable: no RPCs, no SubFlows, one Flow, untimed. |
| 11 | `Run.waitersOn` sorts by `sws` only. Lemma `registeredSeq_isInjective` (proved). |
| 12 | `Signature.isTimerIdAtMost`, a parameter of the model. |
| 13 | `Obs` has no read event. |
| 14 | `Theorems.lean`. |
| 15 | `Dex.Fairness`, `L.Fairness`, `TransitionSystem.RequestsArrive`. |
| 17 | `Dex.Request` has only `startFlow` and `publish`. |
| 18 | No request IDs. |
| 19 | `Frame.handler`; `completeStatement` cases `raise`, `tryCatch`. |
| 20 | `move.options.skipWaitFor` is set by the translation (`encodeIntent_moves_agree`, proved). The start Step reads it from its definition. |
| 23 | `L/Semantics.lean`. |
| 24 | `IsRunFor` counts every request label, accepted or rejected. |
| 25 | `DexVal.exitTag`, `Close.forceFail`; `project`. |
| 26 | `StepType.timed`; `signature.isTimerIdAtMost` orders by Step number. Lemma `armedTimers_areTimedSteps` (proved). |
| 27 | `StepExecId.number`, `Run.counters`, and `L.Config.timerCounter` are `Nat`. |
| 28 | `Program.IsWellFormed`; the hypothesis of every theorem. |
| 29 | The phases of `L.Phase` and `L.stepThread`; `Label.fire count`. The file comment of `L/Semantics.lean` repeats the table. |
| 30 | `Intent.halt`, `Phase.halted`. |
| 31 | `CvId.mutex`; `completeStatement` case `wait` halts on another mutex. |
| 32 | `resumeBlocked` and `resumeTimed` give `Intent.endThread` on `canceled`; `encodeIntent` case `endThread` publishes `join/t`. |
| 33 | `Config.joinable`; `Config.registerWait` case `join`. |
| 34 | `Config.applyIntent` case `fail` calls `Config.closeProgram`, as `exit` does. |
| 35 | `Translation.environment` (`IsProgramInput`, `IsExternalMessage`); `project`. |
| 36 | `Dex.step`, case `.request (.startFlow ..)`. |
| 37 | `Dex.Fairness.WorkersSettle`, `Dex.Fairness.ResultsCommit`. |
| 38 | `L.Fairness`, `L.IsFair`. |
| 39 | `Stmt.select`, `Stmt.recv`. |
| 40 | `Literal`, `Expr.staticMutex`; `evalExpr_mutex_origin`, `evalExpr_thread_origin`. |

## What the Dex model leaves out

The model has what the translation and `E_adm` use. Each omission is marked at
its definition.

| Left out | Why it does not matter for the theorem |
| --- | --- |
| RPCs, `RL`, `Rq`, `Tx`, pending close `pc` | Spec §5.3: no `call`. The #1450 decision does not arise. |
| StopFlow, SetAttributes, DeleteChannelMessage, SkipTimer | Spec §5.3: not in `E_adm`. |
| SubFlows, outbox, `SFR`, the network | Spec §8.2: the translation uses none. One Flow, one partition, one clock. |
| GracefulComplete, `gc`, `gcheck`; ForceCompleteIfChannelsEmpty; `K` | Spec §8.2. `gcheck()` is a no-op when `gc` is never set. |
| CancelSteps, CancelSiblingSteps | Spec §8.2. |
| Attribute locks, `L`, `pend`'s queue, `release` | Spec §8.2. Every pending invocation is startable. |
| Step execution locals | The translation passes all state in the Step input. |
| Channel bounds other than ForOne; AnyCombinationOf | Spec §8.3 uses ForOne, AllOf, and AnyOf only. |
| Message IDs and enqueue sequence numbers | They serve deletions and ordering; `Q[c]` is kept in commit order. |
| `Snap.sizes`, `Snap.loaded`, Channel load selections (`loadChans`) | The translation reads Attributes only. AttributeMap load selections (`loadMaps`) are modeled. |
| Canceled and Terminated close statuses | They need StopFlow or a Flow timeout. |
| The Flow ID in events | There is one Flow. |

## Modeling decisions

Each is the most faithful reading found. The ones that need the owner are
repeated in the next section.

1. **Rules are functions.** `step config action` is a partial function; the
   action carries the rule's choice. Determinism is then by construction.
2. **Finite maps are association lists**, so every model function evaluates
   in the Lean kernel and the examples are checked by `decide`.
3. **Names are structured, not strings.** A Step execution ID is a Step type
   and a number. The byte order of TimerIds is the parameter
   `Signature.isTimerIdAtMost`.
4. **The translation is one Step per statement** (spec §8.2). A program
   point is the code left in the current procedure (`StepType.point code`).
   The Step input is the thread ID, a counter, the locals, and the stack.
5. **L is defined in two layers.** `L/ThreadLocal.lean` says what a statement
   does to its own thread and which atomic action it asks for (`Intent`).
   `L/Semantics.lean` performs that action on the shared state. The
   translation encodes the same `Intent` as effects and a decision, so
   `encodeIntent` is spec §8.3's table.
6. **`sleep` and `select` get their own program-point Step** whose Execute
   only GoTos `Timed`. So Enter is a commit of its own, as spec §8.1 requires
   ("other actions may come between Enter and Arm").
7. **The waiter ID of a condition-variable wait is the thread ID.** A thread
   waits on one condition variable at a time.
8. **Thread IDs and created mutex IDs are paths.** A child's ID is the
   parent's ID plus the parent's counter (spec §8.3 `fork`). L uses the same
   naming, because a thread handle is a value and values can be emitted.
9. **Dex null is `Option.none`.** Writing L's null to a shared variable
   deletes the Attribute; an absent Attribute reads null.
10. **`[WORKER-EFFECT]` is three actions**: run the handler (`workerRun`),
    emit one effect (`workerEffect`), return (`workerReturn`). The first and
    last produce no event.
11. **Source and runtime syntax are separate types.** `Stmt` has the
    constructs of spec §8.1 only. The code a thread has left is a list of
    `Instr`: a source statement, or the runtime `park` and `relock` that
    `wait(cv, m)` leaves behind.
12. **The waiter list is loaded by an exact selection.** `Key.cvWaiters cv`
    is an AttributeMap instance. `moveToPoint` puts `waiters/cv` in the
    Execute load selection of the Step of a `wait`, `signal`, or `broadcast`
    (`executeLoadMaps`). `readWaiters_snapshot_of_cvOperand` proves that this
    Step's snapshot then holds the waiter list.

## Points for the owner

Every point is decided or closed, and every one is in the spec. The owner
decisions are dated 2026-11-09 and are in the spec's Review log (revision 5,
rows L1 to L4). "Kept as taken" means that the first review asked for no
change and the owner accepted the choice by not objecting; the spec's Review
log lists each as row K3 to K15, and the spec section is given below.

1. **Clock and action granularity. Decided [owner], spec row L1.**
   - The point: spec revision 4 said L's clock "strictly increases between
     any two L actions" without fixing how many actions a statement takes. A
     deadline is the Arm timestamp plus whole seconds, so two runs can carry
     the same deadlines only if the same number of stamped steps lies between
     any two Arms. One [TIMER-FIRE] commit of `k` timers also needed `k`
     clock values in L.
   - Decision: L has a fixed timing model, aligned with the Dex commits of
     the translation, and a batched `fire` shares one stamp. Spec §8.1
     "Clock and timing model" tabulates the counts; spec §8.2 fixes one Step
     per statement.
   - Rationale [owner]: real programs cannot rely on timing races, so any
     fixed timing model leaves the meaning of correct programs unchanged.
     Choosing the model aligned with Dex makes the equivalence exact without
     narrowing what the theorem covers in practice. It is like counting one
     time unit per instruction in algorithm analysis.
   - In Lean: unchanged from the first submission. The lockstep checks
     confirm that the stamps of both sides agree after every step.
2. **Mutex requirements. Decided [owner], spec row L2.**
   - Decision: the theorems cover well-formed programs only. Calling `wait`,
     `signal`, or `broadcast` without holding the mutex, or `unlock` on a
     mutex the thread does not hold, is outside the domain, as a data race is
     outside a theorem about data-race-free programs.
   - In Lean: `Program.IsSafe` is now `Program.IsWellFormed`. It is defined
     from `requiredMutex`, which reads the requirement off the head
     statement, and `requiredMutex_eq_of_intent` (proved) ties it to the
     atomic action the statement asks for. The theorems are named
     `..._of_isWellFormed`.
3. **Which mutex `signal(cv)` requires. Kept as taken, spec row K3.** A
   `CvId` holds its mutex. `wait(cv, m)` with another mutex is a type error.
   Spec §8.1 "Condition variable".
4. **A canceled thread is joinable. Kept as taken, spec row K4.** It
   publishes `join/t`. Spec §8.1 "Cancellation", §8.3 `cancel` row.
5. **A second join of a thread blocks. Kept as taken, spec row K5.** The
   first join takes the thread's one join token. Spec §8.1 "Join".
6. **Uncaught exceptions. Decided [owner], spec row L4.** A string value `s`
   fails the program with `fail(s)`, in any thread. Any other value halts the
   thread. Spec §8.1 "Halting".
7. **Dynamic type errors. Decided [owner], spec row L4.** The thread halts.
   It takes the statement's first 2 or 4 actions, never ends, and is never
   joinable. Spec §8.1 "Halting".
8. **`fail` and late emits. Kept as taken, spec row K8.** As after `exit`.
   Spec §8.1 "Exit".
9. **Values of `E_adm`. Kept as taken, spec row K9.** The start input and
   every external message are encoded L values (`DexVal.value`). Spec §5.3.
10. **`waiters/cv` and the snapshot. Fixed, spec row R4-1.** The first
    submission loaded every Attribute, which spec §4.2 does not do. The model
    now applies `loadMaps` as spec §4.2 states it, and the translation
    declares the exact instance `waiters/cv` in the Execute options of the
    Steps that read it. Spec §8.2 and §8.3 say so.
11. **Options of the start Step. Kept as taken, spec row K11.**
    `skipWaitFor` is read from the Step definition; the start Step selects no
    AttributeMap. Spec §4.3.
12. **A rejected input takes no stamp. Decided with point 1.** The timing
    table of spec §8.1 gives `refused(c, v)` zero stamped actions.
13. **`F4`, `F5`, and membership in `Θ`. Kept as taken, spec row K13.** A
    task stays in `Θ` until its result commits or it is dropped. Spec §5.5.
14. **L's fairness. Kept as taken, spec row K14.** `L.Fairness`, the L forms
    of F1 to F6. Spec §5.5.
15. **`select` without `after`. Kept as taken, spec row K15.** It is `recv`
    followed by the branch; `select` has one `recv` branch. Spec §8.1
    "`select`".
16. **The runtime `park`. Fixed, spec row R4-2.** `park` is no longer a
    `Stmt`. Source statements and runtime instructions are separate types.
17. **Unbounded counters. Decided [owner], spec row L3.** Step numbers and
    timer numbers are unbounded naturals in the model. The real limit is
    fewer than 2³² Step executions of one type per run; the numeric timer-ID
    order is exact within it. Spec §8.2; Appendix A, item 27.

## What is proved

All of this is free of `sorry`. `#print axioms` shows only `propext` and
`Quot.sound`; no `native_decide`.

- `translate_execute_isWellFormed`: every Execute of `⟦S⟧` returns a
  well-formed decision (spec §2.5), for every input.
- `encodeIntent_moves_agree`, `moveToPoint_agrees`, `entryStep_hasNoWaitFor`:
  every move's `skipWaitFor` agrees with its target Step definition.
- `readShared_snapshot`, `readWaiters_snapshot_of_cvOperand`: the load
  selections the translation declares are enough. Every snapshot holds every
  shared variable, and the Execute snapshot of a `wait`, `signal`, or
  `broadcast` Step holds `waiters/cv`.
- `requiredMutex_eq_of_intent`: the mutex requirement is on exactly the
  statements that ask for an `unlock`, a condition-variable wait, a signal, or
  a broadcast.
- `step_deterministic`, `exec_deterministic`: a label has one outcome; a
  schedule has one run.
- `dex_tick_enabled`, `l_tick_enabled`, `tick_isNotProgress`.
- `project_start`, `project_pub`, `project_rejectedPub`, `project_fx`,
  `project_exit`, `project_fail`: the rows of the `π` table.
- `isAdmissibleWord_encode`, `isAdmissibleWord_decode`: the admissible words
  of `E_adm` are exactly the encoded admissible L words.
- `TransitionSystem.requestsArrive_of_isRunFor`: F0 follows from "run for `w`".
- `related_initial`: the candidate relation holds initially.
- `safety_of_isWellFormed`, `liveness_of_isWellFormed`,
  `expressiveness_of_isWellFormed`: proved from the four inclusion lemmas,
  which are `sorry`.
- Examples, end to end on concrete traces, by kernel evaluation:
  - `Examples/Mutex.lean`: a two-thread mutex program. Both of its complete
    traces are finite traces of the L program and projected finite traces of
    its Flow (`bothTraces_onBothSides`).
  - `Examples/TimedSelect.lean`: `sleep`, `select` with a timeout, `recv`,
    `select` with a message, `cancel` of a sleeping thread, `join`, and the
    return of `main`, on both sides.
  - `Examples/TimerOrder.lean`: two timers with equal deadlines fire in
    timer-number order on both sides.

## Lockstep checks

`Examples/Lockstep.lean` runs an L program and its Flow side by side. It turns
each L label into the Dex actions meant to simulate it, and each Dex action
into the L labels meant to simulate it. After every step it checks that the L
events equal the projected Dex events and that the L clock stamp equals the
Dex commit timestamp.

`Examples/LockstepChecks.lean` runs this on pseudo-random schedules, in both
directions, for twelve programs that together use every L construct. A
mismatch fails the build. This is testing, not proof.

Three changes were planted during development to see whether the build
notices. Dropping the cancel of a blocked timed operation failed the
`TimedSelect` theorems. Dropping the timer-number tie-break passed the random
checks, because equal deadlines are rare under random ticks; `TimerOrder.lean`
was added for that case and fails under the change. Dropping the `waiters/cv`
load selection from the translation failed `readWaiters_snapshot_of_cvOperand`.

## The `sorry` inventory

There are 4, all in `DexExpressiveness/Simulation.lean`. The numbers are
those of the Stage 1 inventory; numbers 1 to 6, 9, and 10 are proved (see
[Stage 2a progress](#stage-2a-progress)).

| # | Lemma | What it needs |
| --- | --- | --- |
| 7 | `dex_step_simulated` | Case analysis on the Dex action, the thread's phase, and the `Intent`; uses `ConfigInvariant` and the lemmas of `Dex/WaitLemmas.lean`. `Related` may need more invariants. |
| 8 | `l_step_simulated` | The converse case analysis; inserts `workerRun` and `workerReturn` before each commit. |
| 11 | `fairMaximalTrace_dex_to_l` | Stage 2b. Build the L run from the Dex run by dropping handler steps; transfer F0 to F6 and the marker; equal limits of the two traces. |
| 12 | `fairMaximalTrace_l_to_dex` | Stage 2b. Build the Dex run from the L run by inserting handler steps; the same transfers. |

`safety_of_isWellFormed` now depends on exactly two `sorry`: items 7 and 8.

`Related` in `Simulation.lean` is a candidate relation, not yet shown to be
preserved. Lemmas 7 and 8 are stated against it and will change if Stage 2
strengthens it.

Two statements changed in Stage 2a, both in the proof plan and not in the
theorems. `dex_step_simulated` now also assumes that the L configuration is
reachable, which is where `program.IsWellFormed` applies.
`l_step_simulated` no longer assumes well-formedness, and concludes that the
requests of the simulating Dex actions are the encoded request of the L
label.

## Decided in Stage 2a: handles are unforgeable

Work on the step simulations found that a program could forge the handle of a
mutex before `newMutex` created it. A thread that locked it blocked forever
in L and was woken in Dex, so the liveness statement was false for such a
well-formed program (spec Review log, row L5, with the program).

**Owner decision, 2026-11-10: handles are unforgeable** (spec §8.1 "Handles
and literals"; Appendix A, item 40).

- `Expr.lit` takes a `Literal`: null, a Boolean, an integer, or a string.
- `Expr.staticMutex index` is the handle of a statically known mutex.
- Thread handles follow the same rule: there is no thread literal, so a
  thread handle comes only from `fork`. This choice costs nothing, because it
  follows from the restriction on literals, and it means that no thread can
  cancel or join itself and that `main` is never canceled.
- The runtime instruction `Instr.relock mutex` replaces the `lock` of a
  literal that `wait(cv, m)` used to leave behind.
- `evalExpr_mutex_origin` and `evalExpr_thread_origin` (`Properties.lean`,
  proved) state the rule: a mutex handle that an expression evaluates to is
  static or already in a local variable, and a thread handle is already in a
  local variable.

`Examples/ForgedMutexHandle.lean`, which checked the counterexample in the
kernel, is deleted: the program can no longer be written, because no
expression denotes the handle of a mutex that `newMutex` has not created. The
two lemmas above are the check that replaces it.

## Open issue found in Stage 2a: forged handles through the input

**Stage 2a is stopped on this point. It needs an owner decision.** Nothing in
the model, the translation, or the theorem statements was changed for it.

The decision of 2026-11-10 closed the source syntax: no expression denotes a
handle that was not created. But the program input and every external message
are L values (`Val`), and a `Val` can be a mutex handle. So the environment
can still pass the handle of a mutex that `newMutex` has not created yet, and
the counterexample of spec Review log row L5 comes back:

```text
locker(h):  lock(h); emit(1)
main(x):    t := fork locker(x); sleep(0); m := newMutex(); y := recv(9)
```

started with `x = <handle of the mutex main will create>`. The program has no
literal handle and is well formed. `Examples/ForgedHandleInput.lean` checks
both runs in the kernel:

| Fact | Lean |
| --- | --- |
| The L run in which the locker blocks first has trace `[begin(x)]`. | `l_trace` |
| At its end the mutex is unlocked and the locker is still blocked on it. | `l_lockerIsStuck` |
| The translated Flow has trace `[begin(x), out(1)]`. | `dex_trace` |

So `liveness_of_isWellFormed` is still false as the model stands, and the
step simulations (items 7 and 8) are false for this program. The same input
can arrive as an external message (`in(c, v)`).

Options:

| Option | Change | Cost |
| --- | --- | --- |
| (a) The environment sends plain data only. Recommended. | The program input and every external message are literals: null, a Boolean, an integer, or a string. In Lean, `L.Request` carries a `Literal`, and `E_adm` admits encoded literals only. Spec §5.3 and §8.1 say so. | Small. It completes the decision of 2026-11-10: a mutex handle then really comes from `newMutex` or the static-mutex expression only. Thread and condition variable handles are excluded by the same rule. |
| (b) `newMutex` serves blocked lockers. | L semantics: creating a mutex grants it to the first thread already blocked on its identity. | Small in Lean, but L keeps a rule that only a forged handle can trigger, and the unforgeability of 2026-11-10 stays incomplete. |
| (c) Well-formedness excludes it. | `Program.IsWellFormed` also requires that no thread locks a mutex that does not exist. | Small, but the condition then depends on the input, and it also excludes `lock` of a static mutex beyond `staticMutexCount`. |

With (a), the L-side invariant "every handle of a created mutex that a thread
can reach names an existing mutex" holds for every reachable configuration.
The step simulations need it.

## Stage 2a progress

Stage 2a proves `safety_of_isWellFormed`. Liveness stays `sorry` until
Stage 2b. No Batteries and no Mathlib so far: the list facts needed are in
Lean core or are proved in `Dex/RunLemmas.lean`. Proofs use `grind` and
classical case splits, so `Classical.choice` may appear among the axioms.

| # | Lemma | Status |
| --- | --- | --- |
| 1 | `Dex.wake_servesEarliestWaiter` | Proved, `Dex/WaitLemmas.lean`. |
| 2 | `Dex.registerWait_any_firstChannelWins` | Proved, `Dex/WaitLemmas.lean`. |
| 3 | `Dex.registerWait_timer_blocks` | Proved, `Dex/WaitLemmas.lean`. |
| 4 | `noWaiterOnNonEmptyChannel` | Proved, `Invariants.lean`. |
| 5 | `registeredSeq_isInjective` | Proved, `Invariants.lean`. |
| 6 | `armedTimers_areTimedSteps` | Proved, `Invariants.lean`. |
| 9 | `finiteTrace_dex_to_l` | Proved from item 7, `FiniteTraces.lean`. |
| 10 | `finiteTrace_l_to_dex` | Proved from item 8, `FiniteTraces.lean`. |

Proof lines added in Stage 2a so far: about 2,800, in `Dex/RunLemmas.lean`,
`Dex/WaitLemmas.lean`, `Dex/RunInvariant.lean`, `Invariants.lean`, and
`FiniteTraces.lean`.

What remains for safety is items 7 and 8. The plan for them:

- Both step simulations take "the L configuration is reachable". The facts
  about the L side alone go into an invariant of reachable L configurations,
  proved by induction on L runs as `ConfigInvariant` is for Dex: unique and
  fresh thread IDs, fresh created mutex IDs, at most one owner per mutex,
  distinct `since` stamps, and "every reachable handle of a created mutex
  names an existing mutex" (which needs the open issue above decided).
- `Related` keeps only the clauses that link the two sides.
- The L-to-Dex simulation is proved case by case: per label, per thread
  phase, per `Intent`. The Dex-to-L simulation follows from it by
  determinism, plus "the L label is enabled" and the four Dex actions that L
  does not see.

Three strengthenings of `Related` are already known to be needed:

- While `main` boots, the relation must fix the whole thread state (thread
  ID, counter, stack, code, no timer), not only the locals.
- A thread blocked in a timed operation must have exactly one timer condition
  in its Step's wait.
- Thread IDs and created mutex IDs must be fresh: for live threads `a` and
  `b` and every `k` at or above `a`'s counter, `a.tid ++ [k]` is not a prefix
  of `b.tid`, and the same for joinable thread IDs and the creators of
  existing mutexes.

Items 4 to 6 follow from one inductive invariant. `Dex/RunInvariant.lean`
defines `Run.Invariant` (unique Step IDs, counted Step numbers, quiet
waiters, injective `sws`, allowed timers) and proves that every meta-function
of spec §4.2 keeps it, for any Flow whose waits are AnyOf or
`Until(c.ForOne())`. `Invariants.lean` proves that every transition of a
translated Flow keeps `ConfigInvariant`, so every reachable configuration
satisfies it. The step simulations (items 7 and 8) can use it as a fact about
the Dex side.

## Stage 2 outline

1. List and `AssocList` lemmas (`get?` after `set`, `find?` after `map`,
   `sortStable`).
2. Lemmas 1 to 3, then the invariants 4 to 6.
3. L-side invariants: thread IDs are unique and fresh; at most one owner per
   mutex; the `since` stamps are distinct.
4. Lemmas 7 and 8, one case per rule and per `Intent`. This is the bulk.
   The `cvWait`, `signal`, and `broadcast` cases use
   `readWaiters_snapshot_of_cvOperand` to read the waiter list from the
   filtered snapshot.
5. Lemmas 9 and 10, which give `safety_of_isWellFormed`.
6. Infinite runs: the stuttering construction in both directions, fairness
   and marker transfer, and the limit trace. Then lemmas 11 and 12, which
   give `liveness_of_isWellFormed`.
