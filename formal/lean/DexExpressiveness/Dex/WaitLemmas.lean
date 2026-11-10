import DexExpressiveness.Dex.RunLemmas

/-!
# Dex-side lemmas for the synchronization rows of spec §8.3

How `eval`, `register`, and `wake` (spec §4.2) behave on the waits the
translation uses. Everything here is proved.
-/

namespace DexExpressiveness.Dex

variable {σ : Signature}

namespace Run

/-- `eval(e)` changes nothing when `plan` finds no satisfied candidate. -/
theorem evalStep_of_plan_none (run : Run σ) (step : StepExecId σ) (record : StepRecord σ)
    (waitState : WaitState σ) (isFound : run.findStep? step = some record)
    (hasWaitState : record.waitState = some waitState)
    (isNotPlanned : plan waitState.wait (fun chan => (run.pendingMessages chan).length)
      waitState.timersDone = none) :
    run.evalStep step = run := by
  unfold evalStep
  rw [isFound]
  cases phaseCase : record.phase <;> simp [hasWaitState, isNotPlanned, phaseCase]

/-- `eval(e)` of a Step that waits and whose plan is `consumeFlags`. -/
theorem evalStep_of_plan_some (run : Run σ) (step : StepExecId σ) (record : StepRecord σ)
    (waitState : WaitState σ) (consumeFlags : List Bool)
    (isFound : run.findStep? step = some record) (isWaiting : record.phase = .waiting)
    (hasWaitState : record.waitState = some waitState)
    (isPlanned : plan waitState.wait (fun chan => (run.pendingMessages chan).length)
      waitState.timersDone = some consumeFlags) :
    run.evalStep step =
      (consumeMessages waitState.wait.channels consumeFlags (run.disarmTimersOf step)).1.updateStep
        step (fun current =>
          { current with
            conditionResults := some
              { timersDone := waitState.timersDone
                channelMessages :=
                  (consumeMessages waitState.wait.channels consumeFlags
                    (run.disarmTimersOf step)).2 }
            phase := .execute
            invocation := some { method := .execute, state := .pending } }) := by
  unfold evalStep
  rw [isFound]
  simp [isWaiting, hasWaitState, isPlanned]

/-- `eval(e)` changes nothing when the Step does not wait. -/
theorem evalStep_of_not_waiting (run : Run σ) (step : StepExecId σ) (record : StepRecord σ)
    (isFound : run.findStep? step = some record) (isNotWaiting : record.phase ≠ .waiting) :
    run.evalStep step = run := by
  unfold evalStep
  rw [isFound]
  cases phaseCase : record.phase <;> simp_all

end Run

/-- Timers are never done when armed (spec §4.2 `register`: "A zero-second
timer is armed, not completed"; spec §8.1: "A timer is never complete at the
moment it is armed, even for `d = 0`"): with no pending message, registering
`AnyOf(Timer(d), ...)` leaves the Step waiting with its timer armed at
`now + d · 10⁶`. -/
theorem registerWait_timer_blocks (run : Run σ) (step : StepExecId σ) (record : StepRecord σ)
    (seconds : Nat) (chans : List σ.Chan) (now : Nat)
    (isFound : run.findStep? step = some record)
    (areEmpty : ∀ chan, chan ∈ chans → run.pendingMessages chan = []) :
    let registered := run.registerWait step
      { kind := .any, timerSeconds := [seconds], channels := chans } now
    (∃ current, registered.findStep? step = some current ∧ current.phase = .waiting) ∧
    registered.timers.get? { step := step, index := 0 } = some (now + seconds * 1000000) := by
  have isId : record.id = step := by
    have isMatch := List.find?_some isFound
    simpa using isMatch
  simp only [Run.registerWait, Wait.hasNoCondition, List.isEmpty_cons, Bool.false_and,
    Bool.false_eq_true, ↓reduceIte, List.map_cons, List.map_nil, Run.armTimers]
  have isUpdated := Run.findStep?_updateStep_self
    { run with timers := run.timers.set { step := step, index := 0 } (now + seconds * 1000000) }
    step (fun current =>
      { current with
        phase := .waiting
        waitState := some
          { wait := { kind := .any, timerSeconds := [seconds], channels := chans }
            fireAt := [now + seconds * 1000000]
            timersDone := [false]
            registeredSeq := run.seq } }) record isFound rfl
  rw [Run.evalStep_of_plan_none _ step _ _ isUpdated rfl]
  · exact ⟨⟨_, isUpdated, rfl⟩, AssocList.get?_set_self _ _ _⟩
  · simp only [Run.plan, Wait.hasNoCondition, List.isEmpty_cons, Bool.false_and,
      Bool.false_eq_true, ↓reduceIte, List.any_cons, id, List.any_nil, Bool.or_false]
    apply Run.planAnyChannel_eq_none
    intro chan isMember
    have isEmpty := areEmpty chan isMember
    simp only [Run.pendingMessages] at isEmpty ⊢
    simp [Run.updateStep, isEmpty]

/-- AnyOf at registration (spec §8.3: "at registration the Channel conditions
decide in declaration order: the operation's own Channel before
`cancel/self`"): when the first Channel has a pending message, the wait is
satisfied at once by that message, also when a timer condition is present. -/
theorem registerWait_any_firstChannelWins (run : Run σ) (step : StepExecId σ)
    (record : StepRecord σ) (timerSeconds : List Nat) (firstChan : σ.Chan)
    (laterChans : List σ.Chan) (message : σ.Val) (laterMessages : List σ.Val) (now : Nat)
    (isFound : run.findStep? step = some record)
    (hasMessage : run.pendingMessages firstChan = message :: laterMessages) :
    let registered := run.registerWait step
      { kind := .any, timerSeconds := timerSeconds, channels := firstChan :: laterChans } now
    (∃ current results,
      registered.findStep? step = some current ∧ current.phase = .execute ∧
      current.conditionResults = some results ∧
      results.channelMessages = some message :: laterChans.map (fun _ => none)) ∧
    registered.pendingMessages firstChan = laterMessages ∧
    registered.timers = run.timers.filter (fun entry => !decide (entry.1.step = step)) := by
  simp only [Run.registerWait, Wait.hasNoCondition, List.isEmpty_cons, Bool.and_false,
    Bool.false_eq_true, ↓reduceIte]
  have isUpdated := Run.findStep?_updateStep_self
    { run with
      timers := Run.armTimers step (timerSeconds.map (fun seconds => now + seconds * 1000000)) 0
        run.timers }
    step (fun current =>
      { current with
        phase := .waiting
        waitState := some
          { wait := { kind := .any, timerSeconds := timerSeconds, channels := firstChan :: laterChans }
            fireAt := timerSeconds.map (fun seconds => now + seconds * 1000000)
            timersDone := timerSeconds.map (fun _ => false)
            registeredSeq := run.seq } }) record isFound rfl
  have isPlanned : Run.plan
      ({ kind := .any, timerSeconds := timerSeconds, channels := firstChan :: laterChans } : Wait σ)
      (fun chan => (run.pendingMessages chan).length) (timerSeconds.map (fun _ => false)) =
      some (true :: laterChans.map (fun _ => false)) := by
    have noTimerDone : (timerSeconds.map (fun _ => false)).any id = false := by simp
    simp only [Run.plan, Wait.hasNoCondition, List.isEmpty_cons, Bool.and_false,
      Bool.false_eq_true, ↓reduceIte, noTimerDone]
    exact Run.planAnyChannel_first _ _ _ (by simp [hasMessage])
  rw [Run.evalStep_of_plan_some _ step _ _ _ isUpdated rfl rfl isPlanned]
  rw [Run.consumeMessages_first (message := message) (remaining := laterMessages)]
  case hasPending => exact hasMessage
  refine ⟨⟨_, _, Run.findStep?_updateStep_self _ step _ _ isUpdated rfl, rfl, rfl, rfl⟩, ?_, ?_⟩
  · simp [Run.pendingMessages, Run.updateStep, Run.setPendingMessages, AssocList.get?_set_self]
  · simp only [Run.updateStep, Run.setPendingMessages, Run.disarmTimersOf]
    exact Run.filter_armTimers step _ 0 run.timers

/-- What `wake_servesEarliestWaiter` establishes about a run: the Step has
the message in its condition results and is ready to Execute, and the Channel
is empty. -/
def HasReceived (run : Run σ) (step : StepExecId σ) (chan : σ.Chan) (message : σ.Val) : Prop :=
  (∃ record results,
    run.findStep? step = some record ∧ record.phase = .execute ∧
    record.conditionResults = some results ∧ results.channelMessages = [some message]) ∧
  run.pendingMessages chan = []

/-- Evaluating further Steps does not disturb a Step that has received its
message from a Channel that is empty again. -/
theorem hasReceived_foldl_evalStep (steps : List (StepExecId σ)) (run : Run σ)
    (step : StepExecId σ) (chan : σ.Chan) (message : σ.Val)
    (hasReceived : HasReceived run step chan message) :
    HasReceived (steps.foldl Run.evalStep run) step chan message := by
  induction steps generalizing run with
  | nil => exact hasReceived
  | cons head laterSteps inductionHypothesis =>
    apply inductionHypothesis
    obtain ⟨⟨record, results, isFound, isExecute, hasResults, hasMessage⟩, isEmpty⟩ := hasReceived
    by_cases isHead : head = step
    · subst isHead
      rw [Run.evalStep_of_not_waiting run head record isFound (by simp [isExecute])]
      exact ⟨⟨record, results, isFound, isExecute, hasResults, hasMessage⟩, isEmpty⟩
    · refine ⟨⟨record, results, ?_, isExecute, hasResults, hasMessage⟩,
        Run.pendingMessages_evalStep_nil run head chan isEmpty⟩
      rw [Run.findStep?_evalStep_other run head step (Ne.symm isHead)]
      exact isFound

/-- Mutex and FIFO wake-up (spec §7 item 4, §8.3 "Mutex"): a message published
to an empty Channel goes to the earliest ForOne waiter, and the Channel is
empty again afterwards. -/
theorem wake_servesEarliestWaiter (run : Run σ) (chan : σ.Chan) (message : σ.Val)
    (earliest : StepRecord σ) (waitState : WaitState σ)
    (isMember : earliest ∈ run.steps)
    (idsAreDistinct : ∀ first, first ∈ run.steps → ∀ second, second ∈ run.steps →
      first.id = second.id → first = second)
    (isWaiting : earliest.phase = .waiting)
    (hasWaitState : earliest.waitState = some waitState)
    (waitsForOne : waitState.wait = { kind := .all, timerSeconds := [], channels := [chan] })
    (hasNoTimer : waitState.timersDone = [])
    (isEmpty : run.pendingMessages chan = [])
    (isEarliest : ∀ other, other ∈ run.steps → ∀ otherWaitState,
      other.phase = .waiting → other.waitState = some otherWaitState →
      chan ∈ otherWaitState.wait.channels → other.id ≠ earliest.id →
      waitState.registeredSeq < otherWaitState.registeredSeq) :
    let woken := (run.applyPublications [(chan, message)]).wake [chan]
    (∃ record results,
      woken.findStep? earliest.id = some record ∧ record.phase = .execute ∧
      record.conditionResults = some results ∧ results.channelMessages = [some message]) ∧
    woken.pendingMessages chan = [] := by
  have isPublished : run.applyPublications [(chan, message)] =
      run.setPendingMessages chan [message] := by
    simp [Run.applyPublications, isEmpty]
  rw [isPublished]
  have isFound : (run.setPendingMessages chan [message]).findStep? earliest.id = some earliest :=
    Run.findStep?_of_mem run earliest isMember idsAreDistinct
  -- The earliest waiter is the first Step that `wake` evaluates.
  have isFirst : ((run.setPendingMessages chan [message]).waitersOn [chan]).head? =
      some earliest.id := by
    simp only [Run.waitersOn, List.head?_map]
    rw [head?_sortStable_of_least _ (waitState.registeredSeq, earliest.id)]
    · rfl
    · refine List.mem_filterMap.mpr ⟨earliest, isMember, ?_⟩
      simp [isWaiting, hasWaitState, waitsForOne]
    · simp
    · intro other isOtherMember
      obtain ⟨otherRecord, isRecordMember, isOther⟩ := List.mem_filterMap.mp isOtherMember
      have isRecordMember' : otherRecord ∈ run.steps := isRecordMember
      cases phaseCase : otherRecord.phase <;> simp only [phaseCase] at isOther <;>
        try (cases isOther)
      cases waitCase : otherRecord.waitState with
      | none => simp [waitCase] at isOther
      | some otherWaitState =>
        simp only [waitCase] at isOther
        split at isOther
        · rename_i namesChan
          have isOtherPair : (otherWaitState.registeredSeq, otherRecord.id) = other :=
            Option.some.inj isOther
          subst isOtherPair
          have namesChan' : chan ∈ otherWaitState.wait.channels := by
            simpa using namesChan
          by_cases sameId : otherRecord.id = earliest.id
          · have isSame := idsAreDistinct otherRecord isRecordMember' earliest isMember sameId
            subst isSame
            rw [hasWaitState] at waitCase
            cases waitCase
            exact Or.inl rfl
          · have isLater := isEarliest otherRecord isRecordMember' otherWaitState phaseCase
              waitCase namesChan' sameId
            exact Or.inr ⟨by simpa using Nat.le_of_lt isLater, by simpa using isLater⟩
        · cases isOther
  -- So `wake` is `eval` of the earliest waiter, then `eval` of the others.
  unfold Run.wake
  cases waitersCase : (run.setPendingMessages chan [message]).waitersOn [chan] with
  | nil => simp [waitersCase] at isFirst
  | cons first laterWaiters =>
    have isEarliestFirst : first = earliest.id := by simpa [waitersCase] using isFirst
    subst isEarliestFirst
    simp only [List.foldl_cons]
    apply hasReceived_foldl_evalStep
    -- `eval` of the earliest waiter consumes the message.
    have isPlanned : Run.plan waitState.wait
        (fun other => ((run.setPendingMessages chan [message]).pendingMessages other).length)
        waitState.timersDone = some [true] := by
      simp [waitsForOne, hasNoTimer, Run.plan, Wait.hasNoCondition, Run.pendingMessages,
        Run.setPendingMessages, AssocList.get?_set_self]
    rw [Run.evalStep_of_plan_some _ _ earliest waitState [true] isFound isWaiting hasWaitState
      isPlanned, waitsForOne]
    have isConsumed : Run.consumeMessages [chan] [true]
        ((run.setPendingMessages chan [message]).disarmTimersOf earliest.id) =
        (((run.setPendingMessages chan [message]).disarmTimersOf earliest.id).setPendingMessages
          chan [], [some message]) :=
      Run.consumeMessages_first chan [] _ message []
        (by simp [Run.pendingMessages, Run.setPendingMessages, Run.disarmTimersOf,
          AssocList.get?_set_self])
    dsimp only
    rw [isConsumed]
    refine ⟨⟨_, _, Run.findStep?_updateStep_self _ _ _ earliest isFound rfl, rfl, rfl, rfl⟩, ?_⟩
    simp [Run.pendingMessages, Run.updateStep, Run.setPendingMessages, AssocList.get?_set_self]

end DexExpressiveness.Dex
