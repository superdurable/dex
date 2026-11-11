import DexExpressiveness.Failure.Proofs.WPF1Commit

/-!
# WP-F1: helpers shared by the rule cases

The commit premise under α, lookups of ghost histories, and the facts that
the meta-functions keep Step IDs and `seq`.
-/

namespace DexExpressiveness.DexF.WPF1

variable {σ : Dex.Signature}

/-- A DexF log commit may stamp now, so the abstract Dex commit may too: the
last normal stamp is at most the last log stamp. -/
theorem canCommit_abstract {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) (canCommit : config.canCommit = true) :
    (abstractConfig config).canCommit = true := by
  have atMost := invariant.lastNormalCommitAtMost
  unfold Config.canCommit at canCommit
  unfold Dex.Config.canCommit abstractConfig
  dsimp only
  cases isNormal : config.ghost.lastNormalCommitAt with
  | none => rfl
  | some normal =>
    rw [isNormal] at atMost
    cases isLast : config.lastCommitAt with
    | none => rw [isLast] at atMost; exact absurd atMost id
    | some last =>
      rw [isLast] at atMost canCommit
      simp only [IsStampAtMost] at atMost
      simp only [decide_eq_true_eq] at canCommit ⊢
      omega

theorem get?_of_mem_nodup {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (entry : κ × ν)
    (isMember : entry ∈ map) (keysNodup : (map.map (fun candidate => candidate.1)).Nodup) :
    AssocList.get? map entry.1 = some entry.2 := by
  induction map with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    rw [List.map_cons, List.nodup_cons] at keysNodup
    obtain ⟨headKey, headValue⟩ := head
    rcases List.mem_cons.mp isMember with isHead | inRest
    · subst isHead
      simp [AssocList.get?]
    · have isOther : headKey ≠ entry.1 := by
        intro isKey
        exact keysNodup.1 (List.mem_map.mpr ⟨entry, inRest, isKey.symm⟩)
      simp only [AssocList.get?, isOther, ↓reduceIte]
      exact inductionHypothesis inRest keysNodup.2

theorem findInvocation?_of_mem {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) (entry : InvocationKey σ × InvocationHistory σ)
    (isMember : entry ∈ config.ghost.invocations) :
    config.ghost.findInvocation? entry.1 = some entry.2 :=
  get?_of_mem_nodup _ entry isMember invariant.ghostKeysNodup

/-- Every live history is current in the run. -/
theorem live_isCurrent {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (runInvariant : RunInvariant config run)
    (entry : InvocationKey σ × InvocationHistory σ) (isMember : entry ∈ config.ghost.invocations)
    (isLive : entry.2.outcome = .live) :
    ∃ record invocation, record ∈ run.base.steps ∧ record.id = entry.1.1 ∧
      record.invocation = some invocation ∧ invocation.method = entry.1.2 := by
  obtain ⟨record, state, isRecord, isId, hasInvocation⟩ :=
    runInvariant.liveIsCurrent entry.1 entry.2 (findInvocation?_of_mem invariant entry isMember)
      isLive
  exact ⟨record, _, isRecord, isId, hasInvocation, rfl⟩

/-! ## Step IDs -/

theorem ids_foldl_evalStep (steps : List (Dex.StepExecId σ)) (run : Dex.Run σ) :
    (steps.foldl Dex.Run.evalStep run).steps.map (fun record => record.id) =
      run.steps.map (fun record => record.id) := by
  induction steps generalizing run with
  | nil => rfl
  | cons step laterSteps inductionHypothesis =>
    exact (inductionHypothesis _).trans (Dex.Run.evalStep_frame run step).1

theorem ids_wake (run : Dex.Run σ) (changed : List σ.Chan) :
    (run.wake changed).steps.map (fun record => record.id) =
      run.steps.map (fun record => record.id) :=
  ids_foldl_evalStep _ run

theorem ids_applyEffects (run : Dex.Run σ) (effects : Dex.Effects σ) :
    (run.applyEffects effects).steps.map (fun record => record.id) =
      run.steps.map (fun record => record.id) := by
  unfold Dex.Run.applyEffects
  rw [ids_wake, (Dex.Run.applyPublications_frame _ _).1, (Dex.Run.applyWrites_frame _ _).1]

theorem ids_registerWait (run : Dex.Run σ) (step : Dex.StepExecId σ) (wait : Dex.Wait σ)
    (now : Nat) :
    (run.registerWait step wait now).steps.map (fun record => record.id) =
      run.steps.map (fun record => record.id) := by
  unfold Dex.Run.registerWait
  split
  · exact Dex.Run.ids_updateStep _ _ _ (fun _ => rfl)
  · dsimp only
    rw [(Dex.Run.evalStep_frame _ _).1]
    exact Dex.Run.ids_updateStep _ _ _ (fun _ => rfl)

theorem ids_fireTimer (run : Dex.Run σ) (timer : Dex.TimerId σ) :
    (run.fireTimer timer).steps.map (fun record => record.id) =
      run.steps.map (fun record => record.id) := by
  unfold Dex.Run.fireTimer
  split
  · dsimp only
    split
    · rfl
    · split
      · split
        · rfl
        · rw [(Dex.Run.evalStep_frame _ _).1]
          exact Dex.Run.ids_updateStep _ _ _ (fun _ => rfl)
      · rfl
  · rfl

theorem ids_foldl_fireTimer (timers : List (Dex.TimerId σ)) (run : Dex.Run σ) :
    (timers.foldl Dex.Run.fireTimer run).steps.map (fun record => record.id) =
      run.steps.map (fun record => record.id) := by
  induction timers generalizing run with
  | nil => rfl
  | cons timer laterTimers inductionHypothesis =>
    exact (inductionHypothesis _).trans (ids_fireTimer run timer)

theorem ids_finishStep (run : Dex.Run σ) (step : Dex.StepExecId σ) (finalPhase : Dex.Phase) :
    (run.finishStep step finalPhase).steps.map (fun record => record.id) =
      run.steps.map (fun record => record.id) :=
  Dex.Run.ids_updateStep _ _ _ (fun _ => rfl)

theorem ids_foldl_finishStep (steps : List (Dex.StepExecId σ)) (finalPhase : Dex.Phase)
    (run : Dex.Run σ) :
    (steps.foldl (fun current step => current.finishStep step finalPhase) run).steps.map
        (fun record => record.id) = run.steps.map (fun record => record.id) := by
  induction steps generalizing run with
  | nil => rfl
  | cons step laterSteps inductionHypothesis =>
    exact (inductionHypothesis _).trans (ids_finishStep run step finalPhase)

theorem counters_foldl_finishStep (steps : List (Dex.StepExecId σ)) (finalPhase : Dex.Phase)
    (run : Dex.Run σ) :
    (steps.foldl (fun current step => current.finishStep step finalPhase) run).counters =
      run.counters := by
  induction steps generalizing run with
  | nil => rfl
  | cons step laterSteps inductionHypothesis => exact inductionHypothesis _

theorem ids_closeRun (run : Dex.Run σ) (status : Dex.CloseStatus) :
    (run.closeRun status).steps.map (fun record => record.id) =
      run.steps.map (fun record => record.id) := by
  unfold Dex.Run.closeRun
  split
  · rfl
  · exact ids_foldl_finishStep _ _ run

theorem counters_closeRun (run : Dex.Run σ) (status : Dex.CloseStatus) :
    (run.closeRun status).counters = run.counters := by
  unfold Dex.Run.closeRun
  split
  · rfl
  · exact counters_foldl_finishStep _ _ run

/-- Every Step number in use is counted. -/
def NumbersCounted (run : Dex.Run σ) : Prop :=
  ∀ step, step ∈ run.steps.map (fun record => record.id) →
    step.number ≤ (run.counters.get? step.stepType).getD 0

theorem NumbersCounted.of_same {run other : Dex.Run σ} (isCounted : NumbersCounted run)
    (sameIds : other.steps.map (fun record => record.id) = run.steps.map (fun record => record.id))
    (sameCounters : other.counters = run.counters) : NumbersCounted other := by
  intro step isMember
  rw [sameIds] at isMember
  rw [sameCounters]
  exact isCounted step isMember

theorem nodup_newStep (run : Dex.Run σ) (stepType : σ.StepType) (input : Option σ.Val)
    (options : Dex.StepOptions σ) (origin : Dex.Origin σ)
    (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (isCounted : NumbersCounted run) :
    ((run.newStep stepType input options origin).steps.map (fun record => record.id)).Nodup ∧
      NumbersCounted (run.newStep stepType input options origin) := by
  simp only [Dex.Run.newStep, List.map_append, List.map_cons, List.map_nil]
  constructor
  · apply List.nodup_append.mpr
    refine ⟨idsNodup, List.nodup_cons.mpr ⟨List.not_mem_nil, List.nodup_nil⟩, ?_⟩
    intro step isOld other isNew
    simp only [List.mem_singleton] at isNew
    subst isNew
    intro isSame
    have := isCounted step isOld
    rw [isSame] at this
    simp only at this
    omega
  · intro step isMember
    simp only [List.map_append, List.map_cons, List.map_nil, List.mem_append,
      List.mem_singleton] at isMember
    dsimp only
    rcases isMember with isOld | isNew
    · have := isCounted step isOld
      by_cases isType : step.stepType = stepType
      · rw [isType, AssocList.get?_set_self]
        rw [isType] at this
        simp only [Option.getD_some]
        omega
      · rw [AssocList.get?_set_other _ _ _ _ isType]
        exact this
    · subst isNew
      simp [AssocList.get?_set_self]

theorem nodup_foldl_newStep (moves : List (Dex.Move σ)) (origin : Dex.Origin σ)
    (run : Dex.Run σ) (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (isCounted : NumbersCounted run) :
    ((moves.foldl (fun current move =>
        current.newStep move.stepType move.input move.options origin) run).steps.map
          (fun record => record.id)).Nodup ∧
      NumbersCounted (moves.foldl (fun current move =>
        current.newStep move.stepType move.input move.options origin) run) := by
  induction moves generalizing run with
  | nil => exact ⟨idsNodup, isCounted⟩
  | cons move laterMoves inductionHypothesis =>
    obtain ⟨nextNodup, nextCounted⟩ := nodup_newStep run _ _ _ origin idsNodup isCounted
    exact inductionHypothesis _ nextNodup nextCounted

theorem nodup_applyDecision (run : Dex.Run σ) (step : Dex.StepExecId σ)
    (decision : Dex.Decision σ) (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (isCounted : NumbersCounted run) :
    ((run.applyDecision step decision).steps.map (fun record => record.id)).Nodup := by
  have finishedIds := ids_finishStep run step .completed
  unfold Dex.Run.applyDecision
  dsimp only
  split
  · rw [ids_closeRun]
    cases ‹Option σ.Val› <;>
      · simp only [Dex.Run.collectOutput]
        rw [finishedIds]
        exact idsNodup
  · rw [ids_closeRun, finishedIds]
    exact idsNodup
  · rw [finishedIds]
    exact idsNodup
  · exact (nodup_foldl_newStep _ _ _ (by rw [finishedIds]; exact idsNodup)
      (isCounted.of_same finishedIds rfl)).1

/-! ## One Dex step, and the result of a normal commit -/

theorem exec_singleton (flow : Dex.FlowDef σ) (config : Dex.Config σ) (action : Dex.Action σ) :
    (Dex.system flow).exec config [action] = Dex.step flow config action := by
  simp only [TransitionSystem.exec, Dex.system]
  cases Dex.step flow config action with
  | none => rfl
  | some result => simp

theorem exec_cons (flow : Dex.FlowDef σ) (config : Dex.Config σ) (action : Dex.Action σ)
    (laterActions : List (Dex.Action σ)) (events : List (Dex.Obs σ)) (next : Dex.Config σ)
    (isStep : Dex.step flow config action = some (events, next)) :
    (Dex.system flow).exec config (action :: laterActions) =
      ((Dex.system flow).exec next laterActions).map
        (fun result => (events ++ result.1, result.2)) := by
  simp only [TransitionSystem.exec]
  rw [show (Dex.system flow).step config action = some (events, next) from isStep]
  dsimp only
  cases isLater : (Dex.system flow).exec next laterActions with
  | none => rfl
  | some result => rfl

/-- The events and α of a normal commit whose change commutes with the
relabeling. -/
theorem commit_simulated (config : Config σ) (run : Run σ)
    (runInvariant : RunInvariant config run) (completed : Option (InvocationKey σ))
    (inputEvents : List (Obs σ)) (change : Run σ → Run σ) (baseChange : Dex.Run σ → Dex.Run σ)
    (isBase : (change (bumpedRun run)).base = baseChange (bumpedRun run).base)
    (keepsAttempts : ∀ step, ((change (bumpedRun run)).findAttempt? step).map attemptShape =
      (run.findAttempt? step).map attemptShape)
    (commutes : baseChange (relabelRun (commitView config run) (commitRelabel config run)
        (bumpedRun run).base) =
      relabelRun (commitView config run) (commitRelabel config run)
        (baseChange (bumpedRun run).base))
    (keepsSeq : (baseChange (bumpedRun run).base).seq = run.base.seq + 1)
    (idsNodup : ((baseChange (bumpedRun run).base).steps.map (fun record => record.id)).Nodup) :
    eraseStepEvents config (config.commitRun run true completed inputEvents change).1 =
        eraseStepEvents config inputEvents ++
          Dex.Config.closeEvents (Run.abstract config.ghost run)
            (baseChange { Run.abstract config.ghost run with seq := config.ghost.normalCount + 1 }) ∧
      abstractConfig (config.commitRun run true completed inputEvents change).2 =
        { clock := config.clock
          lastCommitAt := some config.clock
          run := some (baseChange
            { Run.abstract config.ghost run with seq := config.ghost.normalCount + 1 })
          tasks := (config.commitRun run true completed inputEvents change).2.virtualTasks
          nextTaskId := config.ghost.nextVirtualTaskId } := by
  have isAbstract := abstract_commitRun config run runInvariant completed change baseChange isBase
    keepsAttempts commutes keepsSeq idsNodup
  rw [commitRun_normal]
  constructor
  · rw [← isAbstract]
    apply eraseStepEvents_closeEvents <;> rfl
  · simp only [abstractConfig, Option.map_some, isAbstract]
    rfl

end DexExpressiveness.DexF.WPF1
