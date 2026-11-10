import DexExpressiveness.Liveness.LToDexRun
import DexExpressiveness.Simulation.Frame

/-!
# Liveness, work package L6b: the Worker assumptions of the Dex run of a fair L run

The helpers of `DexRunOfLRun.workersSettle` (F4) and
`DexRunOfLRun.resultsCommit` (F5) of `Liveness/LToDexFair.lean`.

F5 needs the block boundaries only: every position is at or before a
boundary, and no task is `Returned` at a boundary
(`DexFollowsL.noReturnedTask`).

F4 is proved by contradiction. Assume that a task stays in `Θ` and never
returns from some position on.

- Every Dex step keeps the input of a task that stays in `Θ`, and gives it no
  effect to emit (`taskFollows_of_step`, `taskFollows_of_isRun`). The proof
  reads this off every rule, so it needs no block shapes.
- At a boundary where the Flow runs, the task is current, so its Step carries
  a thread that issued an emit and holds the effect, or that began an action
  and holds none (`IsSimulationState.thread_of_task`). The thread is
  the same at every boundary, because its ID is in the task's input.
- F5 of L ends a thread's begun action, and the task holds no effect then or
  later, so the Flow has closed
  (`DexRunOfLRun.not_unsettled_forever_of_noEffect`). F4 of L ends an issued
  emit, which gives the same case.
- After the close the program stays closed (`L.step_keeps_closed`), and F4
  of L empties the late emits (`exists_lateEmits_eq_nil`). A task that is left
  between two blocks then holds an effect (`DexFollowsL.isSettled`), which is
  a late emit (`DexRunOfLRun.not_unsettled_forever_of_closed`).

Nothing here uses `flattening_position` or `flattening_exec_take`. The order
of the block boundaries is `BlockRun.IsFlattenedTo.boundary_mono` and
`BlockRun.IsFlattenedTo.exists_boundary_ge` of `Core/BlockRun.lean`.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## One task across a Dex step -/

/-- `later` is `task` after some Dex steps that kept it in `Θ`: it has the
same input, and it holds no effect to emit when `task` held none. -/
def TaskFollows (flow : Dex.FlowDef signature) (task later : Dex.Task signature) : Prop :=
  later.input = task.input ∧ (pendingEffects flow task = [] → pendingEffects flow later = [])

/-- A task follows itself. -/
theorem TaskFollows.refl (flow : Dex.FlowDef signature) (task : Dex.Task signature) :
    TaskFollows flow task task :=
  ⟨rfl, id⟩

/-- Following composes. -/
theorem TaskFollows.trans {flow : Dex.FlowDef signature}
    {task middle later : Dex.Task signature} (first : TaskFollows flow task middle)
    (second : TaskFollows flow middle later) : TaskFollows flow task later :=
  ⟨second.1.trans first.1, fun noEffect => second.2 (first.2 noEffect)⟩

/-- A step that keeps `Θ` keeps the task. -/
theorem taskFollows_of_same_tasks (flow : Dex.FlowDef signature) (config next : Dex.Config signature)
    (taskId : Nat) (task later : Dex.Task signature)
    (isFound : config.findTask? taskId = some task)
    (isLaterFound : next.findTask? taskId = some later) (sameTasks : next.tasks = config.tasks) :
    TaskFollows flow task later := by
  have sameLookup : next.findTask? taskId = config.findTask? taskId := by
    simp only [Dex.Config.findTask?, sameTasks]
  rw [sameLookup, isFound] at isLaterFound
  cases isLaterFound
  exact TaskFollows.refl flow task

/-- A step that removes a task keeps every other task. -/
theorem taskFollows_of_removeTask (flow : Dex.FlowDef signature)
    (config next : Dex.Config signature) (removed taskId : Nat) (task later : Dex.Task signature)
    (isFound : config.findTask? taskId = some task)
    (isLaterFound : next.findTask? taskId = some later)
    (sameTasks : next.tasks = (config.removeTask removed).tasks) :
    TaskFollows flow task later := by
  have sameLookup : next.findTask? taskId = (config.removeTask removed).findTask? taskId := by
    simp only [Dex.Config.findTask?, sameTasks]
  rw [sameLookup, findTask?_removeTask] at isLaterFound
  split at isLaterFound
  · cases isLaterFound
  · rw [isFound] at isLaterFound
    cases isLaterFound
    exact TaskFollows.refl flow task

/-- A step that adds a task at the end of `Θ` keeps the task. -/
theorem taskFollows_of_append (flow : Dex.FlowDef signature)
    (config next : Dex.Config signature) (added : Dex.Task signature) (taskId : Nat)
    (task later : Dex.Task signature) (isFound : config.findTask? taskId = some task)
    (isLaterFound : next.findTask? taskId = some later)
    (sameTasks : next.tasks = config.tasks ++ [added]) : TaskFollows flow task later := by
  have sameLookup : next.findTask? taskId = config.findTask? taskId := by
    simp only [Dex.Config.findTask?] at isFound ⊢
    rw [sameTasks, List.find?_append, isFound]
    rfl
  rw [sameLookup, isFound] at isLaterFound
  cases isLaterFound
  exact TaskFollows.refl flow task

/-- A Worker step that changes the phase of one task keeps the input of every
task, and gives the changed task no effect when `keepsNoEffect` holds. -/
theorem taskFollows_of_updateTask (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (changed taskId : Nat) (change : Dex.Task signature → Dex.Task signature)
    (task later : Dex.Task signature) (isFound : config.findTask? taskId = some task)
    (isLaterFound : (config.updateTask changed change).findTask? taskId = some later)
    (keepsId : ∀ current, (change current).id = current.id)
    (keepsInput : ∀ current, (change current).input = current.input)
    (keepsNoEffect : changed = taskId → pendingEffects flow task = [] →
      pendingEffects flow (change task) = []) : TaskFollows flow task later := by
  have hasId := (mem_of_findTask? config taskId task isFound).2
  rw [findTask?_updateTask config changed taskId change keepsId, isFound] at isLaterFound
  simp only [Option.map_some] at isLaterFound
  injection isLaterFound with isLater
  subst isLater
  by_cases isChanged : task.id = changed
  · simp only [isChanged, ↓reduceIte]
    exact ⟨keepsInput task, keepsNoEffect (isChanged.symm.trans hasId)⟩
  · simp only [isChanged, ↓reduceIte]
    exact TaskFollows.refl flow task

/-- One Dex step keeps the input of a task that stays in `Θ`, and gives it no
effect to emit: a Worker step changes the phase of one task, [STEP-START]
adds a task with a fresh ID at the end, and every other rule keeps `Θ` or
removes one task. -/
theorem taskFollows_of_step (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (action : Dex.Action signature) (events : List (Dex.Obs signature))
    (next : Dex.Config signature) (isStep : Dex.step flow config action = some (events, next))
    (taskId : Nat) (task later : Dex.Task signature)
    (isFound : config.findTask? taskId = some task)
    (isLaterFound : next.findTask? taskId = some later) : TaskFollows flow task later := by
  cases action with
  | tick duration =>
    simp only [Dex.step] at isStep
    split at isStep <;> cases isStep
    exact taskFollows_of_same_tasks flow config _ taskId task later isFound isLaterFound rfl
  | request request =>
    cases request with
    | startFlow stepType input =>
      simp only [Dex.step] at isStep
      split at isStep
      · cases isStep
      · split at isStep <;> cases isStep
        exact taskFollows_of_same_tasks flow config _ taskId task later isFound isLaterFound rfl
    | publish chan value =>
      simp only [Dex.step] at isStep
      split at isStep
      · cases isStep
        exact taskFollows_of_same_tasks flow config _ taskId task later isFound isLaterFound rfl
      · split at isStep
        · cases isStep
          exact taskFollows_of_same_tasks flow config _ taskId task later isFound isLaterFound rfl
        · split at isStep <;> cases isStep
          exact taskFollows_of_same_tasks flow config _ taskId task later isFound isLaterFound rfl
  | stepStart stepId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
      · split at isStep
        · split at isStep
          · cases isStep
            exact taskFollows_of_append flow config _ _ taskId task later isFound isLaterFound rfl
          · cases isStep
        · cases isStep
  | workerRun changed =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i running isRunningFound
      split at isStep <;> cases isStep
      rename_i isStarted
      refine taskFollows_of_updateTask flow config changed taskId _ task later isFound isLaterFound
        (fun _ => rfl) (fun _ => rfl) ?_
      intro isChanged noEffect
      subst isChanged
      rw [isFound] at isRunningFound
      cases isRunningFound
      simp only [pendingEffects, isStarted] at noEffect ⊢
      exact noEffect
  | workerEffect changed =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i running isRunningFound
      split at isStep <;> cases isStep
      rename_i effect laterEffects response isEmitting
      refine taskFollows_of_updateTask flow config changed taskId _ task later isFound isLaterFound
        (fun _ => rfl) (fun _ => rfl) ?_
      intro isChanged noEffect
      subst isChanged
      rw [isFound] at isRunningFound
      cases isRunningFound
      simp [pendingEffects, isEmitting] at noEffect
  | workerReturn changed =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      refine taskFollows_of_updateTask flow config changed taskId _ task later isFound isLaterFound
        (fun _ => rfl) (fun _ => rfl) ?_
      intro _ _
      rfl
  | workerAbandon removed =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact taskFollows_of_removeTask flow config _ removed taskId task later isFound isLaterFound rfl
  | stepCompleteStale removed =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · split at isStep <;> cases isStep
        exact taskFollows_of_removeTask flow config _ removed taskId task later isFound
          isLaterFound rfl
      · cases isStep
  | stepComplete removed =>
    simp only [Dex.step] at isStep
    split at isStep
    · split at isStep
      · split at isStep <;> cases isStep
        · exact taskFollows_of_removeTask flow config _ removed taskId task later isFound
            isLaterFound rfl
        · exact taskFollows_of_removeTask flow config _ removed taskId task later isFound
            isLaterFound rfl
      · cases isStep
    · cases isStep
  | timerFire count =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact taskFollows_of_same_tasks flow config _ taskId task later isFound isLaterFound rfl

/-- Along a Dex run, a task that stays in `Θ` keeps its input and gets no
effect to emit. -/
theorem taskFollows_of_isRun (flow : Dex.FlowDef signature)
    (dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature))
    (isRun : (Dex.system flow).IsRun dexRun) (taskId start : Nat)
    (stays : ∀ position, start ≤ position →
      ∃ task, (dexRun.states position).findTask? taskId = some task)
    (first last : Nat) (isAfter : start ≤ first) (isOrdered : first ≤ last)
    (task later : Dex.Task signature)
    (isFound : (dexRun.states first).findTask? taskId = some task)
    (isLaterFound : (dexRun.states last).findTask? taskId = some later) :
    TaskFollows flow task later := by
  obtain ⟨distance, isLast⟩ := Nat.exists_eq_add_of_le isOrdered
  subst isLast
  induction distance generalizing later with
  | zero =>
    rw [Nat.add_zero, isFound] at isLaterFound
    cases isLaterFound
    exact TaskFollows.refl flow task
  | succ distance inductionHypothesis =>
    obtain ⟨middle, isMiddleFound⟩ := stays (first + distance) (by omega)
    have toMiddle := inductionHypothesis middle (Nat.le_add_right first distance) isMiddleFound
    obtain ⟨events, isStep⟩ := isRun.2 (first + distance)
    exact toMiddle.trans (taskFollows_of_step flow _ _ events _ isStep taskId middle later
      isMiddleFound isLaterFound)

/-! ## After the close -/

/-- A closed L program with no thread stays closed. -/
theorem L.step_keeps_closed (program : Program) (config next : L.Config) (label : L.Label)
    (events : List Event) (isStep : L.step program config label = some (events, next))
    (isClosed : config.status = .closed) (noThread : config.threads = []) :
    next.status = .closed := by
  cases label with
  | tick duration =>
    simp only [L.step] at isStep
    split at isStep <;> cases isStep
    exact isClosed
  | request request =>
    cases request with
    | start input =>
      simp [L.step, isClosed] at isStep
    | input chan value =>
      simp only [L.step, isClosed] at isStep
      cases isStep
      exact isClosed
  | thread thread =>
    simp [L.step, L.Config.findThread?, noThread] at isStep
  | fire count =>
    cases count with
    | zero => simp [L.step] at isStep
    | succ count =>
      simp [L.step, L.Config.fireTimers, L.Config.leastDueTimerThread, noThread,
        sortStable] at isStep
  | lateDeliver index =>
    simp only [L.step] at isStep
    split at isStep
    · cases isStep
      exact isClosed
    · cases isStep
  | lateDrop index =>
    simp only [L.step] at isStep
    split at isStep
    · cases isStep
      exact isClosed
    · cases isStep

/-- F4 of L, applied until no late emit is left. -/
theorem exists_lateEmits_eq_nil (lRun : InfiniteRun L.Config L.Label)
    (emitsSettle : L.Fairness.EmitsSettle lRun) (index : Nat) :
    ∃ later, index ≤ later ∧ (lRun.states later).lateEmits = [] := by
  generalize isLength : (lRun.states index).lateEmits.length = count
  induction count using Nat.strongRecOn generalizing index with
  | _ count inductionHypothesis =>
    by_cases isEmpty : (lRun.states index).lateEmits = []
    · exact ⟨index, Nat.le_refl _, isEmpty⟩
    · obtain ⟨next, isAfter, isShorter⟩ := emitsSettle.2 index isEmpty
      obtain ⟨later, isLater, isDone⟩ :=
        inductionHypothesis _ (by rw [← isLength]; exact isShorter) next rfl
      exact ⟨later, Nat.le_trans isAfter isLater, isDone⟩

/-! ## The thread of a task while the Flow runs -/

/-- While the Flow runs, a task of `Θ` runs the invocation of a Step that
carries a thread. The thread's ID is in the task's input. The thread issued an
emit and the task holds that effect, or the thread began an action and the
task holds no effect. -/
theorem IsSimulationState.thread_of_task {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (state : IsSimulationState program lConfig dexConfig)
    (run : Dex.Run signature) (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running) (taskId : Nat) (task : Dex.Task signature)
    (isFound : dexConfig.findTask? taskId = some task) :
    ∃ current, lConfig.findThread? (threadOfInput task.input) = some current ∧
      ((∃ effect, current.phase = .emitting effect ∧
          pendingEffects ⟦program⟧ task = [effect]) ∨
        (pendingEffects ⟦program⟧ task = [] ∧
          (current.phase = .bootRunning ∨ current.phase = .emitted ∨
            current.phase = .waitStarted ∨ (∃ loaded, current.phase = .running loaded) ∨
            ∃ outcome, current.phase = .resuming outcome))) := by
  have related := state.isRelated.running run hasRun isRunning
  obtain ⟨isTask, hasId⟩ := mem_of_findTask? dexConfig taskId task isFound
  have isCurrent := related.tasksAreCurrent task isTask
  simp only [Dex.Config.isTaskCurrent, hasRun] at isCurrent
  split at isCurrent
  · cases isCurrent
  · rename_i record isRecordFound
    have hasInvocation : record.invocation =
        some { method := task.method, state := .running task.id } := of_decide_eq_true isCurrent
    have isRecord : record ∈ run.steps := List.mem_of_find?_eq_some isRecordFound
    have isActive := related.invocationsAreActive record isRecord (by rw [hasInvocation]; simp)
    obtain ⟨thread, isThread, isLive, carries⟩ :=
      related.stepsHaveThreads record isRecord isActive
    have phaseMatches :=
      related.phaseMatchesStep_of_carries thread isThread isLive record isRecord isActive carries
    have isThreadFound : lConfig.findThread? (threadOfInput record.input) = some thread := by
      rw [threadOfInput_of_mem state.lInvariant isThread carries]
      exact findThread?_of_mem state.lInvariant isThread
    have ofRunningTask : ∀ method Holds, HasRunningTask dexConfig record method Holds →
        task.input = record.input ∧ Holds task := by
      rintro method Holds ⟨running, isRunningTask, hasRunning, _, _, sameInput, holds⟩
      have sameId : running.id = task.id := by
        rw [hasInvocation] at hasRunning
        simp only [Option.some.injEq, Dex.Invocation.mk.injEq,
          Dex.InvocationState.running.injEq] at hasRunning
        exact hasRunning.2.symm
      have isSame : running = task :=
        eq_of_isFound dexConfig taskId running task state.tasksDistinct isRunningTask
          (sameId.trans hasId) isFound
      rw [← isSame]
      exact ⟨sameInput, holds⟩
    have notPending : record.invocation ≠ some { method := .execute, state := .pending } := by
      rw [hasInvocation]
      simp
    cases phaseCase : thread.phase <;> simp only [PhaseMatchesStep, phaseCase] at phaseMatches
    case bootPending => exact absurd phaseMatches.2 notPending
    case bootRunning =>
      obtain ⟨sameInput, noEffect⟩ := ofRunningTask _ _ phaseMatches.2
      exact ⟨thread, by rw [sameInput]; exact isThreadFound,
        Or.inr ⟨noEffect, Or.inl phaseCase⟩⟩
    case ready =>
      have hasPending := phaseMatches.2.2
      rw [hasInvocation] at hasPending
      simp at hasPending
    case running loaded =>
      obtain ⟨sameInput, holds⟩ := ofRunningTask _ _ phaseMatches.2
      exact ⟨thread, by rw [sameInput]; exact isThreadFound,
        Or.inr ⟨holds.1, Or.inr (Or.inr (Or.inr (Or.inl ⟨loaded, phaseCase⟩)))⟩⟩
    case emitting effect =>
      obtain ⟨sameInput, holds⟩ := ofRunningTask _ _ phaseMatches.2
      exact ⟨thread, by rw [sameInput]; exact isThreadFound,
        Or.inl ⟨effect, phaseCase, holds.1⟩⟩
    case emitted =>
      obtain ⟨sameInput, holds⟩ := ofRunningTask _ _ phaseMatches.2
      exact ⟨thread, by rw [sameInput]; exact isThreadFound,
        Or.inr ⟨holds.1, Or.inr (Or.inl phaseCase)⟩⟩
    case waitStarted =>
      obtain ⟨sameInput, noEffect⟩ := ofRunningTask _ _ phaseMatches.2
      exact ⟨thread, by rw [sameInput]; exact isThreadFound,
        Or.inr ⟨noEffect, Or.inr (Or.inr (Or.inl phaseCase))⟩⟩
    case blocked on since =>
      have hasNone := phaseMatches.2.1
      rw [hasInvocation] at hasNone
      cases hasNone
    case woken outcome => exact absurd phaseMatches.2.1 notPending
    case resuming outcome =>
      obtain ⟨sameInput, holds⟩ := ofRunningTask _ _ phaseMatches.2
      exact ⟨thread, by rw [sameInput]; exact isThreadFound,
        Or.inr ⟨holds.1, Or.inr (Or.inr (Or.inr (Or.inr ⟨outcome, phaseCase⟩)))⟩⟩

/-! ## F5: results commit -/

variable {program : Program} {lRun : InfiniteRun L.Config L.Label}
  {dexBlocks : BlockRun (Dex.Config signature) (Dex.Action signature) (Dex.Obs signature)}
  {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)} {boundary : Nat → Nat}

/-- F5: no task is `Returned` at the next block boundary. -/
theorem DexRunOfLRun.resultsCommit_of_noReturnedTask (built : DexRunOfLRun program lRun dexBlocks dexRun boundary) :
    Dex.Fairness.ResultsCommit dexRun := by
  intro taskId start _
  obtain ⟨index, isAfter⟩ := BlockRun.IsFlattenedTo.exists_boundary_ge built.isFlattened built.isInfinite start
  refine ⟨boundary index, isAfter, ?_⟩
  rintro ⟨task, response, isFound, isReturned⟩
  rw [built.isFlattened.stateAtBoundary index] at isFound
  exact built.follows.noReturnedTask index task (mem_of_findTask? _ taskId task isFound).1
    response isReturned

/-! ## F4: Workers settle -/

/-- A closed L run stays closed. -/
theorem DexRunOfLRun.closed_of_le (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    {first last : Nat} (isOrdered : first ≤ last)
    (isClosed : (lRun.states first).status = .closed) : (lRun.states last).status = .closed := by
  obtain ⟨distance, isLast⟩ := Nat.exists_eq_add_of_le isOrdered
  subst isLast
  induction distance with
  | zero => exact isClosed
  | succ distance inductionHypothesis =>
    have isMiddleClosed := inductionHypothesis (Nat.le_add_right first distance)
    obtain ⟨events, isStep⟩ := built.isLRun.2 (first + distance)
    exact L.step_keeps_closed program _ _ _ events isStep isMiddleClosed
      ((built.follows.isState (first + distance)).lInvariant.closedHasNoThread isMiddleClosed)

/-- A task cannot stay in `Θ` forever after the close: F4 of L empties the
late emits, and a task that is left between two blocks holds one. -/
theorem DexRunOfLRun.not_unsettled_forever_of_closed (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    (emitsSettle : L.Fairness.EmitsSettle lRun) (taskId start : Nat)
    (stays : ∀ position, start ≤ position →
      Dex.Fairness.IsTaskUnsettled dexRun taskId position)
    (index : Nat) (isAfter : start ≤ boundary index)
    (isClosed : (lRun.states index).status = .closed) : False := by
  obtain ⟨later, isLater, isEmpty⟩ := exists_lateEmits_eq_nil lRun emitsSettle index
  have isLaterClosed := DexRunOfLRun.closed_of_le built isLater isClosed
  have state := built.follows.isState later
  obtain ⟨run, status, hasRun, isStatus⟩ := state.isRelated.run_of_closed isLaterClosed
  have closedRelated := state.isRelated.closed run status hasRun isStatus
  obtain ⟨task, isFound, _⟩ := stays (boundary later)
    (Nat.le_trans isAfter (BlockRun.IsFlattenedTo.boundary_mono built.isFlattened _ _ isLater))
  rw [built.isFlattened.stateAtBoundary later] at isFound
  have isTask := (mem_of_findTask? _ taskId task isFound).1
  have isStale := closedRelated.tasksAreStale task isTask
  have noEffect : pendingEffects ⟦program⟧ task = [] := by
    have lateEmits := closedRelated.lateEmits
    rw [isEmpty] at lateEmits
    exact List.flatMap_eq_nil_iff.mp lateEmits.eq_nil task isTask
  have isSettled := built.follows.isSettled later
  simp only [cleanupActions, List.map_eq_nil_iff] at isSettled
  have isCleaned : task ∈ (dexBlocks.states later).tasks.filter (fun task =>
      !(dexBlocks.states later).isTaskCurrent task &&
        (pendingEffects ⟦program⟧ task).isEmpty) :=
    List.mem_filter.mpr ⟨isTask, by simp [isStale, noEffect]⟩
  rw [isSettled] at isCleaned
  cases isCleaned

/-- What a block boundary after `start` looks like when the task stays in `Θ`:
the program is closed, or the thread of the task's input issued an emit that
the task holds, or began an action while the task holds no effect. -/
theorem DexRunOfLRun.unsettled_boundary_cases (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    (taskId start : Nat)
    (stays : ∀ position, start ≤ position →
      Dex.Fairness.IsTaskUnsettled dexRun taskId position)
    (index : Nat) (isAfter : start ≤ boundary index) :
    (lRun.states index).status = .closed ∨
    ∃ task, (dexRun.states (boundary index)).findTask? taskId = some task ∧
      ((L.Fairness.IsThreadEmitting lRun (threadOfInput task.input) index ∧
          pendingEffects ⟦program⟧ task ≠ []) ∨
        (L.Fairness.IsThreadCompleting lRun (threadOfInput task.input) index ∧
          pendingEffects ⟦program⟧ task = [])) := by
  have state := built.follows.isState index
  obtain ⟨task, isFound, _⟩ := stays (boundary index) isAfter
  have isBlockFound := isFound
  rw [built.isFlattened.stateAtBoundary index] at isBlockFound
  cases runCase : (dexBlocks.states index).run with
  | none =>
    have noTask := state.isRelated.notStarted runCase
    have isTask := (mem_of_findTask? _ taskId task isBlockFound).1
    rw [noTask] at isTask
    cases isTask
  | some run =>
    cases statusCase : run.status with
    | closed status =>
      exact Or.inl (Related.status_of_closed_run program _ _ state.isRelated run status runCase
        statusCase)
    | running =>
      obtain ⟨current, isThread, phases⟩ :=
        state.thread_of_task run runCase statusCase taskId task isBlockFound
      refine Or.inr ⟨task, isFound, ?_⟩
      rcases phases with ⟨effect, isEmitting, hasEffect⟩ | ⟨noEffect, isCompleting⟩
      · exact Or.inl ⟨⟨current, effect, isThread, isEmitting⟩, by rw [hasEffect]; simp⟩
      · exact Or.inr ⟨⟨current, isThread, isCompleting⟩, noEffect⟩

/-- A task cannot stay in `Θ` forever without an effect: F5 of L ends the
action its thread began, and the task holds no effect at any later boundary,
so the program has closed. -/
theorem DexRunOfLRun.not_unsettled_forever_of_noEffect (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    (emitsSettle : L.Fairness.EmitsSettle lRun)
    (actionsComplete : L.Fairness.ActionsComplete lRun) (taskId start : Nat)
    (stays : ∀ position, start ≤ position →
      Dex.Fairness.IsTaskUnsettled dexRun taskId position)
    (index : Nat) (isAfter : start ≤ boundary index) (task : Dex.Task signature)
    (isFound : (dexRun.states (boundary index)).findTask? taskId = some task)
    (noEffect : pendingEffects ⟦program⟧ task = []) : False := by
  have staysFound : ∀ position, start ≤ position →
      ∃ task, (dexRun.states position).findTask? taskId = some task := by
    intro position isLater
    obtain ⟨found, isFoundLater, _⟩ := stays position isLater
    exact ⟨found, isFoundLater⟩
  rcases DexRunOfLRun.unsettled_boundary_cases built taskId start stays index isAfter with
    isClosed | ⟨found, isSameFound, phases⟩
  · exact DexRunOfLRun.not_unsettled_forever_of_closed built emitsSettle taskId start stays index isAfter isClosed
  · rw [isFound] at isSameFound
    cases isSameFound
    rcases phases with ⟨_, hasEffect⟩ | ⟨isCompleting, _⟩
    · exact hasEffect noEffect
    · obtain ⟨later, isLater, isNotCompleting⟩ :=
        actionsComplete (threadOfInput task.input) index isCompleting
      have isLaterAfter : start ≤ boundary later :=
        Nat.le_trans isAfter (BlockRun.IsFlattenedTo.boundary_mono built.isFlattened _ _ isLater)
      rcases DexRunOfLRun.unsettled_boundary_cases built taskId start stays later isLaterAfter with
        isClosed | ⟨laterTask, isLaterFound, laterPhases⟩
      · exact DexRunOfLRun.not_unsettled_forever_of_closed built emitsSettle taskId start stays later isLaterAfter
          isClosed
      · have follows := taskFollows_of_isRun ⟦program⟧ dexRun built.isDexRun taskId start
          staysFound (boundary index) (boundary later) isAfter
          (BlockRun.IsFlattenedTo.boundary_mono built.isFlattened _ _ isLater) task laterTask isFound isLaterFound
        rcases laterPhases with ⟨_, hasEffect⟩ | ⟨isLaterCompleting, _⟩
        · exact hasEffect (follows.2 noEffect)
        · rw [follows.1] at isLaterCompleting
          exact isNotCompleting isLaterCompleting

/-- No task stays in `Θ` forever without returning. -/
theorem DexRunOfLRun.not_unsettled_forever (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    (emitsSettle : L.Fairness.EmitsSettle lRun)
    (actionsComplete : L.Fairness.ActionsComplete lRun) (taskId start : Nat)
    (stays : ∀ position, start ≤ position →
      Dex.Fairness.IsTaskUnsettled dexRun taskId position) :
    False := by
  have staysFound : ∀ position, start ≤ position →
      ∃ task, (dexRun.states position).findTask? taskId = some task := by
    intro position isLater
    obtain ⟨found, isFoundLater, _⟩ := stays position isLater
    exact ⟨found, isFoundLater⟩
  obtain ⟨index, isAfter⟩ := BlockRun.IsFlattenedTo.exists_boundary_ge built.isFlattened built.isInfinite start
  rcases DexRunOfLRun.unsettled_boundary_cases built taskId start stays index isAfter with
    isClosed | ⟨task, isFound, phases⟩
  · exact DexRunOfLRun.not_unsettled_forever_of_closed built emitsSettle taskId start stays index isAfter isClosed
  · rcases phases with ⟨isEmitting, _⟩ | ⟨_, noEffect⟩
    · obtain ⟨later, isLater, isNotEmitting⟩ :=
        emitsSettle.1 (threadOfInput task.input) index isEmitting
      have isLaterAfter : start ≤ boundary later :=
        Nat.le_trans isAfter (BlockRun.IsFlattenedTo.boundary_mono built.isFlattened _ _ isLater)
      rcases DexRunOfLRun.unsettled_boundary_cases built taskId start stays later isLaterAfter with
        isClosed | ⟨laterTask, isLaterFound, laterPhases⟩
      · exact DexRunOfLRun.not_unsettled_forever_of_closed built emitsSettle taskId start stays later isLaterAfter
          isClosed
      · have follows := taskFollows_of_isRun ⟦program⟧ dexRun built.isDexRun taskId start
          staysFound (boundary index) (boundary later) isAfter
          (BlockRun.IsFlattenedTo.boundary_mono built.isFlattened _ _ isLater) task laterTask isFound isLaterFound
        rcases laterPhases with ⟨isLaterEmitting, _⟩ | ⟨_, noLaterEffect⟩
        · rw [follows.1] at isLaterEmitting
          exact isNotEmitting isLaterEmitting
        · exact DexRunOfLRun.not_unsettled_forever_of_noEffect built emitsSettle actionsComplete taskId start stays
            later isLaterAfter laterTask isLaterFound noLaterEffect
    · exact DexRunOfLRun.not_unsettled_forever_of_noEffect built emitsSettle actionsComplete taskId start stays index
        isAfter task isFound noEffect

/-- F4: every task of `Θ` eventually returns or leaves `Θ`. -/
theorem DexRunOfLRun.workersSettle_of_fair (built : DexRunOfLRun program lRun dexBlocks dexRun boundary)
    (emitsSettle : L.Fairness.EmitsSettle lRun)
    (actionsComplete : L.Fairness.ActionsComplete lRun) :
    Dex.Fairness.WorkersSettle dexRun := by
  intro taskId start _
  apply Classical.byContradiction
  intro isNeverSettled
  exact DexRunOfLRun.not_unsettled_forever built emitsSettle actionsComplete taskId start
    (fun position isLater => Classical.byContradiction
      (fun isSettled => isNeverSettled ⟨position, isLater, isSettled⟩))

end DexExpressiveness
