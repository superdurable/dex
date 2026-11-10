import DexExpressiveness.Failure.Proofs.WPF4StepStartBase

/-!
# WP-F4: [STEP-START] keeps the invariants

[STEP-START], extended: a Pending invocation becomes Running as a fresh task
of `Θ`, its attempt state takes the snapshot, and the first start that
computes a snapshot sets `par(e, m)` and takes the next virtual task ID.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- `AttemptMatchesRecord` reads only the histories of its own Step. -/
theorem attemptMatchesRecord_congr_step {run after : Run σ} {ghost changedGhost : Ghost σ}
    {record : Dex.StepRecord σ} (sameAttempt : after.findAttempt? record.id = run.findAttempt? record.id)
    (sameFind : ∀ method, changedGhost.findInvocation? (record.id, method) =
      ghost.findInvocation? (record.id, method))
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

/-- `Invariant` after a [STEP-START], in terms of the pieces of the step. -/
theorem invariant_stepStartGeneral {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    (canCommit : config.canCommit = true) (isUp : config.isUp = true)
    (record : Dex.StepRecord σ) (isRecord : record ∈ run.base.steps) (method : Dex.Method)
    (hasPending : record.invocation = some { method := method, state := .pending })
    (attempt : Attempt σ) (isAttempt : run.findAttempt? record.id = some attempt)
    (notBackoff : attempt.isBackoff = false) (snapshot : Dex.Snapshot σ)
    (snapshotMatches : ∀ found, attempt.snapshot = some found → snapshot = found)
    (newAttempt : Attempt σ) (newMethod : newAttempt.method = attempt.method)
    (newNumber : newAttempt.number = attempt.number)
    (newBackoff : newAttempt.isBackoff = attempt.isBackoff)
    (newSnapshot : newAttempt.snapshot = some snapshot)
    (task : Task σ) (taskId : task.base.id = config.nextTaskId) (taskStep : task.base.step = record.id)
    (taskMethod : task.base.method = method)
    (taskContext : task.base.context =
      { stepExecutionId := record.id, fromStepExecutionId := record.origin })
    (taskInput : task.base.input = record.input) (taskSnapshot : task.base.snapshot = snapshot)
    (taskConditionResults : task.base.conditionResults = record.conditionResults)
    (taskPhase : task.base.phase = .started) (taskEmitted : task.emittedCount = 0)
    (taskAttempt : task.attemptContext.attempt = attempt.number)
    (isNormal isSnapshotReused : Bool) (isReuse : isSnapshotReused = attempt.snapshot.isSome) :
    let changed : Run σ :=
      { (bumpedRun run).setAttempt record.id newAttempt with
        base := (bumpedRun run).base.updateStep record.id
          (setInvocation method (.running config.nextTaskId)) }
    let reconciled := (countedGhost config.ghost isNormal (run.base.seq + 1) config.clock).reconcile
      none changed.reconcileAttempts.base
    Invariant program
      { config with
        run := some changed.reconcileAttempts
        lastCommitAt := some config.clock
        tasks := config.tasks ++ [task]
        nextTaskId := config.nextTaskId + 1
        ghost := if isSnapshotReused then reconciled else
          { reconciled.updateInvocation (record.id, method) (fun history =>
              { history with
                arguments := some
                  { context := { stepExecutionId := record.id, fromStepExecutionId := record.origin }
                    input := record.input, snapshot := snapshot,
                    conditionResults := record.conditionResults,
                    virtualTaskId := reconciled.nextVirtualTaskId } }) with
            nextVirtualTaskId := reconciled.nextVirtualTaskId + 1 } } := by
  intro changed reconciled
  have runInvariant := invariant.runInvariant run isRun
  have idsNodup := runInvariant.stepIdsNodup
  let started := setInvocation method (.running config.nextTaskId) record
  have changedSteps : changed.base.steps = run.base.steps.map (fun current =>
      if current.id = record.id then setInvocation method (.running config.nextTaskId) current
      else current) := rfl
  have changedNodup : (changed.base.steps.map (fun record => record.id)).Nodup := by
    rw [changedSteps, ids_singleRecord]; exact idsNodup
  have isOnly : ∀ other, other ∈ run.base.steps → other.id = record.id → other = record :=
    fun other isOther sameId => eq_of_id_eq idsNodup isOther isRecord sameId
  have memChanged : ∀ changedRecord, changedRecord ∈ changed.base.steps →
      (changedRecord = started) ∨
      (changedRecord ∈ run.base.steps ∧ changedRecord.id ≠ record.id) := by
    intro changedRecord isChanged
    rw [changedSteps] at isChanged
    obtain ⟨original, isOriginal, ⟨isStep, isPended⟩ | ⟨isOther, isSame⟩⟩ :=
      mem_singleRecord isChanged
    · left
      rw [isPended, isOnly original isOriginal isStep]
    · right
      rw [isSame]
      exact ⟨isOriginal, isOther⟩
  have startedMember : started ∈ changed.base.steps := by
    rw [changedSteps]
    exact List.mem_map.mpr ⟨record, isRecord, by simp [started]⟩
  have otherMember : ∀ other, other ∈ run.base.steps → other.id ≠ record.id →
      other ∈ changed.base.steps := by
    intro other isOther isNotRecord
    rw [changedSteps]
    exact List.mem_map.mpr ⟨other, isOther, by simp [isNotRecord]⟩
  have currentSame : ∀ key, key ∈ currentKeys changed.base ↔ key ∈ currentKeys run.base := by
    apply currentKeys_singleRecord run.base changed.base record.id method _ changedSteps
    intro other isOther sameId
    rw [isOnly other isOther sameId]
    exact ⟨.pending, hasPending⟩
  have findEq : ∀ key, reconciled.findInvocation? key = config.ghost.findInvocation? key := by
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
    rw [show changed.attempts.get? other.id = run.attempts.get? other.id from
      AssocList.get?_set_other _ _ _ _ isNotRecord]
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
  have isMatchingRecord := runInvariant.attemptsMatch record isRecord
  unfold AttemptMatchesRecord at isMatchingRecord
  rw [hasPending] at isMatchingRecord
  obtain ⟨found, history, isFound, isHistory, isLive, sameMethod, isPositive, _, snapshotPairs, _⟩ :=
    isMatchingRecord
  rw [isAttempt] at isFound
  cases isFound
  have attemptSelf : changed.reconcileAttempts.findAttempt? record.id = some newAttempt := by
    have isStartedAttempt := findAttempt?_reconcileAttempts changed started startedMember
      changedNodup
    rw [show started.id = record.id from rfl] at isStartedAttempt
    rw [isStartedAttempt]
    unfold reconciledAttempt
    simp [started, setInvocation, changed, Run.setAttempt, AssocList.get?_set_self, newMethod,
      sameMethod]
  have statusRunning : run.base.status = .running := by
    cases isStatus : run.base.status with
    | running => rfl
    | closed status =>
      have := (runInvariant.closedRunIsQuiet ⟨status, isStatus⟩ record isRecord).2
      rw [hasPending] at this
      cases this
  -- The arguments of the started invocation.
  let startedArguments : Arguments σ :=
    { context := { stepExecutionId := record.id, fromStepExecutionId := record.origin }
      input := record.input, snapshot := snapshot, conditionResults := record.conditionResults,
      virtualTaskId := reconciled.nextVirtualTaskId }
  let startedHistory : InvocationHistory σ :=
    if isSnapshotReused then history else { history with arguments := some startedArguments }
  have historyConsistent := historyIsConsistent_of_find invariant isHistory
  have noArgumentsIfFresh : isSnapshotReused = false → history.arguments = none ∧
      history.progress = .notRun ∧ history.emittedCount = 0 := by
    intro isFresh
    rw [isFresh] at isReuse
    have noSnapshot : attempt.snapshot = none := by
      cases hSnapshot : attempt.snapshot with
      | none => rfl
      | some found => rw [hSnapshot] at isReuse; cases isReuse
    rw [noSnapshot] at snapshotPairs
    have noArguments : history.arguments = none := by
      revert snapshotPairs
      cases history.arguments with
      | none => intro _; rfl
      | some arguments => intro isFalse; exact isFalse.elim
    unfold HistoryIsConsistent at historyConsistent
    rw [noArguments] at historyConsistent
    obtain ⟨isNotRun, isZero, _⟩ := historyConsistent
    refine ⟨noArguments, ?_, isZero⟩
    cases hProgress : history.progress with
    | notRun => rfl
    | ran => rw [hProgress] at isNotRun; cases isNotRun
    | returned => rw [hProgress] at isNotRun; cases isNotRun
  have startedFacts : ∃ arguments, startedHistory.arguments = some arguments ∧
      arguments.snapshot = snapshot ∧ arguments.input = record.input ∧
      arguments.conditionResults = record.conditionResults ∧
      arguments.context = { stepExecutionId := record.id, fromStepExecutionId := record.origin } := by
    cases hReuse : isSnapshotReused with
    | false => exact ⟨startedArguments, by simp [startedHistory, hReuse], rfl, rfl, rfl, rfl⟩
    | true =>
      rw [hReuse] at isReuse
      cases hSnapshot : attempt.snapshot with
      | none => rw [hSnapshot] at isReuse; cases isReuse
      | some foundSnapshot =>
        rw [hSnapshot] at snapshotPairs
        revert snapshotPairs
        cases hArguments : history.arguments with
        | none => intro isFalse; exact isFalse.elim
        | some arguments =>
          rintro ⟨isSnapshot, isInput, isConditionResults, isContext⟩
          refine ⟨arguments, by simp [startedHistory, hReuse, hArguments], ?_, isInput,
            isConditionResults, isContext⟩
          rw [snapshotMatches foundSnapshot hSnapshot, isSnapshot]
  have startedFields : startedHistory.outcome = history.outcome ∧
      startedHistory.progress = history.progress ∧
      startedHistory.emittedCount = history.emittedCount := by
    cases isSnapshotReused <;> exact ⟨rfl, rfl, rfl⟩
  obtain ⟨values, increase, taken, keepsIndex, _⟩ := countedGhost_normalIndex config.ghost isNormal
    run.base.seq config.clock runInvariant.normalIndexValues runInvariant.normalIndexKeysIncrease
    runInvariant.normalIndexKeysTaken
  obtain ⟨keysNodup, virtualNodup, virtualBelow, consistent⟩ :=
    reconciled_ghost_clauses invariant isNormal (run.base.seq + 1) config.clock none
      changed.reconcileAttempts.base (fun _ _ isNone => by cases isNone)
  -- The final ghost.
  let ghost : Ghost σ := if isSnapshotReused then reconciled else
    { reconciled.updateInvocation (record.id, method) (fun history =>
        { history with arguments := some startedArguments }) with
      nextVirtualTaskId := reconciled.nextVirtualTaskId + 1 }
  have ghostFind : ∀ key, ghost.findInvocation? key =
      if key = (record.id, method) then some startedHistory
      else config.ghost.findInvocation? key := by
    intro key
    cases hReuse : isSnapshotReused with
    | true =>
      simp only [ghost, startedHistory, hReuse, ↓reduceIte]
      rw [findEq]
      split
      · rename_i isKey
        rw [isKey, isHistory]
      · rfl
    | false =>
      simp only [ghost, startedHistory, hReuse, Bool.false_eq_true, ↓reduceIte]
      show (reconciled.updateInvocation (record.id, method) _).findInvocation? key = _
      rw [findInvocation?_updateInvocation, findEq]
      split
      · rw [isHistory]; rfl
      · exact findEq key
  have ghostKeys : ghost.invocations.map (fun entry => entry.1) =
      reconciled.invocations.map (fun entry => entry.1) := by
    cases isSnapshotReused
    · exact keys_updateInvocation _ _ _
    · rfl
  have ghostMember : ∀ entry, entry ∈ ghost.invocations →
      entry ∈ reconciled.invocations ∨ entry = ((record.id, method), startedHistory) := by
    intro entry isMember
    cases hReuse : isSnapshotReused with
    | true =>
      left
      simp only [ghost, hReuse, ↓reduceIte] at isMember
      exact isMember
    | false =>
      simp only [ghost, hReuse, Bool.false_eq_true, ↓reduceIte] at isMember
      rcases mem_updateInvocation _ _ _ _ isMember with isOld | ⟨found, isFoundHistory, isEntry⟩
      · exact Or.inl isOld
      · right
        rw [findEq, isHistory] at isFoundHistory
        cases isFoundHistory
        rw [isEntry]
        simp [startedHistory, hReuse]
  have ghostOther : ghost.normalCount = reconciled.normalCount ∧
      ghost.normalIndex = reconciled.normalIndex ∧
      ghost.lastNormalCommitAt = reconciled.lastNormalCommitAt := by
    cases hReuse : isSnapshotReused with
    | true =>
      simp [ghost, hReuse]
    | false =>
      simp only [ghost, hReuse, Bool.false_eq_true, ↓reduceIte]
      unfold Ghost.updateInvocation
      split <;> exact ⟨rfl, rfl, rfl⟩
  have ghostNextVirtual : ghost.nextVirtualTaskId =
      if isSnapshotReused then reconciled.nextVirtualTaskId
      else reconciled.nextVirtualTaskId + 1 := by
    cases isSnapshotReused <;> rfl
  have startedConsistent : HistoryIsConsistent program.failureFreeFlow (record.id, method)
      startedHistory := by
    cases hReuse : isSnapshotReused with
    | true =>
      simp only [startedHistory, hReuse, ↓reduceIte]
      exact historyConsistent
    | false =>
      simp only [startedHistory, hReuse, Bool.false_eq_true, ↓reduceIte]
      obtain ⟨_, isNotRun, isZero⟩ := noArgumentsIfFresh hReuse
      refine historyIsConsistent_of_parts (arguments := startedArguments) rfl ?_ ?_ ?_ ?_
      · intro effects response isRan
        have : history.progress = .ran effects response := isRan
        rw [isNotRun] at this; cases this
      · intro response isReturned
        have : history.progress = .returned response := isReturned
        rw [isNotRun] at this; cases this
      · intro _; exact isZero
      · intro isCommitted
        have : history.outcome = .committed := isCommitted
        rw [isLive] at this; cases this
  have isPerm : ((config.tasks ++ [task]) ++ config.zombies).Perm (task :: config.realTasks) := by
    rw [List.append_assoc, List.singleton_append]
    exact List.perm_middle
  have taskFresh : ∀ other, other ∈ config.realTasks → other.base.id ≠ task.base.id := by
    intro other isOther sameId
    have isBelow := invariant.realTaskIdsBelow other isOther
    rw [sameId, taskId] at isBelow
    exact Nat.lt_irrefl _ isBelow
  have realMember : ∀ other, other ∈ (config.tasks ++ [task]) ++ config.zombies →
      other = task ∨ other ∈ config.realTasks := by
    intro other isOther
    exact List.mem_cons.mp (isPerm.mem_iff.mp isOther)
  have taskKey : task.key = (record.id, method) := by
    simp [Task.key, taskStep, taskMethod]
  refine
    { realTaskIdsNodup := ?_
      realTaskIdsBelow := ?_
      interruptsNotInTasks := ?_
      interruptsBelow := ?_
      downIsEmpty := ?_
      lastNormalCommitAtMost := ?_
      notStarted := fun isNone => by cases isNone
      ghostKeysNodup := ?_
      virtualTaskIdsNodup := ?_
      virtualTaskIdsBelow := ?_
      historiesConsistent := ?_
      realTasksFollowHistory := ?_
      liveTasksPassFence := ?_
      runInvariant := ?_ }
  · show (((config.tasks ++ [task]) ++ config.zombies).map (fun task => task.base.id)).Nodup
    refine (isPerm.map _).nodup_iff.mpr ?_
    simp only [List.map_cons, List.nodup_cons]
    refine ⟨?_, invariant.realTaskIdsNodup⟩
    intro isMember
    obtain ⟨other, isOther, sameId⟩ := List.mem_map.mp isMember
    exact taskFresh other isOther sameId
  · intro other isOther
    show other.base.id < config.nextTaskId + 1
    rcases realMember other isOther with isTask | isOld
    · rw [isTask, taskId]; exact Nat.lt_succ_self _
    · exact Nat.lt_succ_of_lt (invariant.realTaskIdsBelow other isOld)
  · intro interrupt other isInterrupt isOther
    rcases List.mem_append.mp isOther with isOld | isNew
    · exact invariant.interruptsNotInTasks interrupt other isInterrupt isOld
    · rw [List.mem_singleton.mp isNew, taskId]
      exact Nat.ne_of_gt (invariant.interruptsBelow interrupt isInterrupt)
  · intro interrupt isInterrupt
    exact Nat.lt_succ_of_lt (invariant.interruptsBelow interrupt isInterrupt)
  · intro isDown
    have isConfigDown : config.isUp = false := isDown
    rw [isUp] at isConfigDown
    cases isConfigDown
  · show IsStampAtMost ghost.lastNormalCommitAt (some config.clock)
    rw [ghostOther.2.2]
    exact countedGhost_lastNormal _ _ _ _ _ invariant.lastNormalCommitAtMost
      (lt_of_canCommit canCommit)
  · show (ghost.invocations.map (fun entry => entry.1)).Nodup
    rw [ghostKeys]
    exact keysNodup
  · show ((ghost.invocations.filterMap (fun entry => entry.2.arguments)).map
      (fun arguments => arguments.virtualTaskId)).Nodup
    cases hReuse : isSnapshotReused with
    | true =>
      simp only [ghost, hReuse, ↓reduceIte]
      exact virtualNodup
    | false =>
      simp only [ghost, hReuse, Bool.false_eq_true, ↓reduceIte]
      apply virtualIds_setArguments reconciled (record.id, method) history startedArguments
        (by rw [findEq]; exact isHistory) (noArgumentsIfFresh hReuse).1 virtualNodup
      intro other isOther sameId
      obtain ⟨entry, isEntry, hasOther⟩ := List.mem_filterMap.mp isOther
      have isBelow := virtualBelow entry other isEntry hasOther
      rw [sameId] at isBelow
      exact Nat.lt_irrefl _ isBelow
  · intro entry arguments isMember hasArguments
    show arguments.virtualTaskId < ghost.nextVirtualTaskId
    rw [ghostNextVirtual]
    rcases ghostMember entry isMember with isOld | isNew
    · have isBelow := virtualBelow entry arguments isOld hasArguments
      cases isSnapshotReused
      · exact Nat.lt_succ_of_lt isBelow
      · exact isBelow
    · rw [isNew] at hasArguments
      cases hReuse : isSnapshotReused with
      | true =>
        simp only [startedHistory, hReuse, ↓reduceIte] at hasArguments
        have := (noArgumentsIfFresh · |>.1)
        exact virtualBelow ((record.id, method), history) arguments
          (by
            have isMemberHistory := mem_of_get? _ _ _ (show reconciled.findInvocation? (record.id, method) = some history by rw [findEq]; exact isHistory)
            exact isMemberHistory) hasArguments
      | false =>
        simp only [startedHistory, hReuse, Bool.false_eq_true, ↓reduceIte, Option.some.injEq] at hasArguments
        rw [← hasArguments]
        exact Nat.lt_succ_self _
  · intro entry isMember
    rcases ghostMember entry isMember with isOld | isNew
    · exact consistent entry isOld
    · rw [isNew]; exact startedConsistent
  · intro other isOther
    show ∃ history, ghost.findInvocation? other.key = some history ∧ _
    rw [ghostFind]
    rcases realMember other isOther with isTask | isOld
    · subst isTask
      rw [if_pos taskKey]
      obtain ⟨arguments, hasArguments, isSnapshot, isInput, isConditionResults, isContext⟩ :=
        startedFacts
      refine ⟨startedHistory, rfl, arguments, hasArguments, taskContext.trans isContext.symm,
        taskInput.trans isInput.symm, taskSnapshot.trans isSnapshot.symm,
        taskConditionResults.trans isConditionResults.symm, ?_, ?_⟩
      · rw [taskEmitted]; exact Nat.zero_le _
      · rw [taskPhase]; exact taskEmitted
    · obtain ⟨oldHistory, isOldHistory, oldFollows⟩ := invariant.realTasksFollowHistory other isOld
      split
      · rename_i isKey
        rw [isKey, isHistory] at isOldHistory
        cases isOldHistory
        refine ⟨startedHistory, rfl, ?_⟩
        cases hReuse : isSnapshotReused with
        | true =>
          simp only [startedHistory, hReuse, ↓reduceIte]
          exact oldFollows
        | false =>
          exfalso
          obtain ⟨arguments, hasArguments, _⟩ := oldFollows
          rw [(noArgumentsIfFresh hReuse).1] at hasArguments
          cases hasArguments
      · exact ⟨oldHistory, isOldHistory, oldFollows⟩
  · intro other otherHistory isOther isOtherHistory isOtherLive
    show Config.isFencePassed { config with run := some changed.reconcileAttempts } other = true
    rw [isFencePassed_iff (run := changed.reconcileAttempts) rfl changedNodup other]
    rcases List.mem_append.mp isOther with isOld | isNew
    · rw [ghostFind] at isOtherHistory
      have oldLive : ∃ oldHistory, config.ghost.findInvocation? other.key = some oldHistory ∧
          oldHistory.outcome = .live := by
        split at isOtherHistory
        · rename_i isKey
          cases isOtherHistory
          rw [isKey]
          exact ⟨history, isHistory, isLive⟩
        · exact ⟨otherHistory, isOtherHistory, isOtherLive⟩
      obtain ⟨oldHistory, isOldHistory, wasLive⟩ := oldLive
      have isOldPassed := invariant.liveTasksPassFence other oldHistory isOld isOldHistory wasLive
      obtain ⟨current, found, isCurrent, isStep, isFound, hasCurrent, isNumber⟩ :=
        (isFencePassed_iff isRun idsNodup other).mp isOldPassed
      have isNotRecord : current.id ≠ record.id := by
        intro sameId
        rw [isOnly current isCurrent sameId, hasPending] at hasCurrent
        simp at hasCurrent
      exact ⟨current, found, otherMember current isCurrent isNotRecord, isStep,
        by rw [attemptOther current isCurrent isNotRecord]; exact isFound, hasCurrent, isNumber⟩
    · rw [List.mem_singleton.mp isNew]
      refine ⟨started, newAttempt, startedMember, taskStep.symm, attemptSelf, ?_, ?_⟩
      · show some _ = some _
        rw [taskMethod, taskId]
      · rw [newNumber, taskAttempt]
  · intro after isAfter
    cases isAfter
    exact
      { stepIdsNodup := changedNodup
        stepNumbersCounted := by
          intro changedRecord isChanged
          rcases memChanged changedRecord isChanged with isStarted | ⟨isOld, _⟩
          · rw [isStarted]
            exact runInvariant.stepNumbersCounted record isRecord
          · exact runInvariant.stepNumbersCounted changedRecord isOld
        invocationMatchesPhase := by
          intro changedRecord isChanged
          rcases memChanged changedRecord isChanged with isStarted | ⟨isOld, _⟩
          · rw [isStarted]
            have phaseMatches := runInvariant.invocationMatchesPhase record isRecord
            rw [hasPending] at phaseMatches
            exact phaseMatches
          · exact runInvariant.invocationMatchesPhase changedRecord isOld
        closedRunIsQuiet := by
          rintro ⟨status, isClosed⟩
          have isOldClosed : run.base.status = .closed status := isClosed
          rw [statusRunning] at isOldClosed
          cases isOldClosed
        historiesHaveSteps := by
          intro entry isMember
          have isKey : entry.1 ∈ reconciled.invocations.map (fun entry => entry.1) := by
            rw [← ghostKeys]
            exact List.mem_map.mpr ⟨entry, isMember, rfl⟩
          obtain ⟨original, isOriginal, sameKey⟩ := List.mem_map.mp isKey
          rw [← sameKey]
          apply reconciled_historiesHaveSteps _ _ _ _ _ _ _ _ original isOriginal
          · intro oldEntry isOldEntry
            obtain ⟨oldRecord, isOldRecord, isId, isExecute⟩ :=
              runInvariant.historiesHaveSteps oldEntry isOldEntry
            by_cases isRecordId : oldRecord.id = record.id
            · rw [isOnly oldRecord isOldRecord isRecordId] at isId isExecute
              exact ⟨started, startedMember, isId, isExecute⟩
            · exact ⟨oldRecord, otherMember oldRecord isOldRecord isRecordId, isId, isExecute⟩
          · intro changedRecord isChanged
            rcases memChanged changedRecord isChanged with isStarted | ⟨isOld, _⟩
            · rw [isStarted]
              have phaseMatches := runInvariant.invocationMatchesPhase record isRecord
              rw [hasPending] at phaseMatches
              exact phaseMatches
            · exact runInvariant.invocationMatchesPhase changedRecord isOld
        retryTimersHaveHistories := by
          intro entry isEntry
          obtain ⟨oldHistory, isOldHistory⟩ := runInvariant.retryTimersHaveHistories entry isEntry
          rw [ghostFind]
          split
          · exact ⟨startedHistory, rfl⟩
          · exact ⟨oldHistory, isOldHistory⟩
        retryTimersNotAhead := by
          intro entry changedRecord invocation found isEntry isChanged isId hasInvocation isMethod
            isFound
          rcases memChanged changedRecord isChanged with isStarted | ⟨isOld, isNotRecord⟩
          · subst isStarted
            have isSelf : changed.reconcileAttempts.findAttempt? record.id = some found := isFound
            rw [attemptSelf] at isSelf
            cases isSelf
            simp only [started, setInvocation, Option.some.injEq] at hasInvocation
            rw [← hasInvocation] at isMethod
            rw [newNumber]
            exact runInvariant.retryTimersNotAhead entry record _ attempt isEntry isRecord isId
              hasPending isMethod isAttempt
          · rw [attemptOther changedRecord isOld isNotRecord] at isFound
            exact runInvariant.retryTimersNotAhead entry changedRecord invocation found isEntry
              isOld isId hasInvocation isMethod isFound
        waitTimersNodup := runInvariant.waitTimersNodup
        retryTimersNodup := runInvariant.retryTimersNodup
        attemptKeysNodup := nodup_reconcileAttempts changed changedNodup
        attemptsHaveInvocations := mem_reconcileAttempts changed
        normalIndexValues := by rw [ghostOther.2.1, ghostOther.1]; exact values
        normalIndexKeysIncrease := by rw [ghostOther.2.1]; exact increase
        normalIndexKeysTaken := by
          rw [ghostOther.2.1]
          intro entry isMember
          exact taken entry isMember
        waitSeqsIndexed := by
          rw [ghostOther.2.1]
          intro changedRecord waitState isChanged hasWait
          apply keepsIndex
          rcases memChanged changedRecord isChanged with isStarted | ⟨isOld, _⟩
          · rw [isStarted] at hasWait
            exact runInvariant.waitSeqsIndexed record waitState isRecord hasWait
          · exact runInvariant.waitSeqsIndexed changedRecord waitState isOld hasWait
        attemptsMatch := by
          intro changedRecord isChanged
          rcases memChanged changedRecord isChanged with isStarted | ⟨isOld, isNotRecord⟩
          · subst isStarted
            obtain ⟨arguments, hasArguments, isSnapshot, isInput, isConditionResults, isContext⟩ :=
              startedFacts
            unfold AttemptMatchesRecord
            show ∃ attempt history, _ ∧ ghost.findInvocation? (record.id, method) = some history ∧ _
            refine ⟨newAttempt, startedHistory, attemptSelf, by rw [ghostFind, if_pos rfl],
              by rw [startedFields.1]; exact isLive, newMethod.trans sameMethod,
              by rw [newNumber]; exact isPositive, ?_, ?_, ?_⟩
            · intro _ _
              exact ⟨newBackoff.trans notBackoff, by rw [newSnapshot]; rfl⟩
            · rw [newSnapshot, hasArguments]
              exact ⟨isSnapshot.symm, isInput, isConditionResults, isContext⟩
            · intro isBackoff
              rw [newBackoff, notBackoff] at isBackoff
              cases isBackoff
          · refine attemptMatchesRecord_congr_step (attemptOther changedRecord isOld isNotRecord)
              ?_ (fun _ _ isContained => isContained) (runInvariant.attemptsMatch changedRecord isOld)
            intro otherMethod
            rw [ghostFind]
            split
            · rename_i isKey
              simp only [Prod.mk.injEq] at isKey
              exact absurd isKey.1 isNotRecord
            · rfl
        liveIsCurrent := by
          intro key foundHistory isFound isFoundLive
          rw [ghostFind] at isFound
          split at isFound
          · rename_i isKey
            exact ⟨started, .running config.nextTaskId, startedMember, by rw [isKey]; rfl,
              by rw [isKey]; rfl⟩
          · exact reconciled_liveIsCurrent _ _ _ _ _ _ key foundHistory
              (by rw [findEq]; exact isFound) isFoundLive
        runningTaskIdsBelow := by
          intro changedRecord runningMethod runningId isChanged hasRunning
          show runningId < config.nextTaskId + 1
          rcases memChanged changedRecord isChanged with isStarted | ⟨isOld, _⟩
          · rw [isStarted] at hasRunning
            simp only [started, setInvocation, Option.some.injEq, Dex.Invocation.mk.injEq,
              Dex.InvocationState.running.injEq] at hasRunning
            rw [← hasRunning.2]
            exact Nat.lt_succ_self _
          · exact Nat.lt_succ_of_lt (runInvariant.runningTaskIdsBelow changedRecord runningMethod
              runningId isOld hasRunning)
        runningTaskMatches := by
          intro changedRecord runningMethod runningId other isChanged hasRunning isOther isId
          rcases memChanged changedRecord isChanged with isStarted | ⟨isOldRecord, isNotRecord⟩
          · subst isStarted
            simp only [started, setInvocation, Option.some.injEq, Dex.Invocation.mk.injEq,
              Dex.InvocationState.running.injEq] at hasRunning
            obtain ⟨isMethodEq, isRunningId⟩ := hasRunning
            rcases realMember other isOther with isTask | isOld
            · subst isTask
              refine ⟨by rw [taskKey, isMethodEq]; rfl, newAttempt, attemptSelf, ?_⟩
              rw [taskAttempt, newNumber]
            · exfalso
              have isBelow := invariant.realTaskIdsBelow other isOld
              rw [isId, ← isRunningId] at isBelow
              exact Nat.lt_irrefl _ isBelow
          · rw [attemptOther changedRecord isOldRecord isNotRecord]
            rcases realMember other isOther with isTask | isOld
            · exfalso
              have isBelow := runInvariant.runningTaskIdsBelow changedRecord runningMethod runningId
                isOldRecord hasRunning
              rw [← isId, isTask, taskId] at isBelow
              exact Nat.lt_irrefl _ isBelow
            · exact runInvariant.runningTaskMatches changedRecord runningMethod runningId other
                isOldRecord hasRunning isOld isId
        runningIsTracked := by
          intro changedRecord runningMethod runningId isChanged hasRunning
          rcases memChanged changedRecord isChanged with isStarted | ⟨isOld, isNotRecord⟩
          · subst isStarted
            simp only [started, setInvocation, Option.some.injEq, Dex.Invocation.mk.injEq,
              Dex.InvocationState.running.injEq] at hasRunning
            left
            exact ⟨task, List.mem_append_right _ (List.mem_singleton.mpr rfl),
              by rw [taskId, hasRunning.2]⟩
          · rw [attemptOther changedRecord isOld isNotRecord]
            rcases runInvariant.runningIsTracked changedRecord runningMethod runningId isOld
                hasRunning with ⟨other, isOther, isId⟩ | isDown | isInterrupt
            · exact Or.inl ⟨other, List.mem_append_left _ isOther, isId⟩
            · exact Or.inr (Or.inl isDown)
            · exact Or.inr (Or.inr isInterrupt) }

end DexExpressiveness.DexF.WPF4
