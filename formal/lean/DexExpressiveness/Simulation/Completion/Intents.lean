import DexExpressiveness.Simulation.Completion.Assemble

/-!
# The rows of the mapping table (spec §8.3), one lemma per `Intent`

Each lemma shows that the commit of `encodeIntent snapshot result` does to
the run what `Config.applyIntent` does to the L configuration: the two
results are related (`IntentGoal`). This file has the rows that wake no
thread by a batch: every `Intent` except `broadcast`, `exit`, and `fail`.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- The hypotheses every row shares. -/
structure IntentStart (program : Program) (lConfig : L.Config) (dexConfig : Dex.Config signature)
    (run : Dex.Run signature) (thread : L.Thread) (record : Dex.StepRecord signature)
    (task : Dex.Task signature) (result : StatementResult) : Prop where
  /-- The configurations are related and satisfy their invariants. -/
  state : IsSimulationState program lConfig dexConfig
  /-- A Step in phase `WT` has no invocation. -/
  waiting : WaitingStepsHaveNoInvocation dexConfig
  /-- The thread, its Step, and its task. -/
  start : Completing program lConfig dexConfig run thread record task
  /-- The statement keeps the thread ID. -/
  sameTid : result.core.tid = thread.core.tid
  /-- The statement does not lower the counter of fresh names. -/
  counterKept : thread.core.freshCounter ≤ result.core.freshCounter
  /-- No thread is in the start action. -/
  noBoot : ∀ other, other ∈ lConfig.threads →
    other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning

/-- The two results of a row are related. -/
def IntentGoal (program : Program) (lConfig : L.Config) (dexConfig : Dex.Config signature)
    (run : Dex.Run signature) (thread : L.Thread) (record : Dex.StepRecord signature)
    (task : Dex.Task signature) (result : StatementResult) : Prop :=
  Related program (lConfig.applyIntent thread.core.tid result).2.stamp
    (withRun (releasedConfig dexConfig task.id)
      (completedRun run record.id (encodeIntent task.snapshot result)))

/-- The run after the commit, in its three parts. -/
theorem wp3b_completedRun_eq (run : Dex.Run signature) (step : Dex.StepExecId signature)
    (response : Dex.ExecuteResponse signature) :
    completedRun run step response =
      ((releasedRun run step).applyEffects response.effects).applyDecision step
        response.decision := rfl

/-- The thread that continues at the next program point. -/
def continuedThread (thread : L.Thread) (result : StatementResult) : L.Thread :=
  { thread with core := result.core, code := result.code, timer := none, phase := .ready }

/-- The L configuration with the committing thread at its next program point. -/
def continuedConfig (lConfig : L.Config) (thread : L.Thread) (result : StatementResult) :
    L.Config :=
  lConfig.updateThread thread.core.tid (fun current =>
    { current with core := result.core, code := result.code, timer := none, phase := .ready })

variable {program : Program} {lConfig : L.Config} {dexConfig : Dex.Config signature}
  {run : Dex.Run signature} {thread : L.Thread} {record : Dex.StepRecord signature}
  {task : Dex.Task signature} {result : StatementResult}

/-- The start of a row whose thread continues at the next program point. -/
theorem IntentStart.continued
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (ownFlag : lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ []) :
    MidState program (continuedConfig lConfig thread result)
      (withRun (releasedConfig dexConfig task.id) (releasedRun run record.id))
      (releasedRun run record.id) thread.core.tid record.id ∧
    continuedThread thread result ∈ (continuedConfig lConfig thread result).threads := by
  obtain ⟨midThreads, midData, isOwn⟩ := wp3b_begin_update program lConfig dexConfig start.state
    start.waiting run thread record task start.start
    (fun current => { current with
      core := result.core, code := result.code, timer := none, phase := .ready })
    start.sameTid (by simp) (by simp) start.counterKept ownFlag
  exact ⟨⟨midThreads, midData, wp3b_start_tokens program lConfig _ dexConfig run start.state
    start.start.hasRun start.start.isRunning record.id start.noBoot rfl⟩, isOwn⟩

/-- The other threads of the continued configuration are threads of the
configuration before. -/
theorem IntentStart.otherOfContinued
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (other : L.Thread) (isOther : other ∈ (continuedConfig lConfig thread result).threads)
    (isDistinct : other.core.tid ≠ thread.core.tid) : other ∈ lConfig.threads :=
  ((wp3b_updateThread_spec lConfig thread start.start.isThread
    start.state.lInvariant.threadIdsNodup _ start.sameTid).1 other isDistinct).mp isOther

/-- The committing thread of the continued configuration is the continued thread. -/
theorem IntentStart.ownOfContinued
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (own : L.Thread) (isOwn : own ∈ (continuedConfig lConfig thread result).threads)
    (ownTid : own.core.tid = thread.core.tid) : own = continuedThread thread result :=
  (wp3b_updateThread_spec lConfig thread start.start.isThread
    start.state.lInvariant.threadIdsNodup _ start.sameTid).2.1 own isOwn ownTid

/-- GoTo the next program point after `effects`: `Related` for the stamped
L configuration. -/
theorem IntentStart.finishGoto
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (lEff : L.Config) (dexEff : Dex.Config signature) (runEff : Dex.Run signature)
    (mid : MidState program lEff dexEff runEff thread.core.tid record.id)
    (isOwn : continuedThread thread result ∈ lEff.threads)
    (sameStatus : lEff.status = lConfig.status) (sameClock : lEff.clock = lConfig.clock)
    (dexNext : Dex.Config signature) (sameTasks : dexNext.tasks = dexEff.tasks)
    (dexClock : dexNext.clock = dexConfig.clock)
    (isCommitted : dexNext.lastCommitAt = some dexConfig.clock)
    (hasRunNext : dexNext.run = some (runEff.applyDecision record.id
      { moves := [moveToPoint result.core result.code], close := none })) :
    Related program lEff.stamp dexNext :=
  wp3b_related_of_running program lConfig lEff.stamp dexConfig dexNext _ start.state.isRelated
    sameClock (by show some lEff.clock = _; rw [sameClock])
    (sameStatus.trans (wp3b_status_running program lConfig dexConfig start.state.isRelated run
      start.start.hasRun start.start.isRunning))
    dexClock isCommitted hasRunNext mid.data.isRunning
    (wp3b_decide_goto program lEff lEff.stamp dexEff dexNext runEff thread.core.tid record.id
      mid.threads mid.data mid.tokens (continuedThread thread result) isOwn start.sameTid rfl rfl
      ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩ rfl rfl sameTasks hasRunNext)

/-- Local computation only: GoTo. -/
theorem wp3b_intent_silent
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (ownFlag : lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [])
    (intentCase : result.intent = .silent) :
    IntentGoal program lConfig dexConfig run thread record task result := by
  obtain ⟨mid, isOwn⟩ := start.continued ownFlag
  have isResponse : encodeIntent task.snapshot result =
      moveDecision [moveToPoint result.core result.code] := by
    simp [encodeIntent, intentCase]
  have isNext : (lConfig.applyIntent thread.core.tid result).2 =
      continuedConfig lConfig thread result := by
    simp [L.Config.applyIntent, intentCase, continuedConfig]
  unfold IntentGoal
  rw [isNext, wp3b_completedRun_eq, isResponse]
  simp only [moveDecision]
  rw [wp3b_effect_none mid.threads]
  exact start.finishGoto _ _ _ mid isOwn rfl rfl _ rfl rfl rfl rfl

/-- The Attribute write of a shared variable. -/
theorem wp3b_shared_write (attributes : AssocList Key DexVal) (shared : AssocList Global Val)
    (target : Global) (value : Val)
    (isShared : ∀ name,
      readShared { attributes := attributes } name = (shared.get? name).getD .null)
    (name : Global) :
    readShared { attributes :=
      match encodeSharedWrite value with
      | some written => attributes.set (Key.shared target) written
      | none => attributes.erase (Key.shared target) } name =
      ((shared.set target value).get? name).getD .null := by
  by_cases isTarget : name = target
  · subst isTarget
    rw [AssocList.get?_set_self]
    cases value <;>
      simp [encodeSharedWrite, readShared, AssocList.get?_set_self, AssocList.get?_erase_self]
  · have isOtherKey : Key.shared name ≠ Key.shared target := by simpa using isTarget
    rw [AssocList.get?_set_other _ _ _ _ isTarget, ← isShared name]
    cases value <;>
      simp [encodeSharedWrite, readShared, AssocList.get?_set_other _ _ _ _ isOtherKey,
        AssocList.get?_erase_other _ _ _ isOtherKey]

/-- `g := e`: one Attribute write. -/
theorem wp3b_intent_store
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (ownFlag : lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [])
    (target : Global) (value : Val) (intentCase : result.intent = .store target value) :
    IntentGoal program lConfig dexConfig run thread record task result := by
  obtain ⟨mid, isOwn⟩ := start.continued ownFlag
  have isResponse : encodeIntent task.snapshot result =
      { effects := { writes := [(Key.shared target, encodeSharedWrite value)] }
        decision := { moves := [moveToPoint result.core result.code], close := none } } := by
    simp [encodeIntent, intentCase]
  have isNext : (lConfig.applyIntent thread.core.tid result).2 =
      { continuedConfig lConfig thread result with
        shared := (continuedConfig lConfig thread result).shared.set target value } := by
    simp [L.Config.applyIntent, intentCase, continuedConfig]
  have written := wp3b_effect_writes mid
    { continuedConfig lConfig thread result with
      shared := (continuedConfig lConfig thread result).shared.set target value }
    [(Key.shared target, encodeSharedWrite value)] rfl rfl rfl rfl rfl rfl rfl
    (fun _ _ _ _ _ _ _ => rfl)
    (by
      intro name
      have isWritten := wp3b_shared_write (releasedRun run record.id).attributes
        (continuedConfig lConfig thread result).shared target value mid.data.shared name
      cases writeCase : encodeSharedWrite value with
      | none =>
        rw [writeCase] at isWritten
        exact isWritten
      | some encoded =>
        rw [writeCase] at isWritten
        exact isWritten)
    (by
      intro cv
      have isOld := mid.data.cvQueues cv
      cases writeCase : encodeSharedWrite value with
      | none =>
        simpa [Dex.Run.applyWrites, writeCase, readWaiters,
          AssocList.get?_erase_other _ _ _ (show Key.cvWaiters cv ≠ Key.shared target by simp)]
          using isOld
      | some encoded =>
        simpa [Dex.Run.applyWrites, writeCase, readWaiters,
          AssocList.get?_set_other _ _ _ _ (show Key.cvWaiters cv ≠ Key.shared target by simp)]
          using isOld)
  unfold IntentGoal
  rw [isNext, wp3b_completedRun_eq, isResponse]
  simp only
  rw [wp3b_applyEffects_writes, wp3b_effect_none written.threads]
  exact start.finishGoto _ _ _ written isOwn rfl rfl _ rfl rfl rfl rfl

/-- `fork`: GoToMany, the parent's continuation and the child's first Step. -/
theorem wp3b_intent_fork
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (ownFlag : lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [])
    (child : ThreadCore) (childCode : Code) (intentCase : result.intent = .fork child childCode)
    (childFresh : ∀ other, other ∈ lConfig.threads → other.core.tid ≠ child.tid)
    (childFlag : lConfig.cancelFlags.contains child.tid = true →
      run.pendingMessages (Chan.cancelRequest child.tid) ≠ [])
    (childIsNoParent : ∀ target, lConfig.cancelFlags.contains target = true →
      run.pendingMessages (Chan.cancelRequest target) = [] →
      ∀ index, child.freshCounter ≤ index → ¬ (child.tid ++ [index]) <+: target) :
    IntentGoal program lConfig dexConfig run thread record task result := by
  obtain ⟨mid, isOwn⟩ := start.continued ownFlag
  have isResponse : encodeIntent task.snapshot result =
      moveDecision [moveToPoint result.core result.code, moveToPoint child childCode] := by
    simp [encodeIntent, intentCase]
  have isNext : (lConfig.applyIntent thread.core.tid result).2 =
      { continuedConfig lConfig thread result with
        threads := (continuedConfig lConfig thread result).threads ++
          [{ core := child, code := childCode, timer := none, phase := .ready }] } := by
    simp [L.Config.applyIntent, intentCase, continuedConfig]
  have freshInContinued : ∀ other, other ∈ (continuedConfig lConfig thread result).threads →
      other.core.tid ≠ child.tid := by
    intro other isOther
    by_cases isDistinct : other.core.tid = thread.core.tid
    · rw [isDistinct]
      exact childFresh thread start.start.isThread
    · exact childFresh other (start.otherOfContinued other isOther isDistinct)
  unfold IntentGoal
  rw [isNext, wp3b_completedRun_eq, isResponse]
  simp only [moveDecision]
  rw [wp3b_effect_none mid.threads]
  refine wp3b_related_of_running program lConfig _ dexConfig _ _ start.state.isRelated rfl rfl
    (wp3b_status_running program lConfig dexConfig start.state.isRelated run start.start.hasRun
      start.start.isRunning) rfl rfl rfl mid.data.isRunning ?_
  exact wp3b_decide_fork program (continuedConfig lConfig thread result) _ _ _ _ thread.core.tid
    record.id mid.threads mid.data mid.tokens (continuedThread thread result) isOwn start.sameTid
    rfl rfl { core := child, code := childCode, timer := none, phase := .ready } rfl rfl
    freshInContinued childFlag childIsNoParent ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩ rfl rfl rfl rfl

/-- Enter of a timed operation: GoTo the `Timed` Step with the next timer number. -/
theorem wp3b_intent_enterTimed
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (ownFlag : lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [])
    (seconds : Nat) (intentCase : result.intent = .enterTimed seconds) :
    IntentGoal program lConfig dexConfig run thread record task result := by
  obtain ⟨midThreads, midData, isOwn⟩ := wp3b_begin_update program lConfig dexConfig start.state
    start.waiting run thread record task start.start
    (fun current => { current with
      core := result.core, code := result.code
      timer := some (lConfig.timerCounter + 1, seconds), phase := .ready })
    start.sameTid (by simp) (by simp) start.counterKept ownFlag
  have tokens := wp3b_start_tokens program lConfig
    (lConfig.updateThread thread.core.tid (fun current => { current with
      core := result.core, code := result.code
      timer := some (lConfig.timerCounter + 1, seconds), phase := .ready }))
    dexConfig run start.state start.start.hasRun start.start.isRunning record.id start.noBoot rfl
  have isResponse : encodeIntent task.snapshot result =
      moveDecision
        [{ stepType := StepType.timed
           input := some (DexVal.timedThread result.core result.code seconds)
           options := { skipWaitFor := false } }] := by
    simp [encodeIntent, intentCase]
  have isNext : (lConfig.applyIntent thread.core.tid result).2 =
      { lConfig.updateThread thread.core.tid (fun current => { current with
          core := result.core, code := result.code
          timer := some (lConfig.timerCounter + 1, seconds), phase := .ready }) with
        timerCounter := lConfig.timerCounter + 1 } := by
    simp [L.Config.applyIntent, intentCase]
  unfold IntentGoal
  rw [isNext, wp3b_completedRun_eq, isResponse]
  simp only [moveDecision]
  rw [wp3b_effect_none midThreads]
  refine wp3b_related_of_running program lConfig _ dexConfig _ _ start.state.isRelated rfl rfl
    (wp3b_status_running program lConfig dexConfig start.state.isRelated run start.start.hasRun
      start.start.isRunning) rfl rfl rfl midData.isRunning ?_
  exact wp3b_decide_timed program _ _ _ _ _ thread.core.tid record.id midThreads midData tokens
    _ isOwn start.sameTid rfl seconds rfl ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩ rfl rfl rfl rfl

/-- A halted thread: DeadEnd, nothing published. -/
theorem wp3b_intent_halt
    (start : IntentStart program lConfig dexConfig run thread record task result)
    (ownFlag : lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [])
    (intentCase : result.intent = .halt) :
    IntentGoal program lConfig dexConfig run thread record task result := by
  obtain ⟨midThreads, midData, isOwn⟩ := wp3b_begin_update program lConfig dexConfig start.state
    start.waiting run thread record task start.start
    (fun current => { current with phase := .halted }) rfl (by simp) (by simp) (Nat.le_refl _)
    ownFlag
  have tokens := wp3b_start_tokens program lConfig
    (lConfig.updateThread thread.core.tid (fun current => { current with phase := .halted }))
    dexConfig run start.state start.start.hasRun start.start.isRunning record.id start.noBoot rfl
  have isResponse : encodeIntent task.snapshot result = closeDecision .deadEnd := by
    simp [encodeIntent, intentCase]
  have isNext : (lConfig.applyIntent thread.core.tid result).2 =
      lConfig.updateThread thread.core.tid (fun current => { current with phase := .halted }) := by
    simp [L.Config.applyIntent, intentCase, L.Config.setPhase]
  unfold IntentGoal
  rw [isNext, wp3b_completedRun_eq, isResponse]
  simp only [closeDecision]
  rw [wp3b_effect_none midThreads]
  refine wp3b_related_of_running program lConfig _ dexConfig _ _ start.state.isRelated rfl rfl
    (wp3b_status_running program lConfig dexConfig start.state.isRelated run start.start.hasRun
      start.start.isRunning) rfl rfl rfl midData.isRunning ?_
  exact wp3b_decide_halt program _ _ _ _ _ thread.core.tid record.id midThreads midData tokens
    _ isOwn rfl rfl ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩ rfl rfl rfl rfl

end DexExpressiveness
