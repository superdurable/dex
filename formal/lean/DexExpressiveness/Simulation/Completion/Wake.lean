import DexExpressiveness.Simulation.Completion.Mid

/-!
# `effects` on the Dex side: publications and `wake`

How `effects` (spec §4.2) changes a run whose waits are those of blocked
threads:

- `wp3b_publish_noWaiter`: publications to Channels that no blocked thread
  waits on only append the messages;
- `wp3b_publish_wake`: one message to a Channel goes to the Step of the
  thread that blocked first among those that wait on it (spec §7 item 4), and
  nothing else changes.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## `plan`, `consume`, and `eval` -/

section Generic

variable {σ : Dex.Signature}

/-- The `Any` plan over Channels with one non-empty Channel consumes from it. -/
theorem wp3b_planAnyChannel_fire (pendingCount : σ.Chan → Nat) (chans : List σ.Chan)
    (chan : σ.Chan) (isNamed : chan ∈ chans) (isNodup : chans.Nodup)
    (hasMessage : 0 < pendingCount chan)
    (othersEmpty : ∀ other, other ∈ chans → other ≠ chan → pendingCount other = 0) :
    Dex.Run.planAnyChannel pendingCount chans =
      some (chans.map (fun other => decide (other = chan))) := by
  induction chans with
  | nil => cases isNamed
  | cons head laterChans inductionHypothesis =>
    have ⟨headIsNew, laterNodup⟩ := List.nodup_cons.mp isNodup
    by_cases isHead : head = chan
    · subst isHead
      have laterFlags : laterChans.map (fun other => decide (other = head)) =
          laterChans.map (fun _ => false) := by
        apply List.map_congr_left
        intro other isLater
        have isOther : other ≠ head := fun isSame => headIsNew (isSame ▸ isLater)
        simp [isOther]
      simp [Dex.Run.planAnyChannel, hasMessage, laterFlags]
    · have isLater : chan ∈ laterChans := by
        rcases List.mem_cons.mp isNamed with isSame | isLater
        · exact absurd isSame.symm isHead
        · exact isLater
      have headEmpty : pendingCount head = 0 := othersEmpty head (List.mem_cons_self ..) isHead
      simp [Dex.Run.planAnyChannel, headEmpty,
        inductionHypothesis isLater laterNodup
          (fun other isOther => othersEmpty other (List.mem_cons_of_mem _ isOther)), isHead]

/-- The plan of a supported wait with no timer done, when exactly one of its
Channels has a message: consume from that Channel. -/
theorem wp3b_plan_fire (wait : Dex.Wait σ) (pendingCount : σ.Chan → Nat)
    (timersDone : List Bool) (chan : σ.Chan) (isSupported : Dex.IsSupportedWait wait)
    (noTimerDone : timersDone.any id = false)
    (sameLength : timersDone.length = wait.timerSeconds.length)
    (isNamed : chan ∈ wait.channels) (isNodup : wait.channels.Nodup)
    (hasMessage : 0 < pendingCount chan)
    (othersEmpty : ∀ other, other ∈ wait.channels → other ≠ chan → pendingCount other = 0) :
    Dex.Run.plan wait pendingCount timersDone =
      some (wait.channels.map (fun other => decide (other = chan))) := by
  have hasCondition : wait.hasNoCondition = false := by
    cases channelCase : wait.channels with
    | nil => rw [channelCase] at isNamed; cases isNamed
    | cons head later => simp [Dex.Wait.hasNoCondition, channelCase]
  unfold Dex.Run.plan
  simp only [hasCondition, Bool.false_eq_true, ↓reduceIte]
  rcases isSupported with isEmpty | isAny | ⟨isAll, noTimers, only, oneChan⟩
  · rw [hasCondition] at isEmpty
    cases isEmpty
  · simp only [isAny, noTimerDone, Bool.false_eq_true, ↓reduceIte]
    exact wp3b_planAnyChannel_fire pendingCount wait.channels chan isNamed isNodup hasMessage
      othersEmpty
  · have noneDone : timersDone = [] := by
      apply List.length_eq_zero_iff.mp
      rw [sameLength, noTimers]
      rfl
    have isOnly : chan = only := by
      rw [oneChan] at isNamed
      simpa using isNamed
    subst isOnly
    have isEnough : 1 ≤ pendingCount chan := hasMessage
    simp [isAll, noneDone, oneChan, isEnough]

/-- Consuming by the plan of `wp3b_plan_fire` takes the oldest message of
that Channel. -/
theorem wp3b_consume_fire (chans : List σ.Chan) (chan : σ.Chan) (run : Dex.Run σ)
    (message : σ.Val) (remaining : List σ.Val) (isNodup : chans.Nodup)
    (hasPending : run.pendingMessages chan = message :: remaining) :
    Dex.Run.consumeMessages chans (chans.map (fun other => decide (other = chan))) run =
      (if chan ∈ chans then run.setPendingMessages chan remaining else run,
        chans.map (fun other => if other = chan then some message else none)) := by
  have withoutChan : ∀ (others : List σ.Chan) (current : Dex.Run σ), chan ∉ others →
      Dex.Run.consumeMessages others (others.map (fun other => decide (other = chan))) current =
        (current, others.map (fun other => if other = chan then some message else none)) := by
    intro others current isAbsent
    have sameFlags : others.map (fun other => decide (other = chan)) =
        others.map (fun _ => false) := by
      apply List.map_congr_left
      intro other isOther
      have isDistinct : other ≠ chan := fun isSame => isAbsent (isSame ▸ isOther)
      simp [isDistinct]
    have sameMessages : others.map (fun other => if other = chan then some message else none) =
        others.map (fun _ => (none : Option σ.Val)) := by
      apply List.map_congr_left
      intro other isOther
      have isDistinct : other ≠ chan := fun isSame => isAbsent (isSame ▸ isOther)
      simp [isDistinct]
    rw [sameFlags, sameMessages]
    exact Dex.Run.consumeMessages_none others current
  induction chans with
  | nil => simp [Dex.Run.consumeMessages]
  | cons head laterChans inductionHypothesis =>
    have ⟨headIsNew, laterNodup⟩ := List.nodup_cons.mp isNodup
    by_cases isHead : head = chan
    · subst isHead
      simp only [Dex.Run.consumeMessages, List.map_cons, decide_true, List.headD_cons,
        hasPending, List.tail_cons, ↓reduceIte, List.mem_cons, true_or]
      rw [withoutChan laterChans _ headIsNew]
    · simp only [Dex.Run.consumeMessages, List.map_cons, isHead, decide_false, List.headD_cons,
        List.tail_cons, ↓reduceIte, List.mem_cons]
      rw [inductionHypothesis laterNodup]
      have isOther : ¬ chan = head := fun isSame => isHead isSame.symm
      simp [isOther]

/-- The pending messages after one publication. -/
theorem wp3b_pendingMessages_publish (run : Dex.Run σ) (chan other : σ.Chan) (message : σ.Val) :
    (run.applyPublications [(chan, message)]).pendingMessages other =
      if other = chan then run.pendingMessages chan ++ [message]
      else run.pendingMessages other := by
  by_cases isChan : other = chan
  · subst isChan
    simp [Dex.Run.applyPublications, Dex.Run.pendingMessages, Dex.Run.setPendingMessages,
      AssocList.get?_set_self]
  · simp [Dex.Run.applyPublications, Dex.Run.pendingMessages, Dex.Run.setPendingMessages,
      AssocList.get?_set_other _ _ _ _ isChan, isChan]

end Generic

/-! ## The waits of blocked threads -/

/-- The Channel conditions of an expected wait are distinct. -/
theorem wp3b_expectedWaitShape_nodup (self : Tid) (on : BlockedOn) :
    (expectedWaitShape self on).2.2.Nodup := by
  cases on with
  | timed number deadline chan => cases chan <;> simp [expectedWaitShape]
  | _ => simp [expectedWaitShape]

/-- An expected wait has a Channel condition. -/
theorem wp3b_expectedWaitShape_ne_nil (self : Tid) (on : BlockedOn) :
    (expectedWaitShape self on).2.2 ≠ [] := by
  cases on with
  | timed number deadline chan => cases chan <;> simp [expectedWaitShape]
  | _ => simp [expectedWaitShape]

/-- The frame lemma for a change that wakes nothing: the same threads, the
same Steps, the same timers. -/
theorem MidThreads.frame {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature} {tid : Tid}
    {stepId : Dex.StepExecId signature}
    (mid : MidThreads program lConfig dexConfig run tid stepId)
    (lNext : L.Config) (dexNext : Dex.Config signature) (runNext : Dex.Run signature)
    (hasRunNext : dexNext.run = some runNext) (sameTasks : dexNext.tasks = dexConfig.tasks)
    (sameQueues : ∀ thread, thread ∈ lConfig.threads → thread.core.tid ≠ tid →
      ∀ loaded, thread.phase = .running loaded → ∀ cv,
        cvOperand thread.core thread.code = some cv →
        lNext.cvQueues.get? cv = lConfig.cvQueues.get? cv)
    (sameThreads : lNext.threads = lConfig.threads) (sameSteps : runNext.steps = run.steps)
    (sameTimers : runNext.timers = run.timers)
    (invariantNext : Dex.Run.Invariant IsTimedTimer (runNext.seq + 1) runNext) :
    MidThreads program lNext dexNext runNext tid stepId := by
  have noThread : wakeThreadBy (fun _ => none) = id := by
    funext thread
    rfl
  have noRecord : wakeRecordBy (fun _ => none) = id := by
    funext record
    rfl
  exact mid.wake (fun _ => none) (fun _ => none) lNext dexNext runNext hasRunNext sameTasks
    sameQueues (by rw [sameThreads, noThread, List.map_id])
    (by rw [sameSteps, noRecord, List.map_id])
    (fun _ _ _ isWoken => by cases isWoken) (fun _ _ _ isFired => by cases isFired)
    (fun _ _ _ _ _ _ _ _ _ => Or.inl ⟨rfl, rfl⟩) (fun timer _ => by rw [sameTimers])
    (fun _ _ _ _ isFired => absurd rfl isFired) invariantNext

/-- Publications to Channels that no blocked thread waits on wake no Step. -/
theorem wp3b_publish_noWaiter {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature} {tid : Tid}
    {stepId : Dex.StepExecId signature}
    (mid : MidThreads program lConfig dexConfig run tid stepId)
    (publications : List (Chan × DexVal))
    (noWaiter : ∀ thread, thread ∈ lConfig.threads → ∀ on since,
      thread.phase = .blocked on since → ∀ chan,
      chan ∈ (expectedWaitShape thread.core.tid on).2.2 →
      chan ∉ publications.map (fun publication => publication.1)) :
    run.applyEffects { publications := publications } = run.applyPublications publications := by
  have noWaiters : (run.applyPublications publications).waitersOn
      (publications.map (fun publication => publication.1)) = [] := by
    apply List.eq_nil_iff_forall_not_mem.mpr
    intro step isWaiter
    simp only [Dex.Run.waitersOn, List.mem_map] at isWaiter
    obtain ⟨pair, isSorted, _⟩ := isWaiter
    obtain ⟨record, isRecord, isPair⟩ :=
      List.mem_filterMap.mp ((mem_sortStable _ _ _).mp isSorted)
    rw [(Dex.Run.applyPublications_frame publications run).1] at isRecord
    cases phaseCase : record.phase <;> simp only [phaseCase] at isPair <;> try (cases isPair)
    cases waitCase : record.waitState with
    | none => simp [waitCase] at isPair
    | some waitState =>
      simp only [waitCase] at isPair
      split at isPair
      · rename_i namesPublished
        obtain ⟨thread, on, since, isThread, isBlocked, _, isMatch⟩ :=
          mid.blockedOfWaiting record isRecord phaseCase
        obtain ⟨_, _, found, hasWait, _, _, hasChannels, _⟩ :=
          waiting_of_phaseMatchesStep program lConfig dexConfig thread record on since isBlocked isMatch
        rw [waitCase] at hasWait
        cases hasWait
        obtain ⟨chan, isChan, isPublished⟩ := List.any_eq_true.mp namesPublished
        rw [hasChannels] at isChan
        exact noWaiter thread isThread on since isBlocked chan isChan (by simpa using isPublished)
      · cases isPair
  show ((run.applyWrites []).applyPublications publications).wake
    (publications.map (fun publication => publication.1)) = run.applyPublications publications
  unfold Dex.Run.wake
  have noWrites : run.applyWrites [] = run := rfl
  rw [noWrites, noWaiters]
  rfl

/-- One message published to a Channel goes to the Step of the thread that
blocked first among the threads that wait on the Channel. That Step leaves
`WT` with the message in its condition results; nothing else changes. -/
theorem wp3b_publish_wake {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature} {tid : Tid}
    {stepId : Dex.StepExecId signature}
    (mid : MidThreads program lConfig dexConfig run tid stepId)
    (chan : Chan) (message : DexVal) (thread : L.Thread) (on : BlockedOn) (since : Nat)
    (outcome : WakeOutcome) (isThread : thread ∈ lConfig.threads)
    (isBlocked : thread.phase = .blocked on since)
    (isNamed : chan ∈ (expectedWaitShape thread.core.tid on).2.2)
    (isEarliest : ∀ other, other ∈ lConfig.threads → ∀ otherOn otherSince,
      other.phase = .blocked otherOn otherSince →
      chan ∈ (expectedWaitShape other.core.tid otherOn).2.2 → since ≤ otherSince)
    (isDecoded : decodeOutcome thread (some
      { timersDone := List.replicate (expectedWaitShape thread.core.tid on).2.1 false
        channelMessages := (expectedWaitShape thread.core.tid on).2.2.map (fun other =>
          if other = chan then some message else none) }) = outcome)
    (lNext : L.Config) (dexNext : Dex.Config signature)
    (hasRunNext : dexNext.run = some (run.applyEffects { publications := [(chan, message)] }))
    (sameTasks : dexNext.tasks = dexConfig.tasks)
    (sameQueues : ∀ other, other ∈ lConfig.threads → other.core.tid ≠ tid →
      ∀ loaded, other.phase = .running loaded → ∀ cv,
        cvOperand other.core other.code = some cv →
        lNext.cvQueues.get? cv = lConfig.cvQueues.get? cv)
    (threadsNext : lNext.threads = (lConfig.wakeThread thread.core.tid outcome).threads) :
    MidThreads program lNext dexNext (run.applyEffects { publications := [(chan, message)] })
      tid stepId ∧
    ∀ other, (run.applyEffects { publications := [(chan, message)] }).pendingMessages other =
      run.pendingMessages other := by
  obtain ⟨record, waitState, isRecord, isActive, carries, isWaiting, hasWait, hasKind, hasLength,
    hasChannels, hasDone⟩ := mid.stepOfBlocked thread isThread on since isBlocked
  have idsNodup := mid.runInvariant.idsNodup
  have isEmpty : run.pendingMessages chan = [] :=
    mid.runInvariant.waitersQuiet record waitState ⟨isRecord, isWaiting, hasWait⟩ chan
      (by rw [hasChannels]; exact isNamed)
  obtain ⟨isSupported, noTimerDone, sameLength, _⟩ :=
    mid.runInvariant.waitsSupported record waitState ⟨isRecord, isWaiting, hasWait⟩
  have channelsNodup : waitState.wait.channels.Nodup := by
    rw [hasChannels]
    exact wp3b_expectedWaitShape_nodup _ _
  have namesChan : chan ∈ waitState.wait.channels := by
    rw [hasChannels]
    exact isNamed
  -- The Steps that wait on the Channel carry the threads that are blocked on it.
  have waiterThread : ∀ other otherWait, other ∈ run.steps → other.phase = .waiting →
      other.waitState = some otherWait → chan ∈ otherWait.wait.channels → other.id ≠ record.id →
      waitState.registeredSeq < otherWait.registeredSeq := by
    intro other otherWait isOther otherWaiting otherHasWait otherNames isDistinct
    obtain ⟨otherThread, otherOn, otherSince, isOtherThread, otherBlocked, otherCarries,
      otherMatch⟩ := mid.blockedOfWaiting other isOther otherWaiting
    obtain ⟨_, _, found, foundWait, _, _, foundChannels, _⟩ :=
      waiting_of_phaseMatchesStep program lConfig dexConfig otherThread other otherOn otherSince
        otherBlocked otherMatch
    rw [otherHasWait] at foundWait
    cases foundWait
    have isLater : since ≤ otherSince :=
      isEarliest otherThread isOtherThread otherOn otherSince otherBlocked
        (by rw [← foundChannels]; exact otherNames)
    have isOtherActive : other.phase.isActive = true := by
      rw [otherWaiting]
      rfl
    have isStrict : since < otherSince := by
      rcases Nat.lt_or_eq_of_le isLater with isLess | isSame
      · exact isLess
      · exfalso
        subst isSame
        have sameThread := mid.sinceInjective thread isThread otherThread isOtherThread on
          otherOn since isBlocked otherBlocked
        subst sameThread
        exact isDistinct (mid.stepOfThread_unique thread isThread other record isOther isRecord
          isOtherActive isActive otherCarries carries ▸ rfl)
    exact (mid.blockingOrder thread otherThread record other on since otherOn otherSince waitState
      otherWait isThread isOtherThread isRecord isOther isActive isOtherActive carries
      otherCarries isBlocked otherBlocked hasWait otherHasWait).mp isStrict
  -- The run after the publication.
  have isPublished : run.applyEffects { publications := [(chan, message)] } =
      (run.setPendingMessages chan [message]).wake [chan] := by
    show ((run.applyWrites []).applyPublications [(chan, message)]).wake [chan] = _
    have noWrites : run.applyWrites [] = run := rfl
    rw [noWrites]
    simp [Dex.Run.applyPublications, isEmpty]
  have isFound : (run.setPendingMessages chan [message]).findStep? record.id = some record :=
    Dex.Run.findStep?_of_mem_nodup run record isRecord idsNodup
  have isFirst : ((run.setPendingMessages chan [message]).waitersOn [chan]).head? =
      some record.id := by
    simp only [Dex.Run.waitersOn, List.head?_map]
    rw [head?_sortStable_of_least _ (waitState.registeredSeq, record.id)]
    · rfl
    · refine List.mem_filterMap.mpr ⟨record, isRecord, ?_⟩
      simp [isWaiting, hasWait, namesChan]
    · simp
    · intro candidate isCandidate
      obtain ⟨other, isOther, isPair⟩ := List.mem_filterMap.mp isCandidate
      have isOtherOld : other ∈ run.steps := isOther
      cases phaseCase : other.phase <;> simp only [phaseCase] at isPair <;> try (cases isPair)
      cases waitCase : other.waitState with
      | none => simp [waitCase] at isPair
      | some otherWait =>
        simp only [waitCase] at isPair
        split at isPair
        · rename_i otherNames
          have isOtherPair : (otherWait.registeredSeq, other.id) = candidate :=
            Option.some.inj isPair
          subst isOtherPair
          have otherNamesChan : chan ∈ otherWait.wait.channels := by simpa using otherNames
          by_cases sameId : other.id = record.id
          · have isSame := eq_of_nodup_map (fun candidate => candidate.id) run.steps idsNodup
              isOtherOld isRecord sameId
            subst isSame
            rw [hasWait] at waitCase
            cases waitCase
            exact Or.inl rfl
          · have isLater := waiterThread other otherWait isOtherOld phaseCase waitCase
              otherNamesChan sameId
            exact Or.inr ⟨by simpa using Nat.le_of_lt isLater, by simpa using isLater⟩
        · cases isPair
  have pendingPublished : ∀ other, (run.setPendingMessages chan [message]).pendingMessages other =
      if other = chan then [message] else run.pendingMessages other := by
    intro other
    by_cases isChan : other = chan
    · subst isChan
      simp [Dex.Run.pendingMessages, Dex.Run.setPendingMessages, AssocList.get?_set_self]
    · simp [Dex.Run.pendingMessages, Dex.Run.setPendingMessages,
        AssocList.get?_set_other _ _ _ _ isChan, isChan]
  have isPlanned : Dex.Run.plan waitState.wait
      (fun other => ((run.setPendingMessages chan [message]).pendingMessages other).length)
      waitState.timersDone =
      some (waitState.wait.channels.map (fun other => decide (other = chan))) := by
    apply wp3b_plan_fire waitState.wait _ waitState.timersDone chan isSupported noTimerDone
      sameLength namesChan channelsNodup
    · simp [pendingPublished]
    · intro other isOther isDistinct
      simp only [pendingPublished, isDistinct, ↓reduceIte, List.length_eq_zero_iff]
      exact mid.runInvariant.waitersQuiet record waitState ⟨isRecord, isWaiting, hasWait⟩ other
        isOther
  have isConsumed := wp3b_consume_fire waitState.wait.channels chan
    ((run.setPendingMessages chan [message]).disarmTimersOf record.id) message [] channelsNodup
    (by
      have isPending := pendingPublished chan
      simp only [↓reduceIte] at isPending
      exact isPending)
  simp only [namesChan, ↓reduceIte] at isConsumed
  have isEvaluated := Dex.Run.evalStep_of_plan_some (run.setPendingMessages chan [message])
    record.id record waitState _ isFound isWaiting hasWait isPlanned
  rw [isConsumed] at isEvaluated
  -- The run after the wake.
  let conditionResults : Dex.ConditionResults signature :=
    { timersDone := waitState.timersDone
      channelMessages := waitState.wait.channels.map (fun other =>
        if other = chan then some message else none) }
  let woken : Dex.Run signature :=
    (((run.setPendingMessages chan [message]).disarmTimersOf record.id).setPendingMessages chan
      []).updateStep record.id (fun current =>
        { current with
          conditionResults := some conditionResults
          phase := .execute
          invocation := some { method := .execute, state := .pending } })
  have isEvaluated' : (run.setPendingMessages chan [message]).evalStep record.id = woken :=
    isEvaluated
  have pendingWoken : ∀ other, woken.pendingMessages other = run.pendingMessages other := by
    intro other
    by_cases isChan : other = chan
    · subst isChan
      rw [isEmpty]
      simp [woken, Dex.Run.pendingMessages, Dex.Run.updateStep, Dex.Run.setPendingMessages,
        Dex.Run.disarmTimersOf, AssocList.get?_set_self]
    · simp [woken, Dex.Run.pendingMessages, Dex.Run.updateStep, Dex.Run.setPendingMessages,
        Dex.Run.disarmTimersOf, AssocList.get?_set_other _ _ _ _ isChan]
  have stepsWoken : woken.steps = run.steps.map (fun current =>
      if current.id = record.id then
        { current with
          conditionResults := some conditionResults
          phase := .execute
          invocation := some { method := .execute, state := .pending } }
      else current) := rfl
  have timersWoken : woken.timers =
      run.timers.filter (fun entry => !decide (entry.1.step = record.id)) := rfl
  have isWoken : run.applyEffects { publications := [(chan, message)] } = woken := by
    rw [isPublished]
    unfold Dex.Run.wake
    cases waitersCase : (run.setPendingMessages chan [message]).waitersOn [chan] with
    | nil => simp [waitersCase] at isFirst
    | cons first laterWaiters =>
      have isRecordFirst : first = record.id := by simpa [waitersCase] using isFirst
      subst isRecordFirst
      simp only [List.foldl_cons]
      rw [isEvaluated']
      apply Dex.Run.foldl_evalStep_of_unplanned
      intro step _ other otherWait otherFound otherWaiting otherHasWait
      obtain ⟨isOtherWoken, otherId⟩ := Dex.Run.mem_of_findStep? woken step other otherFound
      rw [stepsWoken] at isOtherWoken
      obtain ⟨original, isOriginal, isChanged⟩ := List.mem_map.mp isOtherWoken
      by_cases isOwn : original.id = record.id
      · simp only [isOwn, ↓reduceIte] at isChanged
        rw [← isChanged] at otherWaiting
        cases otherWaiting
      · simp only [isOwn, ↓reduceIte] at isChanged
        subst isChanged
        obtain ⟨otherSupported, otherNoTimer, _, _⟩ :=
          mid.runInvariant.waitsSupported original otherWait
            ⟨isOriginal, otherWaiting, otherHasWait⟩
        obtain ⟨otherThread, otherOn, otherSince, _, otherBlocked, _, otherMatch⟩ :=
          mid.blockedOfWaiting original isOriginal otherWaiting
        obtain ⟨_, _, found, foundWait, _, _, foundChannels, _⟩ :=
          waiting_of_phaseMatchesStep program lConfig dexConfig otherThread original otherOn
            otherSince otherBlocked otherMatch
        rw [otherHasWait] at foundWait
        cases foundWait
        apply Dex.Run.plan_none_of_quiet otherWait.wait _ otherWait.timersDone otherSupported
          (by rw [foundChannels]; exact wp3b_expectedWaitShape_ne_nil _ _) otherNoTimer
        intro named isNamedByOther
        rw [pendingWoken named]
        exact List.length_eq_zero_iff.mpr
          (mid.runInvariant.waitersQuiet original otherWait
            ⟨isOriginal, otherWaiting, otherHasWait⟩ named isNamedByOther)
  rw [isWoken] at hasRunNext ⊢
  refine ⟨?_, pendingWoken⟩
  have sameRecord : ∀ other, other ∈ run.steps → other.id = record.id → other = record :=
    fun other isOther sameId =>
      eq_of_nodup_map (fun candidate => candidate.id) run.steps idsNodup isOther isRecord sameId
  have sameThread : ∀ other, other ∈ lConfig.threads → other.core.tid = thread.core.tid →
      other = thread := fun other isOther sameId =>
    eq_of_nodup_map (fun candidate => candidate.core.tid) lConfig.threads mid.threadIdsNodup
      isOther isThread sameId
  apply mid.wake
    (fun other => if other.core.tid = thread.core.tid then some outcome else none)
    (fun other => if other.id = record.id then some conditionResults else none)
    lNext dexNext woken hasRunNext sameTasks sameQueues
  · rw [threadsNext]
    simp only [L.Config.wakeThread, L.Config.setPhase, L.Config.updateThread]
    apply List.map_congr_left
    intro other _
    by_cases isOwn : other.core.tid = thread.core.tid <;> simp [wakeThreadBy, isOwn]
  · rw [stepsWoken]
    apply List.map_congr_left
    intro other _
    by_cases isOwn : other.id = record.id <;> simp [wakeRecordBy, isOwn]
  · intro other isOther wokenOutcome isWokenOther
    by_cases isOwn : other.core.tid = thread.core.tid
    · rw [sameThread other isOther isOwn]
      exact ⟨on, since, isBlocked⟩
    · simp [isOwn] at isWokenOther
  · intro other isOther results isFired
    by_cases isOwn : other.id = record.id
    · rw [sameRecord other isOther isOwn]
      exact isWaiting
    · simp [isOwn] at isFired
  · intro other isOther otherRecord isOtherRecord otherActive otherCarries otherOn otherSince
      otherBlocked
    by_cases isOwn : other.core.tid = thread.core.tid
    · have isSame := sameThread other isOther isOwn
      subst isSame
      have isSameRecord := mid.stepOfThread_unique other isOther otherRecord record isOtherRecord
        isRecord otherActive isActive otherCarries carries
      subst isSameRecord
      right
      refine ⟨outcome, conditionResults, by simp, by simp, ?_⟩
      rw [← isDecoded]
      simp only [conditionResults, hasDone, List.map_const', hasLength, hasChannels]
    · left
      refine ⟨by simp [isOwn], ?_⟩
      have isOtherStep : otherRecord.id ≠ record.id := by
        intro sameId
        rw [sameRecord otherRecord isOtherRecord sameId] at otherCarries
        apply isOwn
        rw [← threadOfInput_of_carries_of_booted program other record (mid.noBoot other isOther).1
            (mid.noBoot other isOther).2 otherCarries,
          threadOfInput_of_carries_of_booted program thread record (mid.noBoot thread isThread).1
            (mid.noBoot thread isThread).2 carries]
      simp [isOtherStep]
  · intro timer allKept
    rw [timersWoken]
    have isFiltered := AssocList.get?_filter
      (fun (candidate : Dex.TimerId signature) => !decide (candidate.step = record.id))
      run.timers timer
    rw [isFiltered]
    have isOtherStep : timer.step ≠ record.id := by
      intro sameId
      have isKept := allKept record isRecord sameId.symm
      simp at isKept
    simp [isOtherStep]
  · intro timer other isOther sameId isFired
    have isOwn : other.id = record.id := by
      by_cases isOwn : other.id = record.id
      · exact isOwn
      · simp [isOwn] at isFired
    rw [timersWoken]
    have isFiltered := AssocList.get?_filter
      (fun (candidate : Dex.TimerId signature) => !decide (candidate.step = record.id))
      run.timers timer
    rw [isFiltered]
    simp [← sameId, isOwn]
  · rw [← isWoken]
    have kept := mid.runInvariant.applyEffects { publications := [(chan, message)] }
    rw [Dex.Run.seq_applyEffects]
    exact kept

end DexExpressiveness
