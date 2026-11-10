import DexExpressiveness.Failure.Composite.WPF6Lift
import DexExpressiveness.Failure.Composite.WPF6Sequences
import DexExpressiveness.Core.BlockRun

/-!
# WP-F6: liveness, the backward inclusion for words without retries

A fair Dex run for `w↓` lifts step by step to a DexF run with no failure
(`wpf6_lift_step`). When `w` has no retry, `w↓` is `w` without request IDs,
so the lifted run is a run for `w`. It has the Dex trace with ghost indexes,
none of them repeated, the same marker, and it is fair: FF, FR, and FI hold
because nothing fails, and F2 to F5 hold because α is transparent on it.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## The lifted run -/

/-- A DexF configuration that follows a Dex configuration with no failure. -/
def wpf6Follows (program : Program σ) (dexConfig : Dex.Config σ) (config : Config σ) : Prop :=
  Invariant program config ∧ NoFailure program config ∧ abstractConfig config = dexConfig

/-- The configuration that follows a Dex step exists, with the step that
reaches it. -/
theorem wpf6_lift_next (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (startRequestId : Nat)
    (dexConfig : Dex.Config σ) (config : Config σ) (follows : wpf6Follows program dexConfig config)
    (dexAction : Dex.Action σ) (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow dexConfig dexAction = some (dexEvents, dexNext)) :
    ∃ next, wpf6Follows program dexNext next ∧
      ∃ events, step program config (liftAction startRequestId dexAction) = some (events, next) ∧
        dexEvents = events.map Obs.toDex ∧ eraseStepEvents config events = events.map Obs.toDex := by
  obtain ⟨invariant, noFailure, isAbstract⟩ := follows
  rw [← isAbstract] at isDexStep
  obtain ⟨events, next, isStep, isNextAbstract, isEvents, isErased, nextNoFailure⟩ :=
    wpf6_lift_step program ignoresAttemptContext startRequestId config invariant noFailure
      dexAction dexEvents dexNext isDexStep
  exact ⟨next, ⟨invariant_step program ignoresAttemptContext config invariant _ events next isStep,
    nextNoFailure, isNextAbstract⟩, events, isStep, isEvents, isErased⟩

/-- One lifted step along a Dex run. -/
noncomputable def wpf6LiftOne (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (startRequestId : Nat)
    (dexRun : InfiniteRun (Dex.Config σ) (Dex.Action σ))
    (isDexRun : (Dex.system program.failureFreeFlow).IsRun dexRun) (index : Nat)
    (previous : { config : Config σ // wpf6Follows program (dexRun.states index) config }) :
    { config : Config σ // wpf6Follows program (dexRun.states (index + 1)) config } :=
  let isNext := wpf6_lift_next program ignoresAttemptContext startRequestId _ previous.1 previous.2
    (dexRun.labels index) _ _
    ((Dex.system program.failureFreeFlow).step_eq_eventsAt dexRun isDexRun.2 index)
  ⟨Classical.choose isNext, (Classical.choose_spec isNext).1⟩

/-- The lifted step is a DexF step with the Dex events and no repeated `fx`. -/
theorem wpf6_liftOne_spec (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (startRequestId : Nat)
    (dexRun : InfiniteRun (Dex.Config σ) (Dex.Action σ))
    (isDexRun : (Dex.system program.failureFreeFlow).IsRun dexRun) (index : Nat)
    (previous : { config : Config σ // wpf6Follows program (dexRun.states index) config }) :
    ∃ events, step program previous.1 (liftAction startRequestId (dexRun.labels index)) =
        some (events, (wpf6LiftOne program ignoresAttemptContext startRequestId dexRun isDexRun
          index previous).1) ∧
      (Dex.system program.failureFreeFlow).eventsAt dexRun index = events.map Obs.toDex ∧
      eraseStepEvents previous.1 events = events.map Obs.toDex :=
  (Classical.choose_spec (wpf6_lift_next program ignoresAttemptContext startRequestId _ previous.1
    previous.2 (dexRun.labels index) _ _
    ((Dex.system program.failureFreeFlow).step_eq_eventsAt dexRun isDexRun.2 index))).2

/-- The configurations of the lifted run. -/
noncomputable def wpf6LiftedStates (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (startRequestId : Nat)
    (dexRun : InfiniteRun (Dex.Config σ) (Dex.Action σ))
    (isDexRun : (Dex.system program.failureFreeFlow).IsRun dexRun) :
    (index : Nat) → { config : Config σ // wpf6Follows program (dexRun.states index) config }
  | 0 => ⟨Config.initial σ, invariant_initial program, wpf6_noFailure_initial program, by
      rw [wpf6_abstractConfig_initial, isDexRun.1]
      rfl⟩
  | index + 1 => wpf6LiftOne program ignoresAttemptContext startRequestId dexRun isDexRun index
      (wpf6LiftedStates program ignoresAttemptContext startRequestId dexRun isDexRun index)

/-- The DexF run with no failure that follows a Dex run. -/
noncomputable def wpf6LiftedRun (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (startRequestId : Nat)
    (dexRun : InfiniteRun (Dex.Config σ) (Dex.Action σ))
    (isDexRun : (Dex.system program.failureFreeFlow).IsRun dexRun) :
    InfiniteRun (Config σ) (Action σ) :=
  { states := fun index =>
      (wpf6LiftedStates program ignoresAttemptContext startRequestId dexRun isDexRun index).1
    labels := fun index => liftAction startRequestId (dexRun.labels index) }

section LiftedRun

variable (program : Program σ) (ignoresAttemptContext : IgnoresAttemptContext program)
  (startRequestId : Nat) (dexRun : InfiniteRun (Dex.Config σ) (Dex.Action σ))
  (isDexRun : (Dex.system program.failureFreeFlow).IsRun dexRun)

/-- Every configuration of the lifted run follows the Dex configuration. -/
theorem wpf6_lifted_follows (index : Nat) :
    wpf6Follows program (dexRun.states index)
      ((wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun).states index) :=
  (wpf6LiftedStates program ignoresAttemptContext startRequestId dexRun isDexRun index).2

/-- Each lifted transition is a DexF step with the Dex events and no repeated
`fx`. -/
theorem wpf6_lifted_step (index : Nat) :
    ∃ events,
      step program
          ((wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun).states index)
          ((wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun).labels
            index) =
        some (events,
          (wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun).states
            (index + 1)) ∧
      (Dex.system program.failureFreeFlow).eventsAt dexRun index = events.map Obs.toDex ∧
      eraseStepEvents
          ((wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun).states index)
          events = events.map Obs.toDex :=
  wpf6_liftOne_spec program ignoresAttemptContext startRequestId dexRun isDexRun index _

/-- The lifted run is a DexF run. -/
theorem wpf6_lifted_isRun :
    (system program).IsRun (wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun
      isDexRun) := by
  refine ⟨rfl, fun index => ?_⟩
  obtain ⟨events, isStep, _, _⟩ :=
    wpf6_lifted_step program ignoresAttemptContext startRequestId dexRun isDexRun index
  exact ⟨events, isStep⟩

/-- The events of a lifted transition are the Dex events with ghost indexes. -/
theorem wpf6_lifted_eventsAt (index : Nat) :
    (Dex.system program.failureFreeFlow).eventsAt dexRun index =
      ((system program).eventsAt
        (wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun) index).map
        Obs.toDex := by
  obtain ⟨events, isStep, isEvents, _⟩ :=
    wpf6_lifted_step program ignoresAttemptContext startRequestId dexRun isDexRun index
  rw [(system program).eventsAt_of_step _ index events _ isStep]
  exact isEvents

end LiftedRun

/-! ## Labels of the lifted run -/

/-- A lifted label attempts the lifted request of the Dex label. -/
theorem wpf6_requestOf_liftAction (flow : Dex.FlowDef σ) (startRequestId : Nat)
    (dexAction : Dex.Action σ) :
    (liftAction startRequestId dexAction).requestOf =
      ((Dex.system flow).requestOf dexAction).map (liftRequest startRequestId) := by
  cases dexAction <;> rfl

/-- A lifted label is a progress step exactly when the Dex label is. -/
theorem wpf6_isProgress_liftAction (flow : Dex.FlowDef σ) (startRequestId : Nat)
    (dexAction : Dex.Action σ) :
    (liftAction startRequestId dexAction).isProgress = (Dex.system flow).isProgress dexAction := by
  cases dexAction <;> rfl

/-- A lifted label is no failure that FF counts. -/
theorem wpf6_isCountedFailure_liftAction (startRequestId : Nat) (dexAction : Dex.Action σ) :
    Fairness.Action.isCountedFailure (liftAction startRequestId dexAction) = false := by
  cases dexAction <;> rfl

/-- A lifted label never exhausts. -/
theorem wpf6_isExhausting_liftAction (startRequestId : Nat) (dexAction : Dex.Action σ) :
    (liftAction startRequestId dexAction).isExhausting = false := by
  cases dexAction <;> rfl

/-- A lifted label is no non-retryable failure. -/
theorem wpf6_isNonRetryable_liftAction (startRequestId : Nat) (dexAction : Dex.Action σ) :
    (liftAction startRequestId dexAction).isNonRetryableFailure = false := by
  cases dexAction <;> rfl

/-- A lifted label is never a crash. -/
theorem wpf6_liftAction_ne_crash (startRequestId : Nat) (dexAction : Dex.Action σ) :
    liftAction startRequestId dexAction ≠ .crash := by
  cases dexAction <;> nofun

/-- [FLOW-START] needs no run. -/
theorem wpf6bw_step_startFlow_run (program : Program σ) (config : Config σ) (requestId : Nat)
    (stepType : σ.StepType) (input : σ.Val) (events : List (Obs σ)) (next : Config σ)
    (isStep :
      step program config (.request (.startFlow requestId stepType input)) = some (events, next)) :
    config.run = none := by
  simp only [step] at isStep
  cases hasRun : config.run with
  | some run => simp [hasRun] at isStep
  | none => rfl

/-! ## The word -/

/-- For a word without retries, `w↓` is the word with request IDs erased. -/
theorem wpf6_erasedWord_eq (environment : Dex.Environment σ) (word : Sequence (Request σ))
    (isAdmissible : IsAdmissibleOutcomeWord environment word)
    (erasedWord : Sequence (Dex.Request σ)) (isErasure : IsRetryErasure word erasedWord)
    (hasNoRetry : HasNoRetry word) : erasedWord = word.map Request.toDex := by
  have isChunk : retryErasureChunk word =
      fun position => ((word.map Request.toDex) position).toList := by
    funext position
    unfold retryErasureChunk
    show _ = ((word position).map Request.toDex).toList
    cases isPresent : word position with
    | none => rfl
    | some request =>
      simp only [Option.map_some, Option.toList_some]
      cases position with
      | zero =>
        cases word 0 <;> rfl
      | succ position =>
        cases isFirst : word 0 with
        | none => rfl
        | some first =>
          simp only [hasNoRetry position first request isFirst isPresent]
          rfl
  unfold IsRetryErasure at isErasure
  rw [isChunk] at isErasure
  refine wpf6_isConcatenation_unique _ erasedWord _ isErasure
    (wpf6_isConcatenation_pointwise _ (fun position isNone => ?_))
  show (word (position + 1)).map Request.toDex = none
  have isWordNone : word position = none := by
    cases isPresent : word position with
    | none => rfl
    | some request =>
      have isMapped : (word position).map Request.toDex = none := isNone
      rw [isPresent] at isMapped
      cases isMapped
  rw [isAdmissible.2.2 position isWordNone]
  rfl

/-- For an admissible word without retries, lifting the requests of `w↓` with
the start's request ID gives the word back. -/
theorem wpf6_liftWord_eq (environment : Dex.Environment σ) (word : Sequence (Request σ))
    (isAdmissible : IsAdmissibleOutcomeWord environment word) (hasNoRetry : HasNoRetry word)
    (startRequestId : Nat) (input : σ.Val)
    (isStart : word 0 = some (.startFlow startRequestId environment.entryStep input)) :
    (word.map Request.toDex).map (liftRequest startRequestId) = word := by
  funext position
  show ((word position).map Request.toDex).map (liftRequest startRequestId) = word position
  cases isPresent : word position with
  | none => rfl
  | some request =>
    simp only [Option.map_some, Option.some.injEq]
    cases position with
    | zero =>
      rw [isStart] at isPresent
      cases isPresent
      rfl
    | succ position =>
      rcases isAdmissible.2.1 position request isPresent with
        ⟨first, isFirst, isRetry⟩ | ⟨chan, value, isPublish, _⟩
      · rw [hasNoRetry position first request isFirst isPresent] at isRetry
        cases isRetry
      · rw [isPublish]
        rfl

/-! ## The trace -/

/-- The position of the first event of transition `index` in the trace. -/
def wpf6bwEventCount (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (index : Nat) : Nat :=
  (concatUpTo ((system program).eventsAt run) index).length

/-- The event at offset `offset` of transition `index` is the trace's event at
its position. -/
theorem wpf6bw_trace_at (program : Program σ) (run : InfiniteRun (Config σ) (Action σ))
    (failureTrace : Sequence (Obs σ))
    (isTrace : IsConcatenation ((system program).eventsAt run) failureTrace)
    (index offset : Nat) (event : Obs σ)
    (isEvent : ((system program).eventsAt run index)[offset]? = some event) :
    failureTrace (wpf6bwEventCount program run index + offset) = some event := by
  have isInside : offset < ((system program).eventsAt run index).length := by
    rcases List.getElem?_eq_some_iff.mp isEvent with ⟨isInside, _⟩
    exact isInside
  rw [wpf6_isConcatenation_getElem _ failureTrace isTrace (index + 1)]
  · show (concatUpTo _ index ++ _)[_]? = _
    rw [wpf6bwEventCount, List.getElem?_append_right (Nat.le_add_right _ _),
      Nat.add_sub_cancel_left]
    exact isEvent
  · show _ < (concatUpTo _ index ++ _).length
    rw [List.length_append]
    unfold wpf6bwEventCount
    omega

/-- Every position of a finite concatenation lies in one chunk. -/
theorem wpf6bw_exists_chunk {α : Type} (chunks : Nat → List α) :
    ∀ count position, position < (concatUpTo chunks count).length →
      ∃ index offset, index < count ∧ offset < (chunks index).length ∧
        position = (concatUpTo chunks index).length + offset := by
  intro count
  induction count with
  | zero => intro position isInside; simp [concatUpTo] at isInside
  | succ count inductionHypothesis =>
    intro position isInside
    by_cases isEarlier : position < (concatUpTo chunks count).length
    · obtain ⟨index, offset, isBefore, isOffset, isSum⟩ := inductionHypothesis position isEarlier
      exact ⟨index, offset, by omega, isOffset, isSum⟩
    · simp only [concatUpTo, List.length_append] at isInside
      exact ⟨count, position - (concatUpTo chunks count).length, by omega, by omega, by omega⟩

/-- A `filterMap` that equals a `map` keeps every element. -/
theorem wpf6bw_filterMap_eq_map {α β : Type} (transform : α → Option β) (image : α → β) :
    ∀ elements : List α, elements.filterMap transform = elements.map image →
      ∀ element, element ∈ elements → transform element ≠ none := by
  intro elements
  induction elements with
  | nil => intro _ element isMember; cases isMember
  | cons head rest inductionHypothesis =>
    intro isEqual element isMember
    cases isHead : transform head with
    | none =>
      rw [List.filterMap_cons, isHead, List.map_cons] at isEqual
      have isLength := congrArg List.length isEqual
      simp only [List.length_cons, List.length_map] at isLength
      have isShort := List.length_filterMap_le transform rest
      omega
    | some value =>
      rw [List.filterMap_cons, isHead, List.map_cons, List.cons.injEq] at isEqual
      rcases List.mem_cons.mp isMember with isFirst | isLater
      · rw [isFirst, isHead]
        nofun
      · exact inductionHypothesis isEqual.2 element isLater

/-- An `fx` of a step whose erasure drops nothing is a first occurrence. -/
theorem wpf6bw_fx_isFirst (config : Config σ) (events : List (Obs σ))
    (isErased : eraseStepEvents config events = events.map Obs.toDex) (stepId : Dex.StepExecId σ)
    (method : Dex.Method) (effect : σ.Fx) (effectIndex : Nat)
    (isMember : Obs.fx stepId method effect effectIndex ∈ events) :
    ¬ effectIndex ≤ config.ghost.emittedCountOf (stepId, method) := by
  intro isAtMost
  have isKept := wpf6bw_filterMap_eq_map _ Obs.toDex events isErased _ isMember
  simp [isAtMost] at isKept

/-- No `fx` of the lifted run is repeated. -/
theorem wpf6bw_not_isRepeatedAt (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (startRequestId : Nat)
    (dexRun : InfiniteRun (Dex.Config σ) (Dex.Action σ))
    (isDexRun : (Dex.system program.failureFreeFlow).IsRun dexRun)
    (failureTrace : Sequence (Obs σ))
    (isTrace : IsConcatenation
      ((system program).eventsAt
        (wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun))
      failureTrace)
    (position : Nat) : ¬ IsRepeatedAt failureTrace position := by
  intro isRepeated
  obtain ⟨earlier, stepId, method, firstEffect, effect, effectIndex, isEarlier, isFirst, isAt⟩ :=
    isRepeated
  have liftedRun := wpf6_lifted_isRun program ignoresAttemptContext startRequestId dexRun isDexRun
  let run := wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun
  have isInTrace : ∃ count, position < (concatUpTo ((system program).eventsAt run) count).length := by
    cases isTrace position with
    | inl found =>
      obtain ⟨count, isInside, _⟩ := found
      exact ⟨count, isInside⟩
    | inr missing => rw [missing.2] at isAt; cases isAt
  obtain ⟨count, isInside⟩ := isInTrace
  obtain ⟨index, offset, _, isOffset, isSum⟩ := wpf6bw_exists_chunk _ count position isInside
  have isEventAt : ((system program).eventsAt run index)[offset]? =
      some (.fx stepId method effect effectIndex) := by
    have isElement := List.getElem?_eq_getElem isOffset
    have isTraceAt := wpf6bw_trace_at program run failureTrace isTrace index offset _ isElement
    rw [← wpf6bwEventCount] at isSum
    rw [← isSum, isAt] at isTraceAt
    rw [isElement, isTraceAt]
  have isMember : Obs.fx stepId method effect effectIndex ∈ (system program).eventsAt run index :=
    List.mem_iff_getElem?.mpr ⟨offset, isEventAt⟩
  obtain ⟨events, isStep, _, isErased⟩ :=
    wpf6_lifted_step program ignoresAttemptContext startRequestId dexRun isDexRun index
  have isEvents := (system program).eventsAt_of_step run index events _ isStep
  rw [isEvents] at isMember isEventAt
  have isAbove := wpf6bw_fx_isFirst _ events isErased stepId method effect effectIndex isMember
  apply isAbove
  by_cases isBefore : earlier < wpf6bwEventCount program run index
  · have history := effectHistory_of_exec program ignoresAttemptContext
      ((List.range index).map run.labels) _ _ (by
        have isRange := (system program).exec_range run liftedRun.2 0 index
        simp only [Nat.zero_add] at isRange
        rw [← liftedRun.1]
        exact isRange)
    have isEarlierMember : Obs.fx stepId method firstEffect effectIndex ∈
        concatUpTo ((system program).eventsAt run) index := by
      rw [wpf6_isConcatenation_getElem _ failureTrace isTrace index earlier isBefore] at isFirst
      exact List.mem_iff_getElem?.mpr ⟨earlier, isFirst⟩
    exact (history.1 stepId method firstEffect effectIndex isEarlierMember).2.1
  · exfalso
    rw [← wpf6bwEventCount] at isSum
    obtain ⟨earlierOffset, isEarlierSum⟩ := Nat.exists_eq_add_of_le (Nat.le_of_not_lt isBefore)
    have isEarlierOffset : earlierOffset < offset := by omega
    rw [isEvents] at isOffset
    rcases wpf6_events_step program _ _ _ _ isStep with isShort | noEffect
    · omega
    · exact noEffect stepId method effect effectIndex isMember


/-! ## What the lifted run carries over -/

/-- A property implied by one that eventually fails also eventually fails,
read backwards: if `weaker` eventually fails, so does `stronger`. -/
theorem wpf6_eventuallyFails_mono {weaker stronger : Nat → Prop}
    (implies : ∀ index, stronger index → weaker index) (eventually : EventuallyFails weaker) :
    EventuallyFails stronger := by
  intro start holdsStart
  obtain ⟨later, isLater, isFailing⟩ := eventually start (implies start holdsStart)
  exact ⟨later, isLater, fun holds => isFailing (implies later holds)⟩

section Transfer

variable (program : Program σ) (ignoresAttemptContext : IgnoresAttemptContext program)
  (startRequestId : Nat) (dexRun : InfiniteRun (Dex.Config σ) (Dex.Action σ))
  (isDexRun : (Dex.system program.failureFreeFlow).IsRun dexRun)

/-- α maps the run of a lifted configuration to the run of the Dex
configuration. -/
theorem wpf6_lifted_run_abstract (index : Nat) (flowRun : Run σ)
    (hasRun : ((wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun).states
      index).run = some flowRun) :
    (dexRun.states index).run = some (flowRun.abstract
      ((wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun).states
        index).ghost) := by
  rw [← (wpf6_lifted_follows program ignoresAttemptContext startRequestId dexRun isDexRun
    index).2.2]
  show Option.map _ _ = _
  rw [hasRun]
  rfl

/-- **Fairness.** The lifted run of a fair Dex run is fair. -/
theorem wpf6_lifted_isFair (isDexFair : Dex.IsFair dexRun) :
    IsFair (wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun) := by
  have follows := wpf6_lifted_follows program ignoresAttemptContext startRequestId dexRun isDexRun
  obtain ⟨timeDiverges, timersFire, stepsStart, workersSettle, resultsCommit, _⟩ := isDexFair
  refine ⟨?_, ?_, ?_, ?_, ?_, trivial, ?_, ?_, ?_⟩
  · intro bound
    obtain ⟨index, isAbove⟩ := timeDiverges bound
    refine ⟨index, ?_⟩
    rw [← (follows index).2.2] at isAbove
    exact isAbove
  · intro timer
    cases timer with
    | wait timer =>
      apply wpf6_eventuallyFails_mono _ (timersFire timer)
      intro index isDue
      obtain ⟨flowRun, fireTime, hasRun, isArmed, isDueNow⟩ := isDue
      refine ⟨flowRun.abstract _, fireTime,
        wpf6_lifted_run_abstract program ignoresAttemptContext startRequestId dexRun isDexRun index
          flowRun hasRun, isArmed, ?_⟩
      rw [← (follows index).2.2]
      exact isDueNow
    | retry timer =>
      intro start isDue
      obtain ⟨flowRun, fireTime, hasRun, isArmed, _⟩ := isDue
      have isEmpty := (follows start).2.1.noRetryTimers flowRun hasRun
      simp [Run.fireTimeOf?, isEmpty, AssocList.get?] at isArmed
  · intro stepId method
    apply wpf6_eventuallyFails_mono _ (stepsStart stepId method)
    intro index isStartable
    obtain ⟨_, flowRun, record, attempt, hasRun, isFound, isPending, _, _⟩ := isStartable
    obtain ⟨dexFlowRun, dexRecord, hasDexRun, isDexFound, isDexPending⟩ :=
      wpf6_abstract_pending_of_noFailure program _ (follows index).1 (follows index).2.1 flowRun
        stepId record method hasRun isFound isPending
    rw [(follows index).2.2] at hasDexRun
    exact ⟨dexFlowRun, dexRecord, hasDexRun, isDexFound, isDexPending⟩
  · intro taskId
    apply wpf6_eventuallyFails_mono _ (workersSettle taskId)
    intro index isUnsettled
    obtain ⟨task, isFound, isNotReturned⟩ := isUnsettled
    obtain ⟨dexTask, isDexFound, isPhase⟩ := wpf6_abstract_task_of_noFailure program _
      (follows index).1 (follows index).2.1 taskId task isFound
    rw [(follows index).2.2] at isDexFound
    refine ⟨dexTask, isDexFound, fun response isReturned => isNotReturned response ?_⟩
    rw [← isPhase]
    exact isReturned
  · intro taskId
    apply wpf6_eventuallyFails_mono _ (resultsCommit taskId)
    intro index isReturned
    obtain ⟨task, response, isFound, isTaskReturned⟩ := isReturned
    obtain ⟨dexTask, isDexFound, isPhase⟩ := wpf6_abstract_task_of_noFailure program _
      (follows index).1 (follows index).2.1 taskId task isFound
    rw [(follows index).2.2] at isDexFound
    refine ⟨dexTask, response, isDexFound, ?_⟩
    rw [isPhase]
    exact isTaskReturned
  · exact ⟨0, fun index _ => wpf6_isCountedFailure_liftAction startRequestId (dexRun.labels index)⟩
  · intro index isCrash
    exact absurd isCrash (wpf6_liftAction_ne_crash startRequestId (dexRun.labels index))
  · intro interrupt start isIn
    rw [(follows start).2.1.noInterrupts] at isIn
    cases isIn

/-- **The hypotheses.** The lifted run satisfies H1, H4, D6, and HP. -/
theorem wpf6_lifted_hypotheses :
    IsHypothesisRun (wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun) := by
  have follows := wpf6_lifted_follows program ignoresAttemptContext startRequestId dexRun isDexRun
  refine ⟨fun index => wpf6_isExhausting_liftAction _ _,
    fun index => wpf6_isNonRetryable_liftAction _ _, ?_, ?_⟩
  · intro index count isWaitTimerFired flowRun timer fireTime _ hasRun isFired isArmed
    exact wpf6_firedTimers_due program _ (follows index).1 flowRun hasRun count timer fireTime
      isFired isArmed
  · intro index requestId stepType input isRequest hasRun
    exfalso
    obtain ⟨events, isStep, _, _⟩ :=
      wpf6_lifted_step program ignoresAttemptContext startRequestId dexRun isDexRun index
    cases isLabel : dexRun.labels index with
    | request dexRequest =>
      cases dexRequest with
      | startFlow dexStepType dexInput =>
        have isLifted : (wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun
            isDexRun).labels index = .request (.startFlow startRequestId dexStepType dexInput) := by
          show liftAction _ _ = _
          rw [isLabel]
          rfl
        rw [isLifted] at isStep
        have hadNoRun := wpf6bw_step_startFlow_run program _ _ _ _ events _ isStep
        rw [hadNoRun] at hasRun
        cases hasRun
      | publish chan value =>
        simp [wpf6LiftedRun, isLabel, liftAction, liftRequest, Action.requestOf] at isRequest
    | _ => simp [wpf6LiftedRun, isLabel, liftAction, Action.requestOf] at isRequest

/-- **The word.** The lifted run attempts the lifted requests of `w↓`. -/
theorem wpf6_lifted_isRunFor (erasedWord : Sequence (Dex.Request σ))
    (isDexRunFor : (Dex.system program.failureFreeFlow).IsRunFor dexRun erasedWord) :
    (system program).IsRunFor
      (wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun)
      (erasedWord.map (liftRequest startRequestId)) := by
  have isMapped := isConcatenation_map (liftRequest startRequestId) _ erasedWord isDexRunFor
  have isChunks : (system program).requestsAt
      (wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun) =
      fun index => ((Dex.system program.failureFreeFlow).requestsAt dexRun index).map
        (liftRequest startRequestId) := by
    funext index
    show ((liftAction startRequestId (dexRun.labels index)).requestOf).toList = _
    rw [wpf6_requestOf_liftAction program.failureFreeFlow]
    show _ = (((Dex.system program.failureFreeFlow).requestOf (dexRun.labels index)).toList).map _
    cases (Dex.system program.failureFreeFlow).requestOf (dexRun.labels index) <;> rfl
  show IsConcatenation ((system program).requestsAt _) _
  rw [isChunks]
  exact isMapped

/-- **The trace.** The Dex trace is `erase(t)` of the lifted trace `t`, which
repeats nothing. -/
theorem wpf6_lifted_trace (trace : Sequence (Dex.Obs σ))
    (isDexTrace : IsConcatenation
      (fun index => ((Dex.system program.failureFreeFlow).eventsAt dexRun index).filterMap some)
      trace)
    (failureTrace : Sequence (Obs σ))
    (isTrace : IsConcatenation (fun index => ((system program).eventsAt
      (wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun)
        index).filterMap some) failureTrace) :
    IsRepeatErasure failureTrace trace := by
  simp only [List.filterMap_some] at isDexTrace isTrace
  have isMapped := isConcatenation_map Obs.toDex _ failureTrace isTrace
  have isTraceEq : trace = failureTrace.map Obs.toDex := by
    apply wpf6_isConcatenation_unique _ trace _ _ isMapped
    have isEvents : (fun index => (Dex.system program.failureFreeFlow).eventsAt dexRun index) =
        fun index => ((system program).eventsAt
          (wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun)
            index).map Obs.toDex :=
      funext (wpf6_lifted_eventsAt program ignoresAttemptContext startRequestId dexRun isDexRun)
    rw [← isEvents]
    exact isDexTrace
  refine ⟨fun position => ((failureTrace position).map Obs.toDex).toList, ?_,
    fun position => ⟨fun isRepeated => absurd isRepeated
      (wpf6bw_not_isRepeatedAt program ignoresAttemptContext startRequestId dexRun isDexRun
        failureTrace isTrace position), fun _ => rfl⟩⟩
  rw [isTraceEq]
  exact wpf6_isConcatenation_pointwise _ (wpf6_isConcatenation_none_succ _ _ isMapped)

/-- **The marker.** The lifted run has the marker of the Dex run. -/
theorem wpf6_lifted_marker (marker : Marker)
    (hasMarker : (Dex.system program.failureFreeFlow).HasMarker dexRun marker) :
    (system program).HasMarker
      (wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun) marker := by
  have follows := wpf6_lifted_follows program ignoresAttemptContext startRequestId dexRun isDexRun
  have isProgressSame : ∀ index, (system program).isProgress
      ((wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun).labels
        index) = (Dex.system program.failureFreeFlow).isProgress (dexRun.labels index) :=
    fun index => wpf6_isProgress_liftAction _ _ _
  have isClosedSame : ∀ index, (system program).isClosed
      ((wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun).states
        index) = (Dex.system program.failureFreeFlow).isClosed (dexRun.states index) := by
    intro index
    rw [← (follows index).2.2]
    exact (wpf6_isClosed_abstract program _).symm
  have isFiniteIff : (system program).HasFinitelyManyProgressSteps
      (wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun) ↔
      (Dex.system program.failureFreeFlow).HasFinitelyManyProgressSteps dexRun := by
    unfold TransitionSystem.HasFinitelyManyProgressSteps
    simp only [isProgressSame]
  cases marker with
  | terminated =>
    obtain ⟨isFinite, index, isClosed⟩ := hasMarker
    exact ⟨isFiniteIff.mpr isFinite, index, by rw [isClosedSame]; exact isClosed⟩
  | deadlocked =>
    obtain ⟨isFinite, isOpen⟩ := hasMarker
    exact ⟨isFiniteIff.mpr isFinite, fun index => by rw [isClosedSame]; exact isOpen index⟩
  | divergent => exact fun isFinite => hasMarker (isFiniteIff.mp isFinite)

end Transfer

/-! ## The backward inclusion -/

/-- **WP-F6, backward.** For an admissible outcome word `w` without retries,
every fair maximal Dex trace for `w↓` is `erase(t)` of a fair maximal DexF
trace `t` for `w` with the same marker. -/
theorem wpf6_liveness_backward (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (environment : Dex.Environment σ)
    (word : Sequence (Request σ)) (isAdmissible : IsAdmissibleOutcomeWord environment word)
    (erasedWord : Sequence (Dex.Request σ)) (isErasure : IsRetryErasure word erasedWord)
    (trace : Sequence (Dex.Obs σ)) (marker : Marker) (hasNoRetry : HasNoRetry word)
    (isDexTrace : (Dex.system program.failureFreeFlow).IsFairMaximalTrace Dex.IsFair some
      erasedWord trace marker) :
    ∃ failureTrace, IsFailureFairMaximalTrace program word failureTrace marker ∧
      IsRepeatErasure failureTrace trace := by
  obtain ⟨dexRun, isDexRun, isDexRunFor, _, isDexFair, isDexEvents, hasMarker⟩ := isDexTrace
  obtain ⟨startRequestId, input, _, isStart⟩ := isAdmissible.1
  obtain ⟨failureTrace, isTrace⟩ := wpf6_exists_isConcatenation (fun index =>
    ((system program).eventsAt
      (wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun)
        index).filterMap some)
  have isWord : erasedWord.map (liftRequest startRequestId) = word := by
    rw [wpf6_erasedWord_eq environment word isAdmissible erasedWord isErasure hasNoRetry]
    exact wpf6_liftWord_eq environment word isAdmissible hasNoRetry startRequestId input isStart
  have isRunFor : (system program).IsRunFor
      (wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun) word := by
    rw [← isWord]
    exact wpf6_lifted_isRunFor program ignoresAttemptContext startRequestId dexRun isDexRun
      erasedWord isDexRunFor
  refine ⟨failureTrace,
    ⟨wpf6LiftedRun program ignoresAttemptContext startRequestId dexRun isDexRun,
      wpf6_lifted_isRun program ignoresAttemptContext startRequestId dexRun isDexRun, isRunFor,
      (system program).requestsArrive_of_isRunFor _ word isRunFor,
      ⟨wpf6_lifted_isFair program ignoresAttemptContext startRequestId dexRun isDexRun isDexFair,
        wpf6_lifted_hypotheses program ignoresAttemptContext startRequestId dexRun isDexRun⟩,
      isTrace,
      wpf6_lifted_marker program ignoresAttemptContext startRequestId dexRun isDexRun marker
        hasMarker⟩,
    wpf6_lifted_trace program ignoresAttemptContext startRequestId dexRun isDexRun trace
      isDexEvents failureTrace isTrace⟩

end DexExpressiveness.DexF
