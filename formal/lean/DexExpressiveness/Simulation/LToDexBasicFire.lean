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
  obtain ⟨isMember, hasTid⟩ := wp2_mem_of_findThread? isFound
  have memNew := wp2_mem_setPhase invariant isFound (.woken .fired)
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

/-- `eval(e)` keeps the status. -/
theorem wp2_status_evalStep (run : Dex.Run signature) (step : Dex.StepExecId signature) :
    (run.evalStep step).status = run.status := by
  rcases Dex.Run.evalStep_shape run step with isSame | ⟨chans, consumeFlags, change, _, isEval⟩
  · rw [isSame]
  · rw [isEval]
    exact (Dex.Run.consumeMessages_frame chans consumeFlags (run.disarmTimersOf step)).2.2.2.2.1

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
        · rw [wp2_status_evalStep]
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
  · have isRejected := wp2_get?_filter_of_rejected
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

end DexExpressiveness
