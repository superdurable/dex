import DexExpressiveness.Simulation.Completion.Wake

/-!
# `wake` of several `Until(c.ForOne())` waits on distinct Channels

`broadcast(cv)` publishes to the Channel of every waiter and then wakes them
all in one `wake` (spec §4.2, §8.3). `wp3b_foldl_evalStep_fire` computes the
result of evaluating a list of Steps when each of them that waits has a wait
`Until(c.ForOne())` on a Channel of its own with a pending message: each
consumes the oldest message of its Channel, in any order.
-/

namespace DexExpressiveness

variable {σ : Dex.Signature}

/-- A Step after its `Until(c.ForOne())` wait fired with the oldest message
of its Channel. -/
def firedRecord (run : Dex.Run σ) (chanOf : Dex.StepExecId σ → σ.Chan)
    (record : Dex.StepRecord σ) : Dex.StepRecord σ :=
  { record with
    conditionResults := some
      { timersDone := []
        channelMessages := [(run.pendingMessages (chanOf record.id)).head?] }
    phase := .execute
    invocation := some { method := .execute, state := .pending } }

/-- Every Step of `steps` that waits has a wait `Until(c.ForOne())` on its
own Channel `chanOf e`, with a pending message and no timer. -/
structure FiresAll (chanOf : Dex.StepExecId σ → σ.Chan) (steps : List (Dex.StepExecId σ))
    (run : Dex.Run σ) : Prop where
  /-- Step execution IDs are unique. -/
  idsNodup : (run.steps.map (fun record => record.id)).Nodup
  /-- The wait of each waiting Step of the list, and its pending message. -/
  fires : ∀ record, record ∈ run.steps → record.id ∈ steps → record.phase = .waiting →
    ∃ waitState, record.waitState = some waitState ∧
      waitState.wait = { kind := .all, timerSeconds := [], channels := [chanOf record.id] } ∧
      waitState.timersDone = [] ∧ run.pendingMessages (chanOf record.id) ≠ []
  /-- Two waiting Steps of the list never share a Channel. -/
  isInjective : ∀ first second, first ∈ run.steps → second ∈ run.steps → first.id ∈ steps →
    second.id ∈ steps → first.phase = .waiting → second.phase = .waiting →
    chanOf first.id = chanOf second.id → first.id = second.id
  /-- No timer of a waiting Step of the list is armed. -/
  noTimers : ∀ (timer : Dex.TimerId σ) record, record ∈ run.steps → record.id = timer.step →
    record.id ∈ steps → record.phase = .waiting → run.timers.get? timer = none

/-- `eval(e)` of the first Step of the list, when it waits. -/
theorem wp3b_evalStep_fire (chanOf : Dex.StepExecId σ → σ.Chan) (step : Dex.StepExecId σ)
    (later : List (Dex.StepExecId σ)) (run : Dex.Run σ)
    (firesAll : FiresAll chanOf (step :: later) run) (record : Dex.StepRecord σ)
    (isRecord : record ∈ run.steps) (hasId : record.id = step)
    (isWaiting : record.phase = .waiting) :
    (run.evalStep step).steps = run.steps.map (fun candidate =>
      if candidate.id = step then firedRecord run chanOf candidate else candidate) ∧
    (∀ chan, (run.evalStep step).pendingMessages chan =
      if chan = chanOf step then (run.pendingMessages chan).tail
      else run.pendingMessages chan) ∧
    (∀ timer, (run.evalStep step).timers.get? timer = run.timers.get? timer) := by
  obtain ⟨waitState, hasWait, isWait, noneDone, hasPending⟩ := firesAll.fires record isRecord
    (by rw [hasId]; exact List.mem_cons_self ..) isWaiting
  rw [hasId] at isWait hasPending
  cases pendingCase : run.pendingMessages (chanOf step) with
  | nil => exact absurd pendingCase hasPending
  | cons message remaining =>
    have isFound : run.findStep? step = some record := by
      rw [← hasId]
      exact Dex.Run.findStep?_of_mem_nodup run record isRecord firesAll.idsNodup
    have isPlanned : Dex.Run.plan waitState.wait (fun chan => (run.pendingMessages chan).length)
        waitState.timersDone = some [true] := by
      simp [isWait, noneDone, Dex.Run.plan, Dex.Wait.hasNoCondition, pendingCase]
    have isEvaluated := Dex.Run.evalStep_of_plan_some run step record waitState [true] isFound
      isWaiting hasWait isPlanned
    rw [isWait] at isEvaluated
    have isConsumed : Dex.Run.consumeMessages [chanOf step] [true] (run.disarmTimersOf step) =
        ((run.disarmTimersOf step).setPendingMessages (chanOf step) remaining, [some message]) :=
      Dex.Run.consumeMessages_first (chanOf step) [] (run.disarmTimersOf step) message remaining
        pendingCase
    simp only [isConsumed] at isEvaluated
    rw [isEvaluated]
    refine ⟨?_, ?_, ?_⟩
    · show run.steps.map _ = _
      apply List.map_congr_left
      intro candidate _
      by_cases isStep : candidate.id = step
      · simp [isStep, firedRecord, noneDone, pendingCase]
      · simp [isStep]
    · intro chan
      by_cases isChan : chan = chanOf step
      · subst isChan
        simp [Dex.Run.pendingMessages, Dex.Run.updateStep, Dex.Run.setPendingMessages,
          Dex.Run.disarmTimersOf, AssocList.get?_set_self] at pendingCase ⊢
        rw [pendingCase]
        rfl
      · simp [Dex.Run.pendingMessages, Dex.Run.updateStep, Dex.Run.setPendingMessages,
          Dex.Run.disarmTimersOf, AssocList.get?_set_other _ _ _ _ isChan, isChan]
    · intro timer
      show AssocList.get? (run.timers.filter (fun entry => !decide (entry.1.step = step))) timer = _
      rw [AssocList.wp3b_get?_filter (fun candidate : Dex.TimerId σ =>
        !decide (candidate.step = step)) run.timers timer]
      by_cases isStep : timer.step = step
      · simp only [isStep, decide_true, Bool.not_true, Bool.false_eq_true, ↓reduceIte]
        exact (firesAll.noTimers timer record isRecord (hasId.trans isStep.symm)
          (by rw [hasId]; exact List.mem_cons_self ..) isWaiting).symm
      · simp [isStep]

/-- Evaluating a list of Steps of which each waiting one has a wait
`Until(c.ForOne())` on its own non-empty Channel: every such Step fires with
the oldest message of its Channel, and nothing else changes. -/
theorem wp3b_foldl_evalStep_fire (chanOf : Dex.StepExecId σ → σ.Chan)
    (steps : List (Dex.StepExecId σ)) (run : Dex.Run σ) (firesAll : FiresAll chanOf steps run) :
    (steps.foldl Dex.Run.evalStep run).steps = run.steps.map (fun record =>
      if record.id ∈ steps ∧ record.phase = .waiting then firedRecord run chanOf record
      else record) ∧
    (∀ chan, (∀ record, record ∈ run.steps → record.id ∈ steps → record.phase = .waiting →
        chanOf record.id ≠ chan) →
      (steps.foldl Dex.Run.evalStep run).pendingMessages chan = run.pendingMessages chan) ∧
    (∀ record, record ∈ run.steps → record.id ∈ steps → record.phase = .waiting →
      (steps.foldl Dex.Run.evalStep run).pendingMessages (chanOf record.id) =
        (run.pendingMessages (chanOf record.id)).tail) ∧
    (∀ timer, (steps.foldl Dex.Run.evalStep run).timers.get? timer = run.timers.get? timer) := by
  induction steps generalizing run with
  | nil =>
    refine ⟨?_, fun _ _ => rfl, fun record _ isMember => by simp at isMember, fun _ => rfl⟩
    show run.steps = _
    have isSame : (fun record : Dex.StepRecord σ =>
        if record.id ∈ ([] : List (Dex.StepExecId σ)) ∧ record.phase = .waiting then
          firedRecord run chanOf record else record) = id := by
      funext record
      simp
    rw [isSame, List.map_id]
  | cons step later inductionHypothesis =>
    simp only [List.foldl_cons]
    have sameRecord : ∀ first, first ∈ run.steps → ∀ second, second ∈ run.steps →
        first.id = second.id → first = second := fun first isFirst second isSecond sameId =>
      eq_of_nodup_map (fun candidate => candidate.id) run.steps firesAll.idsNodup isFirst
        isSecond sameId
    by_cases isFiring : ∃ record, record ∈ run.steps ∧ record.id = step ∧ record.phase = .waiting
    · obtain ⟨record, isRecord, hasId, isWaiting⟩ := isFiring
      obtain ⟨firstSteps, firstPending, firstTimers⟩ :=
        wp3b_evalStep_fire chanOf step later run firesAll record isRecord hasId isWaiting
      have isHead : record.id ∈ step :: later := by
        rw [hasId]
        exact List.mem_cons_self ..
      -- A waiting Step of the rest of the list is unchanged, with its Channel.
      have laterRecord : ∀ candidate, candidate ∈ (run.evalStep step).steps →
          candidate.phase = .waiting →
          candidate ∈ run.steps ∧ candidate.id ≠ step := by
        intro candidate isCandidate candidateWaiting
        rw [firstSteps] at isCandidate
        obtain ⟨original, isOriginal, isChanged⟩ := List.mem_map.mp isCandidate
        by_cases isStep : original.id = step
        · simp only [isStep, ↓reduceIte] at isChanged
          rw [← isChanged] at candidateWaiting
          cases candidateWaiting
        · simp only [isStep, ↓reduceIte] at isChanged
          rw [← isChanged]
          exact ⟨isOriginal, isStep⟩
      have otherChan : ∀ candidate, candidate ∈ run.steps → candidate.id ≠ step →
          candidate.id ∈ later → candidate.phase = .waiting →
          chanOf candidate.id ≠ chanOf step := by
        intro candidate isCandidate isOther isLater candidateWaiting sameChan
        apply isOther
        rw [← hasId]
        exact firesAll.isInjective candidate record isCandidate isRecord
          (List.mem_cons_of_mem _ isLater) isHead candidateWaiting isWaiting
          (by rw [hasId]; exact sameChan)
      have laterFires : FiresAll chanOf later (run.evalStep step) :=
        { idsNodup := by
            rw [(Dex.Run.evalStep_frame run step).1]
            exact firesAll.idsNodup
          fires := by
            intro candidate isCandidate isLater candidateWaiting
            obtain ⟨isOld, isOther⟩ := laterRecord candidate isCandidate candidateWaiting
            obtain ⟨waitState, hasWait, isWait, noneDone, hasPending⟩ :=
              firesAll.fires candidate isOld (List.mem_cons_of_mem _ isLater) candidateWaiting
            refine ⟨waitState, hasWait, isWait, noneDone, ?_⟩
            rw [firstPending]
            simp only [otherChan candidate isOld isOther isLater candidateWaiting, ↓reduceIte]
            exact hasPending
          isInjective := by
            intro first second isFirst isSecond firstLater secondLater firstWaiting secondWaiting
            exact firesAll.isInjective first second (laterRecord first isFirst firstWaiting).1
              (laterRecord second isSecond secondWaiting).1 (List.mem_cons_of_mem _ firstLater)
              (List.mem_cons_of_mem _ secondLater) firstWaiting secondWaiting
          noTimers := by
            intro timer candidate isCandidate candidateId isLater candidateWaiting
            rw [firstTimers]
            exact firesAll.noTimers timer candidate
              (laterRecord candidate isCandidate candidateWaiting).1 candidateId
              (List.mem_cons_of_mem _ isLater) candidateWaiting }
      obtain ⟨laterSteps, laterKept, laterFired, laterTimers⟩ :=
        inductionHypothesis (run.evalStep step) laterFires
      refine ⟨?_, ?_, ?_, ?_⟩
      · rw [laterSteps, firstSteps, List.map_map]
        apply List.map_congr_left
        intro candidate isCandidate
        by_cases isStep : candidate.id = step
        · have isSame := sameRecord candidate isCandidate record isRecord (isStep.trans hasId.symm)
          subst isSame
          simp [isStep, firedRecord, isWaiting]
        · simp only [Function.comp, isStep, ↓reduceIte, List.mem_cons, false_or]
          by_cases isFired : candidate.id ∈ later ∧ candidate.phase = .waiting
          · simp only [isFired, and_self, ↓reduceIte, firedRecord]
            rw [firstPending]
            simp [otherChan candidate isCandidate isStep isFired.1 isFired.2]
          · simp [isFired]
      · intro chan noFiring
        have isOtherChan : chan ≠ chanOf step := by
          intro isChan
          exact noFiring record isRecord isHead isWaiting (by rw [hasId]; exact isChan.symm)
        rw [laterKept chan (fun candidate isCandidate isLater candidateWaiting =>
          noFiring candidate (laterRecord candidate isCandidate candidateWaiting).1
            (List.mem_cons_of_mem _ isLater) candidateWaiting), firstPending]
        simp [isOtherChan]
      · intro candidate isCandidate isMember candidateWaiting
        by_cases isStep : candidate.id = step
        · rw [isStep]
          rw [laterKept (chanOf step) (fun other isOther isLater otherWaiting sameChan => by
            obtain ⟨isOld, isDistinct⟩ := laterRecord other isOther otherWaiting
            exact otherChan other isOld isDistinct isLater otherWaiting sameChan), firstPending]
          simp
        · have isLater : candidate.id ∈ later := by
            rcases List.mem_cons.mp isMember with isHeadId | isLater
            · exact absurd isHeadId isStep
            · exact isLater
          have isKept : candidate ∈ (run.evalStep step).steps := by
            rw [firstSteps]
            exact List.mem_map.mpr ⟨candidate, isCandidate, by simp [isStep]⟩
          rw [laterFired candidate isKept isLater candidateWaiting, firstPending]
          simp [otherChan candidate isCandidate isStep isLater candidateWaiting]
      · intro timer
        rw [laterTimers, firstTimers]
    · have isSkipped : run.evalStep step = run := by
        apply wp3b_evalStep_quiet
        intro record waitState isFound isWaiting _
        obtain ⟨isRecord, hasId⟩ := Dex.Run.mem_of_findStep? run step record isFound
        exact absurd ⟨record, isRecord, hasId, isWaiting⟩ isFiring
      rw [isSkipped]
      have notHead : ∀ record, record ∈ run.steps → record.phase = .waiting →
          record.id ≠ step := fun record isRecord isWaiting hasId =>
        isFiring ⟨record, isRecord, hasId, isWaiting⟩
      have laterFires : FiresAll chanOf later run :=
        { idsNodup := firesAll.idsNodup
          fires := fun record isRecord isLater isWaiting =>
            firesAll.fires record isRecord (List.mem_cons_of_mem _ isLater) isWaiting
          isInjective := fun first second isFirst isSecond firstLater secondLater =>
            firesAll.isInjective first second isFirst isSecond (List.mem_cons_of_mem _ firstLater)
              (List.mem_cons_of_mem _ secondLater)
          noTimers := fun timer record isRecord hasId isLater isWaiting =>
            firesAll.noTimers timer record isRecord hasId (List.mem_cons_of_mem _ isLater)
              isWaiting }
      obtain ⟨laterSteps, laterKept, laterFired, laterTimers⟩ :=
        inductionHypothesis run laterFires
      have isLaterOf : ∀ record, record ∈ run.steps → record.id ∈ step :: later →
          record.phase = .waiting → record.id ∈ later := by
        intro record isRecord isMember isWaiting
        rcases List.mem_cons.mp isMember with isHeadId | isLater
        · exact absurd isHeadId (notHead record isRecord isWaiting)
        · exact isLater
      refine ⟨?_, ?_, ?_, laterTimers⟩
      · rw [laterSteps]
        apply List.map_congr_left
        intro record isRecord
        by_cases isWaiting : record.phase = .waiting
        · have isOther := notHead record isRecord isWaiting
          simp [isWaiting, isOther]
        · simp [isWaiting]
      · intro chan noFiring
        exact laterKept chan (fun record isRecord isLater isWaiting =>
          noFiring record isRecord (List.mem_cons_of_mem _ isLater) isWaiting)
      · intro record isRecord isMember isWaiting
        exact laterFired record isRecord (isLaterOf record isRecord isMember isWaiting) isWaiting

end DexExpressiveness
