import DexExpressiveness.Failure.Proofs.WPF4DexCommit

/-!
# WP-F4: the general lemma for commits that run the meta-functions
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- `Invariant` after a commit whose Step table evolves by the meta-functions,
whose attempt states keep or clear Backoff, and whose retry timers only
shrink. -/
theorem invariant_dexCommit {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    (canCommit : config.canCommit = true) (isUp : config.isUp = true)
    (pre : Config σ) (preRun : pre.run = config.run) (preGhost : pre.ghost = config.ghost)
    (preNextTaskId : pre.nextTaskId = config.nextTaskId) (preUp : pre.isUp = config.isUp)
    (realSub : ∀ task, task ∈ pre.realTasks → task ∈ config.realTasks)
    (realNodup : (pre.realTasks.map (fun task => task.base.id)).Nodup)
    (tasksSub : ∀ task, task ∈ pre.tasks → task ∈ config.tasks)
    (interruptsSub : ∀ interrupt, interrupt ∈ pre.interrupts → interrupt ∈ config.interrupts)
    (changed : Run σ) (registered : Option Nat) (isNormal : Bool)
    (completed : Option (InvocationKey σ))
    (evolve : StepsEvolve registered (bumpedRun run).base changed.base)
    (registeredNormal : ∀ seq, registered = some seq → isNormal = true ∧ seq = run.base.seq + 1)
    (closedQuiet : (∃ status, changed.base.status = .closed status) →
      ∀ record, record ∈ changed.base.steps → IsQuietRecord record)
    (attemptsKept : AttemptsKept run.attempts changed.attempts)
    (backoffArmed : ∀ step attempt, changed.attempts.get? step = some attempt →
      attempt.isBackoff = true →
      changed.retryTimers.contains
        { step := step, method := attempt.method, attempt := attempt.number } = true)
    (retrySub : ∀ entry, entry ∈ changed.retryTimers → entry ∈ run.retryTimers)
    (retryNodup : (changed.retryTimers.map (fun entry => entry.1)).Nodup)
    (completedReturned : ∀ key history, completed = some key →
      config.ghost.findInvocation? key = some history → history.progress.isReturned = true)
    (tasksKept : ∀ task, task ∈ config.tasks → task ∉ pre.tasks →
      ∀ record method, record ∈ changed.base.steps →
        record.invocation ≠ some { method := method, state := .running task.base.id })
    (interruptsKept : ∀ interrupt, interrupt ∈ config.interrupts → interrupt ∉ pre.interrupts →
      ∀ record method taskId, record ∈ changed.base.steps → record.id = interrupt.step →
        record.invocation ≠ some { method := method, state := .running taskId }) :
    Invariant program
      { pre with
        run := some changed.reconcileAttempts
        lastCommitAt := some config.clock
        ghost := (countedGhost config.ghost isNormal (run.base.seq + 1) config.clock).reconcile
          completed changed.reconcileAttempts.base } := by
  have runInvariant := invariant.runInvariant run isRun
  have idsNodup := runInvariant.stepIdsNodup
  have changedNodup : (changed.base.steps.map (fun record => record.id)).Nodup :=
    evolve.idsNodup idsNodup
  have classify : ∀ changedRecord, changedRecord ∈ changed.base.steps →
      (∃ record, record ∈ run.base.steps ∧ RecordStep registered record changedRecord) ∨
        IsNewRecord run.base changed.base changedRecord :=
    evolve.backward
  have newFresh : ∀ changedRecord, IsNewRecord run.base changed.base changedRecord →
      ∀ record, record ∈ run.base.steps → record.id ≠ changedRecord.id :=
    fun _ isNew => newRecord_fresh isNew runInvariant.stepNumbersCounted
  have attemptAfter : ∀ changedRecord, changedRecord ∈ changed.base.steps →
      changed.reconcileAttempts.findAttempt? changedRecord.id =
        reconciledAttempt changed.attempts changedRecord :=
    fun changedRecord isChanged => findAttempt?_reconcileAttempts changed changedRecord
      (show changedRecord ∈ changed.base.steps from isChanged) changedNodup
  -- The unique successor of an old Step.
  have successor : ∀ record changedRecord, record ∈ run.base.steps →
      RecordStep registered record changedRecord → changedRecord ∈ changed.base.steps →
      ∀ other, other ∈ changed.base.steps → other.id = record.id → other = changedRecord :=
    fun _ _ _ isStep isChanged other isOther sameId =>
      eq_of_id_eq changedNodup isOther isChanged (sameId.trans isStep.id.symm)
  have currentOf : ∀ changedRecord invocation, changedRecord ∈ changed.base.steps →
      changedRecord.invocation = some invocation →
      (changedRecord.id, invocation.method) ∈ currentKeys changed.reconcileAttempts.base :=
    fun changedRecord invocation isChanged hasInvocation =>
      (mem_currentKeys _ _).mpr ⟨changedRecord, invocation, isChanged, hasInvocation, rfl, rfl⟩
  -- What an old history becomes.
  have findOld : ∀ key history, config.ghost.findInvocation? key = some history →
      ((countedGhost config.ghost isNormal (run.base.seq + 1) config.clock).reconcile completed
        changed.reconcileAttempts.base).findInvocation? key =
        some (markHistory completed (currentKeys changed.reconcileAttempts.base) key history) :=
    fun key history isFound => reconciled_find_of_some _ _ _ _ _ _ _ _ isFound
  have findFresh : ∀ changedRecord method state, changedRecord ∈ changed.base.steps →
      changedRecord.invocation = some { method := method, state := state } →
      config.ghost.findInvocation? (changedRecord.id, method) = none →
      ((countedGhost config.ghost isNormal (run.base.seq + 1) config.clock).reconcile completed
        changed.reconcileAttempts.base).findInvocation? (changedRecord.id, method) =
        some (InvocationHistory.fresh
          (countedGhost config.ghost isNormal (run.base.seq + 1) config.clock).normalCount) := by
    intro changedRecord method state isChanged hasInvocation isAbsent
    rw [reconciled_find_of_none _ _ _ _ _ _ _ isAbsent,
      if_pos (currentOf changedRecord _ isChanged hasInvocation)]
  have newNoHistory : ∀ changedRecord method, IsNewRecord run.base changed.base changedRecord →
      config.ghost.findInvocation? (changedRecord.id, method) = none := by
    intro changedRecord method isNew
    cases isFound : config.ghost.findInvocation? (changedRecord.id, method) with
    | none => rfl
    | some history =>
      obtain ⟨other, isOther, isId, _⟩ :=
        runInvariant.historiesHaveSteps _ (mem_of_get? _ _ _ isFound)
      exact absurd isId (newFresh changedRecord isNew other isOther)
  have newNoAttempt : ∀ changedRecord, IsNewRecord run.base changed.base changedRecord →
      ∀ attempt, run.attempts.get? changedRecord.id = some attempt → False := by
    intro changedRecord isNew attempt isAttempt
    obtain ⟨other, isOther, isId, _⟩ :=
      runInvariant.attemptsHaveInvocations _ (mem_of_get? _ _ _ isAttempt)
    exact newFresh changedRecord isNew other isOther isId
  obtain ⟨values, increase, taken, keepsIndex, registeredIndex⟩ :=
    countedGhost_normalIndex config.ghost isNormal run.base.seq config.clock
      runInvariant.normalIndexValues runInvariant.normalIndexKeysIncrease
      runInvariant.normalIndexKeysTaken
  obtain ⟨keysNodup, virtualNodup, virtualBelow, consistent⟩ :=
    reconciled_ghost_clauses invariant isNormal (run.base.seq + 1) config.clock completed
      changed.reconcileAttempts.base completedReturned
  -- The attempt of a Step whose invocation stayed.
  have keptAttempt : ∀ record changedRecord invocation, record ∈ run.base.steps →
      RecordStep registered record changedRecord → changedRecord ∈ changed.base.steps →
      record.invocation = some invocation → changedRecord.invocation = some invocation →
      ∃ attempt changedAttempt, run.findAttempt? record.id = some attempt ∧
        changed.reconcileAttempts.findAttempt? changedRecord.id = some changedAttempt ∧
        changed.attempts.get? changedRecord.id = some changedAttempt ∧
        changedAttempt.method = attempt.method ∧ changedAttempt.number = attempt.number ∧
        changedAttempt.snapshot = attempt.snapshot ∧
        (changedAttempt.isBackoff = true → changedAttempt = attempt) ∧
        attempt.method = invocation.method := by
    intro record changedRecord invocation isRecord isStep isChanged hasOld hasNew
    have isMatching := runInvariant.attemptsMatch record isRecord
    unfold AttemptMatchesRecord at isMatching
    rw [hasOld] at isMatching
    obtain ⟨attempt, _, isAttempt, _, _, sameMethod, _⟩ := isMatching
    have isAttemptAtNew : run.attempts.get? changedRecord.id = some attempt := by
      rw [isStep.id]; exact isAttempt
    obtain ⟨changedAttempt, isReconciled, isGet, sameNewMethod, sameNumber, sameSnapshot,
      backoffSame⟩ := reconciled_of_kept attemptsKept hasNew isAttemptAtNew sameMethod
    exact ⟨attempt, changedAttempt, isAttempt, by rw [attemptAfter _ isChanged]; exact isReconciled,
      isGet, sameNewMethod, sameNumber, sameSnapshot, backoffSame, sameMethod⟩
  -- A running Step after the commit kept its invocation.
  have runningKept : ∀ changedRecord method taskId, changedRecord ∈ changed.base.steps →
      changedRecord.invocation = some { method := method, state := .running taskId } →
      ∃ record, record ∈ run.base.steps ∧ RecordStep registered record changedRecord ∧
        record.invocation = some { method := method, state := .running taskId } := by
    intro changedRecord method taskId isChanged hasRunning
    rcases classify changedRecord isChanged with ⟨record, isRecord, isStep⟩ | isNew
    · rcases isStep.invocation with ⟨sameInvocation, _⟩ | ⟨noInvocation, _⟩ |
        ⟨executeInvocation, _⟩
      · exact ⟨record, isRecord, isStep, sameInvocation ▸ hasRunning⟩
      · rw [noInvocation] at hasRunning; cases hasRunning
      · rw [executeInvocation] at hasRunning; simp at hasRunning
    · obtain ⟨_, _, _, newMethod, hasPending, _⟩ := isNew
      rw [hasPending] at hasRunning
      simp at hasRunning
  have changedMatches : ∀ changedRecord, changedRecord ∈ changed.base.steps →
      match changedRecord.invocation with
      | some invocation =>
        changedRecord.phase = (match invocation.method with
          | .waitFor => Dex.Phase.waitFor
          | .execute => Dex.Phase.execute)
      | none => changedRecord.phase ≠ .waitFor ∧ changedRecord.phase ≠ .execute := by
    intro changedRecord isChanged
    rcases classify changedRecord isChanged with ⟨record, isRecord, isStep⟩ | isNew
    · rcases isStep.invocation with ⟨sameInvocation, _, _, samePhase⟩ |
        ⟨noInvocation, notWaitFor, notExecute⟩ | ⟨executeInvocation, executePhase, _⟩
      · rw [sameInvocation, samePhase]
        exact runInvariant.invocationMatchesPhase record isRecord
      · rw [noInvocation]
        exact ⟨notWaitFor, notExecute⟩
      · rw [executeInvocation]
        exact executePhase
    · obtain ⟨_, _, _, newMethod, hasPending, hasPhase⟩ := isNew
      rw [hasPending]
      simp only
      rw [hasPhase]
      cases newMethod <;> rfl
  refine
    { realTaskIdsNodup := realNodup
      realTaskIdsBelow := ?_
      interruptsNotInTasks := ?_
      interruptsBelow := ?_
      downIsEmpty := ?_
      lastNormalCommitAtMost := countedGhost_lastNormal _ _ _ _ _ invariant.lastNormalCommitAtMost
        (lt_of_canCommit canCommit)
      notStarted := fun isNone => by cases isNone
      ghostKeysNodup := keysNodup
      virtualTaskIdsNodup := virtualNodup
      virtualTaskIdsBelow := ?_
      historiesConsistent := consistent
      realTasksFollowHistory := ?_
      liveTasksPassFence := ?_
      runInvariant := ?_ }
  · intro task isTask
    show task.base.id < pre.nextTaskId
    rw [preNextTaskId]
    exact invariant.realTaskIdsBelow task (realSub task isTask)
  · intro interrupt task isInterrupt isTask
    exact invariant.interruptsNotInTasks interrupt task (interruptsSub interrupt isInterrupt)
      (tasksSub task isTask)
  · intro interrupt isInterrupt
    show interrupt.taskId < pre.nextTaskId
    rw [preNextTaskId]
    exact invariant.interruptsBelow interrupt (interruptsSub interrupt isInterrupt)
  · intro isDown
    have isPreDown : pre.isUp = false := isDown
    rw [preUp, isUp] at isPreDown
    cases isPreDown
  · intro entry arguments isMember hasArguments
    have := virtualBelow entry arguments isMember hasArguments
    simpa [countedGhost_nextVirtual] using this
  · intro task isTask
    exact follows_reconciled invariant isNormal (run.base.seq + 1) config.clock completed
      changed.reconcileAttempts.base (realSub task isTask)
  · intro task history isTask isHistory isLive
    show Config.isFencePassed { pre with run := some changed.reconcileAttempts } task = true
    obtain ⟨oldHistory, isOldHistory, _⟩ :=
      invariant.realTasksFollowHistory task (List.mem_append_left _ (tasksSub task isTask))
    rw [findOld _ _ isOldHistory] at isHistory
    cases isHistory
    obtain ⟨wasLive, isCurrent⟩ := markHistory_live _ _ _ _ isLive
    have isOldPassed := invariant.liveTasksPassFence task oldHistory (tasksSub task isTask)
      isOldHistory wasLive
    obtain ⟨record, attempt, isRecord, isStepId, isAttempt, hasRunning, isNumber⟩ :=
      (isFencePassed_iff isRun idsNodup task).mp isOldPassed
    obtain ⟨changedRecord, isChanged, isStep⟩ := evolve.forward record isRecord
    obtain ⟨other, invocation, isOther, hasOther, isOtherId, isOtherMethod⟩ :=
      (mem_currentKeys _ _).mp isCurrent
    have isSame := successor record changedRecord isRecord isStep isChanged other isOther
      (by rw [isOtherId, isStepId]; rfl)
    subst isSame
    rcases isStep.invocation with ⟨sameInvocation, _⟩ | ⟨noInvocation, _⟩ |
      ⟨executeInvocation, _, isBefore⟩
    · obtain ⟨found, changedAttempt, isFound, isChangedAttempt, _, _, sameNumber, _⟩ :=
        keptAttempt record other _ isRecord isStep isChanged hasRunning
          (sameInvocation.trans hasRunning)
      rw [isAttempt] at isFound
      cases isFound
      rw [isFencePassed_iff (run := changed.reconcileAttempts) rfl changedNodup task]
      exact ⟨other, changedAttempt, isChanged, isStep.id.trans isStepId, isChangedAttempt,
        sameInvocation.trans hasRunning, sameNumber.trans isNumber⟩
    · rw [noInvocation] at hasOther; cases hasOther
    · rw [executeInvocation] at hasOther
      cases hasOther
      have isWaitFor := waitFor_of_before invariant isRun isRecord isBefore hasRunning
      simp only [Task.key] at isOtherMethod isWaitFor
      rw [← isOtherMethod] at isWaitFor
      cases isWaitFor
  · intro after isAfter
    cases isAfter
    exact
      { stepIdsNodup := changedNodup
        stepNumbersCounted := by
          intro changedRecord isChanged
          rcases classify changedRecord isChanged with ⟨record, isRecord, isStep⟩ | isNew
          · rw [isStep.id]
            exact Nat.le_trans (runInvariant.stepNumbersCounted record isRecord)
              (evolve.countersGrow _)
          · exact isNew.2.1
        invocationMatchesPhase := changedMatches
        closedRunIsQuiet := fun isClosed changedRecord isChanged =>
          closedQuiet isClosed changedRecord isChanged
        historiesHaveSteps := by
          apply reconciled_historiesHaveSteps _ _ _ _ _ _ _ changedMatches
          intro entry isMember
          obtain ⟨record, isRecord, isId, isExecute⟩ := runInvariant.historiesHaveSteps entry isMember
          obtain ⟨changedRecord, isChanged, isStep⟩ := evolve.forward record isRecord
          refine ⟨changedRecord, isChanged, isStep.id.trans isId, fun isExecuteKey => ?_⟩
          exact (isExecuteOrDone_iff _).mp
            (isStep.executeKept ((isExecuteOrDone_iff _).mpr (isExecute isExecuteKey)))
        retryTimersHaveHistories := by
          apply reconciled_retryTimersHaveHistories
          intro entry isEntry
          exact Or.inl (runInvariant.retryTimersHaveHistories entry (retrySub entry isEntry))
        retryTimersNotAhead := by
          intro entry changedRecord invocation found isEntry isChanged isId hasInvocation isMethod
            isFound
          have isOldEntry := retrySub entry isEntry
          rcases classify changedRecord isChanged with ⟨record, isRecord, isStep⟩ | isNew
          · rcases isStep.invocation with ⟨sameInvocation, _⟩ | ⟨noInvocation, _⟩ |
              ⟨executeInvocation, _, isBefore⟩
            · obtain ⟨attempt, changedAttempt, isAttempt, isChangedAttempt, _, _, sameNumber, _⟩ :=
                keptAttempt record changedRecord invocation isRecord isStep isChanged
                  (sameInvocation ▸ hasInvocation) hasInvocation
              rw [isChangedAttempt] at isFound
              cases isFound
              rw [sameNumber]
              exact runInvariant.retryTimersNotAhead entry record invocation attempt isOldEntry
                isRecord (isStep.id.symm.trans isId) (sameInvocation ▸ hasInvocation) isMethod
                isAttempt
            · rw [noInvocation] at hasInvocation; cases hasInvocation
            · rw [executeInvocation] at hasInvocation
              cases hasInvocation
              obtain ⟨history, isHistory⟩ := runInvariant.retryTimersHaveHistories entry isOldEntry
              have isKey : (entry.1.step, entry.1.method) = (record.id, Dex.Method.execute) := by
                rw [← isMethod, ← isId, isStep.id]
              rw [isKey, noExecuteHistory invariant isRun isRecord isBefore] at isHistory
              cases isHistory
          · obtain ⟨history, isHistory⟩ := runInvariant.retryTimersHaveHistories entry isOldEntry
            rw [← isId, newNoHistory changedRecord _ isNew] at isHistory
            cases isHistory
        waitTimersNodup := evolve.timersNodup runInvariant.waitTimersNodup
        retryTimersNodup := retryNodup
        attemptKeysNodup := nodup_reconcileAttempts changed changedNodup
        attemptsHaveInvocations := mem_reconcileAttempts changed
        normalIndexValues := values
        normalIndexKeysIncrease := increase
        normalIndexKeysTaken := by
          intro entry isMember
          show entry.1 ≤ changed.base.seq
          rw [evolve.seq]
          exact taken entry isMember
        waitSeqsIndexed := by
          intro changedRecord waitState isChanged hasWait
          rcases classify changedRecord isChanged with ⟨record, isRecord, isStep⟩ | isNew
          · rcases isStep.waitKept waitState hasWait with ⟨oldWait, isOldWait, sameSeq⟩ |
              isRegistered
            · rw [← sameSeq]
              exact keepsIndex _ (runInvariant.waitSeqsIndexed record oldWait isRecord isOldWait)
            · obtain ⟨isNormalCommit, isSeq⟩ := registeredNormal _ isRegistered
              rw [isSeq]
              exact registeredIndex isNormalCommit
          · rw [isNew.2.2.1] at hasWait
            cases hasWait
        attemptsMatch := by
          intro changedRecord isChanged
          unfold AttemptMatchesRecord
          rcases classify changedRecord isChanged with ⟨record, isRecord, isStep⟩ | isNew
          · rcases isStep.invocation with ⟨sameInvocation, sameInput, sameConditionResults, _⟩ |
              ⟨noInvocation, _⟩ | ⟨executeInvocation, _, isBefore⟩
            · have isMatching := runInvariant.attemptsMatch record isRecord
              unfold AttemptMatchesRecord at isMatching
              rw [sameInvocation]
              split at isMatching
              · rename_i hasNone
                rw [attemptAfter _ isChanged]
                unfold reconciledAttempt
                rw [sameInvocation, hasNone]
              · rename_i invocation hasSome
                obtain ⟨attempt, history, isAttempt, isHistory, isLive, sameMethod, isPositive,
                  isRunning, snapshotMatches, isBackoff⟩ := isMatching
                obtain ⟨found, changedAttempt, isFound, isChangedAttempt, isGet, sameNewMethod,
                  sameNumber, sameSnapshot, backoffSame, _⟩ :=
                  keptAttempt record changedRecord invocation isRecord isStep isChanged hasSome
                    (sameInvocation.trans hasSome)
                rw [isAttempt] at isFound
                cases isFound
                have isCurrentKey := currentOf changedRecord invocation isChanged
                  (sameInvocation.trans hasSome)
                refine ⟨changedAttempt, history, isChangedAttempt, ?_, isLive,
                  sameNewMethod.trans sameMethod, by rw [sameNumber]; exact isPositive, ?_, ?_, ?_⟩
                · rw [isStep.id, findOld _ _ isHistory, ← isStep.id,
                    markHistory_of_current _ _ _ _ isCurrentKey]
                · intro runningId isRunningState
                  obtain ⟨notBackoff, hasSnapshot⟩ := isRunning runningId isRunningState
                  refine ⟨?_, by rw [sameSnapshot]; exact hasSnapshot⟩
                  cases isChangedBackoff : changedAttempt.isBackoff with
                  | false => rfl
                  | true =>
                    rw [backoffSame isChangedBackoff] at isChangedBackoff
                    rw [notBackoff] at isChangedBackoff
                    cases isChangedBackoff
                · rw [sameSnapshot, sameInput, sameConditionResults, isStep.id, isStep.origin]
                  exact snapshotMatches
                · intro isChangedBackoff
                  have isSameAttempt := backoffSame isChangedBackoff
                  have isPending := (isBackoff (isSameAttempt ▸ isChangedBackoff)).1
                  refine ⟨isPending, ?_⟩
                  have armed := backoffArmed changedRecord.id changedAttempt isGet isChangedBackoff
                  rw [sameNewMethod, sameMethod] at armed
                  exact armed
            · rw [noInvocation]
              simp only
              rw [attemptAfter _ isChanged]
              unfold reconciledAttempt
              rw [noInvocation]
            · rw [executeInvocation]
              simp only
              have isFresh := findFresh changedRecord .execute .pending isChanged executeInvocation
                (by rw [isStep.id]; exact noExecuteHistory invariant isRun isRecord isBefore)
              have isFreshAttempt : reconciledAttempt changed.attempts changedRecord =
                  some (Attempt.fresh .execute) := by
                apply reconciled_fresh attemptsKept executeInvocation
                intro attempt isAttempt sameMethod
                rw [isStep.id] at isAttempt
                have isMatching := runInvariant.attemptsMatch record isRecord
                unfold AttemptMatchesRecord at isMatching
                split at isMatching
                · unfold Run.findAttempt? at isMatching
                  rw [isMatching] at isAttempt
                  cases isAttempt
                · rename_i invocation hasSome
                  obtain ⟨found, _, isFound, _, _, foundMethod, _⟩ := isMatching
                  unfold Run.findAttempt? at isFound
                  rw [isFound] at isAttempt
                  cases isAttempt
                  have isWaitFor := waitFor_of_before invariant isRun isRecord isBefore hasSome
                  rw [← foundMethod, sameMethod] at isWaitFor
                  cases isWaitFor
              refine ⟨Attempt.fresh .execute, _, by rw [attemptAfter _ isChanged]; exact isFreshAttempt,
                isFresh, rfl, rfl, Nat.le_refl _, fun _ isRunningState => (by cases isRunningState),
                trivial, fun isFreshBackoff => (by cases isFreshBackoff)⟩
          · obtain ⟨_, _, _, newMethod, hasPending, _⟩ := id isNew
            rw [hasPending]
            simp only
            have isFresh := findFresh changedRecord newMethod .pending isChanged hasPending
              (newNoHistory changedRecord newMethod isNew)
            have isFreshAttempt : reconciledAttempt changed.attempts changedRecord =
                some (Attempt.fresh newMethod) :=
              reconciled_fresh attemptsKept hasPending
                (fun attempt isAttempt => absurd (newNoAttempt changedRecord isNew attempt isAttempt)
                  id)
            refine ⟨Attempt.fresh newMethod, _, by rw [attemptAfter _ isChanged]; exact isFreshAttempt,
              isFresh, rfl, rfl, Nat.le_refl _, fun _ isRunningState => (by cases isRunningState),
              trivial, fun isFreshBackoff => (by cases isFreshBackoff)⟩
        liveIsCurrent := by
          intro key history isFound isLive
          exact reconciled_liveIsCurrent _ _ _ _ _ _ key history isFound isLive
        runningTaskIdsBelow := by
          intro changedRecord method taskId isChanged hasRunning
          obtain ⟨record, isRecord, _, hasOld⟩ :=
            runningKept changedRecord method taskId isChanged hasRunning
          show taskId < pre.nextTaskId
          rw [preNextTaskId]
          exact runInvariant.runningTaskIdsBelow record method taskId isRecord hasOld
        runningTaskMatches := by
          intro changedRecord method taskId task isChanged hasRunning isTask isId
          obtain ⟨record, isRecord, isStep, hasOld⟩ :=
            runningKept changedRecord method taskId isChanged hasRunning
          obtain ⟨attempt, changedAttempt, isAttempt, isChangedAttempt, _, _, sameNumber, _⟩ :=
            keptAttempt record changedRecord _ isRecord isStep isChanged hasOld hasRunning
          obtain ⟨isKey, found, isFound, isNumber⟩ := runInvariant.runningTaskMatches record method
            taskId task isRecord hasOld (realSub task isTask) isId
          rw [isAttempt] at isFound
          cases isFound
          exact ⟨by rw [isKey, isStep.id], changedAttempt, isChangedAttempt,
            by rw [sameNumber]; exact isNumber⟩
        runningIsTracked := by
          intro changedRecord method taskId isChanged hasRunning
          obtain ⟨record, isRecord, isStep, hasOld⟩ :=
            runningKept changedRecord method taskId isChanged hasRunning
          obtain ⟨attempt, changedAttempt, isAttempt, isChangedAttempt, _, _, sameNumber, _⟩ :=
            keptAttempt record changedRecord _ isRecord isStep isChanged hasOld hasRunning
          rcases runInvariant.runningIsTracked record method taskId isRecord hasOld with
            ⟨task, isTask, isId⟩ | isDown | ⟨found, isFound, isInterrupt⟩
          · left
            refine ⟨task, ?_, isId⟩
            by_cases isKept : task ∈ pre.tasks
            · exact isKept
            · exact absurd (by rw [isId]; exact hasRunning)
                (tasksKept task isTask isKept changedRecord method isChanged)
          · right; left
            show pre.isUp = false
            rw [preUp]
            exact isDown
          · rw [isAttempt] at isFound
            cases isFound
            right; right
            refine ⟨changedAttempt, isChangedAttempt, ?_⟩
            rw [sameNumber, isStep.id]
            by_cases isKept :
              (⟨record.id, method, attempt.number, taskId⟩ : Interrupt σ) ∈ pre.interrupts
            · exact isKept
            · exact absurd hasRunning (interruptsKept _ isInterrupt isKept changedRecord method
                taskId isChanged isStep.id) }

end DexExpressiveness.DexF.WPF4
