import DexExpressiveness.Failure.Observation

/-!
# WP-F6: the progress Boolean is the progress rule of spec §11.5

`isProgress_eq_dexProgress_proof` proves `isProgress_eq_dexProgress`
(`Failure/Statements/Leaf.lean`). Most labels map to a fixed Dex action list.
A Worker label whose Boolean is set maps to one action on the virtual task,
which exists because every real task follows a history with arguments.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- A real task found by its ID is one of the real tasks. -/
theorem wpf6_mem_realTasks_of_findRealTask (config : Config σ) (taskId : Nat) (task : Task σ)
    (isFound : config.findRealTask? taskId = some task) : task ∈ config.realTasks :=
  List.mem_of_find?_eq_some isFound

/-- A real task found by its ID has a virtual task ID: its invocation has
arguments. -/
theorem wpf6_virtualTaskIdOf_isSome (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (task : Task σ)
    (isFound : config.findRealTask? taskId = some task) :
    (config.virtualTaskIdOf taskId).isSome = true := by
  obtain ⟨history, isHistory, follows⟩ :=
    invariant.realTasksFollowHistory task
      (wpf6_mem_realTasks_of_findRealTask config taskId task isFound)
  obtain ⟨arguments, hasArguments, _⟩ := follows
  simp [Config.virtualTaskIdOf, isFound, Ghost.argumentsOf, isHistory, hasArguments]

/-- One action on an existing virtual task holds a progress step when the
action is not a tick. -/
theorem wpf6_onVirtualTask_any (program : Program σ) (config : Config σ) (taskId : Nat)
    (action : Nat → Dex.Action σ) (isNotTick : ∀ virtualTaskId duration,
      action virtualTaskId ≠ Dex.Action.tick duration)
    (hasVirtualTask : (config.virtualTaskIdOf taskId).isSome = true) :
    (config.onVirtualTask taskId action).any (Dex.system program.failureFreeFlow).isProgress =
      true := by
  unfold Config.onVirtualTask
  cases isVirtual : config.virtualTaskIdOf taskId with
  | none => rw [isVirtual] at hasVirtualTask; cases hasVirtualTask
  | some virtualTaskId =>
    simp only [List.any_cons, List.any_nil, Bool.or_false]
    cases isAction : action virtualTaskId with
    | tick duration => exact absurd isAction (isNotTick virtualTaskId duration)
    | _ => rfl

/-- **WP-F6.** The progress Boolean of a label is the progress rule of spec
§11.5. -/
theorem isProgress_eq_dexProgress_proof (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ)
    (isNotExhausting : action.isExhausting = false) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    action.isProgress =
      (dexActionsOf program config action).any (Dex.system program.failureFreeFlow).isProgress := by
  cases action with
  | tick duration => rfl
  | request request => rfl
  | startRetry requestId stepType input => rfl
  | engineReject request => rfl
  | stepStart stepId isSnapshotReused => cases isSnapshotReused <;> rfl
  | workerRun taskId isFirstRun =>
    cases isFirstRun with
    | false => rfl
    | true =>
      show true = (config.onVirtualTask taskId Dex.Action.workerRun).any _
      simp only [step] at isStep
      cases isFound : config.findRealTask? taskId with
      | none => rw [isFound] at isStep; cases isStep
      | some task =>
        exact (wpf6_onVirtualTask_any program config taskId _ (by intro _ _ isTick; cases isTick)
          (wpf6_virtualTaskIdOf_isSome program config invariant taskId task isFound)).symm
  | workerEffect taskId isFirstOccurrence =>
    cases isFirstOccurrence with
    | false => rfl
    | true =>
      show true = (config.onVirtualTask taskId Dex.Action.workerEffect).any _
      simp only [step] at isStep
      cases isFound : config.findRealTask? taskId with
      | none => rw [isFound] at isStep; cases isStep
      | some task =>
        exact (wpf6_onVirtualTask_any program config taskId _ (by intro _ _ isTick; cases isTick)
          (wpf6_virtualTaskIdOf_isSome program config invariant taskId task isFound)).symm
  | workerReturn taskId isFirstReturn =>
    cases isFirstReturn with
    | false => rfl
    | true =>
      show true = (config.onVirtualTask taskId Dex.Action.workerReturn).any _
      simp only [step] at isStep
      cases isFound : config.findRealTask? taskId with
      | none => rw [isFound] at isStep; cases isStep
      | some task =>
        exact (wpf6_onVirtualTask_any program config taskId _ (by intro _ _ isTick; cases isTick)
          (wpf6_virtualTaskIdOf_isSome program config invariant taskId task isFound)).symm
  | workerAbandon taskId isLastOfCanceled =>
    cases isLastOfCanceled with
    | false => rfl
    | true =>
      show true = (config.onVirtualTask taskId Dex.Action.workerAbandon).any _
      have isReal : ∃ task, config.findRealTask? taskId = some task := by
        simp only [step] at isStep
        unfold Config.findRealTask? Config.realTasks
        cases isLive : config.findLiveTask? taskId with
        | some task =>
          refine ⟨task, ?_⟩
          rw [List.find?_append]
          unfold Config.findLiveTask? at isLive
          rw [isLive]
          rfl
        | none =>
          rw [isLive] at isStep
          cases isZombie : config.findZombie? taskId with
          | none => rw [isZombie] at isStep; cases isStep
          | some task =>
            refine ⟨task, ?_⟩
            rw [List.find?_append]
            unfold Config.findLiveTask? at isLive
            unfold Config.findZombie? at isZombie
            rw [isLive, isZombie]
            rfl
      obtain ⟨task, isFound⟩ := isReal
      exact (wpf6_onVirtualTask_any program config taskId _ (by intro _ _ isTick; cases isTick)
        (wpf6_virtualTaskIdOf_isSome program config invariant taskId task isFound)).symm
  | stepComplete taskId =>
    show true = (config.onVirtualTask taskId Dex.Action.stepComplete ++ _).any _
    rw [List.any_append]
    simp only [step] at isStep
    split at isStep
    · split at isStep
      · rename_i task _ isFound
        rw [wpf6_onVirtualTask_any program config taskId _ (by intro _ _ isTick; cases isTick)
          (wpf6_virtualTaskIdOf_isSome program config invariant taskId task isFound)]
        rfl
      · cases isStep
    · cases isStep
  | resultStale taskId isLastOfCanceled =>
    cases isLastOfCanceled with
    | false => rfl
    | true =>
      show true = (config.onVirtualTask taskId Dex.Action.stepCompleteStale).any _
      simp only [step] at isStep
      split at isStep
      · cases isFound : config.findRealTask? taskId with
        | none => rw [isFound] at isStep; cases isStep
        | some task =>
          exact (wpf6_onVirtualTask_any program config taskId _ (by intro _ _ isTick; cases isTick)
            (wpf6_virtualTaskIdOf_isSome program config invariant taskId task isFound)).symm
      · cases isStep
  | timerFire count isWaitTimerFired => cases isWaitTimerFired <;> rfl
  | attemptFailed stepId decision =>
    simp only [Action.isExhausting] at isNotExhausting
    simp [Action.isProgress, dexActionsOf, isNotExhausting]
  | attemptReturned stepId => rfl
  | attemptHeartbeat stepId value => rfl
  | crash => rfl
  | recover => rfl
  | recoverInterrupt interrupt decision =>
    simp only [Action.isExhausting] at isNotExhausting
    simp [Action.isProgress, dexActionsOf, isNotExhausting]
  | internalCommit => rfl

end DexExpressiveness.DexF
