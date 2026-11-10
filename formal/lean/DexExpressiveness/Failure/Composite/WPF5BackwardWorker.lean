import DexExpressiveness.Failure.Composite.WPF5Backward

/-!
# WP-F5: Dex Worker steps from α of a run without failures

A Dex Worker step of a virtual task is the same DexF Worker step of its real
task, with the first-occurrence Boolean set: the real task and the virtual
task have the same phase and effect counter.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

theorem wpf5_virtualPhase_started (history : InvocationHistory σ)
    (isStarted : history.virtualPhase = .started) : history.progress = .notRun := by
  unfold InvocationHistory.virtualPhase at isStarted
  split at isStarted <;> first | assumption | cases isStarted

theorem wpf5_virtualPhase_running (history : InvocationHistory σ)
    (remaining : List σ.Fx) (response : Dex.Response σ)
    (isRunning : history.virtualPhase = .running remaining response) :
    ∃ effects, history.progress = .ran effects response ∧
      effects.drop history.emittedCount = remaining := by
  unfold InvocationHistory.virtualPhase at isRunning
  split at isRunning
  · cases isRunning
  · rename_i effects ranResponse isProgress
    injection isRunning with isRemaining isResponse
    subst isResponse
    exact ⟨effects, isProgress, isRemaining⟩
  · cases isRunning

theorem wpf5_virtualPhase_returned (history : InvocationHistory σ) (response : Dex.Response σ)
    (isReturned : history.virtualPhase = .returned response) :
    history.progress = .returned response := by
  unfold InvocationHistory.virtualPhase at isReturned
  split at isReturned
  · cases isReturned
  · cases isReturned
  · rename_i returnedResponse isProgress
    injection isReturned with isResponse
    subst isResponse
    exact isProgress

theorem wpf5_updateInvocation_lastNormalCommitAt (ghost : Ghost σ) (key : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ) :
    (ghost.updateInvocation key change).lastNormalCommitAt = ghost.lastNormalCommitAt := by
  unfold Ghost.updateInvocation
  split <;> rfl

theorem wpf5_updateInvocation_nextVirtualTaskId (ghost : Ghost σ) (key : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ) :
    (ghost.updateInvocation key change).nextVirtualTaskId = ghost.nextVirtualTaskId := by
  unfold Ghost.updateInvocation
  split <;> rfl

theorem wpf5_historyConsistent (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (key : InvocationKey σ) (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? key = some history) :
    HistoryIsConsistent program.failureFreeFlow key history :=
  invariant.historiesConsistent (key, history) (wpf5_findInvocation_mem _ _ _ isHistory)

/-! ## One task and its history change -/

/-- Changing one real task and the history of its invocation keeps the shape
when the task and the history still agree. -/
theorem wpf5_shape_updateTask (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config) (task : Task σ)
    (isTask : task ∈ config.tasks) (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? task.key = some history)
    (taskChange : Task σ → Task σ) (historyChange : InvocationHistory σ → InvocationHistory σ)
    (keepsKey : ∀ other, (taskChange other).key = other.key)
    (keepsId : (taskChange task).base.id = task.base.id)
    (keepsArguments : (historyChange history).arguments = history.arguments)
    (keepsOutcome : (historyChange history).outcome = history.outcome)
    (agreesEmitted : (taskChange task).emittedCount = (historyChange history).emittedCount)
    (agreesPhase : (taskChange task).base.phase = (historyChange history).virtualPhase) :
    WPF5FreeShape
      { config.updateRealTask task.base.id taskChange with
        ghost := config.ghost.updateInvocation task.key historyChange } := by
  have findAfter : ∀ key, (config.ghost.updateInvocation task.key historyChange).findInvocation? key =
      if key = task.key then some (historyChange history) else config.ghost.findInvocation? key := by
    intro key
    rw [wpf5_updateInvocation_find, isHistory]
    rfl
  have keyChanged : ∀ other, other ∈ config.tasks →
      (if other.base.id = task.base.id then taskChange other else other).key = other.key := by
    intro other _
    split
    · exact keepsKey other
    · rfl
  have isOtherKey : ∀ other, other ∈ config.tasks → other.base.id ≠ task.base.id →
      other.key ≠ task.key := by
    intro other isOther isOtherId isSameKey
    exact isOtherId (by rw [wpf5_eq_of_key config shape other task isOther isTask isSameKey])
  refine
    { isUp := shape.isUp
      noZombies := by simp [Config.updateRealTask, shape.noZombies]
      noInterrupts := shape.noInterrupts
      lastCommitIsNormal := by
        simp only [Config.updateRealTask, wpf5_updateInvocation_lastNormalCommitAt]
        exact shape.lastCommitIsNormal
      noRetryTimers := shape.noRetryTimers
      nextTaskIds := by
        simp only [Config.updateRealTask, wpf5_updateInvocation_nextVirtualTaskId]
        exact shape.nextTaskIds
      tasksMatch := ?_
      liveHaveTasks := ?_
      taskKeysNodup := ?_ }
  · intro changed isChanged
    simp only [Config.updateRealTask, List.mem_map] at isChanged
    obtain ⟨other, isOther, isChangedOther⟩ := isChanged
    subst isChangedOther
    by_cases isOtherId : other.base.id = task.base.id
    · have isSame := wpf5_eq_of_id config invariant shape other task isOther isTask isOtherId
      subst isSame
      obtain ⟨_, arguments, isFound, isArguments, isVirtualId, _, _, isNotCommitted⟩ :=
        shape.tasksMatch other isTask
      rw [isHistory] at isFound
      cases isFound
      simp only [isOtherId, if_true]
      refine ⟨historyChange history, arguments, ?_, by rw [keepsArguments, isArguments],
        by rw [keepsId, isVirtualId], agreesEmitted, agreesPhase,
        by rw [keepsOutcome]; exact isNotCommitted⟩
      rw [findAfter, keepsKey, if_pos rfl]
    · simp only [isOtherId, if_false]
      obtain ⟨found, arguments, isFound, rest⟩ := shape.tasksMatch other isOther
      refine ⟨found, arguments, ?_, rest⟩
      rw [findAfter, if_neg (isOtherKey other isOther isOtherId)]
      exact isFound
  · intro key after isAfter isLive hasArguments
    simp only [Config.updateRealTask, List.mem_map]
    rw [findAfter] at isAfter
    by_cases isKey : key = task.key
    · subst isKey
      exact ⟨taskChange task, ⟨task, isTask, by simp⟩, keepsKey task⟩
    · rw [if_neg isKey] at isAfter
      obtain ⟨other, isOther, isOtherKeyEq⟩ :=
        shape.liveHaveTasks key after isAfter isLive hasArguments
      exact ⟨_, ⟨other, isOther, rfl⟩, by rw [keyChanged other isOther]; exact isOtherKeyEq⟩
  · simp only [Config.updateRealTask, List.map_map]
    have isSameKeys : config.tasks.map (Task.key ∘ fun other =>
        if other.base.id = task.base.id then taskChange other else other) =
        config.tasks.map Task.key := by
      apply List.map_congr_left
      intro other isOther
      exact keyChanged other isOther
    rw [isSameKeys]
    exact shape.taskKeysNodup

/-! ## The Worker steps -/

theorem wpf5_free_workerRun (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config) (virtualTaskId : Nat)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config)
      (.workerRun virtualTaskId) = some (dexEvents, dexNext)) :
    WPF5FreeStepResult program config (.workerRun virtualTaskId) dexEvents dexNext := by
  cases isFound : (abstractConfig config).findTask? virtualTaskId with
  | none => simp [Dex.step, isFound] at isDexStep
  | some virtualTask =>
    have isVirtualPhase : virtualTask.phase = .started := by
      simp only [Dex.step, isFound] at isDexStep
      split at isDexStep
      · assumption
      · cases isDexStep
    obtain ⟨task, history, arguments, isTask, isTaskId, isHistory, isArguments, isVirtualId,
      isEmitted, isPhase, _, isVirtual⟩ :=
      wpf5_virtualTask_lookup program config invariant shape virtualTaskId virtualTask isFound
    subst isVirtual isTaskId
    have isProgress := wpf5_virtualPhase_started history isVirtualPhase
    have consistent := wpf5_historyConsistent program config invariant task.key history isHistory
    have isZero : history.emittedCount = 0 := by
      unfold HistoryIsConsistent at consistent
      rw [isArguments] at consistent
      simp only [isProgress] at consistent
      exact consistent.1
    have isReal := (wpf5_findLiveTask config invariant shape task isTask).2
    have isTaskPhase : task.base.phase = .started := by
      rw [isPhase]
      unfold InvocationHistory.virtualPhase
      rw [isProgress]
    have isStep : step program config (.workerRun task.base.id true) =
        some ([], { config.updateRealTask task.base.id (fun current =>
            { current with
              base := { current.base with
                phase := .running (task.base.compute (program task.attemptContext)).1
                  (task.base.compute (program task.attemptContext)).2 } }) with
          ghost := config.ghost.updateInvocation task.key (fun previous =>
            { previous with
              progress := .ran (task.base.compute (program task.attemptContext)).1
                (task.base.compute (program task.attemptContext)).2 }) }) := by
      simp [step, isReal, isTaskPhase, isHistory, isProgress, VirtualProgress.isNotRun,
        Config.updateRealTask]
    have isMap : dexActionsOf program config (.workerRun task.base.id true) =
        [.workerRun task.base.id] := by
      simp only [dexActionsOf, if_true, Config.onVirtualTask,
        wpf5_virtualTaskIdOf config invariant shape task isTask history arguments isHistory
          isArguments, isVirtualId]
    exact wpf5_free_result program ignoresAttemptContext config invariant _ dexEvents dexNext
      isDexStep _ _ _ isStep _ isMap rfl
      (wpf5_erase_of_notEffect program config _ (by intro _ _ isEqual; cases isEqual) _ _ isStep)
      (wpf5_shape_updateTask program config invariant shape task isTask history isHistory _ _
        (fun _ => rfl) rfl rfl rfl isEmitted
        (by simp [InvocationHistory.virtualPhase, isZero]))
      rfl rfl rfl

theorem wpf5_drop_succ {α : Type} (elements : List α) (count : Nat) (head : α) (rest : List α)
    (isDrop : elements.drop count = head :: rest) : elements.drop (count + 1) = rest := by
  induction elements generalizing count with
  | nil => simp at isDrop
  | cons first later inductionHypothesis =>
    cases count with
    | zero =>
      simp only [List.drop_zero, List.cons.injEq] at isDrop
      simp [isDrop.2]
    | succ count =>
      simp only [List.drop_succ_cons] at isDrop ⊢
      exact inductionHypothesis count isDrop

theorem wpf5_free_workerEffect (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config) (virtualTaskId : Nat)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config)
      (.workerEffect virtualTaskId) = some (dexEvents, dexNext)) :
    WPF5FreeStepResult program config (.workerEffect virtualTaskId) dexEvents dexNext := by
  cases isFound : (abstractConfig config).findTask? virtualTaskId with
  | none => simp [Dex.step, isFound] at isDexStep
  | some virtualTask =>
    obtain ⟨effect, laterEffects, response, isVirtualPhase⟩ : ∃ effect laterEffects response,
        virtualTask.phase = .running (effect :: laterEffects) response := by
      simp only [Dex.step, isFound] at isDexStep
      split at isDexStep
      · exact ⟨_, _, _, by assumption⟩
      · cases isDexStep
    obtain ⟨task, history, arguments, isTask, isTaskId, isHistory, isArguments, isVirtualId,
      isEmitted, isPhase, _, isVirtual⟩ :=
      wpf5_virtualTask_lookup program config invariant shape virtualTaskId virtualTask isFound
    subst isVirtual isTaskId
    obtain ⟨effects, isProgress, isDrop⟩ :=
      wpf5_virtualPhase_running history _ _ isVirtualPhase
    have isReal := (wpf5_findLiveTask config invariant shape task isTask).2
    have isTaskPhase : task.base.phase = .running (effect :: laterEffects) response := by
      rw [isPhase]
      exact isVirtualPhase
    have isCount : config.ghost.emittedCountOf task.key = task.emittedCount := by
      simp [Ghost.emittedCountOf, isHistory, isEmitted]
    have isStep : step program config (.workerEffect task.base.id true) =
        some ([Obs.fx task.base.step task.base.method effect (task.emittedCount + 1)],
          { config.updateRealTask task.base.id (fun current =>
              { current with
                base := { current.base with phase := .running laterEffects response }
                emittedCount := task.emittedCount + 1 }) with
            ghost := config.ghost.updateInvocation task.key (fun previous =>
              { previous with emittedCount := task.emittedCount + 1 }) }) := by
      simp [step, isReal, isTaskPhase, isCount, Config.updateRealTask]
    have isMap : dexActionsOf program config (.workerEffect task.base.id true) =
        [.workerEffect task.base.id] := by
      simp only [dexActionsOf, if_true, Config.onVirtualTask,
        wpf5_virtualTaskIdOf config invariant shape task isTask history arguments isHistory
          isArguments, isVirtualId]
    have isErase : eraseStepEvents config
        [Obs.fx task.base.step task.base.method effect (task.emittedCount + 1)] =
        [Obs.fx task.base.step task.base.method effect (task.emittedCount + 1)].map Obs.toDex := by
      simp [eraseStepEvents, Task.key] at isCount ⊢
      simp [isCount, Obs.toDex]
    exact wpf5_free_result program ignoresAttemptContext config invariant _ dexEvents dexNext
      isDexStep _ _ _ isStep _ isMap rfl isErase
      (wpf5_shape_updateTask program config invariant shape task isTask history isHistory _ _
        (fun _ => rfl) rfl rfl rfl rfl
        (by
          simp only [InvocationHistory.virtualPhase, isProgress]
          rw [isEmitted, wpf5_drop_succ effects history.emittedCount effect laterEffects isDrop]))
      rfl rfl rfl

theorem wpf5_free_workerReturn (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config) (virtualTaskId : Nat)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config)
      (.workerReturn virtualTaskId) = some (dexEvents, dexNext)) :
    WPF5FreeStepResult program config (.workerReturn virtualTaskId) dexEvents dexNext := by
  cases isFound : (abstractConfig config).findTask? virtualTaskId with
  | none => simp [Dex.step, isFound] at isDexStep
  | some virtualTask =>
    obtain ⟨response, isVirtualPhase⟩ : ∃ response,
        virtualTask.phase = .running [] response := by
      simp only [Dex.step, isFound] at isDexStep
      split at isDexStep
      · exact ⟨_, by assumption⟩
      · cases isDexStep
    obtain ⟨task, history, arguments, isTask, isTaskId, isHistory, isArguments, isVirtualId,
      isEmitted, isPhase, _, isVirtual⟩ :=
      wpf5_virtualTask_lookup program config invariant shape virtualTaskId virtualTask isFound
    subst isVirtual isTaskId
    obtain ⟨effects, isProgress, _⟩ := wpf5_virtualPhase_running history _ _ isVirtualPhase
    have isReal := (wpf5_findLiveTask config invariant shape task isTask).2
    have isTaskPhase : task.base.phase = .running [] response := by
      rw [isPhase]
      exact isVirtualPhase
    have isStep : step program config (.workerReturn task.base.id true) =
        some ([], { config.updateRealTask task.base.id (fun current =>
            { current with base := { current.base with phase := .returned response } }) with
          ghost := config.ghost.updateInvocation task.key (fun previous =>
            { previous with progress := .returned response }) }) := by
      simp [step, isReal, isTaskPhase, isHistory, isProgress, VirtualProgress.isReturned,
        Config.updateRealTask]
    have isMap : dexActionsOf program config (.workerReturn task.base.id true) =
        [.workerReturn task.base.id] := by
      simp only [dexActionsOf, if_true, Config.onVirtualTask,
        wpf5_virtualTaskIdOf config invariant shape task isTask history arguments isHistory
          isArguments, isVirtualId]
    exact wpf5_free_result program ignoresAttemptContext config invariant _ dexEvents dexNext
      isDexStep _ _ _ isStep _ isMap rfl
      (wpf5_erase_of_notEffect program config _ (by intro _ _ isEqual; cases isEqual) _ _ isStep)
      (wpf5_shape_updateTask program config invariant shape task isTask history isHistory _ _
        (fun _ => rfl) rfl rfl rfl isEmitted rfl)
      rfl rfl rfl

end DexExpressiveness.DexF
