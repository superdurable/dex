import DexExpressiveness.Simulation.LToDexBasicWake

/-!
# Helper lemmas of work package 2: a batch of timer fires

L fires the least due timer, `count` times in a row, and recomputes the least
due timer each time. [TIMER-FIRE] fires the first `count` timers of one
sorted list. This file shows that the two agree, one fire at a time:

- the sorted list of due timers is strictly sorted (`wp2_sortStable_pairwise`);
- [TIMER-FIRE] of one armed timer of a waiting `Timed` Step
  (`wp2_fireTimer_eq`);
- the L invariant after a fire (`wp2_invariant_wake_fired`).

Every lemma is prefixed `wp2_`.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Stable sort: a sorted permutation -/

/-- Inserting an element adds it. -/
theorem wp2_insertSorted_perm {α : Type} (isAtMost : α → α → Bool) (newElement : α)
    (elements : List α) : (insertSorted isAtMost newElement elements).Perm (newElement :: elements) := by
  induction elements with
  | nil => exact List.Perm.refl _
  | cons head rest inductionHypothesis =>
    simp only [insertSorted]
    split
    · exact List.Perm.refl _
    · exact (inductionHypothesis.cons head).trans (List.Perm.swap _ _ _)

/-- Sorting permutes. -/
theorem wp2_sortStable_perm {α : Type} (isAtMost : α → α → Bool) (elements : List α) :
    (sortStable isAtMost elements).Perm elements := by
  induction elements with
  | nil => exact List.Perm.refl _
  | cons head rest inductionHypothesis =>
    simp only [sortStable]
    exact (wp2_insertSorted_perm isAtMost head _).trans (inductionHypothesis.cons head)

/-- Inserting into a sorted list keeps it sorted, for a total preorder. -/
theorem wp2_insertSorted_pairwise {α : Type} (isAtMost : α → α → Bool)
    (isTotal : ∀ first second, isAtMost first second = true ∨ isAtMost second first = true)
    (isTransitive : ∀ first second third, isAtMost first second = true →
      isAtMost second third = true → isAtMost first third = true)
    (newElement : α) (elements : List α)
    (isSorted : elements.Pairwise (fun first second => isAtMost first second = true)) :
    (insertSorted isAtMost newElement elements).Pairwise
      (fun first second => isAtMost first second = true) := by
  induction elements with
  | nil => simp [insertSorted]
  | cons head rest inductionHypothesis =>
    obtain ⟨headIsLeast, restIsSorted⟩ := List.pairwise_cons.mp isSorted
    simp only [insertSorted]
    split
    · rename_i isBeforeHead
      refine List.pairwise_cons.mpr ⟨?_, isSorted⟩
      intro other isOther
      rcases List.mem_cons.mp isOther with isHead | isInRest
      · rw [isHead]
        exact isBeforeHead
      · exact isTransitive _ _ _ isBeforeHead (headIsLeast other isInRest)
    · rename_i isNotBeforeHead
      refine List.pairwise_cons.mpr ⟨?_, inductionHypothesis restIsSorted⟩
      intro other isOther
      rcases (mem_insertSorted isAtMost newElement other rest).mp isOther with isNew | isInRest
      · rw [isNew]
        rcases isTotal newElement head with isBefore | isAfter
        · exact absurd isBefore isNotBeforeHead
        · exact isAfter
      · exact headIsLeast other isInRest

/-- The result of sorting by a total preorder is sorted. -/
theorem wp2_sortStable_pairwise {α : Type} (isAtMost : α → α → Bool)
    (isTotal : ∀ first second, isAtMost first second = true ∨ isAtMost second first = true)
    (isTransitive : ∀ first second third, isAtMost first second = true →
      isAtMost second third = true → isAtMost first third = true)
    (elements : List α) :
    (sortStable isAtMost elements).Pairwise (fun first second => isAtMost first second = true) := by
  induction elements with
  | nil => simp [sortStable]
  | cons head rest inductionHypothesis =>
    simp only [sortStable]
    exact wp2_insertSorted_pairwise isAtMost isTotal isTransitive head _ inductionHypothesis

/-! ## Association lists with distinct keys -/

/-- With distinct keys, a binding is in the list exactly when lookup finds it. -/
theorem wp2_mem_iff_get? {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν)
    (keysNodup : (map.map (fun entry => entry.1)).Nodup) (key : κ) (value : ν) :
    (key, value) ∈ map ↔ AssocList.get? map key = some value := by
  induction map with
  | nil => simp [AssocList.get?]
  | cons entry rest inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    simp only [List.map_cons, List.nodup_cons, List.mem_map] at keysNodup
    obtain ⟨isFresh, restNodup⟩ := keysNodup
    by_cases isKey : entryKey = key
    · subst isKey
      simp only [List.mem_cons, Prod.mk.injEq, true_and, AssocList.get?, ↓reduceIte,
        Option.some.injEq]
      constructor
      · rintro (isValue | isInRest)
        · exact isValue.symm
        · exact absurd ⟨(entryKey, value), isInRest, rfl⟩ isFresh
      · intro isValue
        exact Or.inl isValue.symm
    · simp only [List.mem_cons, Prod.mk.injEq, AssocList.get?, isKey, ↓reduceIte]
      rw [← inductionHypothesis restNodup]
      constructor
      · rintro (⟨isSameKey, _⟩ | isInRest)
        · exact absurd isSameKey.symm isKey
        · exact isInRest
      · intro isInRest
        exact Or.inr isInRest

/-! ## [TIMER-FIRE] of one timer -/

/-- The run after the armed timer of a waiting `Timed` Step fired: the timer
condition is done, the wait is decided, and Execute is pending. -/
def wp2_firedRun (run : Dex.Run signature) (timer : Dex.TimerId signature)
    (waitState : Dex.WaitState signature) : Dex.Run signature :=
  (((({ run with timers := run.timers.erase timer } : Dex.Run signature).updateStep timer.step
    (fun current =>
      { current with waitState := some { waitState with timersDone := [true] } })
    ).disarmTimersOf timer.step)).updateStep timer.step (fun current =>
      { current with
        conditionResults := some
          { timersDone := [true]
            channelMessages := waitState.wait.channels.map (fun _ => none) }
        phase := .execute
        invocation := some { method := .execute, state := .pending } })

/-- [TIMER-FIRE] of the one timer of an AnyOf wait that has not fired. -/
theorem wp2_fireTimer_eq (run : Dex.Run signature) (timer : Dex.TimerId signature)
    (record : Dex.StepRecord signature) (waitState : Dex.WaitState signature)
    (isArmed : run.timers.contains timer = true)
    (isFound : run.findStep? timer.step = some record) (isWaiting : record.phase = .waiting)
    (hasWait : record.waitState = some waitState) (isAny : waitState.wait.kind = .any)
    (hasTimer : waitState.wait.timerSeconds ≠ []) (isNotDone : waitState.timersDone = [false])
    (isIndex : timer.index = 0) :
    run.fireTimer timer = wp2_firedRun run timer waitState := by
  have isFoundDisarmed :
      ({ run with timers := run.timers.erase timer } : Dex.Run signature).findStep? timer.step =
        some record := isFound
  have isFoundMarked := Dex.Run.findStep?_updateStep_self
    ({ run with timers := run.timers.erase timer } : Dex.Run signature) timer.step
    (fun current => { current with waitState := some { waitState with timersDone := [true] } })
    record isFoundDisarmed rfl
  have isPlanned : Dex.Run.plan ({ waitState with timersDone := [true] } :
      Dex.WaitState signature).wait
      (fun chan => ((({ run with timers := run.timers.erase timer } : Dex.Run signature).updateStep
        timer.step (fun current =>
          { current with waitState := some { waitState with timersDone := [true] } })
        ).pendingMessages chan).length) [true] =
      some (waitState.wait.channels.map (fun _ => false)) := by
    have hasCondition : waitState.wait.hasNoCondition = false := by
      cases timerCase : waitState.wait.timerSeconds with
      | nil => exact absurd timerCase hasTimer
      | cons seconds laterSeconds => simp [Dex.Wait.hasNoCondition, timerCase]
    unfold Dex.Run.plan
    simp [hasCondition, isAny]
  have isEval := Dex.Run.evalStep_of_plan_some _ timer.step _
    ({ waitState with timersDone := [true] } : Dex.WaitState signature)
    (waitState.wait.channels.map (fun _ => false)) isFoundMarked isWaiting rfl isPlanned
  rw [Dex.Run.consumeMessages_none] at isEval
  unfold Dex.Run.fireTimer
  simp only [isArmed, ↓reduceIte, isFoundDisarmed, isWaiting, hasWait, isNotDone, isIndex]
  exact isEval

/-! ## The least due timer of L -/

/-- The due timers of L: deadline, timer number, and thread. -/
def wp2_dueCandidates (config : L.Config) : List (Nat × Nat × Tid) :=
  config.threads.filterMap (fun thread =>
    match thread.phase with
    | .blocked (.timed number deadline _) _ =>
      if deadline ≤ config.clock then some (deadline, number, thread.core.tid) else none
    | _ => none)

/-- The order `(deadline, n)` of spec §8.1. -/
def wp2_isDueAtMost (first second : Nat × Nat × Tid) : Bool :=
  decide (first.1 < second.1) ||
    (decide (first.1 = second.1) && decide (first.2.1 ≤ second.2.1))

/-- `leastDueTimerThread` is the first due candidate in the order `(deadline, n)`. -/
theorem wp2_leastDueTimerThread_eq (config : L.Config) :
    config.leastDueTimerThread =
      (sortStable wp2_isDueAtMost (wp2_dueCandidates config)).head?.map
        (fun entry => entry.2.2) := by
  simp only [L.Config.leastDueTimerThread, wp2_dueCandidates]
  congr 3

/-- The due candidates are the threads whose armed timer is due. -/
theorem wp2_mem_dueCandidates (config : L.Config) (deadline number : Nat) (tid : Tid) :
    (deadline, number, tid) ∈ wp2_dueCandidates config ↔
      ∃ thread chan since, thread ∈ config.threads ∧
        thread.phase = .blocked (.timed number deadline chan) since ∧
        deadline ≤ config.clock ∧ thread.core.tid = tid := by
  simp only [wp2_dueCandidates, List.mem_filterMap]
  constructor
  · rintro ⟨thread, isMember, isCandidate⟩
    cases phaseCase : thread.phase with
    | blocked on since =>
      cases on with
      | timed threadNumber threadDeadline chan =>
        simp only [phaseCase] at isCandidate
        split at isCandidate
        · rename_i isDue
          simp only [Option.some.injEq, Prod.mk.injEq] at isCandidate
          obtain ⟨rfl, rfl, rfl⟩ := isCandidate
          exact ⟨thread, chan, since, isMember, phaseCase, isDue, rfl⟩
        · cases isCandidate
      | lock mutex => simp [phaseCase] at isCandidate
      | recv chan => simp [phaseCase] at isCandidate
      | join joined => simp [phaseCase] at isCandidate
      | park cv => simp [phaseCase] at isCandidate
    | bootPending => simp [phaseCase] at isCandidate
    | bootRunning => simp [phaseCase] at isCandidate
    | ready => simp [phaseCase] at isCandidate
    | running loaded => simp [phaseCase] at isCandidate
    | emitting effect => simp [phaseCase] at isCandidate
    | emitted => simp [phaseCase] at isCandidate
    | waitStarted => simp [phaseCase] at isCandidate
    | woken outcome => simp [phaseCase] at isCandidate
    | resuming outcome => simp [phaseCase] at isCandidate
    | halted => simp [phaseCase] at isCandidate
  · rintro ⟨thread, chan, since, isMember, isBlocked, isDue, rfl⟩
    exact ⟨thread, isMember, by simp [isBlocked, isDue]⟩

/-! ## The L invariant after a fire -/

/-- Waking a thread that is blocked in a timed operation, by its timer, keeps
the L invariant. -/
theorem wp2_invariant_wake_fired {program : Program} {config : L.Config}
    (invariant : L.Invariant program config) {tid : Tid} {thread : L.Thread}
    (isFound : config.findThread? tid = some thread) {number deadline since : Nat}
    {chan : Option ExtChan}
    (isBlocked : thread.phase = .blocked (.timed number deadline chan) since) :
    L.Invariant program (config.setPhase tid (.woken .fired)) := by
  obtain ⟨isMember, hasTid⟩ := mem_of_findThread? isFound
  have memNew := mem_setPhase invariant isFound (.woken .fired)
  -- Every thread keeps its core, code, and timer.
  have origin : ∀ candidate, candidate ∈ (config.setPhase tid (.woken .fired)).threads →
      ∃ original, original ∈ config.threads ∧ candidate.core = original.core ∧
        candidate.code = original.code ∧ candidate.timer = original.timer ∧
        (candidate = original ∨ candidate.phase = .woken .fired) := by
    intro candidate isCandidate
    rcases (memNew candidate).mp isCandidate with ⟨isOld, _⟩ | isNew
    · exact ⟨candidate, isOld, rfl, rfl, rfl, Or.inl rfl⟩
    · subst isNew
      exact ⟨thread, isMember, rfl, rfl, rfl, Or.inr rfl⟩
  have sameTids : (config.setPhase tid (.woken .fired)).threads.map (fun current => current.core.tid) =
      config.threads.map (fun current => current.core.tid) := by
    simp only [L.Config.setPhase, L.Config.updateThread, List.map_map]
    apply List.map_congr_left
    intro current _
    by_cases isChanged : current.core.tid = tid <;> simp [isChanged]
  exact
    { notStartedIsInitial := fun isNotStarted => by
        obtain ⟨noThread, rest⟩ := invariant.notStartedIsInitial isNotStarted
        exact ⟨by simp [L.Config.setPhase, L.Config.updateThread, noThread], rest⟩
      closedHasNoThread := fun isClosed => by
        simp [L.Config.setPhase, L.Config.updateThread, invariant.closedHasNoThread isClosed]
      threadIdsNodup := by
        rw [sameTids]
        exact invariant.threadIdsNodup
      threadIdsFresh := fun first isFirst second isSecond index isFresh => by
        obtain ⟨firstOld, isFirstOld, firstCore, _⟩ := origin first isFirst
        obtain ⟨secondOld, isSecondOld, secondCore, _⟩ := origin second isSecond
        rw [firstCore, secondCore]
        exact invariant.threadIdsFresh firstOld isFirstOld secondOld isSecondOld index
          (by rw [← firstCore]; exact isFresh)
      joinableNodup := invariant.joinableNodup
      joinableFresh := fun candidate isCandidate => by
        obtain ⟨original, isOriginal, sameCore, _⟩ := origin candidate isCandidate
        rw [sameCore]
        exact invariant.joinableFresh original isOriginal
      mutexesFresh := fun candidate isCandidate => by
        obtain ⟨original, isOriginal, sameCore, _⟩ := origin candidate isCandidate
        rw [sameCore]
        exact invariant.mutexesFresh original isOriginal
      handlesExist := by
        refine ⟨?_, invariant.handlesExist.2.1, invariant.handlesExist.2.2⟩
        intro candidate isCandidate
        obtain ⟨original, isOriginal, sameCore, sameCode, _, samePhase⟩ :=
          origin candidate isCandidate
        obtain ⟨validLocals, validStack, validCode, validPhase⟩ :=
          invariant.handlesExist.1 original isOriginal
        refine ⟨?_, ?_, ?_, ?_⟩
        · rw [sameCore]
          exact validLocals
        · rw [sameCore]
          exact validStack
        · rw [sameCode]
          exact validCode
        · rcases samePhase with isSame | isWoken
          · rw [isSame]
            exact validPhase
          · rw [isWoken]
            trivial
      bootIsInitial := fun candidate isCandidate isBoot => by
        rcases (memNew candidate).mp isCandidate with ⟨isOld, otherTid⟩ | isNew
        · obtain ⟨isOnly, _⟩ := invariant.bootIsInitial candidate isOld isBoot
          rw [isOnly] at isMember
          rw [List.mem_singleton.mp isMember] at hasTid
          exact absurd hasTid otherTid
        · subst isNew
          rcases isBoot with isBoot | isBoot <;> cases isBoot
      sinceBelowCounter := fun candidate isCandidate on candidateSince isCandidateBlocked => by
        rcases (memNew candidate).mp isCandidate with ⟨isOld, _⟩ | isNew
        · exact invariant.sinceBelowCounter candidate isOld on candidateSince isCandidateBlocked
        · subst isNew
          cases isCandidateBlocked
      sinceInjective := fun first isFirst second isSecond firstOn secondOn sharedSince
          isFirstBlocked isSecondBlocked => by
        rcases (memNew first).mp isFirst with ⟨isFirstOld, _⟩ | isNew
        · rcases (memNew second).mp isSecond with ⟨isSecondOld, _⟩ | isNew
          · exact invariant.sinceInjective first isFirstOld second isSecondOld firstOn secondOn
              sharedSince isFirstBlocked isSecondBlocked
          · subst isNew
            cases isSecondBlocked
        · subst isNew
          cases isFirstBlocked
      timerNumbersCounted := fun candidate isCandidate timerNumber seconds hasTimer => by
        obtain ⟨original, isOriginal, _, _, sameTimer, _⟩ := origin candidate isCandidate
        rw [sameTimer] at hasTimer
        exact invariant.timerNumbersCounted original isOriginal timerNumber seconds hasTimer
      timerNumbersInjective := fun first isFirst second isSecond timerNumber firstSeconds
          secondSeconds hasFirstTimer hasSecondTimer => by
        rcases (memNew first).mp isFirst with ⟨isFirstOld, firstTid⟩ | isFirstNew
        · rcases (memNew second).mp isSecond with ⟨isSecondOld, _⟩ | isSecondNew
          · exact invariant.timerNumbersInjective first isFirstOld second isSecondOld timerNumber
              firstSeconds secondSeconds hasFirstTimer hasSecondTimer
          · subst isSecondNew
            have isThread := invariant.timerNumbersInjective first isFirstOld thread isMember
              timerNumber firstSeconds secondSeconds hasFirstTimer hasSecondTimer
            rw [isThread] at firstTid
            exact absurd hasTid firstTid
        · rcases (memNew second).mp isSecond with ⟨isSecondOld, secondTid⟩ | isSecondNew
          · subst isFirstNew
            have isThread := invariant.timerNumbersInjective second isSecondOld thread isMember
              timerNumber secondSeconds firstSeconds hasSecondTimer hasFirstTimer
            rw [isThread] at secondTid
            exact absurd hasTid secondTid
          · rw [isFirstNew, isSecondNew]
      phasesFitCode := fun candidate isCandidate => by
        rcases (memNew candidate).mp isCandidate with ⟨isOld, _⟩ | isNew
        · exact invariant.phasesFitCode candidate isOld
        · subst isNew
          obtain ⟨⟨seconds, hasTimer⟩, _⟩ :=
            wp2_timer_of_blocked_timed invariant isMember isBlocked
          unfold L.Thread.PhaseFitsCode
          simp [hasTimer]
      runningHoldsRequiredMutex := fun candidate isCandidate loaded isRunning => by
        rcases (memNew candidate).mp isCandidate with ⟨isOld, _⟩ | isNew
        · exact invariant.runningHoldsRequiredMutex candidate isOld loaded isRunning
        · subst isNew
          cases isRunning }

/-! ## The run during [TIMER-FIRE] -/

/-- [TIMER-FIRE] of one timer keeps the status. -/
theorem wp2_status_fireTimer (run : Dex.Run signature) (timer : Dex.TimerId signature) :
    (run.fireTimer timer).status = run.status := by
  unfold Dex.Run.fireTimer
  split
  · dsimp only
    split
    · rfl
    · split
      · split
        · rfl
        · rw [(Dex.Run.evalStep_keeps_data _ _).2.1]
          rfl
      · rfl
  · rfl

/-- [TIMER-FIRE] of several timers keeps the status. -/
theorem wp2_status_foldl_fireTimer (timers : List (Dex.TimerId signature))
    (run : Dex.Run signature) : (timers.foldl Dex.Run.fireTimer run).status = run.status := by
  induction timers generalizing run with
  | nil => rfl
  | cons timer laterTimers inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [inductionHypothesis, wp2_status_fireTimer]

/-- The timers after a fire: the timers of every other Step. -/
theorem wp2_firedRun_timers (run : Dex.Run signature) (timer : Dex.TimerId signature)
    (waitState : Dex.WaitState signature) (other : Dex.TimerId signature) (fireTime : Nat) :
    (wp2_firedRun run timer waitState).timers.get? other = some fireTime ↔
      run.timers.get? other = some fireTime ∧ other.step ≠ timer.step := by
  have hasTimers : (wp2_firedRun run timer waitState).timers =
      (run.timers.erase timer).filter (fun entry => !decide (entry.1.step = timer.step)) := rfl
  rw [hasTimers]
  by_cases isTimerStep : other.step = timer.step
  · have isRejected := AssocList.get?_filter_of_rejected
      (fun (key : Dex.TimerId signature) => !decide (key.step = timer.step))
      (run.timers.erase timer) other (by simp [isTimerStep])
    rw [isRejected]
    constructor
    · intro isArmed
      cases isArmed
    · intro isArmed
      exact absurd isTimerStep isArmed.2
  · have isKept := AssocList.get?_filter_of_kept
      (fun (key : Dex.TimerId signature) => !decide (key.step = timer.step))
      (run.timers.erase timer) other (by simp [isTimerStep])
    rw [isKept, AssocList.get?_erase_other _ _ _ (fun isTimer => isTimerStep (by rw [isTimer]))]
    exact ⟨fun isArmed => ⟨isArmed, isTimerStep⟩, fun isArmed => isArmed.1⟩

/-- The index order of two armed timers of a translated Flow: `(fire time,
Step number)`. -/
theorem wp2_isTimerEntryAtMost_iff (first second : Dex.TimerId signature × Nat) :
    Dex.Run.isTimerEntryAtMost first second = true ↔
      first.2 < second.2 ∨
      (first.2 = second.2 ∧ first.1.step.number ≤ second.1.step.number) := by
  simp [Dex.Run.isTimerEntryAtMost]

/-- The configuration during a [TIMER-FIRE] commit whose run so far is `run`. -/
def wp2_firingConfig (dexConfig : Dex.Config signature) (run : Dex.Run signature) :
    Dex.Config signature :=
  { dexConfig with run := some run, lastCommitAt := some dexConfig.clock }

/-- One fire: the armed timer belongs to a blocked thread, [TIMER-FIRE] of it
is `wp2_firedRun`, and waking the thread by its timer is related to it. -/
theorem wp2_fire_one {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature} {seqBound : Nat}
    (lInvariant : L.Invariant program lConfig)
    (runInvariant : Dex.Run.Invariant IsTimedTimer seqBound run)
    (related : RunningRelated program lConfig (wp2_firingConfig dexConfig run) run)
    (timer : Dex.TimerId signature) (fireTime : Nat)
    (isArmed : run.timers.get? timer = some fireTime) :
    ∃ thread chan since waitState, thread ∈ lConfig.threads ∧
      thread.phase = .blocked (.timed timer.step.number fireTime chan) since ∧
      timer.step.stepType = StepType.timed ∧ timer.index = 0 ∧
      run.fireTimer timer = wp2_firedRun run timer waitState ∧
      RunningRelated program (lConfig.setPhase thread.core.tid (.woken .fired))
        (wp2_firingConfig dexConfig (wp2_firedRun run timer waitState))
        (wp2_firedRun run timer waitState) := by
  obtain ⟨thread, chan, since, isMember, isBlocked, isTimed, isIndex⟩ :=
    (related.timers timer fireTime).mp isArmed
  have isFound := findThread?_of_mem lInvariant isMember
  obtain ⟨⟨seconds, hasTimer⟩, _⟩ := wp2_timer_of_blocked_timed lInvariant isMember isBlocked
  obtain ⟨record, isRecord, isActive, carries, isMatch⟩ :=
    related.threadsHaveSteps thread isMember (by simp [isBlocked])
  have isNotBoot : isBootPhase thread.phase = false := by rw [isBlocked]; rfl
  have isRecordId := wp2_record_of_timer carries isNotBoot hasTimer
  have isStep : timer.step = record.id := by
    rw [isRecordId]
    cases stepCase : timer.step with
    | mk stepType stepNumber =>
      rw [stepCase] at isTimed
      simp only at isTimed
      rw [isTimed]
  unfold PhaseMatchesStep at isMatch
  simp only [isBlocked] at isMatch
  obtain ⟨isWaiting, hasNoInvocation, waitState, hasWait, isKind, hasLength, _, hasTimersDone⟩ :=
    isMatch
  have isAny : waitState.wait.kind = .any := by
    rw [isKind]
    cases chan <;> rfl
  have hasOneTimer : waitState.wait.timerSeconds.length = 1 := by
    rw [hasLength]
    cases chan <;> rfl
  obtain ⟨timerSeconds, isOneTimer⟩ := List.length_eq_one_iff.mp hasOneTimer
  have isNotDone : waitState.timersDone = [false] := by
    rw [hasTimersDone, isOneTimer]
    rfl
  have isFoundRecord : run.findStep? timer.step = some record := by
    rw [isStep]
    exact Dex.Run.findStep?_of_mem_nodup run record isRecord runInvariant.idsNodup
  have isFire := wp2_fireTimer_eq run timer record waitState
    (by simp [AssocList.contains, isArmed]) isFoundRecord isWaiting hasWait isAny
    (by rw [isOneTimer]; simp) isNotDone isIndex
  refine ⟨thread, chan, since, waitState, isMember, isBlocked, isTimed, isIndex, isFire, ?_⟩
  let mark : Dex.StepRecord signature → Dex.StepRecord signature := fun candidate =>
    if candidate.id = timer.step then
      { candidate with waitState := some { waitState with timersDone := [true] } }
    else candidate
  let decided : Dex.StepRecord signature → Dex.StepRecord signature := fun candidate =>
    if candidate.id = timer.step then
      { candidate with
        conditionResults := some
          { timersDone := [true]
            channelMessages := waitState.wait.channels.map (fun _ => none) }
        phase := .execute
        invocation := some { method := .execute, state := .pending } }
    else candidate
  refine wp2_runningRelated_changeThread related lInvariant runInvariant.idsNodup isFound isRecord
    isActive carries (.woken .fired) (by rw [isBlocked]; rfl) (by simp) (by simp)
    (by simp [isBlocked]) (fun candidate => decided (mark candidate)) ?_ ?_ ?_ rfl (fun _ => rfl)
    ?_ rfl rfl (fun task isTask _ => isTask) ?_ ?_
  · show (run.steps.map mark).map decided = _
    rw [List.map_map]
    rfl
  · intro candidate isCandidate
    by_cases isChanged : candidate.id = timer.step
    · have sameRecord : candidate = record :=
        eq_of_nodup_map (fun step => step.id) run.steps runInvariant.idsNodup isCandidate isRecord
          (isChanged.trans isStep)
      subst sameRecord
      refine ⟨by simp [mark, decided, isChanged], by simp [mark, decided, isChanged],
        by simp [mark, decided, isChanged], ?_⟩
      simp only [mark, decided, isChanged, ↓reduceIte]
      rw [isActive]
      rfl
    · simp [mark, decided, isChanged]
  · intro candidate isOther
    have isOtherStep : candidate.id ≠ timer.step := by
      rw [isStep]
      exact isOther
    simp [mark, decided, isOtherStep]
  · apply wp2_timers_of_wake related lInvariant isFound carries isBlocked
    intro other otherTime
    have hasTimers := wp2_firedRun_timers run timer waitState other otherTime
    rw [isStep] at hasTimers
    exact hasTimers
  · have isImage : decided (mark record) =
        { record with
          waitState := some { waitState with timersDone := [true] }
          conditionResults := some
            { timersDone := [true]
              channelMessages := waitState.wait.channels.map (fun _ => none) }
          phase := .execute
          invocation := some { method := .execute, state := .pending } } := by
      simp [mark, decided, isStep]
    rw [isImage]
    unfold PhaseMatchesStep
    refine ⟨rfl, rfl, ?_⟩
    unfold decodeOutcome
    simp [hasTimer, decodeTimedOutcome]
  · intro task isTask
    have wasCurrent := related.tasksAreCurrent task isTask
    simp only [Dex.Config.isTaskCurrent, wp2_firingConfig] at wasCurrent ⊢
    by_cases sameStep : task.step = timer.step
    · rw [sameStep, isFoundRecord] at wasCurrent
      simp [hasNoInvocation] at wasCurrent
    · have outerFind : (wp2_firedRun run timer waitState).findStep? task.step =
          ((({ run with timers := run.timers.erase timer } : Dex.Run signature).updateStep
            timer.step (fun current =>
              { current with waitState := some { waitState with timersDone := [true] } })
            ).disarmTimersOf timer.step).findStep? task.step :=
        Dex.Run.findStep?_updateStep_other _ _ _ _ (fun _ => rfl) sameStep
      have innerFind : (({ run with timers := run.timers.erase timer } : Dex.Run signature
          ).updateStep timer.step (fun current =>
            { current with waitState := some { waitState with timersDone := [true] } })
          ).findStep? task.step = run.findStep? task.step :=
        Dex.Run.findStep?_updateStep_other
          ({ run with timers := run.timers.erase timer } : Dex.Run signature) _ _ _
          (fun _ => rfl) sameStep
      have sameFind : (wp2_firedRun run timer waitState).findStep? task.step =
          run.findStep? task.step := outerFind.trans innerFind
      rw [sameFind]
      exact wasCurrent

/-- A batch of fires, by induction on its length. `entries` is the list of
the due timers of the run with their fire times, strictly sorted, that
[TIMER-FIRE] works through; L recomputes its least due timer each time and
finds the head of the list. -/
theorem wp2_fire_batch (program : Program) (dexConfig : Dex.Config signature) (seqBound : Nat) :
    ∀ (count : Nat) (lConfig : L.Config) (run : Dex.Run signature)
      (entries : List (Dex.TimerId signature × Nat)) (lFinal : L.Config),
      L.Invariant program lConfig → Dex.Run.Invariant IsTimedTimer seqBound run →
      RunningRelated program lConfig (wp2_firingConfig dexConfig run) run →
      entries.Pairwise (fun first second => Dex.Run.isTimerEntryAtMost first second = true) →
      (entries.map (fun entry => entry.1)).Nodup →
      (∀ timer fireTime, (timer, fireTime) ∈ entries ↔
        run.timers.get? timer = some fireTime ∧ fireTime ≤ lConfig.clock) →
      lConfig.fireTimers count = some lFinal →
      count ≤ entries.length ∧ lFinal.clock = lConfig.clock ∧ lFinal.status = lConfig.status ∧
      RunningRelated program lFinal
        (wp2_firingConfig dexConfig
          (((entries.map (fun entry => entry.1)).take count).foldl Dex.Run.fireTimer run))
        (((entries.map (fun entry => entry.1)).take count).foldl Dex.Run.fireTimer run) := by
  intro count
  induction count with
  | zero =>
    intro lConfig run entries lFinal _ _ related _ _ _ isFired
    simp only [L.Config.fireTimers, Option.some.injEq] at isFired
    subst isFired
    exact ⟨Nat.zero_le _, rfl, rfl, by simpa using related⟩
  | succ count inductionHypothesis =>
    intro lConfig run entries lFinal lInvariant runInvariant related entriesPairwise
      entriesNodup entriesMem isFired
    cases entries with
    | nil =>
      have noCandidate : wp2_dueCandidates lConfig = [] := by
        apply List.eq_nil_iff_forall_not_mem.mpr
        rintro ⟨deadline, number, tid⟩ isCandidate
        obtain ⟨owner, chan, since, isOwner, isOwnerBlocked, isOwnerDue, _⟩ :=
          (wp2_mem_dueCandidates lConfig deadline number tid).mp isCandidate
        have isEntry := (entriesMem
          { step := { stepType := StepType.timed, number := number }, index := 0 } deadline).mpr
          ⟨(related.timers _ _).mpr ⟨owner, chan, since, isOwner, isOwnerBlocked, rfl, rfl⟩,
            isOwnerDue⟩
        cases isEntry
      simp [L.Config.fireTimers, wp2_leastDueTimerThread_eq, noCandidate, sortStable] at isFired
    | cons entry rest =>
      obtain ⟨timer, fireTime⟩ := entry
      obtain ⟨isArmed, isDue⟩ := (entriesMem timer fireTime).mp List.mem_cons_self
      obtain ⟨thread, chan, since, waitState, isMember, isBlocked, isTimed, isIndex, isFire,
        newRelated⟩ := wp2_fire_one lInvariant runInvariant related timer fireTime isArmed
      have isFound := findThread?_of_mem lInvariant isMember
      simp only [List.map_cons, List.nodup_cons] at entriesNodup
      obtain ⟨headFresh, restNodup⟩ := entriesNodup
      obtain ⟨headIsLeast, restPairwise⟩ := List.pairwise_cons.mp entriesPairwise
      -- A timer of a translated Flow is its Step number.
      have timerOfNumber : ∀ other : Dex.TimerId signature,
          other.step.stepType = StepType.timed → other.index = 0 →
          other.step.number = timer.step.number → other = timer := by
        intro other isOtherTimed isOtherIndex sameNumber
        obtain ⟨⟨otherType, otherNumber⟩, otherIndex⟩ := other
        obtain ⟨⟨timerType, timerNumber⟩, timerIndex⟩ := timer
        simp_all
      -- L finds the thread of the head timer.
      have isHead := head?_sortStable_of_least wp2_isDueAtMost
        (fireTime, timer.step.number, thread.core.tid) (wp2_dueCandidates lConfig)
        ((wp2_mem_dueCandidates lConfig _ _ _).mpr
          ⟨thread, chan, since, isMember, isBlocked, isDue, rfl⟩)
        (by simp [wp2_isDueAtMost])
        (by
          rintro ⟨otherDeadline, otherNumber, otherTid⟩ isOther
          obtain ⟨owner, ownerChan, ownerSince, isOwner, isOwnerBlocked, isOwnerDue, isOwnerTid⟩ :=
            (wp2_mem_dueCandidates lConfig _ _ _).mp isOther
          have isEntry := (entriesMem
            { step := { stepType := StepType.timed, number := otherNumber }, index := 0 }
            otherDeadline).mpr
            ⟨(related.timers _ _).mpr
              ⟨owner, ownerChan, ownerSince, isOwner, isOwnerBlocked, rfl, rfl⟩, isOwnerDue⟩
          rcases List.mem_cons.mp isEntry with isHeadEntry | isInRest
          · simp only [Prod.mk.injEq] at isHeadEntry
            obtain ⟨isTimer, isDeadline⟩ := isHeadEntry
            have sameNumber : otherNumber = timer.step.number := by rw [← isTimer]
            subst sameNumber
            subst isDeadline
            obtain ⟨⟨ownerSeconds, hasOwnerTimer⟩, _⟩ :=
              wp2_timer_of_blocked_timed lInvariant isOwner isOwnerBlocked
            obtain ⟨⟨seconds, hasTimer⟩, _⟩ :=
              wp2_timer_of_blocked_timed lInvariant isMember isBlocked
            have isThread : owner = thread :=
              lInvariant.timerNumbersInjective owner isOwner thread isMember _ ownerSeconds
                seconds hasOwnerTimer hasTimer
            rw [← isOwnerTid, isThread]
            exact Or.inl rfl
          · have isBefore := (wp2_isTimerEntryAtMost_iff _ _).mp (headIsLeast _ isInRest)
            simp only at isBefore
            have isOtherNumber : otherNumber ≠ timer.step.number := by
              intro sameNumber
              have isTimer := timerOfNumber
                { step := { stepType := StepType.timed, number := otherNumber }, index := 0 } rfl
                rfl sameNumber
              exact headFresh (List.mem_map.mpr ⟨_, isInRest, isTimer⟩)
            refine Or.inr ⟨?_, ?_⟩
            · simp only [wp2_isDueAtMost, Bool.or_eq_true, Bool.and_eq_true, decide_eq_true_eq]
              omega
            · simp only [wp2_isDueAtMost, Bool.or_eq_false_iff, Bool.and_eq_false_iff,
                decide_eq_false_iff_not]
              omega)
      have isLeast : lConfig.leastDueTimerThread = some thread.core.tid := by
        rw [wp2_leastDueTimerThread_eq, isHead]
        rfl
      simp only [L.Config.fireTimers, isLeast] at isFired
      have restMem : ∀ other otherTime, (other, otherTime) ∈ rest ↔
          (wp2_firedRun run timer waitState).timers.get? other = some otherTime ∧
          otherTime ≤ (lConfig.setPhase thread.core.tid (.woken .fired)).clock := by
        intro other otherTime
        rw [wp2_firedRun_timers]
        constructor
        · intro isInRest
          obtain ⟨isOtherArmed, isOtherDue⟩ :=
            (entriesMem other otherTime).mp (List.mem_cons_of_mem _ isInRest)
          refine ⟨⟨isOtherArmed, ?_⟩, isOtherDue⟩
          intro sameStep
          obtain ⟨_, _, _, _, _, isOtherTimed, isOtherIndex⟩ :=
            (related.timers other otherTime).mp isOtherArmed
          have isTimer := timerOfNumber other isOtherTimed isOtherIndex (by rw [sameStep])
          exact headFresh (List.mem_map.mpr ⟨(other, otherTime), isInRest, isTimer⟩)
        · rintro ⟨⟨isOtherArmed, isOtherStep⟩, isOtherDue⟩
          rcases List.mem_cons.mp ((entriesMem other otherTime).mpr ⟨isOtherArmed, isOtherDue⟩)
            with isHeadEntry | isInRest
          · simp only [Prod.mk.injEq] at isHeadEntry
            exact absurd (by rw [isHeadEntry.1]) isOtherStep
          · exact isInRest
      obtain ⟨isEnough, sameClock, sameStatus, finalRelated⟩ := inductionHypothesis
        (lConfig.setPhase thread.core.tid (.woken .fired)) (wp2_firedRun run timer waitState) rest
        lFinal (wp2_invariant_wake_fired lInvariant isFound isBlocked)
        (by rw [← isFire]; exact runInvariant.fireTimer timer) newRelated restPairwise restNodup
        restMem isFired
      refine ⟨by simpa using isEnough, sameClock, sameStatus, ?_⟩
      simp only [List.map_cons, List.take_succ_cons, List.foldl_cons, isFire]
      exact finalRelated

/-! ## [TIMER-FIRE] as one action -/

/-- The index order of the timers of a translated Flow is total. -/
theorem wp2_isTimerEntryAtMost_total (first second : Dex.TimerId signature × Nat) :
    Dex.Run.isTimerEntryAtMost first second = true ∨
      Dex.Run.isTimerEntryAtMost second first = true := by
  rw [wp2_isTimerEntryAtMost_iff, wp2_isTimerEntryAtMost_iff]
  omega

/-- The index order of the timers of a translated Flow is transitive. -/
theorem wp2_isTimerEntryAtMost_trans (first second third : Dex.TimerId signature × Nat)
    (isFirstBefore : Dex.Run.isTimerEntryAtMost first second = true)
    (isSecondBefore : Dex.Run.isTimerEntryAtMost second third = true) :
    Dex.Run.isTimerEntryAtMost first third = true := by
  rw [wp2_isTimerEntryAtMost_iff] at isFirstBefore isSecondBefore ⊢
  omega

/-- The relation at the start of a [TIMER-FIRE] commit, after `seq := seq + 1`. -/
theorem wp2_runningRelated_firing {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run) (hasRun : dexConfig.run = some run) :
    RunningRelated program lConfig (wp2_firingConfig dexConfig { run with seq := run.seq + 1 })
      { run with seq := run.seq + 1 } :=
  { shared := related.shared
    cvQueues := related.cvQueues
    inputQueues := related.inputQueues
    mutexTokens := related.mutexTokens
    mutexesBeforeBoot := related.mutexesBeforeBoot
    joinTokens := related.joinTokens
    cancelRequestsAreFlags := related.cancelRequestsAreFlags
    cancelFlagsAreRequests := related.cancelFlagsAreRequests
    cvWakes := related.cvWakes
    timers := related.timers
    timerCounter := related.timerCounter
    outputsEmpty := related.outputsEmpty
    threadsHaveSteps := fun thread isMember isNotHalted => by
      obtain ⟨record, isRecord, isActive, carries, isMatch⟩ :=
        related.threadsHaveSteps thread isMember isNotHalted
      exact ⟨record, isRecord, isActive, carries,
        phaseMatchesStep_transfer rfl (fun _ isTask _ => isTask) isMatch⟩
    stepsHaveThreads := related.stepsHaveThreads
    activeStepsHaveDistinctThreads := related.activeStepsHaveDistinctThreads
    invocationsAreActive := related.invocationsAreActive
    blockingOrder := related.blockingOrder
    tasksAreCurrent := fun task isTask => by
      have wasCurrent := related.tasksAreCurrent task isTask
      simp only [Dex.Config.isTaskCurrent, hasRun] at wasCurrent
      simp only [Dex.Config.isTaskCurrent, wp2_firingConfig]
      exact wasCurrent }

/-- [TIMER-FIRE] of the first `count` due timers, as one explicit action. -/
theorem wp2_exec_timerFire (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (run : Dex.Run signature) (hasRun : config.run = some run) (isRunning : run.status = .running)
    (canCommit : config.canCommit = true) (count : Nat) (isPositive : 0 < count)
    (isEnough : count ≤ (run.dueTimers config.clock).length) (newRun : Dex.Run signature)
    (isFire : ((run.dueTimers config.clock).take count).foldl Dex.Run.fireTimer
      { run with seq := run.seq + 1 } = newRun)
    (isStillRunning : newRun.status = .running) :
    (Dex.system flow).exec config [.timerFire count] = some ([], wp2_firingConfig config newRun) := by
  obtain ⟨status, outputs, steps, counters, attributes, channels, timers, seq⟩ := run
  simp only at isRunning
  subst isRunning
  subst isFire
  simp [TransitionSystem.exec, Dex.system, Dex.step, hasRun, canCommit, Dex.Config.commit,
    Dex.Config.closeEvents, isStillRunning, isPositive, isEnough, wp2_firingConfig]

/-- The named-sequence form of a batch of `fire` actions, under the
hypothesis that the keys of `run.timers` are distinct: one [TIMER-FIRE] of
the same number of timers, and the result is related.

The hypothesis is not part of `IsSimulationState` at the base commit of this
package, and `l_fire_simulated` is false without it. -/
theorem wp2_fire_exec (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (timerKeysNodup : ∀ run, dexConfig.run = some run →
      (run.timers.map (fun entry => entry.1)).Nodup)
    (count : Nat) (lFinal : L.Config) (isPositive : 0 < count)
    (canStamp : lConfig.canStamp = true) (isFired : lConfig.fireTimers count = some lFinal) :
    ∃ dexNext,
      (Dex.system ⟦program⟧).exec dexConfig [.timerFire count] = some ([], dexNext) ∧
      Related program lFinal.stamp dexNext := by
  obtain ⟨previous, rfl⟩ : ∃ previous, count = previous + 1 := ⟨count - 1, by omega⟩
  have isRunningL : lConfig.status = .running := by
    cases statusCase : lConfig.status with
    | running => rfl
    | notStarted =>
      have noThread := (state.lInvariant.notStartedIsInitial statusCase).1
      simp [L.Config.fireTimers, wp2_leastDueTimerThread_eq, wp2_dueCandidates, noThread,
        sortStable] at isFired
    | closed =>
      have noThread := state.lInvariant.closedHasNoThread statusCase
      simp [L.Config.fireTimers, wp2_leastDueTimerThread_eq, wp2_dueCandidates, noThread,
        sortStable] at isFired
  obtain ⟨run, hasRun, isRunning⟩ := Related.run_of_running state.isRelated isRunningL
  have related := state.isRelated.running run hasRun isRunning
  have keysNodup := timerKeysNodup run hasRun
  have canCommit := Related.canCommit_of_canStamp state.isRelated canStamp
  have entriesNodup : ((sortStable Dex.Run.isTimerEntryAtMost
      (run.timers.filter (fun entry => decide (entry.2 ≤ dexConfig.clock)))).map
        (fun entry => entry.1)).Nodup :=
    ((wp2_sortStable_perm _ _).map _).nodup_iff.mpr
      (keysNodup.sublist (List.Sublist.map _ List.filter_sublist))
  have entriesMem : ∀ timer fireTime,
      (timer, fireTime) ∈ sortStable Dex.Run.isTimerEntryAtMost
        (run.timers.filter (fun entry => decide (entry.2 ≤ dexConfig.clock))) ↔
      ({ run with seq := run.seq + 1 } : Dex.Run signature).timers.get? timer = some fireTime ∧
        fireTime ≤ lConfig.clock := by
    intro timer fireTime
    rw [mem_sortStable, List.mem_filter, wp2_mem_iff_get? _ keysNodup, state.isRelated.clock]
    simp
  obtain ⟨isEnough, sameClock, sameStatus, finalRelated⟩ := wp2_fire_batch program dexConfig _
    (previous + 1) lConfig { run with seq := run.seq + 1 }
    (sortStable Dex.Run.isTimerEntryAtMost
      (run.timers.filter (fun entry => decide (entry.2 ≤ dexConfig.clock))))
    lFinal state.lInvariant (state.dexInvariant.runInvariant run hasRun).bumpSeq
    (wp2_runningRelated_firing related hasRun)
    (wp2_sortStable_pairwise _ wp2_isTimerEntryAtMost_total wp2_isTimerEntryAtMost_trans _)
    entriesNodup entriesMem isFired
  have isStillRunning := (wp2_status_foldl_fireTimer
    (((sortStable Dex.Run.isTimerEntryAtMost
      (run.timers.filter (fun entry => decide (entry.2 ≤ dexConfig.clock)))).map
        (fun entry => entry.1)).take (previous + 1))
    { run with seq := run.seq + 1 }).trans isRunning
  refine ⟨_, wp2_exec_timerFire ⟦program⟧ dexConfig run hasRun isRunning canCommit (previous + 1)
    isPositive (by simpa [Dex.Run.dueTimers] using isEnough) _ rfl isStillRunning, ?_⟩
  exact
    { clock := sameClock.trans state.isRelated.clock
      stamp := congrArg some (sameClock.trans state.isRelated.clock)
      status := by
        simp only [L.Config.stamp, sameStatus, isRunningL, wp2_firingConfig]
        exact isStillRunning
      notStarted := fun hasNoRun => by simp [wp2_firingConfig] at hasNoRun
      running := fun otherRun hasOtherRun _ => by
        obtain rfl := Option.some.inj hasOtherRun
        exact wp2_runningRelated_stamp finalRelated
      closed := fun otherRun status hasOtherRun isClosed => by
        obtain rfl := Option.some.inj hasOtherRun
        have isBoth : Dex.RunStatus.running = Dex.RunStatus.closed status :=
          isStillRunning.symm.trans isClosed
        cases isBoth }

end DexExpressiveness
