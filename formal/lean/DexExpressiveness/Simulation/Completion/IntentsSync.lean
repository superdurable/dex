import DexExpressiveness.Simulation.Completion.Intents

/-!
# The synchronization rows of the mapping table (spec §8.3)

`newMutex`, `unlock`, `wait`, `signal`, `cancel`, and the end of a thread:
each publishes one message, which the Step of the first blocked thread
consumes when there is one.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

variable {program : Program} {lConfig : L.Config} {dexConfig : Dex.Config signature}
  {run : Dex.Run signature} {thread : L.Thread} {record : Dex.StepRecord signature}
  {task : Dex.Task signature} {result : StatementResult}

/-- No other thread runs a `wait`, `signal`, or `broadcast` on the condition
variable: it would hold the mutex that the committing thread holds. -/
def NoOtherOperand (lConfig : L.Config) (tid : Tid) (cv : CvId) : Prop :=
  ∀ other, other ∈ lConfig.threads → other.core.tid ≠ tid → ∀ loaded,
    other.phase = .running loaded → cvOperand other.core other.code ≠ some cv

/-- `newMutex`: publish the first token. -/
theorem wp3b_intent_newMutex
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (ownFlag : lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [])
    (mutex : MutexId) (intentCase : result.intent = .newMutex mutex)
    (isFresh : lConfig.mutexOwners.get? mutex = none)
    (noLocker : ∀ other, other ∈ lConfig.threads → ∀ since,
      other.phase ≠ .blocked (.lock mutex) since) :
    IntentGoal program lConfig dexConfig run thread record task result := by
  obtain ⟨mid, isOwn⟩ := start.continued ownFlag
  have isResponse : encodeIntent task.snapshot result =
      { effects := { publications := [(Chan.mutexToken mutex, DexVal.token)] }
        decision := { moves := [moveToPoint result.core result.code], close := none } } := by
    simp [encodeIntent, intentCase]
  have isNext : (lConfig.applyIntent thread.core.tid result).2 =
      { continuedConfig lConfig thread result with
        mutexOwners := (continuedConfig lConfig thread result).mutexOwners.set mutex none } := by
    simp [L.Config.applyIntent, intentCase, continuedConfig]
  have published := wp3b_effect_token mid mutex
    (by
      show lConfig.mutexOwners.get? mutex ≠ some none
      rw [isFresh]
      simp)
    (by
      intro other isOther since isBlocked
      by_cases isDistinct : other.core.tid = thread.core.tid
      · rw [start.ownOfContinued other isOther isDistinct] at isBlocked
        cases isBlocked
      · exact noLocker other (start.otherOfContinued other isOther isDistinct) since isBlocked)
  unfold IntentGoal
  rw [isNext, wp3b_completedRun_eq, isResponse]
  exact start.finishGoto _ _ _ published isOwn rfl rfl _ rfl rfl rfl rfl

/-- `unlock(m)`: publish one token to `m`. -/
theorem wp3b_intent_unlock
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (ownFlag : lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [])
    (mutex : MutexId) (intentCase : result.intent = .unlock mutex)
    (isHeld : lConfig.mutexOwners.get? mutex = some (some thread.core.tid)) :
    IntentGoal program lConfig dexConfig run thread record task result := by
  obtain ⟨mid, isOwn⟩ := start.continued ownFlag
  have isResponse : encodeIntent task.snapshot result =
      { effects := { publications := [(Chan.mutexToken mutex, DexVal.token)] }
        decision := { moves := [moveToPoint result.core result.code], close := none } } := by
    simp [encodeIntent, intentCase]
  have isNext : (lConfig.applyIntent thread.core.tid result).2 =
      (continuedConfig lConfig thread result).releaseMutex mutex := by
    simp [L.Config.applyIntent, intentCase, continuedConfig]
  obtain ⟨released, kept⟩ := wp3b_effect_release mid mutex
    (by
      show lConfig.mutexOwners.get? mutex ≠ some none
      rw [isHeld]
      simp)
  obtain ⟨sameStatus, sameClock⟩ :=
    wp3b_scalars_releaseMutex (continuedConfig lConfig thread result) mutex
  unfold IntentGoal
  rw [isNext, wp3b_completedRun_eq, isResponse]
  exact start.finishGoto _ _ _ released (kept.1 _ isOwn start.sameTid) sameStatus sameClock _ rfl
    rfl rfl rfl

/-- The waiter list after a write of `waiters/cv`. -/
theorem wp3b_waiters_write (attributes : AssocList Key DexVal) (cvQueues : AssocList CvId (List Tid))
    (cv : CvId) (waiters : List Tid)
    (isQueues : ∀ other,
      readWaiters { attributes := attributes } other = (cvQueues.get? other).getD [])
    (other : CvId) :
    readWaiters { attributes := attributes.set (Key.cvWaiters cv) (DexVal.waiterQueue waiters) }
      other = ((cvQueues.set cv waiters).get? other).getD [] := by
  by_cases isCv : other = cv
  · subst isCv
    simp [readWaiters, AssocList.get?_set_self]
  · have isOtherKey : Key.cvWaiters other ≠ Key.cvWaiters cv := by simpa using isCv
    rw [AssocList.get?_set_other _ _ _ _ isCv, ← isQueues other]
    simp [readWaiters, AssocList.get?_set_other _ _ _ _ isOtherKey]

/-- A write of `waiters/cv` leaves the shared variables. -/
theorem wp3b_shared_of_waiters_write (attributes : AssocList Key DexVal) (cv : CvId)
    (waiters : List Tid) (name : Global) :
    readShared { attributes := attributes.set (Key.cvWaiters cv) (DexVal.waiterQueue waiters) }
      name = readShared { attributes := attributes } name := by
  simp [readShared,
    AssocList.get?_set_other _ _ _ _ (show Key.shared name ≠ Key.cvWaiters cv by simp)]

/-- The write of `waiters/cv` and the change of the queue of `cv`, for the
thread that holds the mutex of `cv`. -/
theorem IntentStart.writeWaiters
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (lEff : L.Config) (dexEff : Dex.Config signature) (runEff : Dex.Run signature)
    (mid : MidState program lEff dexEff runEff thread.core.tid record.id)
    (cv : CvId) (waiters : List Tid)
    (noOther : ∀ other, other ∈ lEff.threads → other.core.tid ≠ thread.core.tid → ∀ loaded,
      other.phase = .running loaded → cvOperand other.core other.code ≠ some cv) :
    MidState program { lEff with cvQueues := lEff.cvQueues.set cv waiters }
      (withRun dexEff
        (runEff.applyWrites [(Key.cvWaiters cv, some (DexVal.waiterQueue waiters))]))
      (runEff.applyWrites [(Key.cvWaiters cv, some (DexVal.waiterQueue waiters))])
      thread.core.tid record.id :=
  wp3b_effect_writes mid { lEff with cvQueues := lEff.cvQueues.set cv waiters }
    [(Key.cvWaiters cv, some (DexVal.waiterQueue waiters))] rfl rfl rfl rfl rfl rfl rfl
    (by
      intro other isOther isDistinct loaded isRunning otherCv isOperand
      have isOtherCv : otherCv ≠ cv := by
        intro isSame
        rw [isSame] at isOperand
        exact noOther other isOther isDistinct loaded isRunning isOperand
      exact AssocList.get?_set_other _ _ _ _ isOtherCv)
    (by
      intro name
      exact (wp3b_shared_of_waiters_write runEff.attributes cv waiters name).trans
        (mid.data.shared name))
    (fun other => wp3b_waiters_write runEff.attributes lEff.cvQueues cv waiters mid.data.cvQueues
      other)

/-- No other thread of the continued configuration runs on the condition variable. -/
theorem IntentStart.noOtherOfContinued
    (start : IntentStart program lConfig dexConfig run thread record task result) (cv : CvId)
    (noOther : NoOtherOperand lConfig thread.core.tid cv) :
    ∀ other, other ∈ (continuedConfig lConfig thread result).threads →
      other.core.tid ≠ thread.core.tid → ∀ loaded, other.phase = .running loaded →
      cvOperand other.core other.code ≠ some cv :=
  fun other isOther isDistinct =>
    noOther other (start.otherOfContinued other isOther isDistinct) isDistinct

/-- `wait(cv, m)`: append the waiter ID to `waiters/cv` and publish `m`'s token. -/
theorem wp3b_intent_cvWait
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (ownFlag : lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [])
    (cv : CvId) (intentCase : result.intent = .cvWait cv)
    (isHeld : lConfig.mutexOwners.get? cv.mutex = some (some thread.core.tid))
    (sameWaiters : readWaiters task.snapshot cv = (lConfig.cvQueues.get? cv).getD [])
    (noOther : NoOtherOperand lConfig thread.core.tid cv) :
    IntentGoal program lConfig dexConfig run thread record task result := by
  obtain ⟨mid, isOwn⟩ := start.continued ownFlag
  have isResponse : encodeIntent task.snapshot result =
      { effects :=
          { writes := [(Key.cvWaiters cv, some (DexVal.waiterQueue
              ((lConfig.cvQueues.get? cv).getD [] ++ [thread.core.tid])))]
            publications := [(Chan.mutexToken cv.mutex, DexVal.token)] }
        decision := { moves := [moveToPoint result.core result.code], close := none } } := by
    simp [encodeIntent, intentCase, sameWaiters, start.sameTid]
  have isNext : (lConfig.applyIntent thread.core.tid result).2 =
      ({ continuedConfig lConfig thread result with
        cvQueues := (continuedConfig lConfig thread result).cvQueues.set cv
          ((lConfig.cvQueues.get? cv).getD [] ++ [thread.core.tid]) }).releaseMutex cv.mutex := by
    simp [L.Config.applyIntent, intentCase, continuedConfig, L.Config.updateThread]
  have written := start.writeWaiters _ _ _ mid cv
    ((lConfig.cvQueues.get? cv).getD [] ++ [thread.core.tid]) (start.noOtherOfContinued cv noOther)
  obtain ⟨released, kept⟩ := wp3b_effect_release written cv.mutex
    (by
      show lConfig.mutexOwners.get? cv.mutex ≠ some none
      rw [isHeld]
      simp)
  obtain ⟨sameStatus, sameClock⟩ := wp3b_scalars_releaseMutex
    { continuedConfig lConfig thread result with
      cvQueues := (continuedConfig lConfig thread result).cvQueues.set cv
        ((lConfig.cvQueues.get? cv).getD [] ++ [thread.core.tid]) } cv.mutex
  unfold IntentGoal
  rw [isNext, wp3b_completedRun_eq, isResponse]
  simp only
  rw [wp3b_applyEffects_writes]
  exact start.finishGoto _ _ _ released (kept.1 _ isOwn start.sameTid) sameStatus sameClock _ rfl
    rfl rfl rfl

/-- `signal(cv)`: remove the head of `waiters/cv` and publish to its Channel. -/
theorem wp3b_intent_signal
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (ownFlag : lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [])
    (cv : CvId) (intentCase : result.intent = .signal cv)
    (sameWaiters : readWaiters task.snapshot cv = (lConfig.cvQueues.get? cv).getD [])
    (noOther : NoOtherOperand lConfig thread.core.tid cv) :
    IntentGoal program lConfig dexConfig run thread record task result := by
  obtain ⟨mid, isOwn⟩ := start.continued ownFlag
  cases waitersCase : (lConfig.cvQueues.get? cv).getD [] with
  | nil =>
    have isResponse : encodeIntent task.snapshot result =
        moveDecision [moveToPoint result.core result.code] := by
      simp [encodeIntent, intentCase, sameWaiters, waitersCase]
    have isNext : (lConfig.applyIntent thread.core.tid result).2 =
        continuedConfig lConfig thread result := by
      simp [L.Config.applyIntent, intentCase, continuedConfig, L.Config.updateThread,
        waitersCase]
    unfold IntentGoal
    rw [isNext, wp3b_completedRun_eq, isResponse]
    simp only [moveDecision]
    rw [wp3b_effect_none mid.threads]
    exact start.finishGoto _ _ _ mid isOwn rfl rfl _ rfl rfl rfl rfl
  | cons waiter laterWaiters =>
    have isResponse : encodeIntent task.snapshot result =
        { effects :=
            { writes := [(Key.cvWaiters cv, some (DexVal.waiterQueue laterWaiters))]
              publications := [(Chan.cvWake cv waiter, DexVal.token)] }
          decision := { moves := [moveToPoint result.core result.code], close := none } } := by
      simp [encodeIntent, intentCase, sameWaiters, waitersCase]
    have isNext : (lConfig.applyIntent thread.core.tid result).2 =
        ({ continuedConfig lConfig thread result with
          cvQueues := (continuedConfig lConfig thread result).cvQueues.set cv
            laterWaiters }).wakeCvWaiter cv waiter := by
      simp [L.Config.applyIntent, intentCase, continuedConfig, L.Config.updateThread,
        waitersCase]
    have written := start.writeWaiters _ _ _ mid cv laterWaiters
      (start.noOtherOfContinued cv noOther)
    obtain ⟨woken, kept⟩ := wp3b_effect_wakeCvWaiter written cv waiter
    obtain ⟨sameStatus, sameClock⟩ := wp3b_scalars_wakeCvWaiter
      { continuedConfig lConfig thread result with
        cvQueues := (continuedConfig lConfig thread result).cvQueues.set cv laterWaiters } cv
      waiter
    unfold IntentGoal
    rw [isNext, wp3b_completedRun_eq, isResponse]
    simp only
    rw [wp3b_applyEffects_writes]
    exact start.finishGoto _ _ _ woken (kept.1 _ isOwn start.sameTid) sameStatus sameClock _ rfl
      rfl rfl rfl

/-- `cancel(t)`: publish to `cancel/t`. -/
theorem wp3b_intent_cancel
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (ownFlag : lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [])
    (target : Tid) (intentCase : result.intent = .cancel target) :
    IntentGoal program lConfig dexConfig run thread record task result := by
  obtain ⟨mid, isOwn⟩ := start.continued ownFlag
  have isResponse : encodeIntent task.snapshot result =
      { effects := { publications := [(Chan.cancelRequest target, DexVal.token)] }
        decision := { moves := [moveToPoint result.core result.code], close := none } } := by
    simp [encodeIntent, intentCase]
  have isNext : (lConfig.applyIntent thread.core.tid result).2 =
      (flagThread (continuedConfig lConfig thread result) target).cancelBlockedThread target := by
    simp [L.Config.applyIntent, intentCase, continuedConfig, flagThread]
  obtain ⟨canceled, kept⟩ := wp3b_effect_cancel mid target
  obtain ⟨sameStatus, sameClock⟩ := wp3b_scalars_cancelBlockedThread
    (flagThread (continuedConfig lConfig thread result) target) target
  unfold IntentGoal
  rw [isNext, wp3b_completedRun_eq, isResponse]
  exact start.finishGoto _ _ _ canceled (kept.1 _ isOwn start.sameTid) sameStatus sameClock _ rfl
    rfl rfl rfl

/-- The end of a thread: publish to `join/t` and decide DeadEnd. -/
theorem wp3b_intent_endThread
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (intentCase : result.intent = .endThread) :
    IntentGoal program lConfig dexConfig run thread record task result := by
  obtain ⟨midThreads, midData⟩ := wp3b_begin_drop program lConfig dexConfig start.state
    start.waiting run thread record task start.start
  have tokens := wp3b_start_tokens program lConfig (dropThread lConfig thread.core.tid) dexConfig
    run start.state start.start.hasRun start.start.isRunning record.id start.noBoot rfl
  have mid : MidState program (dropThread lConfig thread.core.tid)
      (withRun (releasedConfig dexConfig task.id) (releasedRun run record.id))
      (releasedRun run record.id) thread.core.tid record.id := ⟨midThreads, midData, tokens⟩
  have isResponse : encodeIntent task.snapshot result =
      { effects := { publications := [(Chan.joinDone thread.core.tid, DexVal.token)] }
        decision := { moves := [], close := some .deadEnd } } := by
    simp [encodeIntent, intentCase, start.sameTid]
  have isNotJoinable : (dropThread lConfig thread.core.tid).joinable.contains thread.core.tid =
      false := by
    have isNotMember := (start.state.lInvariant.joinableFresh thread start.start.isThread).1
    show lConfig.joinable.contains thread.core.tid = false
    simpa using isNotMember
  have ownGone : ∀ other, other ∈ (dropThread lConfig thread.core.tid).threads →
      other.core.tid ≠ thread.core.tid := fun other isOther =>
    ((wp3b_mem_dropThread lConfig thread.core.tid other).mp isOther).2
  have lStatus := wp3b_status_running program lConfig dexConfig start.state.isRelated run
    start.start.hasRun start.start.isRunning
  unfold IntentGoal
  rw [wp3b_completedRun_eq, isResponse]
  simp only [L.Config.applyIntent, intentCase]
  split
  · rename_i joiner earliestCase
    obtain ⟨joined, kept⟩ := wp3b_effect_join_some mid thread.core.tid _
      (by intro on; cases on <;> simp) joiner earliestCase
    refine wp3b_related_of_running program lConfig _ dexConfig _ _ start.state.isRelated rfl rfl
      lStatus rfl rfl rfl joined.data.isRunning ?_
    exact wp3b_decide_end program _ _ _ _ _ thread.core.tid record.id joined.threads joined.data
      joined.tokens (fun other isOther sameId => ownGone other (kept.2 other isOther sameId) sameId)
      ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩ rfl rfl rfl rfl
  · rename_i earliestCase
    have joined := wp3b_effect_join_none mid thread.core.tid isNotJoinable _
      (by intro on; cases on <;> simp) earliestCase
    refine wp3b_related_of_running program lConfig _ dexConfig _ _ start.state.isRelated rfl rfl
      lStatus rfl rfl rfl joined.data.isRunning ?_
    exact wp3b_decide_end program _ _ _ _ _ thread.core.tid record.id joined.threads joined.data
      joined.tokens ownGone ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩ rfl rfl rfl rfl

end DexExpressiveness
