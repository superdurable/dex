import DexExpressiveness.Invariants
import DexExpressiveness.Dex.Observation

/-!
# Liveness, work package L5: facts about Dex runs alone

Two facts about the restricted Dex system that the Dex run of an L run needs
(`Liveness/LToDexRun.lean`), and one about finite runs of any system. None
mentions L or the translation.

- A finite run of `first ++ second` splits at the end of `first`.

- A closed run stays closed (spec §5.5: "closed" in the markers `✓` and `δ`
  means closed at some point). No meta-function of spec §4.2 reopens a run.
- Which tasks are `Returned` after a step: those that were, and the task of
  a [WORKER-EFFECT] return. A Complete removes its task.
-/

namespace DexExpressiveness

/-- A run of `first ++ second` splits into a run of `first` and a run of
`second` from where the first ends. The converse of `exec_append`. -/
theorem TransitionSystem.exec_of_append {State Label Event Request : Type}
    (system : TransitionSystem State Label Event Request) (state : State)
    (firstLabels secondLabels : List Label) (events : List Event) (final : State)
    (isExec : system.exec state (firstLabels ++ secondLabels) = some (events, final)) :
    ∃ firstEvents middle secondEvents,
      system.exec state firstLabels = some (firstEvents, middle) ∧
      system.exec middle secondLabels = some (secondEvents, final) ∧
      events = firstEvents ++ secondEvents := by
  induction firstLabels generalizing state events with
  | nil => exact ⟨[], state, events, rfl, by simpa using isExec, rfl⟩
  | cons label laterLabels inductionHypothesis =>
    obtain ⟨stepEvents, next, laterEvents, isStep, isLater, isEvents⟩ :=
      system.exec_cons state label (laterLabels ++ secondLabels) events final isExec
    obtain ⟨firstEvents, middle, secondEvents, isFirst, isSecond, isLaterEvents⟩ :=
      inductionHypothesis next laterEvents isLater
    refine ⟨stepEvents ++ firstEvents, middle, secondEvents, ?_, isSecond, ?_⟩
    · simp only [TransitionSystem.exec, isStep, isFirst]
    · rw [isEvents, isLaterEvents, List.append_assoc]

end DexExpressiveness

namespace DexExpressiveness.Dex

variable {σ : Signature}

namespace Run

/-! ## The meta-functions keep the status of a closed run -/

/-- `eval(e)` keeps the status. -/
theorem status_evalStep (run : Run σ) (step : StepExecId σ) :
    (run.evalStep step).status = run.status := by
  rcases evalStep_shape run step with isSame | ⟨chans, consumeFlags, change, _, isEval⟩
  · rw [isSame]
  · rw [isEval]
    exact (consumeMessages_frame chans consumeFlags (run.disarmTimersOf step)).2.2.2.2.1

/-- Evaluating Steps keeps the status. -/
theorem status_foldl_evalStep (steps : List (StepExecId σ)) (run : Run σ) :
    (steps.foldl Run.evalStep run).status = run.status := by
  induction steps generalizing run with
  | nil => rfl
  | cons step laterSteps inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [inductionHypothesis, status_evalStep]

/-- `wake` keeps the status. -/
theorem status_wake (run : Run σ) (changed : List σ.Chan) :
    (run.wake changed).status = run.status :=
  status_foldl_evalStep _ run

/-- `effects` keeps the status. -/
theorem status_applyEffects (run : Run σ) (effects : Effects σ) :
    (run.applyEffects effects).status = run.status := by
  unfold Run.applyEffects
  rw [status_wake, (applyPublications_frame _ _).2.2.2.2.1,
    (applyWrites_frame _ _).2.2.2.2.2.1]

/-- `register` keeps the status. -/
theorem status_registerWait (run : Run σ) (step : StepExecId σ) (wait : Wait σ) (now : Nat) :
    (run.registerWait step wait now).status = run.status := by
  unfold Run.registerWait
  split
  · rfl
  · exact status_evalStep _ step

/-- Firing one timer keeps the status. -/
theorem status_fireTimer (run : Run σ) (timer : TimerId σ) :
    (run.fireTimer timer).status = run.status := by
  unfold Run.fireTimer
  split
  · simp only
    split
    · rfl
    · split
      · split
        · rfl
        · exact status_evalStep _ timer.step
      · rfl
  · rfl

/-- Firing timers keeps the status. -/
theorem status_foldl_fireTimer (timers : List (TimerId σ)) (run : Run σ) :
    (timers.foldl Run.fireTimer run).status = run.status := by
  induction timers generalizing run with
  | nil => rfl
  | cons timer laterTimers inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [inductionHypothesis, status_fireTimer]

/-- Taking moves keeps the status. -/
theorem status_foldl_newStep (moves : List (Move σ)) (origin : Origin σ) (run : Run σ) :
    (moves.foldl (fun current move =>
      current.newStep move.stepType move.input move.options origin) run).status = run.status := by
  induction moves generalizing run with
  | nil => rfl
  | cons move laterMoves inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [inductionHypothesis]
    rfl

/-- `close` changes nothing in a closed run. -/
theorem closeRun_of_closed (run : Run σ) (status closedStatus : CloseStatus)
    (isClosed : run.status = .closed closedStatus) : run.closeRun status = run := by
  simp only [Run.closeRun, isClosed]

/-- `decide` keeps a closed run closed. -/
theorem status_applyDecision_of_closed (run : Run σ) (step : StepExecId σ)
    (decision : Decision σ) (closedStatus : CloseStatus)
    (isClosed : run.status = .closed closedStatus) :
    (run.applyDecision step decision).status = .closed closedStatus := by
  have isFinishedClosed : (run.finishStep step .completed).status = .closed closedStatus :=
    isClosed
  simp only [Run.applyDecision]
  split
  · rename_i output _
    have isCollectedClosed :
        ((run.finishStep step .completed).collectOutput step output).status =
          .closed closedStatus := by
      unfold Run.collectOutput
      split <;> exact isFinishedClosed
    rw [closeRun_of_closed _ _ closedStatus isCollectedClosed]
    exact isCollectedClosed
  · rw [closeRun_of_closed _ _ closedStatus isFinishedClosed]
    exact isFinishedClosed
  · exact isFinishedClosed
  · rw [status_foldl_newStep]
    exact isFinishedClosed

end Run

/-! ## A closed run stays closed -/

/-- The program is closed exactly when the Flow has a closed run. -/
theorem isClosed_iff (flow : FlowDef σ) (config : Config σ) :
    (system flow).isClosed config = true ↔
      ∃ run status, config.run = some run ∧ run.status = .closed status := by
  simp only [system]
  constructor
  · intro isClosed
    split at isClosed
    · cases isClosed
    · rename_i run hasRun
      split at isClosed
      · cases isClosed
      · rename_i status isStatus
        exact ⟨run, status, hasRun, isStatus⟩
  · rintro ⟨run, status, hasRun, isStatus⟩
    simp only [hasRun, isStatus]

/-- Every transition keeps a closed run closed (spec §5.5: "the root Flow is
closed" is stable). -/
theorem step_isClosed (flow : FlowDef σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step flow config action = some (events, next))
    (isClosed : (system flow).isClosed config = true) : (system flow).isClosed next = true := by
  obtain ⟨run, status, hasRun, isStatus⟩ := (isClosed_iff flow config).mp isClosed
  rw [isClosed_iff]
  cases action with
  | tick duration =>
    simp only [step] at isStep
    split at isStep <;> cases isStep
    exact ⟨run, status, hasRun, isStatus⟩
  | request request =>
    cases request with
    | startFlow stepType input => simp [step, hasRun] at isStep
    | publish chan value =>
      simp only [step, hasRun, isStatus] at isStep
      cases isStep
      exact ⟨run, status, hasRun, isStatus⟩
  | stepStart stepId =>
    simp only [step, hasRun] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         exact ⟨_, status, rfl, isStatus⟩)
  | workerRun taskId =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         exact ⟨run, status, hasRun, isStatus⟩)
  | workerEffect taskId =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         exact ⟨run, status, hasRun, isStatus⟩)
  | workerReturn taskId =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         exact ⟨run, status, hasRun, isStatus⟩)
  | workerAbandon taskId =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         exact ⟨run, status, hasRun, isStatus⟩)
  | stepCompleteStale taskId =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         exact ⟨run, status, hasRun, isStatus⟩)
  | stepComplete taskId =>
    simp only [step, hasRun] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         injections
         subst_vars
         refine ⟨_, status, rfl, ?_⟩
         dsimp only
         first
           | (rw [Run.status_registerWait, Run.status_applyEffects]
              exact isStatus)
           | (refine Run.status_applyDecision_of_closed _ _ _ _ ?_
              rw [Run.status_applyEffects]
              exact isStatus))
  | timerFire count =>
    simp only [step, hasRun] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         refine ⟨_, status, rfl, ?_⟩
         rw [Run.status_foldl_fireTimer]
         exact isStatus)

/-- A finite run keeps a closed run closed. -/
theorem exec_isClosed (flow : FlowDef σ) (actions : List (Action σ)) (config : Config σ)
    (events : List (Obs σ)) (final : Config σ)
    (isExec : (system flow).exec config actions = some (events, final))
    (isClosed : (system flow).isClosed config = true) : (system flow).isClosed final = true :=
  TransitionSystem.invariant_of_exec (system flow)
    (fun state => (system flow).isClosed state = true)
    (fun state label stepEvents next holds isStep =>
      step_isClosed flow state label stepEvents next isStep holds)
    actions config events final isClosed isExec

/-! ## The `Returned` tasks after a step -/

/-- No task is `Returned`, except possibly the task with the ID `exempt`. -/
def Config.HasNoReturnedTaskExcept (config : Config σ) (exempt : Option Nat) : Prop :=
  ∀ task, task ∈ config.tasks → ∀ response, task.phase = .returned response →
    exempt = some task.id

/-- A task that is `Returned` after a step was `Returned` before it and the
step is not its Complete, or the step is its [WORKER-EFFECT] return. -/
theorem step_returnedTask (flow : FlowDef σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step flow config action = some (events, next))
    (task : Task σ) (isMember : task ∈ next.tasks) (response : Response σ)
    (isReturned : task.phase = .returned response) :
    action = .workerReturn task.id ∨
    ((∃ original earlier, original ∈ config.tasks ∧ original.id = task.id ∧
        original.phase = .returned earlier) ∧
      action ≠ .stepComplete task.id) := by
  have unchanged : next.tasks = config.tasks → (∀ taskId, action ≠ .stepComplete taskId) →
      action = .workerReturn task.id ∨
      ((∃ original earlier, original ∈ config.tasks ∧ original.id = task.id ∧
          original.phase = .returned earlier) ∧
        action ≠ .stepComplete task.id) := by
    intro sameTasks isNotComplete
    exact Or.inr ⟨⟨task, response, sameTasks ▸ isMember, rfl, isReturned⟩, isNotComplete task.id⟩
  have removed : ∀ taskId, next.tasks = (config.removeTask taskId).tasks →
      (∀ completed, action = .stepComplete completed → completed = taskId) →
      action = .workerReturn task.id ∨
      ((∃ original earlier, original ∈ config.tasks ∧ original.id = task.id ∧
          original.phase = .returned earlier) ∧
        action ≠ .stepComplete task.id) := by
    intro taskId isRemoved isCompleteOf
    rw [isRemoved] at isMember
    obtain ⟨isOriginalMember, isOther⟩ := List.mem_filter.mp isMember
    refine Or.inr ⟨⟨task, response, isOriginalMember, rfl, isReturned⟩, ?_⟩
    intro isComplete
    have isSame := isCompleteOf task.id isComplete
    simp [isSame] at isOther
  cases action with
  | tick duration =>
    simp only [step] at isStep
    split at isStep <;> cases isStep
    exact unchanged rfl (fun _ => nofun)
  | request request =>
    cases request with
    | startFlow stepType input =>
      simp only [step] at isStep
      repeat' split at isStep
      all_goals first
        | (cases isStep; done)
        | (cases isStep
           exact unchanged rfl (fun _ => nofun))
    | publish chan value =>
      simp only [step] at isStep
      repeat' split at isStep
      all_goals first
        | (cases isStep; done)
        | (cases isStep
           exact unchanged rfl (fun _ => nofun))
  | stepStart stepId =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         rcases List.mem_append.mp isMember with isOriginalMember | isNew
         · exact Or.inr ⟨⟨task, response, isOriginalMember, rfl, isReturned⟩,
             nofun⟩
         · rw [List.mem_singleton.mp isNew] at isReturned
           cases isReturned)
  | workerRun taskId =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         obtain ⟨original, isOriginalMember, isTask⟩ := List.mem_map.mp isMember
         by_cases isChanged : original.id = taskId
         · rw [ite_eq_left_of_eq_true _ _ (eq_true isChanged)] at isTask
           rw [← isTask] at isReturned
           cases isReturned
         · rw [ite_eq_right_of_eq_false _ _ (eq_false isChanged)] at isTask
           subst isTask
           exact Or.inr ⟨⟨original, response, isOriginalMember, rfl, isReturned⟩,
             nofun⟩)
  | workerEffect taskId =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         obtain ⟨original, isOriginalMember, isTask⟩ := List.mem_map.mp isMember
         by_cases isChanged : original.id = taskId
         · rw [ite_eq_left_of_eq_true _ _ (eq_true isChanged)] at isTask
           rw [← isTask] at isReturned
           cases isReturned
         · rw [ite_eq_right_of_eq_false _ _ (eq_false isChanged)] at isTask
           subst isTask
           exact Or.inr ⟨⟨original, response, isOriginalMember, rfl, isReturned⟩,
             nofun⟩)
  | workerReturn taskId =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         obtain ⟨original, isOriginalMember, isTask⟩ := List.mem_map.mp isMember
         by_cases isChanged : original.id = taskId
         · rw [ite_eq_left_of_eq_true _ _ (eq_true isChanged)] at isTask
           left
           rw [← isTask, isChanged]
         · rw [ite_eq_right_of_eq_false _ _ (eq_false isChanged)] at isTask
           subst isTask
           exact Or.inr ⟨⟨original, response, isOriginalMember, rfl, isReturned⟩,
             nofun⟩)
  | workerAbandon taskId =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         exact removed taskId rfl (by intro completed isComplete; cases isComplete))
  | stepCompleteStale taskId =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         exact removed taskId rfl (by intro completed isComplete; cases isComplete))
  | stepComplete taskId =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         exact removed taskId rfl (by intro completed isComplete; cases isComplete; rfl))
  | timerFire count =>
    simp only [step] at isStep
    repeat' split at isStep
    all_goals first
      | (cases isStep; done)
      | (cases isStep
         exact unchanged rfl (fun _ => nofun))

/-- A step other than a [WORKER-EFFECT] return adds no `Returned` task. -/
theorem Config.HasNoReturnedTaskExcept.step {config : Config σ} {exempt : Option Nat}
    (holds : config.HasNoReturnedTaskExcept exempt) (flow : FlowDef σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : Dex.step flow config action = some (events, next))
    (isNotReturn : ∀ taskId, action ≠ .workerReturn taskId) :
    next.HasNoReturnedTaskExcept exempt := by
  intro task isMember response isReturned
  rcases step_returnedTask flow config action events next isStep task isMember response
    isReturned with isReturn | ⟨⟨original, earlier, isOriginalMember, sameId, wasReturned⟩, _⟩
  · exact absurd isReturn (isNotReturn task.id)
  · rw [← sameId]
    exact holds original isOriginalMember earlier wasReturned

/-- The return of a task leaves it as the only `Returned` task. -/
theorem Config.HasNoReturnedTaskExcept.workerReturn {config : Config σ}
    (holds : config.HasNoReturnedTaskExcept none) (flow : FlowDef σ) (taskId : Nat)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : Dex.step flow config (.workerReturn taskId) = some (events, next)) :
    next.HasNoReturnedTaskExcept (some taskId) := by
  intro task isMember response isReturned
  rcases step_returnedTask flow config _ events next isStep task isMember response
    isReturned with isReturn | ⟨⟨original, earlier, isOriginalMember, _, wasReturned⟩, _⟩
  · injection isReturn with sameId
    rw [sameId]
  · cases holds original isOriginalMember earlier wasReturned

/-- The Complete of the only `Returned` task leaves none. -/
theorem Config.HasNoReturnedTaskExcept.stepComplete {config : Config σ} {taskId : Nat}
    (holds : config.HasNoReturnedTaskExcept (some taskId)) (flow : FlowDef σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : Dex.step flow config (.stepComplete taskId) = some (events, next)) :
    next.HasNoReturnedTaskExcept none := by
  intro task isMember response isReturned
  rcases step_returnedTask flow config _ events next isStep task isMember response
    isReturned with isReturn | ⟨⟨original, earlier, isOriginalMember, sameId, wasReturned⟩,
      isNotComplete⟩
  · cases isReturn
  · have isExempt := holds original isOriginalMember earlier wasReturned
    injection isExempt with isTaskId
    exact absurd (by rw [isTaskId, sameId]) isNotComplete

/-- A finite run without a [WORKER-EFFECT] return adds no `Returned` task. -/
theorem Config.HasNoReturnedTaskExcept.exec {exempt : Option Nat} (flow : FlowDef σ)
    (actions : List (Action σ)) (config : Config σ) (events : List (Obs σ)) (final : Config σ)
    (holds : config.HasNoReturnedTaskExcept exempt)
    (isExec : (system flow).exec config actions = some (events, final))
    (isNotReturn : ∀ action, action ∈ actions → ∀ taskId, action ≠ .workerReturn taskId) :
    final.HasNoReturnedTaskExcept exempt := by
  induction actions generalizing config events with
  | nil =>
    simp only [TransitionSystem.exec] at isExec
    cases isExec
    exact holds
  | cons action laterActions inductionHypothesis =>
    obtain ⟨stepEvents, next, laterEvents, isStep, isLater, _⟩ :=
      (system flow).exec_cons config action laterActions events final isExec
    exact inductionHypothesis next laterEvents
      (holds.step flow action stepEvents next isStep
        (isNotReturn action (List.mem_cons_self ..)))
      isLater (fun later isLaterMember => isNotReturn later (List.mem_cons_of_mem _ isLaterMember))

end DexExpressiveness.Dex
