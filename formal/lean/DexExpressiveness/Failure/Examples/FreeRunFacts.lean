import DexExpressiveness.Failure.Statements

/-!
# Facts about every failure-free run, for the Stage 3 counterexamples

Test tooling, not part of the theorem. A counterexample to a hypothesis must
show that a DexF trace is not a repetition of **any** failure-free trace. The
facts below bound every finite Dex trace of a Flow:

- `isRepetition_mem`, `isRepetition_filter_not_fx`: a repetition inserts only
  `fx` events, each equal to a kept one; so every event of the trace is in the
  original, and the two have the same events other than `fx`.
- `freeTrace_fx_isComputed`: every `fx` of a failure-free trace carries a
  label that the Flow computes. It follows from the proved
  `freeFiniteTrace_isFailureFiniteTrace` and `effectHistory_of_exec`.
- `freeTrace_hasNoFailedClose`: a Flow whose Execute handlers never decide
  ForceFail never closes as Failed in a failure-free run.
- `freeTrace_pubCount_le`: a failure-free trace has at most as many `pub`
  events as its requests have publications.
-/

namespace DexExpressiveness.DexF.Examples

open DexExpressiveness

variable {σ : Dex.Signature}

/-! ## Repetitions -/

/-- Every event of a repetition is kept or in the original. -/
theorem isRepetitionAfter_mem {α : Type} {IsRepeatable : α → Prop} {kept trace original : List α}
    (isRepetition : IsRepetitionAfter IsRepeatable kept trace original) :
    ∀ event, event ∈ trace → event ∈ kept ∨ event ∈ original := by
  induction isRepetition with
  | done _ => intro event isMember; cases isMember
  | keep kept keptEvent trace original _ inductionHypothesis =>
    intro event isMember
    rcases List.mem_cons.mp isMember with isKeptEvent | isLater
    · exact Or.inr (isKeptEvent ▸ List.mem_cons_self ..)
    · rcases inductionHypothesis event isLater with isKept | isOriginal
      · rcases List.mem_append.mp isKept with isOld | isNew
        · exact Or.inl isOld
        · exact Or.inr (by simp at isNew; exact isNew ▸ List.mem_cons_self ..)
      · exact Or.inr (List.mem_cons_of_mem _ isOriginal)
  | insert kept insertedEvent trace original _ isInsertedKept _ inductionHypothesis =>
    intro event isMember
    rcases List.mem_cons.mp isMember with isInserted | isLater
    · exact Or.inl (isInserted ▸ isInsertedKept)
    · exact inductionHypothesis event isLater

/-- Every event of a repetition of `original` is in `original`. -/
theorem isRepetition_mem {trace original : List (Dex.Obs σ)}
    (isRepetition : IsRepetition trace original) (event : Dex.Obs σ) (isMember : event ∈ trace) :
    event ∈ original := by
  rcases isRepetitionAfter_mem isRepetition event isMember with isKept | isOriginal
  · cases isKept
  · exact isOriginal

/-- Whether a Dex event is an external effect `fx`. -/
def Dex.Obs.isFxEvent : Dex.Obs σ → Bool
  | .fx _ _ _ => true
  | _ => false

/-- A repetition inserts only `fx` events: the events other than `fx` are the
same. -/
theorem isRepetitionAfter_filter_not_fx {kept trace original : List (Dex.Obs σ)}
    (isRepetition : IsRepetitionAfter Dex.Obs.IsFx kept trace original) :
    trace.filter (fun event => !Dex.Obs.isFxEvent event) =
      original.filter (fun event => !Dex.Obs.isFxEvent event) := by
  induction isRepetition with
  | done _ => rfl
  | keep _ event _ _ _ inductionHypothesis =>
    simp only [List.filter_cons, inductionHypothesis]
  | insert _ event _ _ isFx _ _ inductionHypothesis =>
    have isFxEvent : Dex.Obs.isFxEvent event = true := by
      cases event <;> simp_all [Dex.Obs.IsFx, Dex.Obs.isFxEvent]
    simp [isFxEvent, inductionHypothesis]

/-- A repetition and its original have the same events other than `fx`. -/
theorem isRepetition_filter_not_fx {trace original : List (Dex.Obs σ)}
    (isRepetition : IsRepetition trace original) :
    trace.filter (fun event => !Dex.Obs.isFxEvent event) =
      original.filter (fun event => !Dex.Obs.isFxEvent event) :=
  isRepetitionAfter_filter_not_fx isRepetition

/-! ## Effects of failure-free runs -/

/-- Every `fx` of a failure-free trace of `flow` carries a label that `flow`
computes for some arguments of its invocation. -/
theorem freeTrace_fx_isComputed (flow : Dex.FlowDef σ) (environment : Dex.Environment σ)
    (requests : List (Dex.Request σ)) (trace : List (Dex.Obs σ))
    (isFree : IsFreeFiniteTrace flow environment requests trace) (step : Dex.StepExecId σ)
    (method : Dex.Method) (effect : σ.Fx) (isMember : Dex.Obs.fx step method effect ∈ trace) :
    ∃ (arguments : Arguments σ) (index : Nat),
      (arguments.compute flow (step, method)).1[index]? = some effect := by
  obtain ⟨_, failureTrace, isFailureTrace, _, isMapped⟩ :=
    freeFiniteTrace_isFailureFiniteTrace (Program.ofFlow flow) (fun _ _ => rfl) environment
      requests trace isFree
  obtain ⟨labels, final, isExec, _⟩ := isFailureTrace
  have history := effectHistory_of_exec (Program.ofFlow flow) (fun _ _ => rfl) labels
    failureTrace final isExec
  rw [← isMapped] at isMember
  obtain ⟨event, isEventMember, isEvent⟩ := List.mem_map.mp isMember
  cases event with
  | fx eventStep eventMethod eventEffect index =>
    simp only [Obs.toDex, Dex.Obs.fx.injEq] at isEvent
    obtain ⟨rfl, rfl, rfl⟩ := isEvent
    obtain ⟨_, _, arguments, _, isComputed⟩ :=
      history.1 eventStep eventMethod eventEffect index isEventMember
    exact ⟨arguments, index - 1, isComputed⟩
  | _ => simp [Obs.toDex] at isEvent

/-! ## Failure-free runs never close as Failed -/

/-- The Execute handlers of a Flow never decide ForceFail. -/
def FlowHasNoForceFail (flow : Dex.FlowDef σ) : Prop :=
  ∀ stepType context input snapshot conditionResults message,
    ((flow.stepDef stepType).execute context input snapshot conditionResults).2.decision.close ≠
      some (.forceFail message)

/-- A response that does not decide ForceFail. -/
def ResponseHasNoForceFail : Dex.Response σ → Prop
  | .execute response => ∀ message, response.decision.close ≠ some (.forceFail message)
  | .waitFor _ => True

/-- A task phase whose response, if any, does not decide ForceFail. -/
def PhaseHasNoForceFail : Dex.TaskPhase σ → Prop
  | .started => True
  | .running _ response => ResponseHasNoForceFail response
  | .returned response => ResponseHasNoForceFail response

/-- The invariant: the run is not closed as Failed, and no task holds a
ForceFail. -/
structure HasNoFailedClose (config : Dex.Config σ) : Prop where
  /-- The run is not closed as Failed. -/
  runIsNotFailed : ∀ run, config.run = some run → ∀ message, run.status ≠ .closed (.failed message)
  /-- No task holds a ForceFail. -/
  tasksHaveNoForceFail : ∀ task, task ∈ config.tasks → PhaseHasNoForceFail task.phase

/-- The events of a step contain no `closed` of a Failed close. -/
def HasNoFailedCloseEvent (events : List (Dex.Obs σ)) : Prop :=
  ∀ message outputs, Dex.Obs.closed (.failed message) outputs ∉ events

/-- A task's handler call does not decide ForceFail. -/
theorem compute_hasNoForceFail (flow : Dex.FlowDef σ) (hasNoForceFail : FlowHasNoForceFail flow)
    (task : Dex.Task σ) : ResponseHasNoForceFail (task.compute flow).2 := by
  unfold Dex.Task.compute
  cases task.method with
  | waitFor =>
    simp only
    split <;> trivial
  | execute =>
    intro message
    exact hasNoForceFail _ _ _ _ _ message

/-- The `closed` event of a commit carries the status of the run after it. -/
theorem closeEvents_hasNoFailedCloseEvent (before after : Dex.Run σ)
    (isNotFailed : ∀ message, after.status ≠ .closed (.failed message)) :
    HasNoFailedCloseEvent (Dex.Config.closeEvents before after) := by
  intro message outputs isMember
  unfold Dex.Config.closeEvents at isMember
  split at isMember
  · rename_i status _ isAfter
    simp only [List.mem_singleton, Dex.Obs.closed.injEq] at isMember
    exact isNotFailed message (isAfter.trans (congrArg _ isMember.1.symm))
  · cases isMember

/-- A commit keeps the invariant when the changed run is not Failed. -/
theorem commit_hasNoFailedClose (config : Dex.Config σ) (run : Dex.Run σ)
    (inputEvents : List (Dex.Obs σ)) (change : Dex.Run σ → Dex.Run σ)
    (tasksHaveNoForceFail : ∀ task, task ∈ config.tasks → PhaseHasNoForceFail task.phase)
    (isNotFailed : ∀ message,
      (change { run with seq := run.seq + 1 }).status ≠ .closed (.failed message))
    (inputHasNoFailedClose : HasNoFailedCloseEvent inputEvents) :
    HasNoFailedClose (config.commit run inputEvents change).2 ∧
      HasNoFailedCloseEvent (config.commit run inputEvents change).1 := by
  refine ⟨⟨?_, tasksHaveNoForceFail⟩, ?_⟩
  · intro committedRun isRun message
    simp only [Dex.Config.commit, Option.some.injEq] at isRun
    rw [← isRun]
    exact isNotFailed message
  · intro message outputs isMember
    simp only [Dex.Config.commit, List.mem_append] at isMember
    rcases isMember with isInput | isClose
    · exact inputHasNoFailedClose message outputs isInput
    · exact closeEvents_hasNoFailedCloseEvent _ _ isNotFailed message outputs isClose

/-- `commit_hasNoFailedClose` for a step whose result is a commit. -/
theorem commit_case (config : Dex.Config σ) (run : Dex.Run σ) (inputEvents : List (Dex.Obs σ))
    (change : Dex.Run σ → Dex.Run σ) (events : List (Dex.Obs σ)) (next : Dex.Config σ)
    (isCommit : config.commit run inputEvents change = (events, next))
    (tasksHaveNoForceFail : ∀ task, task ∈ config.tasks → PhaseHasNoForceFail task.phase)
    (isNotFailed : ∀ message,
      (change { run with seq := run.seq + 1 }).status ≠ .closed (.failed message))
    (inputHasNoFailedClose : HasNoFailedCloseEvent inputEvents) :
    HasNoFailedClose next ∧ HasNoFailedCloseEvent events := by
  have isEvents : events = (config.commit run inputEvents change).1 := by rw [isCommit]
  have isNext : next = (config.commit run inputEvents change).2 := by rw [isCommit]
  subst isEvents isNext
  exact commit_hasNoFailedClose config run inputEvents change tasksHaveNoForceFail isNotFailed
    inputHasNoFailedClose

/-- The close of a run gives the close status or keeps the status. -/
theorem status_closeRun (run : Dex.Run σ) (status : Dex.CloseStatus) :
    (run.closeRun status).status = .closed status ∨ (run.closeRun status).status = run.status := by
  unfold Dex.Run.closeRun
  split
  · exact Or.inr rfl
  · exact Or.inl rfl

/-- `finish` keeps the status. -/
theorem status_finishStep (run : Dex.Run σ) (step : Dex.StepExecId σ) (phase : Dex.Phase) :
    (run.finishStep step phase).status = run.status := rfl

/-- An Execute decision without ForceFail does not close the run as Failed. -/
theorem applyDecision_isNotFailed (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (decision : Dex.Decision σ) (hasNoForceFail : ∀ message, decision.close ≠ some (.forceFail message))
    (isNotFailed : ∀ message, run.status ≠ .closed (.failed message)) (message : String) :
    (run.applyDecision step decision).status ≠ .closed (.failed message) := by
  unfold Dex.Run.applyDecision
  split
  · rcases status_closeRun ((run.finishStep step .completed).collectOutput step _) .completed
      with isClosed | isSame
    · rw [isClosed]; simp
    · rw [isSame]
      unfold Dex.Run.collectOutput
      split
      · exact isNotFailed message
      · exact isNotFailed message
  · rename_i failMessage isFail
    exact absurd isFail (hasNoForceFail failMessage)
  · exact isNotFailed message
  · rw [Dex.Run.status_foldl_newStep]
    exact isNotFailed message

/-- Changing tasks of `Θ` keeps the invariant on tasks when every changed task
has no ForceFail. -/
theorem updateTask_hasNoForceFail (config : Dex.Config σ) (taskId : Nat)
    (change : Dex.Task σ → Dex.Task σ)
    (tasksHaveNoForceFail : ∀ task, task ∈ config.tasks → PhaseHasNoForceFail task.phase)
    (changeHasNoForceFail : ∀ task, PhaseHasNoForceFail (change task).phase) :
    ∀ task, task ∈ (config.updateTask taskId change).tasks → PhaseHasNoForceFail task.phase := by
  intro task isMember
  simp only [Dex.Config.updateTask, List.mem_map] at isMember
  obtain ⟨original, isOriginal, isTask⟩ := isMember
  rw [← isTask]
  split
  · exact changeHasNoForceFail original
  · exact tasksHaveNoForceFail original isOriginal

/-- Removing a task keeps the invariant on tasks. -/
theorem removeTask_hasNoForceFail (config : Dex.Config σ) (taskId : Nat)
    (tasksHaveNoForceFail : ∀ task, task ∈ config.tasks → PhaseHasNoForceFail task.phase) :
    ∀ task, task ∈ (config.removeTask taskId).tasks → PhaseHasNoForceFail task.phase := by
  intro task isMember
  simp only [Dex.Config.removeTask, List.mem_filter] at isMember
  exact tasksHaveNoForceFail task isMember.1

/-- The task that `findTask?` finds is in `Θ`. -/
theorem findTask?_mem (config : Dex.Config σ) (taskId : Nat) (task : Dex.Task σ)
    (isFound : config.findTask? taskId = some task) : task ∈ config.tasks :=
  List.mem_of_find?_eq_some isFound

/-- Every Dex step keeps the invariant and emits no Failed close. -/
theorem step_hasNoFailedClose (flow : Dex.FlowDef σ) (hasNoForceFail : FlowHasNoForceFail flow)
    (config : Dex.Config σ) (invariant : HasNoFailedClose config) (action : Dex.Action σ)
    (events : List (Dex.Obs σ)) (next : Dex.Config σ)
    (isStep : Dex.step flow config action = some (events, next)) :
    HasNoFailedClose next ∧ HasNoFailedCloseEvent events := by
  have noEvent : HasNoFailedCloseEvent ([] : List (Dex.Obs σ)) := by
    intro _ _ isMember; cases isMember
  cases action with
  | tick duration =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
      exact ⟨⟨invariant.runIsNotFailed, invariant.tasksHaveNoForceFail⟩, noEvent⟩
    · cases isStep
  | request clientRequest =>
    cases clientRequest with
    | startFlow stepType input =>
      simp only [Dex.step] at isStep
      split at isStep
      · cases isStep
      · split at isStep
        · refine commit_case _ _ _ _ _ _ (Option.some.inj isStep) invariant.tasksHaveNoForceFail
            ?_ ?_
          · intro message isFailed
            cases isFailed
          · intro message outputs isMember
            simp at isMember
        · cases isStep
    | publish chan value =>
      simp only [Dex.step] at isStep
      split at isStep
      · cases isStep
        refine ⟨invariant, ?_⟩
        intro message outputs isMember
        simp at isMember
      · rename_i run isRun
        split at isStep
        · cases isStep
          refine ⟨invariant, ?_⟩
          intro message outputs isMember
          simp at isMember
        · split at isStep
          · refine commit_case _ _ _ _ _ _ (Option.some.inj isStep)
              invariant.tasksHaveNoForceFail ?_ ?_
            · intro message
              rw [Dex.Run.status_wake, wpf6c_status_applyPublications]
              exact invariant.runIsNotFailed run isRun message
            · intro message outputs isMember
              simp at isMember
          · cases isStep
  | stepStart stepId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i run isRun
      split at isStep
      · cases isStep
      · split at isStep
        · split at isStep
          · cases isStep
            refine ⟨⟨?_, ?_⟩, ?_⟩
            · intro committedRun isCommittedRun message
              simp only [Dex.Config.commit, Option.some.injEq] at isCommittedRun
              rw [← isCommittedRun]
              exact invariant.runIsNotFailed run isRun message
            · intro task isMember
              rcases List.mem_append.mp isMember with isOld | isNew
              · exact invariant.tasksHaveNoForceFail task isOld
              · simp at isNew
                rw [isNew]
                trivial
            · intro message outputs isMember
              simp only [Dex.Config.commit, List.nil_append] at isMember
              refine closeEvents_hasNoFailedCloseEvent _ _ ?_ message outputs isMember
              intro failMessage
              exact invariant.runIsNotFailed run isRun failMessage
          · cases isStep
        · cases isStep
  | workerRun taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
        refine ⟨⟨invariant.runIsNotFailed, ?_⟩, noEvent⟩
        exact updateTask_hasNoForceFail config taskId _ invariant.tasksHaveNoForceFail
          (fun _ => compute_hasNoForceFail flow hasNoForceFail _)
      · cases isStep
  | workerEffect taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i task isFound
      split at isStep
      · rename_i effect laterEffects response isPhase
        cases isStep
        have responseOk : ResponseHasNoForceFail response := by
          have phaseOk := invariant.tasksHaveNoForceFail task (findTask?_mem config taskId task isFound)
          rw [isPhase] at phaseOk
          exact phaseOk
        refine ⟨⟨invariant.runIsNotFailed, ?_⟩, ?_⟩
        · exact updateTask_hasNoForceFail config taskId _ invariant.tasksHaveNoForceFail
            (fun _ => responseOk)
        · intro message outputs isMember
          simp at isMember
      · cases isStep
  | workerReturn taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i task isFound
      split at isStep
      · rename_i response isPhase
        cases isStep
        have responseOk : ResponseHasNoForceFail response := by
          have phaseOk := invariant.tasksHaveNoForceFail task (findTask?_mem config taskId task isFound)
          rw [isPhase] at phaseOk
          exact phaseOk
        refine ⟨⟨invariant.runIsNotFailed, ?_⟩, noEvent⟩
        exact updateTask_hasNoForceFail config taskId _ invariant.tasksHaveNoForceFail
          (fun _ => responseOk)
      · cases isStep
  | workerAbandon taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
      · cases isStep
        exact ⟨⟨invariant.runIsNotFailed,
          removeTask_hasNoForceFail config taskId invariant.tasksHaveNoForceFail⟩, noEvent⟩
  | stepComplete taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · rename_i run task isRun isFound
      split at isStep
      · split at isStep
        · refine commit_case _ _ _ _ _ _ (Option.some.inj isStep)
            (removeTask_hasNoForceFail config taskId invariant.tasksHaveNoForceFail) ?_ noEvent
          intro message
          rw [Dex.Run.status_registerWait, Dex.Run.status_applyEffects]
          exact invariant.runIsNotFailed run isRun message
        · rename_i response _ isPhase
          have responseOk : ResponseHasNoForceFail (.execute response) := by
            have phaseOk := invariant.tasksHaveNoForceFail task (findTask?_mem config taskId task isFound)
            rw [isPhase] at phaseOk
            exact phaseOk
          refine commit_case _ _ _ _ _ _ (Option.some.inj isStep)
            (removeTask_hasNoForceFail config taskId invariant.tasksHaveNoForceFail) ?_ noEvent
          intro message
          refine applyDecision_isNotFailed _ _ _ responseOk ?_ message
          intro failMessage
          rw [Dex.Run.status_applyEffects]
          exact invariant.runIsNotFailed run isRun failMessage
        · cases isStep
      · cases isStep
    · cases isStep
  | stepCompleteStale taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · split at isStep
        · cases isStep
        · cases isStep
          exact ⟨⟨invariant.runIsNotFailed,
            removeTask_hasNoForceFail config taskId invariant.tasksHaveNoForceFail⟩, noEvent⟩
      · cases isStep
  | timerFire count =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i run isRun
      split at isStep
      · refine commit_case _ _ _ _ _ _ (Option.some.inj isStep) invariant.tasksHaveNoForceFail ?_
          noEvent
        intro message
        rw [Dex.Run.status_foldl_fireTimer]
        exact invariant.runIsNotFailed run isRun message
      · cases isStep

/-- A finite Dex run of a Flow without ForceFail emits no Failed close. -/
theorem exec_hasNoFailedClose (flow : Dex.FlowDef σ) (hasNoForceFail : FlowHasNoForceFail flow)
    (labels : List (Dex.Action σ)) (config : Dex.Config σ) (invariant : HasNoFailedClose config)
    (events : List (Dex.Obs σ)) (final : Dex.Config σ)
    (isExec : (Dex.system flow).exec config labels = some (events, final)) :
    HasNoFailedCloseEvent events := by
  induction labels generalizing config events with
  | nil =>
    simp only [TransitionSystem.exec, Option.some.injEq, Prod.mk.injEq] at isExec
    rw [← isExec.1]
    intro _ _ isMember
    cases isMember
  | cons label laterLabels inductionHypothesis =>
    obtain ⟨stepEvents, next, laterEvents, isStep, isLater, isEvents⟩ :=
      (Dex.system flow).exec_cons config label laterLabels events final isExec
    obtain ⟨nextInvariant, stepOk⟩ :=
      step_hasNoFailedClose flow hasNoForceFail config invariant label stepEvents next isStep
    have laterOk := inductionHypothesis next nextInvariant laterEvents isLater
    rw [isEvents]
    intro message outputs isMember
    rcases List.mem_append.mp isMember with isStepMember | isLaterMember
    · exact stepOk message outputs isStepMember
    · exact laterOk message outputs isLaterMember

/-- **A failure-free trace of a Flow without ForceFail has no Failed close.** -/
theorem freeTrace_hasNoFailedClose (flow : Dex.FlowDef σ) (hasNoForceFail : FlowHasNoForceFail flow)
    (environment : Dex.Environment σ) (requests : List (Dex.Request σ)) (trace : List (Dex.Obs σ))
    (isFree : IsFreeFiniteTrace flow environment requests trace) :
    HasNoFailedCloseEvent trace := by
  obtain ⟨labels, final, isExec, _⟩ := isFree
  exact exec_hasNoFailedClose flow hasNoForceFail labels _
    ⟨fun _ isRun => (by simp [Dex.system, Dex.Config.initial] at isRun),
      fun _ isMember => (by simp [Dex.system, Dex.Config.initial] at isMember)⟩ trace final isExec

/-! ## Publications of failure-free runs -/

/-- Whether a Dex event is a `pub`. -/
def Dex.Obs.isPubEvent : Dex.Obs σ → Bool
  | .pub _ _ => true
  | _ => false

/-- Whether a Dex request is a publication. -/
def Dex.Request.isPublication : Dex.Request σ → Bool
  | .publish _ _ => true
  | _ => false

/-- The `closed` event of a commit is not a `pub`. -/
theorem closeEvents_pubCount (before after : Dex.Run σ) :
    (Dex.Config.closeEvents before after).countP Dex.Obs.isPubEvent = 0 := by
  unfold Dex.Config.closeEvents
  split <;> rfl

/-- A commit with no input event emits no `pub`. -/
theorem commit_pubCount (config : Dex.Config σ) (run : Dex.Run σ) (inputEvents : List (Dex.Obs σ))
    (change : Dex.Run σ → Dex.Run σ) :
    (config.commit run inputEvents change).1.countP Dex.Obs.isPubEvent =
      inputEvents.countP Dex.Obs.isPubEvent := by
  simp only [Dex.Config.commit, List.countP_append, closeEvents_pubCount, Nat.add_zero]

/-- A Dex step emits a `pub` only for a publication request, and at most one. -/
theorem step_pubCount (flow : Dex.FlowDef σ) (config : Dex.Config σ) (action : Dex.Action σ)
    (events : List (Dex.Obs σ)) (next : Dex.Config σ)
    (isStep : Dex.step flow config action = some (events, next)) :
    events.countP Dex.Obs.isPubEvent ≤
      (((Dex.system flow).requestOf action).toList).countP Dex.Request.isPublication := by
  cases action with
  | request clientRequest =>
    cases clientRequest with
    | startFlow stepType input =>
      simp only [Dex.step] at isStep
      repeat' (split at isStep)
      all_goals (cases isStep)
      simp [closeEvents_pubCount, Dex.Obs.isPubEvent]
    | publish chan value =>
      have isAtMostOne : events.countP Dex.Obs.isPubEvent ≤ 1 := by
        simp only [Dex.step] at isStep
        repeat' (split at isStep)
        all_goals (cases isStep)
        all_goals simp [List.countP_cons, closeEvents_pubCount, Dex.Obs.isPubEvent]
      simpa [Dex.system, Dex.Request.isPublication] using isAtMostOne
  | _ =>
    simp only [Dex.step] at isStep
    repeat' (split at isStep)
    all_goals (cases isStep)
    all_goals simp [Dex.Config.commit, closeEvents_pubCount, Dex.Obs.isPubEvent]

/-- A finite Dex run has at most as many `pub` events as publication requests. -/
theorem exec_pubCount (flow : Dex.FlowDef σ) (labels : List (Dex.Action σ))
    (config : Dex.Config σ) (events : List (Dex.Obs σ)) (final : Dex.Config σ)
    (isExec : (Dex.system flow).exec config labels = some (events, final)) :
    events.countP Dex.Obs.isPubEvent ≤
      (labels.filterMap (Dex.system flow).requestOf).countP Dex.Request.isPublication := by
  induction labels generalizing config events with
  | nil =>
    simp only [TransitionSystem.exec, Option.some.injEq, Prod.mk.injEq] at isExec
    rw [← isExec.1]
    simp
  | cons label laterLabels inductionHypothesis =>
    obtain ⟨stepEvents, next, laterEvents, isStep, isLater, isEvents⟩ :=
      (Dex.system flow).exec_cons config label laterLabels events final isExec
    have stepCount := step_pubCount flow config label stepEvents next isStep
    have laterCount := inductionHypothesis next laterEvents isLater
    rw [isEvents, List.countP_append, List.filterMap_cons]
    cases isRequest : (Dex.system flow).requestOf label with
    | none =>
      rw [isRequest] at stepCount
      simp only [Option.toList, List.countP_nil] at stepCount
      simp only
      omega
    | some request =>
      rw [isRequest] at stepCount
      simp only [Option.toList, List.countP_cons, List.countP_nil, Nat.zero_add] at stepCount ⊢
      omega

/-- **A failure-free trace has at most as many `pub` events as its requests
have publications.** -/
theorem freeTrace_pubCount_le (flow : Dex.FlowDef σ) (environment : Dex.Environment σ)
    (requests : List (Dex.Request σ)) (trace : List (Dex.Obs σ))
    (isFree : IsFreeFiniteTrace flow environment requests trace) :
    trace.countP Dex.Obs.isPubEvent ≤ requests.countP Dex.Request.isPublication := by
  obtain ⟨labels, final, isExec, isRequests, _⟩ := isFree
  rw [isRequests]
  exact exec_pubCount flow labels _ trace final isExec

end DexExpressiveness.DexF.Examples
