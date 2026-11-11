import DexExpressiveness.Failure.Proofs.WPF4WorkerRules

/-!
# WP-F4: `workerEffect` and `workerReturn`
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- Dropping one more element after a non-empty drop. -/
theorem drop_succ_of_cons {α : Type} (elements : List α) (count : Nat) (head : α)
    (rest : List α) (isDrop : elements.drop count = head :: rest) :
    elements.drop (count + 1) = rest := by
  rw [← List.tail_drop, isDrop]
  rfl

/-- A non-empty drop leaves the count below the length. -/
theorem lt_length_of_drop_cons {α : Type} (elements : List α) (count : Nat) (head : α)
    (rest : List α) (isDrop : elements.drop count = head :: rest) : count < elements.length := by
  have isLength := congrArg List.length isDrop
  rw [List.length_drop, List.length_cons] at isLength
  omega

/-- The ghost part of I3, for a history with arguments, by its progress. -/
theorem historyIsConsistent_of_some {flow : Dex.FlowDef σ} {key : InvocationKey σ}
    {history : InvocationHistory σ} {arguments : Arguments σ}
    (consistent : HistoryIsConsistent flow key history)
    (hasArguments : history.arguments = some arguments) :
    (∀ effects response, history.progress = .ran effects response →
      effects = (arguments.compute flow key).1 ∧ response = (arguments.compute flow key).2 ∧
        history.emittedCount ≤ (arguments.compute flow key).1.length) ∧
    (∀ response, history.progress = .returned response →
      response = (arguments.compute flow key).2 ∧
        history.emittedCount = (arguments.compute flow key).1.length) ∧
    (history.progress = .notRun → history.emittedCount = 0) ∧
    (history.outcome = .committed → history.progress.isReturned = true) := by
  unfold HistoryIsConsistent at consistent
  rw [hasArguments] at consistent
  obtain ⟨progressMatches, isCommitted⟩ := consistent
  refine ⟨?_, ?_, ?_, isCommitted⟩
  · intro effects response isRan
    rw [isRan] at progressMatches
    exact progressMatches
  · intro response isReturned
    rw [isReturned] at progressMatches
    exact progressMatches
  · intro isNotRun
    rw [isNotRun] at progressMatches
    exact progressMatches

/-- Build the ghost part of I3 for a history with arguments. -/
theorem historyIsConsistent_of_parts {flow : Dex.FlowDef σ} {key : InvocationKey σ}
    {history : InvocationHistory σ} {arguments : Arguments σ}
    (hasArguments : history.arguments = some arguments)
    (ranMatches : ∀ effects response, history.progress = .ran effects response →
      effects = (arguments.compute flow key).1 ∧ response = (arguments.compute flow key).2 ∧
        history.emittedCount ≤ (arguments.compute flow key).1.length)
    (returnedMatches : ∀ response, history.progress = .returned response →
      response = (arguments.compute flow key).2 ∧
        history.emittedCount = (arguments.compute flow key).1.length)
    (notRunMatches : history.progress = .notRun → history.emittedCount = 0)
    (committedMatches : history.outcome = .committed → history.progress.isReturned = true) :
    HistoryIsConsistent flow key history := by
  unfold HistoryIsConsistent
  rw [hasArguments]
  refine ⟨?_, committedMatches⟩
  cases hProgress : history.progress with
  | notRun => exact notRunMatches hProgress
  | ran effects response => exact ranMatches effects response hProgress
  | returned response => exact returnedMatches response hProgress

/-- [WORKER-EFFECT] or [ZOMBIE-EFFECT] keeps the invariants. -/
theorem invariant_workerEffect (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (isFirstOccurrence : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.workerEffect taskId isFirstOccurrence) = some (events, next)) :
    Invariant program next := by
  unfold step at isStep
  cases isFound : config.findRealTask? taskId with
  | none => simp [isFound] at isStep
  | some task =>
    obtain ⟨isTask, _⟩ := mem_of_findRealTask? isFound
    obtain ⟨history, arguments, isHistory, hasArguments, sameContext, sameInput, sameSnapshot,
      sameConditionResults, isBelow, follows⟩ := follows_of_task invariant isTask
    have historyConsistent := historyIsConsistent_of_find invariant isHistory
    have phaseMatches := phase_of_follows follows hasArguments
    have emittedOf : config.ghost.emittedCountOf task.key = history.emittedCount := by
      simp [Ghost.emittedCountOf, isHistory]
    cases hPhase : task.base.phase with
    | started => simp [isFound, hPhase] at isStep
    | returned => simp [isFound, hPhase] at isStep
    | running remaining response =>
      cases remaining with
      | nil => simp [isFound, hPhase] at isStep
      | cons effect laterEffects =>
      rw [hPhase] at phaseMatches
      obtain ⟨isRemaining, isResponse, _, isRun⟩ := phaseMatches
      have isLater := drop_succ_of_cons _ _ _ _ isRemaining.symm
      have isBelowLength := lt_length_of_drop_cons _ _ _ _ isRemaining.symm
      let change : Task σ → Task σ := fun current =>
        { current with
          base := { current.base with phase := .running laterEffects response }
          emittedCount := task.emittedCount + 1 }
      have keepsFields : ∀ current, (change current).base.id = current.base.id ∧
          (change current).base.step = current.base.step ∧
          (change current).base.method = current.base.method ∧
          (change current).attemptContext.attempt = current.attemptContext.attempt :=
        fun _ => ⟨rfl, rfl, rfl, rfl⟩
      have changedFollows : ∀ changedHistory : InvocationHistory σ,
          changedHistory.arguments = some arguments →
          task.emittedCount + 1 ≤ changedHistory.emittedCount →
          changedHistory.progress.isNotRun = false →
          TaskFollowsHistory program.failureFreeFlow (change task) changedHistory := by
        intro changedHistory hasChangedArguments isAtMost isChangedRun
        refine ⟨arguments, hasChangedArguments, sameContext, sameInput, sameSnapshot,
          sameConditionResults, isAtMost, ?_⟩
        show _ = _ ∧ _ = _ ∧ _ ∧ _
        exact ⟨isLater.symm, isResponse, isBelowLength, isChangedRun⟩
      cases isFirstOccurrence with
      | false =>
        simp [isFound, hPhase, emittedOf] at isStep
        obtain ⟨isRepeat, -, isNext⟩ := isStep
        have isNext' : next = { config.updateRealTask taskId change with ghost := config.ghost } :=
          isNext.symm
        subst isNext'
        refine invariant_updateRealTask invariant taskId change keepsFields config.ghost
          (GhostFrame.refl _) rfl rfl rfl invariant.historiesConsistent ?_
        intro changed isChanged
        rcases mem_updateRealTask invariant isFound change isChanged with isNew | ⟨isOld, _⟩
        · subst isNew
          exact ⟨history, isHistory, changedFollows history hasArguments (by omega) isRun⟩
        · exact invariant.realTasksFollowHistory changed isOld
      | true =>
        simp [isFound, hPhase, emittedOf] at isStep
        obtain ⟨isFirst, -, isNext⟩ := isStep
        let counted : InvocationHistory σ → InvocationHistory σ := fun current =>
          { current with emittedCount := task.emittedCount + 1 }
        have isNext' : next = { config.updateRealTask taskId change with
            ghost := config.ghost.updateInvocation task.key counted } := isNext.symm
        subst isNext'
        obtain ⟨sameLast, sameVirtual⟩ := updateInvocation_other config.ghost task.key counted
        refine invariant_updateRealTask invariant taskId change keepsFields _
          (ghostFrame_updateInvocation _ _ _ (fun _ => rfl) (fun _ => rfl)) sameLast sameVirtual
          (filterMap_updateInvocation _ _ _ (fun _ => rfl)) ?_ ?_
        · intro entry isMember
          rcases mem_updateInvocation _ _ _ _ isMember with isOld | ⟨found, isFoundHistory, isEntry⟩
          · exact invariant.historiesConsistent entry isOld
          · rw [isHistory] at isFoundHistory
            cases isFoundHistory
            subst isEntry
            show HistoryIsConsistent program.failureFreeFlow task.key (counted history)
            obtain ⟨ranOld, returnedOld, _, committedOld⟩ :=
              historyIsConsistent_of_some historyConsistent hasArguments
            refine historyIsConsistent_of_parts hasArguments ?_ ?_ ?_ committedOld
            · intro effects otherResponse isRan
              obtain ⟨isEffects, isOtherResponse, _⟩ := ranOld effects otherResponse isRan
              exact ⟨isEffects, isOtherResponse, isBelowLength⟩
            · intro otherResponse isReturned
              obtain ⟨_, isFullLength⟩ := returnedOld otherResponse isReturned
              omega
            · intro isNotRun
              have isOldNotRun : history.progress = .notRun := isNotRun
              rw [isOldNotRun] at isRun
              cases isRun
        · intro changed isChanged
          rw [findInvocation?_updateInvocation]
          rcases mem_updateRealTask invariant isFound change isChanged with isNew | ⟨isOld, _⟩
          · subst isNew
            refine ⟨counted history, ?_, changedFollows (counted history) hasArguments
              (Nat.le_refl _) isRun⟩
            simp only [show (change task).key = task.key from rfl, ↓reduceIte, isHistory,
              Option.map_some]
          · obtain ⟨oldHistory, isOldHistory, oldFollows⟩ :=
              invariant.realTasksFollowHistory changed isOld
            by_cases isKey : changed.key = task.key
            · rw [isKey, isHistory] at isOldHistory
              cases isOldHistory
              refine ⟨counted history, by simp [isKey, isHistory], ?_⟩
              exact taskFollowsHistory_of_progress oldFollows rfl
                (show history.emittedCount ≤ task.emittedCount + 1 by omega) id id
            · exact ⟨oldHistory, by simp [isKey, isOldHistory], oldFollows⟩

/-- [WORKER-EFFECT] or [ZOMBIE-EFFECT], last part, keeps the invariants. -/
theorem invariant_workerReturn (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (isFirstReturn : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.workerReturn taskId isFirstReturn) = some (events, next)) :
    Invariant program next := by
  unfold step at isStep
  cases isFound : config.findRealTask? taskId with
  | none => simp [isFound] at isStep
  | some task =>
    obtain ⟨isTask, _⟩ := mem_of_findRealTask? isFound
    obtain ⟨history, arguments, isHistory, hasArguments, sameContext, sameInput, sameSnapshot,
      sameConditionResults, isBelow, follows⟩ := follows_of_task invariant isTask
    have historyConsistent := historyIsConsistent_of_find invariant isHistory
    have phaseMatches := phase_of_follows follows hasArguments
    cases hPhase : task.base.phase with
    | started => simp [isFound, hPhase] at isStep
    | returned => simp [isFound, hPhase] at isStep
    | running remaining response =>
      cases remaining with
      | cons => simp [isFound, hPhase] at isStep
      | nil =>
      rw [hPhase] at phaseMatches
      obtain ⟨isRemaining, isResponse, isAtMostLength, isRun⟩ := phaseMatches
      have isLength : task.emittedCount =
          (arguments.compute program.failureFreeFlow task.key).1.length := by
        have isEmpty := isRemaining.symm
        rw [List.drop_eq_nil_iff] at isEmpty
        omega
      let change : Task σ → Task σ := fun current =>
        { current with base := { current.base with phase := .returned response } }
      have keepsFields : ∀ current, (change current).base.id = current.base.id ∧
          (change current).base.step = current.base.step ∧
          (change current).base.method = current.base.method ∧
          (change current).attemptContext.attempt = current.attemptContext.attempt :=
        fun _ => ⟨rfl, rfl, rfl, rfl⟩
      have changedFollows : ∀ changedHistory : InvocationHistory σ,
          changedHistory.arguments = some arguments →
          task.emittedCount ≤ changedHistory.emittedCount →
          changedHistory.progress.isReturned = true →
          TaskFollowsHistory program.failureFreeFlow (change task) changedHistory := by
        intro changedHistory hasChangedArguments isAtMost isReturned
        refine ⟨arguments, hasChangedArguments, sameContext, sameInput, sameSnapshot,
          sameConditionResults, isAtMost, ?_⟩
        show _ = _ ∧ _ = _ ∧ _
        exact ⟨isResponse, isLength, isReturned⟩
      cases isFirstReturn with
      | false =>
        simp [isFound, hPhase, isHistory] at isStep
        obtain ⟨isReturned, -, isNext⟩ := isStep
        have isNext' : next = { config.updateRealTask taskId change with ghost := config.ghost } :=
          isNext.symm
        subst isNext'
        refine invariant_updateRealTask invariant taskId change keepsFields config.ghost
          (GhostFrame.refl _) rfl rfl rfl invariant.historiesConsistent ?_
        intro changed isChanged
        rcases mem_updateRealTask invariant isFound change isChanged with isNew | ⟨isOld, _⟩
        · subst isNew
          exact ⟨history, isHistory, changedFollows history hasArguments isBelow
            (by simpa using isReturned)⟩
        · exact invariant.realTasksFollowHistory changed isOld
      | true =>
        simp [isFound, hPhase, isHistory] at isStep
        obtain ⟨isNotReturned, -, isNext⟩ := isStep
        let returned : InvocationHistory σ → InvocationHistory σ := fun current =>
          { current with progress := .returned response }
        have isNext' : next = { config.updateRealTask taskId change with
            ghost := config.ghost.updateInvocation task.key returned } := isNext.symm
        subst isNext'
        obtain ⟨sameLast, sameVirtual⟩ := updateInvocation_other config.ghost task.key returned
        have historyLength : history.emittedCount =
            (arguments.compute program.failureFreeFlow task.key).1.length := by
          obtain ⟨ranOld, _, _, _⟩ := historyIsConsistent_of_some historyConsistent hasArguments
          cases hProgress : history.progress with
          | notRun => rw [hProgress] at isRun; cases isRun
          | ran effects otherResponse =>
            have isAtMost := (ranOld effects otherResponse hProgress).2.2
            omega
          | returned otherResponse =>
            rw [hProgress] at isNotReturned
            simp [VirtualProgress.isReturned] at isNotReturned
        refine invariant_updateRealTask invariant taskId change keepsFields _
          (ghostFrame_updateInvocation _ _ _ (fun _ => rfl) (fun _ => rfl)) sameLast sameVirtual
          (filterMap_updateInvocation _ _ _ (fun _ => rfl)) ?_ ?_
        · intro entry isMember
          rcases mem_updateInvocation _ _ _ _ isMember with isOld | ⟨found, isFoundHistory, isEntry⟩
          · exact invariant.historiesConsistent entry isOld
          · rw [isHistory] at isFoundHistory
            cases isFoundHistory
            subst isEntry
            unfold HistoryIsConsistent
            show match history.arguments with
              | none => _
              | some arguments => _
            rw [hasArguments]
            exact ⟨⟨isResponse, historyLength⟩, fun _ => rfl⟩
        · intro changed isChanged
          rw [findInvocation?_updateInvocation]
          rcases mem_updateRealTask invariant isFound change isChanged with isNew | ⟨isOld, _⟩
          · subst isNew
            refine ⟨returned history, ?_, changedFollows (returned history) hasArguments isBelow rfl⟩
            simp only [show (change task).key = task.key from rfl, ↓reduceIte, isHistory,
              Option.map_some]
          · obtain ⟨oldHistory, isOldHistory, oldFollows⟩ :=
              invariant.realTasksFollowHistory changed isOld
            by_cases isKey : changed.key = task.key
            · rw [isKey, isHistory] at isOldHistory
              cases isOldHistory
              refine ⟨returned history, by simp [isKey, isHistory], ?_⟩
              exact taskFollowsHistory_of_progress oldFollows rfl (Nat.le_refl _)
                (fun _ => rfl) (fun _ => rfl)
            · exact ⟨oldHistory, by simp [isKey, isOldHistory], oldFollows⟩

end DexExpressiveness.DexF.WPF4
