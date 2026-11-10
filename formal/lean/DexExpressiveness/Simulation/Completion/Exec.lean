import DexExpressiveness.Simulation.Relation

/-!
# The Dex actions that complete an Execute

The completion of an L action is simulated by a fixed action sequence
(`completionActions`): the Worker steps that are still missing to return the
handler (`returnActions`), then one [STEP-COMPLETE-EXECUTE].
`exec_completionActions` computes the configuration the sequence ends in.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- The actions that complete the invocation a task runs: return the handler,
then commit its response. -/
def completionActions (task : Dex.Task signature) : List (Dex.Action signature) :=
  returnTaskActions task ++ [.stepComplete task.id]

/-- The completion actions attempt no client request. -/
theorem wp3b_completionActions_requests (program : Program) (task : Dex.Task signature) :
    (completionActions task).filterMap (Dex.system ⟦program⟧).requestOf = [] := by
  unfold completionActions returnTaskActions
  cases task.phase <;> simp [Dex.system]

/-- A task of the configuration is what lookup by its ID finds. -/
theorem wp3b_findTask?_of_mem (config : Dex.Config signature) (task : Dex.Task signature)
    (tasksDistinct : TasksAreDistinct config) (isMember : task ∈ config.tasks) :
    config.findTask? task.id = some task := by
  cases foundCase : config.findTask? task.id with
  | none =>
    have noMatch := List.find?_eq_none.mp foundCase task isMember
    simp at noMatch
  | some found =>
    rw [eq_of_isFound config task.id task found tasksDistinct isMember rfl foundCase]

/-- Set the phase of a task. -/
def setTaskPhase (config : Dex.Config signature) (taskId : Nat)
    (phase : Dex.TaskPhase signature) : Dex.Config signature :=
  config.updateTask taskId (fun current => { current with phase := phase })

/-- Lookup of a task after its phase was set. -/
theorem wp3b_findTask?_setTaskPhase (config : Dex.Config signature) (taskId : Nat)
    (task : Dex.Task signature) (phase : Dex.TaskPhase signature)
    (isFound : config.findTask? taskId = some task) :
    (setTaskPhase config taskId phase).findTask? taskId = some { task with phase := phase } := by
  have hasId := (mem_of_findTask? config taskId task isFound).2
  unfold Dex.Config.findTask? setTaskPhase Dex.Config.updateTask at *
  simp only [List.find?_map]
  have samePredicate : ((fun candidate : Dex.Task signature => decide (candidate.id = taskId)) ∘
      fun candidate => if candidate.id = taskId then { candidate with phase := phase }
        else candidate) =
      fun candidate => decide (candidate.id = taskId) := by
    funext candidate
    by_cases isTask : candidate.id = taskId <;> simp [isTask]
  rw [samePredicate, isFound]
  simp [hasId]

/-- Removing a task after its phase was set removes the original task. -/
theorem wp3b_filter_setTaskPhase (config : Dex.Config signature) (taskId : Nat)
    (phase : Dex.TaskPhase signature) :
    (setTaskPhase config taskId phase).tasks.filter (fun other => !decide (other.id = taskId)) =
      config.tasks.filter (fun other => !decide (other.id = taskId)) := by
  unfold setTaskPhase Dex.Config.updateTask
  simp only [List.filter_map]
  induction config.tasks with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    by_cases isTask : head.id = taskId
    · simp [isTask, inductionHypothesis]
    · simp [isTask, inductionHypothesis]

/-- Whether a task is current does not depend on the other tasks. -/
theorem wp3b_isTaskCurrent_congr (config other : Dex.Config signature)
    (task changed : Dex.Task signature) (sameRun : other.run = config.run)
    (sameStep : changed.step = task.step) (sameMethod : changed.method = task.method)
    (sameId : changed.id = task.id) :
    other.isTaskCurrent changed = config.isTaskCurrent task := by
  unfold Dex.Config.isTaskCurrent
  rw [sameRun, sameStep, sameMethod, sameId]

/-- The run after the commit that completes an Execute with this response
(spec §4.4 [STEP-COMPLETE-EXECUTE]): `seq := seq + 1`, `S[e].inv := ⊥`,
`effects`, `decide`. -/
def completedRun (run : Dex.Run signature) (step : Dex.StepExecId signature)
    (response : Dex.ExecuteResponse signature) : Dex.Run signature :=
  ((({ run with seq := run.seq + 1 } : Dex.Run signature).updateStep step
    (fun record => { record with invocation := none })).applyEffects response.effects).applyDecision
      step response.decision

/-- The Execute response of a task, as its handler computes it. -/
def executeResponse (program : Program) (task : Dex.Task signature) :
    Dex.ExecuteResponse signature :=
  ((⟦program⟧.stepDef task.step.stepType).execute task.context task.input task.snapshot
    task.conditionResults).2

/-- [STEP-COMPLETE-EXECUTE] of a returned task. -/
theorem wp3b_step_stepComplete (program : Program) (config : Dex.Config signature)
    (run : Dex.Run signature) (task : Dex.Task signature)
    (response : Dex.ExecuteResponse signature) (hasRun : config.run = some run)
    (isFound : config.findTask? task.id = some task) (isExecute : task.method = .execute)
    (isReturned : task.phase = .returned (.execute response))
    (isCurrent : config.isTaskCurrent task = true) (canCommit : config.canCommit = true) :
    (Dex.system ⟦program⟧).step config (.stepComplete task.id) =
      some (Dex.Config.closeEvents run (completedRun run task.step response),
        { config with
          run := some (completedRun run task.step response)
          lastCommitAt := some config.clock
          tasks := config.tasks.filter (fun other => !decide (other.id = task.id)) }) := by
  have canCommitRemoved : (config.removeTask task.id).canCommit = true := canCommit
  simp only [Dex.system, Dex.step, hasRun, isFound, isCurrent, canCommit, isExecute, isReturned,
    Bool.and_self, ↓reduceIte, Dex.Config.commit, completedRun, List.nil_append]
  rfl

/-- The completion actions of a task that runs an Execute with no effect left
return its handler and commit its response in one [STEP-COMPLETE-EXECUTE]. -/
theorem wp3b_exec_completionActions (program : Program) (config : Dex.Config signature)
    (run : Dex.Run signature) (task : Dex.Task signature) (hasRun : config.run = some run)
    (isMember : task ∈ config.tasks) (tasksDistinct : TasksAreDistinct config)
    (isComputed : IsTaskComputed program task) (isExecute : task.method = .execute)
    (noEffects : pendingEffects ⟦program⟧ task = [])
    (isCurrent : config.isTaskCurrent task = true) (canCommit : config.canCommit = true) :
    (Dex.system ⟦program⟧).exec config (completionActions task) =
      some (Dex.Config.closeEvents run (completedRun run task.step (executeResponse program task)),
        { config with
          run := some (completedRun run task.step (executeResponse program task))
          lastCommitAt := some config.clock
          tasks := config.tasks.filter (fun other => !decide (other.id = task.id)) }) := by
  have isFound := wp3b_findTask?_of_mem config task tasksDistinct isMember
  have isComputation : task.compute ⟦program⟧ =
      (((⟦program⟧.stepDef task.step.stepType).execute task.context task.input task.snapshot
        task.conditionResults).1, .execute (executeResponse program task)) := by
    simp [Dex.Task.compute, isExecute, executeResponse]
  -- The last action, from any configuration whose task has returned.
  have lastStep : ∀ (returned : Dex.Config signature) (returnedTask : Dex.Task signature),
      returned.run = some run → returned.clock = config.clock →
      returned.lastCommitAt = config.lastCommitAt → returned.nextTaskId = config.nextTaskId →
      returned.findTask? task.id = some returnedTask → returnedTask.id = task.id →
      returnedTask.step = task.step → returnedTask.method = .execute →
      returnedTask.phase = .returned (.execute (executeResponse program task)) →
      returned.tasks.filter (fun other => !decide (other.id = task.id)) =
        config.tasks.filter (fun other => !decide (other.id = task.id)) →
      (Dex.system ⟦program⟧).step returned (.stepComplete task.id) =
        some (Dex.Config.closeEvents run
            (completedRun run task.step (executeResponse program task)),
          { config with
            run := some (completedRun run task.step (executeResponse program task))
            lastCommitAt := some config.clock
            tasks := config.tasks.filter (fun other => !decide (other.id = task.id)) }) := by
    intro returned returnedTask returnedRun sameClock sameCommit sameNext returnedFound sameId
      sameStep returnedExecute returnedPhase sameTasks
    have returnedCurrent : returned.isTaskCurrent returnedTask = true := by
      rw [wp3b_isTaskCurrent_congr config returned task returnedTask (returnedRun.trans hasRun.symm)
        sameStep (returnedExecute.trans isExecute.symm) sameId]
      exact isCurrent
    have returnedCommit : returned.canCommit = true := by
      unfold Dex.Config.canCommit at canCommit ⊢
      rw [sameCommit, sameClock]
      exact canCommit
    rw [← sameId] at returnedFound
    have isStep := wp3b_step_stepComplete program returned run returnedTask
      (executeResponse program task) returnedRun returnedFound returnedExecute returnedPhase
      returnedCurrent returnedCommit
    rw [sameId] at isStep
    rw [isStep, sameStep, sameTasks, sameClock]
    cases returned
    cases config
    simp_all
  unfold completionActions returnTaskActions
  cases phaseCase : task.phase with
  | started =>
    have noComputedEffects : (task.compute ⟦program⟧).1 = [] := by
      simpa [pendingEffects, phaseCase] using noEffects
    have firstFound := wp3b_findTask?_setTaskPhase config task.id task
      (.running [] (task.compute ⟦program⟧).2) isFound
    have secondFound := wp3b_findTask?_setTaskPhase _ task.id _
      (.returned (task.compute ⟦program⟧).2) firstFound
    have isLast := lastStep
      (setTaskPhase (setTaskPhase config task.id (.running [] (task.compute ⟦program⟧).2)) task.id
        (.returned (task.compute ⟦program⟧).2)) _ hasRun rfl rfl rfl secondFound rfl rfl isExecute
      (by simp [isComputation])
      (by rw [wp3b_filter_setTaskPhase, wp3b_filter_setTaskPhase])
    have runStep : (Dex.system ⟦program⟧).step config (.workerRun task.id) =
        some ([], setTaskPhase config task.id (.running [] (task.compute ⟦program⟧).2)) := by
      simp [Dex.system, Dex.step, isFound, phaseCase, setTaskPhase, noComputedEffects]
    have returnStep : (Dex.system ⟦program⟧).step
        (setTaskPhase config task.id (.running [] (task.compute ⟦program⟧).2))
        (.workerReturn task.id) =
        some ([], setTaskPhase (setTaskPhase config task.id
          (.running [] (task.compute ⟦program⟧).2)) task.id
          (.returned (task.compute ⟦program⟧).2)) := by
      simp only [Dex.system, Dex.step, firstFound]
      rfl
    simp only [List.cons_append, List.nil_append, TransitionSystem.exec, runStep, returnStep,
      isLast]
    simp
  | running remainingEffects response =>
    have noRemaining : remainingEffects = [] := by
      simpa [pendingEffects, phaseCase] using noEffects
    subst noRemaining
    have isResponse : response = .execute (executeResponse program task) := by
      have computed : response = (task.compute ⟦program⟧).2 := by
        unfold IsTaskComputed at isComputed
        rw [phaseCase] at isComputed
        exact isComputed.1
      rw [computed, isComputation]
    have firstFound := wp3b_findTask?_setTaskPhase config task.id task (.returned response) isFound
    have isLast := lastStep (setTaskPhase config task.id (.returned response)) _ hasRun rfl rfl rfl
      firstFound rfl rfl isExecute (by simp [isResponse]) (wp3b_filter_setTaskPhase _ _ _)
    have returnStep : (Dex.system ⟦program⟧).step config (.workerReturn task.id) =
        some ([], setTaskPhase config task.id (.returned response)) := by
      simp [Dex.system, Dex.step, isFound, phaseCase, setTaskPhase]
    simp only [List.cons_append, List.nil_append, TransitionSystem.exec, returnStep, isLast]
    simp
  | returned response =>
    have isResponse : response = .execute (executeResponse program task) := by
      have computed : response = (task.compute ⟦program⟧).2 := by
        simpa [IsTaskComputed, phaseCase] using isComputed
      rw [computed, isComputation]
    have isLast := lastStep config task hasRun rfl rfl rfl isFound rfl rfl isExecute
      (by rw [phaseCase, isResponse]) rfl
    simp only [List.nil_append, TransitionSystem.exec, isLast]
    simp

end DexExpressiveness
