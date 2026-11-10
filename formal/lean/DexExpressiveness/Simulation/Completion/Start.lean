import DexExpressiveness.Simulation.Completion.Mid

/-!
# The start of a completion: `seq := seq + 1`, `S[e].inv := ⊥`

From `IsSimulationState` to the relation with the committing thread and Step
left out (`MidThreads`, `MidData`, `TokensRelated`), for the run right after
the commit released the invocation and before `effects`.

`WaitingStepsHaveNoInvocation` holds in every simulation state
(`IsSimulationState.waitingHasNoInvocation`).
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- A Step in phase `WT` has no invocation: [STEP-COMPLETE-WAITFOR] clears
`S[e].inv` before `register(e, W)` (spec §4.4), and `eval(e)` sets a new one
only when it leaves `WT` (spec §4.2). -/
def WaitingStepsHaveNoInvocation (config : Dex.Config signature) : Prop :=
  ∀ run, config.run = some run → ∀ record, record ∈ run.steps → record.phase = .waiting →
    record.invocation = none

/-- The run after `seq := seq + 1` and `S[e].inv := ⊥`. -/
def releasedRun (run : Dex.Run signature) (stepId : Dex.StepExecId signature) :
    Dex.Run signature :=
  ({ run with seq := run.seq + 1 } : Dex.Run signature).updateStep stepId
    (fun record => { record with invocation := none })

/-- A Step that carries a thread of a configuration with the L invariant has
the thread's ID in its input, also during the start action. -/
theorem wp3b_threadOfInput_of_invariant (program : Program) (lConfig : L.Config)
    (lInvariant : L.Invariant program lConfig) (thread : L.Thread)
    (isThread : thread ∈ lConfig.threads) (record : Dex.StepRecord signature)
    (carries : StepCarriesThread program thread record) :
    threadOfInput record.input = thread.core.tid := by
  by_cases isBoot : thread.phase = .bootPending ∨ thread.phase = .bootRunning
  · obtain ⟨_, _, _, input, isCore⟩ := lInvariant.bootIsInitial thread isThread isBoot
    unfold StepCarriesThread at carries
    rcases isBoot with isPending | isRunning
    · simp only [isPending] at carries
      obtain ⟨_, value, hasInput, _⟩ := carries
      simp [threadOfInput, hasInput, isCore]
    · simp only [isRunning] at carries
      obtain ⟨_, value, hasInput, _⟩ := carries
      simp [threadOfInput, hasInput, isCore]
  · exact wp3b_threadOfInput_of_carries program thread record (fun isPending => isBoot (Or.inl isPending))
      (fun isRunning => isBoot (Or.inr isRunning)) carries

/-- While a thread is outside the start action, no thread is in it. -/
theorem wp3b_noBoot_of_other (program : Program) (lConfig : L.Config)
    (lInvariant : L.Invariant program lConfig) (thread other : L.Thread)
    (isThread : thread ∈ lConfig.threads) (isOther : other ∈ lConfig.threads)
    (isDistinct : other ≠ thread) :
    other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning := by
  have notBoot : ¬ (other.phase = .bootPending ∨ other.phase = .bootRunning) := by
    intro isBoot
    obtain ⟨isOnly, _⟩ := lInvariant.bootIsInitial other isOther isBoot
    rw [isOnly] at isThread
    have isSame : thread = other := by simpa using isThread
    exact isDistinct isSame.symm
  exact ⟨fun isPending => notBoot (Or.inl isPending), fun isRunning => notBoot (Or.inr isRunning)⟩

/-- The thread-and-Step part of the relation after the commit released the
invocation of the Step that carries `thread`. `lMid` is any L configuration
with the other threads unchanged and the committing thread, if still there,
neither blocked nor in the start action. -/
theorem wp3b_start_threads (program : Program) (lConfig lMid : L.Config)
    (dexConfig dexMid : Dex.Config signature) (run : Dex.Run signature)
    (state : IsSimulationState program lConfig dexConfig)
    (hasRun : dexConfig.run = some run) (isRunning : run.status = .running)
    (waitingHasNoInvocation : WaitingStepsHaveNoInvocation dexConfig)
    (thread : L.Thread) (isThread : thread ∈ lConfig.threads)
    (record : Dex.StepRecord signature) (isRecord : record ∈ run.steps)
    (isActive : record.phase.isActive = true)
    (carries : StepCarriesThread program thread record)
    (task : Dex.Task signature) (isTask : task ∈ dexConfig.tasks)
    (hasInvocation : record.invocation = some { method := .execute, state := .running task.id })
    (hasStep : task.step = record.id)
    (isNotBlocked : ∀ on since, thread.phase ≠ .blocked on since)
    (others : ∀ other, other.core.tid ≠ thread.core.tid →
      (other ∈ lMid.threads ↔ other ∈ lConfig.threads))
    (ownMid : ∀ own, own ∈ lMid.threads → own.core.tid = thread.core.tid →
      (∀ on since, own.phase ≠ .blocked on since) ∧
      own.phase ≠ .bootPending ∧ own.phase ≠ .bootRunning)
    (nodupMid : (lMid.threads.map (fun other => other.core.tid)).Nodup)
    (sameQueues : lMid.cvQueues = lConfig.cvQueues)
    (hasRunMid : dexMid.run = some (releasedRun run record.id))
    (tasksMid : dexMid.tasks =
      dexConfig.tasks.filter (fun other => !decide (other.id = task.id))) :
    MidThreads program lMid dexMid (releasedRun run record.id) thread.core.tid record.id := by
  have related := state.isRelated.running run hasRun isRunning
  have lInvariant := state.lInvariant
  have runInvariant := state.dexInvariant.runInvariant run hasRun
  let release : Dex.StepRecord signature → Dex.StepRecord signature := fun original =>
    if original.id = record.id then { original with invocation := none } else original
  have stepsReleased : (releasedRun run record.id).steps = run.steps.map release := rfl
  have releaseFacts : ∀ original, (release original).id = original.id ∧
      (release original).input = original.input ∧
      (release original).options = original.options ∧
      (release original).phase = original.phase ∧
      (release original).waitState = original.waitState := by
    intro original
    by_cases isOwn : original.id = record.id <;> simp [release, isOwn]
  have releaseOther : ∀ original, original.id ≠ record.id → release original = original := by
    intro original isOther
    simp [release, isOther]
  have sameRecord : ∀ original, original ∈ run.steps → original.id = record.id →
      original = record := fun original isOriginal sameId =>
    eq_of_nodup_map (fun candidate => candidate.id) run.steps runInvariant.idsNodup isOriginal
      isRecord sameId
  have sameThreadOfTid : ∀ other, other ∈ lConfig.threads → other.core.tid = thread.core.tid →
      other = thread := fun other isOther sameId =>
    eq_of_nodup_map (fun candidate => candidate.core.tid) lConfig.threads
      lInvariant.threadIdsNodup isOther isThread sameId
  have threadTid : ∀ other, other ∈ lConfig.threads → ∀ candidate,
      StepCarriesThread program other candidate →
      threadOfInput candidate.input = other.core.tid := fun other isOther candidate =>
    wp3b_threadOfInput_of_invariant program lConfig lInvariant other isOther candidate
  have otherNoBoot : ∀ other, other ∈ lConfig.threads → other.core.tid ≠ thread.core.tid →
      other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning := by
    intro other isOther isDistinct
    exact wp3b_noBoot_of_other program lConfig lInvariant thread other isThread isOther
      (fun isSame => isDistinct (by rw [isSame]))
  have blockedIsOld : ∀ other, other ∈ lMid.threads → ∀ on since,
      other.phase = .blocked on since →
      other ∈ lConfig.threads ∧ other.core.tid ≠ thread.core.tid := by
    intro other isOther on since isBlocked
    have isDistinct : other.core.tid ≠ thread.core.tid := fun isOwn =>
      (ownMid other isOther isOwn).1 on since isBlocked
    exact ⟨(others other isDistinct).mp isOther, isDistinct⟩
  have otherStep : ∀ other, other ∈ lConfig.threads → other.core.tid ≠ thread.core.tid →
      ∀ candidate, candidate ∈ run.steps → StepCarriesThread program other candidate →
      candidate.id ≠ record.id := by
    intro other isOther isDistinct candidate isCandidate otherCarries isOwn
    rw [sameRecord candidate isCandidate isOwn] at otherCarries
    exact isDistinct ((threadTid other isOther record otherCarries).symm.trans
      (threadTid thread isThread record carries))
  have noBootMid : ∀ other, other ∈ lMid.threads →
      other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning := by
    intro other isOther
    by_cases isOwn : other.core.tid = thread.core.tid
    · exact (ownMid other isOther isOwn).2
    · exact otherNoBoot other ((others other isOwn).mp isOther) isOwn
  exact
    { hasRun := hasRunMid
      timers := by
        intro timer fireTime
        constructor
        · intro isArmed
          obtain ⟨other, chan, since, isOther, isBlocked, isTimed, isIndex⟩ :=
            (related.timers timer fireTime).mp isArmed
          have isDistinct : other.core.tid ≠ thread.core.tid := by
            intro sameId
            rw [sameThreadOfTid other isOther sameId] at isBlocked
            exact isNotBlocked _ since isBlocked
          exact ⟨other, chan, since, (others other isDistinct).mpr isOther, isBlocked, isTimed,
            isIndex⟩
        · rintro ⟨other, chan, since, isOther, isBlocked, isTimed, isIndex⟩
          exact (related.timers timer fireTime).mpr ⟨other, chan, since,
            (blockedIsOld other isOther _ since isBlocked).1, isBlocked, isTimed, isIndex⟩
      threadsHaveSteps := by
        intro other isOther isDistinct isLive
        have isOld := (others other isDistinct).mp isOther
        obtain ⟨candidate, isCandidate, candidateActive, otherCarries, isMatch⟩ :=
          related.threadsHaveSteps other isOld isLive
        have isOtherStep := otherStep other isOld isDistinct candidate isCandidate otherCarries
        refine ⟨candidate, ?_, candidateActive, otherCarries, ?_⟩
        · rw [stepsReleased]
          exact List.mem_map.mpr ⟨candidate, isCandidate, releaseOther candidate isOtherStep⟩
        · apply wp3b_phaseMatches_congr program lConfig lMid dexConfig dexMid other candidate
            ?_ (fun _ _ cv _ => by rw [sameQueues]) isMatch
          intro otherTask isOtherTask otherHasStep
          rw [tasksMid]
          refine List.mem_filter.mpr ⟨isOtherTask, ?_⟩
          simp only [Bool.not_eq_eq_eq_not, Bool.not_true, decide_eq_false_iff_not]
          intro sameId
          have isSame : otherTask = task :=
            eq_of_nodup_map (fun candidateTask => candidateTask.id) dexConfig.tasks
              state.tasksDistinct.1 isOtherTask isTask sameId
          rw [isSame, hasStep] at otherHasStep
          exact isOtherStep otherHasStep.symm
      stepsHaveThreads := by
        intro released isReleased releasedActive isOtherStep
        rw [stepsReleased] at isReleased
        obtain ⟨candidate, isCandidate, rfl⟩ := List.mem_map.mp isReleased
        rw [(releaseFacts candidate).1] at isOtherStep
        rw [(releaseFacts candidate).2.2.2.1] at releasedActive
        rw [releaseOther candidate isOtherStep]
        obtain ⟨other, isOther, isLive, otherCarries⟩ :=
          related.stepsHaveThreads candidate isCandidate releasedActive
        have isDistinct : other.core.tid ≠ thread.core.tid := by
          intro sameId
          rw [sameThreadOfTid other isOther sameId] at otherCarries
          apply isOtherStep
          rw [related.activeStepsHaveDistinctThreads candidate isCandidate record isRecord
            releasedActive isActive ((threadTid thread isThread candidate otherCarries).trans
              (threadTid thread isThread record carries).symm)]
        exact ⟨other, (others other isDistinct).mpr isOther, isDistinct, isLive, otherCarries⟩
      activeStepsHaveDistinctThreads := by
        intro first isFirst second isSecond firstActive secondActive sameThread
        rw [stepsReleased] at isFirst isSecond
        obtain ⟨firstOld, isFirstOld, rfl⟩ := List.mem_map.mp isFirst
        obtain ⟨secondOld, isSecondOld, rfl⟩ := List.mem_map.mp isSecond
        rw [(releaseFacts firstOld).2.2.2.1] at firstActive
        rw [(releaseFacts secondOld).2.2.2.1] at secondActive
        rw [(releaseFacts firstOld).2.1, (releaseFacts secondOld).2.1] at sameThread
        rw [related.activeStepsHaveDistinctThreads firstOld isFirstOld secondOld isSecondOld
          firstActive secondActive sameThread]
      invocationsAreActive := by
        intro released isReleased hasReleasedInvocation
        rw [stepsReleased] at isReleased
        obtain ⟨candidate, isCandidate, rfl⟩ := List.mem_map.mp isReleased
        rw [(releaseFacts candidate).2.2.2.1]
        by_cases isOwn : candidate.id = record.id
        · simp [release, isOwn] at hasReleasedInvocation
        · rw [releaseOther candidate isOwn] at hasReleasedInvocation
          exact related.invocationsAreActive candidate isCandidate hasReleasedInvocation
      blockingOrder := by
        intro first second firstRecord secondRecord firstOn firstSince secondOn secondSince
          firstWait secondWait isFirst isSecond isFirstRecord isSecondRecord firstActive
          secondActive firstCarries secondCarries firstBlocked secondBlocked firstHasWait
          secondHasWait
        rw [stepsReleased] at isFirstRecord isSecondRecord
        obtain ⟨firstOld, isFirstOld, rfl⟩ := List.mem_map.mp isFirstRecord
        obtain ⟨secondOld, isSecondOld, rfl⟩ := List.mem_map.mp isSecondRecord
        rw [(releaseFacts firstOld).2.2.2.1] at firstActive
        rw [(releaseFacts secondOld).2.2.2.1] at secondActive
        rw [(releaseFacts firstOld).2.2.2.2] at firstHasWait
        rw [(releaseFacts secondOld).2.2.2.2] at secondHasWait
        have firstNoBoot := noBootMid first isFirst
        have secondNoBoot := noBootMid second isSecond
        exact related.blockingOrder first second firstOld secondOld firstOn firstSince secondOn
          secondSince firstWait secondWait
          (blockedIsOld first isFirst firstOn firstSince firstBlocked).1
          (blockedIsOld second isSecond secondOn secondSince secondBlocked).1 isFirstOld
          isSecondOld firstActive secondActive
          (wp3b_carries_congr program first first _ firstOld firstNoBoot.1 firstNoBoot.2
            firstNoBoot.1 firstNoBoot.2 rfl rfl rfl (releaseFacts firstOld).1.symm
            (releaseFacts firstOld).2.1.symm (releaseFacts firstOld).2.2.1.symm firstCarries)
          (wp3b_carries_congr program second second _ secondOld secondNoBoot.1 secondNoBoot.2
            secondNoBoot.1 secondNoBoot.2 rfl rfl rfl (releaseFacts secondOld).1.symm
            (releaseFacts secondOld).2.1.symm (releaseFacts secondOld).2.2.1.symm secondCarries)
          firstBlocked secondBlocked firstHasWait secondHasWait
      tasksAreCurrent := by
        intro otherTask isOtherTask
        rw [tasksMid] at isOtherTask
        obtain ⟨isOldTask, isOtherId⟩ := List.mem_filter.mp isOtherTask
        simp only [Bool.not_eq_eq_eq_not, Bool.not_true, decide_eq_false_iff_not] at isOtherId
        have wasCurrent := related.tasksAreCurrent otherTask isOldTask
        unfold Dex.Config.isTaskCurrent at wasCurrent ⊢
        rw [hasRun] at wasCurrent
        rw [hasRunMid]
        simp only at wasCurrent ⊢
        cases foundCase : run.findStep? otherTask.step with
        | none => simp [foundCase] at wasCurrent
        | some candidate =>
          simp only [foundCase, decide_eq_true_eq] at wasCurrent
          obtain ⟨isCandidate, hasId⟩ := Dex.Run.mem_of_findStep? run otherTask.step candidate
            foundCase
          have isOtherStep : otherTask.step ≠ record.id := by
            intro isOwn
            rw [sameRecord candidate isCandidate (hasId.trans isOwn), hasInvocation] at wasCurrent
            simp only [Option.some.injEq, Dex.Invocation.mk.injEq,
              Dex.InvocationState.running.injEq] at wasCurrent
            exact isOtherId wasCurrent.2.symm
          have foundReleased : (releasedRun run record.id).findStep? otherTask.step =
              some candidate := by
            have isSame := Dex.Run.findStep?_updateStep_other
              ({ run with seq := run.seq + 1 } : Dex.Run signature) record.id otherTask.step
              (fun current => { current with invocation := none }) (fun _ => rfl) isOtherStep
            exact isSame.trans foundCase
          simp [foundReleased, wasCurrent]
      runInvariant := by
        have bumped := runInvariant.bumpSeq
        have updated := bumped.updateStep record.id
          (fun current => { current with invocation := none }) (fun _ => rfl)
          (fun _ isPhase => ⟨isPhase, rfl⟩)
        exact updated.mono (Nat.le_succ _)
      waitingHasNoInvocation := by
        intro released isReleased isWaiting
        rw [stepsReleased] at isReleased
        obtain ⟨candidate, isCandidate, rfl⟩ := List.mem_map.mp isReleased
        rw [(releaseFacts candidate).2.2.2.1] at isWaiting
        by_cases isOwn : candidate.id = record.id
        · simp [release, isOwn]
        · rw [releaseOther candidate isOwn]
          exact waitingHasNoInvocation run hasRun candidate isCandidate isWaiting
      ownStep := by
        intro released isReleased isOwn
        rw [stepsReleased] at isReleased
        obtain ⟨candidate, isCandidate, rfl⟩ := List.mem_map.mp isReleased
        rw [(releaseFacts candidate).1] at isOwn
        have isSame := sameRecord candidate isCandidate isOwn
        rw [isSame]
        refine ⟨?_, by simp [release], ?_⟩
        · rw [(releaseFacts record).2.2.2.1]
          intro isWaiting
          rw [waitingHasNoInvocation run hasRun record isRecord isWaiting] at hasInvocation
          cases hasInvocation
        · rw [(releaseFacts record).2.1]
          exact threadTid thread isThread record carries
      ownThread := fun own isOwn sameId => (ownMid own isOwn sameId).1
      noBoot := noBootMid
      threadIdsNodup := nodupMid
      blockedFit := by
        intro other isOther on since isBlocked
        exact lInvariant.phasesFitCode other (blockedIsOld other isOther on since isBlocked).1
      sinceInjective := by
        intro first isFirst second isSecond firstOn secondOn since firstBlocked secondBlocked
        exact lInvariant.sinceInjective first
          (blockedIsOld first isFirst firstOn since firstBlocked).1 second
          (blockedIsOld second isSecond secondOn since secondBlocked).1 firstOn secondOn since
          firstBlocked secondBlocked }

/-- The data part of the relation after the commit released the invocation. -/
theorem wp3b_start_data (program : Program) (lConfig lMid : L.Config)
    (dexConfig : Dex.Config signature) (run : Dex.Run signature)
    (state : IsSimulationState program lConfig dexConfig)
    (hasRun : dexConfig.run = some run) (isRunning : run.status = .running) (thread : L.Thread)
    (isThread : thread ∈ lConfig.threads)
    (stepId : Dex.StepExecId signature) (sameData : SameData lConfig lMid)
    (sameCounter : lMid.timerCounter = lConfig.timerCounter)
    (others : ∀ other, other.core.tid ≠ thread.core.tid →
      (other ∈ lMid.threads ↔ other ∈ lConfig.threads))
    (ownCounter : ∀ own, own ∈ lMid.threads → own.core.tid = thread.core.tid →
      thread.core.freshCounter ≤ own.core.freshCounter)
    (nodupMid : (lMid.threads.map (fun other => other.core.tid)).Nodup)
    (ownFlag : lMid.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [] ∨
      lMid.findThread? thread.core.tid = none) :
    MidData lMid (releasedRun run stepId) thread.core.tid := by
  have related := state.isRelated.running run hasRun isRunning
  exact
    { isRunning := isRunning
      shared := by
        intro name
        have sameRead : lMid.readShared name = lConfig.readShared name := by
          simp [L.Config.readShared, sameData.shared]
        rw [sameRead]
        exact related.shared name
      cvQueues := by
        intro cv
        rw [sameData.cvQueues]
        exact related.cvQueues cv
      inputQueues := by
        intro chan
        have sameInput : lMid.pendingInput chan = lConfig.pendingInput chan := by
          simp [L.Config.pendingInput, sameData.inputQueues]
        rw [sameInput]
        exact related.inputQueues chan
      joinTokens := by
        intro joined
        rw [sameData.joinable]
        exact related.joinTokens joined
      cancelRequestsAreFlags := by
        intro target hasRequest
        rw [sameData.cancelFlags]
        exact related.cancelRequestsAreFlags target hasRequest
      cancelFlagsAreRequests := by
        intro target isOther isFlagged
        rw [sameData.cancelFlags] at isFlagged
        have sameFound : lConfig.findThread? target = lMid.findThread? target :=
          wp3b_findThread?_congr lConfig lMid target nodupMid (fun candidate hasId =>
            (others candidate (by rw [hasId]; exact isOther)).symm)
        rw [← sameFound]
        rcases related.cancelFlagsAreRequests target isFlagged with hasRequest | isCanceled |
          ⟨isGone, isNoChild⟩
        · exact Or.inl hasRequest
        · exact Or.inr (Or.inl isCanceled)
        · refine Or.inr (Or.inr ⟨isGone, ?_⟩)
          intro live isLive index isAbove
          by_cases isOwn : live.core.tid = thread.core.tid
          · rw [isOwn]
            exact isNoChild thread isThread index
              (Nat.le_trans (ownCounter live isLive isOwn) isAbove)
          · exact isNoChild live ((others live isOwn).mp isLive) index isAbove
      ownIsNoChild := by
        intro live isLive index isAbove isPrefix
        by_cases isOwn : live.core.tid = thread.core.tid
        · rw [isOwn] at isPrefix
          have isShorter := isPrefix.length_le
          rw [List.length_append, List.length_singleton] at isShorter
          omega
        · exact state.lInvariant.threadIdsFresh live ((others live isOwn).mp isLive) thread isThread
            index isAbove isPrefix
      ownFlag := ownFlag
      cvWakes := by
        intro cv waiter
        rw [sameData.cvWakes]
        exact related.cvWakes cv waiter
      timerCounter := by
        rw [sameCounter]
        exact related.timerCounter
      outputsEmpty := related.outputsEmpty }

/-- The mutex tokens after the commit released the invocation, once no thread
is in the start action. -/
theorem wp3b_start_tokens (program : Program) (lConfig lMid : L.Config)
    (dexConfig : Dex.Config signature) (run : Dex.Run signature)
    (state : IsSimulationState program lConfig dexConfig)
    (hasRun : dexConfig.run = some run) (isRunning : run.status = .running)
    (stepId : Dex.StepExecId signature)
    (noBoot : ∀ other, other ∈ lConfig.threads →
      other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning)
    (sameOwners : lMid.mutexOwners = lConfig.mutexOwners) :
    TokensRelated lMid (releasedRun run stepId) := by
  intro mutex
  rw [sameOwners]
  exact (state.isRelated.running run hasRun isRunning).mutexTokens noBoot mutex

end DexExpressiveness
