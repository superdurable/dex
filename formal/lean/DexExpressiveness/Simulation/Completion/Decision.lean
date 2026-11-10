import DexExpressiveness.Simulation.Completion.Mid

/-!
# The decision of an Execute

`decide(e, D)` finishes the committing Step and then creates the Steps of the
moves (spec §4.2). On the L side the committing thread continues, forks,
halts, or ends.

- `wp3b_finish`: `finish(e, CO)` turns the relation with the committing
  thread and Step left out into `RunningRelated` for the L configuration
  without that thread.
- `wp3b_spawn`: one `new(T, v, o, e)` and one new ready thread.
- `wp3b_addHalted`: a halted thread, which has no Step.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- The configuration without the thread with this ID. -/
def dropThread (config : L.Config) (tid : Tid) : L.Config :=
  { config with threads := config.threads.filter (fun thread => !decide (thread.core.tid = tid)) }

/-- The threads of `dropThread`. -/
theorem wp3b_mem_dropThread (config : L.Config) (tid : Tid) (thread : L.Thread) :
    thread ∈ (dropThread config tid).threads ↔ thread ∈ config.threads ∧ thread.core.tid ≠ tid := by
  simp [dropThread]

/-- Dropping a thread keeps thread IDs unique. -/
theorem wp3b_nodup_dropThread (config : L.Config) (tid : Tid)
    (idsNodup : (config.threads.map (fun thread => thread.core.tid)).Nodup) :
    ((dropThread config tid).threads.map (fun thread => thread.core.tid)).Nodup :=
  idsNodup.sublist (List.Sublist.map _ List.filter_sublist)

/-- The Step that `new(T, v, o, from)` creates (spec §4.2). -/
def createdRecord (run : Dex.Run signature) (move : Dex.Move signature)
    (origin : Dex.Origin signature) : Dex.StepRecord signature :=
  { id := { stepType := move.stepType, number := (run.counters.get? move.stepType).getD 0 + 1 }
    input := move.input
    options := move.options
    origin := origin
    phase := if move.options.skipWaitFor then .execute else .waitFor
    invocation := some
      { method := if move.options.skipWaitFor then Dex.Method.execute else Dex.Method.waitFor
        state := .pending }
    waitState := none
    conditionResults := none }

/-- The created Step is active. -/
theorem wp3b_createdRecord_isActive (run : Dex.Run signature) (move : Dex.Move signature)
    (origin : Dex.Origin signature) : (createdRecord run move origin).phase.isActive = true := by
  unfold createdRecord
  cases move.options.skipWaitFor <;> rfl

/-- `finish(e, CO)` of the committing Step. The committing thread is left out
of the L configuration; `wp3b_spawn` or `wp3b_addHalted` puts it back. -/
theorem wp3b_finish (program : Program) (lConfig : L.Config)
    (dexConfig finished : Dex.Config signature) (run : Dex.Run signature) (tid : Tid)
    (stepId : Dex.StepExecId signature)
    (mid : MidThreads program lConfig dexConfig run tid stepId)
    (data : MidData lConfig run tid) (tokens : TokensRelated lConfig run)
    (sameTasks : finished.tasks = dexConfig.tasks)
    (hasRun : finished.run = some (run.finishStep stepId .completed)) :
    RunningRelated program (dropThread lConfig tid) finished
      (run.finishStep stepId .completed) := by
  have stepsFinished : ∀ record, record ∈ (run.finishStep stepId .completed).steps ↔
      ∃ original, original ∈ run.steps ∧
        record = if original.id = stepId then
          { original with
            invocation := none, input := none, conditionResults := none, phase := .completed }
          else original :=
    fun record => Dex.Run.mem_updateStep (run.disarmTimersOf stepId) stepId _ record
  have keptStep : ∀ record, record ∈ run.steps → record.id ≠ stepId →
      record ∈ (run.finishStep stepId .completed).steps := by
    intro record isRecord isOther
    exact (stepsFinished record).mpr ⟨record, isRecord, by simp [isOther]⟩
  have activeWasOther : ∀ record, record ∈ (run.finishStep stepId .completed).steps →
      record.phase.isActive = true → record ∈ run.steps ∧ record.id ≠ stepId := by
    intro record isRecord isActive
    obtain ⟨original, isOriginal, isChanged⟩ := (stepsFinished record).mp isRecord
    by_cases isOwn : original.id = stepId
    · simp only [isOwn, ↓reduceIte] at isChanged
      rw [isChanged] at isActive
      cases isActive
    · simp only [isOwn, ↓reduceIte] at isChanged
      rw [isChanged]
      exact ⟨isOriginal, isOwn⟩
  have findOther : ∀ thread, thread ≠ tid →
      (dropThread lConfig tid).findThread? thread = lConfig.findThread? thread := by
    intro thread isOther
    symm
    apply wp3b_findThread?_congr lConfig (dropThread lConfig tid) thread
      (wp3b_nodup_dropThread lConfig tid mid.threadIdsNodup)
    intro candidate hasId
    rw [wp3b_mem_dropThread]
    exact ⟨fun isMember => ⟨isMember, by rw [hasId]; exact isOther⟩, fun isMember => isMember.1⟩
  exact
    { shared := data.shared
      cvQueues := data.cvQueues
      inputQueues := data.inputQueues
      mutexTokens := fun _ => tokens
      mutexesBeforeBoot := by
        intro thread isThread isBoot
        have isNotBoot := mid.noBoot thread ((wp3b_mem_dropThread lConfig tid thread).mp isThread).1
        rcases isBoot with isPending | isRunning
        · exact absurd isPending isNotBoot.1
        · exact absurd isRunning isNotBoot.2
      joinTokens := data.joinTokens
      cancelRequestsAreFlags := data.cancelRequestsAreFlags
      cancelFlagsAreRequests := by
        intro thread isFlagged
        by_cases isOwn : thread = tid
        · refine Or.inr (Or.inr ⟨?_, ?_⟩)
          · apply (wp3b_findThread?_none _ _).mpr
            intro candidate isCandidate
            rw [isOwn]
            exact ((wp3b_mem_dropThread lConfig tid candidate).mp isCandidate).2
          · intro live isLive index isAbove
            rw [isOwn]
            exact data.ownIsNoChild live ((wp3b_mem_dropThread lConfig tid live).mp isLive).1 index
              isAbove
        · rw [findOther thread isOwn]
          rcases data.cancelFlagsAreRequests thread isOwn isFlagged with hasRequest | isCanceled |
            ⟨isGone, isNoChild⟩
          · exact Or.inl hasRequest
          · exact Or.inr (Or.inl isCanceled)
          · exact Or.inr (Or.inr ⟨isGone, fun live isLive index isAbove =>
              isNoChild live ((wp3b_mem_dropThread lConfig tid live).mp isLive).1 index isAbove⟩)
      cvWakes := data.cvWakes
      timers := by
        intro timer fireTime
        have isFiltered : (run.finishStep stepId .completed).timers.get? timer =
            if (!decide (timer.step = stepId)) = true then run.timers.get? timer else none :=
          AssocList.wp3b_get?_filter (fun (candidate : Dex.TimerId signature) =>
            !decide (candidate.step = stepId)) run.timers timer
        constructor
        · intro isArmed
          rw [isFiltered] at isArmed
          split at isArmed
          · obtain ⟨thread, chan, since, isThread, isBlocked, isTimed, isIndex⟩ :=
              (mid.timers timer fireTime).mp isArmed
            refine ⟨thread, chan, since, ?_, isBlocked, isTimed, isIndex⟩
            exact (wp3b_mem_dropThread lConfig tid thread).mpr ⟨isThread, fun isOwn =>
              mid.ownThread thread isThread isOwn _ since isBlocked⟩
          · cases isArmed
        · rintro ⟨thread, chan, since, isThread, isBlocked, isTimed, isIndex⟩
          obtain ⟨isOld, isOther⟩ := (wp3b_mem_dropThread lConfig tid thread).mp isThread
          obtain ⟨record, isRecord, hasId, _, carries⟩ :=
            mid.timedStep thread isOld timer fireTime chan since isBlocked isTimed
          have isOtherStep : timer.step ≠ stepId := by
            rw [← hasId]
            exact mid.ne_ownStep thread isOld isOther record isRecord carries
          rw [isFiltered]
          simp only [isOtherStep, decide_false, Bool.not_false, ↓reduceIte]
          exact (mid.timers timer fireTime).mpr ⟨thread, chan, since, isOld, isBlocked, isTimed,
            isIndex⟩
      timerCounter := data.timerCounter
      outputsEmpty := data.outputsEmpty
      threadsHaveSteps := by
        intro thread isThread isLive
        obtain ⟨isOld, isOther⟩ := (wp3b_mem_dropThread lConfig tid thread).mp isThread
        obtain ⟨record, isRecord, isActive, carries, isMatch⟩ :=
          mid.threadsHaveSteps thread isOld isOther isLive
        refine ⟨record, keptStep record isRecord
          (mid.ne_ownStep thread isOld isOther record isRecord carries), isActive, carries, ?_⟩
        exact wp3b_phaseMatches_congr program lConfig _ dexConfig finished thread record
          (fun task isTask _ => by rw [sameTasks]; exact isTask) (fun _ _ _ _ => rfl) isMatch
      stepsHaveThreads := by
        intro record isRecord isActive
        obtain ⟨isOld, isOther⟩ := activeWasOther record isRecord isActive
        obtain ⟨thread, isThread, isOtherThread, isLive, carries⟩ :=
          mid.stepsHaveThreads record isOld isActive isOther
        exact ⟨thread, (wp3b_mem_dropThread lConfig tid thread).mpr ⟨isThread, isOtherThread⟩,
          isLive, carries⟩
      activeStepsHaveDistinctThreads := by
        intro first isFirst second isSecond firstActive secondActive sameThread
        exact mid.activeStepsHaveDistinctThreads first (activeWasOther first isFirst firstActive).1
          second (activeWasOther second isSecond secondActive).1 firstActive secondActive sameThread
      invocationsAreActive := by
        intro record isRecord hasInvocation
        obtain ⟨original, isOriginal, isChanged⟩ := (stepsFinished record).mp isRecord
        by_cases isOwn : original.id = stepId
        · simp only [isOwn, ↓reduceIte] at isChanged
          rw [isChanged] at hasInvocation
          exact absurd rfl hasInvocation
        · simp only [isOwn, ↓reduceIte] at isChanged
          rw [isChanged] at hasInvocation ⊢
          exact mid.invocationsAreActive original isOriginal hasInvocation
      blockingOrder := by
        intro first second firstRecord secondRecord firstOn firstSince secondOn secondSince
          firstWait secondWait isFirst isSecond isFirstRecord isSecondRecord firstActive
          secondActive firstCarries secondCarries firstBlocked secondBlocked firstHasWait
          secondHasWait
        exact mid.blockingOrder first second firstRecord secondRecord firstOn firstSince secondOn
          secondSince firstWait secondWait
          ((wp3b_mem_dropThread lConfig tid first).mp isFirst).1
          ((wp3b_mem_dropThread lConfig tid second).mp isSecond).1
          (activeWasOther firstRecord isFirstRecord firstActive).1
          (activeWasOther secondRecord isSecondRecord secondActive).1 firstActive secondActive
          firstCarries secondCarries firstBlocked secondBlocked firstHasWait secondHasWait
      tasksAreCurrent := by
        intro task isTask
        rw [sameTasks] at isTask
        have wasCurrent := mid.tasksAreCurrent task isTask
        unfold Dex.Config.isTaskCurrent at wasCurrent ⊢
        rw [mid.hasRun] at wasCurrent
        rw [hasRun]
        simp only at wasCurrent ⊢
        cases foundCase : run.findStep? task.step with
        | none => simp [foundCase] at wasCurrent
        | some record =>
          simp only [foundCase, decide_eq_true_eq] at wasCurrent
          obtain ⟨isRecord, hasId⟩ := Dex.Run.mem_of_findStep? run task.step record foundCase
          have isOther : task.step ≠ stepId := by
            intro isOwn
            rw [(mid.ownStep record isRecord (hasId.trans isOwn)).2.1] at wasCurrent
            cases wasCurrent
          have foundFinished : (run.finishStep stepId .completed).findStep? task.step =
              some record := by
            unfold Dex.Run.finishStep
            have isSame := Dex.Run.findStep?_updateStep_other (run.disarmTimersOf stepId) stepId
              task.step (fun current =>
                { current with
                  invocation := none, input := none, conditionResults := none
                  phase := .completed }) (fun _ => rfl) isOther
            exact isSame.trans foundCase
          simp [foundFinished, wasCurrent] }

/-- One `new(T, v, o, from)` and one new ready thread that the created Step
carries. -/
theorem wp3b_spawn (program : Program) (lConfig lNext : L.Config)
    (dexConfig dexNext : Dex.Config signature) (run : Dex.Run signature)
    (related : RunningRelated program lConfig dexConfig run)
    (hasRun : dexConfig.run = some run)
    (noBoot : ∀ other, other ∈ lConfig.threads →
      other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning)
    (thread : L.Thread) (move : Dex.Move signature) (origin : Dex.Origin signature)
    (sameData : SameData lConfig lNext)
    (threadsNext : ∀ other, other ∈ lNext.threads ↔ other ∈ lConfig.threads ∨ other = thread)
    (nodupNext : (lNext.threads.map (fun other => other.core.tid)).Nodup)
    (isFresh : ∀ other, other ∈ lConfig.threads → other.core.tid ≠ thread.core.tid)
    (isReady : thread.phase = .ready)
    (flagIsRequest : lConfig.cancelFlags.contains thread.core.tid = true →
      run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ [])
    (isNoParent : ∀ target, lConfig.cancelFlags.contains target = true →
      lConfig.findThread? target = none →
      run.pendingMessages (Chan.cancelRequest target) = [] →
      ∀ index, thread.core.freshCounter ≤ index → ¬ (thread.core.tid ++ [index]) <+: target)
    (carries : StepCarriesThread program thread (createdRecord run move origin))
    (waitsFirst : move.options.skipWaitFor = !(thread.timer.isSome || isBlockingHead thread.code))
    (counterNext :
      (((run.newStep move.stepType move.input move.options origin).counters.get?
        StepType.timed).getD 0) = lNext.timerCounter)
    (sameTasks : dexNext.tasks = dexConfig.tasks)
    (hasRunNext : dexNext.run =
      some (run.newStep move.stepType move.input move.options origin)) :
    RunningRelated program lNext dexNext
      (run.newStep move.stepType move.input move.options origin) := by
  have stepsNext : (run.newStep move.stepType move.input move.options origin).steps =
      run.steps ++ [createdRecord run move origin] := rfl
  have isNotBoot : thread.phase ≠ .bootPending ∧ thread.phase ≠ .bootRunning := by
    rw [isReady]
    exact ⟨by simp, by simp⟩
  have createdThread : threadOfInput (createdRecord run move origin).input = thread.core.tid :=
    wp3b_threadOfInput_of_carries program thread _ isNotBoot.1 isNotBoot.2 carries
  have oldThread : ∀ record, record ∈ run.steps → record.phase.isActive = true →
      threadOfInput record.input ≠ thread.core.tid := by
    intro record isRecord isActive
    obtain ⟨other, isOther, _, otherCarries⟩ := related.stepsHaveThreads record isRecord isActive
    rw [wp3b_threadOfInput_of_carries program other record (noBoot other isOther).1
      (noBoot other isOther).2 otherCarries]
    exact isFresh other isOther
  have blockedIsOld : ∀ other, other ∈ lNext.threads → ∀ on since,
      other.phase = .blocked on since → other ∈ lConfig.threads := by
    intro other isOther on since isBlocked
    rcases (threadsNext other).mp isOther with isOld | isNew
    · exact isOld
    · rw [isNew, isReady] at isBlocked
      cases isBlocked
  have carrierIsOld : ∀ other, other ∈ lConfig.threads → ∀ record,
      record ∈ (run.newStep move.stepType move.input move.options origin).steps →
      StepCarriesThread program other record → record ∈ run.steps := by
    intro other isOther record isRecord otherCarries
    rw [stepsNext] at isRecord
    rcases List.mem_append.mp isRecord with isOld | isNew
    · exact isOld
    · have isCreated : record = createdRecord run move origin := by simpa using isNew
      rw [isCreated] at otherCarries
      have sameThread := wp3b_threadOfInput_of_carries program other _ (noBoot other isOther).1
        (noBoot other isOther).2 otherCarries
      rw [createdThread] at sameThread
      exact absurd sameThread.symm (isFresh other isOther)
  have findOld : ∀ target current, lConfig.findThread? target = some current →
      lNext.findThread? target = some current := by
    intro target current isFound
    obtain ⟨isCurrent, hasId⟩ := wp3b_findThread?_some lConfig target current isFound
    rw [← hasId]
    exact wp3b_findThread?_of_mem lNext current nodupNext
      ((threadsNext current).mpr (Or.inl isCurrent))
  exact
    { shared := by
        intro name
        have sameRead : lNext.readShared name = lConfig.readShared name := by
          simp [L.Config.readShared, sameData.shared]
        rw [sameRead]
        exact related.shared name
      cvQueues := by
        intro cv
        rw [sameData.cvQueues]
        exact related.cvQueues cv
      inputQueues := by
        intro chan
        have sameInput : lNext.pendingInput chan = lConfig.pendingInput chan := by
          simp [L.Config.pendingInput, sameData.inputQueues]
        rw [sameInput]
        exact related.inputQueues chan
      mutexTokens := by
        intro noBootNext mutex
        rw [sameData.mutexOwners]
        exact related.mutexTokens (fun other isOther => noBoot other isOther) mutex
      mutexesBeforeBoot := by
        intro other isOther isBoot
        exfalso
        rcases (threadsNext other).mp isOther with isOld | isCreated
        · rcases isBoot with isPending | isRunning
          · exact (noBoot other isOld).1 isPending
          · exact (noBoot other isOld).2 isRunning
        · rw [isCreated, isReady] at isBoot
          simp at isBoot
      joinTokens := by
        intro joined
        rw [sameData.joinable]
        exact related.joinTokens joined
      cancelRequestsAreFlags := by
        intro target hasRequest
        rw [sameData.cancelFlags]
        exact related.cancelRequestsAreFlags target hasRequest
      cancelFlagsAreRequests := by
        intro target isFlagged
        rw [sameData.cancelFlags] at isFlagged
        by_cases isNew : target = thread.core.tid
        · left
          rw [isNew]
          exact flagIsRequest (by rw [← isNew]; exact isFlagged)
        · rcases related.cancelFlagsAreRequests target isFlagged with hasRequest |
            ⟨current, isFound, isCanceled⟩ | ⟨isGone, isNoChild⟩
          · exact Or.inl hasRequest
          · exact Or.inr (Or.inl ⟨current, findOld target current isFound, isCanceled⟩)
          · by_cases isEmpty : run.pendingMessages (Chan.cancelRequest target) = []
            · refine Or.inr (Or.inr ⟨?_, ?_⟩)
              · apply (wp3b_findThread?_none _ _).mpr
                intro other isOther
                rcases (threadsNext other).mp isOther with isOld | isCreated
                · exact (wp3b_findThread?_none _ _).mp isGone other isOld
                · rw [isCreated]
                  exact fun sameId => isNew sameId.symm
              · intro live isLive index isAbove
                rcases (threadsNext live).mp isLive with isOld | isCreated
                · exact isNoChild live isOld index isAbove
                · rw [isCreated] at isAbove ⊢
                  exact isNoParent target isFlagged isGone isEmpty index isAbove
            · exact Or.inl isEmpty
      cvWakes := by
        intro cv waiter
        rw [sameData.cvWakes]
        exact related.cvWakes cv waiter
      timers := by
        intro timer fireTime
        constructor
        · intro isArmed
          obtain ⟨other, chan, since, isOther, isBlocked, isTimed, isIndex⟩ :=
            (related.timers timer fireTime).mp isArmed
          exact ⟨other, chan, since, (threadsNext other).mpr (Or.inl isOther), isBlocked, isTimed,
            isIndex⟩
        · rintro ⟨other, chan, since, isOther, isBlocked, isTimed, isIndex⟩
          exact (related.timers timer fireTime).mpr ⟨other, chan, since,
            blockedIsOld other isOther _ since isBlocked, isBlocked, isTimed, isIndex⟩
      timerCounter := counterNext
      outputsEmpty := related.outputsEmpty
      threadsHaveSteps := by
        intro other isOther isLive
        rcases (threadsNext other).mp isOther with isOld | isNew
        · obtain ⟨record, isRecord, isActive, otherCarries, isMatch⟩ :=
            related.threadsHaveSteps other isOld isLive
          refine ⟨record, by rw [stepsNext]; exact List.mem_append_left _ isRecord, isActive,
            otherCarries, ?_⟩
          exact wp3b_phaseMatches_congr program lConfig lNext dexConfig dexNext other record
            (fun task isTask _ => by rw [sameTasks]; exact isTask)
            (fun _ _ cv _ => by rw [sameData.cvQueues]) isMatch
        · rw [isNew]
          refine ⟨createdRecord run move origin, by rw [stepsNext]; simp,
            wp3b_createdRecord_isActive run move origin, carries, ?_⟩
          unfold PhaseMatchesStep
          simp only [isReady, createdRecord, waitsFirst]
          cases thread.timer.isSome || isBlockingHead thread.code <;> simp
      stepsHaveThreads := by
        intro record isRecord isActive
        rw [stepsNext] at isRecord
        rcases List.mem_append.mp isRecord with isOld | isNew
        · obtain ⟨other, isOther, isLive, otherCarries⟩ :=
            related.stepsHaveThreads record isOld isActive
          exact ⟨other, (threadsNext other).mpr (Or.inl isOther), isLive, otherCarries⟩
        · have isCreated : record = createdRecord run move origin := by simpa using isNew
          rw [isCreated]
          exact ⟨thread, (threadsNext thread).mpr (Or.inr rfl), by simp [isReady], carries⟩
      activeStepsHaveDistinctThreads := by
        intro first isFirst second isSecond firstActive secondActive sameThread
        rw [stepsNext] at isFirst isSecond
        rcases List.mem_append.mp isFirst with firstOld | firstNew <;>
          rcases List.mem_append.mp isSecond with secondOld | secondNew
        · exact related.activeStepsHaveDistinctThreads first firstOld second secondOld firstActive
            secondActive sameThread
        · have isCreated : second = createdRecord run move origin := by simpa using secondNew
          rw [isCreated, createdThread] at sameThread
          exact absurd sameThread (oldThread first firstOld firstActive)
        · have isCreated : first = createdRecord run move origin := by simpa using firstNew
          rw [isCreated, createdThread] at sameThread
          exact absurd sameThread.symm (oldThread second secondOld secondActive)
        · have firstCreated : first = createdRecord run move origin := by simpa using firstNew
          have secondCreated : second = createdRecord run move origin := by simpa using secondNew
          rw [firstCreated, secondCreated]
      invocationsAreActive := by
        intro record isRecord hasInvocation
        rw [stepsNext] at isRecord
        rcases List.mem_append.mp isRecord with isOld | isNew
        · exact related.invocationsAreActive record isOld hasInvocation
        · have isCreated : record = createdRecord run move origin := by simpa using isNew
          rw [isCreated]
          exact wp3b_createdRecord_isActive run move origin
      blockingOrder := by
        intro first second firstRecord secondRecord firstOn firstSince secondOn secondSince
          firstWait secondWait isFirst isSecond isFirstRecord isSecondRecord firstActive
          secondActive firstCarries secondCarries firstBlocked secondBlocked firstHasWait
          secondHasWait
        have firstOld := blockedIsOld first isFirst firstOn firstSince firstBlocked
        have secondOld := blockedIsOld second isSecond secondOn secondSince secondBlocked
        exact related.blockingOrder first second firstRecord secondRecord firstOn firstSince
          secondOn secondSince firstWait secondWait firstOld secondOld
          (carrierIsOld first firstOld firstRecord isFirstRecord firstCarries)
          (carrierIsOld second secondOld secondRecord isSecondRecord secondCarries) firstActive
          secondActive firstCarries secondCarries firstBlocked secondBlocked firstHasWait
          secondHasWait
      tasksAreCurrent := by
        intro task isTask
        rw [sameTasks] at isTask
        have wasCurrent := related.tasksAreCurrent task isTask
        unfold Dex.Config.isTaskCurrent at wasCurrent ⊢
        rw [hasRun] at wasCurrent
        rw [hasRunNext]
        simp only at wasCurrent ⊢
        cases foundCase : run.findStep? task.step with
        | none => simp [foundCase] at wasCurrent
        | some record =>
          simp only [foundCase, decide_eq_true_eq] at wasCurrent
          have foundNext : (run.newStep move.stepType move.input move.options origin).findStep?
              task.step = some record := by
            unfold Dex.Run.findStep? at foundCase ⊢
            rw [stepsNext, List.find?_append, foundCase]
            rfl
          simp [foundNext, wasCurrent] }

/-- The relation carries over to an L configuration with the same shared
state and the same threads, plus at most one halted thread, which has no
Step; and to a Dex configuration with the same run and tasks. -/
theorem wp3b_addHalted (program : Program) (lConfig lNext : L.Config)
    (dexConfig dexNext : Dex.Config signature) (run : Dex.Run signature)
    (related : RunningRelated program lConfig dexConfig run)
    (noBoot : ∀ other, other ∈ lConfig.threads →
      other.phase ≠ .bootPending ∧ other.phase ≠ .bootRunning)
    (extra : Option L.Thread) (sameData : SameData lConfig lNext)
    (sameCounter : lNext.timerCounter = lConfig.timerCounter)
    (threadsNext : ∀ other, other ∈ lNext.threads ↔ other ∈ lConfig.threads ∨ extra = some other)
    (nodupNext : (lNext.threads.map (fun other => other.core.tid)).Nodup)
    (extraHalted : ∀ thread, extra = some thread → thread.phase = .halted ∧
      (lConfig.cancelFlags.contains thread.core.tid = true →
        run.pendingMessages (Chan.cancelRequest thread.core.tid) ≠ []) ∧
      ∀ target, lConfig.cancelFlags.contains target = true →
        lConfig.findThread? target = none →
        run.pendingMessages (Chan.cancelRequest target) = [] →
        ∀ index, thread.core.freshCounter ≤ index → ¬ (thread.core.tid ++ [index]) <+: target)
    (sameTasks : dexNext.tasks = dexConfig.tasks) (sameRun : dexNext.run = dexConfig.run) :
    RunningRelated program lNext dexNext run := by
  have liveIsOld : ∀ other, other ∈ lNext.threads → other.phase ≠ .halted →
      other ∈ lConfig.threads := by
    intro other isOther isLive
    rcases (threadsNext other).mp isOther with isOld | isNew
    · exact isOld
    · exact absurd (extraHalted other isNew).1 isLive
  have findOld : ∀ target current, lConfig.findThread? target = some current →
      lNext.findThread? target = some current := by
    intro target current isFound
    obtain ⟨isCurrent, hasId⟩ := wp3b_findThread?_some lConfig target current isFound
    rw [← hasId]
    exact wp3b_findThread?_of_mem lNext current nodupNext
      ((threadsNext current).mpr (Or.inl isCurrent))
  exact
    { shared := by
        intro name
        have sameRead : lNext.readShared name = lConfig.readShared name := by
          simp [L.Config.readShared, sameData.shared]
        rw [sameRead]
        exact related.shared name
      cvQueues := by
        intro cv
        rw [sameData.cvQueues]
        exact related.cvQueues cv
      inputQueues := by
        intro chan
        have sameInput : lNext.pendingInput chan = lConfig.pendingInput chan := by
          simp [L.Config.pendingInput, sameData.inputQueues]
        rw [sameInput]
        exact related.inputQueues chan
      mutexTokens := by
        intro noBootNext mutex
        rw [sameData.mutexOwners]
        exact related.mutexTokens (fun other isOther => noBoot other isOther) mutex
      mutexesBeforeBoot := by
        intro other isOther isBoot
        exfalso
        rcases (threadsNext other).mp isOther with isOld | isCreated
        · rcases isBoot with isPending | isRunning
          · exact (noBoot other isOld).1 isPending
          · exact (noBoot other isOld).2 isRunning
        · rw [(extraHalted other isCreated).1] at isBoot
          simp at isBoot
      joinTokens := by
        intro joined
        rw [sameData.joinable]
        exact related.joinTokens joined
      cancelRequestsAreFlags := by
        intro target hasRequest
        rw [sameData.cancelFlags]
        exact related.cancelRequestsAreFlags target hasRequest
      cancelFlagsAreRequests := by
        intro target isFlagged
        rw [sameData.cancelFlags] at isFlagged
        rcases related.cancelFlagsAreRequests target isFlagged with hasRequest |
          ⟨current, isFound, isCanceled⟩ | ⟨isGone, isNoChild⟩
        · exact Or.inl hasRequest
        · exact Or.inr (Or.inl ⟨current, findOld target current isFound, isCanceled⟩)
        · by_cases isExtra : ∃ other, extra = some other ∧ other.core.tid = target
          · obtain ⟨other, isOther, hasId⟩ := isExtra
            left
            rw [← hasId]
            exact (extraHalted other isOther).2.1 (by rw [hasId]; exact isFlagged)
          · by_cases isEmpty : run.pendingMessages (Chan.cancelRequest target) = []
            · refine Or.inr (Or.inr ⟨?_, ?_⟩)
              · apply (wp3b_findThread?_none _ _).mpr
                intro other isOther hasId
                rcases (threadsNext other).mp isOther with isOld | isNew
                · exact (wp3b_findThread?_none _ _).mp isGone other isOld hasId
                · exact isExtra ⟨other, isNew, hasId⟩
              · intro live isLive index isAbove
                rcases (threadsNext live).mp isLive with isOld | isNew
                · exact isNoChild live isOld index isAbove
                · exact (extraHalted live isNew).2.2 target isFlagged isGone isEmpty index isAbove
            · exact Or.inl isEmpty
      cvWakes := by
        intro cv waiter
        rw [sameData.cvWakes]
        exact related.cvWakes cv waiter
      timers := by
        intro timer fireTime
        constructor
        · intro isArmed
          obtain ⟨other, chan, since, isOther, isBlocked, isTimed, isIndex⟩ :=
            (related.timers timer fireTime).mp isArmed
          exact ⟨other, chan, since, (threadsNext other).mpr (Or.inl isOther), isBlocked, isTimed,
            isIndex⟩
        · rintro ⟨other, chan, since, isOther, isBlocked, isTimed, isIndex⟩
          exact (related.timers timer fireTime).mpr ⟨other, chan, since,
            liveIsOld other isOther (by simp [isBlocked]), isBlocked, isTimed, isIndex⟩
      timerCounter := by
        rw [sameCounter]
        exact related.timerCounter
      outputsEmpty := related.outputsEmpty
      threadsHaveSteps := by
        intro other isOther isLive
        obtain ⟨record, isRecord, isActive, otherCarries, isMatch⟩ :=
          related.threadsHaveSteps other (liveIsOld other isOther isLive) isLive
        refine ⟨record, isRecord, isActive, otherCarries, ?_⟩
        exact wp3b_phaseMatches_congr program lConfig lNext dexConfig dexNext other record
          (fun task isTask _ => by rw [sameTasks]; exact isTask)
          (fun _ _ cv _ => by rw [sameData.cvQueues]) isMatch
      stepsHaveThreads := by
        intro record isRecord isActive
        obtain ⟨other, isOther, isLive, otherCarries⟩ :=
          related.stepsHaveThreads record isRecord isActive
        exact ⟨other, (threadsNext other).mpr (Or.inl isOther), isLive, otherCarries⟩
      activeStepsHaveDistinctThreads := related.activeStepsHaveDistinctThreads
      invocationsAreActive := related.invocationsAreActive
      blockingOrder := by
        intro first second firstRecord secondRecord firstOn firstSince secondOn secondSince
          firstWait secondWait isFirst isSecond isFirstRecord isSecondRecord firstActive
          secondActive firstCarries secondCarries firstBlocked secondBlocked firstHasWait
          secondHasWait
        exact related.blockingOrder first second firstRecord secondRecord firstOn firstSince
          secondOn secondSince firstWait secondWait
          (liveIsOld first isFirst (by simp [firstBlocked]))
          (liveIsOld second isSecond (by simp [secondBlocked])) isFirstRecord isSecondRecord
          firstActive secondActive firstCarries secondCarries firstBlocked secondBlocked
          firstHasWait secondHasWait
      tasksAreCurrent := by
        intro task isTask
        rw [sameTasks] at isTask
        have wasCurrent := related.tasksAreCurrent task isTask
        unfold Dex.Config.isTaskCurrent at wasCurrent ⊢
        rw [sameRun]
        exact wasCurrent }

end DexExpressiveness
