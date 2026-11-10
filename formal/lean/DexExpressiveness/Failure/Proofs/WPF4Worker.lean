import DexExpressiveness.Failure.Proofs.WPF4Transfer
import DexExpressiveness.Failure.Observation

/-!
# WP-F4: the Worker rules keep the invariants

[TICK], [WORKER-EFFECT] and [ZOMBIE-EFFECT] (`workerRun`, `workerEffect`,
`workerReturn`), and [WORKER-ABANDON] with the drop of a zombie
(`workerAbandon`). None of them commits.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-! ## H3 and the handler call -/

/-- Under H3 a real task computes what its invocation's arguments compute. -/
theorem compute_of_arguments {program : Program σ}
    (ignoresAttemptContext : IgnoresAttemptContext program) (task : Task σ)
    (arguments : Arguments σ) (sameContext : task.base.context = arguments.context)
    (sameInput : task.base.input = arguments.input)
    (sameSnapshot : task.base.snapshot = arguments.snapshot)
    (sameConditionResults : task.base.conditionResults = arguments.conditionResults) :
    task.base.compute (program task.attemptContext) =
      arguments.compute program.failureFreeFlow task.key := by
  have isFlow : program task.attemptContext = program.failureFreeFlow :=
    ignoresAttemptContext task.attemptContext (AttemptContext.first σ)
  rw [isFlow]
  unfold Arguments.compute Arguments.toTask Dex.Task.compute Task.key
  simp only [sameContext, sameInput, sameSnapshot, sameConditionResults]

/-- `TaskFollowsHistory` survives a history change that keeps the arguments,
does not lower `h`, and moves `v` forward. -/
theorem taskFollowsHistory_of_progress {flow : Dex.FlowDef σ} {task : Task σ}
    {history changed : InvocationHistory σ} (follows : TaskFollowsHistory flow task history)
    (sameArguments : changed.arguments = history.arguments)
    (emittedGrows : history.emittedCount ≤ changed.emittedCount)
    (keepsRun : history.progress.isNotRun = false → changed.progress.isNotRun = false)
    (keepsReturned : history.progress.isReturned = true → changed.progress.isReturned = true) :
    TaskFollowsHistory flow task changed := by
  obtain ⟨arguments, hasArguments, sameContext, sameInput, sameSnapshot, sameConditionResults,
    isBelow, phaseMatches⟩ := follows
  refine ⟨arguments, by rw [sameArguments]; exact hasArguments, sameContext, sameInput,
    sameSnapshot, sameConditionResults, Nat.le_trans isBelow emittedGrows, ?_⟩
  revert phaseMatches
  cases task.base.phase with
  | started => exact id
  | running remaining response =>
    rintro ⟨isRemaining, isResponse, isLength, isRun⟩
    exact ⟨isRemaining, isResponse, isLength, keepsRun isRun⟩
  | returned response =>
    rintro ⟨isResponse, isLength, isReturned⟩
    exact ⟨isResponse, isLength, keepsReturned isReturned⟩

/-! ## A Worker step that changes one task -/

/-- `Invariant` after a Worker step that changes the phase and counter of one
real task and the progress part of the ghost. -/
theorem invariant_updateRealTask {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) (taskId : Nat) (change : Task σ → Task σ)
    (keepsFields : ∀ task, (change task).base.id = task.base.id ∧
      (change task).base.step = task.base.step ∧ (change task).base.method = task.base.method ∧
      (change task).attemptContext.attempt = task.attemptContext.attempt)
    (ghost : Ghost σ) (frame : GhostFrame config.ghost ghost)
    (sameLastNormal : ghost.lastNormalCommitAt = config.ghost.lastNormalCommitAt)
    (sameNextVirtual : ghost.nextVirtualTaskId = config.ghost.nextVirtualTaskId)
    (sameArguments : ghost.invocations.filterMap (fun entry => entry.2.arguments) =
      config.ghost.invocations.filterMap (fun entry => entry.2.arguments))
    (historiesConsistent : ∀ entry, entry ∈ ghost.invocations →
      HistoryIsConsistent program.failureFreeFlow entry.1 entry.2)
    (realTasksFollowHistory : ∀ task, task ∈ (config.updateRealTask taskId change).realTasks →
      ∃ history, ghost.findInvocation? task.key = some history ∧
        TaskFollowsHistory program.failureFreeFlow task history) :
    Invariant program { config.updateRealTask taskId change with ghost := ghost } := by
  have keepsAll : ∀ task : Task σ,
      (if task.base.id = taskId then change task else task).base.id = task.base.id ∧
      (if task.base.id = taskId then change task else task).base.step = task.base.step ∧
      (if task.base.id = taskId then change task else task).base.method = task.base.method ∧
      (if task.base.id = taskId then change task else task).attemptContext.attempt =
        task.attemptContext.attempt := by
    intro task
    split
    · exact keepsFields task
    · exact ⟨rfl, rfl, rfl, rfl⟩
  have fromMapped : ∀ (tasks : List (Task σ)) task,
      task ∈ tasks.map (fun task => if task.base.id = taskId then change task else task) →
      ∃ original, original ∈ tasks ∧ original.base.id = task.base.id ∧
        original.base.step = task.base.step ∧ original.base.method = task.base.method ∧
        original.attemptContext.attempt = task.attemptContext.attempt := by
    intro tasks task isTask
    obtain ⟨original, isOriginal, isMapped⟩ := List.mem_map.mp isTask
    obtain ⟨sameId, sameStep, sameMethod, sameAttempt⟩ := keepsAll original
    rw [isMapped] at sameId sameStep sameMethod sameAttempt
    exact ⟨original, isOriginal, sameId.symm, sameStep.symm, sameMethod.symm, sameAttempt.symm⟩
  have sameIds : ∀ (tasks : List (Task σ)),
      (tasks.map (fun task => if task.base.id = taskId then change task else task)).map
        (fun task => task.base.id) = tasks.map (fun task => task.base.id) := by
    intro tasks
    rw [List.map_map]
    apply List.map_congr_left
    intro task _
    exact (keepsAll task).1
  refine invariant_transfer invariant rfl rfl frame sameLastNormal sameNextVirtual sameArguments
    ?_ ?_ ?_ (fun _ isMember => isMember) ?_ ?_ historiesConsistent realTasksFollowHistory ?_
  · show ((config.updateRealTask taskId change).realTasks.map (fun task => task.base.id)).Sublist _
    rw [realTasks_updateRealTask, sameIds]; exact List.Sublist.refl _
  · intro task isTask
    rw [show ({ config.updateRealTask taskId change with ghost := ghost } : Config σ).realTasks =
      (config.updateRealTask taskId change).realTasks from rfl, realTasks_updateRealTask] at isTask
    exact fromMapped config.realTasks task isTask
  · intro task isTask
    exact fromMapped config.tasks task isTask
  · intro isDown
    have noTasks := (invariant.downIsEmpty isDown).1
    refine ⟨?_, (invariant.downIsEmpty isDown).2⟩
    show config.tasks.map _ = []
    rw [noTasks]
    rfl
  · show IsStampAtMost ghost.lastNormalCommitAt config.lastCommitAt; rw [sameLastNormal]; exact invariant.lastNormalCommitAtMost
  · intro run isRun record method taskIdRunning isMember hasInvocation
    rcases (invariant.runInvariant run isRun).runningIsTracked record method taskIdRunning isMember
        hasInvocation with ⟨task, isTask, isId⟩ | isDown | isInterrupt
    · left
      refine ⟨if task.base.id = taskId then change task else task,
        List.mem_map.mpr ⟨task, isTask, rfl⟩, ?_⟩
      rw [(keepsAll task).1]
      exact isId
    · exact Or.inr (Or.inl isDown)
    · exact Or.inr (Or.inr isInterrupt)

/-- A history of the ghost is consistent. -/
theorem historyIsConsistent_of_find {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) {key : InvocationKey σ} {history : InvocationHistory σ}
    (isFound : config.ghost.findInvocation? key = some history) :
    HistoryIsConsistent program.failureFreeFlow key history :=
  invariant.historiesConsistent (key, history) (mem_of_get? _ _ _ isFound)

/-- The real task found by ID is the only real task with that ID. -/
theorem eq_found_of_id {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) {taskId : Nat} {task other : Task σ}
    (isFound : config.findRealTask? taskId = some task) (isOther : other ∈ config.realTasks)
    (isId : other.base.id = taskId) : other = task := by
  obtain ⟨isTask, isTaskId⟩ := mem_of_findRealTask? isFound
  exact task_eq_of_id_eq invariant.realTaskIdsNodup isOther isTask (by rw [isId, isTaskId])

/-- The members of the real tasks after `updateRealTask`, by the task found. -/
theorem mem_updateRealTask {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) {taskId : Nat} {task : Task σ}
    (isFound : config.findRealTask? taskId = some task) (change : Task σ → Task σ)
    {changed : Task σ} (isChanged : changed ∈ (config.updateRealTask taskId change).realTasks) :
    (changed = change task) ∨ (changed ∈ config.realTasks ∧ changed.base.id ≠ taskId) := by
  rw [realTasks_updateRealTask] at isChanged
  obtain ⟨original, isOriginal, isMapped⟩ := List.mem_map.mp isChanged
  by_cases isId : original.base.id = taskId
  · left
    rw [eq_found_of_id invariant isFound isOriginal isId] at isMapped
    simp only [mem_of_findRealTask? isFound |>.2, ↓reduceIte] at isMapped
    exact isMapped.symm
  · right
    simp only [isId, ↓reduceIte] at isMapped
    rw [← isMapped]
    exact ⟨isOriginal, isId⟩

end DexExpressiveness.DexF.WPF4
