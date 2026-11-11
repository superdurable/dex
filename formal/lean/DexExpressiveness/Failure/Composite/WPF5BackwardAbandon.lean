import DexExpressiveness.Failure.Composite.WPF5BackwardWorker

/-!
# WP-F5: Dex drops from α of a run without failures

Dex [WORKER-ABANDON] and [STEP-COMPLETE-STALE] of a virtual task whose
invocation is no longer current both remove the task and emit nothing. Both
map to DexF `workerAbandon` of its real task, which fails the fence and is the
last real task of its canceled invocation. `workerAbandon` takes no stamp.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- A real task whose virtual task is not current fails the fence. -/
theorem wpf5_fence_of_notCurrent (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (task : Task σ) (history : InvocationHistory σ)
    (arguments : Arguments σ) (isHistory : config.ghost.findInvocation? task.key = some history)
    (isArguments : history.arguments = some arguments)
    (isVirtualId : arguments.virtualTaskId = task.base.id)
    (isNotCurrent : (abstractConfig config).isTaskCurrent
      (arguments.toTask task.key history.virtualPhase) = false) :
    config.isFencePassed task = false := by
  cases isFence : config.isFencePassed task with
  | false => rfl
  | true =>
    exfalso
    unfold Config.isFencePassed at isFence
    split at isFence
    · cases isFence
    · rename_i run hasRun
      split at isFence
      · rename_i record attempt isRecord isAttempt
        simp only [Bool.and_eq_true, decide_eq_true_eq] at isFence
        obtain ⟨isInvocation, _⟩ := isFence
        have isMember := List.mem_of_find?_eq_some isRecord
        have isRecordId : record.id = task.base.step := by
          simpa using List.find?_some isRecord
        have recordMatches := (invariant.runInvariant run hasRun).attemptsMatch record isMember
        unfold AttemptMatchesRecord at recordMatches
        rw [isInvocation] at recordMatches
        obtain ⟨matchedAttempt, _, isMatchedAttempt, _, _, _, _, isRunning, _⟩ := recordMatches
        rw [isRecordId, isAttempt] at isMatchedAttempt
        cases isMatchedAttempt
        have holdsSnapshot := (isRunning task.base.id rfl).2
        have isAbstract : run.abstractInvocation config.ghost record =
            some { method := task.base.method, state := .running task.base.id } := by
          unfold Run.abstractInvocation
          rw [isInvocation]
          simp only [isRecordId, isAttempt, holdsSnapshot, Ghost.argumentsOf]
          simp only [Task.key] at isHistory
          simp [isHistory, isArguments, isVirtualId]
        have isCurrent : (abstractConfig config).isTaskCurrent
            (arguments.toTask task.key history.virtualPhase) = true := by
          simp only [Dex.Config.isTaskCurrent, abstractConfig, hasRun, Option.map_some,
            Arguments.toTask, Task.key]
          rw [wpf5_abstract_findStep, isRecord]
          simp [isAbstract, isVirtualId]
        rw [isCurrent] at isNotCurrent
        cases isNotCurrent
      · cases isFence

/-- A real task that fails the fence is the last real task of its canceled
invocation. -/
theorem wpf5_isLastOfCanceled (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config) (task : Task σ)
    (isTask : task ∈ config.tasks) (history : InvocationHistory σ) (arguments : Arguments σ)
    (isHistory : config.ghost.findInvocation? task.key = some history)
    (isArguments : history.arguments = some arguments)
    (isNotCommitted : history.outcome ≠ .committed)
    (failsFence : config.isFencePassed task = false) :
    history.outcome = .canceled ∧ config.isLastOfCanceled task = true := by
  have isCanceled : history.outcome = .canceled := by
    cases isOutcome : history.outcome with
    | live =>
      have passes := invariant.liveTasksPassFence task history isTask isHistory isOutcome
      rw [passes] at failsFence
      cases failsFence
    | committed => exact absurd isOutcome isNotCommitted
    | canceled => rfl
  refine ⟨isCanceled, ?_⟩
  unfold Config.isLastOfCanceled
  rw [isHistory]
  simp only [isCanceled, isArguments, decide_true, Option.isSome_some, Bool.true_and,
    Bool.not_eq_true', List.any_eq_false, Bool.and_eq_true, decide_eq_true_eq,
    decide_eq_false_iff_not, not_and, Classical.not_not]
  intro other isOther isSameKey
  simp only [Config.realTasks, shape.noZombies, List.append_nil] at isOther
  rw [wpf5_eq_of_key config shape other task isOther isTask isSameKey]

/-- Removing the last real task of a canceled invocation keeps the shape. -/
theorem wpf5_shape_removeTask (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config) (task : Task σ)
    (isTask : task ∈ config.tasks) (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? task.key = some history)
    (isCanceled : history.outcome = .canceled) :
    WPF5FreeShape (config.removeRealTask task.base.id) := by
  refine
    { isUp := shape.isUp
      noZombies := by simp [Config.removeRealTask, shape.noZombies]
      noInterrupts := shape.noInterrupts
      lastCommitIsNormal := shape.lastCommitIsNormal
      noRetryTimers := shape.noRetryTimers
      nextTaskIds := shape.nextTaskIds
      tasksMatch := ?_
      liveHaveTasks := ?_
      taskKeysNodup := ?_ }
  · intro other isOther
    simp only [Config.removeRealTask, List.mem_filter] at isOther
    exact shape.tasksMatch other isOther.1
  · intro key after isAfter isLive hasArguments
    obtain ⟨other, isOther, isOtherKey⟩ := shape.liveHaveTasks key after isAfter isLive hasArguments
    refine ⟨other, ?_, isOtherKey⟩
    simp only [Config.removeRealTask, List.mem_filter, Bool.not_eq_true', decide_eq_false_iff_not]
    refine ⟨isOther, ?_⟩
    intro isSameId
    have isSame := wpf5_eq_of_id config invariant shape other task isOther isTask isSameId
    subst isSame
    have isAfterBase : config.ghost.findInvocation? key = some after := isAfter
    rw [isOtherKey, isAfterBase] at isHistory
    cases isHistory
    rw [isLive] at isCanceled
    cases isCanceled
  · exact shape.taskKeysNodup.sublist (List.Sublist.map _ List.filter_sublist)

/-- The DexF drop of a virtual task that is not current. -/
theorem wpf5_free_drop (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config) (virtualTaskId : Nat)
    (virtualTask : Dex.Task σ)
    (isFound : (abstractConfig config).findTask? virtualTaskId = some virtualTask)
    (isNotCurrent : (abstractConfig config).isTaskCurrent virtualTask = false)
    (dexAction : Dex.Action σ) (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) dexAction =
      some (dexEvents, dexNext))
    (isSameDex : Dex.step program.failureFreeFlow (abstractConfig config)
      (.workerAbandon virtualTaskId) =
      Dex.step program.failureFreeFlow (abstractConfig config) dexAction)
    (hasNoRequest : (Dex.system program.failureFreeFlow).requestOf dexAction = none) :
    WPF5FreeStepResult program config dexAction dexEvents dexNext := by
  obtain ⟨task, history, arguments, isTask, isTaskId, isHistory, isArguments, isVirtualId,
    _, _, isNotCommitted, isVirtual⟩ :=
    wpf5_virtualTask_lookup program config invariant shape virtualTaskId virtualTask isFound
  subst isVirtual isTaskId
  have failsFence := wpf5_fence_of_notCurrent program config invariant task history arguments
    isHistory isArguments isVirtualId isNotCurrent
  obtain ⟨isCanceled, isLast⟩ := wpf5_isLastOfCanceled program config invariant shape task isTask
    history arguments isHistory isArguments isNotCommitted failsFence
  have isLive := (wpf5_findLiveTask config invariant shape task isTask).1
  have isStep : step program config (.workerAbandon task.base.id true) =
      some ([], config.removeRealTask task.base.id) := by
    simp [step, isLive, failsFence, isLast]
  have isMap : dexActionsOf program config (.workerAbandon task.base.id true) =
      [.workerAbandon task.base.id] := by
    simp only [dexActionsOf, ite_true, Config.onVirtualTask,
      wpf5_virtualTaskIdOf config invariant shape task isTask history arguments isHistory
        isArguments, isVirtualId]
  exact wpf5_free_result program ignoresAttemptContext config invariant _ dexEvents dexNext
    isDexStep _ _ _ isStep _ isMap isSameDex
    (wpf5_erase_of_notEffect program config _ (by intro _ _ isEqual; cases isEqual) _ _ isStep)
    (wpf5_shape_removeTask program config invariant shape task isTask history isHistory isCanceled)
    (by rw [hasNoRequest]; rfl) rfl rfl

theorem wpf5_free_workerAbandon (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config) (virtualTaskId : Nat)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config)
      (.workerAbandon virtualTaskId) = some (dexEvents, dexNext)) :
    WPF5FreeStepResult program config (.workerAbandon virtualTaskId) dexEvents dexNext := by
  cases isFound : (abstractConfig config).findTask? virtualTaskId with
  | none => simp [Dex.step, isFound] at isDexStep
  | some virtualTask =>
    have isNotCurrent : (abstractConfig config).isTaskCurrent virtualTask = false := by
      simp only [Dex.step, isFound] at isDexStep
      split at isDexStep
      · cases isDexStep
      · simpa using (by assumption : ¬ (abstractConfig config).isTaskCurrent virtualTask = true)
    exact wpf5_free_drop program ignoresAttemptContext config invariant shape virtualTaskId
      virtualTask isFound isNotCurrent _ dexEvents dexNext isDexStep rfl rfl

theorem wpf5_free_stepCompleteStale (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config) (virtualTaskId : Nat)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config)
      (.stepCompleteStale virtualTaskId) = some (dexEvents, dexNext)) :
    WPF5FreeStepResult program config (.stepCompleteStale virtualTaskId) dexEvents dexNext := by
  cases isFound : (abstractConfig config).findTask? virtualTaskId with
  | none => simp [Dex.step, isFound] at isDexStep
  | some virtualTask =>
    obtain ⟨response, isReturned⟩ : ∃ response, virtualTask.phase = .returned response := by
      simp only [Dex.step, isFound] at isDexStep
      split at isDexStep
      · exact ⟨_, by assumption⟩
      · cases isDexStep
    have isNotCurrent : (abstractConfig config).isTaskCurrent virtualTask = false := by
      simp only [Dex.step, isFound, isReturned] at isDexStep
      split at isDexStep
      · cases isDexStep
      · simpa using (by assumption : ¬ (abstractConfig config).isTaskCurrent virtualTask = true)
    exact wpf5_free_drop program ignoresAttemptContext config invariant shape virtualTaskId
      virtualTask isFound isNotCurrent _ dexEvents dexNext isDexStep
      (by simp [Dex.step, isFound, isReturned, isNotCurrent]) rfl

end DexExpressiveness.DexF
