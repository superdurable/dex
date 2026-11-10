import DexExpressiveness.Simulation.LToDex
import DexExpressiveness.Simulation.DexToLLemmas

/-!
# Dex to L: the steps that change the run while the Flow runs

Work package 4. These lemmas show `IsSimulatedByL` for the Dex steps of
`IsObservedWhileRunning` (`Simulation/DexToL.lean`): the start of the Flow,
and, while the Flow runs, a publication, [STEP-START], [WORKER-EFFECT], a
Step completion, and [TIMER-FIRE].

Each proof has the same four parts:

1. the L label of the step is enabled;
2. `l_step_simulatedByActions` simulates that label by `simulatingActions`;
3. `simulatingActions` is the given action alone;
4. `Dex.step` is a function, so the given step is the simulating step
   (`wp4_simulated_of_single_action`).

No proof here rebuilds the relation. They depend on the L to Dex lemmas of
work packages 2 and 3 through `l_step_simulatedByActions`.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## From a simulated label to a simulated action -/

/-- A Dex step is simulated by L when it is the only simulating action of an
enabled L label: the step that `l_step_simulatedByActions` builds is the given
step, because `Dex.step` is a function. -/
theorem wp4_simulated_of_single_action (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (label : L.Label) (lEvents : List Event) (lNext : L.Config)
    (isLStep : (L.system program).step lConfig label = some (lEvents, lNext))
    (action : Dex.Action signature) (dexEvents : List (Dex.Obs signature))
    (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig action = some (dexEvents, dexNext))
    (isActions : simulatingActions program lConfig dexConfig label = [action]) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  obtain ⟨simulatedEvents, simulatedNext, isExec, sameEvents, isRelated⟩ :=
    l_step_simulatedByActions program lConfig dexConfig state label lEvents lNext isLStep
  rw [isActions] at isExec
  simp only [TransitionSystem.exec, isStep, List.append_nil, Option.some.injEq,
    Prod.mk.injEq] at isExec
  obtain ⟨rfl, rfl⟩ := isExec
  refine ⟨[label], lNext, Nat.le_refl 1, ?_, isRelated⟩
  simp only [TransitionSystem.exec, isLStep, List.append_nil, sameEvents]

/-! ## Requests -/

/-- [FLOW-START] is simulated by `begin(v)`. -/
theorem dex_startFlow_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (stepType : StepType) (input : DexVal)
    (isAdmissible : IsAdmissibleAction dexConfig (.request (.startFlow stepType input)))
    (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig (.request (.startFlow stepType input)) =
      some (dexEvents, dexNext)) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  obtain ⟨rfl, literal, rfl⟩ := isAdmissible
  have isEnabled : dexConfig.run = none ∧ dexConfig.canCommit = true := by
    simp only [Dex.system, Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i hasNoRun
      split at isStep
      · rename_i canCommit
        exact ⟨hasNoRun, canCommit⟩
      · cases isStep
  have lStatus := Related.status_of_no_run state.isRelated isEnabled.1
  have canStamp := Related.canStamp_of_canCommit state.isRelated isEnabled.2
  have isLStep : (L.system program).step lConfig (.request (.start literal)) =
      some ([Event.begin literal.toVal], (lConfig.begun program literal.toVal).stamp) := by
    simp [L.system, L.step, lStatus, canStamp]
  exact wp4_simulated_of_single_action program lConfig dexConfig state _ _ _ isLStep _ _ _ isStep
    rfl

/-- While the Flow runs, [PUBLISH] is simulated by `in(c, v)`. -/
theorem dex_publish_running_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (run : Dex.Run signature) (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running) (chan : Chan) (message : DexVal)
    (isAdmissible : IsAdmissibleAction dexConfig (.request (.publish chan message)))
    (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig (.request (.publish chan message)) =
      some (dexEvents, dexNext)) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  obtain ⟨_, externalChan, literal, rfl, rfl⟩ := isAdmissible
  have canCommit : dexConfig.canCommit = true := by
    simp only [Dex.system, Dex.step, hasRun, isRunning] at isStep
    split at isStep
    · rename_i canCommit
      exact canCommit
    · cases isStep
  have lStatus := Related.status_of_running_run state.isRelated run hasRun isRunning
  have canStamp := Related.canStamp_of_canCommit state.isRelated canCommit
  have isLStep : (L.system program).step lConfig (.request (.input externalChan literal)) =
      some ([Event.input externalChan literal.toVal],
        (lConfig.deliverInput externalChan literal.toVal).stamp) := by
    simp [L.system, L.step, lStatus, canStamp]
  exact wp4_simulated_of_single_action program lConfig dexConfig state _ _ _ isLStep _ _ _ isStep
    rfl

/-! ## The thread of a Step and of a task -/

/-- A thread whose Step has a pending invocation has not begun its next action. -/
theorem wp4_pending_of_invocation {program : Program} {lConfig : L.Config}
    {config : Dex.Config signature} {thread : L.Thread} {record : Dex.StepRecord signature}
    {method : Dex.Method} (phaseMatches : PhaseMatchesStep program lConfig config thread record)
    (hasInvocation : record.invocation = some { method := method, state := .pending }) :
    thread.phase = .bootPending ∨ thread.phase = .ready ∨
      ∃ outcome, thread.phase = .woken outcome := by
  cases phaseCase : thread.phase <;> simp only [PhaseMatchesStep, phaseCase] at phaseMatches
  all_goals first
    | exact Or.inl rfl
    | exact Or.inr (Or.inl rfl)
    | exact Or.inr (Or.inr ⟨_, rfl⟩)
    | (obtain ⟨_, _, _, isInvocation, _⟩ := phaseMatches
       rw [hasInvocation] at isInvocation
       cases isInvocation)
    | (have isNone := phaseMatches.2.1
       rw [hasInvocation] at isNone
       cases isNone)

/-- The task that runs a Step's invocation is the task of `HasRunningTask`. -/
theorem wp4_holds_of_hasRunningTask {config : Dex.Config signature}
    {record : Dex.StepRecord signature} {method other : Dex.Method}
    {Holds : Dex.Task signature → Prop} (tasksDistinct : TasksAreDistinct config)
    {task : Dex.Task signature} (isTask : task ∈ config.tasks)
    (hasInvocation : record.invocation = some { method := other, state := .running task.id })
    (hasTask : HasRunningTask config record method Holds) : Holds task := by
  obtain ⟨running, isRunning, isInvocation, _, _, _, holds⟩ := hasTask
  rw [hasInvocation] at isInvocation
  simp only [Option.some.injEq, Dex.Invocation.mk.injEq,
    Dex.InvocationState.running.injEq] at isInvocation
  rw [eq_of_nodup_map (fun candidate => candidate.id) config.tasks tasksDistinct.1 isTask
    isRunning isInvocation.2]
  exact holds

/-- The phase of a thread whose Step runs as a given task: the thread emits
the one effect the task holds, or the task holds none and the thread is about
to complete its action. -/
theorem wp4_phase_of_running_task {program : Program} {lConfig : L.Config}
    {config : Dex.Config signature} {thread : L.Thread} {record : Dex.StepRecord signature}
    (tasksDistinct : TasksAreDistinct config)
    (phaseMatches : PhaseMatchesStep program lConfig config thread record)
    (task : Dex.Task signature) (isTask : task ∈ config.tasks) (method : Dex.Method)
    (hasInvocation : record.invocation = some { method := method, state := .running task.id }) :
    (∃ effect, thread.phase = .emitting effect ∧ pendingEffects ⟦program⟧ task = [effect]) ∨
    (pendingEffects ⟦program⟧ task = [] ∧
      (thread.phase = .bootRunning ∨ (∃ loaded, thread.phase = .running loaded) ∨
        thread.phase = .emitted ∨ thread.phase = .waitStarted ∨
        ∃ outcome, thread.phase = .resuming outcome)) := by
  cases phaseCase : thread.phase <;> simp only [PhaseMatchesStep, phaseCase] at phaseMatches
  case bootRunning =>
    exact Or.inr ⟨wp4_holds_of_hasRunningTask tasksDistinct isTask hasInvocation phaseMatches.2,
      Or.inl rfl⟩
  case running loaded =>
    exact Or.inr ⟨(wp4_holds_of_hasRunningTask tasksDistinct isTask hasInvocation
      phaseMatches.2).1, Or.inr (Or.inl ⟨loaded, rfl⟩)⟩
  case emitting effect =>
    exact Or.inl ⟨effect, rfl,
      (wp4_holds_of_hasRunningTask tasksDistinct isTask hasInvocation phaseMatches.2).1⟩
  case emitted =>
    exact Or.inr ⟨(wp4_holds_of_hasRunningTask tasksDistinct isTask hasInvocation
      phaseMatches.2).1, Or.inr (Or.inr (Or.inl rfl))⟩
  case waitStarted =>
    exact Or.inr ⟨wp4_holds_of_hasRunningTask tasksDistinct isTask hasInvocation phaseMatches.2,
      Or.inr (Or.inr (Or.inr (Or.inl rfl)))⟩
  case resuming outcome =>
    exact Or.inr ⟨(wp4_holds_of_hasRunningTask tasksDistinct isTask hasInvocation
      phaseMatches.2).1, Or.inr (Or.inr (Or.inr (Or.inr ⟨outcome, rfl⟩)))⟩
  case bootPending =>
    have isPending := phaseMatches.2
    rw [hasInvocation] at isPending
    cases isPending
  case ready =>
    have isPending := phaseMatches.2.2
    rw [hasInvocation] at isPending
    cases isPending
  case blocked on since =>
    have isNone := phaseMatches.2.1
    rw [hasInvocation] at isNone
    cases isNone
  case woken outcome =>
    have isPending := phaseMatches.2.1
    rw [hasInvocation] at isPending
    cases isPending

/-- A current task runs the invocation of its Step. -/
theorem wp4_record_of_current (config : Dex.Config signature) (run : Dex.Run signature)
    (hasRun : config.run = some run) (task : Dex.Task signature)
    (isCurrent : config.isTaskCurrent task = true) :
    ∃ record, run.findStep? task.step = some record ∧
      record.invocation = some { method := task.method, state := .running task.id } := by
  simp only [Dex.Config.isTaskCurrent, hasRun] at isCurrent
  split at isCurrent
  · cases isCurrent
  · rename_i record isFound
    exact ⟨record, isFound, by simpa using isCurrent⟩

/-- While the Flow runs, every task runs the invocation of an active Step
that carries a thread, and the thread's phase matches that Step. -/
theorem wp4_thread_of_task {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (state : IsSimulationState program lConfig dexConfig)
    (run : Dex.Run signature) (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running) (task : Dex.Task signature)
    (isTask : task ∈ dexConfig.tasks) :
    ∃ record thread, record ∈ run.steps ∧ record.phase.isActive = true ∧
      thread ∈ lConfig.threads ∧
      record.invocation = some { method := task.method, state := .running task.id } ∧
      threadOfInput record.input = thread.core.tid ∧
      PhaseMatchesStep program lConfig dexConfig thread record := by
  have related := state.isRelated.running run hasRun isRunning
  obtain ⟨record, isFoundStep, hasInvocation⟩ :=
    wp4_record_of_current dexConfig run hasRun task (related.tasksAreCurrent task isTask)
  obtain ⟨isRecord, _⟩ := Dex.Run.mem_of_findStep? run task.step record isFoundStep
  have isActive := related.invocationsAreActive record isRecord (by rw [hasInvocation]; simp)
  obtain ⟨thread, isThread, isLive, carries⟩ := related.stepsHaveThreads record isRecord isActive
  exact ⟨record, thread, isRecord, isActive, isThread, hasInvocation,
    threadOfInput_of_mem state.lInvariant isThread carries,
    related.phaseMatchesStep_of_carries thread isThread isLive record isRecord isActive carries⟩

/-! ## The L thread step is enabled -/

/-- A thread that has not begun its next action can begin it when the clock
allows a stamped action and the thread holds the mutex its statement requires. -/
theorem wp4_stepThread_begin_enabled (program : Program) (lConfig : L.Config)
    (thread : L.Thread) (canStamp : lConfig.canStamp = true)
    (isPending : thread.phase = .bootPending ∨ thread.phase = .ready ∨
      ∃ outcome, thread.phase = .woken outcome)
    (holdsMutex : thread.phase = .ready →
      (thread.timer.isSome || isBlockingHead thread.code) = false →
      lConfig.holdsRequiredMutex thread = true) :
    ∃ result, stepThread program lConfig thread = some result := by
  rcases isPending with isPhase | isPhase | ⟨outcome, isPhase⟩
  · simp [stepThread, isPhase, canStamp]
  · cases waitsCase : (thread.timer.isSome || isBlockingHead thread.code) with
    | true => simp [stepThread, isPhase, canStamp, waitsCase]
    | false =>
      have holds := holdsMutex isPhase waitsCase
      cases beginCase : beginKind thread.core thread.code <;>
        simp [stepThread, isPhase, canStamp, waitsCase, holds, beginCase]
  · simp [stepThread, isPhase, canStamp]

/-- A thread that began an action can complete it when the clock allows a
stamped action. -/
theorem wp4_stepThread_complete_enabled (program : Program) (lConfig : L.Config)
    (thread : L.Thread) (canStamp : lConfig.canStamp = true)
    (isCompleting : thread.phase = .bootRunning ∨ (∃ loaded, thread.phase = .running loaded) ∨
      thread.phase = .emitted ∨ thread.phase = .waitStarted ∨
      ∃ outcome, thread.phase = .resuming outcome) :
    ∃ result, stepThread program lConfig thread = some result := by
  rcases isCompleting with isPhase | ⟨loaded, isPhase⟩ | isPhase | isPhase | ⟨outcome, isPhase⟩ <;>
    simp [stepThread, isPhase, canStamp]

/-! ## Step methods -/

/-- While the Flow runs, [STEP-START] is simulated by the begin of the action
of the thread that the Step carries. -/
theorem dex_stepStart_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (breaksNoRequirement : ¬ BreaksMutexRequirement lConfig)
    (run : Dex.Run signature) (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running) (stepId : Dex.StepExecId signature)
    (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig (.stepStart stepId) =
      some (dexEvents, dexNext)) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  have related := state.isRelated.running run hasRun isRunning
  obtain ⟨record, method, isFoundStep, hasInvocation, canCommit⟩ :
      ∃ record method, run.findStep? stepId = some record ∧
        record.invocation = some { method := method, state := .pending } ∧
        dexConfig.canCommit = true := by
    simp only [Dex.system, Dex.step, hasRun] at isStep
    split at isStep
    · cases isStep
    · rename_i record isFoundStep
      split at isStep
      · rename_i method hasInvocation
        split at isStep
        · rename_i canCommit
          exact ⟨record, method, isFoundStep, hasInvocation, canCommit⟩
        · cases isStep
      · cases isStep
  obtain ⟨isRecord, isId⟩ := Dex.Run.mem_of_findStep? run stepId record isFoundStep
  have isActive := related.invocationsAreActive record isRecord (by rw [hasInvocation]; simp)
  obtain ⟨thread, isThread, isLive, carries⟩ := related.stepsHaveThreads record isRecord isActive
  have phaseMatches :=
    related.phaseMatchesStep_of_carries thread isThread isLive record isRecord isActive carries
  have isPending := wp4_pending_of_invocation phaseMatches hasInvocation
  have isFound := findThread?_of_mem state.lInvariant isThread
  have isActions := simulatingActions_begin (program := program) related hasRun isFound record
    isRecord isActive (threadOfInput_of_mem state.lInvariant isThread carries) isPending
  rw [isId] at isActions
  have canStamp := Related.canStamp_of_canCommit state.isRelated canCommit
  obtain ⟨⟨lEvents, lNext⟩, isThreadStep⟩ :=
    wp4_stepThread_begin_enabled program lConfig thread canStamp isPending
      (fun isReady isNotWaiting => by
        have hasNoTimer : thread.timer = none := by
          cases timerCase : thread.timer <;> simp [timerCase] at isNotWaiting ⊢
        unfold L.Config.holdsRequiredMutex
        split
        · rfl
        · rename_i mutex requiredCase
          apply decide_eq_true
          apply Classical.byContradiction
          intro isNotOwner
          apply breaksNoRequirement
          unfold BreaksMutexRequirement Thread.BreaksMutexRequirement
          exact ⟨thread, isThread, isReady, hasNoTimer, mutex, requiredCase, isNotOwner⟩)
  have isLStep : (L.system program).step lConfig (.thread thread.core.tid) =
      some (lEvents, lNext) := by
    simp only [L.system, L.step, isFound]
    exact isThreadStep
  exact wp4_simulated_of_single_action program lConfig dexConfig state _ _ _ isLStep _ _ _ isStep
    isActions

/-- While the Flow runs, [WORKER-EFFECT] is simulated by `out(a)` of the
thread whose Step the task runs. -/
theorem dex_workerEffect_running_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (run : Dex.Run signature) (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running) (taskId : Nat)
    (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig (.workerEffect taskId) =
      some (dexEvents, dexNext)) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  have related := state.isRelated.running run hasRun isRunning
  obtain ⟨task, effect, laterEffects, response, isFoundTask, isPhase⟩ :
      ∃ task effect laterEffects response, dexConfig.findTask? taskId = some task ∧
        task.phase = .running (effect :: laterEffects) response := by
    simp only [Dex.system, Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i task isFoundTask
      split at isStep
      · rename_i effect laterEffects response isPhase
        exact ⟨task, effect, laterEffects, response, isFoundTask, isPhase⟩
      · cases isStep
  obtain ⟨isTask, isId⟩ := mem_of_findTask? dexConfig taskId task isFoundTask
  obtain ⟨record, thread, isRecord, isActive, isThread, hasInvocation, isThreadInput,
    phaseMatches⟩ := wp4_thread_of_task state run hasRun isRunning task isTask
  have isEffects : pendingEffects ⟦program⟧ task = effect :: laterEffects := by
    simp [pendingEffects, isPhase]
  rcases wp4_phase_of_running_task state.tasksDistinct phaseMatches task isTask task.method
    hasInvocation with ⟨emitted, isEmitting, _⟩ | ⟨hasNoEffect, _⟩
  · have isFound := findThread?_of_mem state.lInvariant isThread
    have isActions := simulatingActions_deliver (program := program) related hasRun
      state.tasksDistinct isFound record isRecord isActive isThreadInput task.method task isTask
      hasInvocation emitted isEmitting
    have hasRun' : runTaskActions task = [] := by simp [runTaskActions, isPhase]
    rw [hasRun', isId, List.nil_append] at isActions
    have isLStep : (L.system program).step lConfig (.thread thread.core.tid) =
        some ([Event.out emitted], lConfig.setPhase thread.core.tid .emitted) := by
      simp [L.system, L.step, isFound, stepThread, isEmitting]
    exact wp4_simulated_of_single_action program lConfig dexConfig state _ _ _ isLStep _ _ _
      isStep isActions
  · rw [isEffects] at hasNoEffect
    cases hasNoEffect

/-- While the Flow runs, a Step completion is simulated by the completion of
the action of the thread whose Step the task runs. -/
theorem dex_stepComplete_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (run : Dex.Run signature) (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running) (taskId : Nat)
    (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig (.stepComplete taskId) =
      some (dexEvents, dexNext)) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  have related := state.isRelated.running run hasRun isRunning
  obtain ⟨task, response, isFoundTask, isPhase, canCommit⟩ :
      ∃ task response, dexConfig.findTask? taskId = some task ∧
        task.phase = .returned response ∧ dexConfig.canCommit = true := by
    have isStepCopy := isStep
    simp only [Dex.system, Dex.step] at isStepCopy
    split at isStepCopy
    · rename_i stepRun task _ isFoundTask
      split at isStepCopy
      · rename_i isEnabled
        have canCommit : dexConfig.canCommit = true := by
          simp only [Bool.and_eq_true] at isEnabled
          exact isEnabled.2
        split at isStepCopy
        · rename_i waitForResponse _ isPhase
          exact ⟨task, _, isFoundTask, isPhase, canCommit⟩
        · rename_i executeResponse _ isPhase
          exact ⟨task, _, isFoundTask, isPhase, canCommit⟩
        · cases isStepCopy
      · cases isStepCopy
    · cases isStepCopy
  obtain ⟨isTask, isId⟩ := mem_of_findTask? dexConfig taskId task isFoundTask
  obtain ⟨record, thread, isRecord, isActive, isThread, hasInvocation, isThreadInput,
    phaseMatches⟩ := wp4_thread_of_task state run hasRun isRunning task isTask
  have isEffects : pendingEffects ⟦program⟧ task = [] := by simp [pendingEffects, isPhase]
  rcases wp4_phase_of_running_task state.tasksDistinct phaseMatches task isTask task.method
    hasInvocation with ⟨emitted, _, hasEffect⟩ | ⟨_, isCompleting⟩
  · rw [isEffects] at hasEffect
    cases hasEffect
  · have isFound := findThread?_of_mem state.lInvariant isThread
    have isActions := simulatingActions_complete (program := program) related hasRun
      state.tasksDistinct isFound record isRecord isActive isThreadInput task.method task isTask
      hasInvocation isCompleting
    have hasReturned : returnTaskActions task = [] := by simp [returnTaskActions, isPhase]
    rw [hasReturned, isId, List.nil_append] at isActions
    have canStamp := Related.canStamp_of_canCommit state.isRelated canCommit
    obtain ⟨⟨lEvents, lNext⟩, isThreadStep⟩ :=
      wp4_stepThread_complete_enabled program lConfig thread canStamp isCompleting
    have isLStep : (L.system program).step lConfig (.thread thread.core.tid) =
        some (lEvents, lNext) := by
      simp only [L.system, L.step, isFound]
      exact isThreadStep
    exact wp4_simulated_of_single_action program lConfig dexConfig state _ _ _ isLStep _ _ _
      isStep isActions

/-! ## [TIMER-FIRE] -/

/-- A list without duplicates that an injective function maps into another
list is at most as long. -/
theorem wp4_length_le_of_injOn {α β : Type} (image : α → β) :
    ∀ (elements : List α) (targets : List β), elements.Nodup →
      (∀ first, first ∈ elements → ∀ second, second ∈ elements →
        image first = image second → first = second) →
      (∀ element, element ∈ elements → image element ∈ targets) →
      elements.length ≤ targets.length := by
  intro elements
  induction elements with
  | nil => exact fun _ _ _ _ => Nat.zero_le _
  | cons head rest inductionHypothesis =>
    intro targets isNodup isInjective mapsInto
    obtain ⟨headIsFresh, restNodup⟩ := List.nodup_cons.mp isNodup
    obtain ⟨before, after, rfl⟩ := List.append_of_mem (mapsInto head List.mem_cons_self)
    have restLength := inductionHypothesis (before ++ after) restNodup
      (fun first isFirst second isSecond sameImage =>
        isInjective first (List.mem_cons_of_mem _ isFirst) second (List.mem_cons_of_mem _ isSecond)
          sameImage)
      (fun element isMember => by
        have isTarget := mapsInto element (List.mem_cons_of_mem _ isMember)
        rcases List.mem_append.mp isTarget with isBefore | isAfter
        · exact List.mem_append_left _ isBefore
        · rcases List.mem_cons.mp isAfter with isHead | isLater
          · have isSame := isInjective element (List.mem_cons_of_mem _ isMember) head
              List.mem_cons_self isHead
            exact absurd (isSame ▸ isMember) headIsFresh
          · exact List.mem_append_right _ isLater)
    simp only [List.length_append, List.length_cons] at restLength ⊢
    omega

/-- Changing one thread removes at most one entry of a list computed from the threads. -/
theorem wp4_filterMap_length_update {γ : Type} (candidateOf : L.Thread → Option γ)
    (change : L.Thread → L.Thread) (tid : Tid) :
    ∀ threads : List L.Thread, (threads.map (fun thread => thread.core.tid)).Nodup →
      (threads.filterMap candidateOf).length ≤
        ((threads.map (fun candidate =>
          if candidate.core.tid = tid then change candidate else candidate)).filterMap
            candidateOf).length + 1 := by
  intro threads
  induction threads with
  | nil => exact fun _ => Nat.zero_le _
  | cons head rest inductionHypothesis =>
    intro isNodup
    rw [List.map_cons, List.nodup_cons] at isNodup
    obtain ⟨headIsFresh, restNodup⟩ := isNodup
    by_cases isHead : head.core.tid = tid
    · have keepsRest : rest.map (fun candidate =>
          if candidate.core.tid = tid then change candidate else candidate) = rest := by
        have isSame : ∀ candidate, candidate ∈ rest →
            (if candidate.core.tid = tid then change candidate else candidate) = id candidate := by
          intro candidate isMember
          have differs : candidate.core.tid ≠ tid := fun sameTid =>
            headIsFresh (isHead ▸ sameTid ▸
              List.mem_map_of_mem (f := fun thread => thread.core.tid) isMember)
          simp [differs]
        rw [List.map_congr_left isSame, List.map_id]
      rw [List.map_cons, keepsRest, List.filterMap_cons, List.filterMap_cons]
      cases candidateOf head <;>
        cases candidateOf (if head.core.tid = tid then change head else head) <;>
        simp <;> omega
    · have restLength := inductionHypothesis restNodup
      rw [List.map_cons, List.filterMap_cons, List.filterMap_cons]
      simp only [isHead, ↓reduceIte]
      cases candidateOf head <;> simp only [List.length_cons] <;> omega

/-- L can fire as many timers in a row as it has due timers. -/
theorem wp4_fireTimers_enabled (program : Program) :
    ∀ (count : Nat) (config : L.Config), L.Invariant program config →
      count ≤ (wp2_dueCandidates config).length → ∃ final, config.fireTimers count = some final := by
  intro count
  induction count with
  | zero => exact fun config _ _ => ⟨config, rfl⟩
  | succ count inductionHypothesis =>
    intro config invariant isEnough
    have sortedLength := (wp2_sortStable_perm wp2_isDueAtMost (wp2_dueCandidates config)).length_eq
    cases sortedCase : sortStable wp2_isDueAtMost (wp2_dueCandidates config) with
    | nil =>
      rw [sortedCase] at sortedLength
      simp only [List.length_nil] at sortedLength
      omega
    | cons entry laterEntries =>
      have isCandidate : entry ∈ wp2_dueCandidates config :=
        (mem_sortStable wp2_isDueAtMost entry _).mp (sortedCase ▸ List.mem_cons_self)
      obtain ⟨deadline, number, tid⟩ := entry
      obtain ⟨thread, chan, since, isMember, isBlocked, _, rfl⟩ :=
        (wp2_mem_dueCandidates config deadline number tid).mp isCandidate
      have isFound := findThread?_of_mem invariant isMember
      have nextInvariant := wp2_invariant_wake_fired invariant isFound isBlocked
      have nextLength : (wp2_dueCandidates config).length ≤
          (wp2_dueCandidates (config.setPhase thread.core.tid (.woken .fired))).length + 1 :=
        wp4_filterMap_length_update _ _ thread.core.tid config.threads invariant.threadIdsNodup
      obtain ⟨final, isFired⟩ := inductionHypothesis
        (config.setPhase thread.core.tid (.woken .fired)) nextInvariant (by omega)
      refine ⟨final, ?_⟩
      simp only [L.Config.fireTimers, wp2_leastDueTimerThread_eq, sortedCase, List.head?_cons,
        Option.map_some, L.Config.wakeThread]
      exact isFired

/-- L has at least as many due timers as the run has. -/
theorem wp4_dueTimers_length_le {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (state : IsSimulationState program lConfig dexConfig)
    (run : Dex.Run signature) (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running) :
    (run.dueTimers dexConfig.clock).length ≤ (wp2_dueCandidates lConfig).length := by
  have related := state.isRelated.running run hasRun isRunning
  have keysNodup := state.timerKeysNodup run hasRun
  have isArmed : ∀ timer, timer ∈ (run.timers.filter (fun entry =>
      decide (entry.2 ≤ dexConfig.clock))).map (fun entry => entry.1) →
      ∃ fireTime thread chan since, fireTime ≤ lConfig.clock ∧ thread ∈ lConfig.threads ∧
        thread.phase = .blocked (.timed timer.step.number fireTime chan) since ∧
        timer.step.stepType = StepType.timed ∧ timer.index = 0 := by
    intro timer isMember
    obtain ⟨entry, isEntry, rfl⟩ := List.mem_map.mp isMember
    obtain ⟨isEntryArmed, isDue⟩ := List.mem_filter.mp isEntry
    have isGet := (wp2_mem_iff_get? run.timers keysNodup entry.1 entry.2).mp isEntryArmed
    obtain ⟨thread, chan, since, isThread, isBlocked, isTimed, isIndex⟩ :=
      (related.timers entry.1 entry.2).mp isGet
    exact ⟨entry.2, thread, chan, since, by rw [state.isRelated.clock]; simpa using isDue,
      isThread, isBlocked, isTimed, isIndex⟩
  have isLength : (run.dueTimers dexConfig.clock).length =
      ((run.timers.filter (fun entry => decide (entry.2 ≤ dexConfig.clock))).map
        (fun entry => entry.1)).length := by
    simp only [Dex.Run.dueTimers, List.length_map]
    exact (wp2_sortStable_perm _ _).length_eq
  rw [isLength, ← List.length_map (f := fun candidate : Nat × Nat × Tid => candidate.2.1)]
  apply wp4_length_le_of_injOn (fun timer : Dex.TimerId signature => timer.step.number)
  · exact keysNodup.sublist (List.Sublist.map _ List.filter_sublist)
  · intro first isFirst second isSecond sameNumber
    obtain ⟨_, _, _, _, _, _, _, firstTimed, firstIndex⟩ := isArmed first isFirst
    obtain ⟨_, _, _, _, _, _, _, secondTimed, secondIndex⟩ := isArmed second isSecond
    obtain ⟨⟨firstType, firstNumber⟩, firstPosition⟩ := first
    obtain ⟨⟨secondType, secondNumber⟩, secondPosition⟩ := second
    simp only at firstTimed firstIndex secondTimed secondIndex sameNumber
    subst firstTimed firstIndex secondTimed secondIndex sameNumber
    rfl
  · intro timer isMember
    obtain ⟨fireTime, thread, chan, since, isDue, isThread, isBlocked, _, _⟩ :=
      isArmed timer isMember
    exact List.mem_map.mpr ⟨(fireTime, timer.step.number, thread.core.tid),
      (wp2_mem_dueCandidates lConfig fireTime timer.step.number thread.core.tid).mpr
        ⟨thread, chan, since, isThread, isBlocked, isDue, rfl⟩, rfl⟩

/-- While the Flow runs, [TIMER-FIRE] of `count` timers is simulated by a
batch of `count` `fire` actions. -/
theorem dex_timerFire_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (run : Dex.Run signature) (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running) (count : Nat)
    (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig (.timerFire count) =
      some (dexEvents, dexNext)) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  obtain ⟨isPositive, isEnough, canCommit⟩ :
      0 < count ∧ count ≤ (run.dueTimers dexConfig.clock).length ∧
        dexConfig.canCommit = true := by
    have isStepCopy := isStep
    simp only [Dex.system, Dex.step, hasRun] at isStepCopy
    split at isStepCopy
    · rename_i isEnabled
      simpa [and_assoc] using isEnabled
    · cases isStepCopy
  have canStamp := Related.canStamp_of_canCommit state.isRelated canCommit
  obtain ⟨final, isFired⟩ := wp4_fireTimers_enabled program count lConfig state.lInvariant
    (Nat.le_trans isEnough (wp4_dueTimers_length_le state run hasRun isRunning))
  have isLStep : (L.system program).step lConfig (.fire count) = some ([], final.stamp) := by
    simp [L.system, L.step, isPositive, canStamp, isFired]
  exact wp4_simulated_of_single_action program lConfig dexConfig state _ _ _ isLStep _ _ _ isStep
    rfl

end DexExpressiveness
