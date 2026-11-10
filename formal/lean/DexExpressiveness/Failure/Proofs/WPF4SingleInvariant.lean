import DexExpressiveness.Failure.Proofs.WPF4Single

/-!
# WP-F4: `Invariant` after a commit that pends one invocation

The general lemma behind [ATTEMPT-LOST] with RetryAt, [ATTEMPT-RETURNED],
[ATTEMPT-HEARTBEAT], and an applied [RECOVER-INTERRUPT] with RetryAt.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- `AttemptMatchesRecord` of a Step whose attempt state, histories, and
retry timers stay. -/
theorem attemptMatchesRecord_congr {run after : Run σ} {ghost changedGhost : Ghost σ}
    {record : Dex.StepRecord σ} (sameAttempt : after.findAttempt? record.id = run.findAttempt? record.id)
    (sameFind : ∀ key, changedGhost.findInvocation? key = ghost.findInvocation? key)
    (timersKept : ∀ timer : RetryTimerId σ, timer.step = record.id →
      run.retryTimers.contains timer = true → after.retryTimers.contains timer = true)
    (isMatching : AttemptMatchesRecord run ghost record) :
    AttemptMatchesRecord after changedGhost record := by
  unfold AttemptMatchesRecord at isMatching ⊢
  split at isMatching
  · rw [sameAttempt]; exact isMatching
  · obtain ⟨attempt, history, isAttempt, isHistory, isLive, sameMethod, isPositive, isRunning,
      snapshotMatches, isBackoff⟩ := isMatching
    refine ⟨attempt, history, by rw [sameAttempt]; exact isAttempt,
      by rw [sameFind]; exact isHistory, isLive, sameMethod, isPositive, isRunning,
      snapshotMatches, fun backoff => ?_⟩
    obtain ⟨isPending, isContained⟩ := isBackoff backoff
    exact ⟨isPending, timersKept _ rfl isContained⟩

/-- `Invariant` after a commit that pends the invocation of one Step `e`,
changes its attempt state, and may arm a retry timer of `e`. -/
theorem invariant_singleRecord {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    (canCommit : config.canCommit = true) (isUp : config.isUp = true)
    (pre : Config σ) (preRun : pre.run = config.run) (preGhost : pre.ghost = config.ghost)
    (preNextTaskId : pre.nextTaskId = config.nextTaskId) (preUp : pre.isUp = config.isUp)
    (realSub : ∀ task, task ∈ pre.realTasks → task ∈ config.realTasks)
    (realNodup : (pre.realTasks.map (fun task => task.base.id)).Nodup)
    (tasksSub : ∀ task, task ∈ pre.tasks → task ∈ config.tasks)
    (interruptsSub : ∀ interrupt, interrupt ∈ pre.interrupts → interrupt ∈ config.interrupts)
    (record : Dex.StepRecord σ) (isRecord : record ∈ run.base.steps)
    (method : Dex.Method) (oldState newState : Dex.InvocationState)
    (hasInvocation : record.invocation = some { method := method, state := oldState })
    (tasksKept : ∀ task, task ∈ config.tasks → task ∉ pre.tasks →
      oldState = .running task.base.id)
    (removedRunning : ∀ taskId, oldState = .running taskId → newState = .pending →
      ∀ task, task ∈ pre.tasks → task.base.id ≠ taskId)
    (interruptsKept : ∀ interrupt, interrupt ∈ config.interrupts → interrupt ∉ pre.interrupts →
      interrupt.step = record.id)
    (attempt newAttempt : Attempt σ) (isAttempt : run.findAttempt? record.id = some attempt)
    (newMethod : newAttempt.method = method) (newPositive : 1 ≤ newAttempt.number)
    (newSnapshot : newAttempt.snapshot = attempt.snapshot)
    (keptIfRunning : ∀ taskId, newState = .running taskId →
      oldState = .running taskId ∧ newAttempt.number = attempt.number ∧
        newAttempt.isBackoff = attempt.isBackoff ∧ pre.tasks = config.tasks ∧
        pre.interrupts = config.interrupts)
    (changed : Run σ)
    (changedSteps : changed.base.steps = run.base.steps.map (fun current =>
      if current.id = record.id then setInvocation method newState current else current))
    (changedCounters : changed.base.counters = run.base.counters)
    (changedTimers : changed.base.timers = run.base.timers)
    (changedStatus : changed.base.status = run.base.status)
    (changedSeq : changed.base.seq = run.base.seq + 1)
    (changedAttemptSelf : changed.attempts.get? record.id = some newAttempt)
    (changedAttemptOther : ∀ step, step ≠ record.id → changed.attempts.get? step = run.attempts.get? step)
    (retryFrom : ∀ entry, entry ∈ changed.retryTimers →
      entry ∈ run.retryTimers ∨ (entry.1.step = record.id ∧ entry.1.method = method))
    (retryKept : ∀ timer : RetryTimerId σ, timer.step ≠ record.id →
      run.retryTimers.contains timer = true → changed.retryTimers.contains timer = true)
    (retryNodup : (changed.retryTimers.map (fun entry => entry.1)).Nodup)
    (newBackoff : newAttempt.isBackoff = true → newState = .pending ∧
      changed.retryTimers.contains
        { step := record.id, method := method, attempt := newAttempt.number } = true)
    (newAhead : ∀ entry, entry ∈ changed.retryTimers → entry.1.step = record.id →
      entry.1.method = method → entry.1.attempt ≤ newAttempt.number)
    (isNormal : Bool) :
    Invariant program
      { pre with
        run := some changed.reconcileAttempts
        lastCommitAt := some config.clock
        ghost := (countedGhost config.ghost isNormal (run.base.seq + 1) config.clock).reconcile
          none changed.reconcileAttempts.base } := by
  have runInvariant := invariant.runInvariant run isRun
  have idsNodup := runInvariant.stepIdsNodup
  have changedIds : changed.base.steps.map (fun record => record.id) =
      run.base.steps.map (fun record => record.id) := by
    rw [changedSteps]; exact ids_singleRecord _ _ _ _
  have changedNodup : (changed.base.steps.map (fun record => record.id)).Nodup := by
    rw [changedIds]; exact idsNodup
  have isOnly : ∀ other, other ∈ run.base.steps → other.id = record.id → other = record :=
    fun other isOther sameId => eq_of_id_eq idsNodup isOther isRecord sameId
  have memChanged : ∀ changedRecord, changedRecord ∈ changed.base.steps →
      (changedRecord = setInvocation method newState record) ∨
      (changedRecord ∈ run.base.steps ∧ changedRecord.id ≠ record.id) := by
    intro changedRecord isChanged
    rw [changedSteps] at isChanged
    obtain ⟨original, isOriginal, ⟨isStep, isPended⟩ | ⟨isOther, isSame⟩⟩ := mem_singleRecord isChanged
    · left
      rw [isPended, isOnly original isOriginal isStep]
    · right
      rw [isSame]
      exact ⟨isOriginal, isOther⟩
  have pendedMember : setInvocation method newState record ∈ changed.base.steps := by
    rw [changedSteps]
    exact List.mem_map.mpr ⟨record, isRecord, by simp⟩
  have otherMember : ∀ other, other ∈ run.base.steps → other.id ≠ record.id →
      other ∈ changed.base.steps := by
    intro other isOther isNotRecord
    rw [changedSteps]
    exact List.mem_map.mpr ⟨other, isOther, by simp [isNotRecord]⟩
  have currentSame : ∀ key, key ∈ currentKeys changed.base ↔ key ∈ currentKeys run.base := by
    apply currentKeys_singleRecord run.base changed.base record.id method newState changedSteps
    intro other isOther sameId
    rw [isOnly other isOther sameId]
    exact ⟨oldState, hasInvocation⟩
  have findEq : ∀ key,
      ((countedGhost config.ghost isNormal (run.base.seq + 1) config.clock).reconcile none
        changed.reconcileAttempts.base).findInvocation? key = config.ghost.findInvocation? key := by
    apply reconciled_find_eq
    · intro key history isFound isLive
      rw [show changed.reconcileAttempts.base = changed.base from rfl, currentSame]
      obtain ⟨current, state, isCurrent, isId, hasCurrent⟩ :=
        runInvariant.liveIsCurrent key history isFound isLive
      exact (mem_currentKeys run.base key).mpr ⟨current, _, isCurrent, hasCurrent, isId, rfl⟩
    · intro key isCurrent
      rw [show changed.reconcileAttempts.base = changed.base from rfl, currentSame] at isCurrent
      obtain ⟨current, invocation, isMember, hasCurrent, isId, isMethod⟩ :=
        (mem_currentKeys run.base key).mp isCurrent
      have isMatching := runInvariant.attemptsMatch current isMember
      unfold AttemptMatchesRecord at isMatching
      rw [hasCurrent] at isMatching
      obtain ⟨_, history, _, isHistory, _⟩ := isMatching
      refine ⟨history, ?_⟩
      have isKey : key = (current.id, invocation.method) := by
        obtain ⟨first, second⟩ := key
        simp only at isId isMethod
        rw [isId, isMethod]
      rw [isKey]
      exact isHistory
  have attemptOther : ∀ other, other ∈ run.base.steps → other.id ≠ record.id →
      changed.reconcileAttempts.findAttempt? other.id = run.findAttempt? other.id := by
    intro other isOther isNotRecord
    rw [findAttempt?_reconcileAttempts changed other (otherMember other isOther isNotRecord)
      changedNodup]
    unfold reconciledAttempt
    rw [changedAttemptOther other.id isNotRecord]
    have isMatching := runInvariant.attemptsMatch other isOther
    unfold AttemptMatchesRecord at isMatching
    split
    · rename_i hasNone
      rw [hasNone] at isMatching
      exact isMatching.symm
    · rename_i invocation hasSome
      rw [hasSome] at isMatching
      obtain ⟨found, _, isFound, _, _, sameMethod, _⟩ := isMatching
      unfold Run.findAttempt? at isFound
      rw [isFound]
      simp [sameMethod, Run.findAttempt?, isFound]
  have attemptSelf : changed.reconcileAttempts.findAttempt? record.id = some newAttempt := by
    have isPendedAttempt := findAttempt?_reconcileAttempts changed (setInvocation method newState record)
      pendedMember changedNodup
    rw [show (setInvocation method newState record).id = record.id from rfl] at isPendedAttempt
    rw [isPendedAttempt]
    unfold reconciledAttempt
    simp [setInvocation, changedAttemptSelf, newMethod]
  have statusRunning : run.base.status = .running := by
    cases isStatus : run.base.status with
    | running => rfl
    | closed status =>
      have := (runInvariant.closedRunIsQuiet ⟨status, isStatus⟩ record isRecord).2
      rw [hasInvocation] at this
      cases this
  obtain ⟨values, increase, taken, keepsIndex, _⟩ := countedGhost_normalIndex config.ghost isNormal
    run.base.seq config.clock runInvariant.normalIndexValues runInvariant.normalIndexKeysIncrease
    runInvariant.normalIndexKeysTaken
  obtain ⟨keysNodup, virtualNodup, virtualBelow, consistent⟩ :=
    reconciled_ghost_clauses invariant isNormal (run.base.seq + 1) config.clock none
      changed.reconcileAttempts.base (fun _ _ isNone => by cases isNone)
  refine
    { realTaskIdsNodup := realNodup
      realTaskIdsBelow := ?_
      interruptsNotInTasks := ?_
      interruptsBelow := ?_
      downIsEmpty := ?_
      lastNormalCommitAtMost := ?_
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
  · exact countedGhost_lastNormal _ _ _ _ _ invariant.lastNormalCommitAtMost
      (lt_of_canCommit canCommit)
  · intro entry arguments isMember hasArguments
    have := virtualBelow entry arguments isMember hasArguments
    simpa [countedGhost_nextVirtual] using this
  · intro task isTask
    exact follows_reconciled invariant isNormal (run.base.seq + 1) config.clock none
      changed.reconcileAttempts.base (realSub task isTask)
  · intro task history isTask isHistory isLive
    show Config.isFencePassed { pre with run := some changed.reconcileAttempts } task = true
    rw [findEq] at isHistory
    have isOldPassed := invariant.liveTasksPassFence task history (tasksSub task isTask) isHistory
      isLive
    obtain ⟨current, found, isCurrent, isStep, isFound, hasCurrent, isNumber⟩ :=
      (isFencePassed_iff isRun idsNodup task).mp isOldPassed
    rw [isFencePassed_iff (run := changed.reconcileAttempts) rfl changedNodup task]
    by_cases isRecordId : current.id = record.id
    · have isSame := isOnly current isCurrent isRecordId
      rw [isSame, hasInvocation] at hasCurrent
      simp only [Option.some.injEq, Dex.Invocation.mk.injEq] at hasCurrent
      rw [isSame, isAttempt] at isFound
      cases isFound
      cases hState : newState with
      | pending =>
        exact absurd rfl (removedRunning task.base.id hasCurrent.2 hState task isTask)
      | running runningId =>
        obtain ⟨isOldRunning, sameNumber, _⟩ := keptIfRunning runningId hState
        rw [hasCurrent.2] at isOldRunning
        cases isOldRunning
        refine ⟨setInvocation method newState record, newAttempt, pendedMember,
          by rw [← isStep, isSame]; rfl, attemptSelf, ?_, by rw [sameNumber]; exact isNumber⟩
        rw [hState, hasCurrent.1]
        rfl
    · exact ⟨current, found, otherMember current isCurrent isRecordId, isStep,
        by rw [attemptOther current isCurrent isRecordId]; exact isFound, hasCurrent, isNumber⟩
  · intro after isAfter
    cases isAfter
    exact
      { stepIdsNodup := changedNodup
        stepNumbersCounted := by
          intro changedRecord isChanged
          show _ ≤ (changed.base.counters.get? _).getD 0
          rw [changedCounters]
          rcases memChanged changedRecord isChanged with isPended | ⟨isOld, _⟩
          · rw [isPended]
            exact runInvariant.stepNumbersCounted record isRecord
          · exact runInvariant.stepNumbersCounted changedRecord isOld
        invocationMatchesPhase := by
          intro changedRecord isChanged
          rcases memChanged changedRecord isChanged with isPended | ⟨isOld, _⟩
          · rw [isPended]
            have phaseMatches := runInvariant.invocationMatchesPhase record isRecord
            rw [hasInvocation] at phaseMatches
            exact phaseMatches
          · exact runInvariant.invocationMatchesPhase changedRecord isOld
        closedRunIsQuiet := by
          rintro ⟨status, isClosed⟩
          have isOldClosed : run.base.status = .closed status := by
            rw [← changedStatus]; exact isClosed
          rw [statusRunning] at isOldClosed
          cases isOldClosed
        historiesHaveSteps := by
          apply reconciled_historiesHaveSteps
          · intro entry isMember
            obtain ⟨original, isOriginal, isId, isExecute⟩ :=
              runInvariant.historiesHaveSteps entry isMember
            by_cases isRecordId : original.id = record.id
            · rw [isOnly original isOriginal isRecordId] at isId isExecute
              exact ⟨setInvocation method newState record, pendedMember, isId, isExecute⟩
            · exact ⟨original, otherMember original isOriginal isRecordId, isId, isExecute⟩
          · intro changedRecord isChanged
            rcases memChanged changedRecord isChanged with isPended | ⟨isOld, _⟩
            · rw [isPended]
              have phaseMatches := runInvariant.invocationMatchesPhase record isRecord
              rw [hasInvocation] at phaseMatches
              exact phaseMatches
            · exact runInvariant.invocationMatchesPhase changedRecord isOld
        retryTimersHaveHistories := by
          apply reconciled_retryTimersHaveHistories
          intro entry isMember
          rcases retryFrom entry isMember with isOld | ⟨isStep, isMethod⟩
          · exact Or.inl (runInvariant.retryTimersHaveHistories entry isOld)
          · right
            rw [isStep, isMethod]
            exact (mem_currentKeys _ _).mpr ⟨setInvocation method newState record, _, pendedMember, rfl,
              rfl, rfl⟩
        retryTimersNotAhead := by
          intro entry changedRecord invocation found isEntry isChanged isId hasCurrent isMethod
            isFound
          rcases memChanged changedRecord isChanged with isPended | ⟨isOld, isNotRecord⟩
          · subst isPended
            have isSelf : changed.reconcileAttempts.findAttempt? record.id = some found := isFound
            rw [attemptSelf] at isSelf
            cases isSelf
            simp only [setInvocation, Option.some.injEq] at hasCurrent
            rw [← hasCurrent] at isMethod
            exact newAhead entry isEntry isId.symm isMethod.symm
          · have isOldEntry : entry ∈ run.retryTimers := by
              rcases retryFrom entry isEntry with isOldEntry | ⟨isStep, _⟩
              · exact isOldEntry
              · exact absurd (isId.trans isStep) isNotRecord
            rw [attemptOther changedRecord isOld isNotRecord] at isFound
            exact runInvariant.retryTimersNotAhead entry changedRecord invocation found isOldEntry
              isOld isId hasCurrent isMethod isFound
        waitTimersNodup := by
          show (changed.base.timers.map _).Nodup
          rw [changedTimers]
          exact runInvariant.waitTimersNodup
        retryTimersNodup := retryNodup
        attemptKeysNodup := nodup_reconcileAttempts changed changedNodup
        attemptsHaveInvocations := mem_reconcileAttempts changed
        normalIndexValues := values
        normalIndexKeysIncrease := increase
        normalIndexKeysTaken := by
          intro entry isMember
          show entry.1 ≤ changed.base.seq
          rw [changedSeq]
          exact taken entry isMember
        waitSeqsIndexed := by
          intro changedRecord waitState isChanged hasWait
          apply keepsIndex
          rcases memChanged changedRecord isChanged with isPended | ⟨isOld, _⟩
          · rw [isPended] at hasWait
            exact runInvariant.waitSeqsIndexed record waitState isRecord hasWait
          · exact runInvariant.waitSeqsIndexed changedRecord waitState isOld hasWait
        attemptsMatch := by
          intro changedRecord isChanged
          rcases memChanged changedRecord isChanged with isPended | ⟨isOld, isNotRecord⟩
          · subst isPended
            have isMatching := runInvariant.attemptsMatch record isRecord
            unfold AttemptMatchesRecord at isMatching ⊢
            rw [hasInvocation] at isMatching
            obtain ⟨found, history, isFound, isHistory, isLive, _, _, oldRunning, snapshotMatches,
              _⟩ := isMatching
            rw [isAttempt] at isFound
            cases isFound
            refine ⟨newAttempt, history, attemptSelf, ?_, isLive, newMethod, newPositive, ?_, ?_,
              ?_⟩
            · rw [findEq]; exact isHistory
            · intro runningId isRunning
              obtain ⟨isOldRunning, _, sameBackoff, _⟩ := keptIfRunning runningId isRunning
              obtain ⟨notBackoff, hasSnapshot⟩ := oldRunning runningId isOldRunning
              exact ⟨by rw [sameBackoff]; exact notBackoff, by rw [newSnapshot]; exact hasSnapshot⟩
            · rw [newSnapshot]
              exact snapshotMatches
            · intro backoff
              exact newBackoff backoff
          · exact attemptMatchesRecord_congr (attemptOther changedRecord isOld isNotRecord) findEq
              (fun timer isStep isContained => retryKept timer (by rw [isStep]; exact isNotRecord)
                isContained)
              (runInvariant.attemptsMatch changedRecord isOld)
        liveIsCurrent := by
          intro key history isFound isLive
          exact reconciled_liveIsCurrent _ _ _ _ _ _ key history isFound isLive
        runningTaskIdsBelow := by
          intro changedRecord runningMethod taskId isChanged hasRunning
          rcases memChanged changedRecord isChanged with isPended | ⟨isOld, _⟩
          · rw [isPended] at hasRunning
            simp only [setInvocation, Option.some.injEq, Dex.Invocation.mk.injEq] at hasRunning
            obtain ⟨isOldRunning, _⟩ := keptIfRunning taskId hasRunning.2
            show taskId < pre.nextTaskId
            rw [preNextTaskId]
            exact runInvariant.runningTaskIdsBelow record method taskId isRecord
              (by rw [hasInvocation, isOldRunning])
          · show taskId < pre.nextTaskId
            rw [preNextTaskId]
            exact runInvariant.runningTaskIdsBelow changedRecord runningMethod taskId isOld
              hasRunning
        runningTaskMatches := by
          intro changedRecord runningMethod taskId task isChanged hasRunning isTask isId
          rcases memChanged changedRecord isChanged with isPended | ⟨isOld, isNotRecord⟩
          · subst isPended
            simp only [setInvocation, Option.some.injEq, Dex.Invocation.mk.injEq] at hasRunning
            obtain ⟨isMethodEq, isState⟩ := hasRunning
            obtain ⟨isOldRunning, sameNumber, _⟩ := keptIfRunning taskId isState
            obtain ⟨isKey, found, isFound, isNumber⟩ := runInvariant.runningTaskMatches record
              method taskId task isRecord (by rw [hasInvocation, isOldRunning]) (realSub task isTask)
              isId
            rw [isAttempt] at isFound
            cases isFound
            refine ⟨by rw [isKey, isMethodEq]; rfl, newAttempt, attemptSelf, ?_⟩
            rw [sameNumber]
            exact isNumber
          · rw [attemptOther changedRecord isOld isNotRecord]
            exact runInvariant.runningTaskMatches changedRecord runningMethod taskId task isOld
              hasRunning (realSub task isTask) isId
        runningIsTracked := by
          intro changedRecord runningMethod taskId isChanged hasRunning
          rcases memChanged changedRecord isChanged with isPended | ⟨isOld, isNotRecord⟩
          · subst isPended
            simp only [setInvocation, Option.some.injEq, Dex.Invocation.mk.injEq] at hasRunning
            obtain ⟨isMethodEq, isState⟩ := hasRunning
            obtain ⟨isOldRunning, sameNumber, _, sameTasks, sameInterrupts⟩ :=
              keptIfRunning taskId isState
            subst isMethodEq
            show _ ∨ pre.isUp = false ∨ _
            rw [show (setInvocation method newState record).id = record.id from rfl, attemptSelf,
              sameTasks, sameInterrupts, preUp]
            rcases runInvariant.runningIsTracked record method taskId isRecord
                (by rw [hasInvocation, isOldRunning]) with isTask | isDown |
                ⟨found, isFound, isInterrupt⟩
            · exact Or.inl isTask
            · exact Or.inr (Or.inl isDown)
            · rw [isAttempt] at isFound
              cases isFound
              right; right
              refine ⟨newAttempt, rfl, ?_⟩
              rw [sameNumber]
              exact isInterrupt
          · rw [attemptOther changedRecord isOld isNotRecord]
            rcases runInvariant.runningIsTracked changedRecord runningMethod taskId isOld
                hasRunning with ⟨task, isTask, isId⟩ | isDown | ⟨found, isFound, isInterrupt⟩
            · left
              refine ⟨task, ?_, isId⟩
              by_cases isKept : task ∈ pre.tasks
              · exact isKept
              · exfalso
                have isRecordRunning := tasksKept task isTask isKept
                have recordMatches := runInvariant.runningTaskMatches record method task.base.id
                  task isRecord (by rw [hasInvocation, isRecordRunning])
                  (List.mem_append_left _ isTask) rfl
                have otherMatches := runInvariant.runningTaskMatches changedRecord runningMethod
                  taskId task isOld hasRunning (List.mem_append_left _ isTask) isId
                have sameKey := recordMatches.1.symm.trans otherMatches.1
                simp only [Prod.mk.injEq] at sameKey
                exact isNotRecord sameKey.1.symm
            · right; left
              show pre.isUp = false
              rw [preUp]
              exact isDown
            · right; right
              refine ⟨found, isFound, ?_⟩
              by_cases isKept :
                (⟨changedRecord.id, runningMethod, found.number, taskId⟩ : Interrupt σ) ∈
                  pre.interrupts
              · exact isKept
              · exact absurd (interruptsKept _ isInterrupt isKept) isNotRecord }

end DexExpressiveness.DexF.WPF4
