import DexExpressiveness.Failure.Proofs.WPF4CompleteBase

/-!
# WP-F4: the Complete rules keep the invariants

[STEP-COMPLETE-WAITFOR] and [STEP-COMPLETE-EXECUTE], extended: a returned
task that passes the fence leaves `Θ` or `Z`, and its Step goes through the
effects and a wait registration or a decision.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- The Step table after clearing the invocation of one Step. -/
theorem mem_cleared {base : Dex.Run σ} {record : Dex.StepRecord σ} (isRecord : record ∈ base.steps)
    (idsNodup : (base.steps.map (fun record => record.id)).Nodup) {cleared : Dex.StepRecord σ}
    (isCleared : cleared ∈ (base.updateStep record.id clearInvocation).steps) :
    (cleared = clearInvocation record) ∨ (cleared ∈ base.steps ∧ cleared.id ≠ record.id) := by
  obtain ⟨original, isOriginal, isMapped⟩ := (Dex.Run.mem_updateStep _ _ _ _).mp isCleared
  by_cases isRecordId : original.id = record.id
  · left
    simp only [isRecordId, ↓reduceIte] at isMapped
    rw [isMapped, eq_of_id_eq idsNodup isOriginal isRecord isRecordId]
  · right
    simp only [isRecordId, ↓reduceIte] at isMapped
    rw [isMapped]
    exact ⟨isOriginal, isRecordId⟩

/-- A Complete rule keeps the invariants, given the last meta-function. -/
theorem invariant_completeCommit {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    (canCommit : config.canCommit = true) (isUp : config.isUp = true) (taskId : Nat)
    (task : Task σ) (isFound : config.findRealTask? taskId = some task)
    (isPassed : config.isFencePassed task = true) (response : Dex.Response σ)
    (isReturned : task.base.phase = .returned response) (change : Run σ → Run σ)
    (final : Dex.Run σ) (isChange : change (bumpedRun run) = { bumpedRun run with base := final })
    (registered : Option Nat) (registeredSeq : ∀ seq, registered = some seq → seq = run.base.seq + 1)
    (evolve : StepsEvolve registered ((bumpedRun run).base.updateStep task.base.step clearInvocation)
      final)
    (leavesPhase : ∀ record, record ∈ run.base.steps → record.id = task.base.step →
      ∀ changed, changed ∈ final.steps → changed.id = task.base.step →
        changed.phase ≠ record.phase) :
    Invariant program ((config.removeRealTask taskId).commitRun run true (some task.key) [] change).2 := by
  have runInvariant := invariant.runInvariant run isRun
  have idsNodup := runInvariant.stepIdsNodup
  obtain ⟨isTask, isTaskId⟩ := mem_of_findRealTask? isFound
  obtain ⟨record, attempt, isRecord, isStepId, _, hasRunning, _⟩ :=
    (isFencePassed_iff isRun idsNodup task).mp isPassed
  have isRunning := running_of_invocation invariant isRun isRecord hasRunning
  have evolveAll : StepsEvolve registered (bumpedRun run).base final := by
    apply stepsEvolve_of_cleared (bumpedRun run).base record isRecord idsNodup
      (by rw [hasRunning]; rfl) final
    · rw [isStepId]; exact evolve
    · intro changed isChanged isId
      exact leavesPhase record isRecord isStepId changed isChanged (isId.trans isStepId)
  have completedReturned : ∀ key history, some task.key = some key →
      config.ghost.findInvocation? key = some history → history.progress.isReturned = true := by
    intro key history isKey isHistory
    cases isKey
    obtain ⟨found, arguments, isFoundHistory, hasArguments, _, _, _, _, _, follows⟩ :=
      follows_of_task invariant isTask
    rw [isHistory] at isFoundHistory
    cases isFoundHistory
    have phaseMatches := phase_of_follows follows hasArguments
    rw [isReturned] at phaseMatches
    exact phaseMatches.2.2
  have realFrom : ∀ other, other ∈ (config.removeRealTask taskId).realTasks →
      other ∈ config.realTasks := by
    intro other isOther
    rw [realTasks_removeRealTask] at isOther
    exact (List.mem_filter.mp isOther).1
  rw [commitRun_eq, isChange]
  refine invariant_dexCommit invariant isRun canCommit isUp (config.removeRealTask taskId) rfl rfl
    rfl rfl realFrom ?_ (fun other isOther => (List.mem_filter.mp isOther).1)
    (fun _ isInterrupt => isInterrupt) { bumpedRun run with base := final } registered true
    (some task.key) evolveAll (fun seq isSeq => ⟨rfl, registeredSeq seq isSeq⟩)
    (closedQuiet_of_evolve evolveAll isRunning) (fun _ => Or.inl rfl)
    (backoffArmed_of_invariant (run := run) invariant isRun) (fun _ isEntry => isEntry)
    runInvariant.retryTimersNodup completedReturned ?_ (fun _ isInterrupt isNot => absurd isInterrupt isNot)
  · rw [realTasks_removeRealTask]
    exact List.Nodup.sublist (List.Sublist.map _ List.filter_sublist) invariant.realTaskIdsNodup
  · intro other isOther isNot changed method isChanged hasChanged
    have isRemoved : other.base.id = taskId := by
      by_cases isSame : other.base.id = taskId
      · exact isSame
      · exact absurd (List.mem_filter.mpr ⟨isOther, by simp [isSame]⟩) isNot
    have isOtherTask : other = task :=
      task_eq_of_id_eq invariant.realTaskIdsNodup (List.mem_append_left _ isOther) isTask
        (by rw [isRemoved, isTaskId])
    subst isOtherTask
    rcases evolveAll.backward changed isChanged with ⟨original, isOriginal, isStep⟩ | isNew
    · rcases isStep.invocation with ⟨sameInvocation, _, _, samePhase⟩ | ⟨noInvocation, _⟩ |
        ⟨executeInvocation, _⟩
      · obtain ⟨isKey, _⟩ := runInvariant.runningTaskMatches original method other.base.id other
          isOriginal (sameInvocation ▸ hasChanged) isTask rfl
        simp only [Task.key, Prod.mk.injEq] at isKey
        have isRecordOriginal : original = record :=
          eq_of_id_eq idsNodup isOriginal isRecord (by rw [← isKey.1, isStepId])
        subst isRecordOriginal
        exact leavesPhase original isOriginal isStepId changed isChanged
          (isStep.id.trans isStepId) samePhase
      · rw [noInvocation] at hasChanged; cases hasChanged
      · rw [executeInvocation] at hasChanged; simp at hasChanged
    · obtain ⟨_, _, _, newMethod, hasPending, _⟩ := isNew
      rw [hasPending] at hasChanged
      simp at hasChanged

/-- The facts about the Step of a task that passes the fence, after its
invocation is cleared. -/
theorem cleared_facts {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run) (task : Task σ)
    (isPassed : config.isFencePassed task = true) :
    ∃ record, record ∈ run.base.steps ∧ record.id = task.base.step ∧
      record.phase = phaseOfMethod task.base.method ∧
      (((bumpedRun run).base.updateStep task.base.step clearInvocation).steps.map
        (fun record => record.id)).Nodup ∧
      clearInvocation record ∈ ((bumpedRun run).base.updateStep task.base.step clearInvocation).steps ∧
      (∀ cleared, cleared ∈ ((bumpedRun run).base.updateStep task.base.step clearInvocation).steps →
        cleared.id = task.base.step → cleared = clearInvocation record) ∧
      (∀ cleared, cleared ∈ ((bumpedRun run).base.updateStep task.base.step clearInvocation).steps →
        cleared.id ≠ task.base.step → cleared ∈ run.base.steps) := by
  have runInvariant := invariant.runInvariant run isRun
  have idsNodup := runInvariant.stepIdsNodup
  obtain ⟨record, _, isRecord, isStepId, _, hasRunning, _⟩ :=
    (isFencePassed_iff isRun idsNodup task).mp isPassed
  have phaseMatches := runInvariant.invocationMatchesPhase record isRecord
  rw [hasRunning] at phaseMatches
  refine ⟨record, isRecord, isStepId, ?_, ?_, ?_, ?_, ?_⟩
  · rw [phaseMatches]
    cases task.base.method <;> rfl
  · rw [Dex.Run.ids_updateStep _ task.base.step clearInvocation (fun _ => rfl)]
    exact idsNodup
  · exact (Dex.Run.mem_updateStep _ _ _ _).mpr ⟨record, isRecord, by simp [isStepId]⟩
  · intro cleared isCleared isId
    rw [← isStepId] at isCleared
    rcases mem_cleared isRecord idsNodup isCleared with isSame | ⟨_, isOther⟩
    · exact isSame
    · exact absurd (isId.trans isStepId.symm) isOther
  · intro cleared isCleared isId
    rw [← isStepId] at isCleared
    rcases mem_cleared isRecord idsNodup isCleared with isSame | ⟨isOld, _⟩
    · rw [isSame] at isId
      exact absurd isStepId isId
    · exact isOld

/-- [STEP-COMPLETE-WAITFOR], extended, keeps the invariants. -/
theorem invariant_completeWaitFor {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    (canCommit : config.canCommit = true) (isUp : config.isUp = true) (taskId : Nat)
    (task : Task σ) (isFound : config.findRealTask? taskId = some task)
    (isPassed : config.isFencePassed task = true) (isMethod : task.base.method = .waitFor)
    (response : Dex.WaitForResponse σ) (isReturned : task.base.phase = .returned (.waitFor response)) :
    Invariant program ((config.removeRealTask taskId).commitRun run true (some task.key) []
      (fun current =>
        { current with
          base := ((current.base.updateStep task.base.step (fun record =>
              { record with invocation := none })).applyEffects response.effects
            ).registerWait task.base.step response.wait config.clock })).2 := by
  have idsNodup := (invariant.runInvariant run isRun).stepIdsNodup
  obtain ⟨record, isRecord, isStepId, recordPhase, clearedNodup, _, isOnly, _⟩ :=
    cleared_facts invariant isRun task isPassed
  rw [isMethod] at recordPhase
  have notWaiting : ∀ cleared,
      cleared ∈ ((bumpedRun run).base.updateStep task.base.step clearInvocation).steps →
      cleared.id = task.base.step → cleared.phase ≠ .waiting := by
    intro cleared isCleared isId
    rw [isOnly cleared isCleared isId]
    show record.phase ≠ .waiting
    rw [recordPhase]
    simp [phaseOfMethod]
  have keepEffects := stepsKeep_applyEffects
    ((bumpedRun run).base.updateStep task.base.step clearInvocation) response.effects clearedNodup
  have effectsNodup : ((((bumpedRun run).base.updateStep task.base.step clearInvocation).applyEffects
      response.effects).steps.map (fun record => record.id)).Nodup := by
    rw [keepEffects.ids]; exact clearedNodup
  have isReady : ∀ changed, changed ∈ (((bumpedRun run).base.updateStep task.base.step
      clearInvocation).applyEffects response.effects).steps → changed.id = task.base.step →
      changed.phase = .waitFor ∧ changed.invocation = none := by
    intro changed isChanged isId
    have isCleared := applyEffects_keeps_record _ response.effects task.base.step clearedNodup
      notWaiting changed isChanged isId
    rw [isOnly changed isCleared isId]
    exact ⟨recordPhase, rfl⟩
  have keepRegister := stepsKeep_registerWait _ task.base.step response.wait config.clock
    effectsNodup isReady
  refine invariant_completeCommit invariant isRun canCommit isUp taskId task isFound isPassed _
    isReturned _ _ rfl _ ?_ ((keepEffects.mono _).trans keepRegister).toEvolve ?_
  · intro seq isSeq
    cases isSeq
    rw [Dex.Run.seq_applyEffects]
    rfl
  · intro original isOriginal isId changed isChanged isChangedId
    rw [eq_of_id_eq idsNodup isOriginal isRecord (isId.trans isStepId.symm), recordPhase]
    exact registerWait_leaves_waitFor _ task.base.step response.wait config.clock effectsNodup
      isReady changed isChanged isChangedId

/-- [STEP-COMPLETE-EXECUTE], extended, keeps the invariants. -/
theorem invariant_completeExecute {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    (canCommit : config.canCommit = true) (isUp : config.isUp = true) (taskId : Nat)
    (task : Task σ) (isFound : config.findRealTask? taskId = some task)
    (isPassed : config.isFencePassed task = true) (isMethod : task.base.method = .execute)
    (response : Dex.ExecuteResponse σ) (isReturned : task.base.phase = .returned (.execute response)) :
    Invariant program ((config.removeRealTask taskId).commitRun run true (some task.key) []
      (fun current =>
        { current with
          base := ((current.base.updateStep task.base.step (fun record =>
              { record with invocation := none })).applyEffects response.effects
            ).applyDecision task.base.step response.decision })).2 := by
  have runInvariant := invariant.runInvariant run isRun
  have idsNodup := runInvariant.stepIdsNodup
  obtain ⟨record, isRecord, isStepId, recordPhase, clearedNodup, clearedMember, isOnly, isOther⟩ :=
    cleared_facts invariant isRun task isPassed
  rw [isMethod] at recordPhase
  have notWaiting : ∀ cleared,
      cleared ∈ ((bumpedRun run).base.updateStep task.base.step clearInvocation).steps →
      cleared.id = task.base.step → cleared.phase ≠ .waiting := by
    intro cleared isCleared isId
    rw [isOnly cleared isCleared isId]
    show record.phase ≠ .waiting
    rw [recordPhase]
    simp [phaseOfMethod]
  have keepEffects := stepsKeep_applyEffects
    ((bumpedRun run).base.updateStep task.base.step clearInvocation) response.effects clearedNodup
  have effectsNodup : ((((bumpedRun run).base.updateStep task.base.step clearInvocation).applyEffects
      response.effects).steps.map (fun record => record.id)).Nodup := by
    rw [keepEffects.ids]; exact clearedNodup
  have numbersCounted : ∀ changed, changed ∈ (((bumpedRun run).base.updateStep task.base.step
      clearInvocation).applyEffects response.effects).steps →
      changed.id.number ≤ ((((bumpedRun run).base.updateStep task.base.step
        clearInvocation).applyEffects response.effects).counters.get? changed.id.stepType).getD 0 := by
    intro changed isChanged
    rw [keepEffects.counters]
    obtain ⟨middle, isMiddle, isStep⟩ := keepEffects.backward changed isChanged
    rw [isStep.id]
    by_cases isRecordId : middle.id = task.base.step
    · rw [isOnly middle isMiddle isRecordId]
      exact runInvariant.stepNumbersCounted record isRecord
    · exact runInvariant.stepNumbersCounted middle (isOther middle isMiddle isRecordId)
  have inactiveQuiet : ∀ changed, changed ∈ (((bumpedRun run).base.updateStep task.base.step
      clearInvocation).applyEffects response.effects).steps → changed.phase.isActive = false →
      changed.invocation = none := by
    intro changed isChanged isInactive
    obtain ⟨middle, isMiddle, isStep⟩ := keepEffects.backward changed isChanged
    rcases isStep.invocation with ⟨sameInvocation, _, _, samePhase⟩ | ⟨noInvocation, _⟩ |
      ⟨_, executePhase, _⟩
    · rw [sameInvocation]
      rw [samePhase] at isInactive
      by_cases isRecordId : middle.id = task.base.step
      · rw [isOnly middle isMiddle isRecordId]
        rfl
      · exact inactiveQuiet_of_invariant invariant isRun middle (isOther middle isMiddle isRecordId)
          isInactive
    · exact noInvocation
    · rw [executePhase] at isInactive
      cases isInactive
  obtain ⟨effectsRecord, isEffectsRecord, effectsStep⟩ :=
    keepEffects.forward (clearInvocation record) clearedMember
  have evolveDecision := stepsEvolve_applyDecision _ task.base.step response.decision effectsNodup
    numbersCounted inactiveQuiet
  refine invariant_completeCommit invariant isRun canCommit isUp taskId task isFound isPassed _
    isReturned _ _ rfl none (fun _ isNone => by cases isNone)
    (keepEffects.thenEvolve_of_sameStatus (Dex.Run.status_applyEffects _ _) evolveDecision) ?_
  intro original isOriginal isId changed isChanged isChangedId
  rw [eq_of_id_eq idsNodup isOriginal isRecord (isId.trans isStepId.symm), recordPhase]
  have isCompleted := (applyDecision_completes _ task.base.step response.decision effectsNodup
    numbersCounted ⟨effectsRecord, isEffectsRecord, effectsStep.id.trans isStepId⟩ changed isChanged
    isChangedId).1
  rw [isCompleted]
  simp [phaseOfMethod]

end DexExpressiveness.DexF.WPF4
