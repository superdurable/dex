# Dex expressiveness theorem in Lean 4

This directory holds a Lean 4 development of one theorem:

> Every well-formed program of a small concurrent imperative language **L**
> translates into a Dex Flow that is trace-equivalent to it, assuming no
> failures.

The semantics it formalizes is
[`formal/semantics/dex-core-semantics.md`](../semantics/dex-core-semantics.md),
revision 8. "Spec" below means that document.

## Status: the expressiveness theorem is proved

`safety_of_isWellFormed`, `liveness_of_isWellFormed`, and
`expressiveness_of_isWellFormed` are proved. No file of the expressiveness
proof has a `sorry`. The failure-transparency statements of
`DexExpressiveness/Failure/Statements.lean` end in `sorry` by design: they are
the fixed statements of the next proof. See
[Failure transparency (issue 1514), Stage 1](#failure-transparency-issue-1514-stage-1).

```text
'DexExpressiveness.safety_of_isWellFormed' depends on axioms:
[propext, Classical.choice, Quot.sound]
'DexExpressiveness.liveness_of_isWellFormed' depends on axioms:
[propext, Classical.choice, Quot.sound]
'DexExpressiveness.expressiveness_of_isWellFormed' depends on axioms:
[propext, Classical.choice, Quot.sound]
```

Safety (Stage 2a) was split into four work packages with fixed statements,
and liveness (Stage 2b) into six. See
[Stage 2a: definitions and work packages](#stage-2a-definitions-and-work-packages)
and [Stage 2b: liveness](#stage-2b-liveness).

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

Stage 1 was the model and the statements. The two main theorems were stated
and reduced to four inclusion lemmas. Those lemmas, and eight more that the
proof plan needed, ended in `sorry`. Stage 2 was the proofs. It started after
the owner and a reviewer confirmed that the statements match the spec.

The first review (of commit `cc123891a`) returned "fix before Stage 2". This
revision applies the owner decisions of 2026-11-09 (spec Review log, L1 to L4)
and the review's two model findings (R4-1, R4-2). See
[Points for the owner](#points-for-the-owner) for the status of each point.

`lake build` succeeds. At the end of Stage 1 every `sorry` was listed in
[The `sorry` inventory](#the-sorry-inventory); none is left.

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

# The checks of the liveness definitions, compiled. Arguments: fuel, sampling
# period, walk length (unused), seeds, and the word `liveness`.
lake exe inductiveness-fuzz 200 4 6 3 liveness
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
| `DexExpressiveness/Core/TransitionSystem.lean` | Runs, finite traces, fair maximal traces, markers; runs of appended schedules. Shared by both sides. |
| `DexExpressiveness/Core/BlockRun.lean`, `Core/BlockRunFlattening.lean` | Runs in blocks and their flattening. Fixed definitions; work package L1, proved. |
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
| `DexExpressiveness/L/LockDiscipline.lean`, `L/LockDisciplineSound.lean` | Proved: a decidable static lock discipline (`Program.hasStaticLockDiscipline`) implies `Program.IsWellFormed`. |
| `DexExpressiveness/L/InvariantStep.lean`, `L/InvariantStepLemmas.lean`, `L/InvariantStepLocal.lean`, `L/InvariantStepFrame.lean`, `L/InvariantStepActions.lean` | Work package 1, proved: every L transition keeps `L.Invariant`. |
| `DexExpressiveness/Translation/Flow.lean` | The translation `⟦·⟧` (spec §8.2, §8.3). |
| `DexExpressiveness/Translation/Projection.lean` | The projection `π`, the request encoding, `E_adm` of a translated program (spec §5.3). |
| `DexExpressiveness/TraceSets.lean` | The four trace sets the theorem compares. |
| `DexExpressiveness/Theorems.lean` | **The main statements**: `safety_of_isWellFormed`, `liveness_of_isWellFormed`, `expressiveness_of_isWellFormed`. |
| `DexExpressiveness/Simulation/Relation.lean` | `Related`, `simulatingActions`, and the statements `IsSimulatedByActions`, `IsSimulatedByDex`, and `IsSimulatedByL`. Fixed definitions. |
| `DexExpressiveness/Simulation/RelationLemmas.lean` | Proved consequences of the fixed definitions, for every package: a task holds at most one effect, a waiting Step has no invocation, and the action list of each case of `simulatingActions`. |
| `DexExpressiveness/Simulation/LToDexBasic.lean`, `Simulation/LToDexBasicLemmas.lean`, `Simulation/LToDexBasicWake.lean`, `Simulation/LToDexBasicFire.lean` | Work package 2, proved: L to Dex for ticks, requests, timer fires, late emits, and the begin of an action. The frame lemmas of `RunningRelated` are here. |
| `DexExpressiveness/Simulation/Frame.lean` | Proved lemmas that several step simulations share: status and clock, lookup of threads and tasks, the thread that blocked first, which Step carries a thread, what `PhaseMatchesStep` reads. |
| `DexExpressiveness/Simulation/LToDexComplete.lean`, `Simulation/Register.lean`, `Simulation/Completion.lean`, `Simulation/Completion/*.lean` | Work package 3, proved: L to Dex for the registration of a wait (`Register.lean`) and for the completion of an action (`Completion*.lean`). |
| `DexExpressiveness/Simulation/LToDex.lean` | Proved from packages 2 and 3: `l_step_simulatedByActions` and `l_step_simulated`. |
| `DexExpressiveness/Simulation/DexToL.lean`, `Simulation/DexToLLemmas.lean`, `Simulation/DexToLObserved.lean` | Work package 4, proved: `dex_step_simulated`. |
| `DexExpressiveness/Simulation.lean` | Gathers the step simulations. |
| `DexExpressiveness/Liveness/Statements.lean` | The fixed definitions of the liveness proof: silent steps, `fairActions`, `LFollowsDex`, `DexFollowsL`. |
| `DexExpressiveness/Liveness/Silent.lean`, `Liveness/SilentLemmas.lean` | Work package L2, proved: silent Dex steps, the kinds of labels, admissible actions. |
| `DexExpressiveness/Liveness/DexToLRun.lean`, `Liveness/DexToLRunLemmas.lean` | Work package L3, proved: the L run of a Dex run, its word, trace, and marker. |
| `DexExpressiveness/Liveness/DexToLFair.lean`, `Liveness/DexToLFairSteps.lean`, `Liveness/DexToLFairLate.lean`, `Liveness/DexToLFairLemmas.lean` | Work package L4, proved: the L run of a fair Dex run is fair. |
| `DexExpressiveness/Liveness/LToDexRun.lean`, `Liveness/LToDexRunDex.lean`, `Liveness/LToDexRunStep.lean` | Work package L5, proved: the Dex run of an L run, its word, trace, and marker. |
| `DexExpressiveness/Liveness/LToDexFair.lean`, `Liveness/LToDexFairTime.lean`, `Liveness/LToDexFairWorkers.lean` | Work package L6, proved: the Dex run of a fair L run is fair. |
| `DexExpressiveness/Liveness/RestingRuns.lean` | Proved: an L run that comes to rest after a finite schedule is a fair maximal run, with marker `✓`, `δ`, or, for refused inputs forever, `∞`. |
| `DexExpressiveness/Liveness.lean` | Proved from the work packages: the two liveness inclusions, `fairMaximalTrace_dex_to_l` and `fairMaximalTrace_l_to_dex`. |
| `DexExpressiveness/FiniteTraces.lean` | Proved from the step simulations and the two invariants: the two finite-trace inclusions that give safety. |
| `DexExpressiveness/Properties.lean` | Proved facts. No `sorry`. |
| `DexExpressiveness/Examples/*.lean` | Concrete runs checked in the Lean kernel, lockstep checks, executable checks of `IsSimulationState` (`RelatedCheck.lean`), the inductiveness fuzz (`Inductiveness.lean`), and the checks of the liveness definitions (`LivenessChecks.lean`). No `sorry`. |
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
- Liveness compares fair runs only. The fairness assumptions F0 to F6 of
  spec §5.5 are assumptions of the theorem, not guarantees of the engine. In
  particular F4, "Workers settle", assumes cooperative Workers: every task
  emits its effects and returns, or is dropped after its invocation was
  canceled. The Dex server does not guarantee this. A Worker that never
  returns gives an unfair run, about which the theorem says nothing.
- Both trace sets of liveness are inhabited: see
  [Fair maximal traces exist](#fair-maximal-traces-exist).
  The word is quantified in L form and encoded for the Dex side.
  `isAdmissibleWord_encode` and `isAdmissibleWord_decode` (proved) show that
  the encoded admissible L words are exactly the words of `E_adm`.
- A trace is a `Sequence Event`, finite or infinite. A marker is `terminated`
  (`✓`), `deadlocked` (`δ`), or `divergent` (`∞`).
- Both statements range over well-formed programs (spec §0, §5.4, §5.5,
  §8.1): `program.IsWellFormed`. It is a property of the L program under L's
  semantics, quantified over every input and schedule, like data-race
  freedom. The translation plays no part in it. The theorem names carry the
  scope. The scope is not empty: see
  [Well-formed programs exist](#well-formed-programs-exist).
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
| 41 | `L.Request` (carries a `Literal`), `encodeRequest`, `environment`; `literal_isNotHandle`, `isAdmissibleWord_encode`, `isAdmissibleWord_decode`. |

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
   - Rationale [owner]: real programs cannot rely on timing races. A
     program whose outcomes do not depend on the order of two deadlines means
     the same under this timing model as under any other. The theorem adopts
     this one model, the one aligned with Dex, which makes the equivalence
     exact without narrowing what the theorem covers in practice. It is like
     counting one time unit per instruction in algorithm analysis. The
     theorem does not say that a program is equivalent to itself under
     another cost model: other counts can reorder two deadlines and change
     the outcome of a `select`.
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
- Work package 3: `l_thread_register_simulated`, `l_thread_boot_simulated`,
  `l_thread_complete_simulated`, `l_thread_resume_simulated`.
- `l_step_simulatedByActions` and `l_step_simulated`, from the case lemmas.
- Work package 4: `dex_step_simulated`, every Dex step.
- `finiteTrace_dex_to_l`, `finiteTrace_l_to_dex`: the two inclusions of
  safety, from the step simulations and the invariants.
- `literal_isNotHandle`: no request carries a handle.
- `Program.isWellFormed_of_hasStaticLockDiscipline`, and `IsWellFormed` for
  eight example programs (`Examples/WellFormed.lean`); the safety theorem at
  three of them with no hypothesis (`Examples/WellFormedSafety.lean`).
- Fair maximal traces of example programs for each marker, on the L side and
  on the Dex side (`Examples/FairRuns.lean`).
- `safety_of_isWellFormed`: proved, with no `sorry`.
- `fairMaximalTrace_dex_to_l`, `fairMaximalTrace_l_to_dex`: the two
  inclusions of liveness, from work packages L1 to L6 (`Liveness.lean`).
- `liveness_of_isWellFormed`, `expressiveness_of_isWellFormed`: proved, with
  no `sorry`.
- Examples, end to end on concrete traces, by kernel evaluation:
  - `Examples/Mutex.lean`: a two-thread mutex program. Both of its complete
    traces are finite traces of the L program and projected finite traces of
    its Flow (`bothTraces_onBothSides`).
  - `Examples/TimedSelect.lean`: `sleep`, `select` with a timeout, `recv`,
    `select` with a message, `cancel` of a sleeping thread, `join`, and the
    return of `main`, on both sides.
  - `Examples/TimerOrder.lean`: two timers with equal deadlines fire in
    timer-number order on both sides.

## Well-formed programs exist

`Program.IsWellFormed` quantifies over every reachable configuration, so a
concrete trace does not show that a program satisfies it. Without a witness
the theorems could hold for no program at all.

`L/LockDiscipline.lean` defines a decidable, syntactic sufficient condition,
`Program.hasStaticLockDiscipline`: a must-hold lock-set analysis over the
statically known mutexes. `lock(staticMutex i)` adds `i` to the set;
`unlock`, `wait`, `signal`, and `broadcast` need a statically known operand
whose mutex is in the set; the branches of an `if` or a `select` must agree;
a loop body must give back the set it started from; a called or forked
procedure body, a `try` body, and a handler are checked from the empty set.
`L/LockDisciplineSound.lean` proves

```lean
theorem Program.isWellFormed_of_hasStaticLockDiscipline (program : Program)
    (isDisciplined : program.hasStaticLockDiscipline = true) : program.IsWellFormed
```

The proof rests on one fact of L: only the owner of a mutex releases it, so
the steps of other threads never shrink the set of mutexes a thread holds.

`Examples/WellFormed.lean` proves `IsWellFormed` for eight programs, each by
`decide` in the Lean kernel:

| Program | Constructs |
| --- | --- |
| `Mutex.program` | `fork`, `join`, `lock`, `unlock`, `emit`, `exit` |
| `TimedSelect.program` | `sleep`, timed `select`, `recv`, `cancel`, `fork`, `join` |
| `LockstepChecks.signalProgram` | `wait`, `signal`, `lock`, `unlock`, `sleep` |
| `LockstepChecks.broadcastProgram` | `wait` in a `while`, `broadcast`, shared variables |
| `LockstepChecks.recursionProgram` | recursive `call`, `ret`, `if`, `while` |
| `LockstepChecks.exceptionProgram` | `try`, `throw` across a call, an uncaught exception |
| `LockstepChecks.selectRaceProgram` | three threads racing in `select`, `cancel` |
| `WellFormed.createdMutexProgram` | `newMutex`, `lock` of a created mutex, `fail` |

Together they use every construct of L. `Examples/WellFormedSafety.lean`
states the safety theorem at three of them with no hypothesis left.

The discipline is sufficient, not necessary. It tracks statically known
mutexes only, and it empties the lock set after a `call` and after a `try`.
So it rejects `unlock`, `wait`, `signal`, and `broadcast` on a created mutex
(`LockstepChecks.newMutexProgram`), and a program that holds a lock across a
call and unlocks after it. No program that was expected to be well formed
turned out not to be.

## Fair maximal traces exist

The trace sets of liveness are defined by the existence of a fair run (spec
§5.5). Without a fair run of some program, `liveness_of_isWellFormed` would
compare two empty sets.

`Liveness/RestingRuns.lean` builds fair maximal runs of L. A configuration is
at rest (`L.Config.isAtRest`) when every thread is blocked without a timer or
halted, and no late emit is left. An L run that reaches a configuration at
rest by a finite schedule, and then takes only steps that keep it at rest
while time diverges, is fair (`IsRestingTail.isLFairMaximalTrace`). Two
tails are built: clock ticks alone, and clock ticks with inputs that are
refused after the close.

`Examples/FairRuns.lean` gives the witnesses. Each schedule is checked by
evaluation in the Lean kernel. Each L trace is also a projected trace of the
translated Flow, by `liveness_of_isWellFormed` at a well-formed program and
an admissible word:

| Marker | Program | Word | Trace | L side | Dex side |
| --- | --- | --- | --- | --- | --- |
| `✓` | `Mutex.program` | the start | `begin`, both emits, `exit` | `mutexProgram_terminates` | `mutexProgram_terminates_dex` |
| `δ` | `waitingProgram`: `emit(1); recv(5)` | the start | `begin`, `out(1)` | `waitingProgram_deadlocks` | `waitingProgram_deadlocks_dex` |
| `δ` | `selfDeadlockProgram`: `lock(m0); emit(1); lock(m0)` | the start | `begin`, `out(1)` | `selfDeadlockProgram_deadlocks` | `selfDeadlockProgram_deadlocks_dex` |
| `∞` | `Mutex.program` | the start, then one input forever | the `✓` trace, then `refused` forever | `mutexProgram_diverges` | `mutexProgram_diverges_dex` |

The `∞` witness has an infinite word (spec §5.5: "With an infinite `w`,
every fair run has infinitely many inputs and is `∞`"). There is no witness
of a program that runs forever on a finite word, such as a loop that emits:
its configurations never come to rest, and its run is not a finite schedule
followed by a regular tail. There is also no general lemma that every
reachable configuration of a well-formed program has a fair maximal
continuation.

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

Empty for the expressiveness proof. Stage 1 left 12 `sorry`, Stage 2a proved
the 10 of safety, and Stage 2b proved the 2 of liveness through the 32
statements of its work packages. The failure-transparency proof has its own
inventory: see
[The `sorry` inventory of Stage 1](#the-sorry-inventory-of-stage-1).

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

Stage 2a proved `safety_of_isWellFormed`. Stage 2b proved liveness. No Batteries and no Mathlib: the list facts needed are in Lean core
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
| 7 | `dex_step_simulated` | Proved, work package 4, `Simulation/DexToL.lean`. |
| 8 | `l_step_simulated` | Proved from the case lemmas of work packages 2 and 3, `Simulation/LToDex.lean`. |
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
copies, keeps about 190,000, and reports no failure. On the finished proof,
`inductiveness-fuzz 200 4 6 3` changes 5,876 pairs into 1,820,177 copies and
reports no failure. It checks single steps
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

All four packages are proved. None changed a definition of a model file
(`Dex/Syntax.lean`, `Dex/Config.lean`, `Dex/MetaFunctions.lean`,
`Dex/Rules.lean`, `L/Syntax.lean`, `L/ThreadLocal.lean`, `L/Semantics.lean`,
`Translation/*.lean`), a definition of `TraceSets.lean`, or a statement of
`Theorems.lean`.

| Package | Files | Lemmas |
| --- | --- | --- |
| 1. L invariant | `L/InvariantStep*.lean` | `L.Invariant.step` |
| 2. L to Dex, basic steps | `Simulation/LToDexBasic*.lean` | `l_tick_simulated`, `l_start_simulated`, `l_input_simulated`, `l_fire_simulated`, `l_lateDeliver_simulated`, `l_lateDrop_simulated`, `l_thread_begin_simulated`, `l_thread_deliver_simulated` |
| 3a. L to Dex, registration | `Simulation/Register.lean`, `Simulation/LToDexComplete.lean` | `l_thread_register_simulated` |
| 3b. L to Dex, completion | `Simulation/Completion.lean`, `Simulation/Completion/*.lean`, `Simulation/LToDexComplete.lean` | `l_thread_boot_simulated`, `l_thread_complete_simulated`, `l_thread_resume_simulated` |
| 4. Dex to L | `Simulation/DexToL.lean`, `Simulation/DexToLLemmas.lean`, `Simulation/DexToLObserved.lean` | `dex_step_simulated` |

Every L to Dex lemma has the form

```lean
theorem l_..._simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    ... (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig label = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig label lEvents lNext
```

The packages proved the same helper lemmas separately. One copy of each is
kept, under a name without a package prefix: in `Simulation/Frame.lean`
(lookup of threads and tasks, `earliestBlocked`, `StepCarriesThread`,
`PhaseMatchesStep`, status and clock), in `Dex/RunInvariant.lean` (`plan` and
`eval` of quiet waits, what `eval` and `effects` keep), in
`Dex/RunLemmas.lean` (`get?` of a filtered map, the head of a stable sort),
and in `Core/TransitionSystem.lean` (`exec_append`, `exec_snoc`). A lemma
that only one package uses keeps its prefix (`wp2_`, `wp3a_`, `wp3b_`,
`wp4_`).

## Stage 2b: liveness

Liveness (spec §5.5) is the equality of fair maximal traces with markers.
Each inclusion builds an infinite run of one side from an infinite run of the
other with the step simulations of Stage 2a, and then carries over the word,
the trace, the marker, and the fairness assumptions F1 to F5.

### The fixed definitions

| Definition | Where | What it is |
| --- | --- | --- |
| `BlockRun`, `BlockRun.HasInfinitelyManySteps`, `BlockRun.IsFlattenedTo`, `IsBlockRun` | `Core/BlockRun.lean`, lines 23 to 66 | A run given as one finite list of labels per step of the other side, and its flattening into an `InfiniteRun`. |
| `Dex.Action.isSilentKind`, `Dex.Task.silentWeight`, `Dex.Config.silentWeight` | `Liveness/Statements.lean`, lines 39 to 57 | The Dex steps L may not see (running a handler, returning it, dropping a task), and a number that every one of them lowers. |
| `cleanupActions`, `fairActions` | `Liveness/Statements.lean`, lines 65 to 78 | `fairActions` is `simulatingActions` followed by the [WORKER-ABANDON] of every canceled task that holds no effect. |
| `LFollowsDex`, `LRunOfDexRun` | `Liveness/Statements.lean`, lines 84 to 114 | The L run in blocks of length 0 or 1 that simulates a Dex run step by step, and its flattening. |
| `DexFollowsL`, `DexRunOfLRun` | `Liveness/Statements.lean`, lines 118 to 153 | The Dex run in blocks of `fairActions` that simulates an L run step by step, and its flattening. |

Two points of the design:

- A Dex run can take steps that L does not see. They are all of a silent
  kind, and each lowers the silent weight, so infinitely many progress steps
  of the Flow include infinitely many that L sees. This gives the marker `∞`.
- After the close, the tasks of the canceled Steps stay in `Θ`. L has no step
  for a task that holds no effect, and F4 says that every task eventually
  leaves `Θ`. So the Dex run built from an L run abandons these tasks right
  after the step that left them. Between two blocks no such task and no
  `Returned` task is left (`DexFollowsL.isSettled`, `noReturnedTask`).

### Work packages

`Liveness.lean` proves `fairMaximalTrace_dex_to_l` and
`fairMaximalTrace_l_to_dex` from the statements below. All six packages are
proved. None changed `Liveness/Statements.lean`, the definitions of
`Core/BlockRun.lean`, or a definition or a theorem statement of Stage 2a.
Some files of Stage 2a did change after Stage 2a: `Core/TransitionSystem.lean`
gained lemmas about finite runs (`exec_append`, `exec_cons`, `exec_snoc`,
`exec_append_inv`, `eventsAt_of_step`), `Dex/RunInvariant.lean`,
`Dex/RunLemmas.lean`, `Simulation/Frame.lean`, and `Properties.lean` gained
shared lemmas, the comment on the timing model in `L/Semantics.lean` was
reworded (spec revision 8), and `Theorems.lean` imports `Liveness.lean`.

One fixed statement changed while it was proved.
`silentWeight_lt_of_isSilentKind` (`Liveness/Silent.lean`) now assumes
`TasksAreDistinct config`. Without it the statement is false: with two tasks
of one ID, one `Started` and one `Returned`, `workerRun` changes both to
`Running` (`updateTask` changes every task with the ID), and the weight
stays 4. Every configuration of a run has distinct task IDs
(`TasksAreDistinct.step`), so the liveness proof loses nothing. The checks below did not find this, because they start from pairs
that the mirror of `IsSimulationState` accepts, and those have distinct task
IDs.

The packages proved some helper lemmas more than once. One copy of each is
kept: `exec_append_inv` and `eventsAt_of_step` in `Core/TransitionSystem.lean`;
`BlockRun.IsFlattenedTo.boundary_mono` and `.exists_boundary_ge` in
`Core/BlockRun.lean`; the status of a run under each meta-function
(`Dex.Run.status_*`) in `Dex/RunInvariant.lean`, also for Stage 2a; the
lookup of a task after a change of `Θ` (`findTask?_removeTask`,
`findTask?_updatePhase`) in `Simulation/Frame.lean`; and
`Literal.toVal_injective` and `encodeRequest_injective` in `Properties.lean`.
The lemmas of `Liveness/LToDexFairWorkers.lean` lost their `workers_` prefix.
`Dex.Action.isWorkerStep` (`Liveness/LToDexFairTime.lean`) and
`Dex.Action.isSilentKind` stay apart: they differ on [WORKER-EFFECT], which
changes only a task and yet is seen by L.

Work package L4 added one fact about L runs: a configuration of an L run
that is not closed has no late emit (`L.lateEmits_eq_nil_of_isRun`,
`Liveness/DexToLFairLate.lean`). `L.Invariant` does not say so, and F4 of L
speaks about every configuration with a late emit.

| Package | File | Uses | Content |
| --- | --- | --- | --- |
| L1 | `Core/BlockRun.lean`, `Core/BlockRunFlattening.lean` | nothing | The flattening exists; it keeps the trace, the attempted requests, and whether the progress steps are finitely many. |
| L2 | `Liveness/Silent.lean`, `Liveness/SilentLemmas.lean` | Stage 2a | Silent actions lower the silent weight; the L labels of a Dex step are progress steps exactly when the action is one; every action of a run for an admissible word is admissible. |
| L3 | `Liveness/DexToLRun.lean`, `Liveness/DexToLRunLemmas.lean` | L1, L2 | The L run of a Dex run exists (`dex_step_simulated`), attempts the same word, has the same trace and the same marker. |
| L4 | `Liveness/DexToLFair.lean`, `Liveness/DexToLFair{Steps,Late,Lemmas}.lean` | L1, the definitions | The L run of a fair Dex run is fair, one lemma per assumption. |
| L5 | `Liveness/LToDexRun.lean`, `Liveness/LToDexRun{Dex,Step}.lean` | L1, L2 | The Dex run of an L run exists (`l_step_simulatedByActions` and the cleanup), attempts the same word, has the same trace and the same marker. |
| L6 | `Liveness/LToDexFair.lean`, `Liveness/LToDexFair{Time,Workers}.lean` | L1, the definitions | The Dex run of a fair L run is fair, one lemma per assumption. |

### Tested before proving

`Examples/LivenessChecks.lean` tests the definitions on the configuration
pairs of pseudo-random lockstep runs, on the pairs of runs in which the Dex
side takes `fairActions`, and on the changed copies of all of them that the
mirror of `IsSimulationState` accepts:

1. Fair steps: from a settled pair, `fairActions` of every enabled L label
   are enabled, have the events of the label, and end in a settled pair that
   `checkState` accepts.
2. Fairness correspondence: a thread whose next action has not begun has a
   pending invocation (F3); a thread that issued an emit or began an action
   has a task, with an effect exactly when it issued an emit (F4, F5); a due
   timer is a due timer (F2); after the close a late emit is an effect of a
   task (F4).
3. Silent steps: every action of a silent kind lowers the silent weight, a
   clock tick keeps it, a step with no L label is of a silent kind, and the L
   labels are progress steps exactly when the action is one.
4. Quiescence: in a settled pair, L has a step other than a clock tick or a
   request exactly when the translated Flow has one. The markers `✓` and `δ`
   compare this.

`lake exe inductiveness-fuzz 200 4 6 3 liveness` checks 1,396,813 pairs,
1,205,008 of them settled and 408,933 settled after the close, and reports no
failure. The run on the finished proof gives the same numbers. Five `#guard` lines run the checks on few pairs when the file is
built.

Each check finds a planted fault (`120 5 6 2 liveness`; the fault was removed
afterwards):

| Check | Planted fault | Failures | Reported as |
| --- | --- | --- | --- |
| 1 | `cleanupActions` also abandons the tasks that hold a late emit | 42 | the fair step of the closing thread: `Related.lateEmits` |
| 2 | the mirror of `PhaseMatchesStep` asks no task of a `waitStarted` thread | 18 | correspondence F3, and F4 and F5 |
| 3 | a `Started` task weighs 2, like a `Running` one | 4,104 | `workerRun`: the weight does not fall |
| 4 | `cleanupActions` abandons only `Returned` tasks | 60 | quiescence: L is quiescent, the Flow is not |

The checks found no fault in the definitions. They test facts about one pair
or one step. They cannot test a statement about an infinite run.

## Stage 2 outline

1. Done: work packages 1 to 4 above give `safety_of_isWellFormed`.
2. Done: work packages L1 to L6 above give `fairMaximalTrace_dex_to_l` and
   `fairMaximalTrace_l_to_dex`, and with them `liveness_of_isWellFormed` and
   `expressiveness_of_isWellFormed`.

## Failure transparency (issue 1514), Stage 1

The second theorem: a Flow that goes through server crashes, takeovers,
Worker retries, zombie Workers, and duplicate deliveries has the same
observable behavior as without failures, except that external effects may
repeat. The spec is
[`dex-core-semantics.md`](../semantics/dex-core-semantics.md) Section 11 and
Appendix A, items 42 to 70. "Spec §11.x" below means that section.

Stage 1 is the definitions and the statements, with no proofs. DexF is the
Lean model of spec §11: the failure-free Dex model of the files above plus
the state of §11.2 and the rules of §11.3. The abstraction map α of §11.6
maps every DexF configuration to a Dex configuration. The statements say
that every DexF step maps to a list of Dex steps with the same events less
the repeated effects, and they lift this to traces. Stage 2 proves them in
the work packages of plan §5, without changing the definitions.

No definition or theorem of the expressiveness proof changed. The new files
import the existing modules. `DexExpressiveness.lean` imports the new
modules, and `lakefile.toml` has a new executable `failure-fuzz`, whose
binary `formal/lean/.lake/build/bin/failure-fuzz` is in `.gitignore` and
`.dockerignore`.

### Files

| File | Content | Status |
| --- | --- | --- |
| `Failure/Config.lean` | `DexF.Config` (spec §11.2): the failure-free run as `Run.base`, attempt states (`Attempt`: Backoff, attempt number, retained snapshot, `ctxA` inputs), retry timers, `Z`, `I`, `up`, and the ghosts `h`, `v`, `par`, `ps₀`, `n`, `ζ` (`Ghost`). `Program`, the handlers as a function of `ctxA`. | Fixed |
| `Failure/Rules.lean` | `Request`, `Obs` (with the ghost index of `fx`), `Action`, and `DexF.step` (spec §11.3): every failure-free rule with its attempt bookkeeping, and [CRASH], [RECOVER], [RECOVER-INTERRUPT], [ATTEMPT-LOST] (Running and before-start), [ATTEMPT-EXHAUSTED], [ATTEMPT-RETURNED], [ATTEMPT-HEARTBEAT], [RESULT-STALE], [RETRY-FIRE], [ZOMBIE-EFFECT], [START-RETRY], [ENGINE-REJECT]. | Fixed |
| `Failure/Repeats.lean` | `erase` with the ghost index (`eraseRepeats`, `IsRepeatErasure`, `IsErasedProjection`) and repetition with real labels (`IsRepetition`, and `IsOutRepetition` for L), spec §11.4 and §11.5. A Boolean check of repetition and its soundness, proved. | Fixed |
| `Failure/Abstraction.lean` | α (`abstractConfig`, spec §11.6): virtual tasks from `h`, `v`, `par`; `seq` relabeled by `n` and `ζ`; canceled invocations keep a virtual task while a real task remains. | Fixed |
| `Failure/Relation.lean` | The transition map `dexActionsOf`, `eraseStepEvents`, `IsStepSimulated`, `SimulationKind`, the effect history `HasEffectHistory`, and the invariants I1 to I7 (`Invariant`, `RunInvariant`). | Fixed |
| `Failure/Observation.lean` | `DexF.system` with the progress rule of spec §11.5; outcome words, `IsAdmissibleOutcomeWord`, `w↓` (`eraseRetries`, `IsRetryErasure`); fairness F1 to F6, FF, FR, FI (`DexF.IsFair`); the hypotheses H1 to H4, SYNC, D6, HP; the trace sets `IsFailureFiniteTrace`, `IsFreeFiniteTrace`, `IsFailureFairMaximalTrace`. | Fixed |
| `Failure/Statements.lean` | The statements, one group per work package. The only file with `sorry`. | Statements |
| `Failure/Equality.lean`, `Failure/Check.lean` | Test tooling: decidable equality of configurations; `checkStepSimulated`, the decision of `IsStepSimulated`; `checkInvariant`, the Boolean mirror of `Invariant`, clause by clause. | Tooling |
| `Failure/Examples/Script.lean`, `Failure/Examples/CrashRetryZombie.lean` | Two DexF runs of `main(x): emit(1); exit(0)`, checked in the kernel: one with a crash, two retries, a zombie effect, an interrupt, a stale result, a zombie drop, and a StartFlow retry; one in which a zombie's result commits before its interrupt, which apply then ignores. | Proved, no `sorry` |
| `Failure/Examples/Fuzz.lean`, `FailureFuzz.lean` | The inductiveness fuzz and its executable `lake exe failure-fuzz`. | Tooling |

A reviewer reads, in order: `Failure/Config.lean`, `Failure/Rules.lean`,
`Failure/Repeats.lean`, `Failure/Abstraction.lean`,
`Failure/Relation.lean`, `Failure/Observation.lean`, and
`Failure/Statements.lean`.

### The statements

From `DexExpressiveness/Failure/Statements.lean`. The trace sets:
`IsFailureFiniteTrace program environment requests trace` is `Tr_fail` for
finite traces (a DexF run with H1 and H4, for an admissible outcome-word
prefix); `IsFreeFiniteTrace flow environment requests trace` is `Tr_free`;
`IsFailureFairMaximalTrace program word trace marker` is `Tr_fail` for fair
maximal traces (DexF fairness with FF, FR, FI, and H1, H4, D6, HP).

```lean
theorem safety_failure_forward (program : Program σ)
    (hasNoFailurePolicy : HasNoFailurePolicy program) (isSyncOnly : IsSyncOnly program)
    (ignoresAttemptContext : IgnoresAttemptContext program) (environment : Dex.Environment σ)
    (requests : List (Request σ)) (trace : List (Obs σ))
    (isTrace : IsFailureFiniteTrace program environment requests trace) :
    IsFreeFiniteTrace program.failureFreeFlow environment (eraseRetries requests)
      (eraseRepeats trace)

theorem liveness_failure (program : Program σ)
    (hasNoFailurePolicy : HasNoFailurePolicy program) (isSyncOnly : IsSyncOnly program)
    (ignoresAttemptContext : IgnoresAttemptContext program) (environment : Dex.Environment σ)
    (word : Sequence (Request σ)) (isAdmissible : IsAdmissibleOutcomeWord environment word)
    (erasedWord : Sequence (Dex.Request σ)) (isErasure : IsRetryErasure word erasedWord)
    (trace : Sequence (Dex.Obs σ)) (marker : Marker) :
    (∃ failureTrace, IsFailureFairMaximalTrace program word failureTrace marker ∧
        IsRepeatErasure failureTrace trace) ↔
      (Dex.system program.failureFreeFlow).IsFairMaximalTrace Dex.IsFair some erasedWord trace
        marker

theorem endToEnd_safety (program : L.Program) (isWellFormed : program.IsWellFormed) :
    (∀ lTrace, IsLFiniteTrace program lTrace →
      ∃ requests failureTrace,
        IsFailureFiniteTrace (Program.ofFlow ⟦program⟧) environment requests failureTrace ∧
        failureTrace.filterMap (fun event => project event.toDex) = lTrace) ∧
    (∀ requests failureTrace,
      IsFailureFiniteTrace (Program.ofFlow ⟦program⟧) environment requests failureTrace →
      ∃ lTrace, IsLFiniteTrace program lTrace ∧
        IsOutRepetition (failureTrace.filterMap (fun event => project event.toDex)) lTrace)
```

| Package (plan §5) | Statement | What it says |
| --- | --- | --- |
| WP-F1 | `normalStep_simulated` | A normal step (`SimulationKind.normal`) satisfies `IsStepSimulated`. |
| WP-F2 | `failureStep_isStutter` | A failure step other than [ATTEMPT-EXHAUSTED] maps to no Dex action, no event, and the same abstraction. |
| WP-F3 | `workerStep_simulated` | A Worker step, drop, or stale result satisfies `IsStepSimulated`: first occurrences map to the virtual task, repeats to stutters. |
| WP-F3 | `effectHistory_of_exec` | On every finite run the `fx` indexes of `(e, m)` are exactly `1, …, h(e, m)`, each with the label `fx⃗(e, m)[j]`. This ties `eraseStepEvents` to `erase`. |
| WP-F4 | `invariant_initial` (proved), `invariant_step` | The invariants hold initially and after every step. |
| WP-F1 to F3 | `step_simulated` (proved from the three) | Every step other than [ATTEMPT-EXHAUSTED] is simulated. |
| WP-F5 | `freeFiniteTrace_isFailureFiniteTrace` | The Lemma (no failures): `Tr_free ⊆ Tr_fail`. |
| WP-F5 | `safety_failure_forward` | `erase(t)` is a finite Dex trace for `w↓`. |
| WP-F5 | `safety_failure_repetition` | `t` is a repetition of a finite Dex trace for `w↓`. |
| WP-F6 | `isProgress_eq_dexProgress` | The progress Boolean of an enabled label is the progress rule of spec §11.5: its image under the transition map has a Dex progress step. So the DexF marker is the spec's. |
| WP-F6 | `liveness_failure` | Fair maximal traces with markers, per admissible outcome word, are equal after `erase`. |
| WP-F7 | `endToEnd_safety`, `endToEnd_liveness` | Composition with `expressiveness_of_isWellFormed` for well-formed L programs: finite traces in the `out(x)` repetition form, fair maximal traces in the erase-then-project form. |

Every step lemma and theorem assumes H3, `IgnoresAttemptContext program`.
I2 and I3 are false without it.

`#print axioms` on every statement shows `propext` and `sorryAx` only;
`invariant_initial`, `isRepetitionAfter_sound`, and every theorem of
`Failure/Examples/CrashRetryZombie.lean` show no `sorryAx`.

### Lean decisions

Each is the reading closest to the code-backed rule of the spec. Each is
marked "Lean decision" at its definition.

1. **H3 is a hypothesis, not a restriction.** `Dex.Context` has no
   attempt-varying field, so a plain Flow definition would make H3 hold by
   construction and leave no room for a counterexample (plan §4.1). A DexF
   `Program` is `AttemptContext σ → Dex.FlowDef σ`, a task carries its
   `ctxA`, and `IgnoresAttemptContext` says the program is constant. DexF is
   compared with `program.failureFreeFlow`, the program at
   `AttemptContext.first`; [FLOW-START] reads `skipWaitFor` from it.
2. **One place for `P | R`.** The run keeps the failure-free `Dex.Run` as
   `Run.base`, so every meta-function of spec §4.2 applies verbatim. `R` is
   `Dex.InvocationState.running`; `P` and `B` are both `pending`, told apart
   by `Attempt.isBackoff`. After each commit `reconcileAttempts` gives a new
   invocation a fresh attempt state and drops the state of a cleared one.
3. **Ghosts not named in the spec.** `InvocationHistory.outcome` (live,
   committed, canceled), because spec §11.6 needs the difference after
   `finish` clears the invocation; `Ghost.lastNormalCommitAt`, "the stamp of
   every normal commit" that α keeps; and the virtual task ID inside `par`,
   with `Ghost.nextVirtualTaskId`, because `Dex.Invocation` names its task.
   `par` also keeps the handler context, which is the same for every
   attempt.
4. **`ps₀` is kept but unread.** The Lean `Dex.Invocation` has no `ps`, and
   spec §11.6 says α may ignore it in the fragment.
5. **Progress labels.** `TransitionSystem.isProgress` reads the label only.
   The labels whose image under the transition map depends on the state carry
   that classification as a Boolean (`isSnapshotReused`, `isFirstRun`,
   `isFirstOccurrence`, `isFirstReturn`, `isLastOfCanceled`,
   `isWaitTimerFired`), and `DexF.step` rejects a label whose Boolean disagrees
   with the state. `Core/TransitionSystem.lean` is unchanged.
6. **Retention, RunPurge, and `Rc` are left out.** In the fragment the only
   observable consequence of a purge is that a later publication gets
   [ENGINE-REJECT] instead of the apply rejection, with the same event and no
   state change; HP orders every StartFlow retry before RunPurge; the
   housekeeping commands are stutters. So HP holds by construction,
   `RetriesPrecedePurge` states its consequence, and [ENGINE-REJECT] applies
   only to a publication before the start, which `E_adm` excludes.
7. **StartFlow.** [START-RETRY] is its own label, `startRetry`, whose
   request is the StartFlow; `request (.startFlow ..)` is enabled only
   before the run exists. Every StartFlow sets
   `ignore_already_started_error`; a StartFlow with another request ID is
   rejected with ALREADY_EXISTS and is outside `E_adm`, so it is not modeled.
8. **Timer order.** At equal fire times a wait timer sorts before a retry
   timer (`isDueEntryAtMost`). The model does not compute TimerId bytes, and
   spec §11.3 says the position of a retry timer does not change the wait
   timers' effects.
9. **[ATTEMPT-EXHAUSTED]** closes with `CloseStatus.failed "WORKER_API_FAIL"`,
   because `Dex.CloseStatus.failed` has no error type, and the failed Step is
   canceled by the close. H1 excludes the rule.
10. **Rejected publication.** It needs `up` (every commit rule does) and takes
    no stamp, as in the failure-free model (Points for the owner, 12).
11. **Internal commits.** A stale fenced command that carries no returned
    task, a bookkeeping command, a duplicate proposal, and a TimersFired
    whose timers are all gone are one label, `internalCommit`, which only
    stamps. [RESULT-STALE] of a returned task is `resultStale`.
12. **Shared Worker labels.** [WORKER-EFFECT] and [ZOMBIE-EFFECT] are the same
    three labels (`workerRun`, `workerEffect`, `workerReturn`) for a task in
    `Θ` or `Z`. `workerAbandon` is [WORKER-ABANDON] of a non-current task of
    `Θ` and the drop of any zombie.
13. **Structural hypotheses.** H2 (`HasNoFailurePolicy`) and SYNC
    (`IsSyncOnly`) are named predicates that are always true: the Lean
    `StepOptions` has no failure policy, and DexF has only the SYNC start.
    The non-empty task ID assumption holds because task IDs are minted
    natural numbers. H4 is a case of H1 in the model: a non-retryable failure
    is the decision `exhausted .nonRetryable`. D6 (`FiredTimersAreDue`) is a
    predicate on runs that the `timerFire` premise enforces.
14. **H1 on labels.** `recoverInterrupt` carries a retry decision. H1 rejects
    every label with the decision Exhausted, also an ignored interrupt, which
    is the same transition as with RetryAt.
15. **I6 and I7.** Spec I6 as written is always true ("or it is stale and its
    firing is a no-op"). The Lean I6 is the form liveness needs: a Backoff
    invocation is pending and its retry timer is armed. I7 is strengthened:
    an interrupt's task ID was minted (`interruptsBelow`), and a running
    invocation's attempt is in `Θ`, in `I`, or the server is down
    (`runningIsTracked`).
16. **End to end.** Finite traces use the `out(x)` repetition form of spec
    §11.5. Fair maximal traces use the stronger erase-then-project form:
    `π(erase(t))` equals an L trace exactly.

### What DexF leaves out

Besides what the Dex model leaves out: retention timers, RunPurge, and `Rc`
(decision 6); ASYNC starts (D2); Attribute locks and lock queues (D1); the
pending sequence number `ps` (decision 4); StartFlow without
`ignore_already_started_error` or with another request ID (decision 7);
heartbeat and method deadlines, which DexF replaces by unconditioned
failures (spec §11.3, "Over-approximation").

### The `sorry` inventory of Stage 1

Twelve, all in `Failure/Statements.lean`: `normalStep_simulated`,
`failureStep_isStutter`, `workerStep_simulated`, `effectHistory_of_exec`,
`invariant_step`, `freeFiniteTrace_isFailureFiniteTrace`,
`safety_failure_forward`, `safety_failure_repetition`,
`isProgress_eq_dexProgress`, `liveness_failure`, `endToEnd_safety`,
`endToEnd_liveness`. No other file has a `sorry`.

### The executable checks and the inductiveness fuzz

`Failure/Examples/CrashRetryZombie.lean` checks in the kernel, by
`decide +kernel`, that every label of its run is enabled, every step is
simulated, its progress Boolean is the image of the transition map, and the
invariants hold after every step; that the run has a
crash, a retry fire, a start on a retained snapshot, and an effect of a
zombie task; that its trace is in `Tr_fail`; that `erase` of the trace is a
Dex trace for `w↓` and that the trace is a repetition of it; and that its L
projection `begin(7), out(1), out(1), out(1), exit(0)` is the L trace
`begin(7), out(1), exit(0)` with two inserted `out(1)`. A second run takes
the order that spec §11.3 allows and the walks below never reached: after
[CRASH] and [RECOVER], a zombie returns and its Complete rule commits, and
its [RECOVER-INTERRUPT] is then ignored.

The fuzz walks pseudo-random DexF runs with failures and checks
`IsStepSimulated`, the progress rule (`checkProgressMatches`), and the mirror
of `Invariant` at every step. It then changes every fifth configuration in
one place (a task's counter, phase,
attempt, or place; a history's `h`, `v`, `par`, or outcome; an attempt state;
a retry timer; an interrupt; `up`; a stamp; a counter), skips a copy equal
to its source, keeps the copies that the mirror accepts, and checks every
enabled single step from them, [ATTEMPT-EXHAUSTED] included. It runs on a hand-written Flow with effect
lists longer than one, WaitFor effects, mixed wait and retry timers,
concurrent Steps, and a close that cancels a running Step, and on the
translations of eleven example L programs.

```bash
# Arguments: fuel per walk, sampling period, failure budget per walk, seeds.
lake exe failure-fuzz 300 5 10 12

# The planted fault: a program that breaks H3. It must report failures.
lake exe failure-fuzz 300 5 10 12 planted
```

`failure-fuzz 300 5 10 12` takes 31,160 walk steps: 194 crashes, 170
recoveries, 324 attempt losses, 71 returned attempts, 71 heartbeats, 11
applied and 2 ignored interrupts, 335 retry-only and 68 wait timer fires, 189
starts on a retained snapshot, 27 repeated effects, 22 stale results of
zombies, 14 drops and 2 stale results of the last real task of a canceled
invocation, and 118 StartFlow retries. The walks take every label kind
except three: [ENGINE-REJECT], which applies only before the start;
[ATTEMPT-EXHAUSTED], which only the steps from kept copies take; and the
Complete rule of a zombie, which the second kernel-checked run covers. The
fuzz samples 6,264 configurations, changes them into 465,681 copies that
differ from their source, keeps 84,452, and checks 763,516 single steps from
them. It reports no failure. The planted fault reports 131 failures. Two
`#guard` lines run a small fuzz when the file is built.

#### Inductiveness: gaps found and fixed

| Gap | A configuration that was allowed | Closed by |
| --- | --- | --- |
| Interrupt of an unminted task | `I` holds `(e, m, a, t)` with `t = nextTaskId`; the next [STEP-START] mints task `t` into `Θ`, which breaks "`t ∉ dom(Θ)`" of I7. The first run of the fuzz found it with the change `interrupt.add`. | `Invariant.interruptsBelow`: every interrupt's task ID is below `nextTaskId`. |

The fuzz is evidence, not proof: the work packages prove inductiveness.

### Notes for Stage 2

- The backward direction of `liveness_failure` inserts each retry of `w`
  into a fair Dex run for `w↓`. A [START-RETRY] stamps the clock, so it needs
  a [TICK] before it and one after it. This is always possible: `canCommit`
  is monotone in the clock, and a timer armed later has a later fire time.
- `freeFiniteTrace_isFailureFiniteTrace` should simulate Dex
  [STEP-COMPLETE-STALE] by DexF `workerAbandon`, which takes no stamp, not by
  `resultStale`, which does.
- `effectHistory_of_exec` is what connects the per-step erasure of
  `IsStepSimulated` to `erase` of whole traces in `safety_failure_forward`.
