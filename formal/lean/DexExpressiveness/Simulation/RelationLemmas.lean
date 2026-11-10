import DexExpressiveness.Simulation.Relation

/-!
# Consequences of the simulation relation

Facts that follow from the fixed definitions of `Simulation/Relation.lean` and
`Invariants.lean`, for the proofs of the step simulations. Everything here is
proved. The proofs of the work packages may add lemmas to this file.

- A task holds at most one effect, and a WaitFor task holds none
  (`pendingEffects_length_le_one`, `pendingEffects_eq_nil_of_waitFor`).
- The Step that carries a live thread is the one whose state matches the
  thread's phase (`RunningRelated.phaseMatchesStep_of_carries`). So a waiting
  Step has no invocation and carries a blocked thread
  (`RunningRelated.blocked_of_waiting`).
- The actions of `simulatingActions` hold the encoded request of the label and
  no other request, so `IsSimulatedByActions` implies `IsSimulatedByDex`.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Tasks hold at most one effect -/

/-- A task of a translated Flow holds at most one effect it has not emitted. -/
theorem pendingEffects_length_le_one (program : Program) (task : Dex.Task signature)
    (isComputed : IsTaskComputed program task) :
    (pendingEffects ⟦program⟧ task).length ≤ 1 := by
  unfold pendingEffects
  split
  · exact (translate_compute_effects program task).1
  · rename_i remainingEffects response isPhase
    exact (isComputed.remainingEffects remainingEffects response isPhase).1
  · exact Nat.zero_le 1

/-- A WaitFor task of a translated Flow holds no effect. -/
theorem pendingEffects_eq_nil_of_waitFor (program : Program) (task : Dex.Task signature)
    (isComputed : IsTaskComputed program task) (isWaitFor : task.method = .waitFor) :
    pendingEffects ⟦program⟧ task = [] := by
  unfold pendingEffects
  split
  · exact (translate_compute_effects program task).2 isWaitFor
  · rename_i remainingEffects response isPhase
    exact (isComputed.remainingEffects remainingEffects response isPhase).2 isWaitFor
  · rfl

/-! ## The Step of a thread -/

/-- Two Steps that carry the same thread have the same input. -/
theorem StepCarriesThread.input_eq {program : Program} {thread : L.Thread}
    {first second : Dex.StepRecord signature}
    (carriesFirst : StepCarriesThread program thread first)
    (carriesSecond : StepCarriesThread program thread second) : first.input = second.input := by
  obtain ⟨core, code, timer, phase⟩ := thread
  rcases timer with _ | ⟨number, seconds⟩ <;> cases phase <;>
    simp only [StepCarriesThread] at carriesFirst carriesSecond
  all_goals first
    | (obtain ⟨_, firstInput, isFirst, firstLocals⟩ := carriesFirst
       obtain ⟨_, secondInput, isSecond, secondLocals⟩ := carriesSecond
       have sameLocals := firstLocals.symm.trans secondLocals
       simp only [List.cons.injEq, Prod.mk.injEq, true_and, and_true] at sameLocals
       rw [isFirst, isSecond, sameLocals])
    | rw [carriesFirst.2, carriesSecond.2]
    | rw [carriesFirst.2.1, carriesSecond.2.1]

/-- The Step that carries a live thread is the one whose state matches the
thread's phase. -/
theorem RunningRelated.phaseMatchesStep_of_carries {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (thread : L.Thread) (isThread : thread ∈ lConfig.threads) (isLive : thread.phase ≠ .halted)
    (record : Dex.StepRecord signature) (isRecord : record ∈ run.steps)
    (isActive : record.phase.isActive = true)
    (carries : StepCarriesThread program thread record) :
    PhaseMatchesStep program lConfig dexConfig thread record := by
  obtain ⟨matching, isMatching, isMatchingActive, matchingCarries, phaseMatches⟩ :=
    related.threadsHaveSteps thread isThread isLive
  have isSame : record = matching :=
    related.activeStepsHaveDistinctThreads record isRecord matching isMatching isActive
      isMatchingActive (by rw [carries.input_eq matchingCarries])
  rw [isSame]
  exact phaseMatches

/-- A waiting Step has no invocation, and the thread it carries is blocked:
the Step waits (`WT`) exactly while its thread is blocked. -/
theorem RunningRelated.blocked_of_waiting {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (record : Dex.StepRecord signature) (isRecord : record ∈ run.steps)
    (isWaiting : record.phase = .waiting) :
    record.invocation = none ∧
    ∃ thread on since, thread ∈ lConfig.threads ∧ thread.phase = .blocked on since ∧
      StepCarriesThread program thread record := by
  have isActive : record.phase.isActive = true := by
    rw [isWaiting]
    rfl
  obtain ⟨thread, isThread, isLive, carries⟩ := related.stepsHaveThreads record isRecord isActive
  have phaseMatches :=
    related.phaseMatchesStep_of_carries thread isThread isLive record isRecord isActive carries
  cases phaseCase : thread.phase <;> simp only [PhaseMatchesStep, phaseCase] at phaseMatches
  all_goals first
    | exact ⟨phaseMatches.2.1, thread, _, _, isThread, phaseCase, carries⟩
    | (have isPhase := phaseMatches.1
       rw [isWaiting] at isPhase
       first
         | cases isPhase
         | (split at isPhase <;> cases isPhase))

/-! ## Facts about the Dex configuration of a simulation state -/

/-- A waiting Step has no invocation, while the Flow runs and after the close. -/
theorem IsSimulationState.waitingHasNoInvocation {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (state : IsSimulationState program lConfig dexConfig) :
    ∀ run, dexConfig.run = some run → ∀ record, record ∈ run.steps →
      record.phase = .waiting → record.invocation = none := by
  intro run hasRun record isRecord isWaiting
  cases isStatus : run.status with
  | running =>
    exact ((state.isRelated.running run hasRun isStatus).blocked_of_waiting record isRecord
      isWaiting).1
  | closed status =>
    exact (state.isRelated.closed run status hasRun isStatus).noInvocation record isRecord

/-- The armed timers have distinct IDs. -/
theorem IsSimulationState.timerKeysNodup {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (state : IsSimulationState program lConfig dexConfig) :
    ∀ run, dexConfig.run = some run → (run.timers.map (fun entry => entry.1)).Nodup :=
  state.timersDistinct

/-- Every task holds at most one effect it has not emitted. -/
theorem IsSimulationState.holdsAtMostOneEffect {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (state : IsSimulationState program lConfig dexConfig) :
    ∀ task, task ∈ dexConfig.tasks → (pendingEffects ⟦program⟧ task).length ≤ 1 :=
  fun task isMember =>
    pendingEffects_length_le_one program task (state.dexInvariant.tasksComputed task isMember)

/-! ## The simulating actions of each case -/

/-- With unique task IDs, the lookup of a task's ID finds the task. -/
theorem findTask?_of_mem {config : Dex.Config signature} (tasksDistinct : TasksAreDistinct config)
    {task : Dex.Task signature} (isMember : task ∈ config.tasks) :
    config.findTask? task.id = some task := by
  cases foundCase : config.findTask? task.id with
  | none =>
    have isRejected := List.find?_eq_none.mp foundCase task isMember
    simp at isRejected
  | some found =>
    rw [eq_of_isFound config task.id task found tasksDistinct isMember rfl foundCase]

/-- The lookup of `stepOfThread` finds the active Step whose input belongs to
the thread. -/
theorem RunningRelated.find?_activeStep {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run)
    (record : Dex.StepRecord signature) (isRecord : record ∈ run.steps)
    (isActive : record.phase.isActive = true) :
    run.steps.find? (fun candidate =>
      candidate.phase.isActive &&
        decide (threadOfInput candidate.input = threadOfInput record.input)) = some record := by
  cases findCase : run.steps.find? (fun candidate =>
      candidate.phase.isActive &&
        decide (threadOfInput candidate.input = threadOfInput record.input)) with
  | none =>
    have isRejected := List.find?_eq_none.mp findCase record isRecord
    simp [isActive] at isRejected
  | some found =>
    have isFoundMember := List.mem_of_find?_eq_some findCase
    have holds := List.find?_some findCase
    simp only [Bool.and_eq_true, decide_eq_true_eq] at holds
    rw [related.activeStepsHaveDistinctThreads found isFoundMember record isRecord holds.1
      isActive holds.2]

/-- `stepOfThread` finds the active Step that carries the thread. -/
theorem RunningRelated.stepOfThread_eq {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run) (hasRun : dexConfig.run = some run)
    (record : Dex.StepRecord signature) (isRecord : record ∈ run.steps)
    (isActive : record.phase.isActive = true) :
    ∃ task, stepOfThread dexConfig (threadOfInput record.input) = some (record, task) := by
  simp only [stepOfThread, hasRun, related.find?_activeStep record isRecord isActive]
  split <;> exact ⟨_, rfl⟩

/-- `stepOfThread` finds the active Step that carries the thread, with the
task that runs its invocation. -/
theorem RunningRelated.stepOfThread_running {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run) (hasRun : dexConfig.run = some run)
    (tasksDistinct : TasksAreDistinct dexConfig)
    (record : Dex.StepRecord signature) (isRecord : record ∈ run.steps)
    (isActive : record.phase.isActive = true) (method : Dex.Method)
    (task : Dex.Task signature) (isTask : task ∈ dexConfig.tasks)
    (hasInvocation : record.invocation = some { method := method, state := .running task.id }) :
    stepOfThread dexConfig (threadOfInput record.input) = some (record, some task) := by
  simp only [stepOfThread, hasRun, related.find?_activeStep record isRecord isActive,
    hasInvocation, findTask?_of_mem tasksDistinct isTask]

/-- The simulating action of a thread that begins an action is the
[STEP-START] of the Step that carries it. -/
theorem simulatingActions_begin {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run) (hasRun : dexConfig.run = some run)
    {tid : Tid} {thread : L.Thread} (isFound : lConfig.findThread? tid = some thread)
    (record : Dex.StepRecord signature) (isRecord : record ∈ run.steps)
    (isActive : record.phase.isActive = true) (isThread : threadOfInput record.input = tid)
    (isPending : thread.phase = .bootPending ∨ thread.phase = .ready ∨
      ∃ outcome, thread.phase = .woken outcome) :
    simulatingActions program lConfig dexConfig (.thread tid) = [.stepStart record.id] := by
  subst isThread
  obtain ⟨task, isStep⟩ := related.stepOfThread_eq hasRun record isRecord isActive
  simp only [simulatingActions, isFound, isStep]
  rcases isPending with isPhase | isPhase | ⟨outcome, isPhase⟩ <;> simp [isPhase]

/-- The simulating actions of `out(a)`: the Worker step that runs the handler
if it has not run, then the [WORKER-EFFECT] of the thread's task. -/
theorem simulatingActions_deliver {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run) (hasRun : dexConfig.run = some run)
    (tasksDistinct : TasksAreDistinct dexConfig)
    {tid : Tid} {thread : L.Thread} (isFound : lConfig.findThread? tid = some thread)
    (record : Dex.StepRecord signature) (isRecord : record ∈ run.steps)
    (isActive : record.phase.isActive = true) (isThread : threadOfInput record.input = tid)
    (method : Dex.Method) (task : Dex.Task signature) (isTask : task ∈ dexConfig.tasks)
    (hasInvocation : record.invocation = some { method := method, state := .running task.id })
    (effect : Val) (isEmitting : thread.phase = .emitting effect) :
    simulatingActions program lConfig dexConfig (.thread tid) =
      runTaskActions task ++ [.workerEffect task.id] := by
  subst isThread
  simp only [simulatingActions, isFound, isEmitting,
    related.stepOfThread_running hasRun tasksDistinct record isRecord isActive method task isTask
      hasInvocation]

/-- The simulating actions of a thread that completes an action: the Worker
steps that run and return the handler as far as they have not happened, then
the Complete of the thread's task. -/
theorem simulatingActions_complete {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (related : RunningRelated program lConfig dexConfig run) (hasRun : dexConfig.run = some run)
    (tasksDistinct : TasksAreDistinct dexConfig)
    {tid : Tid} {thread : L.Thread} (isFound : lConfig.findThread? tid = some thread)
    (record : Dex.StepRecord signature) (isRecord : record ∈ run.steps)
    (isActive : record.phase.isActive = true) (isThread : threadOfInput record.input = tid)
    (method : Dex.Method) (task : Dex.Task signature) (isTask : task ∈ dexConfig.tasks)
    (hasInvocation : record.invocation = some { method := method, state := .running task.id })
    (isCompleting : thread.phase = .bootRunning ∨ (∃ loaded, thread.phase = .running loaded) ∨
      thread.phase = .emitted ∨ thread.phase = .waitStarted ∨
      ∃ outcome, thread.phase = .resuming outcome) :
    simulatingActions program lConfig dexConfig (.thread tid) =
      returnTaskActions task ++ [.stepComplete task.id] := by
  subst isThread
  simp only [simulatingActions, isFound,
    related.stepOfThread_running hasRun tasksDistinct record isRecord isActive method task isTask
      hasInvocation]
  rcases isCompleting with isPhase | ⟨loaded, isPhase⟩ | isPhase | isPhase | ⟨outcome, isPhase⟩ <;>
    simp [isPhase]

/-- After the close, a late emit is held by a task, and `simulatingActions`
picks the first task that holds it. -/
theorem ClosedRelated.exists_lateTask {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {run : Dex.Run signature}
    (closed : ClosedRelated program lConfig dexConfig run) {index : Nat} {effect : Val}
    (isLate : lConfig.lateEmits[index]? = some effect) :
    ∃ task, dexConfig.tasks.find? (fun candidate =>
        (pendingEffects ⟦program⟧ candidate).contains effect) = some task ∧
      task ∈ dexConfig.tasks ∧ effect ∈ pendingEffects ⟦program⟧ task := by
  have isHeld : effect ∈ dexConfig.tasks.flatMap (pendingEffects ⟦program⟧) :=
    closed.lateEmits.mem_iff.mpr (List.mem_of_getElem? isLate)
  obtain ⟨holder, isHolder, holdsEffect⟩ := List.mem_flatMap.mp isHeld
  cases findCase : dexConfig.tasks.find? (fun candidate =>
      (pendingEffects ⟦program⟧ candidate).contains effect) with
  | none =>
    have isRejected := List.find?_eq_none.mp findCase holder isHolder
    simp [holdsEffect] at isRejected
  | some task =>
    exact ⟨task, rfl, List.mem_of_find?_eq_some findCase, by
      simpa using List.find?_some findCase⟩

/-- The simulating actions of the delivery of a late emit. -/
theorem simulatingActions_lateDeliver {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {index : Nat} {effect : Val}
    (isLate : lConfig.lateEmits[index]? = some effect) {task : Dex.Task signature}
    (isPicked : dexConfig.tasks.find? (fun candidate =>
      (pendingEffects ⟦program⟧ candidate).contains effect) = some task) :
    simulatingActions program lConfig dexConfig (.lateDeliver index) =
      runTaskActions task ++ [.workerEffect task.id] := by
  simp only [simulatingActions, isLate, isPicked]

/-- The simulating action of the drop of a late emit. -/
theorem simulatingActions_lateDrop {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {index : Nat} {effect : Val}
    (isLate : lConfig.lateEmits[index]? = some effect) {task : Dex.Task signature}
    (isPicked : dexConfig.tasks.find? (fun candidate =>
      (pendingEffects ⟦program⟧ candidate).contains effect) = some task) :
    simulatingActions program lConfig dexConfig (.lateDrop index) = [.workerAbandon task.id] := by
  simp only [simulatingActions, isLate, isPicked]

/-! ## The simulating actions hold the encoded request only -/

/-- The Worker step that runs a handler is no request. -/
theorem runTaskActions_requests (flow : Dex.FlowDef signature) (task : Dex.Task signature) :
    (runTaskActions task).filterMap (Dex.system flow).requestOf = [] := by
  unfold runTaskActions
  split <;> rfl

/-- The Worker steps that return a handler are no requests. -/
theorem returnTaskActions_requests (flow : Dex.FlowDef signature) (task : Dex.Task signature) :
    (returnTaskActions task).filterMap (Dex.system flow).requestOf = [] := by
  unfold returnTaskActions
  split <;> rfl

/-- The only request among the simulating actions of a label is the encoded
request of the label. -/
theorem simulatingActions_requests (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (label : L.Label) :
    (simulatingActions program lConfig dexConfig label).filterMap
        (Dex.system ⟦program⟧).requestOf =
      ((L.system program).requestOf label).toList.map encodeRequest := by
  cases label with
  | tick duration => rfl
  | request request => rfl
  | fire count => rfl
  | thread thread =>
    show _ = []
    simp only [simulatingActions]
    split
    · split
      all_goals first
        | rfl
        | (rw [List.filterMap_append, runTaskActions_requests]; rfl)
        | (rw [List.filterMap_append, returnTaskActions_requests]; rfl)
    · rfl
  | lateDeliver index =>
    show _ = []
    simp only [simulatingActions]
    split
    · rfl
    · split
      · rw [List.filterMap_append, runTaskActions_requests]
        rfl
      · rfl
  | lateDrop index =>
    show _ = []
    simp only [simulatingActions]
    split
    · rfl
    · split <;> rfl

/-- A step that is simulated by the simulating actions is simulated by the
translated Flow. -/
theorem IsSimulatedByActions.isSimulatedByDex {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} {label : L.Label} {lEvents : List Event}
    {lNext : L.Config}
    (isSimulated : IsSimulatedByActions program lConfig dexConfig label lEvents lNext) :
    IsSimulatedByDex program dexConfig label lEvents lNext := by
  obtain ⟨dexEvents, dexNext, isExec, sameEvents, isRelated⟩ := isSimulated
  exact ⟨_, dexEvents, dexNext, isExec, sameEvents,
    simulatingActions_requests program lConfig dexConfig label, isRelated⟩

end DexExpressiveness
