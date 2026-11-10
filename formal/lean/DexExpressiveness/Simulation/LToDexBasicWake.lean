import DexExpressiveness.Simulation.LToDexBasicLemmas

/-!
# Helper lemmas of work package 2: waking a blocked thread

The wake of one blocked thread by an external message or by its timer, on
both sides:

- L picks the thread that blocked first (`earliestBlocked`);
- Dex evaluates the waiters in `sws` order, and only the first one finds a
  plan (`wake`).

Every lemma is prefixed `wp2_`. The lemmas that link a waiting Step to a
blocked thread take the hypothesis that a waiting Step has no invocation.
`IsSimulationState` does not hold that fact at the base commit of this
package; see the README.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Association lists -/

/-- Filtering a map by a predicate on keys removes a key the predicate rejects. -/
theorem wp2_get?_filter_of_rejected {κ ν : Type} [DecidableEq κ] (isKept : κ → Bool)
    (map : AssocList κ ν) (wanted : κ) (isRejected : isKept wanted = false) :
    AssocList.get? (map.filter (fun entry => isKept entry.1)) wanted = none := by
  induction map with
  | nil => rfl
  | cons entry rest inductionHypothesis =>
    obtain ⟨key, value⟩ := entry
    by_cases isWanted : key = wanted
    · subst isWanted
      simp [isRejected, inductionHypothesis]
    · by_cases keepsKey : isKept key = true
      · simp [keepsKey, AssocList.get?, isWanted, inductionHypothesis]
      · simp [keepsKey, inductionHypothesis]

/-! ## `eval` and `wake` -/

/-- Evaluating Steps that have no plan changes nothing. -/
theorem wp2_foldl_evalStep_of_unplanned (steps : List (Dex.StepExecId signature))
    (run : Dex.Run signature)
    (isUnplanned : ∀ step, step ∈ steps → ∀ record waitState,
      run.findStep? step = some record → record.phase = .waiting →
      record.waitState = some waitState →
      Dex.Run.plan waitState.wait (fun chan => (run.pendingMessages chan).length)
        waitState.timersDone = none) :
    steps.foldl Dex.Run.evalStep run = run := by
  induction steps with
  | nil => rfl
  | cons head laterSteps inductionHypothesis =>
    have isSame : run.evalStep head = run := by
      cases foundCase : run.findStep? head with
      | none => simp [Dex.Run.evalStep, foundCase]
      | some record =>
        by_cases isWaiting : record.phase = .waiting
        · cases waitCase : record.waitState with
          | none =>
            unfold Dex.Run.evalStep
            rw [foundCase]
            simp [isWaiting, waitCase]
          | some waitState =>
            exact Dex.Run.evalStep_of_plan_none run head record waitState foundCase waitCase
              (isUnplanned head List.mem_cons_self record waitState foundCase isWaiting waitCase)
        · exact Dex.Run.evalStep_of_not_waiting run head record foundCase isWaiting
    simp only [List.foldl_cons, isSame]
    exact inductionHypothesis (fun step isStep => isUnplanned step (List.mem_cons_of_mem _ isStep))

/-- A supported wait that names a Channel has no plan while none of its
timers is done and every Channel it names is empty. -/
theorem wp2_plan_none_of_quiet (wait : Dex.Wait signature) (pendingCount : Chan → Nat)
    (timersDone : List Bool) (isSupported : Dex.IsSupportedWait wait)
    (hasChannel : wait.channels ≠ []) (noTimerDone : timersDone.any id = false)
    (isQuiet : ∀ chan, chan ∈ wait.channels → pendingCount chan = 0) :
    Dex.Run.plan wait pendingCount timersDone = none := by
  have hasCondition : wait.hasNoCondition = false := by
    cases channelCase : wait.channels with
    | nil => exact absurd channelCase hasChannel
    | cons chan laterChans => simp [Dex.Wait.hasNoCondition, channelCase]
  unfold Dex.Run.plan
  simp only [hasCondition, Bool.false_eq_true, ↓reduceIte]
  rcases isSupported with isEmpty | isAny | ⟨isAll, _, chan, isOne⟩
  · rw [hasCondition] at isEmpty
    cases isEmpty
  · simp only [isAny, noTimerDone, Bool.false_eq_true, ↓reduceIte]
    exact Dex.Run.planAnyChannel_eq_none pendingCount wait.channels isQuiet
  · have isEmptyChan := isQuiet chan (by rw [isOne]; simp)
    simp [isAll, isOne, isEmptyChan]

/-! ## The thread that blocked first -/

/-- The blocked threads in a wanted wait, each with its `since` stamp. -/
def wp2_blockedCandidates (config : L.Config) (isWanted : BlockedOn → Bool) : List (Nat × Tid) :=
  config.threads.filterMap (fun candidate =>
    match candidate.phase with
    | .blocked on since => if isWanted on then some (since, candidate.core.tid) else none
    | _ => none)

/-- `earliestBlocked` is the first candidate in `since` order. -/
theorem wp2_earliestBlocked_eq (config : L.Config) (isWanted : BlockedOn → Bool) :
    config.earliestBlocked isWanted =
      (sortStable (fun first second => decide (first.1 ≤ second.1))
        (wp2_blockedCandidates config isWanted)).head?.map (fun candidate => candidate.2) := rfl

/-- No thread is blocked in a wanted wait: `earliestBlocked` finds none. -/
theorem wp2_earliestBlocked_eq_none (config : L.Config) (isWanted : BlockedOn → Bool)
    (noneBlocked : ∀ thread, thread ∈ config.threads → ∀ on since,
      thread.phase = .blocked on since → isWanted on = false) :
    config.earliestBlocked isWanted = none := by
  have noCandidate : wp2_blockedCandidates config isWanted = [] := by
    apply List.filterMap_eq_nil_iff.mpr
    intro thread isMember
    cases phaseCase : thread.phase <;> simp
    rename_i on since
    exact noneBlocked thread isMember on since phaseCase
  rw [wp2_earliestBlocked_eq, noCandidate]
  rfl

/-- The thread whose `since` is the least among the wanted blocked threads is
the one `earliestBlocked` finds. -/
theorem wp2_earliestBlocked_eq_some {program : Program} (config : L.Config)
    (invariant : L.Invariant program config) (isWanted : BlockedOn → Bool)
    (thread : L.Thread) (isMember : thread ∈ config.threads) (on : BlockedOn) (since : Nat)
    (isBlocked : thread.phase = .blocked on since) (isWantedOn : isWanted on = true)
    (isEarliest : ∀ other, other ∈ config.threads → ∀ otherOn otherSince,
      other.phase = .blocked otherOn otherSince → isWanted otherOn = true →
      since ≤ otherSince) :
    config.earliestBlocked isWanted = some thread.core.tid := by
  have isHead := head?_sortStable_of_least
    (fun (first second : Nat × Tid) => decide (first.1 ≤ second.1)) (since, thread.core.tid)
    (wp2_blockedCandidates config isWanted)
    (List.mem_filterMap.mpr ⟨thread, isMember, by simp [isBlocked, isWantedOn]⟩)
    (by simp)
    (by
      intro other isOther
      obtain ⟨owner, isOwner, isCandidate⟩ := List.mem_filterMap.mp isOther
      cases phaseCase : owner.phase <;> simp [phaseCase] at isCandidate
      rename_i otherOn otherSince
      obtain ⟨isOtherWanted, isOtherPair⟩ := isCandidate
      subst isOtherPair
      by_cases sameSince : otherSince = since
      · subst sameSince
        have isThread : owner = thread :=
          invariant.sinceInjective owner isOwner thread isMember otherOn on otherSince
            phaseCase isBlocked
        rw [isThread]
        exact Or.inl rfl
      · have isAtMost := isEarliest owner isOwner otherOn otherSince phaseCase isOtherWanted
        refine Or.inr ⟨by simpa using isAtMost, ?_⟩
        simp only [decide_eq_false_iff_not, Nat.not_le]
        omega)
  rw [wp2_earliestBlocked_eq, isHead]
  rfl

/-- Among the wanted blocked threads, one blocked first. -/
theorem wp2_exists_earliest (config : L.Config) (isWanted : BlockedOn → Bool)
    (thread : L.Thread) (isMember : thread ∈ config.threads) (on : BlockedOn) (since : Nat)
    (isBlocked : thread.phase = .blocked on since) (isWantedOn : isWanted on = true) :
    ∃ earliest earliestOn earliestSince, earliest ∈ config.threads ∧
      earliest.phase = .blocked earliestOn earliestSince ∧ isWanted earliestOn = true ∧
      ∀ other, other ∈ config.threads → ∀ otherOn otherSince,
        other.phase = .blocked otherOn otherSince → isWanted otherOn = true →
        earliestSince ≤ otherSince := by
  induction since using Nat.strongRecOn generalizing thread on with
  | ind since inductionHypothesis =>
    by_cases hasEarlier : ∃ other otherOn otherSince, other ∈ config.threads ∧
        other.phase = .blocked otherOn otherSince ∧ isWanted otherOn = true ∧ otherSince < since
    · obtain ⟨other, otherOn, otherSince, isOther, isOtherBlocked, isOtherWanted, isEarlier⟩ :=
        hasEarlier
      exact inductionHypothesis otherSince isEarlier other isOther otherOn isOtherBlocked
        isOtherWanted
    · refine ⟨thread, on, since, isMember, isBlocked, isWantedOn, ?_⟩
      intro other isOther otherOn otherSince isOtherBlocked isOtherWanted
      apply Nat.le_of_not_lt
      intro isEarlier
      exact hasEarlier ⟨other, otherOn, otherSince, isOther, isOtherBlocked, isOtherWanted,
        isEarlier⟩

/-! ## A waiting Step carries a blocked thread -/

/-- A Step in phase `WT` carries a blocked thread, given that a waiting Step
has no invocation. -/
theorem wp2_blocked_of_waiting {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (state : IsSimulationState program lConfig dexConfig)
    {run : Dex.Run signature} (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running)
    (waitingHasNoInvocation : ∀ record, record ∈ run.steps → record.phase = .waiting →
      record.invocation = none)
    (record : Dex.StepRecord signature) (isRecord : record ∈ run.steps)
    (isWaiting : record.phase = .waiting) :
    ∃ thread on since, thread ∈ lConfig.threads ∧ thread.phase = .blocked on since ∧
      StepCarriesThread program thread record ∧
      PhaseMatchesStep program lConfig dexConfig thread record := by
  have related := state.isRelated.running run hasRun isRunning
  have isActive : record.phase.isActive = true := by rw [isWaiting]; rfl
  obtain ⟨thread, isMember, isNotHalted, carries⟩ :=
    related.stepsHaveThreads record isRecord isActive
  obtain ⟨other, isOther, otherActive, otherCarries, isMatch⟩ :=
    related.threadsHaveSteps thread isMember isNotHalted
  have sameRecord : other = record :=
    related.activeStepsHaveDistinctThreads other isOther record isRecord otherActive isActive
      ((wp2_threadOfInput_of_mem state.lInvariant isMember otherCarries).trans
        (wp2_threadOfInput_of_mem state.lInvariant isMember carries).symm)
  subst sameRecord
  have noInvocation := waitingHasNoInvocation other isOther isWaiting
  have isMatchCopy := isMatch
  unfold PhaseMatchesStep at isMatchCopy
  cases phaseCase : thread.phase with
  | blocked on since => exact ⟨thread, on, since, isMember, phaseCase, carries, isMatch⟩
  | halted => exact absurd phaseCase isNotHalted
  | bootPending => simp [phaseCase, noInvocation] at isMatchCopy
  | ready => simp [phaseCase, noInvocation] at isMatchCopy
  | woken outcome => simp [phaseCase, noInvocation] at isMatchCopy
  | bootRunning =>
    simp only [phaseCase] at isMatchCopy
    obtain ⟨_, _, _, hasInvocation, _⟩ := isMatchCopy
    simp [noInvocation] at hasInvocation
  | running loaded =>
    simp only [phaseCase] at isMatchCopy
    obtain ⟨_, _, _, hasInvocation, _⟩ := isMatchCopy
    simp [noInvocation] at hasInvocation
  | emitting effect =>
    simp only [phaseCase] at isMatchCopy
    obtain ⟨_, _, _, hasInvocation, _⟩ := isMatchCopy
    simp [noInvocation] at hasInvocation
  | emitted =>
    simp only [phaseCase] at isMatchCopy
    obtain ⟨_, _, _, hasInvocation, _⟩ := isMatchCopy
    simp [noInvocation] at hasInvocation
  | waitStarted =>
    simp only [phaseCase] at isMatchCopy
    obtain ⟨_, _, _, hasInvocation, _⟩ := isMatchCopy
    simp [noInvocation] at hasInvocation
  | resuming outcome =>
    simp only [phaseCase] at isMatchCopy
    obtain ⟨_, _, _, hasInvocation, _⟩ := isMatchCopy
    simp [noInvocation] at hasInvocation

/-- Two threads carried by the same active Step are the same thread. -/
theorem wp2_thread_eq_of_carries {program : Program} {lConfig : L.Config}
    (lInvariant : L.Invariant program lConfig) {first second : L.Thread}
    {record : Dex.StepRecord signature} (isFirst : first ∈ lConfig.threads)
    (isSecond : second ∈ lConfig.threads) (firstCarries : StepCarriesThread program first record)
    (secondCarries : StepCarriesThread program second record) : first = second :=
  wp2_thread_eq_of_tid lInvariant isFirst isSecond
    ((wp2_threadOfInput_of_mem lInvariant isFirst firstCarries).symm.trans
      (wp2_threadOfInput_of_mem lInvariant isSecond secondCarries))

/-! ## [PUBLISH] to an external Channel: who is woken -/

/-- The waiters `wake` evaluates, each with its `sws`. -/
def wp2_waiters (run : Dex.Run signature) (changed : List Chan) :
    List (Nat × Dex.StepExecId signature) :=
  run.steps.filterMap (fun record =>
    match record.phase, record.waitState with
    | .waiting, some waitState =>
      if waitState.wait.channels.any (fun chan => changed.contains chan)
      then some (waitState.registeredSeq, record.id) else none
    | _, _ => none)

/-- `wake` evaluates the waiters in `sws` order. -/
theorem wp2_wake_eq (run : Dex.Run signature) (changed : List Chan) :
    run.wake changed =
      ((sortStable (fun first second => decide (first.1 ≤ second.1))
        (wp2_waiters run changed)).map (fun waiter => waiter.2)).foldl Dex.Run.evalStep run := by
  simp only [Dex.Run.wake, Dex.Run.waitersOn, wp2_waiters]
  congr 4
  funext record
  cases record.phase <;> cases record.waitState <;> rfl

/-- A waiter of `wp2_waiters` is a waiting Step that names a changed Channel. -/
theorem wp2_mem_waiters (run : Dex.Run signature) (changed : List Chan) (seq : Nat)
    (step : Dex.StepExecId signature) (isWaiter : (seq, step) ∈ wp2_waiters run changed) :
    ∃ record waitState, record ∈ run.steps ∧ record.phase = .waiting ∧
      record.waitState = some waitState ∧
      (∃ chan, chan ∈ waitState.wait.channels ∧ chan ∈ changed) ∧
      waitState.registeredSeq = seq ∧ record.id = step := by
  obtain ⟨record, isRecord, isCandidate⟩ := List.mem_filterMap.mp isWaiter
  cases phaseCase : record.phase <;> cases waitCase : record.waitState <;>
    simp [phaseCase, waitCase] at isCandidate
  rename_i waitState
  obtain ⟨isNamed, isSeq, isId⟩ := isCandidate
  exact ⟨record, waitState, isRecord, phaseCase, waitCase, isNamed, isSeq, isId⟩

/-- No Step waits on a changed Channel: `wake` changes nothing. -/
theorem wp2_wake_of_no_waiter (run : Dex.Run signature) (changed : List Chan)
    (noWaiter : ∀ record, record ∈ run.steps → ∀ waitState, record.phase = .waiting →
      record.waitState = some waitState → ∀ chan, chan ∈ waitState.wait.channels →
      chan ∉ changed) :
    run.wake changed = run := by
  have noCandidate : wp2_waiters run changed = [] := by
    cases candidateCase : wp2_waiters run changed with
    | nil => rfl
    | cons candidate laterCandidates =>
      obtain ⟨seq, step⟩ := candidate
      obtain ⟨record, waitState, isRecord, isWaiting, hasWait, ⟨chan, isNamed, isChanged⟩, _⟩ :=
        wp2_mem_waiters run changed seq step (by rw [candidateCase]; exact List.mem_cons_self)
      exact absurd isChanged (noWaiter record isRecord waitState isWaiting hasWait chan isNamed)
  rw [wp2_wake_eq, noCandidate]
  rfl

/-- The run in whose commit `message` was published to the empty Channel `chan`. -/
def wp2_publishedRun (run : Dex.Run signature) (chan : Chan) (message : DexVal) :
    Dex.Run signature :=
  { run with seq := run.seq + 1 }.setPendingMessages chan [message]

/-- The run after the Step `record` took `message` from `chan`, the first
Channel of its AnyOf wait. -/
def wp2_receivedRun (run : Dex.Run signature) (record : Dex.StepRecord signature)
    (waitState : Dex.WaitState signature) (chan : Chan) (laterChans : List Chan)
    (message : DexVal) : Dex.Run signature :=
  (((wp2_publishedRun run chan message).disarmTimersOf record.id).setPendingMessages chan
    []).updateStep record.id (fun current =>
      { current with
        conditionResults := some
          { timersDone := waitState.timersDone
            channelMessages := some message :: laterChans.map (fun _ => none) }
        phase := .execute
        invocation := some { method := .execute, state := .pending } })

/-- After the receive, every Channel holds what it held before the publication. -/
theorem wp2_receivedRun_pending (run : Dex.Run signature) (record : Dex.StepRecord signature)
    (waitState : Dex.WaitState signature) (chan : Chan) (laterChans : List Chan)
    (message : DexVal) (wasEmpty : run.pendingMessages chan = []) (other : Chan) :
    (wp2_receivedRun run record waitState chan laterChans message).pendingMessages other =
      run.pendingMessages other := by
  simp only [wp2_receivedRun, wp2_publishedRun, Dex.Run.pendingMessages,
    Dex.Run.setPendingMessages, Dex.Run.disarmTimersOf, Dex.Run.updateStep]
  by_cases isChan : other = chan
  · subst isChan
    rw [AssocList.get?_set_self]
    exact wasEmpty.symm
  · rw [AssocList.get?_set_other _ _ _ _ isChan, AssocList.get?_set_other _ _ _ _ isChan]

/-- [PUBLISH] of one message to an empty Channel that the earliest waiter
names first in an AnyOf wait: that Step receives the message, and no other
Step changes. -/
theorem wp2_publish_wake_received {seqBound : Nat} (run : Dex.Run signature)
    (invariant : Dex.Run.Invariant IsTimedTimer seqBound run)
    (record : Dex.StepRecord signature) (isRecord : record ∈ run.steps)
    (isWaiting : record.phase = .waiting) (waitState : Dex.WaitState signature)
    (hasWait : record.waitState = some waitState) (isAny : waitState.wait.kind = .any)
    (chan : Chan) (laterChans : List Chan)
    (hasChannels : waitState.wait.channels = chan :: laterChans) (message : DexVal)
    (isEarliest : ∀ other, other ∈ run.steps → ∀ otherWait, other.phase = .waiting →
      other.waitState = some otherWait → chan ∈ otherWait.wait.channels →
      other = record ∨ waitState.registeredSeq < otherWait.registeredSeq) :
    (({ run with seq := run.seq + 1 } : Dex.Run signature).applyPublications
      [(chan, message)]).wake [chan] =
      wp2_receivedRun run record waitState chan laterChans message := by
  have isWaitingRecord : run.IsWaiting record waitState := ⟨isRecord, isWaiting, hasWait⟩
  have wasEmpty : run.pendingMessages chan = [] :=
    invariant.waitersQuiet record waitState isWaitingRecord chan (by rw [hasChannels]; simp)
  have noTimerDone := (invariant.waitsSupported record waitState isWaitingRecord).2.1
  have isPublished : ({ run with seq := run.seq + 1 } : Dex.Run signature).applyPublications
      [(chan, message)] = wp2_publishedRun run chan message := by
    have wasEmptyBumped :
        ({ run with seq := run.seq + 1 } : Dex.Run signature).pendingMessages chan = [] := wasEmpty
    simp [Dex.Run.applyPublications, wp2_publishedRun, wasEmptyBumped]
  have hasMessage : (wp2_publishedRun run chan message).pendingMessages chan = [message] := by
    simp [wp2_publishedRun, Dex.Run.pendingMessages, Dex.Run.setPendingMessages,
      AssocList.get?_set_self]
  have isFound : (wp2_publishedRun run chan message).findStep? record.id = some record :=
    Dex.Run.findStep?_of_mem_nodup run record isRecord invariant.idsNodup
  have isEval : (wp2_publishedRun run chan message).evalStep record.id =
      wp2_receivedRun run record waitState chan laterChans message := by
    have isPlanned : Dex.Run.plan waitState.wait
        (fun other => ((wp2_publishedRun run chan message).pendingMessages other).length)
        waitState.timersDone = some (true :: laterChans.map (fun _ => false)) := by
      unfold Dex.Run.plan
      simp only [Dex.Wait.hasNoCondition, hasChannels, List.isEmpty_cons, Bool.and_false,
        Bool.false_eq_true, ↓reduceIte, isAny, noTimerDone]
      exact Dex.Run.planAnyChannel_first (σ := signature) _ chan laterChans
        (by simp [hasMessage])
    rw [Dex.Run.evalStep_of_plan_some _ record.id record waitState _ isFound isWaiting hasWait
      isPlanned]
    rw [hasChannels, Dex.Run.consumeMessages_first (σ := signature) chan laterChans
      ((wp2_publishedRun run chan message).disarmTimersOf record.id) message [] hasMessage]
    rfl
  rw [isPublished, wp2_wake_eq]
  have isHead := head?_sortStable_of_least
    (fun (first second : Nat × Dex.StepExecId signature) => decide (first.1 ≤ second.1))
    (waitState.registeredSeq, record.id) (wp2_waiters (wp2_publishedRun run chan message) [chan])
    (List.mem_filterMap.mpr ⟨record, isRecord, by simp [isWaiting, hasWait, hasChannels]⟩)
    (by simp)
    (by
      intro other isOther
      obtain ⟨otherSeq, otherStep⟩ := other
      obtain ⟨otherRecord, otherWait, isOtherRecord, isOtherWaiting, hasOtherWait,
        ⟨named, isNamed, isChanged⟩, isSeq, isId⟩ := wp2_mem_waiters _ _ _ _ isOther
      have isChan : named = chan := by simpa using isChanged
      subst isChan
      rcases isEarliest otherRecord isOtherRecord otherWait isOtherWaiting hasOtherWait isNamed
        with isSame | isLater
      · subst isSame
        have sameWait : otherWait = waitState := Option.some.inj (hasOtherWait.symm.trans hasWait)
        subst sameWait
        exact Or.inl (by rw [← isSeq, ← isId])
      · refine Or.inr ⟨?_, ?_⟩
        · simp only [decide_eq_true_eq]
          omega
        · simp only [decide_eq_false_iff_not, Nat.not_le]
          omega)
  cases sortedCase : sortStable
      (fun (first second : Nat × Dex.StepExecId signature) => decide (first.1 ≤ second.1))
      (wp2_waiters (wp2_publishedRun run chan message) [chan]) with
  | nil => simp [sortedCase] at isHead
  | cons head laterWaiters =>
    have isHeadWaiter : head = (waitState.registeredSeq, record.id) := by
      simpa [sortedCase] using isHead
    subst isHeadWaiter
    simp only [List.map_cons, List.foldl_cons]
    rw [isEval]
    apply wp2_foldl_evalStep_of_unplanned
    intro step isStep found foundWait isFoundStep isFoundWaiting hasFoundWait
    obtain ⟨⟨otherSeq, otherStep⟩, isLater, isStepId⟩ := List.mem_map.mp isStep
    simp only at isStepId
    subst isStepId
    obtain ⟨otherRecord, otherWait, isOtherRecord, isOtherWaiting, hasOtherWait,
      ⟨named, isNamed, _⟩, _, isId⟩ := wp2_mem_waiters _ _ _ _
        ((mem_sortStable _ _ _).mp (by rw [sortedCase]; exact List.mem_cons_of_mem _ isLater))
    by_cases isRecordStep : otherStep = record.id
    · subst isRecordStep
      have isFoundChanged := Dex.Run.findStep?_updateStep_self
        (((wp2_publishedRun run chan message).disarmTimersOf record.id).setPendingMessages chan [])
        record.id (fun current =>
          { current with
            conditionResults := some
              { timersDone := waitState.timersDone
                channelMessages := some message :: laterChans.map (fun _ => none) }
            phase := .execute
            invocation := some { method := .execute, state := .pending } })
        record isFound rfl
      have isChanged : found = _ := Option.some.inj (isFoundStep.symm.trans isFoundChanged)
      rw [isChanged] at isFoundWaiting
      cases isFoundWaiting
    · have isFoundOld : (wp2_receivedRun run record waitState chan laterChans message).findStep?
          otherStep = run.findStep? otherStep :=
        Dex.Run.findStep?_updateStep_other _ _ _ _ (fun _ => rfl) isRecordStep
      have isFoundOther : run.findStep? otherStep = some otherRecord := by
        rw [← isId]
        exact Dex.Run.findStep?_of_mem_nodup run otherRecord isOtherRecord invariant.idsNodup
      have isOther : found = otherRecord :=
        Option.some.inj (isFoundStep.symm.trans (isFoundOld.trans isFoundOther))
      subst isOther
      have sameWait : foundWait = otherWait := Option.some.inj (hasFoundWait.symm.trans hasOtherWait)
      subst sameWait
      have isWaitingOther : run.IsWaiting found foundWait :=
        ⟨isOtherRecord, isOtherWaiting, hasOtherWait⟩
      obtain ⟨isSupported, noOtherTimerDone, _⟩ := invariant.waitsSupported found foundWait
        isWaitingOther
      apply wp2_plan_none_of_quiet foundWait.wait _ foundWait.timersDone isSupported
        (List.ne_nil_of_mem isNamed) noOtherTimerDone
      intro otherChan isOtherChan
      rw [wp2_receivedRun_pending run record waitState chan laterChans message wasEmpty otherChan,
        invariant.waitersQuiet found foundWait isWaitingOther otherChan isOtherChan]
      rfl

/-! ## Threads that receive from an external channel -/

/-- Whether a blocked thread receives from this external channel: `recv(c)`
or a `select` on `c`. -/
def wp2_isReceiveOn (chan : ExtChan) : BlockedOn → Bool
  | .recv wanted => decide (wanted = chan)
  | .timed _ _ (some wanted) => decide (wanted = chan)
  | _ => false

/-- `deliverInput` serves the earliest receiver on the channel. -/
theorem wp2_deliverInput_eq (config : L.Config) (chan : ExtChan) (value : Val) :
    config.deliverInput chan value =
      match config.earliestBlocked (wp2_isReceiveOn chan) with
      | some receiver => config.wakeThread receiver (.received value)
      | none =>
        { config with
          inputQueues := config.inputQueues.set chan (config.pendingInput chan ++ [value]) } := by
  have sameWanted : ∀ isWanted : BlockedOn → Bool,
      (∀ on, isWanted on = wp2_isReceiveOn chan on) →
      config.earliestBlocked isWanted = config.earliestBlocked (wp2_isReceiveOn chan) := by
    intro isWanted isSame
    rw [show isWanted = wp2_isReceiveOn chan from funext isSame]
  unfold L.Config.deliverInput
  simp only []
  rw [sameWanted _ (by
    intro on
    cases on with
    | timed number deadline optionalChan => cases optionalChan <;> rfl
    | _ => rfl)]
  cases config.earliestBlocked (wp2_isReceiveOn chan) <;> rfl

/-- The wait of a receiver names the external Channel first, in an AnyOf. -/
theorem wp2_shape_of_isReceiveOn (self : Tid) (chan : ExtChan) (on : BlockedOn)
    (isReceive : wp2_isReceiveOn chan on = true) :
    (expectedWaitShape self on).1 = .any ∧
    (expectedWaitShape self on).2.2 = [Chan.external chan, Chan.cancelRequest self] := by
  cases on with
  | timed number deadline optionalChan =>
    cases optionalChan with
    | none => simp [wp2_isReceiveOn] at isReceive
    | some wanted =>
      simp only [wp2_isReceiveOn, decide_eq_true_eq] at isReceive
      subst isReceive
      exact ⟨rfl, rfl⟩
  | recv wanted =>
    simp only [wp2_isReceiveOn, decide_eq_true_eq] at isReceive
    subst isReceive
    exact ⟨rfl, rfl⟩
  | lock mutex => simp [wp2_isReceiveOn] at isReceive
  | join thread => simp [wp2_isReceiveOn] at isReceive
  | park cv => simp [wp2_isReceiveOn] at isReceive

/-- A wait that names an external Channel is the wait of a receiver on it. -/
theorem wp2_isReceiveOn_of_mem (self : Tid) (chan : ExtChan) (on : BlockedOn)
    (isNamed : Chan.external chan ∈ (expectedWaitShape self on).2.2) :
    wp2_isReceiveOn chan on = true := by
  cases on with
  | timed number deadline optionalChan =>
    cases optionalChan <;> simp_all [expectedWaitShape, wp2_isReceiveOn]
  | _ => simp_all [expectedWaitShape, wp2_isReceiveOn]

/-- With unique thread IDs, the lookup of a thread's ID finds the thread. -/
theorem wp2_findThread?_of_mem {program : Program} {config : L.Config}
    (invariant : L.Invariant program config) {thread : L.Thread}
    (isMember : thread ∈ config.threads) : config.findThread? thread.core.tid = some thread := by
  cases foundCase : config.findThread? thread.core.tid with
  | none =>
    have isRejected := List.find?_eq_none.mp foundCase thread isMember
    simp at isRejected
  | some found =>
    obtain ⟨isFoundMember, sameTid⟩ := wp2_mem_of_findThread? foundCase
    rw [wp2_thread_eq_of_tid invariant isFoundMember isMember sameTid]

/-- A thread blocked in a timed operation has entered it with that timer number. -/
theorem wp2_timer_of_blocked_timed {program : Program} {config : L.Config}
    (invariant : L.Invariant program config) {thread : L.Thread}
    (isMember : thread ∈ config.threads) {number deadline since : Nat}
    {chan : Option ExtChan} (isBlocked : thread.phase = .blocked (.timed number deadline chan) since) :
    (∃ seconds, thread.timer = some (number, seconds)) ∧ timedChan thread.code = some chan := by
  have fitsCode := invariant.phasesFitCode thread isMember
  unfold L.Thread.PhaseFitsCode at fitsCode
  simpa [isBlocked] using fitsCode

/-- The Step that carries a thread inside a timed operation is the `Timed`
Step with the thread's timer number. -/
theorem wp2_record_of_timer {program : Program} {thread : L.Thread}
    {record : Dex.StepRecord signature} (carries : StepCarriesThread program thread record)
    (isNotBoot : wp2_isBootPhase thread.phase = false) {number seconds : Nat}
    (hasTimer : thread.timer = some (number, seconds)) :
    record.id = { stepType := StepType.timed, number := number } := by
  rw [wp2_stepCarriesThread_iff] at carries
  simp only [isNotBoot, hasTimer, Bool.false_eq_true, ↓reduceIte] at carries
  exact carries.1

/-- The Step that carries a thread outside a timed operation is a program point. -/
theorem wp2_record_of_no_timer {program : Program} {thread : L.Thread}
    {record : Dex.StepRecord signature} (carries : StepCarriesThread program thread record)
    (isNotBoot : wp2_isBootPhase thread.phase = false) (hasNoTimer : thread.timer = none) :
    record.id.stepType = StepType.point thread.code := by
  rw [wp2_stepCarriesThread_iff] at carries
  simp only [isNotBoot, hasNoTimer, Bool.false_eq_true, ↓reduceIte] at carries
  exact carries.1

/-- The timer clause after a blocked thread is woken and the timers of its
Step are disarmed. -/
theorem wp2_timers_of_wake {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (lInvariant : L.Invariant program lConfig)
    {tid : Tid} {thread : L.Thread} (isFound : lConfig.findThread? tid = some thread)
    {record : Dex.StepRecord signature} (carries : StepCarriesThread program thread record)
    {on : BlockedOn} {since : Nat} (isBlocked : thread.phase = .blocked on since)
    (outcome : WakeOutcome) (newTimers : AssocList (Dex.TimerId signature) Nat)
    (hasTimers : ∀ timer fireTime, newTimers.get? timer = some fireTime ↔
      run.timers.get? timer = some fireTime ∧ timer.step ≠ record.id) :
    ∀ timer fireTime, newTimers.get? timer = some fireTime ↔
      ∃ owner chan ownerSince, owner ∈ (lConfig.setPhase tid (.woken outcome)).threads ∧
        owner.phase = .blocked (.timed timer.step.number fireTime chan) ownerSince ∧
        timer.step.stepType = StepType.timed ∧ timer.index = 0 := by
  obtain ⟨isMember, hasTid⟩ := wp2_mem_of_findThread? isFound
  have memNew := wp2_mem_setPhase lInvariant isFound (.woken outcome)
  have isNotBoot : wp2_isBootPhase thread.phase = false := by rw [isBlocked]; rfl
  intro timer fireTime
  rw [hasTimers, related.timers]
  constructor
  · rintro ⟨⟨owner, chan, ownerSince, isOwner, isOwnerBlocked, isTimed, isIndex⟩, isOtherStep⟩
    refine ⟨owner, chan, ownerSince, (memNew owner).mpr (Or.inl ⟨isOwner, ?_⟩), isOwnerBlocked,
      isTimed, isIndex⟩
    intro sameTid
    have isThread : owner = thread :=
      wp2_thread_eq_of_tid lInvariant isOwner isMember (sameTid.trans hasTid.symm)
    subst isThread
    obtain ⟨⟨seconds, hasTimer⟩, _⟩ := wp2_timer_of_blocked_timed lInvariant isOwner isOwnerBlocked
    have isRecordId := wp2_record_of_timer carries isNotBoot hasTimer
    apply isOtherStep
    rw [isRecordId]
    cases stepCase : timer.step with
    | mk stepType number =>
      rw [stepCase] at isTimed
      simp only at isTimed
      rw [isTimed]
  · rintro ⟨owner, chan, ownerSince, isOwner, isOwnerBlocked, isTimed, isIndex⟩
    rcases (memNew owner).mp isOwner with ⟨isOld, otherTid⟩ | isNew
    · refine ⟨⟨owner, chan, ownerSince, isOld, isOwnerBlocked, isTimed, isIndex⟩, ?_⟩
      intro sameStep
      obtain ⟨⟨ownerSeconds, hasOwnerTimer⟩, _⟩ :=
        wp2_timer_of_blocked_timed lInvariant isOld isOwnerBlocked
      cases timerCase : thread.timer with
      | none =>
        have isPoint := wp2_record_of_no_timer carries isNotBoot timerCase
        rw [← sameStep, isTimed] at isPoint
        cases isPoint
      | some threadTimer =>
        obtain ⟨number, seconds⟩ := threadTimer
        have isRecordId := wp2_record_of_timer carries isNotBoot timerCase
        rw [← sameStep] at isRecordId
        have sameNumber : timer.step.number = number := by rw [isRecordId]
        rw [sameNumber] at hasOwnerTimer
        have isThread : owner = thread :=
          lInvariant.timerNumbersInjective owner isOld thread isMember number ownerSeconds seconds
            hasOwnerTimer timerCase
        exact otherTid ((congrArg (fun current => current.core.tid) isThread).trans hasTid)
    · subst isNew
      cases isOwnerBlocked

/-! ## The frame of a queued external message -/

/-- An external message that no thread waits for is queued on both sides. -/
theorem wp2_runningRelated_queueInput {program : Program} {lConfig : L.Config}
    {dexConfig dexNext : Dex.Config signature} {run newRun : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run) (chan : ExtChan) (value : Val)
    (sameSteps : newRun.steps = run.steps) (sameAttributes : newRun.attributes = run.attributes)
    (sameTimers : newRun.timers = run.timers) (sameCounters : newRun.counters = run.counters)
    (sameOutputs : newRun.outputs = run.outputs)
    (pendingChan : newRun.pendingMessages (Chan.external chan) =
      run.pendingMessages (Chan.external chan) ++ [DexVal.value value])
    (pendingOther : ∀ other, other ≠ Chan.external chan →
      newRun.pendingMessages other = run.pendingMessages other)
    (sameTasks : dexNext.tasks = dexConfig.tasks)
    (tasksAreCurrent : ∀ task, task ∈ dexNext.tasks → dexNext.isTaskCurrent task = true) :
    RunningRelated program
      { lConfig with
        inputQueues := lConfig.inputQueues.set chan (lConfig.pendingInput chan ++ [value]) }
      dexNext newRun :=
  { shared := fun name => by
      rw [sameAttributes]
      exact related.shared name
    cvQueues := fun cv => by
      rw [sameAttributes]
      exact related.cvQueues cv
    inputQueues := fun other => by
      by_cases isChan : other = chan
      · subst isChan
        rw [pendingChan, related.inputQueues]
        simp [L.Config.pendingInput, AssocList.get?_set_self]
      · rw [pendingOther _ (by simpa using isChan), related.inputQueues]
        simp [L.Config.pendingInput, AssocList.get?_set_other _ _ _ _ isChan]
    mutexTokens := fun areBooted mutex => by
      rw [pendingOther _ (by simp)]
      exact related.mutexTokens areBooted mutex
    mutexesBeforeBoot := fun thread isMember isBooting => by
      obtain ⟨sameOwners, noToken⟩ := related.mutexesBeforeBoot thread isMember isBooting
      exact ⟨sameOwners, fun mutex => by
        rw [pendingOther _ (by simp)]
        exact noToken mutex⟩
    joinTokens := fun joined => by
      rw [pendingOther _ (by simp)]
      exact related.joinTokens joined
    cancelRequestsAreFlags := fun target isPending => by
      rw [pendingOther _ (by simp)] at isPending
      exact related.cancelRequestsAreFlags target isPending
    cancelFlagsAreRequests := fun target isFlagged => by
      rw [pendingOther _ (by simp)]
      exact related.cancelFlagsAreRequests target isFlagged
    cvWakes := fun cv waiter => by
      rw [pendingOther _ (by simp)]
      exact related.cvWakes cv waiter
    timers := fun timer fireTime => by
      rw [sameTimers]
      exact related.timers timer fireTime
    timerCounter := by
      rw [sameCounters]
      exact related.timerCounter
    outputsEmpty := sameOutputs.trans related.outputsEmpty
    threadsHaveSteps := fun thread isMember isNotHalted => by
      obtain ⟨record, isRecord, isActive, carries, isMatch⟩ :=
        related.threadsHaveSteps thread isMember isNotHalted
      exact ⟨record, by rw [sameSteps]; exact isRecord, isActive, carries,
        wp2_phaseMatchesStep_transfer rfl
          (fun task isTask _ => by rw [sameTasks]; exact isTask) isMatch⟩
    stepsHaveThreads := fun record isRecord isActive => by
      rw [sameSteps] at isRecord
      exact related.stepsHaveThreads record isRecord isActive
    activeStepsHaveDistinctThreads := fun first isFirst second isSecond => by
      rw [sameSteps] at isFirst isSecond
      exact related.activeStepsHaveDistinctThreads first isFirst second isSecond
    invocationsAreActive := fun record isRecord => by
      rw [sameSteps] at isRecord
      exact related.invocationsAreActive record isRecord
    blockingOrder := fun first second firstRecord secondRecord firstOn firstSince secondOn
        secondSince firstWait secondWait isFirst isSecond isFirstRecord isSecondRecord => by
      rw [sameSteps] at isFirstRecord isSecondRecord
      exact related.blockingOrder first second firstRecord secondRecord firstOn firstSince
        secondOn secondSince firstWait secondWait isFirst isSecond isFirstRecord isSecondRecord
    tasksAreCurrent := tasksAreCurrent }

end DexExpressiveness
