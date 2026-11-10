import DexExpressiveness.Simulation.Completion.Broadcast

/-!
# The `broadcast` row of the mapping table (spec §8.3)

"`broadcast(cv)`: As `signal`, for every waiter in order": empty
`waiters/cv` and publish to the Channel of every waiter.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

variable {program : Program} {lConfig : L.Config} {dexConfig : Dex.Config signature}
  {run : Dex.Run signature} {thread : L.Thread} {record : Dex.StepRecord signature}
  {task : Dex.Task signature} {result : StatementResult}

/-- `broadcast(cv)`: empty `waiters/cv` and publish to every waiter's Channel. -/
theorem wp3b_intent_broadcast
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (ownFlag : lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [])
    (cv : CvId) (intentCase : result.intent = .broadcast cv)
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
        { continuedConfig lConfig thread result with
          cvQueues := (continuedConfig lConfig thread result).cvQueues.set cv [] } := by
      simp [L.Config.applyIntent, intentCase, continuedConfig, L.Config.updateThread,
        waitersCase]
    have emptied := wp3b_effect_writes mid
      { continuedConfig lConfig thread result with
        cvQueues := (continuedConfig lConfig thread result).cvQueues.set cv [] }
      [] rfl rfl rfl rfl rfl rfl rfl
      (by
        intro other isOther isDistinct loaded isRunning otherCv isOperand
        have isOtherCv : otherCv ≠ cv := by
          intro isSame
          rw [isSame] at isOperand
          exact start.noOtherOfContinued cv noOther other isOther isDistinct loaded isRunning
            isOperand
        exact AssocList.get?_set_other _ _ _ _ isOtherCv)
      mid.data.shared
      (by
        intro other
        have isOld := mid.data.cvQueues other
        by_cases isCv : other = cv
        · subst isCv
          have wasEmpty : (lConfig.cvQueues.get? other).getD [] = [] := waitersCase
          show readWaiters { attributes := (releasedRun run record.id).attributes } other =
            ((lConfig.cvQueues.set other []).get? other).getD []
          rw [AssocList.get?_set_self]
          exact isOld.trans wasEmpty
        · show readWaiters { attributes := (releasedRun run record.id).attributes } other =
            ((lConfig.cvQueues.set cv []).get? other).getD []
          rw [AssocList.get?_set_other _ _ _ _ isCv]
          exact isOld)
    unfold IntentGoal
    rw [isNext, wp3b_completedRun_eq, isResponse]
    simp only [moveDecision]
    rw [wp3b_effect_none mid.threads]
    exact start.finishGoto _ _ _ emptied isOwn rfl rfl _ rfl rfl rfl rfl
  | cons waiter laterWaiters =>
    have isResponse : encodeIntent task.snapshot result =
        { effects :=
            { writes := [(Key.cvWaiters cv, some (DexVal.waiterQueue []))]
              publications := broadcastPublications cv (waiter :: laterWaiters) }
          decision := { moves := [moveToPoint result.core result.code], close := none } } := by
      simp [encodeIntent, intentCase, sameWaiters, waitersCase, broadcastPublications]
    have isNext : (lConfig.applyIntent thread.core.tid result).2 =
        (waiter :: laterWaiters).foldl (fun current other => current.wakeCvWaiter cv other)
          { continuedConfig lConfig thread result with
            cvQueues := (continuedConfig lConfig thread result).cvQueues.set cv [] } := by
      simp [L.Config.applyIntent, intentCase, continuedConfig, L.Config.updateThread,
        waitersCase]
    have written := start.writeWaiters _ _ _ mid cv [] (start.noOtherOfContinued cv noOther)
    obtain ⟨woken, kept⟩ := wp3b_effect_broadcast written cv (waiter :: laterWaiters)
    obtain ⟨_, _, _, _, _, sameStatus, sameClock⟩ := wp3b_broadcast_fold cv
      (waiter :: laterWaiters)
      { continuedConfig lConfig thread result with
        cvQueues := (continuedConfig lConfig thread result).cvQueues.set cv [] }
      written.threads.threadIdsNodup
    unfold IntentGoal
    rw [isNext, wp3b_completedRun_eq, isResponse]
    simp only
    rw [wp3b_applyEffects_writes]
    exact start.finishGoto _ _ _ woken (kept.1 _ isOwn start.sameTid) sameStatus sameClock _ rfl
      rfl rfl rfl

end DexExpressiveness
