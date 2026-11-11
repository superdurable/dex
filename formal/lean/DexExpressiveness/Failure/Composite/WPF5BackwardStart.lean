import DexExpressiveness.Failure.Composite.WPF5BackwardAbandon

/-!
# WP-F5: Dex [STEP-START] from α of a run without failures

A pending invocation of α is pending in DexF too, with no retained snapshot
and not in Backoff, so DexF [STEP-START] computes a snapshot. The new real
task gets the task ID that the new virtual task gets.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

theorem wpf5_findInvocation_setNextVirtualTaskId (ghost : Ghost σ) (nextVirtualTaskId : Nat)
    (key : InvocationKey σ) :
    ({ ghost with nextVirtualTaskId := nextVirtualTaskId } : Ghost σ).findInvocation? key =
      ghost.findInvocation? key :=
  rfl

/-- A Step start that computes a snapshot keeps the shape. -/
theorem wpf5_shape_stepStart (config : Config σ) (shape : WPF5FreeShape config) (run : Run σ)
    (hasNoRetryTimers : run.retryTimers = []) (change : Run σ → Run σ)
    (keepsRetryTimers : ∀ current, current.retryTimers = [] → (change current).retryTimers = [])
    (key : InvocationKey σ) (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? key = some history)
    (hasNoArguments : history.arguments = none) (isNotRun : history.progress = .notRun)
    (isZero : history.emittedCount = 0) (isNotCommitted : history.outcome ≠ .committed)
    (task : Task σ) (isTaskKey : task.key = key) (isTaskId : task.base.id = config.nextTaskId)
    (isTaskEmitted : task.emittedCount = 0) (isTaskPhase : task.base.phase = .started)
    (arguments : Arguments σ)
    (isVirtualId : arguments.virtualTaskId = config.ghost.nextVirtualTaskId) :
    WPF5FreeShape
      { (config.commitRun run true none [] change).2 with
        tasks := config.tasks ++ [task]
        nextTaskId := config.nextTaskId + 1
        ghost :=
          { ((config.commitRun run true none [] change).2.ghost.updateInvocation key
              (fun previous => { previous with arguments := some arguments })) with
            nextVirtualTaskId := (config.commitRun run true none [] change).2.ghost.nextVirtualTaskId + 1 } } := by
  have otherKey : ∀ other, other ∈ config.tasks → other.key ≠ key := by
    intro other isOther isSameKey
    obtain ⟨found, _, isFound, isArguments, _⟩ := shape.tasksMatch other isOther
    rw [isSameKey, isHistory] at isFound
    cases isFound
    rw [hasNoArguments] at isArguments
    cases isArguments
  have committedShape := wpf5_shape_commitRun config shape run hasNoRetryTimers [] change
    keepsRetryTimers
  refine
    { isUp := shape.isUp
      noZombies := shape.noZombies
      noInterrupts := shape.noInterrupts
      lastCommitIsNormal := by
        simp only [wpf5_updateInvocation_lastNormalCommitAt]
        exact committedShape.lastCommitIsNormal
      noRetryTimers := committedShape.noRetryTimers
      nextTaskIds := by
        simp only
        rw [shape.nextTaskIds]
        rfl
      tasksMatch := ?_
      liveHaveTasks := ?_
      taskKeysNodup := ?_ }
  · intro other isOther
    simp only [wpf5_findInvocation_setNextVirtualTaskId, wpf5_updateInvocation_find]
    rcases List.mem_append.mp isOther with isOld | isNew
    · rw [if_neg (otherKey other isOld)]
      exact committedShape.tasksMatch other isOld
    · simp only [List.mem_singleton] at isNew
      subst isNew
      obtain ⟨after, isAfter, isArgumentsAfter, isEmittedAfter, isProgressAfter, isOutcomeAfter⟩ :=
        wpf5_commitRun_historyEnds_of_some config run true none [] change key history isHistory
      rw [isTaskKey, if_pos rfl, isAfter]
      refine ⟨_, arguments, rfl, rfl, by rw [isVirtualId, isTaskId, shape.nextTaskIds],
        by rw [isTaskEmitted, isEmittedAfter, isZero], ?_, ?_⟩
      · simp [InvocationHistory.virtualPhase, isProgressAfter, isNotRun, isTaskPhase]
      · simp only
        rcases isOutcomeAfter with isSame | ⟨_, isCanceled⟩
        · rw [isSame]
          exact isNotCommitted
        · rw [isCanceled]
          simp
  · intro otherKeyValue after isAfter isLive hasArguments
    simp only [wpf5_findInvocation_setNextVirtualTaskId, wpf5_updateInvocation_find] at isAfter
    by_cases isKey : otherKeyValue = key
    · subst isKey
      exact ⟨task, List.mem_append_right _ (List.mem_singleton_self _), isTaskKey⟩
    · rw [if_neg isKey] at isAfter
      obtain ⟨other, isOther, isOtherKey⟩ :=
        committedShape.liveHaveTasks otherKeyValue after isAfter isLive hasArguments
      exact ⟨other, List.mem_append_left _ isOther, isOtherKey⟩
  · simp only [List.map_append, List.map_cons, List.map_nil]
    refine List.nodup_append.mpr ⟨shape.taskKeysNodup, (List.nodup_cons.mpr ⟨List.not_mem_nil, List.nodup_nil⟩), ?_⟩
    intro first isFirst second isSecond isSame
    simp only [List.mem_map] at isFirst
    simp only [List.mem_singleton] at isSecond
    obtain ⟨other, isOther, isOtherKey⟩ := isFirst
    subst isSecond isOtherKey
    exact otherKey other isOther (by rw [isSame, isTaskKey])

theorem wpf5_free_stepStart (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config)
    (stepId : Dex.StepExecId σ) (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) (.stepStart stepId) =
      some (dexEvents, dexNext)) :
    WPF5FreeStepResult program config (.stepStart stepId) dexEvents dexNext := by
  cases hasRun : config.run with
  | none => simp [Dex.step, abstractConfig, hasRun] at isDexStep
  | some run =>
    cases isRecord : run.base.findStep? stepId with
    | none =>
      simp [Dex.step, abstractConfig, hasRun, wpf5_abstract_findStep, isRecord] at isDexStep
    | some record =>
      obtain ⟨method, isAbstract, canCommit⟩ : ∃ method,
          run.abstractInvocation config.ghost record = some { method := method, state := .pending } ∧
            config.canCommit = true := by
        simp only [Dex.step, abstractConfig, hasRun, Option.map_some, wpf5_abstract_findStep,
          isRecord] at isDexStep
        split at isDexStep
        · rename_i method isInvocation
          split at isDexStep
          · rename_i isEnabled
            exact ⟨method, isInvocation, by rw [← wpf5_canCommit config shape]; exact isEnabled⟩
          · cases isDexStep
        · cases isDexStep
      have isMember := List.mem_of_find?_eq_some isRecord
      have isRecordId : record.id = stepId := by simpa using List.find?_some isRecord
      have recordMatches := (invariant.runInvariant run hasRun).attemptsMatch record isMember
      have hasNoRetryTimers := shape.noRetryTimers run hasRun
      cases isInvocation : record.invocation with
      | none =>
        unfold Run.abstractInvocation at isAbstract
        rw [isInvocation] at isAbstract
        cases isAbstract
      | some invocation =>
        unfold AttemptMatchesRecord at recordMatches
        rw [isInvocation] at recordMatches
        obtain ⟨attempt, history, isAttempt, isHistory, _, isMethod, _, isRunning, isSnapshotMatch,
          isBackoffClause⟩ := recordMatches
        have hasNoSnapshot : attempt.snapshot = none := by
          cases isSnapshot : attempt.snapshot with
          | none => rfl
          | some snapshot =>
            exfalso
            rw [isSnapshot] at isSnapshotMatch
            cases isArguments : history.arguments with
            | none => rw [isArguments] at isSnapshotMatch; exact isSnapshotMatch
            | some arguments =>
              unfold Run.abstractInvocation at isAbstract
              simp [isInvocation, isAttempt, isSnapshot, Ghost.argumentsOf, isHistory,
                isArguments] at isAbstract
        have hasNoArguments : history.arguments = none := by
          rw [hasNoSnapshot] at isSnapshotMatch
          cases isArguments : history.arguments with
          | none => rfl
          | some arguments => rw [isArguments] at isSnapshotMatch; exact isSnapshotMatch.elim
        have isPending : invocation = { method := method, state := .pending } := by
          cases isState : invocation.state with
          | running taskId =>
            have holdsSnapshot := (isRunning taskId isState).2
            rw [hasNoSnapshot] at holdsSnapshot
            cases holdsSnapshot
          | pending =>
            unfold Run.abstractInvocation at isAbstract
            simp only [isInvocation, isAttempt, hasNoSnapshot, Option.isSome_none] at isAbstract
            cases invocation with
            | mk invocationMethod invocationState =>
              simp only at isState
              subst isState
              first
                | (injection isAbstract with isSome
                   injection isSome with isMethodEq
                   rw [isMethodEq])
                | (injection isAbstract with isMethodEq
                   try rw [isMethodEq])
        subst isPending
        have isNotBackoff : attempt.isBackoff = false := by
          cases isBackoff : attempt.isBackoff with
          | false => rfl
          | true =>
            have isArmed := (isBackoffClause isBackoff).2
            rw [hasNoRetryTimers] at isArmed
            cases isArmed
        subst isRecordId
        have consistent := wpf5_historyConsistent program config invariant _ history isHistory
        unfold HistoryIsConsistent at consistent
        rw [hasNoArguments] at consistent
        obtain ⟨isNotRunBool, isZero, isNotCommitted⟩ := consistent
        have isNotRun : history.progress = .notRun := by
          cases isProgress : history.progress <;> simp_all [VirtualProgress.isNotRun]
        have isServe := wpf5_canServeCommit config shape
        let snapshot := run.base.snapshot record.options method
        let context : Dex.Context σ :=
          { stepExecutionId := record.id, fromStepExecutionId := record.origin }
        let change : Run σ → Run σ := fun current =>
          { (current.setAttempt record.id
              { attempt with
                snapshot := some snapshot
                firstAttemptTimestamp := some (attempt.context config.clock).firstAttemptTimestamp }) with
            base := current.base.updateStep record.id (fun currentRecord =>
              { currentRecord with
                invocation := some { method := method, state := .running config.nextTaskId } }) }
        let task : Task σ :=
          { base :=
              { id := config.nextTaskId, step := record.id, method := method, context := context,
                input := record.input, snapshot := snapshot,
                conditionResults := record.conditionResults, phase := .started }
            attemptContext := attempt.context config.clock
            emittedCount := 0 }
        let committed := config.commitRun run true none [] change
        let arguments : Arguments σ :=
          { context := context, input := record.input, snapshot := snapshot,
            conditionResults := record.conditionResults,
            virtualTaskId := committed.2.ghost.nextVirtualTaskId }
        have isStep : step program config (.stepStart record.id false) =
            some (committed.1,
              { committed.2 with
                tasks := config.tasks ++ [task]
                nextTaskId := config.nextTaskId + 1
                ghost :=
                  { (committed.2.ghost.updateInvocation (record.id, method)
                      (fun previous => { previous with arguments := some arguments })) with
                    nextVirtualTaskId := committed.2.ghost.nextVirtualTaskId + 1 } }) := by
          have isMethodEq : attempt.method = method := isMethod
          simp [step, isServe, canCommit, hasRun, isRecord, isAttempt, isInvocation, isNotBackoff,
            isMethodEq, hasNoSnapshot, committed, change, task, arguments, snapshot, context]
        exact wpf5_free_result program ignoresAttemptContext config invariant _ dexEvents dexNext
          isDexStep (.stepStart record.id false) _ _ isStep _ rfl rfl
          (wpf5_erase_of_notEffect program config _ (by intro _ _ isEqual; cases isEqual) _ _ isStep)
          (wpf5_shape_stepStart config shape run hasNoRetryTimers change
            (fun current isEmpty => isEmpty) _ history isHistory hasNoArguments isNotRun isZero
            isNotCommitted task rfl rfl rfl rfl arguments rfl)
          rfl rfl rfl

end DexExpressiveness.DexF
