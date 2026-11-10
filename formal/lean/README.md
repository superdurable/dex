# Dex expressiveness theorem in Lean 4

This directory holds a Lean 4 development of one theorem:

> Every well-formed program of a small concurrent imperative language **L**
> translates into a Dex Flow that is trace-equivalent to it, assuming no
> failures.

The semantics it formalizes is
[`formal/semantics/dex-core-semantics.md`](../semantics/dex-core-semantics.md),
revision 7. "Spec" below means that document.

## Status: Stage 2a, packages 1 and 2 proved, definitions fixed again

Stage 2a proves the safety theorem. The proof is split into four work
packages with fixed statements. Work packages 1 and 2 are proved. Work
package 4 is proved for every Dex step except six kinds while the Flow runs.
Five `sorry` of the safety proof are left: the four lemmas of work package 3,
and `dex_step_simulated`. See
[Stage 2a: definitions and work packages](#stage-2a-definitions-and-work-packages).

The first version of the fixed definitions was not inductive: the provers
found configuration pairs that satisfied `IsSimulationState`, were not
reachable, and broke a step lemma. The definitions were strengthened, and a
fuzz now tests that they are inductive. See
[Inductiveness](#inductiveness-gaps-found-and-fixed).

Work on the step simulations found twice that a program could obtain the
handle of a mutex that did not exist yet. The owner decided that handles are
unforgeable and that the environment sends plain data only. See
[Decided in Stage 2a](#decided-in-stage-2a-handles-are-unforgeable).

### Stage 1, revised after the first review

Stage 1 is the model and the statements. The two main theorems are stated and
reduced to four inclusion lemmas. Those lemmas, and eight more that the proof
plan needs, end in `sorry`. Stage 2 is the proofs. It starts after the owner
and a reviewer confirm that the statements match the spec.

The first review (of commit `cc123891a`) returned "fix before Stage 2". This
revision applies the owner decisions of 2026-11-09 (spec Review log, L1 to L4)
and the review's two model findings (R4-1, R4-2). See
[Points for the owner](#points-for-the-owner) for the status of each point.

`lake build` succeeds. Every `sorry` is listed in
[The `sorry` inventory](#the-sorry-inventory).

## Build

```bash
# Once: install elan, the Lean toolchain manager, into ~/.elan (no sudo).
curl -sSf https://raw.githubusercontent.com/leanprover/elan/master/elan-init.sh \
  | sh -s -- -y --default-toolchain none --no-modify-path
export PATH="$HOME/.elan/bin:$PATH"

cd formal/lean
lake build
```

The first build downloads the pinned toolchain. A clean build takes a few
minutes; the lockstep, relation, and inductiveness checks take half a minute
of it.

```bash
# The inductiveness fuzz, compiled, on every example program.
# Arguments: fuel, sampling period, walk length, seeds.
lake exe inductiveness-fuzz 120 5 6 2
```

| Tool | Version |
| --- | --- |
| Lean | 4.35.0 (`leanprover/lean4:v4.35.0`, commit `eb6cafb7290e`), pinned in `lean-toolchain` |
| Lake | 5.0.0 (ships with Lean 4.35.0) |
| elan | 4.2.4 |
| Dependencies | none: Lean core only, no Mathlib, no Batteries |

Build output goes to `formal/lean/.lake/`, which `.gitignore` and
`.dockerignore` exclude, with the fuzz binary
`formal/lean/.lake/build/bin/inductiveness-fuzz`. `.lean` files are outside the license-header policy:
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
| `DexExpressiveness/Dex/TimerLemmas.lean` | Proved: no meta-function of spec §4.2 arms a timer ID twice. |
| `DexExpressiveness/Invariants.lean` | Proved: the invariants of every reachable configuration of a translated Flow (`ConfigInvariant`, `TasksAreDistinct`, `TimersAreDistinct`), and the three invariants of spec §7 item 4 and Appendix A, items 11 and 26. Fixed definitions. |
| `DexExpressiveness/L/Syntax.lean` | L syntax and its event alphabet (spec §8.1). |
| `DexExpressiveness/L/ThreadLocal.lean` | What one statement does to its own thread, and the atomic action it asks for. |
| `DexExpressiveness/L/Semantics.lean` | L small-step semantics, L fairness, `Program.IsWellFormed` (spec §8.1). |
| `DexExpressiveness/L/Invariant.lean` | `L.Invariant`, the invariant of reachable L configurations. Fixed definitions. |
| `DexExpressiveness/L/InvariantStep.lean`, `L/InvariantStepLemmas.lean`, `L/InvariantStepLocal.lean`, `L/InvariantStepFrame.lean`, `L/InvariantStepActions.lean` | Work package 1, proved: every L transition keeps `L.Invariant`. |
| `DexExpressiveness/Translation/Flow.lean` | The translation `⟦·⟧` (spec §8.2, §8.3). |
| `DexExpressiveness/Translation/Projection.lean` | The projection `π`, the request encoding, `E_adm` of a translated program (spec §5.3). |
| `DexExpressiveness/TraceSets.lean` | The four trace sets the theorem compares. |
| `DexExpressiveness/Theorems.lean` | **The main statements**: `safety_of_isWellFormed`, `liveness_of_isWellFormed`, `expressiveness_of_isWellFormed`. |
| `DexExpressiveness/Simulation/Relation.lean` | `Related`, `simulatingActions`, and the statements `IsSimulatedByActions`, `IsSimulatedByDex`, and `IsSimulatedByL`. Fixed definitions. |
| `DexExpressiveness/Simulation/RelationLemmas.lean` | Proved consequences of the fixed definitions, for every package: a task holds at most one effect, a waiting Step has no invocation, and the action list of each case of `simulatingActions`. |
| `DexExpressiveness/Simulation/LToDexBasic.lean`, `Simulation/LToDexBasicLemmas.lean`, `Simulation/LToDexBasicWake.lean`, `Simulation/LToDexBasicFire.lean` | Work package 2, proved: L to Dex for ticks, requests, timer fires, late emits, and the begin of an action. The frame lemmas of `RunningRelated` are here. |
| `DexExpressiveness/Simulation/LToDexComplete.lean` | Work package 3: L to Dex for the registration of a wait and the completion of an action. |
| `DexExpressiveness/Simulation/LToDex.lean` | Proved from packages 2 and 3: `l_step_simulatedByActions` and `l_step_simulated`. |
| `DexExpressiveness/Simulation/DexToL.lean`, `Simulation/DexToLLemmas.lean` | Work package 4: `dex_step_simulated`, proved for the steps outside `IsObservedWhileRunning`. |
| `DexExpressiveness/Simulation.lean` | The two liveness inclusions (`sorry` until Stage 2b). |
| `DexExpressiveness/FiniteTraces.lean` | Proved from the step simulations and the two invariants: the two finite-trace inclusions that give safety. |
| `DexExpressiveness/Properties.lean` | Proved facts. No `sorry`. |
| `DexExpressiveness/Examples/*.lean` | Concrete runs checked in the Lean kernel, lockstep checks, executable checks of `IsSimulationState` (`RelatedCheck.lean`), and the inductiveness fuzz (`Inductiveness.lean`). No `sorry`. |
| `InductivenessFuzz.lean` | The program `lake exe inductiveness-fuzz`. Test tooling. |

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

All of this is free of `sorry`. `#print axioms` shows only `propext`,
`Quot.sound`, and `Classical.choice`; no `native_decide`.

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
- `related_initial`, `L.Invariant.initial`: the relation and the L invariant
  hold initially.
- `L.Invariant.step`: every L transition keeps the L invariant (work
  package 1).
- `ConfigInvariant.step`, `TasksAreDistinct.step`, `TimersAreDistinct.step`:
  every Dex transition of a translated Flow keeps the Dex invariants.
- `translate_compute_effects`: a handler of a translated Flow computes at most
  one effect, and none in WaitFor.
- Work package 2: `l_tick_simulated`, `l_start_simulated`, `l_input_simulated`,
  `l_fire_simulated`, `l_lateDeliver_simulated`, `l_lateDrop_simulated`,
  `l_thread_begin_simulated`, `l_thread_deliver_simulated`.
- `l_step_simulatedByActions` and `l_step_simulated`, from the case lemmas.
- `dex_step_simulated_of_observed`: the Dex to L simulation for a tick, the
  handler steps, the stale drops, and every step after the close.
- `finiteTrace_dex_to_l`, `finiteTrace_l_to_dex`: the two inclusions of
  safety, from the step simulations and the invariants.
- `literal_isNotHandle`: no request carries a handle.
- `safety_of_isWellFormed`, `liveness_of_isWellFormed`,
  `expressiveness_of_isWellFormed`: proved from the four inclusion lemmas.
  The two of liveness are `sorry`; the two of safety rest on the work
  packages.
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
directions, for thirteen programs that together use every L construct. A
mismatch fails the build. This is testing, not proof.

Three changes were planted during development to see whether the build
notices. Dropping the cancel of a blocked timed operation failed the
`TimedSelect` theorems. Dropping the timer-number tie-break passed the random
checks, because equal deadlines are rare under random ticks; `TimerOrder.lean`
was added for that case and fails under the change. Dropping the `waiters/cv`
load selection from the translation failed `readWaiters_snapshot_of_cvOperand`.

## The `sorry` inventory

There are 7. Five are in the safety proof; two are the liveness inclusions of
Stage 2b.

| Package | File | Lemma | What it needs |
| --- | --- | --- | --- |
| 3 | `Simulation/LToDexComplete.lean` | `l_thread_register_simulated` | [STEP-COMPLETE-WAITFOR]: `registerWait` against `Config.registerWait`, per wait request (`Dex.registerWait_any_firstChannelWins`, `Dex.registerWait_timer_blocks`). |
| 3 | `Simulation/LToDexComplete.lean` | `l_thread_boot_simulated` | [STEP-COMPLETE-EXECUTE] of the start Step: the tokens of the statically known mutexes (`mutexesBeforeBoot`). |
| 3 | `Simulation/LToDexComplete.lean` | `l_thread_complete_simulated` | [STEP-COMPLETE-EXECUTE]: `encodeIntent` against `Config.applyIntent`, one case per `Intent`. The largest lemma. |
| 3 | `Simulation/LToDexComplete.lean` | `l_thread_resume_simulated` | The same for the continuation of a wait; the `Intent` is `silent`, `endThread`, or `halt`. |
| 4 | `Simulation/DexToL.lean` | `dex_step_simulated` | `dex_step_simulated_of_observed` leaves the steps of `IsObservedWhileRunning`: the start of the Flow and, while it runs, a publication, [STEP-START], [WORKER-EFFECT], a Step completion, and [TIMER-FIRE]. Each follows from `l_step_simulatedByActions` by determinism, once the L label is shown enabled. |
| Stage 2b | `Simulation.lean` | `fairMaximalTrace_dex_to_l` | Build the L run from the Dex run by dropping handler steps; transfer F0 to F6 and the marker; equal limits of the two traces. |
| Stage 2b | `Simulation.lean` | `fairMaximalTrace_l_to_dex` | Build the Dex run from the L run by inserting handler steps; the same transfers. |

`safety_of_isWellFormed` depends on the five `sorry` of packages 3 and 4 and
on nothing else unproved.

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

### The environment sends plain data only

The first decision closed the source syntax, but the program input and every
external message were still arbitrary L values, and an L value can be a mutex
handle. The counterexample came back with the forged handle as the program
input (spec Review log, row L6).

**Owner decision, completing that of 2026-11-10** (spec §5.3, §8.1 "Handles
and literals"; Appendix A, item 41):

- `L.Request` carries a `Literal`: `Request.start (input : Literal)` and
  `Request.input (chan) (value : Literal)`. The events `begin(v)`, `in(c, v)`,
  and `refused(c, v)` carry the literal's value.
- `E_adm` admits only encoded literals (`Translation.environment`).
- `Examples/ForgedHandleInput.lean` is kept and turned into the check. The
  counterexample is now unrepresentable: its start request,
  `start <mutex handle>`, is not a term, because `Request.start` does not
  accept a `Val`. The file proves `literal_isNotHandle` (no literal's value is
  a thread, mutex, or condition variable handle) and `staysInStep` (the same
  program, started with plain data, runs in step on both sides).

## Stage 2a progress

Stage 2a proves `safety_of_isWellFormed`. Liveness stays `sorry` until
Stage 2b. No Batteries and no Mathlib: the list facts needed are in Lean core
or are proved in `Dex/RunLemmas.lean`. Proofs use `grind` and classical case
splits, so `Classical.choice` appears among the axioms.

| Stage 1 # | Lemma | Status |
| --- | --- | --- |
| 1 | `Dex.wake_servesEarliestWaiter` | Proved, `Dex/WaitLemmas.lean`. |
| 2 | `Dex.registerWait_any_firstChannelWins` | Proved, `Dex/WaitLemmas.lean`. |
| 3 | `Dex.registerWait_timer_blocks` | Proved, `Dex/WaitLemmas.lean`. |
| 4 | `noWaiterOnNonEmptyChannel` | Proved, `Invariants.lean`. |
| 5 | `registeredSeq_isInjective` | Proved, `Invariants.lean`. |
| 6 | `armedTimers_areTimedSteps` | Proved, `Invariants.lean`. |
| 7 | `dex_step_simulated` | Work package 4, partly proved. |
| 8 | `l_step_simulated` | Proved from the case lemmas of work packages 2 (proved) and 3, `Simulation/LToDex.lean`. |
| 9 | `finiteTrace_dex_to_l` | Proved from item 7 and the invariants, `FiniteTraces.lean`. |
| 10 | `finiteTrace_l_to_dex` | Proved from item 8 and the invariants, `FiniteTraces.lean`. |

Items 4 to 6 follow from one inductive invariant. `Dex/RunInvariant.lean`
defines `Run.Invariant` (unique Step IDs, counted Step numbers, quiet
waiters, injective `sws`, allowed timers) and proves that every meta-function
of spec §4.2 keeps it, for any Flow whose waits are AnyOf or
`Until(c.ForOne())`. `Invariants.lean` proves that every transition of a
translated Flow keeps `ConfigInvariant`, so every reachable configuration
satisfies it.

## Stage 2a: definitions and work packages

### The ingredients

Every step simulation lemma takes one hypothesis, `IsSimulationState program
lConfig dexConfig` (`Simulation/Relation.lean`, lines 430 to 441). It has five
parts:

| Part | Definition | Status |
| --- | --- | --- |
| `Related program lConfig dexConfig` | `Simulation/Relation.lean`, lines 267 to 288; `RunningRelated` lines 166 to 250; `ClosedRelated` lines 253 to 263; `PhaseMatchesStep` lines 108 to 161; other helpers lines 42 to 101 | Fixed. Holds initially (`related_initial`, proved). |
| `L.Invariant program lConfig` | `L/Invariant.lean`, lines 131 to 197; helpers lines 42 to 128 | Fixed, unchanged. Holds initially and is kept by every L transition (`L.Invariant.step`, proved). |
| `ConfigInvariant program dexConfig` | `Invariants.lean`, lines 72 to 78, with `IsTaskComputed` lines 63 to 69; it wraps `Dex.Run.Invariant`, `Dex/RunInvariant.lean`, lines 47 to 71 | Fixed. Proved for every Dex transition (`ConfigInvariant.step`). |
| `TasksAreDistinct dexConfig` | `Invariants.lean`, lines 238 to 240 | Fixed. Proved for every Dex transition (`TasksAreDistinct.step`). |
| `TimersAreDistinct dexConfig` | `Invariants.lean`, lines 671 to 672; `Dex.Run.HasDistinctTimers`, `Dex/TimerLemmas.lean`, line 64 | Fixed. Proved for every Dex transition (`TimersAreDistinct.step`). |

The L to Dex lemmas conclude `IsSimulatedByActions` (`Simulation/Relation.lean`,
lines 396 to 404): the Dex actions of `simulatingActions` (lines 358 to 387)
are enabled, have the same projected events, and end in a related
configuration. `IsSimulatedByDex` follows
(`IsSimulatedByActions.isSimulatedByDex`).

`Related` holds only the clauses that mention both sides:

| Clause | What it links |
| --- | --- |
| `clock`, `stamp` | The clocks, and the last stamped L action with the last Dex commit. |
| `status`, `notStarted` | Not started, running, closed; no task before the start. |
| `shared`, `cvQueues` | Shared variables and waiter lists with Attributes. |
| `inputQueues` | Queued external messages with the external Channels. |
| `mutexTokens`, `joinTokens`, `cvWakes` | Unlocked mutexes, joinable threads, and early wakes with the messages of their Channels. |
| `mutexesBeforeBoot` | While `main` boots: the mutexes are the static ones, unlocked, and no token is published. |
| `cancelRequestsAreFlags`, `cancelFlagsAreRequests` | Cancel flags with pending cancel requests. A flag without a request belongs to a thread that observed the request, or to an ended thread that no live thread can still fork. |
| `timers`, `timerCounter` | Armed timers and the timer counter with `Tm` and the `Timed` counter. |
| `outputsEmpty` | No Flow output before the close. |
| `threadsHaveSteps`, `stepsHaveThreads`, `activeStepsHaveDistinctThreads`, `invocationsAreActive` | Threads that have not halted with active Steps, one to one (`StepCarriesThread`), and each thread's phase with its Step's phase, invocation, and task (`PhaseMatchesStep`). |
| `blockingOrder` | The `since` order of blocked threads with the `sws` order of their waits. |
| `tasksAreCurrent` | While the Flow runs every task belongs to a running invocation. |
| `ClosedRelated` | After the close: late emits with the effects stale tasks still hold; no invocation; no timer. |

`L.Invariant` holds the facts about the L side alone:

| Clause | Fact |
| --- | --- |
| `notStartedIsInitial`, `closedHasNoThread` | The configuration before the start and after the end. |
| `threadIdsNodup`, `threadIdsFresh` | Thread IDs are unique, and the next children of a thread are fresh. |
| `joinableNodup`, `joinableFresh` | A thread is joinable at most once, never while it lives, and not before it is created. |
| `mutexesFresh` | The next mutex a thread creates does not exist yet. |
| `handlesExist` | Every handle a thread can reach names an existing mutex. This is where the unforgeability decisions are used. |
| `bootIsInitial` | While `main` boots it is the only thread, in its initial state. |
| `sinceBelowCounter`, `sinceInjective` | Blocking stamps are distinct. |
| `timerNumbersCounted`, `timerNumbersInjective` | Timer numbers are distinct, positive, and counted. |
| `phasesFitCode` | Each thread's phase fits its code and its timer. |
| `runningHoldsRequiredMutex` | A statement that requires a mutex runs while its thread holds it. |

The Dex side has three invariants:

| Invariant | Fact |
| --- | --- |
| `ConfigInvariant.runInvariant` | `Dex.Run.Invariant`: unique Step IDs, counted Step numbers, supported and quiet waits, injective `sws`, allowed timers. |
| `ConfigInvariant.tasksComputed` | A task holds the response its handler computes, and the effects it still holds are the last of those its handler computes. So a task holds at most one effect, and a WaitFor task none. |
| `TasksAreDistinct` | Task IDs are unique and below the next fresh ID. |
| `TimersAreDistinct` | Armed timers have distinct IDs. |

### Inductiveness: gaps found and fixed

The step lemmas quantify over every configuration pair that satisfies
`IsSimulationState`, not only over reachable pairs. The first fixed
definitions allowed unreachable pairs that broke a step lemma. Each gap, and
the clause that closes it:

| Gap | A pair that was allowed | Closed by |
| --- | --- | --- |
| WaitFor task with an effect | A `waitStarted` thread whose WaitFor task holds an effect: its [WORKER-EFFECT] has no L step. | `PhaseMatchesStep`, case `waitStarted`: the task holds no effect. Also follows from `tasksComputed`. |
| Task effects | After the close, a stale task holds two effects: [WORKER-ABANDON] drops both, L drops one late emit per label. | `IsTaskComputed`: the effects a `Running` task holds are the last of those its handler computes; `translate_compute_effects`. |
| Waiting Step with an invocation | A thread that is not blocked is carried by a Step in phase `WT` with a stored wait: a publication wakes the Step and not the thread. | `PhaseMatchesStep` fixes the Step phase in every case. `RunningRelated.blocked_of_waiting` and `IsSimulationState.waitingHasNoInvocation` state the consequence. |
| Cancel flag of an unforked child | A flag is set for the next child of a live thread, with no pending request: after `fork` the child has a flag and no request. | `cancelFlagsAreRequests`: a flag without a request belongs to a thread that observed it, or to an ended thread that is not a future child of a live thread. |
| Boot and static mutexes | While `main` boots, the mutex owners and the token Channels were unconstrained, and the start Step then publishes the tokens. | `mutexesBeforeBoot`. |
| Duplicate timer IDs | `Tm` lists one timer ID twice: `Related.timers` reads the first binding, `Due_f` reads both. | `TimersAreDistinct`. |
| Hidden action sequence | `IsSimulatedByDex` hides the Dex actions, so the Dex to L direction could not reuse an L to Dex lemma by determinism. | `simulatingActions`, `IsSimulatedByActions`. |

The cancel-flag and boot clauses are in `Related`, not in `L.Invariant`:
`L.Invariant` and its proof are unchanged. The cancel-flag clause needs no
validity of thread handles: a cancel publishes a request with the flag, on
both sides, whatever thread ID it names.

### The inductiveness fuzz

`Examples/Inductiveness.lean` tests that `IsSimulationState` is inductive:

1. It runs an L program and its Flow in lockstep, in both directions, and
   takes every `period`-th configuration pair.
2. It changes each pair in about 270 ways: one field of one side (a thread's
   phase, counter, timer, locals, or code; the cancel flags, the joinable
   threads, the mutex owners, the waiter lists, the input queues, the shared
   variables, the counters, the late emits; a Step's phase, invocation, wait,
   condition results, options, or input; a task's phase, condition results,
   or snapshot; added, dropped, or doubled Steps, tasks, Channel messages,
   timers, Attributes), or matching fields of both sides (a cancel flag with
   its request, a late emit with a task effect, and so on).
3. It keeps a changed pair only when the Boolean mirror of
   `IsSimulationState` (`checkState`, `Examples/RelatedCheck.lean`) accepts
   it.
4. From every kept pair it takes every enabled L label with its
   `simulatingActions`, and every enabled admissible Dex action with its L
   labels. The other side must be enabled, the events and stamps must agree,
   and `checkState` must accept the result. It then walks a few pseudo-random
   steps in each direction with the same checks.

Six `#guard` lines run it on few pairs when the file is built. `lake exe
inductiveness-fuzz` runs it compiled on every example program.

With the mirror of the first definitions, `inductiveness-fuzz 120 5 6 2`
reports 322 failures and finds every gap of the table above:

| Perturbation | First failing step | Gap |
| --- | --- | --- |
| `Dex.task.phase`, `both.lateEffect` | The thread's step: the Dex side is not enabled. [WORKER-EFFECT]: the L side is not enabled. | WaitFor task with an effect; task effects |
| `both.lateEffects` | `lateDrop`: `Related.lateEmits` | Task effects after the close |
| `Dex.record.waiting` | `input`: `Related.inputQueues`. The thread's step: `Related.mutexTokens`, `Related.cancelFlagsAreRequests` | Waiting Step with an invocation |
| `L.cancelFlags.add` | The parent's `fork`: `Related.cancelFlagsAreRequests` | Cancel flag of an unforked child |
| `L.mutexOwners.add`, `.set`, `.drop`, `Dex.channels.add` | The end of the boot: `Related.mutexTokens` | Boot and static mutexes |
| `Dex.timers.duplicate`, `Dex.timers.add` | [TIMER-FIRE]: the L side is not enabled. `fire`: `Related.timers` | Duplicate timer IDs |

With the present definitions the same run changes 1,898 pairs into 510,426
copies, keeps about 190,000, and reports no failure. It checks single steps
of every kind: every L label kind and every thread phase that has a step
(`bootPending`, `bootRunning`, `ready`, `running`, `emitting`, `emitted`,
`waitStarted`, `woken`, `resuming`), `fire`, `input` while running and after
the close, `lateDeliver`, `lateDrop`, and every Dex action kind.

Writing the fuzz found one more fault, in the tooling: the `timers` mirror
checked every binding of the timer list, where `Related.timers` reads only
the first binding of a timer ID. A mirror that is stricter than its clause
hides gaps; the duplicate timer gap was invisible until the mirror was made
exact.

The fuzz found no gap beyond those of the table. It is evidence, not proof:
the work packages prove inductiveness.

### Tested on reachable pairs

`Examples/RelatedCheck.lean` checks the Boolean mirror of `IsSimulationState`
in every configuration pair of pseudo-random lockstep runs, in both
directions, for fourteen programs. A failure fails the build. All pass.

### Work packages

Each package proves the `sorry` of its own file(s), may add lemmas there and
to `Simulation/RelationLemmas.lean`, and may add general lemmas to
`Dex/RunLemmas.lean` only by appending. None may change a model file
(`Dex/Syntax.lean`, `Dex/Config.lean`, `Dex/MetaFunctions.lean`,
`Dex/Rules.lean`, `L/Syntax.lean`, `L/ThreadLocal.lean`, `L/Semantics.lean`,
`Translation/*.lean`), `Simulation/Relation.lean`, `L/Invariant.lean`, the
definitions of `Invariants.lean`, `TraceSets.lean`, or `Theorems.lean`. A
package that finds a fixed definition too weak or wrong reports it with the
failing case instead of changing it.

| Package | Files | Lemmas | Status |
| --- | --- | --- | --- |
| 1. L invariant | `L/InvariantStep*.lean` | `L.Invariant.step` | Proved. |
| 2. L to Dex, basic steps | `Simulation/LToDexBasic*.lean` | `l_tick_simulated`, `l_start_simulated`, `l_input_simulated`, `l_fire_simulated`, `l_lateDeliver_simulated`, `l_lateDrop_simulated`, `l_thread_begin_simulated`, `l_thread_deliver_simulated` | Proved. |
| 3a. L to Dex, registration | `Simulation/LToDexComplete.lean` | `l_thread_register_simulated` | Open. |
| 3b. L to Dex, completion | `Simulation/LToDexComplete.lean` | `l_thread_boot_simulated`, `l_thread_complete_simulated`, `l_thread_resume_simulated` | Open. |
| 4. Dex to L | `Simulation/DexToL.lean`, `Simulation/DexToLLemmas.lean` | `dex_step_simulated` | `dex_step_simulated_of_observed` proved; the steps of `IsObservedWhileRunning` are open. |

Every L to Dex lemma has the form

```lean
theorem l_..._simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    ... (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig label = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig label lEvents lNext
```

For package 3 the action list is `returnTaskActions task ++ [.stepComplete
task.id]`, where `task` runs the invocation of the Step that carries the
thread (`simulatingActions_complete`, `Simulation/RelationLemmas.lean`).
Package 3 imports the frame lemmas of package 2
(`wp2_runningRelated_changeThread`, `wp2_runningRelated_setPhase`,
`wp2_runningRelated_stamp`, `wp2_phaseMatchesStep_transfer`,
`wp2_timers_of_wake`, and the forms of `earliestBlocked`, `waitersOn`,
`deliverInput`, and `leastDueTimerThread` without an inner `match`).

## Stage 2 outline

1. Work packages 1 to 4 above give `safety_of_isWellFormed`.
2. Stage 2b, liveness: the stuttering construction on infinite runs in both
   directions (drop or insert the handler steps), the transfer of F0 to F6 and
   of the marker, and the limit trace. Then `fairMaximalTrace_dex_to_l` and
   `fairMaximalTrace_l_to_dex` give `liveness_of_isWellFormed`.
