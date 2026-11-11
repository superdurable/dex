import DexExpressiveness.Failure.Proofs.WPF1CompleteTasks

/-!
# WP-F1: the Complete rules simulate

[STEP-COMPLETE-WAITFOR] and [STEP-COMPLETE-EXECUTE] of a task that passes
the fence map to the same Complete rule of the virtual task of its
invocation, then to [WORKER-ABANDON] of each virtual task the commit leaves
without a real task (spec §11.6).
-/

namespace DexExpressiveness.DexF.WPF1

variable {σ : Dex.Signature}

/-- The virtual task of the invocation a Complete rule commits. -/
theorem complete_virtualTask {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) {run : Run σ} (hasRun : config.run = some run)
    {taskId : Nat} {task : Task σ} (isTask : config.findRealTask? taskId = some task)
    (isFence : config.isFencePassed task = true) {response : Dex.Response σ}
    (isReturned : task.base.phase = .returned response) :
    ∃ history arguments, config.ghost.findInvocation? task.key = some history ∧
      history.outcome = .live ∧ history.arguments = some arguments ∧
      config.virtualTaskIdOf taskId = some arguments.virtualTaskId ∧
      (abstractConfig config).findTask? arguments.virtualTaskId =
        some (arguments.toTask task.key (.returned response)) ∧
      (abstractConfig config).isTaskCurrent (arguments.toTask task.key (.returned response)) =
        true := by
  obtain ⟨record, attempt, history, arguments, isFound, isMember, isId, isAttempt, hasInvocation,
    isMethod, hasSnapshot, isHistory, isLive, hasArguments, follows, isConsistent, isReal,
    isTaskId⟩ := complete_context invariant hasRun isTask isFence
  have runInvariant := invariant.runInvariant run hasRun
  obtain ⟨followedArguments, hasFollowedArguments, _, _, _, _, _, followsPhase⟩ := follows
  rw [hasArguments] at hasFollowedArguments
  cases hasFollowedArguments
  rw [isReturned] at followsPhase
  obtain ⟨isResponse, _, isReturnedHistory⟩ := followsPhase
  have isVirtualPhase : history.virtualPhase = .returned response := by
    unfold HistoryIsConsistent at isConsistent
    rw [hasArguments] at isConsistent
    unfold InvocationHistory.virtualPhase
    cases isProgress : history.progress with
    | notRun => rw [isProgress] at isReturnedHistory; cases isReturnedHistory
    | ran effects ranResponse => rw [isProgress] at isReturnedHistory; cases isReturnedHistory
    | returned historyResponse =>
      rw [isProgress] at isConsistent
      rw [isConsistent.1.1, isResponse]
  refine ⟨history, arguments, isHistory, isLive, hasArguments, ?_, ?_, ?_⟩
  · simp [Config.virtualTaskIdOf, isTask, Ghost.argumentsOf, isHistory, hasArguments]
  · have isVirtual : arguments.toTask task.key (.returned response) ∈ config.virtualTasks := by
      apply (mem_virtualTasks config _).mpr
      refine ⟨(task.key, history), mem_of_get? _ _ _ isHistory, ?_⟩
      apply (virtualTaskOf_eq_some_iff config _ _).mpr
      refine ⟨arguments, hasArguments, ?_, by rw [isVirtualPhase]⟩
      exact hasVirtualTask_live config task.key history (by rw [hasArguments]; rfl) isLive
    exact find?_of_pairwise _ (virtualTasks_pairwise config invariant.virtualTaskIdsNodup) _
      isVirtual
  · unfold Dex.Config.isTaskCurrent
    simp only [abstractConfig, hasRun, Option.map_some]
    rw [findStep?_abstract]
    have isStep : (arguments.toTask task.key (.returned response)).step = task.base.step := rfl
    rw [isStep, isFound]
    apply decide_eq_true
    dsimp only
    rw [abstractInvocation_eq_attemptView config.ghost run record _ hasInvocation
      (fun found isFoundAttempt => by
        rw [isId, isAttempt] at isFoundAttempt
        cases isFoundAttempt
        exact isMethod)]
    unfold attemptView
    rw [isId, isAttempt]
    simp only [isMethod, hasSnapshot, and_self, ↓reduceIte, Ghost.argumentsOf]
    have isKey : (task.base.step, task.base.method) = task.key := rfl
    rw [isKey, isHistory]
    simp [hasArguments, Arguments.toTask]
    rfl

/-- A Complete rule whose change commutes with the relabeling and ends its
invocation is simulated by the Complete rule of the virtual task and the
abandonment of the virtual tasks it leaves without a real task. -/
theorem complete_simulated (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (run : Run σ) (hasRun : config.run = some run)
    (taskId : Nat) (task : Task σ) (isTask : config.findRealTask? taskId = some task)
    (isFence : config.isFencePassed task = true) (response : Dex.Response σ)
    (isReturned : task.base.phase = .returned response) (baseChange : Dex.Run σ → Dex.Run σ)
    (commutes : baseChange (relabelRun (commitView config run) (commitRelabel config run)
        (bumpedRun run).base) =
      relabelRun (commitView config run) (commitRelabel config run)
        (baseChange (bumpedRun run).base))
    (keepsSeq : (baseChange (bumpedRun run).base).seq = run.base.seq + 1)
    (idsNodup : ((baseChange (bumpedRun run).base).steps.map (fun record => record.id)).Nodup)
    (notCurrent : NotCurrentAt task.base.step task.base.method (baseChange (bumpedRun run).base))
    (dexStep : ∀ arguments : Arguments σ,
      (abstractConfig config).findTask? arguments.virtualTaskId =
        some (arguments.toTask task.key (.returned response)) →
      (abstractConfig config).isTaskCurrent (arguments.toTask task.key (.returned response)) =
        true →
      Dex.step program.failureFreeFlow (abstractConfig config)
          (.stepComplete arguments.virtualTaskId) =
        some (Dex.Config.commit ((abstractConfig config).removeTask arguments.virtualTaskId)
          (Run.abstract config.ghost run) [] baseChange))
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.stepComplete taskId) = some (events, next))
    (isResult : (config.removeRealTask taskId).commitRun run true (some task.key) []
        (fun current => { current with base := baseChange current.base }) = (events, next)) :
    IsStepSimulated program config (.stepComplete taskId) events next := by
  unfold IsStepSimulated
  simp only [dexActionsOf]
  rw [isStep]
  obtain ⟨history, arguments, isHistory, isLive, hasArguments, isVirtualId, isFoundTask,
    isCurrentTask⟩ := complete_virtualTask invariant hasRun isTask isFence isReturned
  have runInvariant := invariant.runInvariant run hasRun
  unfold Config.onVirtualTask
  rw [isVirtualId]
  dsimp only
  rw [List.singleton_append,
    exec_cons _ _ _ _ _ _ (dexStep arguments isFoundTask isCurrentTask)]
  obtain ⟨isEvents, isConfig⟩ := commit_simulated config run runInvariant (some task.key) []
    (fun current => { current with base := baseChange current.base }) baseChange rfl
    (fun _ => rfl) commutes keepsSeq idsNodup
  have isAbstract := abstract_commitRun config run runInvariant (some task.key)
    (fun current => { current with base := baseChange current.base }) baseChange rfl
    (fun _ => rfl) commutes keepsSeq idsNodup
  obtain ⟨isEventsEq, isNextEq⟩ := Prod.ext_iff.mp isResult
  subst isEventsEq isNextEq
  have setting : CompleteSetting program config
      ((config.removeRealTask taskId).commitRun run true (some task.key) []
        (fun current => { current with base := baseChange current.base })).2
      taskId task history arguments (baseChange (bumpedRun run).base) :=
    { invariant := invariant
      isTask := isTask
      isHistory := isHistory
      isLive := isLive
      hasArguments := hasArguments
      notCurrent := not_mem_currentKeys_of_notCurrentAt notCurrent
      nextReal := rfl
      nextInvocations := by
        obtain ⟨extra, isInvocations, isFresh⟩ := reconcile_invocations
          (countedGhost config.ghost (run.base.seq + 1) config.clock) (some task.key)
          (baseChange (bumpedRun run).base)
        refine ⟨extra, isInvocations, ?_⟩
        intro entry isMember
        rw [isFresh entry isMember]
        rfl }
  rw [abandonedVirtualTasks_eq]
  rw [exec_workerAbandons]
  · simp only [Option.map_some, List.append_nil]
    have sameEvents : eraseStepEvents config
        ((config.removeRealTask taskId).commitRun run true (some task.key) []
          (fun current => { current with base := baseChange current.base })).1 =
        eraseStepEvents config [] ++
          Dex.Config.closeEvents (Run.abstract config.ghost run)
            (baseChange { Run.abstract config.ghost run with
              seq := config.ghost.normalCount + 1 }) := isEvents
    have sameConfig := congrArg (fun dexConfig : Dex.Config σ =>
      { dexConfig with
        tasks := ((config.removeRealTask taskId).commitRun run true (some task.key) []
          (fun current => { current with base := baseChange current.base })).2.virtualTasks })
      isConfig
    simp only at sameConfig
    rw [sameEvents, show abstractConfig ((config.removeRealTask taskId).commitRun run true
        (some task.key) [] (fun current => { current with base := baseChange current.base })).2 =
      _ from sameConfig, ← setting.tasks_eq]
    rfl
  · exact setting.abandonedIds_nodup
  · intro id isAbandoned
    exact setting.abandoned_present id isAbandoned
  · intro candidate isMember isAbandoned
    refine isTaskCurrent_eq_false _
      ((countedGhost config.ghost (run.base.seq + 1) config.clock).reconcile (some task.key)
        (baseChange (bumpedRun run).base))
      (Run.reconcileAttempts { bumpedRun run with base := baseChange (bumpedRun run).base }) ?_
      candidate ?_
    · rw [isAbstract]
      rfl
    · exact setting.abandoned_notCurrent candidate (List.mem_filter.mp isMember).1 isAbandoned

end DexExpressiveness.DexF.WPF1
