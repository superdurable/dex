import DexExpressiveness.Dex.RunInvariant
import DexExpressiveness.Failure.Proofs.WPF1Lists

/-!
# WP-F1: the meta-functions commute with relabeling

α changes two things in the Step table of a run: the state of each
invocation (by the attempt state and `par`), and the `sws` of each wait (by
`ζ`). `relabelRun` makes both changes for a fixed view and a fixed relabeling.
The meta-functions of spec §4.2 never read an invocation state and read
`sws` only to order waiters, so each one commutes with `relabelRun` when the
invocations it writes are pending under the view and `ζ` keeps the order of
the waiters.
-/

namespace DexExpressiveness.DexF.WPF1

variable {σ : Dex.Signature}

/-- The state α gives an invocation, by Step execution and method. -/
abbrev InvocationView (σ : Dex.Signature) := Dex.StepExecId σ → Dex.Method → Dex.InvocationState

/-- One Step record with its invocation state taken from the view and its
`sws` relabeled. -/
def relabelRecord (view : InvocationView σ) (relabel : Nat → Nat) (record : Dex.StepRecord σ) :
    Dex.StepRecord σ :=
  { record with
    invocation := record.invocation.map (fun invocation =>
      { invocation with state := view record.id invocation.method })
    waitState := record.waitState.map (fun waitState =>
      { waitState with registeredSeq := relabel waitState.registeredSeq }) }

/-- A run with every Step record relabeled and `seq` relabeled. -/
def relabelRun (view : InvocationView σ) (relabel : Nat → Nat) (run : Dex.Run σ) : Dex.Run σ :=
  { run with seq := relabel run.seq, steps := run.steps.map (relabelRecord view relabel) }

/-- Every waiting Step's Execute invocation is pending under the view. -/
def IsWaitFresh (view : InvocationView σ) (run : Dex.Run σ) : Prop :=
  ∀ record, record ∈ run.steps → record.phase = .waiting → view record.id .execute = .pending

/-- Every Step execution ID that `new` may still mint is pending under the view. -/
def IsCounterFresh (view : InvocationView σ) (run : Dex.Run σ) : Prop :=
  ∀ stepType number method, (run.counters.get? stepType).getD 0 < number →
    view { stepType := stepType, number := number } method = .pending

/-- The relabeling keeps the order of the `sws` of the waiting Steps. -/
def IsOrderKept (relabel : Nat → Nat) (run : Dex.Run σ) : Prop :=
  ∀ first, first ∈ run.steps → ∀ second, second ∈ run.steps → ∀ firstWait secondWait,
    first.phase = .waiting → second.phase = .waiting → first.waitState = some firstWait →
    second.waitState = some secondWait →
    decide (relabel firstWait.registeredSeq ≤ relabel secondWait.registeredSeq) =
      decide (firstWait.registeredSeq ≤ secondWait.registeredSeq)

/-! ## Fields -/

section Fields

variable (view : InvocationView σ) (relabel : Nat → Nat)

@[simp] theorem relabelRecord_id (record : Dex.StepRecord σ) :
    (relabelRecord view relabel record).id = record.id := rfl

@[simp] theorem relabelRecord_phase (record : Dex.StepRecord σ) :
    (relabelRecord view relabel record).phase = record.phase := rfl

@[simp] theorem relabelRecord_input (record : Dex.StepRecord σ) :
    (relabelRecord view relabel record).input = record.input := rfl

@[simp] theorem relabelRecord_options (record : Dex.StepRecord σ) :
    (relabelRecord view relabel record).options = record.options := rfl

@[simp] theorem relabelRecord_origin (record : Dex.StepRecord σ) :
    (relabelRecord view relabel record).origin = record.origin := rfl

@[simp] theorem relabelRecord_conditionResults (record : Dex.StepRecord σ) :
    (relabelRecord view relabel record).conditionResults = record.conditionResults := rfl

@[simp] theorem relabelRecord_waitState (record : Dex.StepRecord σ) :
    (relabelRecord view relabel record).waitState = record.waitState.map (fun waitState =>
      { waitState with registeredSeq := relabel waitState.registeredSeq }) := rfl

@[simp] theorem relabelRecord_invocation (record : Dex.StepRecord σ) :
    (relabelRecord view relabel record).invocation = record.invocation.map (fun invocation =>
      { invocation with state := view record.id invocation.method }) := rfl

@[simp] theorem relabelRun_status (run : Dex.Run σ) :
    (relabelRun view relabel run).status = run.status := rfl

@[simp] theorem relabelRun_outputs (run : Dex.Run σ) :
    (relabelRun view relabel run).outputs = run.outputs := rfl

@[simp] theorem relabelRun_counters (run : Dex.Run σ) :
    (relabelRun view relabel run).counters = run.counters := rfl

@[simp] theorem relabelRun_attributes (run : Dex.Run σ) :
    (relabelRun view relabel run).attributes = run.attributes := rfl

@[simp] theorem relabelRun_channels (run : Dex.Run σ) :
    (relabelRun view relabel run).channels = run.channels := rfl

@[simp] theorem relabelRun_timers (run : Dex.Run σ) :
    (relabelRun view relabel run).timers = run.timers := rfl

@[simp] theorem relabelRun_seq (run : Dex.Run σ) :
    (relabelRun view relabel run).seq = relabel run.seq := rfl

@[simp] theorem relabelRun_steps (run : Dex.Run σ) :
    (relabelRun view relabel run).steps = run.steps.map (relabelRecord view relabel) := rfl

theorem findStep?_relabelRun (run : Dex.Run σ) (step : Dex.StepExecId σ) :
    (relabelRun view relabel run).findStep? step =
      (run.findStep? step).map (relabelRecord view relabel) := by
  simp only [Dex.Run.findStep?, relabelRun_steps, List.find?_map]
  rfl

theorem pendingMessages_relabelRun (run : Dex.Run σ) (chan : σ.Chan) :
    (relabelRun view relabel run).pendingMessages chan = run.pendingMessages chan := rfl

theorem setPendingMessages_relabelRun (run : Dex.Run σ) (chan : σ.Chan) (messages : List σ.Val) :
    (relabelRun view relabel run).setPendingMessages chan messages =
      relabelRun view relabel (run.setPendingMessages chan messages) := rfl

theorem disarmTimersOf_relabelRun (run : Dex.Run σ) (step : Dex.StepExecId σ) :
    (relabelRun view relabel run).disarmTimersOf step =
      relabelRun view relabel (run.disarmTimersOf step) := rfl

theorem snapshot_relabelRun (run : Dex.Run σ) (options : Dex.StepOptions σ)
    (method : Dex.Method) :
    (relabelRun view relabel run).snapshot options method = run.snapshot options method := rfl

theorem dueTimers_relabelRun (run : Dex.Run σ) (clock : Nat) :
    (relabelRun view relabel run).dueTimers clock = run.dueTimers clock := rfl

end Fields

/-! ## Commutation -/

section Commutation

variable (view : InvocationView σ) (relabel : Nat → Nat)

theorem updateStep_relabelRun (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (change change' : Dex.StepRecord σ → Dex.StepRecord σ)
    (commutes : ∀ record, record.id = step →
      change (relabelRecord view relabel record) = relabelRecord view relabel (change' record)) :
    (relabelRun view relabel run).updateStep step change =
      relabelRun view relabel (run.updateStep step change') := by
  simp only [Dex.Run.updateStep, relabelRun, List.map_map]
  congr 1
  apply List.map_congr_left
  intro record _
  by_cases isStep : record.id = step
  · simp [isStep, commutes record isStep]
  · simp [isStep]

theorem applyWrites_relabelRun (run : Dex.Run σ) (writes : List (σ.Key × Option σ.Val)) :
    (relabelRun view relabel run).applyWrites writes =
      relabelRun view relabel (run.applyWrites writes) := by
  induction writes generalizing run with
  | nil => rfl
  | cons write laterWrites inductionHypothesis =>
    obtain ⟨key, value⟩ := write
    cases value with
    | none => exact inductionHypothesis { run with attributes := run.attributes.erase key }
    | some value => exact inductionHypothesis { run with attributes := run.attributes.set key value }

theorem applyPublications_relabelRun (run : Dex.Run σ) (publications : List (σ.Chan × σ.Val)) :
    (relabelRun view relabel run).applyPublications publications =
      relabelRun view relabel (run.applyPublications publications) := by
  induction publications generalizing run with
  | nil => rfl
  | cons publication laterPublications inductionHypothesis =>
    exact inductionHypothesis (run.setPendingMessages publication.1
      (run.pendingMessages publication.1 ++ [publication.2]))

theorem consumeMessages_relabelRun (chans : List σ.Chan) (consumeFlags : List Bool)
    (run : Dex.Run σ) :
    Dex.Run.consumeMessages chans consumeFlags (relabelRun view relabel run) =
      (relabelRun view relabel (Dex.Run.consumeMessages chans consumeFlags run).1,
        (Dex.Run.consumeMessages chans consumeFlags run).2) := by
  induction chans generalizing consumeFlags run with
  | nil => rfl
  | cons chan laterChans inductionHypothesis =>
    simp only [Dex.Run.consumeMessages, pendingMessages_relabelRun]
    split
    · rw [setPendingMessages_relabelRun, inductionHypothesis]
    · rw [inductionHypothesis]

theorem evalStep_relabelRun (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (isFresh : IsWaitFresh view run) :
    (relabelRun view relabel run).evalStep step = relabelRun view relabel (run.evalStep step) := by
  unfold Dex.Run.evalStep
  rw [findStep?_relabelRun]
  cases isFound : run.findStep? step with
  | none => rfl
  | some record =>
    have isMember := (Dex.Run.mem_of_findStep? run step record isFound)
    simp only [Option.map_some, relabelRecord_phase, relabelRecord_waitState]
    cases isPhase : record.phase with
    | waiting =>
      cases hasWait : record.waitState with
      | none => rfl
      | some waitState =>
        simp only [Option.map_some, pendingMessages_relabelRun]
        cases Dex.Run.plan waitState.wait (fun chan => (run.pendingMessages chan).length)
            waitState.timersDone with
        | none => rfl
        | some consumeFlags =>
          simp only [disarmTimersOf_relabelRun, consumeMessages_relabelRun]
          apply updateStep_relabelRun
          intro other isOther
          have isPending : view step .execute = .pending := by
            have := isFresh record isMember.1 isPhase
            rwa [isMember.2] at this
          simp [relabelRecord, isOther, isPending]
    | waitFor => rfl
    | execute => rfl
    | completed => rfl
    | canceled => rfl

end Commutation

/-! ## Freshness and order are kept -/

section Keeping

variable {view : InvocationView σ} {relabel : Nat → Nat}

theorem IsWaitFresh.of_steps {run other : Dex.Run σ} (isFresh : IsWaitFresh view run)
    (sameSteps : other.steps = run.steps) : IsWaitFresh view other := by
  intro record isMember
  rw [sameSteps] at isMember
  exact isFresh record isMember

theorem IsOrderKept.of_steps {run other : Dex.Run σ} (isKept : IsOrderKept relabel run)
    (sameSteps : other.steps = run.steps) : IsOrderKept relabel other := by
  intro first isFirst second isSecond
  rw [sameSteps] at isFirst isSecond
  exact isKept first isFirst second isSecond

theorem IsCounterFresh.of_counters {run other : Dex.Run σ} (isFresh : IsCounterFresh view run)
    (sameCounters : other.counters = run.counters) : IsCounterFresh view other := by
  intro stepType number method isAbove
  rw [sameCounters] at isAbove
  exact isFresh stepType number method isAbove

theorem IsWaitFresh.updateStep {run : Dex.Run σ} (isFresh : IsWaitFresh view run)
    (step : Dex.StepExecId σ) (change : Dex.StepRecord σ → Dex.StepRecord σ)
    (keepsId : ∀ record, (change record).id = record.id)
    (staysFresh : ∀ record, record ∈ run.steps → record.id = step →
      (change record).phase = .waiting → view record.id .execute = .pending) :
    IsWaitFresh view (run.updateStep step change) := by
  intro record isMember isWaiting
  obtain ⟨original, isOriginal, isRecord⟩ := (Dex.Run.mem_updateStep _ _ _ _).mp isMember
  by_cases isStep : original.id = step
  · simp only [isStep, ↓reduceIte] at isRecord
    subst isRecord
    rw [keepsId]
    exact staysFresh original isOriginal isStep isWaiting
  · simp only [isStep, ↓reduceIte] at isRecord
    subst isRecord
    exact isFresh record isOriginal isWaiting

theorem IsOrderKept.updateStep {run : Dex.Run σ} (isKept : IsOrderKept relabel run)
    (step : Dex.StepExecId σ) (change : Dex.StepRecord σ → Dex.StepRecord σ)
    (keepsPhase : ∀ record, (change record).phase = record.phase)
    (keepsWait : ∀ record, (change record).waitState = record.waitState) :
    IsOrderKept relabel (run.updateStep step change) := by
  have origin : ∀ record, record ∈ (run.updateStep step change).steps →
      ∃ original, original ∈ run.steps ∧ original.phase = record.phase ∧
        original.waitState = record.waitState := by
    intro record isMember
    obtain ⟨original, isOriginal, isRecord⟩ := (Dex.Run.mem_updateStep _ _ _ _).mp isMember
    refine ⟨original, isOriginal, ?_, ?_⟩ <;>
      by_cases isStep : original.id = step <;> simp [isStep, isRecord, keepsPhase, keepsWait]
  intro first isFirst second isSecond firstWait secondWait firstPhase secondPhase firstHas secondHas
  obtain ⟨firstOriginal, isFirstOriginal, samePhase, sameWait⟩ := origin first isFirst
  obtain ⟨secondOriginal, isSecondOriginal, sameSecondPhase, sameSecondWait⟩ :=
    origin second isSecond
  exact isKept firstOriginal isFirstOriginal secondOriginal isSecondOriginal firstWait secondWait
    (samePhase.trans firstPhase) (sameSecondPhase.trans secondPhase) (sameWait.trans firstHas)
    (sameSecondWait.trans secondHas)

theorem IsWaitFresh.evalStep {run : Dex.Run σ} (isFresh : IsWaitFresh view run)
    (step : Dex.StepExecId σ) : IsWaitFresh view (run.evalStep step) := by
  rcases Dex.Run.evalStep_cases run step with ⟨isSame, _⟩ |
    ⟨record, waitState, consumeFlags, _, _, _, isEval⟩
  · rw [isSame]
    exact isFresh
  · rw [isEval]
    apply IsWaitFresh.updateStep
    · apply isFresh.of_steps
      rw [(Dex.Run.consumeMessages_frame _ _ _).1]
      rfl
    · intro _
      rfl
    · intro _ _ _ isWaiting
      cases isWaiting

theorem IsWaitFresh.foldl_evalStep (steps : List (Dex.StepExecId σ)) {run : Dex.Run σ}
    (isFresh : IsWaitFresh view run) : IsWaitFresh view (steps.foldl Dex.Run.evalStep run) := by
  induction steps generalizing run with
  | nil => exact isFresh
  | cons step laterSteps inductionHypothesis =>
    exact inductionHypothesis (isFresh.evalStep step)

theorem IsWaitFresh.applyEffects {run : Dex.Run σ} (isFresh : IsWaitFresh view run)
    (effects : Dex.Effects σ) : IsWaitFresh view (run.applyEffects effects) := by
  apply IsWaitFresh.foldl_evalStep
  apply isFresh.of_steps
  rw [(Dex.Run.applyPublications_frame _ _).1, (Dex.Run.applyWrites_frame _ _).1]

theorem counters_foldl_evalStep (steps : List (Dex.StepExecId σ)) (run : Dex.Run σ) :
    (steps.foldl Dex.Run.evalStep run).counters = run.counters := by
  induction steps generalizing run with
  | nil => rfl
  | cons step laterSteps inductionHypothesis =>
    exact (inductionHypothesis _).trans (Dex.Run.evalStep_frame run step).2.1

theorem counters_applyEffects (run : Dex.Run σ) (effects : Dex.Effects σ) :
    (run.applyEffects effects).counters = run.counters := by
  unfold Dex.Run.applyEffects Dex.Run.wake
  rw [counters_foldl_evalStep, (Dex.Run.applyPublications_frame _ _).2.1,
    (Dex.Run.applyWrites_frame _ _).2.1]

theorem IsCounterFresh.newStep {run : Dex.Run σ} (isFresh : IsCounterFresh view run)
    (stepType : σ.StepType) (input : Option σ.Val) (options : Dex.StepOptions σ)
    (origin : Dex.Origin σ) : IsCounterFresh view (run.newStep stepType input options origin) := by
  intro otherType number method isAbove
  apply isFresh
  simp only [Dex.Run.newStep] at isAbove
  by_cases isType : otherType = stepType
  · subst isType
    rw [AssocList.get?_set_self] at isAbove
    simp only [Option.getD_some] at isAbove
    omega
  · rwa [AssocList.get?_set_other _ _ _ _ isType] at isAbove

theorem IsCounterFresh.foldl_newStep (moves : List (Dex.Move σ)) (origin : Dex.Origin σ)
    {run : Dex.Run σ} (isFresh : IsCounterFresh view run) :
    IsCounterFresh view (moves.foldl (fun current move =>
      current.newStep move.stepType move.input move.options origin) run) := by
  induction moves generalizing run with
  | nil => exact isFresh
  | cons move laterMoves inductionHypothesis =>
    exact inductionHypothesis (isFresh.newStep _ _ _ _)

theorem IsWaitFresh.fireTimer {run : Dex.Run σ} (isFresh : IsWaitFresh view run)
    (timer : Dex.TimerId σ) : IsWaitFresh view (run.fireTimer timer) := by
  unfold Dex.Run.fireTimer
  split
  · dsimp only
    split
    · exact isFresh.of_steps rfl
    · split
      · split
        · exact isFresh.of_steps rfl
        · apply IsWaitFresh.evalStep
          apply IsWaitFresh.updateStep
          · exact isFresh.of_steps rfl
          · intro _
            rfl
          · intro record isMember _ isWaiting
            exact isFresh record isMember isWaiting
      · exact isFresh.of_steps rfl
  · exact isFresh

end Keeping

/-! ## Commutation of the composite meta-functions -/

section Composite

variable (view : InvocationView σ) (relabel : Nat → Nat)

/-- The waiter entry of one Step record, as `waitersOn` builds it. -/
def waiterOf (changed : List σ.Chan) (record : Dex.StepRecord σ) :
    Option (Nat × Dex.StepExecId σ) :=
  match record.phase, record.waitState with
  | .waiting, some waitState =>
    if waitState.wait.channels.any (fun chan => changed.contains chan)
    then some (waitState.registeredSeq, record.id) else none
  | _, _ => none

theorem waitersOn_eq (run : Dex.Run σ) (changed : List σ.Chan) :
    run.waitersOn changed =
      (sortStable (fun first second => decide (first.1 ≤ second.1))
        (run.steps.filterMap (waiterOf changed))).map (fun waiter => waiter.2) := rfl

theorem waiterOf_relabelRecord (changed : List σ.Chan) (record : Dex.StepRecord σ) :
    waiterOf changed (relabelRecord view relabel record) =
      (waiterOf changed record).map (fun waiter => (relabel waiter.1, waiter.2)) := by
  unfold waiterOf
  simp only [relabelRecord_phase, relabelRecord_waitState]
  cases record.phase <;> cases record.waitState <;> simp

theorem waitersOn_relabelRun (run : Dex.Run σ) (changed : List σ.Chan)
    (isKept : IsOrderKept relabel run) :
    (relabelRun view relabel run).waitersOn changed = run.waitersOn changed := by
  rw [waitersOn_eq, waitersOn_eq, relabelRun_steps, List.filterMap_map]
  have sameWaiters : run.steps.filterMap (waiterOf changed ∘ relabelRecord view relabel) =
      (run.steps.filterMap (waiterOf changed)).map (fun waiter => (relabel waiter.1, waiter.2)) := by
    rw [List.map_filterMap]
    congr 1
    funext record
    exact waiterOf_relabelRecord view relabel changed record
  rw [sameWaiters, sortStable_map, List.map_map]
  · rfl
  · intro first isFirst second isSecond
    obtain ⟨firstRecord, isFirstRecord, firstIs⟩ := List.mem_filterMap.mp isFirst
    obtain ⟨secondRecord, isSecondRecord, secondIs⟩ := List.mem_filterMap.mp isSecond
    unfold waiterOf at firstIs secondIs
    split at firstIs
    · rename_i firstWait firstPhase firstHas
      split at secondIs
      · rename_i secondWait secondPhase secondHas
        split at firstIs
        · split at secondIs
          · cases firstIs
            cases secondIs
            exact isKept firstRecord isFirstRecord secondRecord isSecondRecord firstWait secondWait
              firstPhase secondPhase firstHas secondHas
          · cases secondIs
        · cases firstIs
      · cases secondIs
    · cases firstIs

theorem foldl_evalStep_relabelRun (steps : List (Dex.StepExecId σ)) (run : Dex.Run σ)
    (isFresh : IsWaitFresh view run) :
    steps.foldl Dex.Run.evalStep (relabelRun view relabel run) =
      relabelRun view relabel (steps.foldl Dex.Run.evalStep run) := by
  induction steps generalizing run with
  | nil => rfl
  | cons step laterSteps inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [evalStep_relabelRun view relabel run step isFresh]
    exact inductionHypothesis _ (isFresh.evalStep step)

theorem wake_relabelRun (run : Dex.Run σ) (changed : List σ.Chan)
    (isFresh : IsWaitFresh view run) (isKept : IsOrderKept relabel run) :
    (relabelRun view relabel run).wake changed = relabelRun view relabel (run.wake changed) := by
  unfold Dex.Run.wake
  rw [waitersOn_relabelRun view relabel run changed isKept]
  exact foldl_evalStep_relabelRun view relabel _ run isFresh

theorem applyEffects_relabelRun (run : Dex.Run σ) (effects : Dex.Effects σ)
    (isFresh : IsWaitFresh view run) (isKept : IsOrderKept relabel run) :
    (relabelRun view relabel run).applyEffects effects =
      relabelRun view relabel (run.applyEffects effects) := by
  unfold Dex.Run.applyEffects
  have sameSteps : ((run.applyWrites effects.writes).applyPublications effects.publications).steps =
      run.steps := by
    rw [(Dex.Run.applyPublications_frame _ _).1, (Dex.Run.applyWrites_frame _ _).1]
  rw [applyWrites_relabelRun, applyPublications_relabelRun,
    wake_relabelRun view relabel _ _ (isFresh.of_steps sameSteps) (isKept.of_steps sameSteps)]

theorem registerWait_relabelRun (run : Dex.Run σ) (step : Dex.StepExecId σ) (wait : Dex.Wait σ)
    (now : Nat) (isPending : view step .execute = .pending) (isFresh : IsWaitFresh view run) :
    (relabelRun view relabel run).registerWait step wait now =
      relabelRun view relabel (run.registerWait step wait now) := by
  unfold Dex.Run.registerWait
  split
  · apply updateStep_relabelRun
    intro record isStep
    simp [relabelRecord, isStep, isPending]
  · dsimp only
    have armedEq : ({ relabelRun view relabel run with
          timers := Dex.Run.armTimers step
            (wait.timerSeconds.map (fun seconds => now + seconds * 1000000)) 0
            (relabelRun view relabel run).timers } : Dex.Run σ) =
        relabelRun view relabel { run with
          timers := Dex.Run.armTimers step
            (wait.timerSeconds.map (fun seconds => now + seconds * 1000000)) 0 run.timers } := rfl
    rw [armedEq, relabelRun_seq]
    rw [updateStep_relabelRun view relabel _ step _
      (fun current =>
        { current with
          phase := .waiting
          waitState := some
            { wait := wait
              fireAt := wait.timerSeconds.map (fun seconds => now + seconds * 1000000)
              timersDone := wait.timerSeconds.map (fun _ => false)
              registeredSeq := run.seq } })]
    · apply evalStep_relabelRun
      apply IsWaitFresh.updateStep
      · exact isFresh.of_steps rfl
      · intro _
        rfl
      · intro record _ isStep _
        rw [isStep]
        exact isPending
    · intro record _
      rfl

theorem finishStep_relabelRun (run : Dex.Run σ) (step : Dex.StepExecId σ) (finalPhase : Dex.Phase) :
    (relabelRun view relabel run).finishStep step finalPhase =
      relabelRun view relabel (run.finishStep step finalPhase) := by
  unfold Dex.Run.finishStep
  rw [disarmTimersOf_relabelRun]
  apply updateStep_relabelRun
  intro record _
  rfl

theorem foldl_finishStep_relabelRun (steps : List (Dex.StepExecId σ)) (finalPhase : Dex.Phase)
    (run : Dex.Run σ) :
    steps.foldl (fun current step => current.finishStep step finalPhase)
        (relabelRun view relabel run) =
      relabelRun view relabel
        (steps.foldl (fun current step => current.finishStep step finalPhase) run) := by
  induction steps generalizing run with
  | nil => rfl
  | cons step laterSteps inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [finishStep_relabelRun]
    exact inductionHypothesis _

theorem closeRun_relabelRun (run : Dex.Run σ) (status : Dex.CloseStatus) :
    (relabelRun view relabel run).closeRun status =
      relabelRun view relabel (run.closeRun status) := by
  unfold Dex.Run.closeRun
  rw [relabelRun_status]
  cases run.status with
  | closed => rfl
  | running =>
    dsimp only
    have sameActive :
        ((relabelRun view relabel run).steps.filter (fun record => record.phase.isActive)).map
            (fun record => record.id) =
          (run.steps.filter (fun record => record.phase.isActive)).map (fun record => record.id) := by
      simp [List.filter_map, List.map_map, Function.comp_def]
    rw [sameActive, foldl_finishStep_relabelRun]
    rfl

theorem collectOutput_relabelRun (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (output : Option σ.Val) :
    (relabelRun view relabel run).collectOutput step output =
      relabelRun view relabel (run.collectOutput step output) := by
  cases output <;> rfl

theorem newStep_relabelRun (run : Dex.Run σ) (stepType : σ.StepType) (input : Option σ.Val)
    (options : Dex.StepOptions σ) (origin : Dex.Origin σ) (isFresh : IsCounterFresh view run) :
    (relabelRun view relabel run).newStep stepType input options origin =
      relabelRun view relabel (run.newStep stepType input options origin) := by
  have isPending : ∀ method,
      view { stepType := stepType, number := (run.counters.get? stepType).getD 0 + 1 } method =
        .pending := fun method => isFresh _ _ method (Nat.lt_succ_self _)
  simp [Dex.Run.newStep, relabelRun, relabelRecord, isPending]

theorem foldl_newStep_relabelRun (moves : List (Dex.Move σ)) (origin : Dex.Origin σ)
    (run : Dex.Run σ) (isFresh : IsCounterFresh view run) :
    moves.foldl (fun current move => current.newStep move.stepType move.input move.options origin)
        (relabelRun view relabel run) =
      relabelRun view relabel
        (moves.foldl (fun current move =>
          current.newStep move.stepType move.input move.options origin) run) := by
  induction moves generalizing run with
  | nil => rfl
  | cons move laterMoves inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [newStep_relabelRun view relabel run _ _ _ _ isFresh]
    exact inductionHypothesis _ (isFresh.newStep _ _ _ _)

theorem applyDecision_relabelRun (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (decision : Dex.Decision σ) (isFresh : IsCounterFresh view run) :
    (relabelRun view relabel run).applyDecision step decision =
      relabelRun view relabel (run.applyDecision step decision) := by
  unfold Dex.Run.applyDecision
  rw [finishStep_relabelRun]
  dsimp only
  split
  · rw [collectOutput_relabelRun, closeRun_relabelRun]
  · rw [closeRun_relabelRun]
  · rfl
  · exact foldl_newStep_relabelRun view relabel _ _ _ (isFresh.of_counters rfl)

theorem fireTimer_relabelRun (run : Dex.Run σ) (timer : Dex.TimerId σ)
    (isFresh : IsWaitFresh view run) :
    (relabelRun view relabel run).fireTimer timer = relabelRun view relabel (run.fireTimer timer) := by
  unfold Dex.Run.fireTimer
  rw [relabelRun_timers]
  split
  · dsimp only
    have disarmedEq : ({ relabelRun view relabel run with timers := run.timers.erase timer } :
        Dex.Run σ) = relabelRun view relabel { run with timers := run.timers.erase timer } := rfl
    rw [disarmedEq, findStep?_relabelRun]
    cases isFound : ({ run with timers := run.timers.erase timer } : Dex.Run σ).findStep?
        timer.step with
    | none => rfl
    | some record =>
      simp only [Option.map_some, relabelRecord_phase, relabelRecord_waitState]
      cases record.phase with
      | waiting =>
        cases record.waitState with
        | none => rfl
        | some waitState =>
          simp only [Option.map_some]
          split
          · rfl
          · rw [updateStep_relabelRun view relabel _ timer.step _
              (fun current =>
                { current with
                  waitState := some
                    { waitState with timersDone := waitState.timersDone.set timer.index true } })]
            · apply evalStep_relabelRun
              apply IsWaitFresh.updateStep
              · exact isFresh.of_steps rfl
              · intro _
                rfl
              · intro record isMember _ isWaiting
                exact isFresh record isMember isWaiting
            · intro _ _
              rfl
      | waitFor => rfl
      | execute => rfl
      | completed => rfl
      | canceled => rfl
  · rfl

theorem foldl_fireTimer_relabelRun (timers : List (Dex.TimerId σ)) (run : Dex.Run σ)
    (isFresh : IsWaitFresh view run) :
    timers.foldl Dex.Run.fireTimer (relabelRun view relabel run) =
      relabelRun view relabel (timers.foldl Dex.Run.fireTimer run) := by
  induction timers generalizing run with
  | nil => rfl
  | cons timer laterTimers inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [fireTimer_relabelRun view relabel run timer isFresh]
    exact inductionHypothesis _ (isFresh.fireTimer timer)

end Composite

end DexExpressiveness.DexF.WPF1
