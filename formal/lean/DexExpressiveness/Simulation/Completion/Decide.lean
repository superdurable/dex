import DexExpressiveness.Simulation.Completion.Decision

/-!
# The five decisions of a translated Execute

`decide(e, D)` for the decisions that `encodeIntent` produces without closing
the run: GoTo the next program point, GoToMany for `fork`, GoTo the `Timed`
Step, and DeadEnd for a halted and for an ended thread. Each lemma turns the
relation with the committing thread left out into `RunningRelated`.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- A program-point Step carries a ready thread outside a timed operation. -/
theorem wp3b_carries_point (program : Program) (thread : L.Thread)
    (record : Dex.StepRecord signature) (isReady : thread.phase = .ready)
    (noTimer : thread.timer = none)
    (hasType : record.id.stepType = StepType.point thread.code)
    (hasInput : record.input = some (DexVal.thread thread.core))
    (hasOptions : record.options.executeLoadMaps = executeLoadMaps thread.core thread.code) :
    StepCarriesThread program thread record := by
  rw [wp3b_carries_iff program thread record (by simp [isReady]) (by simp [isReady])]
  simp only [noTimer]
  exact ⟨hasType, hasInput, hasOptions⟩

/-- The threads of a configuration are the other threads and the one thread
with a given ID. -/
theorem wp3b_mem_iff_dropThread (config : L.Config) (tid : Tid) (own : L.Thread)
    (idsNodup : (config.threads.map (fun thread => thread.core.tid)).Nodup)
    (isOwn : own ∈ config.threads) (ownTid : own.core.tid = tid) (other : L.Thread) :
    other ∈ config.threads ↔ other ∈ (dropThread config tid).threads ∨ other = own := by
  rw [wp3b_mem_dropThread]
  constructor
  · intro isMember
    by_cases hasId : other.core.tid = tid
    · exact Or.inr (eq_of_nodup_map (fun thread => thread.core.tid) config.threads idsNodup
        isMember isOwn (hasId.trans ownTid.symm))
    · exact Or.inl ⟨isMember, hasId⟩
  · rintro (⟨isMember, _⟩ | isSame)
    · exact isMember
    · rw [isSame]
      exact isOwn

/-- The lookup of another thread after the committing thread was dropped. -/
theorem wp3b_findThread?_dropThread (config : L.Config) (tid target : Tid)
    (idsNodup : (config.threads.map (fun thread => thread.core.tid)).Nodup)
    (isOther : target ≠ tid) :
    (dropThread config tid).findThread? target = config.findThread? target := by
  symm
  apply wp3b_findThread?_congr config (dropThread config tid) target
    (wp3b_nodup_dropThread config tid idsNodup)
  intro candidate hasId
  rw [wp3b_mem_dropThread]
  exact ⟨fun isMember => ⟨isMember, by rw [hasId]; exact isOther⟩, fun isMember => isMember.1⟩

/-- The committing thread can fork no thread whose cancel flag is set with no
request: such a flag belongs to a thread that has ended. -/
theorem wp3b_own_isNoParent {program : Program} {lEff : L.Config}
    {dexEff : Dex.Config signature} {runEff : Dex.Run signature} {tid : Tid}
    {stepId : Dex.StepExecId signature}
    (mid : MidThreads program lEff dexEff runEff tid stepId) (data : MidData lEff runEff tid)
    (own : L.Thread) (isOwn : own ∈ lEff.threads) (ownTid : own.core.tid = tid) :
    ∀ target, lEff.cancelFlags.contains target = true →
      (dropThread lEff tid).findThread? target = none →
      runEff.pendingMessages (Chan.cancelRequest target) = [] →
      ∀ index, own.core.freshCounter ≤ index → ¬ (own.core.tid ++ [index]) <+: target := by
  intro target isFlagged isGone isEmpty index isAbove
  by_cases isOwnTarget : target = tid
  · rw [isOwnTarget] at isFlagged isEmpty
    rcases data.ownFlag isFlagged with hasRequest | ownGone
    · exact absurd isEmpty hasRequest
    · exact absurd ownTid ((wp3b_findThread?_none lEff tid).mp ownGone own isOwn)
  · rw [wp3b_findThread?_dropThread lEff tid target mid.threadIdsNodup isOwnTarget] at isGone
    rcases data.cancelFlagsAreRequests target isOwnTarget isFlagged with hasRequest |
      ⟨current, isFound, _⟩ | ⟨_, isNoChild⟩
    · exact absurd isEmpty hasRequest
    · rw [isGone] at isFound
      cases isFound
    · exact isNoChild own isOwn index isAbove

/-- GoTo the next program point: the committing thread continues. -/
theorem wp3b_decide_goto (program : Program) (lEff lNext : L.Config)
    (dexEff dexNext : Dex.Config signature) (runEff : Dex.Run signature) (tid : Tid)
    (stepId : Dex.StepExecId signature)
    (mid : MidThreads program lEff dexEff runEff tid stepId) (data : MidData lEff runEff tid)
    (tokens : TokensRelated lEff runEff)
    (own : L.Thread) (isOwn : own ∈ lEff.threads) (ownTid : own.core.tid = tid)
    (isReady : own.phase = .ready) (noTimer : own.timer = none)
    (sameData : SameData lEff lNext) (sameCounter : lNext.timerCounter = lEff.timerCounter)
    (sameThreads : lNext.threads = lEff.threads) (sameTasks : dexNext.tasks = dexEff.tasks)
    (hasRunNext : dexNext.run = some (runEff.applyDecision stepId
      { moves := [moveToPoint own.core own.code], close := none })) :
    RunningRelated program lNext dexNext (runEff.applyDecision stepId
      { moves := [moveToPoint own.core own.code], close := none }) := by
  have finished := wp3b_finish program lEff dexEff
    { dexNext with run := some (runEff.finishStep stepId .completed) } runEff tid stepId mid data
    tokens sameTasks rfl
  exact wp3b_spawn program (dropThread lEff tid) lNext _ dexNext
    (runEff.finishStep stepId .completed) finished rfl
    (fun other isOther => mid.noBoot other ((wp3b_mem_dropThread lEff tid other).mp isOther).1)
    own (moveToPoint own.core own.code) (.step stepId)
    ⟨sameData.shared, sameData.cvQueues, sameData.inputQueues, sameData.mutexOwners,
      sameData.joinable, sameData.cancelFlags, sameData.cvWakes⟩
    (fun other => by
      rw [sameThreads]
      exact wp3b_mem_iff_dropThread lEff tid own mid.threadIdsNodup isOwn ownTid other)
    (by rw [sameThreads]; exact mid.threadIdsNodup)
    (fun other isOther => by
      rw [ownTid]
      exact ((wp3b_mem_dropThread lEff tid other).mp isOther).2)
    isReady
    (fun isFlagged => by
      rw [ownTid] at isFlagged ⊢
      rcases data.ownFlag isFlagged with hasRequest | isGone
      · exact hasRequest
      · exact absurd ownTid ((wp3b_findThread?_none lEff tid).mp isGone own isOwn))
    (wp3b_own_isNoParent mid data own isOwn ownTid)
    (wp3b_carries_point program own _ isReady noTimer rfl rfl rfl)
    (by
      show (!isBlockingHead own.code) = !(own.timer.isSome || isBlockingHead own.code)
      rw [noTimer]
      rfl)
    (by
      show (((runEff.counters.set (StepType.point own.code) _).get? StepType.timed).getD 0) =
        lNext.timerCounter
      rw [AssocList.get?_set_other _ _ _ _ (by simp), sameCounter]
      exact data.timerCounter)
    rfl hasRunNext

/-- GoToMany for `fork`: the committing thread continues and a new thread starts. -/
theorem wp3b_decide_fork (program : Program) (lEff lNext : L.Config)
    (dexEff dexNext : Dex.Config signature) (runEff : Dex.Run signature) (tid : Tid)
    (stepId : Dex.StepExecId signature)
    (mid : MidThreads program lEff dexEff runEff tid stepId) (data : MidData lEff runEff tid)
    (tokens : TokensRelated lEff runEff)
    (own : L.Thread) (isOwn : own ∈ lEff.threads) (ownTid : own.core.tid = tid)
    (isReady : own.phase = .ready) (noTimer : own.timer = none)
    (child : L.Thread) (childReady : child.phase = .ready) (childNoTimer : child.timer = none)
    (childFresh : ∀ other, other ∈ lEff.threads → other.core.tid ≠ child.core.tid)
    (childFlag : lEff.cancelFlags.contains child.core.tid = true →
      runEff.pendingMessages (Chan.cancelRequest child.core.tid) ≠ [])
    (childIsNoParent : ∀ target, lEff.cancelFlags.contains target = true →
      runEff.pendingMessages (Chan.cancelRequest target) = [] →
      ∀ index, child.core.freshCounter ≤ index → ¬ (child.core.tid ++ [index]) <+: target)
    (sameData : SameData lEff lNext) (sameCounter : lNext.timerCounter = lEff.timerCounter)
    (threadsNext : lNext.threads = lEff.threads ++ [child])
    (sameTasks : dexNext.tasks = dexEff.tasks)
    (hasRunNext : dexNext.run = some (runEff.applyDecision stepId
      { moves := [moveToPoint own.core own.code, moveToPoint child.core child.code]
        close := none })) :
    RunningRelated program lNext dexNext (runEff.applyDecision stepId
      { moves := [moveToPoint own.core own.code, moveToPoint child.core child.code]
        close := none }) := by
  have finished := wp3b_finish program lEff dexEff
    { dexNext with run := some (runEff.finishStep stepId .completed) } runEff tid stepId mid data
    tokens sameTasks rfl
  have noBootDropped : ∀ other, other ∈ (dropThread lEff tid).threads →
      other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning := fun other isOther =>
    mid.noBoot other ((wp3b_mem_dropThread lEff tid other).mp isOther).1
  let continuedConfig : L.Config :=
    { dropThread lEff tid with threads := (dropThread lEff tid).threads ++ [own] }
  let continuedRun : Dex.Run signature :=
    (runEff.finishStep stepId .completed).newStep (StepType.point own.code)
      (some (DexVal.thread own.core)) (moveToPoint own.core own.code).options (.step stepId)
  have droppedNodup := wp3b_nodup_dropThread lEff tid mid.threadIdsNodup
  have continuedNodup : (continuedConfig.threads.map (fun other => other.core.tid)).Nodup := by
    show (((dropThread lEff tid).threads ++ [own]).map (fun other => other.core.tid)).Nodup
    rw [List.map_append]
    refine List.nodup_append.mpr ⟨droppedNodup, by simp, ?_⟩
    intro first isFirst second isSecond
    obtain ⟨other, isOther, hasId⟩ := List.mem_map.mp isFirst
    have isOwnId : second = own.core.tid := by simpa using isSecond
    rw [isOwnId, ← hasId, ownTid]
    exact ((wp3b_mem_dropThread lEff tid other).mp isOther).2
  have continued := wp3b_spawn program (dropThread lEff tid) continuedConfig _
    { dexNext with run := some continuedRun }
    (runEff.finishStep stepId .completed) finished rfl noBootDropped
    own (moveToPoint own.core own.code) (.step stepId)
    ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩
    (fun other => by
      show other ∈ (dropThread lEff tid).threads ++ [own] ↔ _
      simp)
    continuedNodup
    (fun other isOther => by
      rw [ownTid]
      exact ((wp3b_mem_dropThread lEff tid other).mp isOther).2)
    isReady
    (fun isFlagged => by
      rw [ownTid] at isFlagged ⊢
      rcases data.ownFlag isFlagged with hasRequest | isGone
      · exact hasRequest
      · exact absurd ownTid ((wp3b_findThread?_none lEff tid).mp isGone own isOwn))
    (wp3b_own_isNoParent mid data own isOwn ownTid)
    (wp3b_carries_point program own _ isReady noTimer rfl rfl rfl)
    (by
      show (!isBlockingHead own.code) = !(own.timer.isSome || isBlockingHead own.code)
      rw [noTimer]
      rfl)
    (by
      show (((runEff.counters.set (StepType.point own.code) _).get? StepType.timed).getD 0) =
        lEff.timerCounter
      rw [AssocList.get?_set_other _ _ _ _ (by simp)]
      exact data.timerCounter)
    rfl rfl
  have continuedThreads : ∀ other, other ∈ continuedConfig.threads ↔ other ∈ lEff.threads := by
    intro other
    show other ∈ (dropThread lEff tid).threads ++ [own] ↔ _
    rw [wp3b_mem_iff_dropThread lEff tid own mid.threadIdsNodup isOwn ownTid other]
    simp
  exact wp3b_spawn program continuedConfig lNext _ dexNext continuedRun continued rfl
    (fun other isOther => mid.noBoot other ((continuedThreads other).mp isOther))
    child (moveToPoint child.core child.code) (.step stepId)
    ⟨sameData.shared, sameData.cvQueues, sameData.inputQueues, sameData.mutexOwners,
      sameData.joinable, sameData.cancelFlags, sameData.cvWakes⟩
    (fun other => by
      rw [threadsNext, continuedThreads other]
      simp)
    (by
      rw [threadsNext, List.map_append]
      refine List.nodup_append.mpr ⟨mid.threadIdsNodup, by simp, ?_⟩
      intro first isFirst second isSecond
      obtain ⟨other, isOther, hasId⟩ := List.mem_map.mp isFirst
      have isChildId : second = child.core.tid := by simpa using isSecond
      rw [isChildId, ← hasId]
      exact childFresh other isOther)
    (fun other isOther => childFresh other ((continuedThreads other).mp isOther))
    childReady
    childFlag
    (fun target isFlagged _ isEmpty => childIsNoParent target isFlagged isEmpty)
    (wp3b_carries_point program child _ childReady childNoTimer rfl rfl rfl)
    (by
      show (!isBlockingHead child.code) = !(child.timer.isSome || isBlockingHead child.code)
      rw [childNoTimer]
      rfl)
    (by
      show (((continuedRun.counters.set (StepType.point child.code) _).get?
        StepType.timed).getD 0) = lNext.timerCounter
      rw [AssocList.get?_set_other _ _ _ _ (by simp)]
      show (((runEff.counters.set (StepType.point own.code) _).get? StepType.timed).getD 0) =
        lNext.timerCounter
      rw [AssocList.get?_set_other _ _ _ _ (by simp), sameCounter]
      exact data.timerCounter)
    rfl hasRunNext

/-- GoTo the `Timed` Step: Enter of a timed operation. -/
theorem wp3b_decide_timed (program : Program) (lEff lNext : L.Config)
    (dexEff dexNext : Dex.Config signature) (runEff : Dex.Run signature) (tid : Tid)
    (stepId : Dex.StepExecId signature)
    (mid : MidThreads program lEff dexEff runEff tid stepId) (data : MidData lEff runEff tid)
    (tokens : TokensRelated lEff runEff)
    (own : L.Thread) (isOwn : own ∈ lEff.threads) (ownTid : own.core.tid = tid)
    (isReady : own.phase = .ready) (seconds : Nat)
    (hasTimer : own.timer = some (lEff.timerCounter + 1, seconds))
    (sameData : SameData lEff lNext) (counterNext : lNext.timerCounter = lEff.timerCounter + 1)
    (sameThreads : lNext.threads = lEff.threads) (sameTasks : dexNext.tasks = dexEff.tasks)
    (hasRunNext : dexNext.run = some (runEff.applyDecision stepId
      { moves :=
          [{ stepType := StepType.timed
             input := some (DexVal.timedThread own.core own.code seconds)
             options := { skipWaitFor := false } }]
        close := none })) :
    RunningRelated program lNext dexNext (runEff.applyDecision stepId
      { moves :=
          [{ stepType := StepType.timed
             input := some (DexVal.timedThread own.core own.code seconds)
             options := { skipWaitFor := false } }]
        close := none }) := by
  have finished := wp3b_finish program lEff dexEff
    { dexNext with run := some (runEff.finishStep stepId .completed) } runEff tid stepId mid data
    tokens sameTasks rfl
  have counted : (runEff.counters.get? StepType.timed).getD 0 = lEff.timerCounter :=
    data.timerCounter
  exact wp3b_spawn program (dropThread lEff tid) lNext _ dexNext
    (runEff.finishStep stepId .completed) finished rfl
    (fun other isOther => mid.noBoot other ((wp3b_mem_dropThread lEff tid other).mp isOther).1)
    own
    { stepType := StepType.timed
      input := some (DexVal.timedThread own.core own.code seconds)
      options := { skipWaitFor := false } }
    (.step stepId)
    ⟨sameData.shared, sameData.cvQueues, sameData.inputQueues, sameData.mutexOwners,
      sameData.joinable, sameData.cancelFlags, sameData.cvWakes⟩
    (fun other => by
      rw [sameThreads]
      exact wp3b_mem_iff_dropThread lEff tid own mid.threadIdsNodup isOwn ownTid other)
    (by rw [sameThreads]; exact mid.threadIdsNodup)
    (fun other isOther => by
      rw [ownTid]
      exact ((wp3b_mem_dropThread lEff tid other).mp isOther).2)
    isReady
    (fun isFlagged => by
      rw [ownTid] at isFlagged ⊢
      rcases data.ownFlag isFlagged with hasRequest | isGone
      · exact hasRequest
      · exact absurd ownTid ((wp3b_findThread?_none lEff tid).mp isGone own isOwn))
    (wp3b_own_isNoParent mid data own isOwn ownTid)
    (by
      rw [wp3b_carries_iff program own _ (by simp [isReady]) (by simp [isReady])]
      simp only [hasTimer]
      refine ⟨?_, rfl⟩
      show ({ stepType := StepType.timed
              number := (runEff.counters.get? StepType.timed).getD 0 + 1 } :
        Dex.StepExecId signature) = _
      rw [counted])
    (by
      show false = !(own.timer.isSome || isBlockingHead own.code)
      rw [hasTimer]
      rfl)
    (by
      show (((runEff.counters.set StepType.timed _).get? StepType.timed).getD 0) =
        lNext.timerCounter
      rw [AssocList.get?_set_self, counterNext, ← counted]
      rfl)
    rfl hasRunNext

/-- DeadEnd of a halted thread: the thread stays, with no Step. -/
theorem wp3b_decide_halt (program : Program) (lEff lNext : L.Config)
    (dexEff dexNext : Dex.Config signature) (runEff : Dex.Run signature) (tid : Tid)
    (stepId : Dex.StepExecId signature)
    (mid : MidThreads program lEff dexEff runEff tid stepId) (data : MidData lEff runEff tid)
    (tokens : TokensRelated lEff runEff)
    (own : L.Thread) (isOwn : own ∈ lEff.threads) (ownTid : own.core.tid = tid)
    (isHalted : own.phase = .halted)
    (sameData : SameData lEff lNext) (sameCounter : lNext.timerCounter = lEff.timerCounter)
    (sameThreads : lNext.threads = lEff.threads) (sameTasks : dexNext.tasks = dexEff.tasks)
    (hasRunNext : dexNext.run = some (runEff.applyDecision stepId
      { moves := [], close := some .deadEnd })) :
    RunningRelated program lNext dexNext (runEff.applyDecision stepId
      { moves := [], close := some .deadEnd }) := by
  have finished := wp3b_finish program lEff dexEff
    { dexNext with run := some (runEff.finishStep stepId .completed) } runEff tid stepId mid data
    tokens sameTasks rfl
  exact wp3b_addHalted program (dropThread lEff tid) lNext _ dexNext
    (runEff.finishStep stepId .completed) finished
    (fun other isOther => mid.noBoot other ((wp3b_mem_dropThread lEff tid other).mp isOther).1)
    (some own)
    ⟨sameData.shared, sameData.cvQueues, sameData.inputQueues, sameData.mutexOwners,
      sameData.joinable, sameData.cancelFlags, sameData.cvWakes⟩
    sameCounter
    (fun other => by
      rw [sameThreads, wp3b_mem_iff_dropThread lEff tid own mid.threadIdsNodup isOwn ownTid other]
      simp [eq_comm])
    (by rw [sameThreads]; exact mid.threadIdsNodup)
    (fun thread isExtra => by
      have isSame : own = thread := Option.some.inj isExtra
      subst isSame
      refine ⟨isHalted, fun isFlagged => ?_, wp3b_own_isNoParent mid data own isOwn ownTid⟩
      rw [ownTid] at isFlagged ⊢
      rcases data.ownFlag isFlagged with hasRequest | isGone
      · exact hasRequest
      · exact absurd ownTid ((wp3b_findThread?_none lEff tid).mp isGone own isOwn))
    rfl hasRunNext

/-- DeadEnd of an ended thread: the thread is gone. -/
theorem wp3b_decide_end (program : Program) (lEff lNext : L.Config)
    (dexEff dexNext : Dex.Config signature) (runEff : Dex.Run signature) (tid : Tid)
    (stepId : Dex.StepExecId signature)
    (mid : MidThreads program lEff dexEff runEff tid stepId) (data : MidData lEff runEff tid)
    (tokens : TokensRelated lEff runEff)
    (ownGone : ∀ other, other ∈ lEff.threads → other.core.tid ≠ tid)
    (sameData : SameData lEff lNext) (sameCounter : lNext.timerCounter = lEff.timerCounter)
    (sameThreads : lNext.threads = lEff.threads) (sameTasks : dexNext.tasks = dexEff.tasks)
    (hasRunNext : dexNext.run = some (runEff.applyDecision stepId
      { moves := [], close := some .deadEnd })) :
    RunningRelated program lNext dexNext (runEff.applyDecision stepId
      { moves := [], close := some .deadEnd }) := by
  have finished := wp3b_finish program lEff dexEff
    { dexNext with run := some (runEff.finishStep stepId .completed) } runEff tid stepId mid data
    tokens sameTasks rfl
  exact wp3b_addHalted program (dropThread lEff tid) lNext _ dexNext
    (runEff.finishStep stepId .completed) finished
    (fun other isOther => mid.noBoot other ((wp3b_mem_dropThread lEff tid other).mp isOther).1)
    none
    ⟨sameData.shared, sameData.cvQueues, sameData.inputQueues, sameData.mutexOwners,
      sameData.joinable, sameData.cancelFlags, sameData.cvWakes⟩
    sameCounter
    (fun other => by
      rw [sameThreads, wp3b_mem_dropThread]
      constructor
      · exact fun isMember => Or.inl ⟨isMember, ownGone other isMember⟩
      · rintro (⟨isMember, _⟩ | isExtra)
        · exact isMember
        · cases isExtra)
    (by rw [sameThreads]; exact mid.threadIdsNodup)
    (fun thread isExtra => by cases isExtra)
    rfl hasRunNext

end DexExpressiveness
