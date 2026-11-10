import DexExpressiveness.Failure.Composite.WPF5Steps

/-!
# WP-F5: safety, forward, and the repetition form

`safety_failure_forward` composes `step_simulated`, `invariant_step`, and
`effectHistory_of_exec` along a DexF run, as `FiniteTraces.lean` does for the
expressiveness proof:

- each step maps to the Dex actions of the transition map, with the step's
  events less the repeated `fx` (`eraseStepEvents`);
- the effect history turns the per-step erasure into `eraseRepeats` of the
  whole trace;
- the requests of the Dex run are `w↓`: a StartFlow after the start is a
  [START-RETRY] with the start request ID, which maps to a stutter.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## Erasure of a whole trace -/

theorem wpf5_eraseRepeatsAfter_append (earlier first second : List (Obs σ)) :
    eraseRepeatsAfter earlier (first ++ second) =
      eraseRepeatsAfter earlier first ++ eraseRepeatsAfter (earlier ++ first) second := by
  induction first generalizing earlier with
  | nil => simp [eraseRepeatsAfter]
  | cons event laterEvents inductionHypothesis =>
    simp only [List.cons_append, eraseRepeatsAfter, List.append_assoc]
    rw [inductionHypothesis]
    simp

theorem wpf5_isRepeatAfter_fx (earlier : List (Obs σ)) (step : Dex.StepExecId σ)
    (method : Dex.Method) (effect : σ.Fx) (index : Nat) :
    (Obs.fx step method effect index).isRepeatAfter earlier = true ↔
      ∃ otherEffect, Obs.fx step method otherEffect index ∈ earlier := by
  simp only [Obs.isRepeatAfter, List.any_eq_true]
  constructor
  · rintro ⟨previous, isMember, isSame⟩
    cases previous with
    | fx previousStep previousMethod previousEffect previousIndex =>
      simp only [Bool.and_eq_true, decide_eq_true_eq] at isSame
      obtain ⟨⟨isStep, isMethod⟩, isIndex⟩ := isSame
      subst isStep isMethod isIndex
      exact ⟨previousEffect, isMember⟩
    | _ => simp at isSame
  · rintro ⟨otherEffect, isMember⟩
    exact ⟨_, isMember, by simp⟩

theorem wpf5_eraseRepeatsAfter_notFx (events : List (Obs σ))
    (isNotFx : ∀ event, event ∈ events → WPF5IsNotFx event) (earlier : List (Obs σ)) :
    eraseRepeatsAfter earlier events = events.map Obs.toDex := by
  induction events generalizing earlier with
  | nil => rfl
  | cons event laterEvents inductionHypothesis =>
    simp only [eraseRepeatsAfter, List.map_cons]
    rw [inductionHypothesis (fun other isMember => isNotFx other (List.mem_cons_of_mem _ isMember))]
    have eventNotFx := isNotFx event (List.mem_cons_self ..)
    cases event with
    | fx => exact absurd eventNotFx id
    | _ => rfl

theorem wpf5_eraseStepEvents_notFx (config : Config σ) (events : List (Obs σ))
    (isNotFx : ∀ event, event ∈ events → WPF5IsNotFx event) :
    eraseStepEvents config events = events.map Obs.toDex := by
  unfold eraseStepEvents
  induction events with
  | nil => rfl
  | cons event laterEvents inductionHypothesis =>
    rw [List.filterMap_cons, List.map_cons,
      inductionHypothesis (fun other isMember => isNotFx other (List.mem_cons_of_mem _ isMember))]
    have eventNotFx := isNotFx event (List.mem_cons_self ..)
    cases event with
    | fx => exact absurd eventNotFx id
    | _ => rfl

/-- The per-step erasure is `erase` of the trace, given the effect histories
before and after the step. -/
theorem wpf5_eraseRepeatsAfter_step (program : Program σ) (config : Config σ)
    (action : Action σ) (earlier events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next))
    (historyBefore : HasEffectHistory program config earlier)
    (historyAfter : HasEffectHistory program next (earlier ++ events)) :
    eraseRepeatsAfter earlier events = eraseStepEvents config events := by
  rcases wpf5_step_events program config action events next isStep with isNotFx |
    ⟨stepId, method, effect, index, isEvents⟩
  · rw [wpf5_eraseRepeatsAfter_notFx events isNotFx, wpf5_eraseStepEvents_notFx config events isNotFx]
  · subst isEvents
    have isPositive := (historyAfter.1 stepId method effect index (by simp)).1
    simp only [eraseRepeatsAfter, eraseStepEvents, List.filterMap_cons, List.filterMap_nil,
      List.append_nil]
    by_cases isRepeated : (Obs.fx stepId method effect index).isRepeatAfter earlier = true
    · obtain ⟨otherEffect, isMember⟩ := (wpf5_isRepeatAfter_fx earlier stepId method effect index).mp
        isRepeated
      have isAtMost := (historyBefore.1 stepId method otherEffect index isMember).2.1
      simp [isRepeated, isAtMost]
    · have isAbove : ¬ index ≤ config.ghost.emittedCountOf (stepId, method) := by
        intro isAtMost
        obtain ⟨otherEffect, isMember⟩ := historyBefore.2 stepId method index isPositive isAtMost
        exact isRepeated ((wpf5_isRepeatAfter_fx earlier stepId method effect index).mpr
          ⟨otherEffect, isMember⟩)
      simp [isRepeated, isAbove, Obs.toDex]

/-! ## Requests -/

/-- The request a label gives the Dex run: its request, except that a
[START-RETRY] maps to a stutter. -/
def wpf5NonRetryRequestOf : Action σ → Option (Request σ)
  | .startRetry _ _ _ => none
  | action => action.requestOf

theorem wpf5_nonRetryRequestOf_of_none (action : Action σ) (isNone : action.requestOf = none) :
    wpf5NonRetryRequestOf action = none := by
  cases action <;> first | rfl | exact isNone

theorem wpf5_nonRetryRequestOf_of_not_startRetry (action : Action σ)
    (isNotRetry : ∀ requestId stepType input, action ≠ .startRetry requestId stepType input) :
    wpf5NonRetryRequestOf action = action.requestOf := by
  cases action with
  | startRetry requestId stepType input => exact absurd rfl (isNotRetry requestId stepType input)
  | _ => rfl

theorem wpf5_onVirtualTask_requests (flow : Dex.FlowDef σ) (config : Config σ) (taskId : Nat)
    (action : Nat → Dex.Action σ) (isNotRequest : ∀ virtualTaskId request,
      action virtualTaskId ≠ .request request) :
    (config.onVirtualTask taskId action).filterMap (Dex.system flow).requestOf = [] := by
  unfold Config.onVirtualTask
  split
  · rename_i virtualTaskId _
    simp only [List.filterMap_cons, List.filterMap_nil]
    cases isAction : action virtualTaskId with
    | request request => exact absurd isAction (isNotRequest virtualTaskId request)
    | _ => rfl
  · rfl

/-- The requests of the Dex actions of one DexF step. -/
theorem wpf5_dexActionsOf_requests (program : Program σ) (config : Config σ)
    (action : Action σ) :
    (dexActionsOf program config action).filterMap
        (Dex.system program.failureFreeFlow).requestOf =
      ((wpf5NonRetryRequestOf action).map Request.toDex).toList := by
  cases action with
  | stepComplete taskId =>
    simp only [dexActionsOf, List.filterMap_append, wpf5NonRetryRequestOf, Action.requestOf,
      Option.map_none, Option.toList_none]
    rw [wpf5_onVirtualTask_requests _ _ _ _ (by intro _ _ isEqual; cases isEqual)]
    split
    · simp only [abandonedVirtualTasks, List.nil_append, List.filterMap_map]
      apply List.filterMap_eq_nil_iff.mpr
      intro _ _
      rfl
    · rfl
  | stepStart stepId isSnapshotReused =>
    cases isSnapshotReused <;> rfl
  | timerFire count isWaitTimerFired =>
    cases isWaitTimerFired <;> rfl
  | workerRun taskId isFirst =>
    cases isFirst
    · rfl
    · exact wpf5_onVirtualTask_requests _ _ _ _ (by intro _ _ isEqual; cases isEqual)
  | workerEffect taskId isFirst =>
    cases isFirst
    · rfl
    · exact wpf5_onVirtualTask_requests _ _ _ _ (by intro _ _ isEqual; cases isEqual)
  | workerReturn taskId isFirst =>
    cases isFirst
    · rfl
    · exact wpf5_onVirtualTask_requests _ _ _ _ (by intro _ _ isEqual; cases isEqual)
  | workerAbandon taskId isLast =>
    cases isLast
    · rfl
    · exact wpf5_onVirtualTask_requests _ _ _ _ (by intro _ _ isEqual; cases isEqual)
  | resultStale taskId isLast =>
    cases isLast
    · rfl
    · exact wpf5_onVirtualTask_requests _ _ _ _ (by intro _ _ isEqual; cases isEqual)
  | _ => rfl

/-- After the start, `w↓` keeps exactly the requests that are not
[START-RETRY]. -/
theorem wpf5_requests_afterStart (program : Program σ) (requestId : Nat)
    (stepType : σ.StepType) (input : σ.Val) :
    ∀ (labels : List (Action σ)) (config : Config σ) (events : List (Obs σ)) (final : Config σ)
      (run : Run σ), config.run = some run → requestId = run.startRequestId →
      (system program).exec config labels = some (events, final) →
      ((labels.filterMap Action.requestOf).filter
          (fun request => !(Request.startFlow requestId stepType input).isRetryOf request)).map
        Request.toDex =
        (labels.filterMap wpf5NonRetryRequestOf).map Request.toDex := by
  intro labels
  induction labels with
  | nil => intros; rfl
  | cons label laterLabels inductionHypothesis =>
    intro config events final run hasRun isStartId isExec
    obtain ⟨stepEvents, next, laterEvents, isStep, isLater, _⟩ :=
      (system program).exec_cons config label laterLabels events final isExec
    have isNextId := wpf5_step_startRequestId program config label stepEvents next run hasRun isStep
    obtain ⟨nextRun, hasNextRun, isNextRunId⟩ : ∃ nextRun, next.run = some nextRun ∧
        nextRun.startRequestId = run.startRequestId := by
      cases isNext : next.run with
      | none => rw [isNext] at isNextId; cases isNextId
      | some nextRun =>
        rw [isNext] at isNextId
        exact ⟨nextRun, rfl, Option.some.inj isNextId⟩
    have laterEqual := inductionHypothesis next laterEvents final nextRun hasNextRun
      (by rw [isNextRunId]; exact isStartId) isLater
    by_cases isRetry : ∃ retryId retryStepType retryInput,
        label = .startRetry retryId retryStepType retryInput
    · obtain ⟨retryId, retryStepType, retryInput, isLabel⟩ := isRetry
      subst isLabel
      have isSame := (wpf5_startFlowRequest_isRetry program config
        (.startRetry retryId retryStepType retryInput) stepEvents next run hasRun
        isStep retryId retryStepType retryInput rfl).2
      have isRetryOfFirst : (Request.startFlow requestId stepType input).isRetryOf
          (.startFlow retryId retryStepType retryInput) = true := by
        simp [Request.isRetryOf, isStartId, isSame]
      simp only [List.filterMap_cons, Action.requestOf, wpf5NonRetryRequestOf, List.filter_cons,
        isRetryOfFirst, Bool.not_true, Bool.false_eq_true, ite_false]
      exact laterEqual
    · have isNonRetry := wpf5_nonRetryRequestOf_of_not_startRetry label
        (fun retryId retryStepType retryInput isLabel =>
          isRetry ⟨retryId, retryStepType, retryInput, isLabel⟩)
      cases isRequest : label.requestOf with
      | none =>
        simp only [List.filterMap_cons, isNonRetry, isRequest]
        exact laterEqual
      | some request =>
        cases request with
        | startFlow otherId otherStepType otherInput =>
          exact absurd (wpf5_startFlowRequest_isRetry program config label stepEvents next run hasRun
            isStep otherId otherStepType otherInput isRequest).1
            (fun isLabel => isRetry ⟨_, _, _, isLabel⟩)
        | publish chan value =>
          have isNotRetryOfFirst : (Request.startFlow requestId stepType input).isRetryOf
              (.publish chan value) = false := rfl
          simp only [List.filterMap_cons, isRequest, isNonRetry, List.filter_cons,
            isNotRetryOfFirst, Bool.not_false, ite_true, List.map_cons, laterEqual]

theorem wpf5_admissible_head (environment : Dex.Environment σ) (chan : σ.Chan) (value : σ.Val)
    (laterRequests : List (Request σ))
    (isAdmissible : IsAdmissibleOutcomePrefix environment (.publish chan value :: laterRequests)) :
    False := by
  rcases isAdmissible with isEmpty | ⟨_, _, _, isRequests, _⟩
  · cases isEmpty
  · cases isRequests

/-- Before the start, `w↓` of the requests of a run is the list of its
requests other than [START-RETRY]. -/
theorem wpf5_requests_beforeStart (program : Program σ) (environment : Dex.Environment σ) :
    ∀ (labels : List (Action σ)) (config : Config σ) (events : List (Obs σ)) (final : Config σ),
      config.run = none → (system program).exec config labels = some (events, final) →
      IsAdmissibleOutcomePrefix environment (labels.filterMap Action.requestOf) →
      eraseRetries (labels.filterMap Action.requestOf) =
        (labels.filterMap wpf5NonRetryRequestOf).map Request.toDex := by
  intro labels
  induction labels with
  | nil => intros; rfl
  | cons label laterLabels inductionHypothesis =>
    intro config events final hasNoRun isExec isAdmissible
    obtain ⟨stepEvents, next, laterEvents, isStep, isLater, _⟩ :=
      (system program).exec_cons config label laterLabels events final isExec
    cases isRequest : label.requestOf with
    | none =>
      have isNonRetry := wpf5_nonRetryRequestOf_of_none label isRequest
      have nextHasNoRun := wpf5_step_noRun program config label
        (by intro requestId stepType input isLabel; subst isLabel; cases isRequest)
        stepEvents next hasNoRun isStep
      simp only [List.filterMap_cons, isRequest, isNonRetry] at isAdmissible ⊢
      exact inductionHypothesis next laterEvents final nextHasNoRun isLater isAdmissible
    | some request =>
      cases request with
      | publish chan value =>
        simp only [List.filterMap_cons, isRequest] at isAdmissible
        exact (wpf5_admissible_head environment chan value _ isAdmissible).elim
      | startFlow requestId stepType input =>
        cases label with
        | request labelRequest =>
          simp only [Action.requestOf, Option.some.injEq] at isRequest
          subst isRequest
          have isNextId := wpf5_step_flowStart program config requestId stepType input stepEvents
            next isStep
          obtain ⟨nextRun, hasNextRun, isNextRunId⟩ : ∃ nextRun, next.run = some nextRun ∧
              nextRun.startRequestId = requestId := by
            cases isNext : next.run with
            | none => rw [isNext] at isNextId; cases isNextId
            | some nextRun =>
              rw [isNext] at isNextId
              exact ⟨nextRun, rfl, Option.some.inj isNextId⟩
          simp only [List.filterMap_cons, Action.requestOf, wpf5NonRetryRequestOf, eraseRetries,
            List.map_cons, Request.toDex]
          rw [wpf5_requests_afterStart program requestId stepType input laterLabels next laterEvents
            final nextRun hasNextRun isNextRunId.symm isLater]
        | startRetry =>
          simp [system, step, hasNoRun] at isStep
        | engineReject labelRequest =>
          simp only [Action.requestOf, Option.some.injEq] at isRequest
          subst isRequest
          simp [system, step] at isStep
        | _ => cases isRequest

/-- `w↓` of an admissible outcome-word prefix is an admissible Dex prefix. -/
theorem wpf5_isAdmissiblePrefix_eraseRetries (environment : Dex.Environment σ)
    (requests : List (Request σ)) (isAdmissible : IsAdmissibleOutcomePrefix environment requests) :
    environment.IsAdmissiblePrefix (eraseRetries requests) := by
  rcases isAdmissible with isEmpty | ⟨requestId, input, laterRequests, isRequests, isInput, isLater⟩
  · subst isEmpty
    exact .inl rfl
  · subst isRequests
    refine .inr ⟨input, _, rfl, isInput, ?_⟩
    intro request isMember
    simp only [List.mem_map, List.mem_filter, Bool.not_eq_true'] at isMember
    obtain ⟨original, ⟨isOriginal, isNotRetry⟩, isErased⟩ := isMember
    rcases isLater original isOriginal with isRetry | ⟨chan, value, isPublish, isExternal⟩
    · rw [isRetry] at isNotRetry
      cases isNotRetry
    · subst isPublish isErased
      exact ⟨chan, value, rfl, isExternal⟩

/-! ## The forward simulation along a run -/

/-- A DexF run from a reachable configuration that satisfies the invariants
maps to a Dex run from its abstraction, with the erased events and the
requests other than [START-RETRY]. -/
theorem wpf5_exec_forward (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) :
    ∀ (labels : List (Action σ)) (config : Config σ) (earlier events : List (Obs σ))
      (final : Config σ),
      Invariant program config →
      (∃ previousLabels,
        (system program).exec (system program).init previousLabels = some (earlier, config)) →
      HasNoExhaustion labels →
      (system program).exec config labels = some (events, final) →
      ∃ dexLabels,
        (Dex.system program.failureFreeFlow).exec (abstractConfig config) dexLabels =
          some (eraseRepeatsAfter earlier events, abstractConfig final) ∧
        dexLabels.filterMap (Dex.system program.failureFreeFlow).requestOf =
          (labels.filterMap wpf5NonRetryRequestOf).map Request.toDex := by
  intro labels
  induction labels with
  | nil =>
    intro config earlier events final _ _ _ isExec
    simp only [TransitionSystem.exec, Option.some.injEq, Prod.mk.injEq] at isExec
    obtain ⟨isEvents, isFinal⟩ := isExec
    subst isEvents isFinal
    exact ⟨[], rfl, rfl⟩
  | cons label laterLabels inductionHypothesis =>
    intro config earlier events final invariant isReachable hasNoExhaustion isExec
    obtain ⟨previousLabels, isPrevious⟩ := isReachable
    obtain ⟨stepEvents, next, laterEvents, isStep, isLater, isEvents⟩ :=
      (system program).exec_cons config label laterLabels events final isExec
    have isNotExhausting := hasNoExhaustion label (List.mem_cons_self ..)
    have isSimulated := step_simulated program ignoresAttemptContext config invariant label
      isNotExhausting stepEvents next isStep
    have nextInvariant := invariant_step program ignoresAttemptContext config invariant label
      stepEvents next isStep
    have isNextReachable := (system program).exec_snoc (system program).init previousLabels label
      earlier stepEvents config next isPrevious isStep
    obtain ⟨laterDexLabels, isLaterDex, laterRequests⟩ := inductionHypothesis next
      (earlier ++ stepEvents) laterEvents final nextInvariant ⟨_, isNextReachable⟩
      (fun other isMember => hasNoExhaustion other (List.mem_cons_of_mem _ isMember)) isLater
    have historyBefore := effectHistory_of_exec program ignoresAttemptContext previousLabels
      earlier config isPrevious
    have historyAfter := effectHistory_of_exec program ignoresAttemptContext
      (previousLabels ++ [label]) (earlier ++ stepEvents) next isNextReachable
    refine ⟨dexActionsOf program config label ++ laterDexLabels, ?_, ?_⟩
    · rw [isEvents, wpf5_eraseRepeatsAfter_append,
        wpf5_eraseRepeatsAfter_step program config label earlier stepEvents next isStep
          historyBefore historyAfter]
      exact (Dex.system program.failureFreeFlow).exec_append _ _ _ _ _ _ _ isSimulated isLaterDex
    · rw [List.filterMap_append, wpf5_dexActionsOf_requests, laterRequests, List.filterMap_cons]
      cases wpf5NonRetryRequestOf label <;> rfl

theorem wpf5_abstractConfig_initial (program : Program σ) :
    abstractConfig (Config.initial σ) = (Dex.system program.failureFreeFlow).init :=
  rfl

/-! ## The repetition form -/

/-- The first occurrence of an `fx` index is kept by the erasure. -/
theorem wpf5_mem_eraseRepeatsAfter (step : Dex.StepExecId σ) (method : Dex.Method)
    (index : Nat) :
    ∀ (trace earlier : List (Obs σ)),
      (∃ effect, Obs.fx step method effect index ∈ trace) →
      (¬ ∃ effect, Obs.fx step method effect index ∈ earlier) →
      ∃ effect, Obs.fx step method effect index ∈ trace ∧
        Dex.Obs.fx step method effect ∈ eraseRepeatsAfter earlier trace := by
  intro trace
  induction trace with
  | nil =>
    intro earlier isPresent _
    obtain ⟨effect, isMember⟩ := isPresent
    cases isMember
  | cons event laterEvents inductionHypothesis =>
    intro earlier isPresent isAbsent
    by_cases isSame : ∃ effect, event = Obs.fx step method effect index
    · obtain ⟨effect, isEvent⟩ := isSame
      subst isEvent
      have isNotRepeated : (Obs.fx step method effect index).isRepeatAfter earlier = false := by
        cases isRepeat : (Obs.fx step method effect index).isRepeatAfter earlier with
        | false => rfl
        | true => exact absurd ((wpf5_isRepeatAfter_fx _ _ _ _ _).mp isRepeat) isAbsent
      refine ⟨effect, List.mem_cons_self .., ?_⟩
      simp [eraseRepeatsAfter, isNotRepeated, Obs.toDex]
    · obtain ⟨effect, isMember⟩ := isPresent
      have isLater : Obs.fx step method effect index ∈ laterEvents := by
        rcases List.mem_cons.mp isMember with isHead | isTail
        · exact absurd ⟨effect, isHead.symm⟩ isSame
        · exact isTail
      obtain ⟨laterEffect, isLaterMember, isKept⟩ := inductionHypothesis (earlier ++ [event])
        ⟨effect, isLater⟩ (by
          rintro ⟨otherEffect, isOther⟩
          rcases List.mem_append.mp isOther with isEarlier | isEvent
          · exact isAbsent ⟨otherEffect, isEarlier⟩
          · simp only [List.mem_singleton] at isEvent
            exact isSame ⟨otherEffect, isEvent.symm⟩)
      refine ⟨laterEffect, List.mem_cons_of_mem _ isLaterMember, ?_⟩
      simp only [eraseRepeatsAfter]
      exact List.mem_append_right _ isKept

/-- A trace whose `fx` indexes carry one label each is a repetition of its
erasure. -/
theorem wpf5_isRepetition_eraseRepeats (trace : List (Obs σ))
    (isConsistent : ∀ step method firstEffect secondEffect index,
      Obs.fx step method firstEffect index ∈ trace →
      Obs.fx step method secondEffect index ∈ trace → firstEffect = secondEffect) :
    ∀ (earlier later : List (Obs σ)), earlier ++ later = trace →
      IsRepetitionAfter Dex.Obs.IsFx (eraseRepeatsAfter [] earlier) (later.map Obs.toDex)
        (eraseRepeatsAfter earlier later) := by
  intro earlier later
  induction later generalizing earlier with
  | nil => intro _; exact .done _
  | cons event laterEvents inductionHypothesis =>
    intro isTrace
    have isNextTrace : (earlier ++ [event]) ++ laterEvents = trace := by
      rw [List.append_assoc]
      exact isTrace
    have laterRepetition := inductionHypothesis (earlier ++ [event]) isNextTrace
    rw [wpf5_eraseRepeatsAfter_append [] earlier [event]] at laterRepetition
    simp only [List.nil_append] at laterRepetition
    simp only [List.map_cons, eraseRepeatsAfter]
    cases isRepeat : event.isRepeatAfter earlier with
    | false =>
      simp only [eraseRepeatsAfter, isRepeat] at laterRepetition ⊢
      simp only [Bool.false_eq_true, ite_false, List.singleton_append, List.append_nil]
        at laterRepetition ⊢
      exact .keep _ _ _ _ laterRepetition
    | true =>
      simp only [eraseRepeatsAfter, isRepeat, ite_true, List.nil_append, List.append_nil]
        at laterRepetition ⊢
      cases event with
      | fx step method effect index =>
        obtain ⟨otherEffect, isOther⟩ := (wpf5_isRepeatAfter_fx _ _ _ _ _).mp isRepeat
        obtain ⟨firstEffect, isFirst, isKept⟩ := wpf5_mem_eraseRepeatsAfter step method index
          earlier [] ⟨otherEffect, isOther⟩ (by rintro ⟨_, isMember⟩; cases isMember)
        have isSameEffect := isConsistent step method firstEffect effect index
          (by rw [← isTrace]; exact List.mem_append_left _ isFirst)
          (by rw [← isTrace]; exact List.mem_append_right _ (List.mem_cons_self ..))
        subst isSameEffect
        exact .insert _ _ _ _ trivial isKept laterRepetition
      | _ => simp [Obs.isRepeatAfter] at isRepeat

/-- The effect history makes the labels of each `fx` index agree. -/
theorem wpf5_effectLabels_consistent (program : Program σ) (config : Config σ)
    (trace : List (Obs σ)) (history : HasEffectHistory program config trace) :
    ∀ step method firstEffect secondEffect index,
      Obs.fx step method firstEffect index ∈ trace →
      Obs.fx step method secondEffect index ∈ trace → firstEffect = secondEffect := by
  intro step method firstEffect secondEffect index isFirst isSecond
  obtain ⟨_, _, firstArguments, isFirstArguments, isFirstEffect⟩ :=
    history.1 step method firstEffect index isFirst
  obtain ⟨_, _, secondArguments, isSecondArguments, isSecondEffect⟩ :=
    history.1 step method secondEffect index isSecond
  rw [isFirstArguments] at isSecondArguments
  cases isSecondArguments
  rw [isFirstEffect] at isSecondEffect
  exact Option.some.inj isSecondEffect

end DexExpressiveness.DexF
