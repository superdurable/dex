import DexExpressiveness.Failure.Proofs.WPF4Worker

/-!
# WP-F4: the Worker rules, rule by rule

`workerRun`, `workerEffect`, and `workerReturn` change one real task and the
progress part of its history. Each proof shows the two clauses that such a
step can break, `historiesConsistent` and `realTasksFollowHistory`, and
`invariant_updateRealTask` gives the rest.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- The history of a real task, with the arguments its fields match. -/
theorem follows_of_task {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) {task : Task σ} (isTask : task ∈ config.realTasks) :
    ∃ history arguments, config.ghost.findInvocation? task.key = some history ∧
      history.arguments = some arguments ∧ task.base.context = arguments.context ∧
      task.base.input = arguments.input ∧ task.base.snapshot = arguments.snapshot ∧
      task.base.conditionResults = arguments.conditionResults ∧
      task.emittedCount ≤ history.emittedCount ∧
      TaskFollowsHistory program.failureFreeFlow task history := by
  obtain ⟨history, isHistory, follows⟩ := invariant.realTasksFollowHistory task isTask
  have copy := follows
  obtain ⟨arguments, hasArguments, sameContext, sameInput, sameSnapshot, sameConditionResults,
    isBelow, _⟩ := copy
  exact ⟨history, arguments, isHistory, hasArguments, sameContext, sameInput, sameSnapshot,
    sameConditionResults, isBelow, follows⟩

/-- The phase part of `TaskFollowsHistory`. -/
theorem phase_of_follows {flow : Dex.FlowDef σ} {task : Task σ} {history : InvocationHistory σ}
    {arguments : Arguments σ} (follows : TaskFollowsHistory flow task history)
    (hasArguments : history.arguments = some arguments) :
    match task.base.phase with
    | .started => task.emittedCount = 0
    | .running remaining response =>
      remaining = (arguments.compute flow task.key).1.drop task.emittedCount ∧
        response = (arguments.compute flow task.key).2 ∧
        task.emittedCount ≤ (arguments.compute flow task.key).1.length ∧
        history.progress.isNotRun = false
    | .returned response =>
      response = (arguments.compute flow task.key).2 ∧
        task.emittedCount = (arguments.compute flow task.key).1.length ∧
        history.progress.isReturned = true := by
  obtain ⟨found, isFound, _, _, _, _, _, phaseMatches⟩ := follows
  rw [hasArguments] at isFound
  cases isFound
  exact phaseMatches

/-- [WORKER-EFFECT] or [ZOMBIE-EFFECT], first part, keeps the invariants. -/
theorem invariant_workerRun (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (isFirstRun : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.workerRun taskId isFirstRun) = some (events, next)) :
    Invariant program next := by
  unfold step at isStep
  cases isFound : config.findRealTask? taskId with
  | none => simp [isFound] at isStep
  | some task =>
    obtain ⟨isTask, _⟩ := mem_of_findRealTask? isFound
    obtain ⟨history, arguments, isHistory, hasArguments, sameContext, sameInput, sameSnapshot,
      sameConditionResults, _, follows⟩ := follows_of_task invariant isTask
    have isResult := compute_of_arguments ignoresAttemptContext task arguments sameContext
      sameInput sameSnapshot sameConditionResults
    have historyConsistent := historyIsConsistent_of_find invariant isHistory
    have phaseMatches := phase_of_follows follows hasArguments
    cases hPhase : task.base.phase with
    | running => simp [isFound, hPhase] at isStep
    | returned => simp [isFound, hPhase] at isStep
    | started =>
      rw [hPhase] at phaseMatches
      have isZero : task.emittedCount = 0 := phaseMatches
      let change : Task σ → Task σ := fun current =>
        { current with
          base := { current.base with
            phase := .running (task.base.compute (program task.attemptContext)).1
              (task.base.compute (program task.attemptContext)).2 } }
      have keepsFields : ∀ current, (change current).base.id = current.base.id ∧
          (change current).base.step = current.base.step ∧
          (change current).base.method = current.base.method ∧
          (change current).attemptContext.attempt = current.attemptContext.attempt :=
        fun _ => ⟨rfl, rfl, rfl, rfl⟩
      have changedFollows : ∀ changedHistory : InvocationHistory σ,
          changedHistory.arguments = some arguments →
          history.emittedCount ≤ changedHistory.emittedCount →
          changedHistory.progress.isNotRun = false →
          TaskFollowsHistory program.failureFreeFlow (change task) changedHistory := by
        intro changedHistory hasChangedArguments _ isRun
        refine ⟨arguments, hasChangedArguments, sameContext, sameInput, sameSnapshot,
          sameConditionResults, ?_, ?_⟩
        · show task.emittedCount ≤ _
          rw [isZero]
          exact Nat.zero_le _
        · show _ = _ ∧ _ = _ ∧ _ ∧ _
          refine ⟨?_, ?_, ?_, isRun⟩
          · show (task.base.compute (program task.attemptContext)).1 =
              (arguments.compute program.failureFreeFlow task.key).1.drop task.emittedCount
            rw [isResult, isZero, List.drop_zero]
          · show (task.base.compute (program task.attemptContext)).2 =
              (arguments.compute program.failureFreeFlow task.key).2
            rw [isResult]
          · show task.emittedCount ≤ _
            rw [isZero]
            exact Nat.zero_le _
      cases isFirstRun with
      | false =>
        simp [isFound, hPhase, isHistory] at isStep
        obtain ⟨isRun, -, isNext⟩ := isStep
        have isNext' : next = { config.updateRealTask taskId change with ghost := config.ghost } :=
          isNext.symm
        subst isNext'
        refine invariant_updateRealTask invariant taskId change keepsFields config.ghost
          (GhostFrame.refl _) rfl rfl rfl invariant.historiesConsistent ?_
        intro changed isChanged
        rcases mem_updateRealTask invariant isFound change isChanged with isNew | ⟨isOld, _⟩
        · subst isNew
          exact ⟨history, isHistory, changedFollows history hasArguments (Nat.le_refl _)
            (by simpa using isRun.symm)⟩
        · exact invariant.realTasksFollowHistory changed isOld
      | true =>
        simp [isFound, hPhase, isHistory] at isStep
        obtain ⟨isNotRun, -, isNext⟩ := isStep
        let progress : InvocationHistory σ → InvocationHistory σ := fun current =>
          { current with
            progress := (.ran (task.base.compute (program task.attemptContext)).1
              (task.base.compute (program task.attemptContext)).2) }
        have isNext' : next = { config.updateRealTask taskId change with
            ghost := config.ghost.updateInvocation task.key progress } := isNext.symm
        subst isNext'
        obtain ⟨sameLast, sameVirtual⟩ := updateInvocation_other config.ghost task.key progress
        have notCommitted : history.outcome ≠ .committed := by
          intro isCommitted
          unfold HistoryIsConsistent at historyConsistent
          rw [hasArguments] at historyConsistent
          have isReturned := historyConsistent.2 isCommitted
          cases hProgress : history.progress with
          | notRun => rw [hProgress] at isReturned; cases isReturned
          | ran => rw [hProgress] at isNotRun; cases isNotRun
          | returned => rw [hProgress] at isNotRun; cases isNotRun
        have isEmptyHistory : history.emittedCount = 0 := by
          unfold HistoryIsConsistent at historyConsistent
          rw [hasArguments] at historyConsistent
          have progressMatches := historyConsistent.1
          cases hProgress : history.progress with
          | notRun => rw [hProgress] at progressMatches; exact progressMatches
          | ran => rw [hProgress] at isNotRun; cases isNotRun
          | returned => rw [hProgress] at isNotRun; cases isNotRun
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
            refine ⟨⟨?_, ?_, ?_⟩, fun isCommitted => absurd isCommitted notCommitted⟩
            · show (task.base.compute (program task.attemptContext)).1 = _
              rw [isResult]
            · show (task.base.compute (program task.attemptContext)).2 = _
              rw [isResult]
            · show history.emittedCount ≤ _
              rw [isEmptyHistory]
              exact Nat.zero_le _
        · intro changed isChanged
          rw [findInvocation?_updateInvocation]
          rcases mem_updateRealTask invariant isFound change isChanged with isNew | ⟨isOld, _⟩
          · subst isNew
            refine ⟨progress history, ?_, changedFollows (progress history) hasArguments
              (Nat.le_refl _) rfl⟩
            simp only [show (change task).key = task.key from rfl, ↓reduceIte, isHistory,
              Option.map_some]
          · obtain ⟨oldHistory, isOldHistory, oldFollows⟩ :=
              invariant.realTasksFollowHistory changed isOld
            by_cases isKey : changed.key = task.key
            · rw [isKey] at isOldHistory
              rw [isHistory] at isOldHistory
              cases isOldHistory
              refine ⟨progress history, by simp [isKey, isHistory], ?_⟩
              refine taskFollowsHistory_of_progress oldFollows rfl (Nat.le_refl _)
                (fun isRun => ?_) (fun isReturned => ?_)
              · rw [isNotRun] at isRun; cases isRun
              · have notReturned : history.progress.isReturned = false := by
                  cases hProgress : history.progress with
                  | notRun => rfl
                  | ran => rw [hProgress] at isNotRun; cases isNotRun
                  | returned => rw [hProgress] at isNotRun; cases isNotRun
                rw [notReturned] at isReturned
                cases isReturned
            · exact ⟨oldHistory, by simp [isKey, isOldHistory], oldFollows⟩

end DexExpressiveness.DexF.WPF4
