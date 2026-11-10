import DexExpressiveness.Dex.WaitLemmas

/-!
# An invariant of runs, kept by every meta-function of spec §4.2

`RunInvariant` collects the facts about one run that the wake-up arguments of
spec §7 item 4 and §8.3 rest on:

- Step execution IDs are unique, and the per-type counters are at or above
  every number in use (spec §3.1);
- a waiting Step has no satisfied condition: none of its timers is done and
  every Channel it names is empty ("a message present when a waiter registers
  means no earlier waiter could take it");
- two waiting Steps never share `sws` (spec Appendix A, assumption 11);
- every armed timer satisfies a fixed predicate, which the translation
  instantiates with "the index-0 timer of a `Timed` Step".

The file proves that each meta-function keeps the invariant. Everything here
is proved, and nothing depends on the translation: the waits are only assumed
to have the shapes of `IsSupportedWait`.
-/

namespace DexExpressiveness.Dex

variable {σ : Signature}

/-- The waits the invariant supports: AnyOf of anything, or AllOf of exactly
one ForOne Channel condition and no timer (`Until(c.ForOne())`). These are the
waits spec §8.3 uses. The empty wait is the AnyOf with no condition or the
wait `Wait.empty`. -/
def IsSupportedWait (wait : Wait σ) : Prop :=
  wait.hasNoCondition = true ∨ wait.kind = .any ∨
  (wait.kind = .all ∧ wait.timerSeconds = [] ∧ ∃ chan, wait.channels = [chan])

namespace Run

/-- `record` is a Step of the run that waits with wait state `waitState`. -/
def IsWaiting (run : Run σ) (record : StepRecord σ) (waitState : WaitState σ) : Prop :=
  record ∈ run.steps ∧ record.phase = .waiting ∧ record.waitState = some waitState

/-- Every Channel the wait names is empty. -/
def IsQuiet (run : Run σ) (waitState : WaitState σ) : Prop :=
  ∀ chan, chan ∈ waitState.wait.channels → run.pendingMessages chan = []

/-- The parts of the invariant that do not mention Channel contents.
`seqBound` is a strict upper bound of the `sws` of every waiter. -/
structure CoreInvariant (IsTimerAllowed : TimerId σ → Prop) (seqBound : Nat) (run : Run σ) :
    Prop where
  /-- Step execution IDs are unique. -/
  idsNodup : (run.steps.map (fun record => record.id)).Nodup
  /-- The counters are at or above every Step number in use. -/
  numbersCounted : ∀ record, record ∈ run.steps →
    record.id.number ≤ (run.counters.get? record.id.stepType).getD 0
  /-- A waiting Step has a supported wait, no timer done, and an `sws` below the bound. -/
  waitsSupported : ∀ record waitState, run.IsWaiting record waitState →
    IsSupportedWait waitState.wait ∧ waitState.timersDone.any id = false ∧
    waitState.timersDone.length = waitState.wait.timerSeconds.length ∧
    waitState.registeredSeq < seqBound
  /-- Two waiting Steps never share `sws`. -/
  seqInjective : ∀ first second firstWait secondWait,
    run.IsWaiting first firstWait → run.IsWaiting second secondWait →
    firstWait.registeredSeq = secondWait.registeredSeq → first.id = second.id
  /-- Every armed timer is allowed. -/
  timersAllowed : ∀ entry, entry ∈ run.timers → IsTimerAllowed entry.1

/-- The invariant of a run between commits: the core invariant, and every
waiting Step is quiet. -/
structure Invariant (IsTimerAllowed : TimerId σ → Prop) (seqBound : Nat) (run : Run σ) :
    Prop extends CoreInvariant IsTimerAllowed seqBound run where
  /-- A pending message never coexists with a waiter on its Channel. -/
  waitersQuiet : ∀ record waitState, run.IsWaiting record waitState → run.IsQuiet waitState

/-- The invariant during `wake`: every waiting Step is quiet, or is still to
be evaluated. -/
def IsQuietExcept (run : Run σ) (remaining : List (StepExecId σ)) : Prop :=
  ∀ record waitState, run.IsWaiting record waitState →
    run.IsQuiet waitState ∨ record.id ∈ remaining

/-! ## Lookup -/

/-- With unique IDs, a member is what lookup by its ID finds. -/
theorem findStep?_of_mem_nodup (run : Run σ) (record : StepRecord σ)
    (isMember : record ∈ run.steps)
    (idsNodup : (run.steps.map (fun candidate => candidate.id)).Nodup) :
    run.findStep? record.id = some record := by
  apply findStep?_of_mem run record isMember
  intro first isFirstMember second isSecondMember sameId
  exact eq_of_nodup_map (fun candidate => candidate.id) run.steps idsNodup isFirstMember
    isSecondMember sameId

/-- What lookup finds is a member with that ID. -/
theorem mem_of_findStep? (run : Run σ) (step : StepExecId σ) (record : StepRecord σ)
    (isFound : run.findStep? step = some record) : record ∈ run.steps ∧ record.id = step :=
  ⟨List.mem_of_find?_eq_some isFound, by simpa using List.find?_some isFound⟩

/-- An update that keeps IDs keeps the list of IDs. -/
theorem ids_updateStep (run : Run σ) (step : StepExecId σ)
    (change : StepRecord σ → StepRecord σ)
    (keepsId : ∀ candidate, (change candidate).id = candidate.id) :
    (run.updateStep step change).steps.map (fun record => record.id) =
      run.steps.map (fun record => record.id) := by
  simp only [updateStep, List.map_map]
  apply List.map_congr_left
  intro candidate _
  by_cases isStep : candidate.id = step <;> simp [isStep, keepsId]

/-- The members of the Step table after an update. -/
theorem mem_updateStep (run : Run σ) (step : StepExecId σ)
    (change : StepRecord σ → StepRecord σ) (record : StepRecord σ) :
    record ∈ (run.updateStep step change).steps ↔
      ∃ original, original ∈ run.steps ∧
        record = if original.id = step then change original else original := by
  simp only [updateStep, List.mem_map]
  constructor
  · rintro ⟨original, isMember, isRecord⟩
    exact ⟨original, isMember, isRecord.symm⟩
  · rintro ⟨original, isMember, isRecord⟩
    exact ⟨original, isMember, isRecord.symm⟩

/-! ## `eval` -/

/-- Consuming messages changes only the Channels. -/
theorem consumeMessages_frame (chans : List σ.Chan) (consumeFlags : List Bool) (run : Run σ) :
    (consumeMessages chans consumeFlags run).1.steps = run.steps ∧
    (consumeMessages chans consumeFlags run).1.counters = run.counters ∧
    (consumeMessages chans consumeFlags run).1.timers = run.timers ∧
    (consumeMessages chans consumeFlags run).1.seq = run.seq ∧
    (consumeMessages chans consumeFlags run).1.status = run.status ∧
    (consumeMessages chans consumeFlags run).1.attributes = run.attributes ∧
    (consumeMessages chans consumeFlags run).1.outputs = run.outputs := by
  induction chans generalizing consumeFlags run with
  | nil => simp [consumeMessages]
  | cons chan laterChans inductionHypothesis =>
    simp only [consumeMessages]
    split
    · simpa [setPendingMessages] using inductionHypothesis _ (run.setPendingMessages chan _)
    · exact inductionHypothesis _ run

/-- The `Any` plan over Channels finds nothing only when every Channel is empty. -/
theorem planAnyChannel_none_imp (pendingCount : σ.Chan → Nat) (chans : List σ.Chan)
    (isNone : planAnyChannel pendingCount chans = none) :
    ∀ chan, chan ∈ chans → pendingCount chan = 0 := by
  induction chans with
  | nil => intro chan isMember; cases isMember
  | cons head laterChans inductionHypothesis =>
    simp only [planAnyChannel] at isNone
    split at isNone
    · cases isNone
    · rename_i isNotPositive
      have laterNone : planAnyChannel pendingCount laterChans = none := by
        cases laterCase : planAnyChannel pendingCount laterChans with
        | none => rfl
        | some laterPlan => simp [laterCase] at isNone
      intro chan isMember
      rcases List.mem_cons.mp isMember with isHead | isLater
      · subst isHead; omega
      · exact inductionHypothesis laterNone chan isLater

/-- A supported wait with no timer done and no plan names only empty Channels. -/
theorem isQuiet_of_plan_none (run : Run σ) (waitState : WaitState σ)
    (isSupported : IsSupportedWait waitState.wait)
    (noTimerDone : waitState.timersDone.any id = false)
    (sameLength : waitState.timersDone.length = waitState.wait.timerSeconds.length)
    (isNotPlanned : plan waitState.wait (fun chan => (run.pendingMessages chan).length)
      waitState.timersDone = none) :
    run.IsQuiet waitState := by
  unfold plan at isNotPlanned
  by_cases hasNoCondition : waitState.wait.hasNoCondition = true
  · simp [hasNoCondition] at isNotPlanned
  · simp only [hasNoCondition, Bool.false_eq_true, ↓reduceIte] at isNotPlanned
    rcases isSupported with isEmpty | isAny | ⟨isAll, noTimers, chan, oneChan⟩
    · exact absurd isEmpty hasNoCondition
    · simp only [isAny, noTimerDone, Bool.false_eq_true, ↓reduceIte] at isNotPlanned
      intro chan isMember
      exact List.length_eq_zero_iff.mp (planAnyChannel_none_imp _ _ isNotPlanned chan isMember)
    · have noneDone : waitState.timersDone = [] := by
        apply List.length_eq_zero_iff.mp
        rw [sameLength, noTimers]
        rfl
      simp only [isAll, noneDone, oneChan, List.all_nil, List.all_cons, List.count_cons_self,
        List.count_nil, Bool.and_true, Bool.true_and, ite_eq_right_iff, reduceCtorEq,
        imp_false, decide_eq_true_eq] at isNotPlanned
      intro other isMember
      rw [oneChan] at isMember
      have isChan : other = chan := by simpa using isMember
      subst isChan
      apply List.length_eq_zero_iff.mp
      omega

/-- The two outcomes of `eval(e)`: nothing changes, and then a waiting `S[e]`
has no plan; or `S[e]` waits, has a plan, and `eval` applies it. -/
theorem evalStep_cases (run : Run σ) (step : StepExecId σ) :
    (run.evalStep step = run ∧
      ∀ record waitState, run.findStep? step = some record → record.phase = .waiting →
        record.waitState = some waitState →
        plan waitState.wait (fun chan => (run.pendingMessages chan).length)
          waitState.timersDone = none) ∨
    (∃ record waitState consumeFlags,
      run.findStep? step = some record ∧ record.phase = .waiting ∧
      record.waitState = some waitState ∧
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
              invocation := some { method := .execute, state := .pending } })) := by
  cases foundCase : run.findStep? step with
  | none =>
    left
    refine ⟨?_, fun record waitState isFound => by cases isFound⟩
    simp [evalStep, foundCase]
  | some record =>
    by_cases isWaiting : record.phase = .waiting
    · cases waitCase : record.waitState with
      | none =>
        left
        refine ⟨?_, ?_⟩
        · simp [evalStep, foundCase, isWaiting, waitCase]
        · intro other otherWait isFound _ hasWait
          cases isFound
          rw [waitCase] at hasWait
          cases hasWait
      | some waitState =>
        cases planCase : plan waitState.wait (fun chan => (run.pendingMessages chan).length)
            waitState.timersDone with
        | none =>
          left
          refine ⟨evalStep_of_plan_none run step record waitState foundCase waitCase planCase, ?_⟩
          intro other otherWait isFound _ hasWait
          cases isFound
          rw [waitCase] at hasWait
          cases hasWait
          exact planCase
        | some consumeFlags =>
          right
          exact ⟨record, waitState, consumeFlags, rfl, isWaiting, waitCase,
            evalStep_of_plan_some run step record waitState consumeFlags foundCase isWaiting
              waitCase planCase⟩
    · left
      refine ⟨evalStep_of_not_waiting run step record foundCase isWaiting, ?_⟩
      intro other otherWait isFound isOtherWaiting
      cases isFound
      exact absurd isOtherWaiting isWaiting

/-- `eval(e)` keeps the IDs, the counters, and `seq`; it can only disarm timers. -/
theorem evalStep_frame (run : Run σ) (step : StepExecId σ) :
    (run.evalStep step).steps.map (fun record => record.id) =
      run.steps.map (fun record => record.id) ∧
    (run.evalStep step).counters = run.counters ∧
    (run.evalStep step).seq = run.seq ∧
    (∀ entry, entry ∈ (run.evalStep step).timers → entry ∈ run.timers) := by
  rcases evalStep_shape run step with isSame | ⟨chans, consumeFlags, change, keepsId, isEval⟩
  · rw [isSame]
    exact ⟨rfl, rfl, rfl, fun _ isMember => isMember⟩
  · rw [isEval]
    obtain ⟨sameSteps, sameCounters, sameTimers, sameSeq, _⟩ :=
      consumeMessages_frame chans consumeFlags (run.disarmTimersOf step)
    refine ⟨?_, ?_, ?_, ?_⟩
    · rw [ids_updateStep _ _ _ keepsId, sameSteps]
      rfl
    · exact sameCounters
    · exact sameSeq
    · intro entry isMember
      have isDisarmedMember : entry ∈ (run.disarmTimersOf step).timers := by
        simpa [updateStep, sameTimers] using isMember
      exact (List.mem_filter.mp isDisarmedMember).1

/-- A Step that waits after `eval(e)` waited before. If it is `e` itself,
`eval(e)` changed nothing and found no plan for it. -/
theorem isWaiting_of_evalStep (run : Run σ) (step : StepExecId σ) (record : StepRecord σ)
    (waitState : WaitState σ) (isWaiting : (run.evalStep step).IsWaiting record waitState) :
    run.IsWaiting record waitState ∧
    (record.id = step → run.evalStep step = run ∧
      ∀ found foundWait, run.findStep? step = some found → found.phase = .waiting →
        found.waitState = some foundWait →
        plan foundWait.wait (fun chan => (run.pendingMessages chan).length)
          foundWait.timersDone = none) := by
  rcases evalStep_cases run step with ⟨isSame, noPlan⟩ |
    ⟨found, foundWait, consumeFlags, _, _, _, isEval⟩
  · rw [isSame] at isWaiting
    exact ⟨isWaiting, fun _ => ⟨isSame, noPlan⟩⟩
  · rw [isEval] at isWaiting
    obtain ⟨isMember, isPhase, hasWait⟩ := isWaiting
    obtain ⟨original, isOriginalMember, isRecord⟩ := (mem_updateStep _ _ _ _).mp isMember
    rw [(consumeMessages_frame _ _ _).1] at isOriginalMember
    by_cases isStep : original.id = step
    · simp only [isStep, ↓reduceIte] at isRecord
      rw [isRecord] at isPhase
      cases isPhase
    · simp only [isStep, ↓reduceIte] at isRecord
      subst isRecord
      exact ⟨⟨isOriginalMember, isPhase, hasWait⟩, fun isSameId => absurd isSameId isStep⟩

/-- `eval(e)` keeps the core invariant. -/
theorem CoreInvariant.evalStep {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    {run : Run σ} (core : CoreInvariant IsTimerAllowed seqBound run) (step : StepExecId σ) :
    CoreInvariant IsTimerAllowed seqBound (run.evalStep step) := by
  obtain ⟨sameIds, sameCounters, _, timersSubset⟩ := evalStep_frame run step
  refine ⟨?_, ?_, ?_, ?_, ?_⟩
  · rw [sameIds]; exact core.idsNodup
  · intro record isMember
    have isIdMember : record.id ∈ (run.evalStep step).steps.map (fun candidate => candidate.id) :=
      List.mem_map.mpr ⟨record, isMember, rfl⟩
    rw [sameIds] at isIdMember
    obtain ⟨original, isOriginalMember, sameId⟩ := List.mem_map.mp isIdMember
    rw [sameCounters, ← sameId]
    exact core.numbersCounted original isOriginalMember
  · intro record waitState isWaiting
    exact core.waitsSupported record waitState (isWaiting_of_evalStep run step _ _ isWaiting).1
  · intro first second firstWait secondWait isFirstWaiting isSecondWaiting
    exact core.seqInjective first second firstWait secondWait
      (isWaiting_of_evalStep run step _ _ isFirstWaiting).1
      (isWaiting_of_evalStep run step _ _ isSecondWaiting).1
  · intro entry isMember
    exact core.timersAllowed entry (timersSubset entry isMember)

/-- One step of `wake`: after `eval(e)`, `e` no longer needs evaluation. -/
theorem isQuietExcept_evalStep {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    {run : Run σ} (core : CoreInvariant IsTimerAllowed seqBound run) (step : StepExecId σ)
    (remaining : List (StepExecId σ)) (isQuietExcept : run.IsQuietExcept (step :: remaining)) :
    (run.evalStep step).IsQuietExcept remaining := by
  intro record waitState isWaitingAfter
  obtain ⟨isWaiting, ofSameId⟩ := isWaiting_of_evalStep run step _ _ isWaitingAfter
  rcases isQuietExcept record waitState isWaiting with isQuiet | isRemaining
  · exact Or.inl (fun chan isMember =>
      pendingMessages_evalStep_nil run step chan (isQuiet chan isMember))
  · rcases List.mem_cons.mp isRemaining with isStep | isLater
    · obtain ⟨isSame, noPlan⟩ := ofSameId isStep
      left
      rw [isSame]
      obtain ⟨isSupported, noTimerDone, sameLength, _⟩ :=
        core.waitsSupported record waitState isWaiting
      have isFound : run.findStep? step = some record := by
        rw [← isStep]
        exact findStep?_of_mem_nodup run record isWaiting.1 core.idsNodup
      exact isQuiet_of_plan_none run waitState isSupported noTimerDone sameLength
        (noPlan record waitState isFound isWaiting.2.1 isWaiting.2.2)
    · exact Or.inr isLater

/-- Evaluating every Step that still needs it makes every waiter quiet. -/
theorem invariant_foldl_evalStep {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    (remaining : List (StepExecId σ)) (run : Run σ)
    (core : CoreInvariant IsTimerAllowed seqBound run)
    (isQuietExcept : run.IsQuietExcept remaining) :
    Invariant IsTimerAllowed seqBound (remaining.foldl Run.evalStep run) := by
  induction remaining generalizing run with
  | nil =>
    refine { core with waitersQuiet := ?_ }
    intro record waitState isWaiting
    rcases isQuietExcept record waitState isWaiting with isQuiet | isRemaining
    · exact isQuiet
    · cases isRemaining
  | cons step laterSteps inductionHypothesis =>
    exact inductionHypothesis (run.evalStep step) (core.evalStep step)
      (isQuietExcept_evalStep core step laterSteps isQuietExcept)

/-- A waiter that names a changed Channel is among the Steps `wake` evaluates. -/
theorem mem_waitersOn (run : Run σ) (changed : List σ.Chan) (record : StepRecord σ)
    (waitState : WaitState σ) (isWaiting : run.IsWaiting record waitState) (chan : σ.Chan)
    (isNamed : chan ∈ waitState.wait.channels) (isChanged : chan ∈ changed) :
    record.id ∈ run.waitersOn changed := by
  obtain ⟨isMember, isPhase, hasWait⟩ := isWaiting
  simp only [waitersOn, List.mem_map]
  refine ⟨(waitState.registeredSeq, record.id), ?_, rfl⟩
  apply (mem_sortStable _ _ _).mpr
  refine List.mem_filterMap.mpr ⟨record, isMember, ?_⟩
  simp only [isPhase, hasWait]
  simp
  exact ⟨chan, isNamed, isChanged⟩

/-- `wake(C)` restores the invariant when only waiters on Channels of `C` can
have a satisfied wait. -/
theorem Invariant.wake {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat} {run : Run σ}
    (core : CoreInvariant IsTimerAllowed seqBound run) (changed : List σ.Chan)
    (othersQuiet : ∀ record waitState, run.IsWaiting record waitState →
      run.IsQuiet waitState ∨ ∃ chan, chan ∈ waitState.wait.channels ∧ chan ∈ changed) :
    Invariant IsTimerAllowed seqBound (run.wake changed) := by
  apply invariant_foldl_evalStep _ run core
  intro record waitState isWaiting
  rcases othersQuiet record waitState isWaiting with isQuiet | ⟨chan, isNamed, isChanged⟩
  · exact Or.inl isQuiet
  · exact Or.inr (mem_waitersOn run changed record waitState isWaiting chan isNamed isChanged)

/-! ## Transfer along changes that create no waiter -/

/-- The core invariant carries over to a run with the same IDs and counters,
whose waiters were waiters, and whose timers were armed. -/
theorem CoreInvariant.transfer {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    {run after : Run σ} (core : CoreInvariant IsTimerAllowed seqBound run)
    (sameIds : after.steps.map (fun record => record.id) =
      run.steps.map (fun record => record.id))
    (sameCounters : after.counters = run.counters)
    (waitersWaited : ∀ record waitState, after.IsWaiting record waitState →
      ∃ original, run.IsWaiting original waitState ∧ original.id = record.id)
    (timersSubset : ∀ entry, entry ∈ after.timers → entry ∈ run.timers) :
    CoreInvariant IsTimerAllowed seqBound after := by
  refine ⟨?_, ?_, ?_, ?_, ?_⟩
  · rw [sameIds]; exact core.idsNodup
  · intro record isMember
    have isIdMember : record.id ∈ after.steps.map (fun candidate => candidate.id) :=
      List.mem_map.mpr ⟨record, isMember, rfl⟩
    rw [sameIds] at isIdMember
    obtain ⟨original, isOriginalMember, sameId⟩ := List.mem_map.mp isIdMember
    rw [sameCounters, ← sameId]
    exact core.numbersCounted original isOriginalMember
  · intro record waitState isWaiting
    obtain ⟨original, wasWaiting, _⟩ := waitersWaited record waitState isWaiting
    exact core.waitsSupported original waitState wasWaiting
  · intro first second firstWait secondWait isFirstWaiting isSecondWaiting sameSeq
    obtain ⟨firstOriginal, firstWaited, firstId⟩ := waitersWaited first firstWait isFirstWaiting
    obtain ⟨secondOriginal, secondWaited, secondId⟩ :=
      waitersWaited second secondWait isSecondWaiting
    rw [← firstId, ← secondId]
    exact core.seqInjective firstOriginal secondOriginal firstWait secondWait firstWaited
      secondWaited sameSeq
  · intro entry isMember
    exact core.timersAllowed entry (timersSubset entry isMember)

/-- The invariant carries over as the core invariant does, when empty
Channels stay empty. -/
theorem Invariant.transfer {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    {run after : Run σ} (invariant : Invariant IsTimerAllowed seqBound run)
    (sameIds : after.steps.map (fun record => record.id) =
      run.steps.map (fun record => record.id))
    (sameCounters : after.counters = run.counters)
    (waitersWaited : ∀ record waitState, after.IsWaiting record waitState →
      ∃ original, run.IsWaiting original waitState ∧ original.id = record.id)
    (timersSubset : ∀ entry, entry ∈ after.timers → entry ∈ run.timers)
    (emptyStaysEmpty : ∀ chan, run.pendingMessages chan = [] → after.pendingMessages chan = []) :
    Invariant IsTimerAllowed seqBound after :=
  { invariant.toCoreInvariant.transfer sameIds sameCounters waitersWaited timersSubset with
    waitersQuiet := by
      intro record waitState isWaiting chan isNamed
      obtain ⟨original, wasWaiting, _⟩ := waitersWaited record waitState isWaiting
      exact emptyStaysEmpty chan (invariant.waitersQuiet original waitState wasWaiting chan isNamed) }

/-- A larger bound on `sws` keeps the invariant. -/
theorem Invariant.mono {IsTimerAllowed : TimerId σ → Prop} {seqBound largerBound : Nat}
    {run : Run σ} (invariant : Invariant IsTimerAllowed seqBound run)
    (isLarger : seqBound ≤ largerBound) : Invariant IsTimerAllowed largerBound run :=
  { invariant with
    waitsSupported := by
      intro record waitState isWaiting
      obtain ⟨isSupported, noTimerDone, sameLength, isBelow⟩ :=
        invariant.waitsSupported record waitState isWaiting
      exact ⟨isSupported, noTimerDone, sameLength, Nat.lt_of_lt_of_le isBelow isLarger⟩ }

/-- A waiter after an update that creates no waiter was a waiter before. -/
theorem isWaiting_of_updateStep (run : Run σ) (step : StepExecId σ)
    (change : StepRecord σ → StepRecord σ)
    (keepsId : ∀ candidate, (change candidate).id = candidate.id)
    (createsNoWaiter : ∀ candidate, (change candidate).phase = .waiting →
      candidate.phase = .waiting ∧ (change candidate).waitState = candidate.waitState)
    (record : StepRecord σ) (waitState : WaitState σ)
    (isWaiting : (run.updateStep step change).IsWaiting record waitState) :
    ∃ original, run.IsWaiting original waitState ∧ original.id = record.id := by
  obtain ⟨isMember, isPhase, hasWait⟩ := isWaiting
  obtain ⟨original, isOriginalMember, isRecord⟩ := (mem_updateStep _ _ _ _).mp isMember
  by_cases isStep : original.id = step
  · simp only [isStep, ↓reduceIte] at isRecord
    subst isRecord
    obtain ⟨wasWaiting, sameWait⟩ := createsNoWaiter original isPhase
    exact ⟨original, ⟨isOriginalMember, wasWaiting, by rw [← sameWait]; exact hasWait⟩,
      (keepsId original).symm⟩
  · simp only [isStep, ↓reduceIte] at isRecord
    subst isRecord
    exact ⟨record, ⟨isOriginalMember, isPhase, hasWait⟩, rfl⟩

/-- An update of `S[e]` that keeps the ID and creates no waiter keeps the invariant. -/
theorem Invariant.updateStep {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    {run : Run σ} (invariant : Invariant IsTimerAllowed seqBound run) (step : StepExecId σ)
    (change : StepRecord σ → StepRecord σ)
    (keepsId : ∀ candidate, (change candidate).id = candidate.id)
    (createsNoWaiter : ∀ candidate, (change candidate).phase = .waiting →
      candidate.phase = .waiting ∧ (change candidate).waitState = candidate.waitState) :
    Invariant IsTimerAllowed seqBound (run.updateStep step change) :=
  invariant.transfer (ids_updateStep run step change keepsId) rfl
    (isWaiting_of_updateStep run step change keepsId createsNoWaiter)
    (fun _ isMember => isMember) (fun _ isEmpty => isEmpty)

/-! ## `effects` -/

/-- Writes change only the Attributes. -/
theorem applyWrites_frame (writes : List (σ.Key × Option σ.Val)) (run : Run σ) :
    (run.applyWrites writes).steps = run.steps ∧
    (run.applyWrites writes).counters = run.counters ∧
    (run.applyWrites writes).timers = run.timers ∧
    (run.applyWrites writes).channels = run.channels ∧
    (run.applyWrites writes).seq = run.seq ∧
    (run.applyWrites writes).status = run.status ∧
    (run.applyWrites writes).outputs = run.outputs := by
  unfold applyWrites
  induction writes generalizing run with
  | nil => simp
  | cons write laterWrites inductionHypothesis =>
    simp only [List.foldl_cons]
    cases write.2 <;> simpa using inductionHypothesis _

/-- Publications change only the Channels published to. -/
theorem applyPublications_frame (publications : List (σ.Chan × σ.Val)) (run : Run σ) :
    (run.applyPublications publications).steps = run.steps ∧
    (run.applyPublications publications).counters = run.counters ∧
    (run.applyPublications publications).timers = run.timers ∧
    (run.applyPublications publications).seq = run.seq ∧
    (run.applyPublications publications).status = run.status ∧
    (run.applyPublications publications).attributes = run.attributes ∧
    (run.applyPublications publications).outputs = run.outputs ∧
    ∀ chan, chan ∉ publications.map (fun publication => publication.1) →
      (run.applyPublications publications).pendingMessages chan = run.pendingMessages chan := by
  unfold applyPublications
  induction publications generalizing run with
  | nil => simp
  | cons publication laterPublications inductionHypothesis =>
    simp only [List.foldl_cons]
    obtain ⟨sameSteps, sameCounters, sameTimers, sameSeq, sameStatus, sameAttributes,
      sameOutputs, samePending⟩ := inductionHypothesis
        (run.setPendingMessages publication.1 (run.pendingMessages publication.1 ++ [publication.2]))
    refine ⟨sameSteps, sameCounters, sameTimers, sameSeq, sameStatus, sameAttributes,
      sameOutputs, ?_⟩
    intro chan isNotPublished
    simp only [List.map_cons, List.mem_cons, not_or] at isNotPublished
    rw [samePending chan isNotPublished.2]
    simp only [pendingMessages, setPendingMessages]
    rw [AssocList.get?_set_other _ _ _ _ isNotPublished.1]

/-- `effects` keeps the invariant: `wake` re-evaluates every waiter on a
Channel that was published to. -/
theorem Invariant.applyEffects {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    {run : Run σ} (invariant : Invariant IsTimerAllowed seqBound run) (effects : Effects σ) :
    Invariant IsTimerAllowed seqBound (run.applyEffects effects) := by
  unfold Run.applyEffects
  obtain ⟨writeSteps, writeCounters, writeTimers, writeChannels, _⟩ :=
    applyWrites_frame effects.writes run
  obtain ⟨pubSteps, pubCounters, pubTimers, _, _, _, _, pubPending⟩ :=
    applyPublications_frame effects.publications (run.applyWrites effects.writes)
  have waitersWaited : ∀ record waitState,
      ((run.applyWrites effects.writes).applyPublications effects.publications).IsWaiting
        record waitState → run.IsWaiting record waitState := by
    intro record waitState isWaiting
    obtain ⟨isMember, isPhase, hasWait⟩ := isWaiting
    rw [pubSteps, writeSteps] at isMember
    exact ⟨isMember, isPhase, hasWait⟩
  apply Invariant.wake
  · apply invariant.toCoreInvariant.transfer
    · rw [pubSteps, writeSteps]
    · rw [pubCounters, writeCounters]
    · intro record waitState isWaiting
      exact ⟨record, waitersWaited record waitState isWaiting, rfl⟩
    · intro entry isMember
      rw [pubTimers, writeTimers] at isMember
      exact isMember
  · intro record waitState isWaiting
    have wasWaiting := waitersWaited record waitState isWaiting
    by_cases namesPublished : ∃ chan, chan ∈ waitState.wait.channels ∧
        chan ∈ effects.publications.map (fun publication => publication.1)
    · exact Or.inr namesPublished
    · left
      intro chan isNamed
      have isNotPublished : chan ∉ effects.publications.map (fun publication => publication.1) :=
        fun isPublished => namesPublished ⟨chan, isNamed, isPublished⟩
      rw [pubPending chan isNotPublished]
      have wasEmpty := invariant.waitersQuiet record waitState wasWaiting chan isNamed
      simpa [pendingMessages, writeChannels] using wasEmpty

/-! ## `register` -/

/-- A timer armed for a wait is an old timer or one of the wait's own timers. -/
theorem mem_armTimers (step : StepExecId σ) (fireAt : List Nat) (index : Nat)
    (timers : AssocList (TimerId σ) Nat) (entry : TimerId σ × Nat)
    (isMember : entry ∈ armTimers step fireAt index timers) :
    entry ∈ timers ∨
      ∃ offset, offset < fireAt.length ∧ entry.1 = { step := step, index := index + offset } := by
  induction fireAt generalizing index timers with
  | nil => exact Or.inl isMember
  | cons fireTime laterFireTimes inductionHypothesis =>
    simp only [armTimers] at isMember
    rcases inductionHypothesis (index + 1) _ isMember with isOld | ⟨offset, isBelow, isEntry⟩
    · rcases AssocList.mem_set _ _ _ _ isOld with isOlder | isNew
      · exact Or.inl isOlder
      · exact Or.inr ⟨0, by simp, by simp [isNew]⟩
    · refine Or.inr ⟨offset + 1, by simp only [List.length_cons]; omega, ?_⟩
      rw [isEntry]
      congr 1
      omega

/-- `register(e, W)` keeps the invariant, for a supported wait whose timers
are allowed. Before it, every waiter's `sws` is below the run's `seq`, which
the new waiter takes. -/
theorem Invariant.registerWait {IsTimerAllowed : TimerId σ → Prop} {run : Run σ}
    (invariant : Invariant IsTimerAllowed run.seq run) (step : StepExecId σ) (wait : Wait σ)
    (now : Nat) (isSupported : IsSupportedWait wait)
    (timersAllowed : ∀ index, index < wait.timerSeconds.length →
      IsTimerAllowed { step := step, index := index }) :
    Invariant IsTimerAllowed (run.seq + 1) (run.registerWait step wait now) := by
  unfold Run.registerWait
  by_cases hasNoCondition : wait.hasNoCondition = true
  · simp only [hasNoCondition, ↓reduceIte]
    refine (Invariant.updateStep invariant step _ ?_ ?_).mono (Nat.le_succ _)
    · intro candidate
      rfl
    · intro candidate isPhase
      cases isPhase
  · simp only [hasNoCondition, Bool.false_eq_true, ↓reduceIte]
    apply invariant_foldl_evalStep [step]
    · -- The core invariant after the new wait is stored.
      have waitersCases : ∀ record waitState,
          (Run.updateStep
            { run with
              timers := armTimers step
                (wait.timerSeconds.map (fun seconds => now + seconds * 1000000)) 0 run.timers }
            step (fun current =>
              { current with
                phase := .waiting
                waitState := some
                  { wait := wait
                    fireAt := wait.timerSeconds.map (fun seconds => now + seconds * 1000000)
                    timersDone := wait.timerSeconds.map (fun _ => false)
                    registeredSeq := run.seq } })).IsWaiting record waitState →
          (run.IsWaiting record waitState ∧ record.id ≠ step) ∨
          (record.id = step ∧ waitState.wait = wait ∧
            waitState.timersDone = wait.timerSeconds.map (fun _ => false) ∧
            waitState.registeredSeq = run.seq) := by
        intro record waitState isWaiting
        obtain ⟨isMember, isPhase, hasWait⟩ := isWaiting
        obtain ⟨original, isOriginalMember, isRecord⟩ := (mem_updateStep _ _ _ _).mp isMember
        by_cases isStep : original.id = step
        · rw [if_pos isStep] at isRecord
          subst isRecord
          have isNewWait := Option.some.inj hasWait
          subst isNewWait
          exact Or.inr ⟨isStep, rfl, rfl, rfl⟩
        · rw [if_neg isStep] at isRecord
          subst isRecord
          exact Or.inl ⟨⟨isOriginalMember, isPhase, hasWait⟩, isStep⟩
      refine ⟨?_, ?_, ?_, ?_, ?_⟩
      · rw [ids_updateStep]
        · exact invariant.idsNodup
        · intro candidate
          rfl
      · intro record isMember
        obtain ⟨middle, isMiddleMember, isRecord⟩ := (mem_updateStep _ _ _ _).mp isMember
        have sameId : middle.id = record.id := by
          rw [isRecord]
          by_cases isStep : middle.id = step
          · rw [if_pos isStep]
          · rw [if_neg isStep]
        obtain ⟨original, isOriginalMember⟩ : ∃ original, original = middle := ⟨middle, rfl⟩
        rw [← isOriginalMember] at isMiddleMember sameId
        have isOriginalMember : original ∈ run.steps := isMiddleMember
        rw [← sameId]
        exact invariant.numbersCounted original isOriginalMember
      · intro record waitState isWaiting
        rcases waitersCases record waitState isWaiting with ⟨wasWaiting, _⟩ |
          ⟨_, isWait, isDone, isSeq⟩
        · obtain ⟨wasSupported, noTimerDone, sameLength, isBelow⟩ :=
            invariant.waitsSupported record waitState wasWaiting
          exact ⟨wasSupported, noTimerDone, sameLength, Nat.lt_succ_of_lt isBelow⟩
        · refine ⟨by rw [isWait]; exact isSupported, by simp [isDone], by simp [isDone, isWait],
            by omega⟩
      · intro first second firstWait secondWait isFirstWaiting isSecondWaiting sameSeq
        rcases waitersCases first firstWait isFirstWaiting with ⟨firstWaited, _⟩ |
          ⟨isFirstStep, _, _, isFirstSeq⟩ <;>
        rcases waitersCases second secondWait isSecondWaiting with ⟨secondWaited, _⟩ |
          ⟨isSecondStep, _, _, isSecondSeq⟩
        · exact invariant.seqInjective first second firstWait secondWait firstWaited
            secondWaited sameSeq
        · have isBelow := (invariant.waitsSupported first firstWait firstWaited).2.2.2
          omega
        · have isBelow := (invariant.waitsSupported second secondWait secondWaited).2.2.2
          omega
        · rw [isFirstStep, isSecondStep]
      · intro entry isMember
        rcases mem_armTimers step _ 0 run.timers entry isMember with isOld |
          ⟨offset, isBelow, isEntry⟩
        · exact invariant.timersAllowed entry isOld
        · rw [isEntry]
          simp only [Nat.zero_add]
          exact timersAllowed offset (by simpa using isBelow)
    · -- Every old waiter is quiet; the new one is evaluated next.
      intro record waitState isWaiting
      obtain ⟨isMember, isPhase, hasWait⟩ := isWaiting
      obtain ⟨original, isOriginalMember, isRecord⟩ := (mem_updateStep _ _ _ _).mp isMember
      by_cases isStep : original.id = step
      · rw [if_pos isStep] at isRecord
        subst isRecord
        exact Or.inr (List.mem_singleton.mpr isStep)
      · rw [if_neg isStep] at isRecord
        subst isRecord
        exact Or.inl (invariant.waitersQuiet record waitState ⟨isOriginalMember, isPhase, hasWait⟩)

/-! ## `finish`, `close`, `new`, `decide` -/

/-- `finish(e, φ')` keeps the invariant. -/
theorem Invariant.finishStep {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    {run : Run σ} (invariant : Invariant IsTimerAllowed seqBound run) (step : StepExecId σ)
    (finalPhase : Phase) (isFinal : finalPhase ≠ .waiting) :
    Invariant IsTimerAllowed seqBound (run.finishStep step finalPhase) := by
  unfold Run.finishStep
  have disarmed : Invariant IsTimerAllowed seqBound (run.disarmTimersOf step) :=
    invariant.transfer rfl rfl (fun record _ isWaiting => ⟨record, isWaiting, rfl⟩)
      (fun _ isMember => (List.mem_filter.mp isMember).1) (fun _ isEmpty => isEmpty)
  exact disarmed.updateStep step _ (fun _ => rfl) (fun _ isPhase => absurd isPhase isFinal)

/-- Canceling a list of Steps keeps the invariant. -/
theorem invariant_foldl_finishStep {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    (steps : List (StepExecId σ)) (run : Run σ)
    (invariant : Invariant IsTimerAllowed seqBound run) :
    Invariant IsTimerAllowed seqBound
      (steps.foldl (fun current step => current.finishStep step .canceled) run) := by
  induction steps generalizing run with
  | nil => exact invariant
  | cons step laterSteps inductionHypothesis =>
    exact inductionHypothesis _ (invariant.finishStep step .canceled (by simp))

/-- `close(κ)` keeps the invariant. -/
theorem Invariant.closeRun {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    {run : Run σ} (invariant : Invariant IsTimerAllowed seqBound run) (status : CloseStatus) :
    Invariant IsTimerAllowed seqBound (run.closeRun status) := by
  unfold Run.closeRun
  split
  · exact invariant
  · exact (invariant_foldl_finishStep _ run invariant).transfer rfl rfl
      (fun record _ isWaiting => ⟨record, isWaiting, rfl⟩) (fun _ isMember => isMember)
      (fun _ isEmpty => isEmpty)

/-- `collect(e, v)` keeps the invariant. -/
theorem Invariant.collectOutput {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    {run : Run σ} (invariant : Invariant IsTimerAllowed seqBound run) (step : StepExecId σ)
    (output : Option σ.Val) :
    Invariant IsTimerAllowed seqBound (run.collectOutput step output) := by
  unfold Run.collectOutput
  cases output with
  | none => exact invariant
  | some value =>
    exact invariant.transfer rfl rfl (fun record _ isWaiting => ⟨record, isWaiting, rfl⟩)
      (fun _ isMember => isMember) (fun _ isEmpty => isEmpty)

/-- `new(T, v, o, from)` keeps the invariant: the new Step has a fresh ID and
does not wait. -/
theorem Invariant.newStep {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    {run : Run σ} (invariant : Invariant IsTimerAllowed seqBound run) (stepType : σ.StepType)
    (input : Option σ.Val) (options : StepOptions σ) (origin : Origin σ) :
    Invariant IsTimerAllowed seqBound (run.newStep stepType input options origin) := by
  have waitersWaited : ∀ record waitState,
      (run.newStep stepType input options origin).IsWaiting record waitState →
      run.IsWaiting record waitState := by
    intro record waitState isWaiting
    obtain ⟨isMember, isPhase, hasWait⟩ := isWaiting
    simp only [Run.newStep, List.mem_append, List.mem_singleton] at isMember
    rcases isMember with isOld | isNew
    · exact ⟨isOld, isPhase, hasWait⟩
    · subst isNew
      by_cases skips : options.skipWaitFor = true <;> simp [skips] at isPhase
  refine { idsNodup := ?_, numbersCounted := ?_, waitsSupported := ?_, seqInjective := ?_,
           timersAllowed := ?_, waitersQuiet := ?_ }
  · simp only [Run.newStep, List.map_append, List.map_cons, List.map_nil]
    apply List.nodup_append.mpr
    refine ⟨invariant.idsNodup, by simp, ?_⟩
    intro existingId isExisting newId isNew
    obtain ⟨record, isMember, isId⟩ := List.mem_map.mp isExisting
    have isCounted := invariant.numbersCounted record isMember
    simp only [List.mem_singleton] at isNew
    intro isSame
    rw [← isId, isNew] at isSame
    rw [isSame] at isCounted
    simp at isCounted
    omega
  · intro record isMember
    simp only [Run.newStep, List.mem_append, List.mem_singleton] at isMember ⊢
    rcases isMember with isOld | isNew
    · have isCounted := invariant.numbersCounted record isOld
      by_cases isType : record.id.stepType = stepType
      · rw [isType, AssocList.get?_set_self]
        rw [isType] at isCounted
        simp only [Option.getD_some]
        omega
      · rw [AssocList.get?_set_other _ _ _ _ isType]
        exact isCounted
    · subst isNew
      simp [AssocList.get?_set_self]
  · intro record waitState isWaiting
    exact invariant.waitsSupported record waitState (waitersWaited record waitState isWaiting)
  · intro first second firstWait secondWait isFirstWaiting isSecondWaiting
    exact invariant.seqInjective first second firstWait secondWait
      (waitersWaited _ _ isFirstWaiting) (waitersWaited _ _ isSecondWaiting)
  · exact invariant.timersAllowed
  · intro record waitState isWaiting
    exact invariant.waitersQuiet record waitState (waitersWaited record waitState isWaiting)

/-- Taking the moves of a decision keeps the invariant. -/
theorem invariant_foldl_newStep {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    (moves : List (Move σ)) (origin : Origin σ) (run : Run σ)
    (invariant : Invariant IsTimerAllowed seqBound run) :
    Invariant IsTimerAllowed seqBound
      (moves.foldl (fun current move =>
        current.newStep move.stepType move.input move.options origin) run) := by
  induction moves generalizing run with
  | nil => exact invariant
  | cons move laterMoves inductionHypothesis =>
    exact inductionHypothesis _ (invariant.newStep move.stepType move.input move.options origin)

/-- `decide(e, D)` keeps the invariant. -/
theorem Invariant.applyDecision {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    {run : Run σ} (invariant : Invariant IsTimerAllowed seqBound run) (step : StepExecId σ)
    (decision : Decision σ) :
    Invariant IsTimerAllowed seqBound (run.applyDecision step decision) := by
  have finished := invariant.finishStep step .completed (by simp)
  simp only [Run.applyDecision]
  split
  · exact (finished.collectOutput step _).closeRun _
  · exact finished.closeRun _
  · exact finished
  · exact invariant_foldl_newStep _ _ _ finished

/-! ## [TIMER-FIRE] -/

/-- Marking a timer that is not done makes some timer done. -/
theorem any_set_of_getD (flags : List Bool) (index : Nat)
    (isNotDone : flags.getD index true = false) : (flags.set index true).any id = true := by
  induction flags generalizing index with
  | nil => simp at isNotDone
  | cons flag laterFlags inductionHypothesis =>
    cases index with
    | zero => simp
    | succ previous =>
      have laterNotDone : laterFlags.getD previous true = false := by simpa using isNotDone
      simp [inductionHypothesis previous laterNotDone]

/-- Firing a timer of a waiting Step keeps the invariant: the Step's wait is
an AnyOf, so the fired timer satisfies it and the Step stops waiting. -/
theorem Invariant.fireWaiting {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    {run : Run σ} (invariant : Invariant IsTimerAllowed seqBound run) (step : StepExecId σ)
    (index : Nat) (record : StepRecord σ) (waitState : WaitState σ)
    (isFound : run.findStep? step = some record) (isPhase : record.phase = .waiting)
    (hasWait : record.waitState = some waitState)
    (isNotDone : waitState.timersDone.getD index true = false) :
    Invariant IsTimerAllowed seqBound
      ((run.updateStep step (fun current =>
        { current with
          waitState := some
            { waitState with timersDone := waitState.timersDone.set index true } })).evalStep
        step) := by
  obtain ⟨isMember, _⟩ := mem_of_findStep? run step record isFound
  obtain ⟨isSupported, _, sameLength, _⟩ :=
    invariant.waitsSupported record waitState ⟨isMember, isPhase, hasWait⟩
  have isUpdated := findStep?_updateStep_self run step (fun current =>
    { current with
      waitState := some
        { waitState with timersDone := waitState.timersDone.set index true } }) record isFound rfl
  have isPlanned : ∃ consumeFlags, plan waitState.wait
      (fun chan => ((run.updateStep step (fun current =>
        { current with
          waitState := some
            { waitState with timersDone := waitState.timersDone.set index true } })
        ).pendingMessages chan).length)
      (waitState.timersDone.set index true) = some consumeFlags := by
    unfold plan
    by_cases hasNoCondition : waitState.wait.hasNoCondition = true
    · exact ⟨[], by simp [hasNoCondition]⟩
    · rcases isSupported with isEmpty | isAny | ⟨_, noTimers, _⟩
      · exact absurd isEmpty hasNoCondition
      · exact ⟨waitState.wait.channels.map (fun _ => false),
          by simp [hasNoCondition, isAny, any_set_of_getD _ _ isNotDone]⟩
      · have noneDone : waitState.timersDone = [] := by
          apply List.length_eq_zero_iff.mp
          rw [sameLength, noTimers]
          rfl
        simp [noneDone] at isNotDone
  obtain ⟨consumeFlags, isPlanned⟩ := isPlanned
  rw [evalStep_of_plan_some _ step _ _ consumeFlags isUpdated isPhase rfl isPlanned]
  obtain ⟨sameSteps, sameCounters, sameTimers, _⟩ := consumeMessages_frame
    waitState.wait.channels consumeFlags
    ((run.updateStep step (fun current =>
      { current with
        waitState := some
          { waitState with timersDone := waitState.timersDone.set index true } })
      ).disarmTimersOf step)
  apply invariant.transfer
  · rw [ids_updateStep]
    · rw [sameSteps]
      apply ids_updateStep
      intro candidate
      rfl
    · intro candidate
      rfl
  · exact sameCounters
  · intro other otherWait isWaiting
    obtain ⟨isOtherMember, isOtherPhase, otherHasWait⟩ := isWaiting
    obtain ⟨middle, isMiddleMember, isOther⟩ := (mem_updateStep _ _ _ _).mp isOtherMember
    rw [sameSteps] at isMiddleMember
    obtain ⟨original, isOriginalMember, isMiddle⟩ := (mem_updateStep _ _ _ _).mp isMiddleMember
    by_cases isStep : original.id = step
    · rw [if_pos isStep] at isMiddle
      subst isMiddle
      rw [if_pos isStep] at isOther
      rw [isOther] at isOtherPhase
      cases isOtherPhase
    · rw [if_neg isStep] at isMiddle
      subst isMiddle
      rw [if_neg isStep] at isOther
      subst isOther
      exact ⟨other, ⟨isOriginalMember, isOtherPhase, otherHasWait⟩, rfl⟩
  · intro entry isTimerMember
    have isDisarmedMember : entry ∈ (run.disarmTimersOf step).timers :=
      Eq.mp (congrArg (fun timers => entry ∈ timers) sameTimers) isTimerMember
    exact (List.mem_filter.mp isDisarmedMember).1
  · intro chan isEmpty
    exact consumeMessages_pending_nil _ _ _ chan isEmpty

/-- The effect of [TIMER-FIRE] for one timer keeps the invariant. -/
theorem Invariant.fireTimer {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    {run : Run σ} (invariant : Invariant IsTimerAllowed seqBound run) (timer : TimerId σ) :
    Invariant IsTimerAllowed seqBound (run.fireTimer timer) := by
  unfold Run.fireTimer
  split
  · have disarmed : Invariant IsTimerAllowed seqBound
        { run with timers := run.timers.erase timer } :=
      invariant.transfer rfl rfl (fun record _ isWaiting => ⟨record, isWaiting, rfl⟩)
        (fun entry isMember => AssocList.mem_of_mem_erase _ _ entry isMember)
        (fun _ isEmpty => isEmpty)
    simp only
    split
    · exact disarmed
    · rename_i record isFound
      split
      · rename_i waitState isPhase hasWait
        split
        · exact disarmed
        · rename_i isNotDone
          exact disarmed.fireWaiting timer.step timer.index record waitState isFound isPhase
            hasWait (by simpa using isNotDone)
      · exact disarmed
  · exact invariant

/-- Firing a prefix of the due timers keeps the invariant. -/
theorem invariant_foldl_fireTimer {IsTimerAllowed : TimerId σ → Prop} {seqBound : Nat}
    (timers : List (TimerId σ)) (run : Run σ)
    (invariant : Invariant IsTimerAllowed seqBound run) :
    Invariant IsTimerAllowed seqBound (timers.foldl Run.fireTimer run) := by
  induction timers generalizing run with
  | nil => exact invariant
  | cons timer laterTimers inductionHypothesis =>
    exact inductionHypothesis _ (invariant.fireTimer timer)

/-! ## `seq` changes only at the start of a commit -/

/-- Evaluating Steps keeps `seq`. -/
theorem seq_foldl_evalStep (steps : List (StepExecId σ)) (run : Run σ) :
    (steps.foldl Run.evalStep run).seq = run.seq := by
  induction steps generalizing run with
  | nil => rfl
  | cons step laterSteps inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [inductionHypothesis, (evalStep_frame run step).2.2.1]

/-- `effects` keeps `seq`. -/
theorem seq_applyEffects (run : Run σ) (effects : Effects σ) :
    (run.applyEffects effects).seq = run.seq := by
  unfold Run.applyEffects Run.wake
  rw [seq_foldl_evalStep, (applyPublications_frame _ _).2.2.2.1, (applyWrites_frame _ _).2.2.2.2.1]

/-- `register` keeps `seq`. -/
theorem seq_registerWait (run : Run σ) (step : StepExecId σ) (wait : Wait σ) (now : Nat) :
    (run.registerWait step wait now).seq = run.seq := by
  unfold Run.registerWait
  split
  · rfl
  · exact (evalStep_frame _ step).2.2.1

/-- Canceling Steps keeps `seq`. -/
theorem seq_foldl_finishStep (steps : List (StepExecId σ)) (run : Run σ) :
    (steps.foldl (fun current step => current.finishStep step .canceled) run).seq = run.seq := by
  induction steps generalizing run with
  | nil => rfl
  | cons step laterSteps inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [inductionHypothesis]
    rfl

/-- `close` keeps `seq`. -/
theorem seq_closeRun (run : Run σ) (status : CloseStatus) :
    (run.closeRun status).seq = run.seq := by
  unfold Run.closeRun
  split
  · rfl
  · exact seq_foldl_finishStep _ run

/-- Taking moves keeps `seq`. -/
theorem seq_foldl_newStep (moves : List (Move σ)) (origin : Origin σ) (run : Run σ) :
    (moves.foldl (fun current move =>
      current.newStep move.stepType move.input move.options origin) run).seq = run.seq := by
  induction moves generalizing run with
  | nil => rfl
  | cons move laterMoves inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [inductionHypothesis]
    rfl

/-- `decide` keeps `seq`. -/
theorem seq_applyDecision (run : Run σ) (step : StepExecId σ) (decision : Decision σ) :
    (run.applyDecision step decision).seq = run.seq := by
  simp only [Run.applyDecision]
  split
  · rw [seq_closeRun]
    unfold Run.collectOutput
    split <;> rfl
  · rw [seq_closeRun]
    rfl
  · rfl
  · rw [seq_foldl_newStep]
    rfl

/-- Firing one timer keeps `seq`. -/
theorem seq_fireTimer (run : Run σ) (timer : TimerId σ) : (run.fireTimer timer).seq = run.seq := by
  unfold Run.fireTimer
  split
  · simp only
    split
    · rfl
    · split
      · split
        · rfl
        · exact (evalStep_frame _ timer.step).2.2.1
      · rfl
  · rfl

/-- Firing timers keeps `seq`. -/
theorem seq_foldl_fireTimer (timers : List (TimerId σ)) (run : Run σ) :
    (timers.foldl Run.fireTimer run).seq = run.seq := by
  induction timers generalizing run with
  | nil => rfl
  | cons timer laterTimers inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [inductionHypothesis, seq_fireTimer]

/-- The empty run satisfies the invariant. -/
theorem Invariant.empty (IsTimerAllowed : TimerId σ → Prop) (seqBound : Nat) :
    Invariant IsTimerAllowed seqBound (Run.empty σ) := by
  refine { idsNodup := by simp [Run.empty], numbersCounted := ?_, waitsSupported := ?_,
           seqInjective := ?_, timersAllowed := ?_, waitersQuiet := ?_ }
  · intro record isMember
    simp [Run.empty] at isMember
  · intro record waitState isWaiting
    have isMember := isWaiting.1
    simp [Run.empty] at isMember
  · intro first second firstWait secondWait isFirstWaiting
    have isMember := isFirstWaiting.1
    simp [Run.empty] at isMember
  · intro entry isMember
    simp [Run.empty] at isMember
  · intro record waitState isWaiting
    have isMember := isWaiting.1
    simp [Run.empty] at isMember

/-- The start of a commit, `seq := seq + 1`, keeps the invariant, and the old
bound becomes the new `seq`. -/
theorem Invariant.bumpSeq {IsTimerAllowed : TimerId σ → Prop} {run : Run σ}
    (invariant : Invariant IsTimerAllowed (run.seq + 1) run) :
    Invariant IsTimerAllowed ({ run with seq := run.seq + 1 } : Run σ).seq
      { run with seq := run.seq + 1 } :=
  invariant.transfer rfl rfl (fun record _ isWaiting => ⟨record, isWaiting, rfl⟩)
    (fun _ isMember => isMember) (fun _ isEmpty => isEmpty)

/-- `eval(e)` changes nothing when a waiting `S[e]` has no plan. -/
theorem evalStep_of_unplanned (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (isQuiet : ∀ record waitState, run.findStep? step = some record → record.phase = .waiting →
      record.waitState = some waitState →
      Dex.Run.plan waitState.wait (fun chan => (run.pendingMessages chan).length)
        waitState.timersDone = none) :
    run.evalStep step = run := by
  cases foundCase : run.findStep? step with
  | none => simp [Dex.Run.evalStep, foundCase]
  | some record =>
    by_cases isWaiting : record.phase = .waiting
    · cases waitCase : record.waitState with
      | none => simp [Dex.Run.evalStep, foundCase, isWaiting, waitCase]
      | some waitState =>
        exact Dex.Run.evalStep_of_plan_none run step record waitState foundCase waitCase
          (isQuiet record waitState foundCase isWaiting waitCase)
    · exact Dex.Run.evalStep_of_not_waiting run step record foundCase isWaiting

/-- Evaluating Steps that have no plan changes nothing. -/
theorem foldl_evalStep_of_unplanned (steps : List (StepExecId σ)) (run : Run σ)
    (isUnplanned : ∀ step, step ∈ steps → ∀ record waitState,
      run.findStep? step = some record → record.phase = .waiting →
      record.waitState = some waitState →
      plan waitState.wait (fun chan => (run.pendingMessages chan).length)
        waitState.timersDone = none) :
    steps.foldl Run.evalStep run = run := by
  induction steps with
  | nil => rfl
  | cons head laterSteps inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [evalStep_of_unplanned run head (isUnplanned head List.mem_cons_self)]
    exact inductionHypothesis (fun step isStep => isUnplanned step (List.mem_cons_of_mem _ isStep))

/-- A supported wait that names a Channel has no plan while none of its
timers is done and every Channel it names is empty. -/
theorem plan_none_of_quiet (wait : Wait σ) (pendingCount : σ.Chan → Nat)
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

/-- `eval(e)` keeps the Attributes, the status, and the outputs. -/
theorem evalStep_keeps_data (run : Dex.Run σ) (step : Dex.StepExecId σ) :
    (run.evalStep step).attributes = run.attributes ∧
    (run.evalStep step).status = run.status ∧
    (run.evalStep step).outputs = run.outputs := by
  rcases Dex.Run.evalStep_shape run step with isSame | ⟨chans, consumeFlags, change, _, isEval⟩
  · rw [isSame]
    exact ⟨rfl, rfl, rfl⟩
  · rw [isEval]
    obtain ⟨_, _, _, _, sameStatus, sameAttributes, sameOutputs⟩ :=
      Dex.Run.consumeMessages_frame chans consumeFlags (run.disarmTimersOf step)
    exact ⟨sameAttributes, sameStatus, sameOutputs⟩

/-- Evaluating Steps keeps the Attributes, the counters, the status, and the outputs. -/
theorem foldl_evalStep_keeps_data (steps : List (Dex.StepExecId σ)) (run : Dex.Run σ) :
    (steps.foldl Dex.Run.evalStep run).attributes = run.attributes ∧
    (steps.foldl Dex.Run.evalStep run).counters = run.counters ∧
    (steps.foldl Dex.Run.evalStep run).status = run.status ∧
    (steps.foldl Dex.Run.evalStep run).outputs = run.outputs := by
  induction steps generalizing run with
  | nil => exact ⟨rfl, rfl, rfl, rfl⟩
  | cons step laterSteps inductionHypothesis =>
    simp only [List.foldl_cons]
    obtain ⟨sameAttributes, sameCounters, sameStatus, sameOutputs⟩ :=
      inductionHypothesis (run.evalStep step)
    obtain ⟨evalAttributes, evalStatus, evalOutputs⟩ := evalStep_keeps_data run step
    rw [sameAttributes, sameCounters, sameStatus, sameOutputs, evalAttributes, evalStatus,
      evalOutputs, (Dex.Run.evalStep_frame run step).2.1]
    exact ⟨rfl, rfl, rfl, rfl⟩

/-- `effects` keeps the counters, the status, and the outputs, and changes
the Attributes by its writes only. -/
theorem applyEffects_keeps_data (run : Dex.Run σ) (effects : Dex.Effects σ) :
    (run.applyEffects effects).attributes = (run.applyWrites effects.writes).attributes ∧
    (run.applyEffects effects).counters = run.counters ∧
    (run.applyEffects effects).status = run.status ∧
    (run.applyEffects effects).outputs = run.outputs := by
  unfold Dex.Run.applyEffects Dex.Run.wake
  obtain ⟨sameAttributes, sameCounters, sameStatus, sameOutputs⟩ :=
    foldl_evalStep_keeps_data
      (((run.applyWrites effects.writes).applyPublications effects.publications).waitersOn
        (effects.publications.map (fun publication => publication.1)))
      ((run.applyWrites effects.writes).applyPublications effects.publications)
  obtain ⟨_, pubCounters, _, _, pubStatus, pubAttributes, pubOutputs, _⟩ :=
    Dex.Run.applyPublications_frame effects.publications (run.applyWrites effects.writes)
  obtain ⟨_, writeCounters, _, _, _, writeStatus, writeOutputs⟩ :=
    Dex.Run.applyWrites_frame effects.writes run
  rw [sameAttributes, sameCounters, sameStatus, sameOutputs, pubAttributes, pubCounters,
    pubStatus, pubOutputs, writeCounters, writeStatus, writeOutputs]
  exact ⟨rfl, rfl, rfl, rfl⟩

end Run

end DexExpressiveness.Dex
