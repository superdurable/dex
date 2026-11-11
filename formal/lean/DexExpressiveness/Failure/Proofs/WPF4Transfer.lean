import DexExpressiveness.Failure.Proofs.WPF4Frame

/-!
# WP-F4: `Invariant` when the run stays

Updating one ghost history, and the transfer of `Invariant` under a change
that keeps the run.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- The fence reads only the run and the task's Step, method, ID, and attempt number. -/
theorem isFencePassed_congr {config next : Config σ} (sameRun : next.run = config.run)
    {task changed : Task σ} (sameStep : changed.base.step = task.base.step)
    (sameMethod : changed.base.method = task.base.method) (sameId : changed.base.id = task.base.id)
    (sameAttempt : changed.attemptContext.attempt = task.attemptContext.attempt) :
    next.isFencePassed changed = config.isFencePassed task := by
  unfold Config.isFencePassed
  rw [sameRun, sameStep, sameMethod, sameId, sameAttempt]

/-! ## Updating one history -/

/-- Setting a present key keeps the keys. -/
theorem keys_set_of_present {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ)
    (value : ν) (isPresent : AssocList.contains map key = true) :
    (AssocList.set map key value).map (fun entry => entry.1) = map.map (fun entry => entry.1) := by
  induction map with
  | nil => simp [AssocList.contains, AssocList.get?] at isPresent
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    by_cases isKey : headKey = key
    · simp [AssocList.set, isKey]
    · have restPresent : AssocList.contains rest key = true := by
        simpa [AssocList.contains, AssocList.get?, isKey] using isPresent
      simp [AssocList.set, isKey, inductionHypothesis restPresent]

/-- Setting a present key to a value with the same arguments keeps the
arguments of the list. -/
theorem filterMap_set_of_present (map : AssocList (InvocationKey σ) (InvocationHistory σ))
    (key : InvocationKey σ) (history value : InvocationHistory σ)
    (isFound : AssocList.get? map key = some history)
    (sameArguments : value.arguments = history.arguments) :
    (AssocList.set map key value).filterMap (fun entry => entry.2.arguments) =
      map.filterMap (fun entry => entry.2.arguments) := by
  induction map with
  | nil => simp [AssocList.get?] at isFound
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    by_cases isKey : headKey = key
    · simp only [AssocList.get?, isKey, ↓reduceIte, Option.some.injEq] at isFound
      subst isFound
      simp [AssocList.set, isKey, List.filterMap_cons, sameArguments]
    · simp only [AssocList.get?, isKey, ↓reduceIte] at isFound
      simp [AssocList.set, isKey, List.filterMap_cons, inductionHypothesis isFound]

/-- Lookup after `updateInvocation`. -/
theorem findInvocation?_updateInvocation (ghost : Ghost σ) (key other : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ) :
    (ghost.updateInvocation key change).findInvocation? other =
      if other = key then (ghost.findInvocation? key).map change
      else ghost.findInvocation? other := by
  unfold Ghost.updateInvocation
  cases isFound : ghost.findInvocation? key with
  | none =>
    simp only [Option.map_none]
    split
    · rename_i isKey
      rw [isKey, isFound]
    · rfl
  | some history =>
    simp only [Option.map_some]
    unfold Ghost.findInvocation?
    split
    · rename_i isKey
      rw [isKey, AssocList.get?_set_self]
    · rename_i isKey
      rw [AssocList.get?_set_other _ _ _ _ isKey]

/-- `updateInvocation` keeps the keys. -/
theorem keys_updateInvocation (ghost : Ghost σ) (key : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ) :
    (ghost.updateInvocation key change).invocations.map (fun entry => entry.1) =
      ghost.invocations.map (fun entry => entry.1) := by
  unfold Ghost.updateInvocation
  cases isFound : ghost.findInvocation? key with
  | none => rfl
  | some history =>
    apply keys_set_of_present
    unfold Ghost.findInvocation? at isFound
    simp [AssocList.contains, isFound]

/-- The members after `updateInvocation`. -/
theorem mem_updateInvocation (ghost : Ghost σ) (key : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ)
    (entry : InvocationKey σ × InvocationHistory σ)
    (isMember : entry ∈ (ghost.updateInvocation key change).invocations) :
    entry ∈ ghost.invocations ∨
      ∃ history, ghost.findInvocation? key = some history ∧ entry = (key, change history) := by
  unfold Ghost.updateInvocation at isMember
  cases isFound : ghost.findInvocation? key with
  | none => rw [isFound] at isMember; exact Or.inl isMember
  | some history =>
    rw [isFound] at isMember
    rcases AssocList.mem_set _ _ _ _ isMember with isOld | isNew
    · exact Or.inl isOld
    · exact Or.inr ⟨history, rfl, isNew⟩

/-- `updateInvocation` keeps the arguments when the change does. -/
theorem filterMap_updateInvocation (ghost : Ghost σ) (key : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ)
    (keepsArguments : ∀ history, (change history).arguments = history.arguments) :
    (ghost.updateInvocation key change).invocations.filterMap (fun entry => entry.2.arguments) =
      ghost.invocations.filterMap (fun entry => entry.2.arguments) := by
  unfold Ghost.updateInvocation
  cases isFound : ghost.findInvocation? key with
  | none => rfl
  | some history => exact filterMap_set_of_present _ _ _ _ isFound (keepsArguments history)

/-- `updateInvocation` with a change of the progress part is a ghost frame. -/
theorem ghostFrame_updateInvocation (ghost : Ghost σ) (key : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ)
    (keepsOutcome : ∀ history, (change history).outcome = history.outcome)
    (keepsArguments : ∀ history, (change history).arguments = history.arguments) :
    GhostFrame ghost (ghost.updateInvocation key change) where
  keys := keys_updateInvocation ghost key change
  find := by
    intro other
    rw [findInvocation?_updateInvocation]
    split
    · rename_i isKey
      subst isKey
      cases ghost.findInvocation? other with
      | none => rfl
      | some history => simp [outcomeAndArguments, keepsOutcome, keepsArguments]
    · rfl
  normalCount := by unfold Ghost.updateInvocation; split <;> rfl
  normalIndex := by unfold Ghost.updateInvocation; split <;> rfl

/-- `updateInvocation` keeps the other fields. -/
theorem updateInvocation_other (ghost : Ghost σ) (key : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ) :
    (ghost.updateInvocation key change).lastNormalCommitAt = ghost.lastNormalCommitAt ∧
    (ghost.updateInvocation key change).nextVirtualTaskId = ghost.nextVirtualTaskId := by
  unfold Ghost.updateInvocation
  split <;> exact ⟨rfl, rfl⟩

/-! ## `Invariant` when the run stays -/

/-- `Invariant` under a change of the tasks, the interrupts, availability,
stamps, and the progress part of the ghost, when the run stays. The clauses
that such a change can break are hypotheses. -/
theorem invariant_transfer_general {program : Program σ} {config next : Config σ}
    (invariant : Invariant program config) (sameRun : next.run = config.run)
    (sameNextTaskId : next.nextTaskId = config.nextTaskId)
    (frame : GhostFrame config.ghost next.ghost)
    (sameLastNormal : next.ghost.lastNormalCommitAt = config.ghost.lastNormalCommitAt)
    (sameNextVirtual : next.ghost.nextVirtualTaskId = config.ghost.nextVirtualTaskId)
    (sameArguments : next.ghost.invocations.filterMap (fun entry => entry.2.arguments) =
      config.ghost.invocations.filterMap (fun entry => entry.2.arguments))
    (realIdsNodup : (next.realTasks.map (fun task => task.base.id)).Nodup)
    (realTasksFrom : ∀ task, task ∈ next.realTasks → ∃ original, original ∈ config.realTasks ∧
      original.base.id = task.base.id ∧ original.base.step = task.base.step ∧
      original.base.method = task.base.method ∧
      original.attemptContext.attempt = task.attemptContext.attempt)
    (tasksFrom : ∀ task, task ∈ next.tasks → ∃ original, original ∈ config.tasks ∧
      original.base.id = task.base.id ∧ original.base.step = task.base.step ∧
      original.base.method = task.base.method ∧
      original.attemptContext.attempt = task.attemptContext.attempt)
    (interruptsNotInTasks : ∀ interrupt task, interrupt ∈ next.interrupts → task ∈ next.tasks →
      task.base.id ≠ interrupt.taskId)
    (interruptsBelow : ∀ interrupt, interrupt ∈ next.interrupts →
      interrupt.taskId < next.nextTaskId)
    (noRunNoInterrupts : next.run = none → next.interrupts = [])
    (downIsEmpty : next.isUp = false → next.tasks = [] ∧ next.interrupts = [])
    (lastNormalCommitAtMost :
      IsStampAtMost next.ghost.lastNormalCommitAt next.lastCommitAt)
    (historiesConsistent : ∀ entry, entry ∈ next.ghost.invocations →
      HistoryIsConsistent program.failureFreeFlow entry.1 entry.2)
    (realTasksFollowHistory : ∀ task, task ∈ next.realTasks →
      ∃ history, next.ghost.findInvocation? task.key = some history ∧
        TaskFollowsHistory program.failureFreeFlow task history)
    (stillTracked : ∀ run, config.run = some run → ∀ record method taskId,
      record ∈ run.base.steps →
      record.invocation = some { method := method, state := .running taskId } →
      (∃ task, task ∈ next.tasks ∧ task.base.id = taskId) ∨ next.isUp = false ∨
        ∃ attempt, run.findAttempt? record.id = some attempt ∧
          ({ step := record.id, method := method, attempt := attempt.number, taskId := taskId } :
            Interrupt σ) ∈ next.interrupts) :
    Invariant program next where
  realTaskIdsNodup := realIdsNodup
  realTaskIdsBelow := by
    intro task isTask
    obtain ⟨original, isOriginal, sameId, _⟩ := realTasksFrom task isTask
    rw [← sameId, sameNextTaskId]
    exact invariant.realTaskIdsBelow original isOriginal
  interruptsNotInTasks := interruptsNotInTasks
  interruptsBelow := interruptsBelow
  downIsEmpty := downIsEmpty
  lastNormalCommitAtMost := lastNormalCommitAtMost
  notStarted := by
    intro hasNoRun
    obtain ⟨noReal, noInterrupts, noTaskId, noInvocations, noCount, noIndex, noLast, noVirtual⟩ :=
      invariant.notStarted (by rw [← sameRun]; exact hasNoRun)
    refine ⟨?_, ?_, ?_, ?_, ?_, ?_, ?_, ?_⟩
    rotate_left 2
    · rw [sameNextTaskId]; exact noTaskId
    rotate_left 1
    · rw [frame.normalCount]; exact noCount
    · rw [frame.normalIndex]; exact noIndex
    · rw [sameLastNormal]; exact noLast
    · rw [sameNextVirtual]; exact noVirtual
    · apply List.eq_nil_iff_forall_not_mem.mpr
      intro task isTask
      obtain ⟨original, isOriginal, _⟩ := realTasksFrom task isTask
      rw [noReal] at isOriginal
      cases isOriginal
    · exact noRunNoInterrupts hasNoRun
    · have sameKeys := frame.keys
      rw [noInvocations] at sameKeys
      exact List.map_eq_nil_iff.mp sameKeys
  ghostKeysNodup := by rw [frame.keys]; exact invariant.ghostKeysNodup
  virtualTaskIdsNodup := by rw [sameArguments]; exact invariant.virtualTaskIdsNodup
  virtualTaskIdsBelow := by
    intro entry arguments isMember hasArguments
    have keysNodup : (next.ghost.invocations.map (fun entry => entry.1)).Nodup := by
      rw [frame.keys]; exact invariant.ghostKeysNodup
    have isFound : next.ghost.findInvocation? entry.1 = some entry.2 :=
      get?_of_mem_nodup _ entry isMember keysNodup
    obtain ⟨original, isOriginal, _, sameArgs⟩ := frame.find_after isFound
    rw [sameNextVirtual]
    exact invariant.virtualTaskIdsBelow (entry.1, original) arguments
      (mem_of_get? _ _ _ isOriginal) (by rw [sameArgs]; exact hasArguments)
  historiesConsistent := historiesConsistent
  realTasksFollowHistory := realTasksFollowHistory
  liveTasksPassFence := by
    intro task history isTask isHistory isLive
    obtain ⟨original, isOriginal, sameId, sameStep, sameMethod, sameAttempt⟩ := tasksFrom task isTask
    have sameKey : original.key = task.key := by simp [Task.key, sameStep, sameMethod]
    obtain ⟨originalHistory, isOriginalHistory, sameOutcome, _⟩ := frame.find_after isHistory
    rw [isFencePassed_congr sameRun sameStep.symm sameMethod.symm sameId.symm sameAttempt.symm]
    exact invariant.liveTasksPassFence original originalHistory isOriginal
      (by rw [sameKey]; exact isOriginalHistory) (by rw [sameOutcome]; exact isLive)
  runInvariant := by
    intro run isRun
    have isOldRun : config.run = some run := by rw [← sameRun]; exact isRun
    refine runInvariant_transfer (invariant.runInvariant run isOldRun) frame
      (Nat.le_of_eq sameNextTaskId.symm) ?_ (stillTracked run isOldRun)
    intro task isTask
    obtain ⟨original, isOriginal, sameId, sameStep, sameMethod, sameAttempt⟩ :=
      realTasksFrom task isTask
    exact ⟨original, isOriginal, sameId, by simp [Task.key, sameStep, sameMethod], sameAttempt⟩

/-- `invariant_transfer_general` when the real task IDs shrink and the
interrupts only shrink. -/
theorem invariant_transfer {program : Program σ} {config next : Config σ}
    (invariant : Invariant program config) (sameRun : next.run = config.run)
    (sameNextTaskId : next.nextTaskId = config.nextTaskId)
    (frame : GhostFrame config.ghost next.ghost)
    (sameLastNormal : next.ghost.lastNormalCommitAt = config.ghost.lastNormalCommitAt)
    (sameNextVirtual : next.ghost.nextVirtualTaskId = config.ghost.nextVirtualTaskId)
    (sameArguments : next.ghost.invocations.filterMap (fun entry => entry.2.arguments) =
      config.ghost.invocations.filterMap (fun entry => entry.2.arguments))
    (realIdsSublist : (next.realTasks.map (fun task => task.base.id)).Sublist
      (config.realTasks.map (fun task => task.base.id)))
    (realTasksFrom : ∀ task, task ∈ next.realTasks → ∃ original, original ∈ config.realTasks ∧
      original.base.id = task.base.id ∧ original.base.step = task.base.step ∧
      original.base.method = task.base.method ∧
      original.attemptContext.attempt = task.attemptContext.attempt)
    (tasksFrom : ∀ task, task ∈ next.tasks → ∃ original, original ∈ config.tasks ∧
      original.base.id = task.base.id ∧ original.base.step = task.base.step ∧
      original.base.method = task.base.method ∧
      original.attemptContext.attempt = task.attemptContext.attempt)
    (interruptsFrom : ∀ interrupt, interrupt ∈ next.interrupts → interrupt ∈ config.interrupts)
    (downIsEmpty : next.isUp = false → next.tasks = [] ∧ next.interrupts = [])
    (lastNormalCommitAtMost :
      IsStampAtMost next.ghost.lastNormalCommitAt next.lastCommitAt)
    (historiesConsistent : ∀ entry, entry ∈ next.ghost.invocations →
      HistoryIsConsistent program.failureFreeFlow entry.1 entry.2)
    (realTasksFollowHistory : ∀ task, task ∈ next.realTasks →
      ∃ history, next.ghost.findInvocation? task.key = some history ∧
        TaskFollowsHistory program.failureFreeFlow task history)
    (stillTracked : ∀ run, config.run = some run → ∀ record method taskId,
      record ∈ run.base.steps →
      record.invocation = some { method := method, state := .running taskId } →
      (∃ task, task ∈ next.tasks ∧ task.base.id = taskId) ∨ next.isUp = false ∨
        ∃ attempt, run.findAttempt? record.id = some attempt ∧
          ({ step := record.id, method := method, attempt := attempt.number, taskId := taskId } :
            Interrupt σ) ∈ next.interrupts) :
    Invariant program next := by
  refine invariant_transfer_general invariant sameRun sameNextTaskId frame sameLastNormal
    sameNextVirtual sameArguments (List.Nodup.sublist realIdsSublist invariant.realTaskIdsNodup)
    realTasksFrom tasksFrom ?_ ?_ ?_ downIsEmpty lastNormalCommitAtMost historiesConsistent
    realTasksFollowHistory stillTracked
  · intro interrupt task isInterrupt isTask
    obtain ⟨original, isOriginal, sameId, _⟩ := tasksFrom task isTask
    rw [← sameId]
    exact invariant.interruptsNotInTasks interrupt original (interruptsFrom interrupt isInterrupt)
      isOriginal
  · intro interrupt isInterrupt
    rw [sameNextTaskId]
    exact invariant.interruptsBelow interrupt (interruptsFrom interrupt isInterrupt)
  · intro hasNoRun
    apply List.eq_nil_iff_forall_not_mem.mpr
    intro interrupt isInterrupt
    have isOriginal := interruptsFrom interrupt isInterrupt
    rw [(invariant.notStarted (by rw [← sameRun]; exact hasNoRun)).2.1] at isOriginal
    cases isOriginal

end DexExpressiveness.DexF.WPF4
