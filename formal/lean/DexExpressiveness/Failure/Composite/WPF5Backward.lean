import DexExpressiveness.Failure.Composite.WPF5Lookup

/-!
# WP-F5: every Dex step from α of a run without failures is a DexF step

For the Lemma (no failures), `Tr_free ⊆ Tr_fail`: from a DexF configuration of
the shape `WPF5FreeShape` that satisfies the invariants, every enabled Dex
step of its abstraction is matched by one enabled DexF step with the same
events. The DexF step keeps the shape. `step_simulated` and the determinism
of `Dex.step` give the abstraction of the next configuration.

Dex [STEP-COMPLETE-STALE] maps to DexF `workerAbandon`, which takes no stamp:
a `resultStale` would stamp a failure-class commit and break the shape.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- The DexF request of a Dex request: a StartFlow gets request ID 0. -/
def wpf5LiftRequest : Dex.Request σ → Request σ
  | .startFlow stepType input => .startFlow 0 stepType input
  | .publish chan value => .publish chan value

/-- One DexF step that matches the Dex step `dexAction`. -/
def WPF5FreeStepResult (program : Program σ) (config : Config σ) (dexAction : Dex.Action σ)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ) : Prop :=
  ∃ action events next, step program config action = some (events, next) ∧
    events.map Obs.toDex = dexEvents ∧ abstractConfig next = dexNext ∧ WPF5FreeShape next ∧
    action.requestOf =
      ((Dex.system program.failureFreeFlow).requestOf dexAction).map wpf5LiftRequest ∧
    action.isExhausting = false ∧ action.isNonRetryableFailure = false

/-- A DexF step whose transition map is one Dex action with the same Dex step
as `dexAction` matches it. -/
theorem wpf5_free_result (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (dexAction : Dex.Action σ)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) dexAction =
      some (dexEvents, dexNext))
    (action : Action σ) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) (mappedAction : Dex.Action σ)
    (isMap : dexActionsOf program config action = [mappedAction])
    (isSameDex : Dex.step program.failureFreeFlow (abstractConfig config) mappedAction =
      Dex.step program.failureFreeFlow (abstractConfig config) dexAction)
    (isErase : eraseStepEvents config events = events.map Obs.toDex)
    (shapeNext : WPF5FreeShape next)
    (isRequest : action.requestOf =
      ((Dex.system program.failureFreeFlow).requestOf dexAction).map wpf5LiftRequest)
    (isNotExhausting : action.isExhausting = false)
    (isNotNonRetryable : action.isNonRetryableFailure = false) :
    WPF5FreeStepResult program config dexAction dexEvents dexNext := by
  have isSimulated := step_simulated program ignoresAttemptContext config invariant action
    isNotExhausting events next isStep
  unfold IsStepSimulated at isSimulated
  rw [isMap] at isSimulated
  simp only [TransitionSystem.exec, Dex.system, isSameDex, isDexStep, List.append_nil,
    Option.some.injEq, Prod.mk.injEq] at isSimulated
  obtain ⟨isEvents, isNext⟩ := isSimulated
  refine ⟨action, events, next, isStep, ?_, isNext.symm, shapeNext, isRequest, isNotExhausting,
    isNotNonRetryable⟩
  rw [← isErase, ← isEvents]

theorem wpf5_erase_of_notEffect (program : Program σ) (config : Config σ) (action : Action σ)
    (isNotEffect : ∀ taskId isFirstOccurrence, action ≠ .workerEffect taskId isFirstOccurrence)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    eraseStepEvents config events = events.map Obs.toDex :=
  wpf5_eraseStepEvents_notFx config events
    (wpf5_step_notFx program config action isNotEffect events next isStep)

theorem wpf5_canCommit (config : Config σ) (shape : WPF5FreeShape config) :
    (abstractConfig config).canCommit = config.canCommit := by
  unfold Dex.Config.canCommit Config.canCommit
  rw [shape.lastCommitIsNormal]
  rfl

theorem wpf5_canServeCommit (config : Config σ) (shape : WPF5FreeShape config) :
    config.canServeCommit = config.canCommit := by
  simp [Config.canServeCommit, shape.isUp]

/-! ## The shape after a commit -/

theorem wpf5_commitRun_historyEnds_of_some (config : Config σ) (run : Run σ) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) (key : InvocationKey σ) (before : InvocationHistory σ)
    (isBefore : config.ghost.findInvocation? key = some before) :
    ∃ after,
      (config.commitRun run isNormal completed inputEvents change).2.ghost.findInvocation? key =
        some after ∧ WPF5HistoryEnds completed key before after := by
  unfold Config.commitRun
  apply wpf5_historyEnds_of_some
  cases isNormal <;> exact isBefore

theorem wpf5_commitRun_historyEnds_of_after (config : Config σ) (run : Run σ) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) (key : InvocationKey σ) (after : InvocationHistory σ)
    (isAfter :
      (config.commitRun run isNormal completed inputEvents change).2.ghost.findInvocation? key =
        some after) :
    (∃ before, config.ghost.findInvocation? key = some before ∧
      WPF5HistoryEnds completed key before after) ∨
    (config.ghost.findInvocation? key = none ∧ ∃ pendedAt, after = InvocationHistory.fresh pendedAt) := by
  unfold Config.commitRun at isAfter
  rcases wpf5_historyEnds_of_after _ completed _ key after isAfter with
    ⟨before, isBefore, ends⟩ | ⟨isNone, isFresh⟩
  · exact .inl ⟨before, by cases isNormal <;> exact isBefore, ends⟩
  · exact .inr ⟨by cases isNormal <;> exact isNone, _, isFresh⟩

/-- A normal commit that completes no invocation and keeps the real tasks
keeps the shape. -/
theorem wpf5_shape_commitRun (config : Config σ) (shape : WPF5FreeShape config) (run : Run σ)
    (hasNoRetryTimers : run.retryTimers = []) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ)
    (keepsRetryTimers : ∀ current, current.retryTimers = [] → (change current).retryTimers = []) :
    WPF5FreeShape (config.commitRun run true none inputEvents change).2 := by
  refine
    { isUp := shape.isUp
      noZombies := shape.noZombies
      noInterrupts := shape.noInterrupts
      lastCommitIsNormal := rfl
      noRetryTimers := ?_
      nextTaskIds := shape.nextTaskIds
      tasksMatch := ?_
      liveHaveTasks := ?_
      taskKeysNodup := shape.taskKeysNodup }
  · intro afterRun isAfterRun
    simp only [Config.commitRun, Option.some.injEq] at isAfterRun
    subst isAfterRun
    exact keepsRetryTimers _ hasNoRetryTimers
  · intro task isTask
    obtain ⟨history, arguments, isHistory, isArguments, isVirtualId, isEmitted, isPhase,
      isNotCommitted⟩ := shape.tasksMatch task isTask
    obtain ⟨after, isAfter, isArgumentsAfter, isEmittedAfter, isProgressAfter, isOutcomeAfter⟩ :=
      wpf5_commitRun_historyEnds_of_some config run true none inputEvents change task.key history
        isHistory
    refine ⟨after, arguments, isAfter, by rw [isArgumentsAfter, isArguments], isVirtualId,
      by rw [isEmittedAfter, isEmitted], ?_, ?_⟩
    · rw [isPhase]
      simp only [InvocationHistory.virtualPhase, isEmittedAfter, isProgressAfter]
    · rcases isOutcomeAfter with isSame | ⟨_, isCanceled⟩
      · rw [isSame]
        exact isNotCommitted
      · rw [isCanceled]
        simp
  · intro key after isAfter isLive hasArguments
    rcases wpf5_commitRun_historyEnds_of_after config run true none inputEvents change key after
        isAfter with
      ⟨before, isBefore, isArgumentsAfter, _, _, isOutcomeAfter⟩ | ⟨_, _, isFresh⟩
    · rcases isOutcomeAfter with isSame | ⟨_, isCanceled⟩
      · exact shape.liveHaveTasks key before isBefore (by rw [← isSame]; exact isLive)
          (by rw [← isArgumentsAfter]; exact hasArguments)
      · rw [isLive] at isCanceled
        simp at isCanceled
    · rw [isFresh] at hasArguments
      cases hasArguments

/-! ## [TICK] and the requests -/

theorem wpf5_free_tick (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config) (duration : Nat)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config) (.tick duration) =
      some (dexEvents, dexNext)) :
    WPF5FreeStepResult program config (.tick duration) dexEvents dexNext := by
  have isPositive : 0 < duration := by
    simp only [Dex.step] at isDexStep
    split at isDexStep
    · assumption
    · cases isDexStep
  have isStep : step program config (.tick duration) =
      some ([], { config with clock := config.clock + duration }) := by
    simp [step, isPositive]
  exact wpf5_free_result program ignoresAttemptContext config invariant _ dexEvents dexNext
    isDexStep _ _ _ isStep (.tick duration) rfl rfl rfl
    ⟨shape.isUp, shape.noZombies, shape.noInterrupts, shape.lastCommitIsNormal,
      shape.noRetryTimers, shape.nextTaskIds, shape.tasksMatch, shape.liveHaveTasks,
      shape.taskKeysNodup⟩ rfl rfl rfl

theorem wpf5_free_startFlow (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config)
    (stepType : σ.StepType) (input : σ.Val)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config)
      (.request (.startFlow stepType input)) = some (dexEvents, dexNext)) :
    WPF5FreeStepResult program config (.request (.startFlow stepType input)) dexEvents dexNext := by
  cases hasRun : config.run with
  | some run =>
    simp [Dex.step, abstractConfig, hasRun] at isDexStep
  | none =>
    have canCommit : config.canCommit = true := by
      simp only [Dex.step, abstractConfig, hasRun, Option.map_none] at isDexStep
      split at isDexStep
      · rename_i isEnabled
        rw [← wpf5_canCommit config shape]
        exact isEnabled
      · cases isDexStep
    let freshRun : Run σ :=
      { base := Dex.Run.empty σ, attempts := [], retryTimers := [], startRequestId := 0 }
    let change : Run σ → Run σ := fun current =>
      { current with
        base := current.base.newStep stepType (some input)
          { skipWaitFor := (program.failureFreeFlow.stepDef stepType).waitFor.isNone }
          .flowStart }
    have isStep : step program config (.request (.startFlow 0 stepType input)) =
        some (config.commitRun freshRun true none [Obs.start stepType input] change) := by
      simp [step, hasRun, wpf5_canServeCommit config shape, canCommit, freshRun, change]
    exact wpf5_free_result program ignoresAttemptContext config invariant _ dexEvents dexNext
      isDexStep _ _ _ isStep _ rfl rfl
      (wpf5_erase_of_notEffect program config _ (by intro _ _ isEqual; cases isEqual) _ _ isStep)
      (wpf5_shape_commitRun config shape freshRun rfl [Obs.start stepType input] change (fun _ isEmpty => isEmpty))
      rfl rfl rfl

theorem wpf5_free_publish (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (shape : WPF5FreeShape config)
    (chan : σ.Chan) (value : σ.Val)
    (dexEvents : List (Dex.Obs σ)) (dexNext : Dex.Config σ)
    (isDexStep : Dex.step program.failureFreeFlow (abstractConfig config)
      (.request (.publish chan value)) = some (dexEvents, dexNext)) :
    WPF5FreeStepResult program config (.request (.publish chan value)) dexEvents dexNext := by
  have sameShape : WPF5FreeShape config := shape
  cases hasRun : config.run with
  | none =>
    have isStep : step program config (.request (.publish chan value)) = some ([Obs.rejectedPub chan value], config) := by
      simp [step, hasRun, shape.isUp]
    exact wpf5_free_result program ignoresAttemptContext config invariant _ dexEvents dexNext
      isDexStep _ _ _ isStep _ rfl rfl
      (wpf5_erase_of_notEffect program config _ (by intro _ _ isEqual; cases isEqual) _ _ isStep)
      sameShape rfl rfl rfl
  | some run =>
    cases hasStatus : run.base.status with
    | closed status =>
      have isStep : step program config (.request (.publish chan value)) = some ([Obs.rejectedPub chan value], config) := by
        simp [step, hasRun, shape.isUp, hasStatus]
      exact wpf5_free_result program ignoresAttemptContext config invariant _ dexEvents dexNext
        isDexStep _ _ _ isStep _ rfl rfl
        (wpf5_erase_of_notEffect program config _ (by intro _ _ isEqual; cases isEqual) _ _
          isStep)
        sameShape rfl rfl rfl
    | running =>
      have canCommit : config.canCommit = true := by
        simp only [Dex.step, abstractConfig, hasRun, Option.map_some] at isDexStep
        have isRunning : (Run.abstract config.ghost run).status = .running := hasStatus
        rw [isRunning] at isDexStep
        simp only at isDexStep
        split at isDexStep
        · rename_i isEnabled
          rw [← wpf5_canCommit config shape]
          exact isEnabled
        · cases isDexStep
      let change : Run σ → Run σ := fun current =>
        { current with base := (current.base.applyPublications [(chan, value)]).wake [chan] }
      have isStep : step program config (.request (.publish chan value)) =
          some (config.commitRun run true none [Obs.pub chan value] change) := by
        simp [step, hasRun, shape.isUp, hasStatus, canCommit, change]
      exact wpf5_free_result program ignoresAttemptContext config invariant _ dexEvents dexNext
        isDexStep _ _ _ isStep _ rfl rfl
        (wpf5_erase_of_notEffect program config _ (by intro _ _ isEqual; cases isEqual) _ _
          isStep)
        (wpf5_shape_commitRun config shape run (shape.noRetryTimers run hasRun) [Obs.pub chan value] change
          (fun _ isEmpty => isEmpty))
        rfl rfl rfl

end DexExpressiveness.DexF
