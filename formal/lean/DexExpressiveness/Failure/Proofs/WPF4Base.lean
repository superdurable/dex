import DexExpressiveness.Failure.Proofs.WPF4Lemmas

/-!
# WP-F4: how the meta-functions of spec §4.2 change Step records

`RecordStep registered old new` says how one Step record may change in a
commit: it keeps its ID and origin, never leaves `EX`, `CO`, `CA` once there,
keeps the `sws` of its wait or registers a wait at `registered`, stays quiet
when quiet, and its invocation either stays (with input, condition results,
and phase), is cleared (and the Step is not in `WF` or `EX`), or becomes a
fresh pending Execute from a phase before `EX`.

`StepsKeep` says a run changes only so, Step by Step; `StepsEvolve` also
allows new Steps with fresh IDs, as `new` creates them. The lemmas show which
meta-functions keep or evolve the Step table.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- Whether a phase is `EX`, `CO`, or `CA`: a phase in which an Execute
history may exist. -/
def isExecuteOrDone : Dex.Phase → Bool
  | .execute | .completed | .canceled => true
  | .waitFor | .waiting => false

/-- A quiet Step: not active and without an invocation. -/
def IsQuietRecord (record : Dex.StepRecord σ) : Prop :=
  record.phase.isActive = false ∧ record.invocation = none

/-- The phase in which an invocation of the method is pended. -/
def phaseOfMethod : Dex.Method → Dex.Phase
  | .waitFor => .waitFor
  | .execute => .execute

/-- How one Step record may change in a commit. -/
structure RecordStep (registered : Option Nat) (old new : Dex.StepRecord σ) : Prop where
  /-- The ID stays. -/
  id : new.id = old.id
  /-- The origin stays. -/
  origin : new.origin = old.origin
  /-- A Step in `EX`, `CO`, or `CA` stays in one of them. -/
  executeKept : isExecuteOrDone old.phase = true → isExecuteOrDone new.phase = true
  /-- A stored wait keeps its `sws`, or it was registered at `registered`. -/
  waitKept : ∀ waitState, new.waitState = some waitState →
    (∃ oldWait, old.waitState = some oldWait ∧ oldWait.registeredSeq = waitState.registeredSeq) ∨
      registered = some waitState.registeredSeq
  /-- A quiet Step stays quiet. -/
  quietKept : IsQuietRecord old → IsQuietRecord new
  /-- The invocation stays, is cleared, or becomes a fresh pending Execute. -/
  invocation :
    (new.invocation = old.invocation ∧ new.input = old.input ∧
      new.conditionResults = old.conditionResults ∧ new.phase = old.phase) ∨
    (new.invocation = none ∧ new.phase ≠ .waitFor ∧ new.phase ≠ .execute) ∨
    (new.invocation = some { method := .execute, state := .pending } ∧ new.phase = .execute ∧
      isExecuteOrDone old.phase = false)

/-- A Step that `new` created: its number is above the old counter and
counted by the new one, it has no wait, and its invocation is pending in the
phase of its method. -/
def IsNewRecord (old new : Dex.Run σ) (record : Dex.StepRecord σ) : Prop :=
  (old.counters.get? record.id.stepType).getD 0 < record.id.number ∧
  record.id.number ≤ (new.counters.get? record.id.stepType).getD 0 ∧
  record.waitState = none ∧
  ∃ method, record.invocation = some { method := method, state := .pending } ∧
    record.phase = phaseOfMethod method

/-- The Step table changes Step by Step, and the rest of the run as a commit
allows. -/
structure StepsKeep (registered : Option Nat) (old new : Dex.Run σ) : Prop where
  /-- Every old Step has a successor. -/
  forward : ∀ record, record ∈ old.steps →
    ∃ changed, changed ∈ new.steps ∧ RecordStep registered record changed
  /-- Every new Step has a predecessor. -/
  backward : ∀ changed, changed ∈ new.steps →
    ∃ record, record ∈ old.steps ∧ RecordStep registered record changed
  /-- The IDs stay. -/
  ids : new.steps.map (fun record => record.id) = old.steps.map (fun record => record.id)
  /-- The counters stay. -/
  counters : new.counters = old.counters
  /-- `seq` stays. -/
  seq : new.seq = old.seq
  /-- Wait timer IDs stay distinct. -/
  timersNodup : (old.timers.map (fun entry => entry.1)).Nodup →
    (new.timers.map (fun entry => entry.1)).Nodup
  /-- The status stays, or the run closed and every Step is quiet. -/
  status : new.status = old.status ∨
    ((∃ status, new.status = .closed status) ∧
      ∀ changed, changed ∈ new.steps → IsQuietRecord changed)

/-- As `StepsKeep`, and new Steps may appear. -/
structure StepsEvolve (registered : Option Nat) (old new : Dex.Run σ) : Prop where
  /-- Every old Step has a successor. -/
  forward : ∀ record, record ∈ old.steps →
    ∃ changed, changed ∈ new.steps ∧ RecordStep registered record changed
  /-- Every new Step has a predecessor or is new. -/
  backward : ∀ changed, changed ∈ new.steps →
    (∃ record, record ∈ old.steps ∧ RecordStep registered record changed) ∨
      IsNewRecord old new changed
  /-- Step IDs stay distinct. -/
  idsNodup : (old.steps.map (fun record => record.id)).Nodup →
    (new.steps.map (fun record => record.id)).Nodup
  /-- The counters only grow. -/
  countersGrow : ∀ stepType,
    (old.counters.get? stepType).getD 0 ≤ (new.counters.get? stepType).getD 0
  /-- `seq` stays. -/
  seq : new.seq = old.seq
  /-- Wait timer IDs stay distinct. -/
  timersNodup : (old.timers.map (fun entry => entry.1)).Nodup →
    (new.timers.map (fun entry => entry.1)).Nodup
  /-- The status stays, or the run closed and every Step is quiet. -/
  status : new.status = old.status ∨
    ((∃ status, new.status = .closed status) ∧
      ∀ changed, changed ∈ new.steps → IsQuietRecord changed)

/-! ## `RecordStep` -/

/-- A record steps to itself. -/
theorem RecordStep.refl (registered : Option Nat) (record : Dex.StepRecord σ) :
    RecordStep registered record record := by
  sorry

/-- Two record steps compose. -/
theorem RecordStep.trans {registered : Option Nat} {first second third : Dex.StepRecord σ}
    (firstStep : RecordStep registered first second) (secondStep : RecordStep registered second third) :
    RecordStep registered first third := by
  sorry

/-- A record step that registers nothing registers at any `seq`. -/
theorem RecordStep.mono {old new : Dex.StepRecord σ} (seq : Nat)
    (isStep : RecordStep none old new) : RecordStep (some seq) old new := by
  sorry

/-! ## `StepsKeep` and `StepsEvolve` -/

/-- A run keeps itself. -/
theorem StepsKeep.refl (registered : Option Nat) (run : Dex.Run σ) :
    StepsKeep registered run run := by
  sorry

/-- Keeping composes. -/
theorem StepsKeep.trans {registered : Option Nat} {first second third : Dex.Run σ}
    (firstKeep : StepsKeep registered first second) (secondKeep : StepsKeep registered second third) :
    StepsKeep registered first third := by
  sorry

/-- Keeping that registers nothing registers at any `seq`. -/
theorem StepsKeep.mono {old new : Dex.Run σ} (seq : Nat) (keep : StepsKeep none old new) :
    StepsKeep (some seq) old new := by
  sorry

/-- Keeping is evolving. -/
theorem StepsKeep.toEvolve {registered : Option Nat} {old new : Dex.Run σ}
    (keep : StepsKeep registered old new) : StepsEvolve registered old new := by
  sorry

/-- Keeping, then evolving, is evolving. -/
theorem StepsKeep.thenEvolve {registered : Option Nat} {first second third : Dex.Run σ}
    (firstKeep : StepsKeep registered first second)
    (secondEvolve : StepsEvolve registered second third) :
    StepsEvolve registered first third := by
  sorry

/-- Evolving that registers nothing registers at any `seq`. -/
theorem StepsEvolve.mono {old new : Dex.Run σ} (seq : Nat) (evolve : StepsEvolve none old new) :
    StepsEvolve (some seq) old new := by
  sorry

/-- A change that keeps the Step table, the counters, `seq`, and the status,
and only drops wait timers, keeps the run. -/
theorem StepsKeep.of_frame {old new : Dex.Run σ} (sameSteps : new.steps = old.steps)
    (sameCounters : new.counters = old.counters) (sameSeq : new.seq = old.seq)
    (timersSublist : new.timers.Sublist old.timers) (sameStatus : new.status = old.status) :
    StepsKeep none old new := by
  sorry

/-! ## The meta-functions -/

/-- `eval(e)` keeps the run, when Step IDs are distinct. -/
theorem stepsKeep_evalStep (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (idsNodup : (run.steps.map (fun record => record.id)).Nodup) :
    StepsKeep none run (run.evalStep step) := by
  sorry

/-- `wake(C)` keeps the run, when Step IDs are distinct. -/
theorem stepsKeep_wake (run : Dex.Run σ) (changed : List σ.Chan)
    (idsNodup : (run.steps.map (fun record => record.id)).Nodup) :
    StepsKeep none run (run.wake changed) := by
  sorry

/-- `effects` keeps the run, when Step IDs are distinct. -/
theorem stepsKeep_applyEffects (run : Dex.Run σ) (effects : Dex.Effects σ)
    (idsNodup : (run.steps.map (fun record => record.id)).Nodup) :
    StepsKeep none run (run.applyEffects effects) := by
  sorry

/-- Publications followed by `wake` keep the run, when Step IDs are distinct
([PUBLISH]). -/
theorem stepsKeep_publish (run : Dex.Run σ) (publications : List (σ.Chan × σ.Val))
    (changed : List σ.Chan) (idsNodup : (run.steps.map (fun record => record.id)).Nodup) :
    StepsKeep none run ((run.applyPublications publications).wake changed) := by
  sorry

/-- `register(e, W)` keeps the run and registers at the run's `seq`, when
Step IDs are distinct and `e` is in `WF` without an invocation. -/
theorem stepsKeep_registerWait (run : Dex.Run σ) (step : Dex.StepExecId σ) (wait : Dex.Wait σ)
    (now : Nat) (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (isReady : ∀ record, record ∈ run.steps → record.id = step →
      record.phase = .waitFor ∧ record.invocation = none) :
    StepsKeep (some run.seq) run (run.registerWait step wait now) := by
  sorry

/-- After `register(e, W)` the Step `e` is no longer in `WF`. -/
theorem registerWait_leaves_waitFor (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (wait : Dex.Wait σ) (now : Nat) (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (isReady : ∀ record, record ∈ run.steps → record.id = step →
      record.phase = .waitFor ∧ record.invocation = none) :
    ∀ changed, changed ∈ (run.registerWait step wait now).steps → changed.id = step →
      changed.phase ≠ .waitFor := by
  sorry

/-- `finish(e, φ')` with `φ' ∈ {CO, CA}` keeps the run. -/
theorem stepsKeep_finishStep (run : Dex.Run σ) (step : Dex.StepExecId σ) (finalPhase : Dex.Phase)
    (isFinal : finalPhase = .completed ∨ finalPhase = .canceled) :
    StepsKeep none run (run.finishStep step finalPhase) := by
  sorry

/-- `close(κ)` keeps the run when every inactive Step has no invocation. -/
theorem stepsKeep_closeRun (run : Dex.Run σ) (status : Dex.CloseStatus)
    (inactiveQuiet : ∀ record, record ∈ run.steps → record.phase.isActive = false →
      record.invocation = none) :
    StepsKeep none run (run.closeRun status) := by
  sorry

/-- `close(κ)` of a running run leaves it closed with every Step quiet, when
every inactive Step has no invocation. -/
theorem closeRun_quiet (run : Dex.Run σ) (status : Dex.CloseStatus)
    (isRunning : run.status = .running)
    (inactiveQuiet : ∀ record, record ∈ run.steps → record.phase.isActive = false →
      record.invocation = none) :
    (run.closeRun status).status = .closed status ∧
      ∀ changed, changed ∈ (run.closeRun status).steps → IsQuietRecord changed := by
  sorry

/-- `decide(e, D)` evolves the run when Step IDs are distinct, the counters
cover every Step number, and every inactive Step has no invocation. -/
theorem stepsEvolve_applyDecision (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (decision : Dex.Decision σ) (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (numbersCounted : ∀ record, record ∈ run.steps →
      record.id.number ≤ (run.counters.get? record.id.stepType).getD 0)
    (inactiveQuiet : ∀ record, record ∈ run.steps → record.phase.isActive = false →
      record.invocation = none) :
    StepsEvolve none run (run.applyDecision step decision) := by
  sorry

/-- After `decide(e, D)` the Step `e` is `CO` without an invocation, when it
existed, Step IDs are distinct, and the counters cover every Step number. -/
theorem applyDecision_completes (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (decision : Dex.Decision σ) (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (numbersCounted : ∀ record, record ∈ run.steps →
      record.id.number ≤ (run.counters.get? record.id.stepType).getD 0)
    (exists_step : ∃ record, record ∈ run.steps ∧ record.id = step) :
    ∀ changed, changed ∈ (run.applyDecision step decision).steps → changed.id = step →
      changed.phase = .completed ∧ changed.invocation = none := by
  sorry

/-- The effect of [TIMER-FIRE] for one timer keeps the run, when Step IDs are
distinct. -/
theorem stepsKeep_fireTimer (run : Dex.Run σ) (timer : Dex.TimerId σ)
    (idsNodup : (run.steps.map (fun record => record.id)).Nodup) :
    StepsKeep none run (run.fireTimer timer) := by
  sorry

/-- `new` on a run evolves it ([FLOW-START] and the moves of a decision). -/
theorem stepsEvolve_newStep (run : Dex.Run σ) (stepType : σ.StepType) (input : Option σ.Val)
    (options : Dex.StepOptions σ) (origin : Dex.Origin σ)
    (numbersCounted : ∀ record, record ∈ run.steps →
      record.id.number ≤ (run.counters.get? record.id.stepType).getD 0) :
    StepsEvolve none run (run.newStep stepType input options origin) := by
  sorry

end DexExpressiveness.DexF.WPF4
