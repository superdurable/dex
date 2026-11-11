import DexExpressiveness.Failure.Statements.Leaf

/-!
# WP-F5: the events and the run of one DexF step

Facts about `DexF.step` that the safety proofs need beyond the step lemmas:

- only [WORKER-EFFECT] emits an `fx`, and it emits exactly one;
- the run, once started, stays started and keeps its start request ID;
- before the start, only [FLOW-START] creates the run.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- An event that is not an external effect. -/
def WPF5IsNotFx : Obs σ → Prop
  | .fx _ _ _ _ => False
  | _ => True

theorem wpf5_nil_notFx : ∀ event, event ∈ ([] : List (Obs σ)) → WPF5IsNotFx event := by
  intro event isMember
  cases isMember

theorem wpf5_closeEvents_notFx (before after : Dex.Run σ) :
    ∀ event, event ∈ Config.closeEvents before after → WPF5IsNotFx event := by
  intro event isMember
  unfold Config.closeEvents at isMember
  split at isMember
  · simp only [List.mem_singleton] at isMember
    subst isMember
    trivial
  · cases isMember

theorem wpf5_commitRun_events (config : Config σ) (run : Run σ) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) :
    (config.commitRun run isNormal completed inputEvents change).1 =
      inputEvents ++ Config.closeEvents run.base
        (change { run with base := { run.base with seq := run.base.seq + 1 } }).reconcileAttempts.base :=
  rfl

theorem wpf5_commitRun_notFx (config : Config σ) (run : Run σ) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) (inputNotFx : ∀ event, event ∈ inputEvents → WPF5IsNotFx event) :
    ∀ event, event ∈ (config.commitRun run isNormal completed inputEvents change).1 →
      WPF5IsNotFx event := by
  intro event isMember
  rw [wpf5_commitRun_events] at isMember
  rcases List.mem_append.mp isMember with isInput | isClose
  · exact inputNotFx event isInput
  · exact wpf5_closeEvents_notFx _ _ event isClose

theorem wpf5_failAttempt_notFx (config : Config σ) (run : Run σ) (step : Dex.StepExecId σ)
    (method : Dex.Method) (attempt : Attempt σ) (cause : FailureCause)
    (lostTaskId : Option Nat) (decision : RetryDecision) :
    ∀ event, event ∈ (config.failAttempt run step method attempt cause lostTaskId decision).1 →
      WPF5IsNotFx event := by
  unfold Config.failAttempt
  cases decision with
  | retryAfter backoffMicros =>
    exact wpf5_commitRun_notFx _ _ _ _ _ _ wpf5_nil_notFx
  | exhausted reason =>
    exact wpf5_commitRun_notFx _ _ _ _ _ _ wpf5_nil_notFx

theorem wpf5_input_close_notFx (inputEvents : List (Obs σ))
    (inputNotFx : ∀ event, event ∈ inputEvents → WPF5IsNotFx event) (before after : Dex.Run σ) :
    ∀ event, event ∈ inputEvents ++ Config.closeEvents before after → WPF5IsNotFx event := by
  intro event isMember
  rcases List.mem_append.mp isMember with isInput | isClose
  · exact inputNotFx event isInput
  · exact wpf5_closeEvents_notFx _ _ event isClose

theorem wpf5_singleton_notFx (event : Obs σ) (isNotFx : WPF5IsNotFx event) :
    ∀ other, other ∈ [event] → WPF5IsNotFx other := by
  intro other isMember
  simp only [List.mem_singleton] at isMember
  subst isMember
  exact isNotFx

set_option hygiene false in
/-- Close a goal `event ∈ events → WPF5IsNotFx event` after `events` is known. -/
macro "wpf5_finish_notFx" : tactic => `(tactic| first
  | (cases isStep; done)
  | (have isEvents := congrArg (fun pair => pair.1) (Option.some.inj isStep)
     simp only at isEvents
     rw [← isEvents]
     first
       | exact wpf5_failAttempt_notFx _ _ _ _ _ _ _ _
       | exact wpf5_commitRun_notFx _ _ _ _ _ _ wpf5_nil_notFx
       | exact wpf5_commitRun_notFx _ _ _ _ _ _ (wpf5_singleton_notFx _ trivial)
       | exact wpf5_singleton_notFx _ trivial
       | exact wpf5_nil_notFx))

/-- Every DexF step other than [WORKER-EFFECT] emits no `fx`. -/
theorem wpf5_step_notFx (program : Program σ) (config : Config σ) (action : Action σ)
    (isNotEffect : ∀ taskId isFirstOccurrence, action ≠ .workerEffect taskId isFirstOccurrence)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    ∀ event, event ∈ events → WPF5IsNotFx event := by
  cases action with
  | workerEffect taskId isFirstOccurrence => exact absurd rfl (isNotEffect taskId isFirstOccurrence)
  | request request =>
    cases request <;> simp only [step] at isStep <;> (repeat' split at isStep) <;>
      wpf5_finish_notFx
  | engineReject request =>
    cases request <;> simp only [step] at isStep <;> (repeat' split at isStep) <;>
      wpf5_finish_notFx
  | _ =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals wpf5_finish_notFx

/-- [WORKER-EFFECT] emits exactly one `fx`. -/
theorem wpf5_workerEffect_events (program : Program σ) (config : Config σ) (taskId : Nat)
    (isFirstOccurrence : Bool) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.workerEffect taskId isFirstOccurrence) = some (events, next)) :
    ∃ step method effect index, events = [Obs.fx step method effect index] := by
  simp only [step] at isStep
  repeat' split at isStep
  all_goals first
    | (cases isStep; done)
    | (injection isStep with isPair
       injection isPair with isEvents
       exact ⟨_, _, _, _, isEvents.symm⟩)

/-- The events of a DexF step: no `fx`, or exactly one. -/
theorem wpf5_step_events (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    (∀ event, event ∈ events → WPF5IsNotFx event) ∨
      ∃ step method effect index, events = [Obs.fx step method effect index] := by
  by_cases isEffect : ∃ taskId isFirstOccurrence, action = .workerEffect taskId isFirstOccurrence
  · obtain ⟨taskId, isFirstOccurrence, isAction⟩ := isEffect
    subst isAction
    exact .inr (wpf5_workerEffect_events program config taskId isFirstOccurrence events next isStep)
  · refine .inl (wpf5_step_notFx program config action ?_ events next isStep)
    intro taskId isFirstOccurrence isAction
    exact isEffect ⟨taskId, isFirstOccurrence, isAction⟩

/-! ## The run of a step -/

theorem wpf5_fireRetryTimer_startRequestId (run : Run σ) (timer : RetryTimerId σ) :
    (run.fireRetryTimer timer).startRequestId = run.startRequestId := by
  unfold Run.fireRetryTimer
  split
  · simp only
    split
    · split <;> rfl
    · rfl
  · rfl

theorem wpf5_foldl_fireDueTimer_startRequestId (timers : List (DueTimer σ)) (run : Run σ) :
    (timers.foldl Run.fireDueTimer run).startRequestId = run.startRequestId := by
  induction timers generalizing run with
  | nil => rfl
  | cons timer laterTimers inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [inductionHypothesis]
    cases timer with
    | wait timer => rfl
    | retry timer => exact wpf5_fireRetryTimer_startRequestId run timer

theorem wpf5_commitRun_run (config : Config σ) (run : Run σ) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) :
    (config.commitRun run isNormal completed inputEvents change).2.run =
      some (change { run with base := { run.base with seq := run.base.seq + 1 } }).reconcileAttempts :=
  rfl

theorem wpf5_failAttempt_run (config : Config σ) (run : Run σ) (step : Dex.StepExecId σ)
    (method : Dex.Method) (attempt : Attempt σ) (cause : FailureCause)
    (lostTaskId : Option Nat) (decision : RetryDecision) :
    ((config.failAttempt run step method attempt cause lostTaskId decision).2.run.map
      (fun current => current.startRequestId)) = some run.startRequestId := by
  unfold Config.failAttempt
  cases decision <;> rfl

set_option hygiene false in
/-- Close a goal about the start request ID after a step is known. -/
macro "wpf5_finish_run" : tactic => `(tactic| first
  | (cases isStep; done)
  | (simp only [hasRun, reduceCtorEq] at *; done)
  | (have isNext := congrArg (fun pair => pair.2) (Option.some.inj isStep)
     simp only at isNext
     rw [← isNext]
     first
       | (rw [wpf5_failAttempt_run]
          try simp only [hasRun, Option.some.injEq] at *
          try subst_vars
          rfl)
       | (rw [wpf5_commitRun_run]
          try simp only [hasRun, Option.some.injEq] at *
          try subst_vars
          simp [Run.reconcileAttempts, Run.setAttempt, wpf5_foldl_fireDueTimer_startRequestId])
       | (show Option.map _ config.run = _
          rw [hasRun]
          rfl)
       | (show Option.map _ (Config.stampOnly _).run = _
          simp [Config.stampOnly, hasRun])))

/-- A started run stays started and keeps its start request ID. -/
theorem wpf5_step_startRequestId (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ) (run : Run σ) (hasRun : config.run = some run)
    (isStep : step program config action = some (events, next)) :
    next.run.map (fun current => current.startRequestId) = some run.startRequestId := by
  cases action with
  | request request =>
    cases request <;> simp only [step] at isStep <;> (repeat' split at isStep) <;>
      wpf5_finish_run
  | engineReject request =>
    cases request <;> simp only [step] at isStep <;> (repeat' split at isStep) <;>
      wpf5_finish_run
  | _ =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals wpf5_finish_run

set_option hygiene false in
/-- Close a goal `next.run = none` after a step is known. -/
macro "wpf5_finish_noRun" : tactic => `(tactic| first
  | (cases isStep; done)
  | (simp only [hasNoRun, reduceCtorEq] at *; done)
  | (have isNext := congrArg (fun pair => pair.2) (Option.some.inj isStep)
     simp only at isNext
     rw [← isNext]
     first
       | (show (Config.stampOnly _).run = none
          simp [Config.stampOnly, hasNoRun]
          done)
       | exact hasNoRun
       | (simp only [Config.updateRealTask, Config.removeRealTask, Config.moveToZombies]
          exact hasNoRun)
       | (simp only [Config.updateRealTask, Config.removeRealTask, Config.moveToZombies]
          split <;> exact hasNoRun)))

/-- Before the start, every step other than [FLOW-START] leaves the Flow
without a run. -/
theorem wpf5_step_noRun (program : Program σ) (config : Config σ) (action : Action σ)
    (isNotStart : ∀ requestId stepType input, action ≠ .request (.startFlow requestId stepType input))
    (events : List (Obs σ)) (next : Config σ) (hasNoRun : config.run = none)
    (isStep : step program config action = some (events, next)) :
    next.run = none := by
  cases action with
  | request request =>
    cases request with
    | startFlow requestId stepType input => exact absurd rfl (isNotStart requestId stepType input)
    | publish chan value =>
      simp only [step] at isStep
      repeat' split at isStep
      all_goals wpf5_finish_noRun
  | engineReject request =>
    cases request <;> simp only [step] at isStep <;> (repeat' split at isStep) <;>
      wpf5_finish_noRun
  | _ =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals wpf5_finish_noRun

/-- [FLOW-START] records its request ID as the start request ID. -/
theorem wpf5_step_flowStart (program : Program σ) (config : Config σ) (requestId : Nat)
    (stepType : σ.StepType) (input : σ.Val) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.request (.startFlow requestId stepType input)) =
      some (events, next)) :
    next.run.map (fun current => current.startRequestId) = some requestId := by
  simp only [step] at isStep
  repeat' split at isStep
  all_goals first
    | (cases isStep; done)
    | (have isNext := congrArg (fun pair => pair.2) (Option.some.inj isStep)
       simp only at isNext
       rw [← isNext, wpf5_commitRun_run]
       simp [Run.reconcileAttempts])

/-- After the start, a label whose request is a StartFlow is [START-RETRY] with
the start request ID. -/
theorem wpf5_startFlowRequest_isRetry (program : Program σ) (config : Config σ)
    (action : Action σ) (events : List (Obs σ)) (next : Config σ) (run : Run σ)
    (hasRun : config.run = some run) (isStep : step program config action = some (events, next))
    (requestId : Nat) (stepType : σ.StepType) (input : σ.Val)
    (isRequest : action.requestOf = some (.startFlow requestId stepType input)) :
    action = .startRetry requestId stepType input ∧ requestId = run.startRequestId := by
  cases action with
  | request request =>
    simp only [Action.requestOf, Option.some.injEq] at isRequest
    subst isRequest
    simp [step, hasRun] at isStep
  | startRetry retryRequestId retryStepType retryInput =>
    simp only [Action.requestOf, Option.some.injEq, Request.startFlow.injEq] at isRequest
    obtain ⟨isId, isStepType, isInput⟩ := isRequest
    subst isId isStepType isInput
    refine ⟨rfl, ?_⟩
    simp only [step, hasRun] at isStep
    split at isStep
    · rename_i isEnabled
      simp only [Bool.and_eq_true, decide_eq_true_eq] at isEnabled
      exact isEnabled.2
    · cases isStep
  | engineReject request =>
    simp only [Action.requestOf, Option.some.injEq] at isRequest
    subst isRequest
    simp [step] at isStep
  | _ => simp [Action.requestOf] at isRequest

end DexExpressiveness.DexF
