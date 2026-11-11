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
    RecordStep registered record record :=
  ⟨rfl, rfl, fun isExecute => isExecute, fun waitState hasWait => Or.inl ⟨waitState, hasWait, rfl⟩,
    fun isQuiet => isQuiet, Or.inl ⟨rfl, rfl, rfl, rfl⟩⟩

/-- Two record steps compose. -/
theorem RecordStep.trans {registered : Option Nat} {first second third : Dex.StepRecord σ}
    (firstStep : RecordStep registered first second) (secondStep : RecordStep registered second third) :
    RecordStep registered first third := by
  obtain ⟨firstId, firstOrigin, firstExecute, firstWait, firstQuiet, firstInvocation⟩ := firstStep
  obtain ⟨secondId, secondOrigin, secondExecute, secondWait, secondQuiet, secondInvocation⟩ :=
    secondStep
  have notExecute : isExecuteOrDone second.phase = false → isExecuteOrDone first.phase = false := by
    intro isNot
    cases isFirst : isExecuteOrDone first.phase with
    | false => rfl
    | true => rw [firstExecute isFirst] at isNot; cases isNot
  refine ⟨secondId.trans firstId, secondOrigin.trans firstOrigin,
    fun isFirst => secondExecute (firstExecute isFirst), ?_,
    fun isQuiet => secondQuiet (firstQuiet isQuiet), ?_⟩
  · intro waitState hasWait
    rcases secondWait waitState hasWait with ⟨middle, hasMiddle, sameSeq⟩ | isRegistered
    · rcases firstWait middle hasMiddle with ⟨oldWait, hasOld, oldSeq⟩ | isMiddleRegistered
      · exact Or.inl ⟨oldWait, hasOld, oldSeq.trans sameSeq⟩
      · right; rw [← sameSeq]; exact isMiddleRegistered
    · exact Or.inr isRegistered
  · rcases secondInvocation with ⟨sameInvocation, sameInput, sameResults, samePhase⟩ |
      ⟨noInvocation, notWaitFor, notExecute'⟩ | ⟨isPending, isPhase, wasBefore⟩
    · rcases firstInvocation with ⟨firstInv, firstInput, firstResults, firstPhase⟩ |
        ⟨noFirst, notWaitForFirst, notExecuteFirst⟩ | ⟨pendingFirst, phaseFirst, beforeFirst⟩
      · exact Or.inl ⟨sameInvocation.trans firstInv, sameInput.trans firstInput,
          sameResults.trans firstResults, samePhase.trans firstPhase⟩
      · exact Or.inr (Or.inl ⟨sameInvocation.trans noFirst, by rw [samePhase]; exact notWaitForFirst,
          by rw [samePhase]; exact notExecuteFirst⟩)
      · exact Or.inr (Or.inr ⟨sameInvocation.trans pendingFirst, samePhase.trans phaseFirst,
          beforeFirst⟩)
    · exact Or.inr (Or.inl ⟨noInvocation, notWaitFor, notExecute'⟩)
    · exact Or.inr (Or.inr ⟨isPending, isPhase, notExecute wasBefore⟩)

/-- A record step that registers nothing registers at any `seq`. -/
theorem RecordStep.mono {old new : Dex.StepRecord σ} (seq : Nat)
    (isStep : RecordStep none old new) : RecordStep (some seq) old new := by
  obtain ⟨sameId, sameOrigin, executeKept, waitKept, quietKept, invocation⟩ := isStep
  refine ⟨sameId, sameOrigin, executeKept, ?_, quietKept, invocation⟩
  intro waitState hasWait
  rcases waitKept waitState hasWait with isOld | isRegistered
  · exact Or.inl isOld
  · cases isRegistered

/-! ## `StepsKeep` and `StepsEvolve` -/

/-- A run keeps itself. -/
theorem StepsKeep.refl (registered : Option Nat) (run : Dex.Run σ) :
    StepsKeep registered run run :=
  ⟨fun record isMember => ⟨record, isMember, RecordStep.refl registered record⟩,
    fun record isMember => ⟨record, isMember, RecordStep.refl registered record⟩,
    rfl, rfl, rfl, id, Or.inl rfl⟩

/-- Keeping composes. -/
theorem StepsKeep.trans {registered : Option Nat} {first second third : Dex.Run σ}
    (firstKeep : StepsKeep registered first second) (secondKeep : StepsKeep registered second third) :
    StepsKeep registered first third := by
  refine ⟨?_, ?_, secondKeep.ids.trans firstKeep.ids, secondKeep.counters.trans firstKeep.counters,
    secondKeep.seq.trans firstKeep.seq, fun isNodup => secondKeep.timersNodup
      (firstKeep.timersNodup isNodup), ?_⟩
  · intro record isMember
    obtain ⟨middle, isMiddle, firstStep⟩ := firstKeep.forward record isMember
    obtain ⟨changed, isChanged, secondStep⟩ := secondKeep.forward middle isMiddle
    exact ⟨changed, isChanged, firstStep.trans secondStep⟩
  · intro changed isChanged
    obtain ⟨middle, isMiddle, secondStep⟩ := secondKeep.backward changed isChanged
    obtain ⟨record, isMember, firstStep⟩ := firstKeep.backward middle isMiddle
    exact ⟨record, isMember, firstStep.trans secondStep⟩
  · rcases secondKeep.status with sameStatus | isClosed
    · rcases firstKeep.status with firstSame | ⟨⟨closedStatus, isFirstClosed⟩, allQuiet⟩
      · exact Or.inl (sameStatus.trans firstSame)
      · right
        refine ⟨⟨closedStatus, sameStatus.trans isFirstClosed⟩, ?_⟩
        intro changed isChanged
        obtain ⟨middle, isMiddle, secondStep⟩ := secondKeep.backward changed isChanged
        exact secondStep.quietKept (allQuiet middle isMiddle)
    · exact Or.inr isClosed

/-- Keeping that registers nothing registers at any `seq`. -/
theorem StepsKeep.mono {old new : Dex.Run σ} (seq : Nat) (keep : StepsKeep none old new) :
    StepsKeep (some seq) old new := by
  refine ⟨?_, ?_, keep.ids, keep.counters, keep.seq, keep.timersNodup, keep.status⟩
  · intro record isMember
    obtain ⟨changed, isChanged, isStep⟩ := keep.forward record isMember
    exact ⟨changed, isChanged, isStep.mono seq⟩
  · intro changed isChanged
    obtain ⟨record, isMember, isStep⟩ := keep.backward changed isChanged
    exact ⟨record, isMember, isStep.mono seq⟩

/-- Keeping is evolving. -/
theorem StepsKeep.toEvolve {registered : Option Nat} {old new : Dex.Run σ}
    (keep : StepsKeep registered old new) : StepsEvolve registered old new := by
  refine ⟨keep.forward, fun changed isChanged => Or.inl (keep.backward changed isChanged), ?_, ?_,
    keep.seq, keep.timersNodup, keep.status⟩
  · intro isNodup
    rw [keep.ids]
    exact isNodup
  · intro stepType
    rw [keep.counters]
    exact Nat.le_refl _

/-- Keeping that keeps the status, then evolving, is evolving. -/
theorem StepsKeep.thenEvolve_of_sameStatus {registered : Option Nat}
    {first second third : Dex.Run σ} (firstKeep : StepsKeep registered first second)
    (sameStatus : second.status = first.status)
    (secondEvolve : StepsEvolve registered second third) :
    StepsEvolve registered first third := by
  refine ⟨?_, ?_, ?_, ?_, secondEvolve.seq.trans firstKeep.seq,
    fun isNodup => secondEvolve.timersNodup (firstKeep.timersNodup isNodup), ?_⟩
  · intro record isMember
    obtain ⟨middle, isMiddle, firstStep⟩ := firstKeep.forward record isMember
    obtain ⟨changed, isChanged, secondStep⟩ := secondEvolve.forward middle isMiddle
    exact ⟨changed, isChanged, firstStep.trans secondStep⟩
  · intro changed isChanged
    rcases secondEvolve.backward changed isChanged with ⟨middle, isMiddle, secondStep⟩ | isNew
    · obtain ⟨record, isMember, firstStep⟩ := firstKeep.backward middle isMiddle
      exact Or.inl ⟨record, isMember, firstStep.trans secondStep⟩
    · right
      obtain ⟨isAbove, rest⟩ := isNew
      refine ⟨?_, rest⟩
      rw [← firstKeep.counters]
      exact isAbove
  · intro isNodup
    apply secondEvolve.idsNodup
    rw [firstKeep.ids]
    exact isNodup
  · intro stepType
    rw [← firstKeep.counters]
    exact secondEvolve.countersGrow stepType
  · rcases secondEvolve.status with thirdSame | isClosed
    · exact Or.inl (thirdSame.trans sameStatus)
    · exact Or.inr isClosed

/-- Evolving that registers nothing registers at any `seq`. -/
theorem StepsEvolve.mono {old new : Dex.Run σ} (seq : Nat) (evolve : StepsEvolve none old new) :
    StepsEvolve (some seq) old new := by
  refine ⟨?_, ?_, evolve.idsNodup, evolve.countersGrow, evolve.seq,
    evolve.timersNodup, evolve.status⟩
  · intro record isMember
    obtain ⟨changed, isChanged, isStep⟩ := evolve.forward record isMember
    exact ⟨changed, isChanged, isStep.mono seq⟩
  · intro changed isChanged
    rcases evolve.backward changed isChanged with ⟨record, isMember, isStep⟩ | isNew
    · exact Or.inl ⟨record, isMember, isStep.mono seq⟩
    · exact Or.inr isNew

/-- A change that keeps the Step table, the counters, `seq`, and the status,
and only drops wait timers, keeps the run. -/
theorem StepsKeep.of_frame {old new : Dex.Run σ} (sameSteps : new.steps = old.steps)
    (sameCounters : new.counters = old.counters) (sameSeq : new.seq = old.seq)
    (timersSublist : new.timers.Sublist old.timers) (sameStatus : new.status = old.status) :
    StepsKeep none old new := by
  refine ⟨?_, ?_, by rw [sameSteps], sameCounters, sameSeq,
    fun isNodup => List.Nodup.sublist (timersSublist.map _) isNodup, Or.inl sameStatus⟩
  · intro record isMember
    exact ⟨record, by rw [sameSteps]; exact isMember, RecordStep.refl none record⟩
  · intro changed isChanged
    exact ⟨changed, by rw [← sameSteps]; exact isChanged, RecordStep.refl none changed⟩

/-- An update of the Step table in which every changed record steps keeps
the run. -/
theorem stepsKeep_updateStep (registered : Option Nat) (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (change : Dex.StepRecord σ → Dex.StepRecord σ)
    (isStep : ∀ record, record ∈ run.steps → record.id = step →
      RecordStep registered record (change record)) :
    StepsKeep registered run (run.updateStep step change) := by
  have stepOf : ∀ record, record ∈ run.steps →
      RecordStep registered record (if record.id = step then change record else record) := by
    intro record isMember
    split
    · rename_i isId
      exact isStep record isMember isId
    · exact RecordStep.refl registered record
  refine ⟨?_, ?_, ?_, rfl, rfl, fun isNodup => isNodup, Or.inl rfl⟩
  · intro record isMember
    exact ⟨_, List.mem_map_of_mem isMember, stepOf record isMember⟩
  · intro changed isChanged
    obtain ⟨record, isMember, isMapped⟩ := List.mem_map.mp isChanged
    rw [← isMapped]
    exact ⟨record, isMember, stepOf record isMember⟩
  · show (run.steps.map _).map _ = _
    rw [List.map_map]
    apply List.map_congr_left
    intro record isMember
    exact (stepOf record isMember).id

/-! ## The meta-functions -/

/-- The change of `eval(e)` on a waiting record is a record step. -/
theorem recordStep_eval (registered : Option Nat) (record : Dex.StepRecord σ)
    (results : Option (Dex.ConditionResults σ)) (isWaiting : record.phase = .waiting) :
    RecordStep registered record
      { record with
        conditionResults := results
        phase := .execute
        invocation := some { method := .execute, state := .pending } } := by
  refine ⟨rfl, rfl, fun _ => rfl, fun waitState hasWait => Or.inl ⟨waitState, hasWait, rfl⟩,
    ?_, Or.inr (Or.inr ⟨rfl, rfl, by rw [isWaiting]; rfl⟩)⟩
  rintro ⟨isInactive, _⟩
  rw [isWaiting] at isInactive
  cases isInactive

/-- An update of a run whose Step table equals another's, Step by Step. -/
theorem steps_of_update {registered : Option Nat} {run base : Dex.Run σ}
    {step : Dex.StepExecId σ} {change : Dex.StepRecord σ → Dex.StepRecord σ}
    (sameSteps : base.steps = run.steps)
    (isStep : ∀ record, record ∈ run.steps → record.id = step →
      RecordStep registered record (change record)) :
    (∀ record, record ∈ run.steps →
      ∃ changed, changed ∈ (base.updateStep step change).steps ∧
        RecordStep registered record changed) ∧
    (∀ changed, changed ∈ (base.updateStep step change).steps →
      ∃ record, record ∈ run.steps ∧ RecordStep registered record changed) := by
  have keep := stepsKeep_updateStep registered base step change
    (fun record isMember => isStep record (sameSteps ▸ isMember))
  refine ⟨fun record isMember => keep.forward record (sameSteps ▸ isMember), ?_⟩
  intro changed isChanged
  obtain ⟨record, isMember, isRecordStep⟩ := keep.backward changed isChanged
  exact ⟨record, sameSteps ▸ isMember, isRecordStep⟩

/-- `eval(e)` keeps the run, when Step IDs are distinct. -/
theorem stepsKeep_evalStep (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (idsNodup : (run.steps.map (fun record => record.id)).Nodup) :
    StepsKeep none run (run.evalStep step) := by
  rcases Dex.Run.evalStep_cases run step with ⟨isSame, _⟩ |
    ⟨record, waitState, consumeFlags, isFound, isWaiting, _, isEval⟩
  · rw [isSame]
    exact StepsKeep.refl none run
  · obtain ⟨sameIds, sameCounters, sameSeq, _⟩ := Dex.Run.evalStep_frame run step
    obtain ⟨isMember, isId⟩ := mem_of_findStep? isFound
    have isStep : ∀ other, other ∈ run.steps → other.id = step →
        RecordStep none other
          { other with
            conditionResults := some
              { timersDone := waitState.timersDone
                channelMessages :=
                  (Dex.Run.consumeMessages waitState.wait.channels consumeFlags
                    (run.disarmTimersOf step)).2 }
            phase := .execute
            invocation := some { method := .execute, state := .pending } } := by
      intro other isOther isOtherId
      have isRecord := eq_of_id_eq idsNodup isOther isMember (isOtherId.trans isId.symm)
      subst isRecord
      exact recordStep_eval none _ _ isWaiting
    have sameSteps := (Dex.Run.consumeMessages_frame waitState.wait.channels consumeFlags
      (run.disarmTimersOf step)).1
    have parts := steps_of_update (registered := none) sameSteps isStep
    refine ⟨?_, ?_, sameIds, sameCounters, sameSeq,
      fun isNodup => List.Nodup.sublist ((Dex.Run.timers_evalStep_sublist run step).map _) isNodup,
      Or.inl (Dex.Run.status_evalStep run step)⟩
    · intro other isOther
      rw [isEval]
      exact parts.1 other isOther
    · intro changed isChanged
      rw [isEval] at isChanged
      exact parts.2 changed isChanged

/-- Evaluating Steps keeps the run, when Step IDs are distinct. -/
theorem stepsKeep_foldl_evalStep (steps : List (Dex.StepExecId σ)) (run : Dex.Run σ)
    (idsNodup : (run.steps.map (fun record => record.id)).Nodup) :
    StepsKeep none run (steps.foldl Dex.Run.evalStep run) := by
  induction steps generalizing run with
  | nil => exact StepsKeep.refl none run
  | cons step rest inductionHypothesis =>
    have first := stepsKeep_evalStep run step idsNodup
    exact first.trans (inductionHypothesis _ (by rw [first.ids]; exact idsNodup))

/-- `wake(C)` keeps the run, when Step IDs are distinct. -/
theorem stepsKeep_wake (run : Dex.Run σ) (changed : List σ.Chan)
    (idsNodup : (run.steps.map (fun record => record.id)).Nodup) :
    StepsKeep none run (run.wake changed) :=
  stepsKeep_foldl_evalStep _ run idsNodup

/-- Writes keep the run. -/
theorem stepsKeep_applyWrites (run : Dex.Run σ) (writes : List (σ.Key × Option σ.Val)) :
    StepsKeep none run (run.applyWrites writes) := by
  obtain ⟨sameSteps, sameCounters, sameTimers, _, sameSeq, sameStatus, _⟩ :=
    Dex.Run.applyWrites_frame writes run
  exact StepsKeep.of_frame sameSteps sameCounters sameSeq (by rw [sameTimers]; exact List.Sublist.refl _) sameStatus

/-- Publications keep the run. -/
theorem stepsKeep_applyPublications (run : Dex.Run σ) (publications : List (σ.Chan × σ.Val)) :
    StepsKeep none run (run.applyPublications publications) := by
  obtain ⟨sameSteps, sameCounters, sameTimers, sameSeq, sameStatus, _⟩ :=
    Dex.Run.applyPublications_frame publications run
  exact StepsKeep.of_frame sameSteps sameCounters sameSeq (by rw [sameTimers]; exact List.Sublist.refl _) sameStatus

/-- `effects` keeps the run, when Step IDs are distinct. -/
theorem stepsKeep_applyEffects (run : Dex.Run σ) (effects : Dex.Effects σ)
    (idsNodup : (run.steps.map (fun record => record.id)).Nodup) :
    StepsKeep none run (run.applyEffects effects) := by
  have writes := stepsKeep_applyWrites run effects.writes
  have publications := stepsKeep_applyPublications (run.applyWrites effects.writes)
    effects.publications
  have both := writes.trans publications
  exact both.trans (stepsKeep_wake _ _ (by rw [both.ids]; exact idsNodup))

/-- Publications followed by `wake` keep the run, when Step IDs are distinct
([PUBLISH]). -/
theorem stepsKeep_publish (run : Dex.Run σ) (publications : List (σ.Chan × σ.Val))
    (changed : List σ.Chan) (idsNodup : (run.steps.map (fun record => record.id)).Nodup) :
    StepsKeep none run ((run.applyPublications publications).wake changed) := by
  have first := stepsKeep_applyPublications run publications
  exact first.trans (stepsKeep_wake _ _ (by rw [first.ids]; exact idsNodup))

/-- The run after the arming and storing part of `register(e, W)`. -/
def registeredRun (run : Dex.Run σ) (step : Dex.StepExecId σ) (wait : Dex.Wait σ) (now : Nat) :
    Dex.Run σ :=
  ({ run with
      timers := Dex.Run.armTimers step
        (wait.timerSeconds.map (fun seconds => now + seconds * 1000000)) 0 run.timers } :
      Dex.Run σ).updateStep step (fun current =>
    { current with
      phase := .waiting
      waitState := some
        { wait := wait
          fireAt := wait.timerSeconds.map (fun seconds => now + seconds * 1000000)
          timersDone := wait.timerSeconds.map (fun _ => false)
          registeredSeq := run.seq } })

/-- `register(e, W)` unfolded. -/
theorem registerWait_eq (run : Dex.Run σ) (step : Dex.StepExecId σ) (wait : Dex.Wait σ)
    (now : Nat) :
    run.registerWait step wait now =
      if wait.hasNoCondition then
        run.updateStep step (fun current =>
          { current with
            conditionResults := some { timersDone := [], channelMessages := [] }
            phase := .execute
            invocation := some { method := .execute, state := .pending } })
      else (registeredRun run step wait now).evalStep step := rfl

/-- The arming and storing part of `register(e, W)` keeps the run. -/
theorem stepsKeep_registeredRun (run : Dex.Run σ) (step : Dex.StepExecId σ) (wait : Dex.Wait σ)
    (now : Nat) (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (isReady : ∀ record, record ∈ run.steps → record.id = step →
      record.phase = .waitFor ∧ record.invocation = none) :
    StepsKeep (some run.seq) run (registeredRun run step wait now) := by
  have armed : StepsKeep (some run.seq) run
      ({ run with
          timers := Dex.Run.armTimers step
            (wait.timerSeconds.map (fun seconds => now + seconds * 1000000)) 0 run.timers } :
        Dex.Run σ) :=
    ⟨fun record isMember => ⟨record, isMember, RecordStep.refl _ record⟩,
      fun record isMember => ⟨record, isMember, RecordStep.refl _ record⟩, rfl, rfl, rfl,
      fun isNodup => Dex.Run.nodup_keys_armTimers step _ 0 run.timers isNodup, Or.inl rfl⟩
  refine armed.trans (stepsKeep_updateStep _ _ step _ ?_)
  intro record isMember isId
  obtain ⟨isWaitFor, noInvocation⟩ := isReady record isMember isId
  refine ⟨rfl, rfl, fun isExecute => ?_, ?_, ?_, Or.inr (Or.inl ⟨noInvocation, ?_, ?_⟩)⟩
  · rw [isWaitFor] at isExecute
    cases isExecute
  · intro waitState hasWait
    cases hasWait
    exact Or.inr rfl
  · rintro ⟨isInactive, _⟩
    rw [isWaitFor] at isInactive
    cases isInactive
  · intro isPhase
    cases isPhase
  · intro isPhase
    cases isPhase

/-- `register(e, W)` keeps the run and registers at the run's `seq`, when
Step IDs are distinct and `e` is in `WF` without an invocation. -/
theorem stepsKeep_registerWait (run : Dex.Run σ) (step : Dex.StepExecId σ) (wait : Dex.Wait σ)
    (now : Nat) (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (isReady : ∀ record, record ∈ run.steps → record.id = step →
      record.phase = .waitFor ∧ record.invocation = none) :
    StepsKeep (some run.seq) run (run.registerWait step wait now) := by
  rw [registerWait_eq]
  split
  · apply stepsKeep_updateStep
    intro record isMember isId
    obtain ⟨isWaitFor, _⟩ := isReady record isMember isId
    refine ⟨rfl, rfl, fun _ => rfl, fun waitState hasWait => Or.inl ⟨waitState, hasWait, rfl⟩,
      ?_, Or.inr (Or.inr ⟨rfl, rfl, by rw [isWaitFor]; rfl⟩)⟩
    rintro ⟨isInactive, _⟩
    rw [isWaitFor] at isInactive
    cases isInactive
  · have registered := stepsKeep_registeredRun run step wait now idsNodup isReady
    exact registered.trans
      ((stepsKeep_evalStep _ step (by rw [registered.ids]; exact idsNodup)).mono run.seq)

/-- After `register(e, W)` the Step `e` is no longer in `WF`. -/
theorem registerWait_leaves_waitFor (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (wait : Dex.Wait σ) (now : Nat) (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (isReady : ∀ record, record ∈ run.steps → record.id = step →
      record.phase = .waitFor ∧ record.invocation = none) :
    ∀ changed, changed ∈ (run.registerWait step wait now).steps → changed.id = step →
      changed.phase ≠ .waitFor := by
  intro changed isChanged isId
  rw [registerWait_eq] at isChanged
  split at isChanged
  · obtain ⟨original, _, isMapped⟩ := List.mem_map.mp isChanged
    by_cases isStep : original.id = step
    · rw [if_pos isStep] at isMapped
      rw [← isMapped]
      intro isPhase
      cases isPhase
    · rw [if_neg isStep] at isMapped
      rw [← isMapped] at isId
      exact absurd isId isStep
  · have registered := stepsKeep_registeredRun run step wait now idsNodup isReady
    obtain ⟨middle, isMiddle, middleStep⟩ :=
      (stepsKeep_evalStep _ step (by rw [registered.ids]; exact idsNodup)).backward changed isChanged
    have isWaiting : middle.phase = .waiting := by
      obtain ⟨original, _, isMapped⟩ := List.mem_map.mp isMiddle
      have middleId : middle.id = step := middleStep.id.symm.trans isId
      by_cases isStep : original.id = step
      · rw [if_pos isStep] at isMapped
        rw [← isMapped]
      · rw [if_neg isStep] at isMapped
        rw [← isMapped] at middleId
        exact absurd middleId isStep
    rcases middleStep.invocation with ⟨_, _, _, samePhase⟩ | ⟨_, notWaitFor, _⟩ |
      ⟨_, isExecute, _⟩
    · rw [samePhase, isWaiting]
      intro isPhase
      cases isPhase
    · exact notWaitFor
    · rw [isExecute]
      intro isPhase
      cases isPhase

/-- `finish(e, φ')` with `φ' ∈ {CO, CA}` keeps the run. -/
theorem stepsKeep_finishStep (run : Dex.Run σ) (step : Dex.StepExecId σ) (finalPhase : Dex.Phase)
    (isFinal : finalPhase = .completed ∨ finalPhase = .canceled) :
    StepsKeep none run (run.finishStep step finalPhase) := by
  have disarmed : StepsKeep none run (run.disarmTimersOf step) :=
    StepsKeep.of_frame rfl rfl rfl List.filter_sublist rfl
  refine disarmed.trans (stepsKeep_updateStep none _ step _ ?_)
  intro record _ _
  refine ⟨rfl, rfl, fun _ => ?_, fun waitState hasWait => Or.inl ⟨waitState, hasWait, rfl⟩,
    fun _ => ⟨?_, rfl⟩, Or.inr (Or.inl ⟨rfl, ?_, ?_⟩)⟩ <;>
    rcases isFinal with isFinalPhase | isFinalPhase <;> subst isFinalPhase <;> first
      | rfl
      | (intro isPhase; cases isPhase)

/-- Canceling Steps keeps the run. -/
theorem stepsKeep_foldl_finishStep (steps : List (Dex.StepExecId σ)) (run : Dex.Run σ) :
    StepsKeep none run (steps.foldl (fun current step => current.finishStep step .canceled) run) := by
  induction steps generalizing run with
  | nil => exact StepsKeep.refl none run
  | cons step rest inductionHypothesis =>
    exact (stepsKeep_finishStep run step .canceled (Or.inr rfl)).trans (inductionHypothesis _)

/-- After canceling a list of Steps, a record is canceled without an
invocation when its ID is in the list, and unchanged otherwise. -/
theorem mem_foldl_finishStep (steps : List (Dex.StepExecId σ)) (run : Dex.Run σ) :
    ∀ changed, changed ∈ (steps.foldl (fun current step => current.finishStep step .canceled)
      run).steps →
      ∃ record, record ∈ run.steps ∧ changed.id = record.id ∧
        ((record.id ∈ steps ∧ changed.phase = .canceled ∧ changed.invocation = none) ∨
          (changed = record ∧ record.id ∉ steps)) := by
  induction steps generalizing run with
  | nil =>
    intro changed isChanged
    exact ⟨changed, isChanged, rfl, Or.inr ⟨rfl, List.not_mem_nil⟩⟩
  | cons step rest inductionHypothesis =>
    intro changed isChanged
    simp only [List.foldl_cons] at isChanged
    obtain ⟨middle, isMiddle, middleId, middleCase⟩ := inductionHypothesis _ changed isChanged
    obtain ⟨record, isMember, isMapped⟩ := List.mem_map.mp isMiddle
    by_cases isStep : record.id = step
    · rw [if_pos isStep] at isMapped
      subst isMapped
      refine ⟨record, isMember, middleId, Or.inl ⟨List.mem_cons.mpr (Or.inl isStep), ?_⟩⟩
      rcases middleCase with ⟨_, isCanceled, noInvocation⟩ | ⟨isSame, _⟩
      · exact ⟨isCanceled, noInvocation⟩
      · rw [isSame]
        exact ⟨rfl, rfl⟩
    · rw [if_neg isStep] at isMapped
      subst isMapped
      refine ⟨record, isMember, middleId, ?_⟩
      rcases middleCase with ⟨isIn, isCanceled, noInvocation⟩ | ⟨isSame, notIn⟩
      · exact Or.inl ⟨List.mem_cons_of_mem _ isIn, isCanceled, noInvocation⟩
      · refine Or.inr ⟨isSame, ?_⟩
        intro isIn
        rcases List.mem_cons.mp isIn with isHead | isRest
        · exact isStep isHead
        · exact notIn isRest

/-- `close(κ)` of a running run: it keeps the run, closes it, and every Step
is quiet after it. -/
theorem closeRun_running (run : Dex.Run σ) (status : Dex.CloseStatus)
    (isRunning : run.status = .running)
    (inactiveQuiet : ∀ record, record ∈ run.steps → record.phase.isActive = false →
      record.invocation = none) :
    StepsKeep none run (run.closeRun status) ∧ (run.closeRun status).status = .closed status ∧
      ∀ changed, changed ∈ (run.closeRun status).steps → IsQuietRecord changed := by
  have isClose : run.closeRun status =
      { (((run.steps.filter (fun record => record.phase.isActive)).map
          (fun record => record.id)).foldl (fun current step => current.finishStep step .canceled)
          run) with status := .closed status } := by
    unfold Dex.Run.closeRun
    rw [isRunning]
  rw [isClose]
  have keep := stepsKeep_foldl_finishStep
    ((run.steps.filter (fun record => record.phase.isActive)).map (fun record => record.id)) run
  have quiet : ∀ changed, changed ∈ (((run.steps.filter (fun record => record.phase.isActive)).map
      (fun record => record.id)).foldl (fun current step => current.finishStep step .canceled)
      run).steps → IsQuietRecord changed := by
    intro changed isChanged
    obtain ⟨record, isMember, _, isCase⟩ := mem_foldl_finishStep _ run changed isChanged
    rcases isCase with ⟨_, isCanceled, noInvocation⟩ | ⟨isSame, notIn⟩
    · exact ⟨by rw [isCanceled]; rfl, noInvocation⟩
    · subst isSame
      have isInactive : changed.phase.isActive = false := by
        cases isActive : changed.phase.isActive with
        | false => rfl
        | true =>
          exact absurd (List.mem_map.mpr ⟨changed, List.mem_filter.mpr ⟨isMember, isActive⟩, rfl⟩)
            notIn
      exact ⟨isInactive, inactiveQuiet changed isMember isInactive⟩
  exact ⟨⟨keep.forward, keep.backward, keep.ids, keep.counters, keep.seq, keep.timersNodup,
    Or.inr ⟨⟨status, rfl⟩, quiet⟩⟩, rfl, quiet⟩

/-- `close(κ)` keeps the run when every inactive Step has no invocation. -/
theorem stepsKeep_closeRun (run : Dex.Run σ) (status : Dex.CloseStatus)
    (inactiveQuiet : ∀ record, record ∈ run.steps → record.phase.isActive = false →
      record.invocation = none) :
    StepsKeep none run (run.closeRun status) := by
  cases isStatus : run.status with
  | running => exact (closeRun_running run status isStatus inactiveQuiet).1
  | closed closedStatus =>
    have isSame : run.closeRun status = run := by
      unfold Dex.Run.closeRun
      rw [isStatus]
    rw [isSame]
    exact StepsKeep.refl none run

/-- `close(κ)` of a running run leaves it closed with every Step quiet, when
every inactive Step has no invocation. -/
theorem closeRun_quiet (run : Dex.Run σ) (status : Dex.CloseStatus)
    (isRunning : run.status = .running)
    (inactiveQuiet : ∀ record, record ∈ run.steps → record.phase.isActive = false →
      record.invocation = none) :
    (run.closeRun status).status = .closed status ∧
      ∀ changed, changed ∈ (run.closeRun status).steps → IsQuietRecord changed :=
  (closeRun_running run status isRunning inactiveQuiet).2

/-- The members after `close(κ)`, when Step IDs are distinct: unchanged, or
canceled from an active Step. -/
theorem mem_closeRun (run : Dex.Run σ) (status : Dex.CloseStatus)
    (idsNodup : (run.steps.map (fun record => record.id)).Nodup) :
    ∀ changed, changed ∈ (run.closeRun status).steps →
      ∃ record, record ∈ run.steps ∧ changed.id = record.id ∧
        (changed = record ∨ record.phase.isActive = true) := by
  intro changed isChanged
  cases isStatus : run.status with
  | closed closedStatus =>
    have isSame : run.closeRun status = run := by
      unfold Dex.Run.closeRun
      rw [isStatus]
    rw [isSame] at isChanged
    exact ⟨changed, isChanged, rfl, Or.inl rfl⟩
  | running =>
    have isClose : run.closeRun status =
        { (((run.steps.filter (fun record => record.phase.isActive)).map
            (fun record => record.id)).foldl (fun current step => current.finishStep step .canceled)
            run) with status := .closed status } := by
      unfold Dex.Run.closeRun
      rw [isStatus]
    rw [isClose] at isChanged
    obtain ⟨record, isMember, sameId, isCase⟩ := mem_foldl_finishStep _ run changed isChanged
    refine ⟨record, isMember, sameId, ?_⟩
    rcases isCase with ⟨isIn, _, _⟩ | ⟨isSame, _⟩
    · right
      obtain ⟨active, isActiveMember, isActiveId⟩ := List.mem_map.mp isIn
      obtain ⟨isActiveIn, isActive⟩ := List.mem_filter.mp isActiveMember
      rw [← eq_of_id_eq idsNodup isActiveIn isMember isActiveId]
      exact isActive
    · exact Or.inl isSame

/-! ## New Steps -/

/-- The Step table only gains new Steps. -/
structure NewStepsOnly (old new : Dex.Run σ) : Prop where
  /-- Old Steps stay. -/
  kept : ∀ record, record ∈ old.steps → record ∈ new.steps
  /-- Every Step is old or new. -/
  fresh : ∀ record, record ∈ new.steps → record ∈ old.steps ∨ IsNewRecord old new record
  /-- The counters only grow. -/
  countersGrow : ∀ stepType,
    (old.counters.get? stepType).getD 0 ≤ (new.counters.get? stepType).getD 0
  /-- The counters keep covering every Step number. -/
  counted : (∀ record, record ∈ old.steps →
      record.id.number ≤ (old.counters.get? record.id.stepType).getD 0) →
    ∀ record, record ∈ new.steps →
      record.id.number ≤ (new.counters.get? record.id.stepType).getD 0
  /-- Step IDs stay distinct. -/
  idsNodup : (∀ record, record ∈ old.steps →
      record.id.number ≤ (old.counters.get? record.id.stepType).getD 0) →
    (old.steps.map (fun record => record.id)).Nodup →
    (new.steps.map (fun record => record.id)).Nodup
  /-- `seq` stays. -/
  seq : new.seq = old.seq
  /-- The timers stay. -/
  timers : new.timers = old.timers
  /-- The status stays. -/
  status : new.status = old.status

/-- `new` only adds a new Step. -/
theorem newStepsOnly_newStep (run : Dex.Run σ) (stepType : σ.StepType) (input : Option σ.Val)
    (options : Dex.StepOptions σ) (origin : Dex.Origin σ) :
    NewStepsOnly run (run.newStep stepType input options origin) := by
  have countersGrow : ∀ other, (run.counters.get? other).getD 0 ≤
      ((run.newStep stepType input options origin).counters.get? other).getD 0 := by
    intro other
    show _ ≤ ((run.counters.set stepType _).get? other).getD 0
    by_cases isType : other = stepType
    · subst isType
      rw [AssocList.get?_set_self]
      exact Nat.le_succ _
    · rw [AssocList.get?_set_other _ _ _ _ isType]
      exact Nat.le_refl _
  have newCount : ((run.newStep stepType input options origin).counters.get? stepType).getD 0 =
      (run.counters.get? stepType).getD 0 + 1 := by
    show ((run.counters.set stepType _).get? stepType).getD 0 = _
    rw [AssocList.get?_set_self]
    rfl
  refine ⟨fun record isMember => List.mem_append_left _ isMember, ?_, countersGrow, ?_, ?_, rfl, rfl,
    rfl⟩
  · intro record isMember
    rcases List.mem_append.mp isMember with isOld | isNew
    · exact Or.inl isOld
    · right
      rw [List.mem_singleton] at isNew
      subst isNew
      refine ⟨Nat.lt_succ_self _, ?_, rfl, ?_⟩
      · rw [newCount]
        exact Nat.le_refl _
      · refine ⟨if options.skipWaitFor then Dex.Method.execute else Dex.Method.waitFor, rfl, ?_⟩
        cases options.skipWaitFor <;> rfl
  · intro counted record isMember
    rcases List.mem_append.mp isMember with isOld | isNew
    · exact Nat.le_trans (counted record isOld) (countersGrow _)
    · rw [List.mem_singleton] at isNew
      subst isNew
      rw [newCount]
      exact Nat.le_refl _
  · intro counted isNodup
    show ((run.steps ++ _).map _).Nodup
    rw [List.map_append, List.nodup_append]
    refine ⟨isNodup, by simp, ?_⟩
    intro first isFirst second isSecond isSame
    obtain ⟨record, isMember, isRecordId⟩ := List.mem_map.mp isFirst
    rw [List.map_cons, List.map_nil, List.mem_singleton] at isSecond
    subst isSecond
    have isCounted := counted record isMember
    rw [isRecordId, isSame] at isCounted
    exact absurd isCounted (Nat.not_le.mpr (Nat.lt_succ_self _))

/-- Gaining only new Steps composes. -/
theorem NewStepsOnly.trans {first second third : Dex.Run σ} (firstNew : NewStepsOnly first second)
    (secondNew : NewStepsOnly second third) : NewStepsOnly first third := by
  refine ⟨fun record isMember => secondNew.kept record (firstNew.kept record isMember), ?_,
    fun stepType => Nat.le_trans (firstNew.countersGrow stepType) (secondNew.countersGrow stepType),
    fun counted => secondNew.counted (firstNew.counted counted),
    fun counted isNodup => secondNew.idsNodup (firstNew.counted counted)
      (firstNew.idsNodup counted isNodup),
    secondNew.seq.trans firstNew.seq, secondNew.timers.trans firstNew.timers,
    secondNew.status.trans firstNew.status⟩
  intro record isMember
  rcases secondNew.fresh record isMember with isSecond | ⟨isAbove, isBelow, rest⟩
  · rcases firstNew.fresh record isSecond with isFirst | ⟨isAbove, isBelow, rest⟩
    · exact Or.inl isFirst
    · exact Or.inr ⟨isAbove, Nat.le_trans isBelow (secondNew.countersGrow _), rest⟩
  · exact Or.inr ⟨Nat.lt_of_le_of_lt (firstNew.countersGrow _) isAbove, isBelow, rest⟩

/-- Taking moves only adds new Steps. -/
theorem newStepsOnly_foldl_newStep (moves : List (Dex.Move σ)) (origin : Dex.Origin σ)
    (run : Dex.Run σ) :
    NewStepsOnly run (moves.foldl (fun current move =>
      current.newStep move.stepType move.input move.options origin) run) := by
  induction moves generalizing run with
  | nil =>
    exact ⟨fun _ isMember => isMember, fun _ isMember => Or.inl isMember,
      fun _ => Nat.le_refl _, fun counted => counted, fun _ isNodup => isNodup, rfl, rfl, rfl⟩
  | cons move rest inductionHypothesis =>
    exact (newStepsOnly_newStep run _ _ _ _).trans (inductionHypothesis _)

/-- Gaining only new Steps evolves the run, when the counters cover every Step number. -/
theorem NewStepsOnly.toEvolve {old new : Dex.Run σ} (onlyNew : NewStepsOnly old new)
    (counted : ∀ record, record ∈ old.steps →
      record.id.number ≤ (old.counters.get? record.id.stepType).getD 0) :
    StepsEvolve none old new := by
  refine ⟨fun record isMember => ⟨record, onlyNew.kept record isMember, RecordStep.refl none record⟩,
    ?_, onlyNew.idsNodup counted, onlyNew.countersGrow, onlyNew.seq,
    fun isNodup => by rw [onlyNew.timers]; exact isNodup, Or.inl onlyNew.status⟩
  intro changed isChanged
  rcases onlyNew.fresh changed isChanged with isOld | isNew
  · exact Or.inl ⟨changed, isOld, RecordStep.refl none changed⟩
  · exact Or.inr isNew

/-! ## `decide` -/

/-- The records with ID `e` after `finish(e, CO)` are completed without invocation. -/
theorem finishStep_completes (run : Dex.Run σ) (step : Dex.StepExecId σ) :
    ∀ changed, changed ∈ (run.finishStep step .completed).steps → changed.id = step →
      changed.phase = .completed ∧ changed.invocation = none := by
  intro changed isChanged isId
  obtain ⟨original, _, isMapped⟩ := List.mem_map.mp isChanged
  by_cases isStep : original.id = step
  · rw [if_pos isStep] at isMapped
    rw [← isMapped]
    exact ⟨rfl, rfl⟩
  · rw [if_neg isStep] at isMapped
    rw [← isMapped] at isId
    exact absurd isId isStep

/-- The facts about `finish(e, CO)` that `decide` needs. -/
theorem finishStep_facts (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (numbersCounted : ∀ record, record ∈ run.steps →
      record.id.number ≤ (run.counters.get? record.id.stepType).getD 0)
    (inactiveQuiet : ∀ record, record ∈ run.steps → record.phase.isActive = false →
      record.invocation = none) :
    StepsKeep none run (run.finishStep step .completed) ∧
    ((run.finishStep step .completed).steps.map (fun record => record.id)).Nodup ∧
    (∀ record, record ∈ (run.finishStep step .completed).steps →
      record.id.number ≤ ((run.finishStep step .completed).counters.get?
        record.id.stepType).getD 0) ∧
    (∀ record, record ∈ (run.finishStep step .completed).steps →
      record.phase.isActive = false → record.invocation = none) := by
  have keep := stepsKeep_finishStep run step .completed (Or.inl rfl)
  refine ⟨keep, by rw [keep.ids]; exact idsNodup, ?_, ?_⟩
  · intro record isMember
    obtain ⟨original, isOriginal, isStep⟩ := keep.backward record isMember
    rw [isStep.id, keep.counters]
    exact numbersCounted original isOriginal
  · intro record isMember isInactive
    obtain ⟨original, isOriginal, isMapped⟩ := List.mem_map.mp isMember
    by_cases isStep : original.id = step
    · rw [if_pos isStep] at isMapped
      rw [← isMapped]
    · rw [if_neg isStep] at isMapped
      subst isMapped
      exact inactiveQuiet original isOriginal isInactive

/-- `decide(e, D)` evolves the run when Step IDs are distinct, the counters
cover every Step number, and every inactive Step has no invocation. -/
theorem stepsEvolve_applyDecision (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (decision : Dex.Decision σ) (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (numbersCounted : ∀ record, record ∈ run.steps →
      record.id.number ≤ (run.counters.get? record.id.stepType).getD 0)
    (inactiveQuiet : ∀ record, record ∈ run.steps → record.phase.isActive = false →
      record.invocation = none) :
    StepsEvolve none run (run.applyDecision step decision) := by
  obtain ⟨finishKeep, _, finishedCounted, finishedQuiet⟩ :=
    finishStep_facts run step idsNodup numbersCounted inactiveQuiet
  unfold Dex.Run.applyDecision
  split
  · rename_i output _
    have collectKeep : StepsKeep none (run.finishStep step .completed)
        ((run.finishStep step .completed).collectOutput step output) :=
      StepsKeep.of_frame (by cases output <;> rfl) (by cases output <;> rfl)
        (by cases output <;> rfl) (by cases output <;> exact List.Sublist.refl _)
        (by cases output <;> rfl)
    refine (finishKeep.trans (collectKeep.trans (stepsKeep_closeRun _ _ ?_))).toEvolve
    intro record isMember isInactive
    have isFinished : record ∈ (run.finishStep step .completed).steps := by
      have sameSteps : ((run.finishStep step .completed).collectOutput step output).steps =
          (run.finishStep step .completed).steps := by cases output <;> rfl
      rw [← sameSteps]
      exact isMember
    exact finishedQuiet record isFinished isInactive
  · exact (finishKeep.trans (stepsKeep_closeRun _ _ finishedQuiet)).toEvolve
  · exact finishKeep.toEvolve
  · exact finishKeep.thenEvolve_of_sameStatus rfl
      ((newStepsOnly_foldl_newStep _ _ _).toEvolve finishedCounted)

/-- After `decide(e, D)` the Step `e` is `CO` without an invocation, when it
existed, Step IDs are distinct, and the counters cover every Step number. -/
theorem applyDecision_completes (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (decision : Dex.Decision σ) (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (numbersCounted : ∀ record, record ∈ run.steps →
      record.id.number ≤ (run.counters.get? record.id.stepType).getD 0)
    (exists_step : ∃ record, record ∈ run.steps ∧ record.id = step) :
    ∀ changed, changed ∈ (run.applyDecision step decision).steps → changed.id = step →
      changed.phase = .completed ∧ changed.invocation = none := by
  have finishKeep := stepsKeep_finishStep run step .completed (Or.inl rfl)
  have finishedNodup : ((run.finishStep step .completed).steps.map (fun record => record.id)).Nodup :=
    by rw [finishKeep.ids]; exact idsNodup
  have completes := finishStep_completes run step
  have throughClose : ∀ (base : Dex.Run σ) (status : Dex.CloseStatus),
      base.steps = (run.finishStep step .completed).steps →
      ∀ changed, changed ∈ (base.closeRun status).steps → changed.id = step →
        changed.phase = .completed ∧ changed.invocation = none := by
    intro base status sameSteps changed isChanged isId
    obtain ⟨record, isMember, sameId, isCase⟩ :=
      mem_closeRun base status (by rw [sameSteps]; exact finishedNodup) changed isChanged
    rw [sameSteps] at isMember
    have isCompleted := completes record isMember (sameId.symm.trans isId)
    rcases isCase with isSame | isActive
    · rw [isSame]
      exact isCompleted
    · rw [isCompleted.1] at isActive
      cases isActive
  intro changed isChanged isId
  unfold Dex.Run.applyDecision at isChanged
  split at isChanged
  · rename_i output _
    exact throughClose _ _ (by cases output <;> rfl) changed isChanged isId
  · exact throughClose _ _ rfl changed isChanged isId
  · exact completes changed isChanged isId
  · rcases (newStepsOnly_foldl_newStep _ _ _).fresh changed isChanged with isOld |
      ⟨isAbove, _, _⟩
    · exact completes changed isOld isId
    · obtain ⟨record, isMember, isRecordId⟩ := exists_step
      have isCounted := numbersCounted record isMember
      rw [isRecordId, ← isId] at isCounted
      exact absurd isAbove (Nat.not_lt.mpr isCounted)

/-! ## [TIMER-FIRE] and `new` -/

/-- The effect of [TIMER-FIRE] for one timer keeps the run, when Step IDs are
distinct. -/
theorem stepsKeep_fireTimer (run : Dex.Run σ) (timer : Dex.TimerId σ)
    (idsNodup : (run.steps.map (fun record => record.id)).Nodup) :
    StepsKeep none run (run.fireTimer timer) := by
  have disarmed : StepsKeep none run { run with timers := run.timers.erase timer } :=
    StepsKeep.of_frame rfl rfl rfl (AssocList.erase_sublist run.timers timer) rfl
  unfold Dex.Run.fireTimer
  split
  · simp only
    split
    · exact disarmed
    · rename_i record isFound
      obtain ⟨isMember, isId⟩ := mem_of_findStep? isFound
      split
      · rename_i waitState _ hasWait
        split
        · exact disarmed
        · have update := stepsKeep_updateStep none { run with timers := run.timers.erase timer }
            timer.step (fun current =>
              { current with
                waitState := some
                  { waitState with timersDone := waitState.timersDone.set timer.index true } })
            (by
              intro other isOther isOtherId
              have isRecord := eq_of_id_eq idsNodup isOther isMember (isOtherId.trans isId.symm)
              subst isRecord
              refine ⟨rfl, rfl, fun isExecute => isExecute, ?_, fun isQuiet => isQuiet,
                Or.inl ⟨rfl, rfl, rfl, rfl⟩⟩
              intro newWait hasNewWait
              cases hasNewWait
              exact Or.inl ⟨waitState, hasWait, rfl⟩)
          have both := disarmed.trans update
          exact both.trans (stepsKeep_evalStep _ _ (by rw [both.ids]; exact idsNodup))
      · exact disarmed
  · exact StepsKeep.refl none run

/-- `new` on a run evolves it ([FLOW-START] and the moves of a decision). -/
theorem stepsEvolve_newStep (run : Dex.Run σ) (stepType : σ.StepType) (input : Option σ.Val)
    (options : Dex.StepOptions σ) (origin : Dex.Origin σ)
    (numbersCounted : ∀ record, record ∈ run.steps →
      record.id.number ≤ (run.counters.get? record.id.stepType).getD 0) :
    StepsEvolve none run (run.newStep stepType input options origin) :=
  (newStepsOnly_newStep run stepType input options origin).toEvolve numbersCounted

end DexExpressiveness.DexF.WPF4
