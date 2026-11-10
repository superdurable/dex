import DexExpressiveness.Failure.Proofs.WPF3Good
import DexExpressiveness.Failure.Proofs.WPF3CompleteEvolution

/-!
# WP-F3: every step keeps the invariant of the effect history

`wpf3_good_step`: every DexF step keeps `WPF3Good`, for a program that
satisfies H3. Everything here is proved.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## Real tasks -/

theorem wpf3_ids_nodup_of_perm {first second : List (Task σ)} (isPerm : first.Perm second)
    (idsNodup : (second.map (fun task => task.base.id)).Nodup) :
    (first.map (fun task => task.base.id)).Nodup :=
  ((isPerm.map _).nodup_iff).mpr idsNodup

theorem wpf3_moveToZombies_perm (config : Config σ) (taskId : Nat) :
    (config.moveToZombies taskId).realTasks.Perm config.realTasks := by
  unfold Config.moveToZombies Config.realTasks
  dsimp only
  have split := List.filter_append_perm (fun task : Task σ => !decide (task.base.id = taskId))
    config.tasks
  simp only [Bool.not_not] at split
  exact (List.Perm.append_left _ List.perm_append_comm).trans
    (by rw [← List.append_assoc]; exact List.Perm.append_right _ split)

/-! ## Steps that keep the run -/

/-- A step that keeps the run and `par`, keeps or grows `h`, and keeps only
real tasks that were there. -/
theorem wpf3_good_of_sameRun (program : Program σ) (config next : Config σ)
    (good : WPF3Good program config) (sameRun : next.run = config.run)
    (sameArguments : ∀ key, next.ghost.argumentsOf key = config.ghost.argumentsOf key)
    (countsGrow : ∀ key, config.ghost.emittedCountOf key ≤ next.ghost.emittedCountOf key)
    (tasksKept : ∀ task, task ∈ next.realTasks → task ∈ config.realTasks)
    (idsNodup : (next.realTasks.map (fun task => task.base.id)).Nodup)
    (nextTaskIdGrows : config.nextTaskId ≤ next.nextTaskId) : WPF3Good program next where
  realTaskIdsNodup := idsNodup
  realTaskIdsBelow := fun task isMember =>
    Nat.lt_of_lt_of_le (good.realTaskIdsBelow task (tasksKept task isMember)) nextTaskIdGrows
  tasksFollow := fun task isMember =>
    wpf3_taskFollows_mono program config.ghost next.ghost task
      (good.tasksFollow task (tasksKept task isMember)) (sameArguments _) (countsGrow _)
  notStarted := by
    intro isNone key
    rw [sameArguments]
    exact good.notStarted (sameRun ▸ isNone) key
  runGood := by
    intro run isRun
    exact wpf3_runGood_congr config.ghost next.ghost run (good.runGood run (sameRun ▸ isRun))
      sameArguments

/-- A step that changes nothing the invariant reads but the stamp, `up`, or `I`. -/
theorem wpf3_good_of_sameTasks (program : Program σ) (config next : Config σ)
    (good : WPF3Good program config) (sameRun : next.run = config.run)
    (sameGhost : next.ghost = config.ghost) (sameRealTasks : next.realTasks = config.realTasks)
    (sameNext : next.nextTaskId = config.nextTaskId) : WPF3Good program next :=
  wpf3_good_of_sameRun program config next good sameRun (by rw [sameGhost]; intro; rfl)
    (by rw [sameGhost]; intro; exact Nat.le_refl _) (by rw [sameRealTasks]; exact fun _ h => h)
    (by rw [sameRealTasks]; exact good.realTaskIdsNodup) (by rw [sameNext]; exact Nat.le_refl _)

theorem wpf3_good_removeRealTask (program : Program σ) (config : Config σ)
    (good : WPF3Good program config) (taskId : Nat) :
    WPF3Good program (config.removeRealTask taskId) := by
  have isRealTasks := wpf3_realTasks_removeRealTask config taskId
  refine wpf3_good_of_sameRun program config _ good rfl (fun _ => rfl) (fun _ => Nat.le_refl _)
    ?_ ?_ (Nat.le_refl _)
  · rw [isRealTasks]
    exact fun task isMember => (List.mem_filter.mp isMember).1
  · rw [isRealTasks]
    exact List.Nodup.sublist (List.filter_sublist.map _) good.realTaskIdsNodup

theorem wpf3_good_moveToZombies (program : Program σ) (config : Config σ)
    (good : WPF3Good program config) (taskId : Nat) :
    WPF3Good program (config.moveToZombies taskId) := by
  have isPerm := wpf3_moveToZombies_perm config taskId
  exact wpf3_good_of_sameRun program config _ good rfl (fun _ => rfl) (fun _ => Nat.le_refl _)
    (fun task isMember => isPerm.mem_iff.mp isMember)
    (wpf3_ids_nodup_of_perm isPerm good.realTaskIdsNodup) (Nat.le_refl _)

/-! ## Commits -/

/-- The run before a commit with its `seq` taken. -/
def wpf3Bumped (run : Run σ) : Run σ :=
  { run with base := { run.base with seq := run.base.seq + 1 } }

theorem wpf3_evolution_bump (run : Run σ) (shape : WPF3RunShape run.base) :
    WPF3Evolution run.base (wpf3Bumped run).base :=
  wpf3_evolution_of_frame shape rfl rfl

theorem wpf3_commitRun_ghostKeeps (config : Config σ) (run : Run σ) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) :
    WPF3GhostKeeps config.ghost
      (config.commitRun run isNormal completed inputEvents change).2.ghost := by
  unfold Config.commitRun
  dsimp only
  exact wpf3_ghostKeeps_trans (by cases isNormal <;> exact fun _ => ⟨rfl, rfl⟩)
    (wpf3_reconcile_keeps _ _ _).1

/-- **A commit keeps the invariant**, when its change of the Step table is an
evolution and it keeps methods and snapshots of attempt states. -/
theorem wpf3_good_commitRun (program : Program σ) (committing : Config σ)
    (good : WPF3Good program committing) (run : Run σ)
    (runGood : WPF3RunGood committing.ghost run) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ)
    (evolution : WPF3Evolution run.base (change (wpf3Bumped run)).base)
    (attemptsEvolve : WPF3AttemptsEvolve run.attempts (change (wpf3Bumped run)).attempts) :
    WPF3Good program (committing.commitRun run isNormal completed inputEvents change).2 := by
  have ghostKeeps := wpf3_commitRun_ghostKeeps committing run isNormal completed inputEvents change
  refine ⟨good.realTaskIdsNodup, good.realTaskIdsBelow, ?_, ?_, ?_⟩
  · intro task isMember
    exact wpf3_taskFollows_mono program committing.ghost _ task (good.tasksFollow task isMember)
      (ghostKeeps _).1 (Nat.le_of_eq (ghostKeeps _).2.symm)
  · intro isNone
    simp [Config.commitRun] at isNone
  · intro after isAfter
    simp only [Config.commitRun, Option.some.injEq] at isAfter
    subst isAfter
    exact wpf3_runGood_reconcile committing.ghost _ run (change (wpf3Bumped run)) runGood
      (fun key => (ghostKeeps key).1) evolution attemptsEvolve

/-! ## Facts the rules need -/

theorem wpf3_runGood_empty (ghost : Ghost σ)
    (noArguments : ∀ key, ghost.argumentsOf key = none) (requestId : Nat) :
    WPF3RunGood ghost
      { base := Dex.Run.empty σ, attempts := [], retryTimers := [], startRequestId := requestId } where
  shape := ⟨by simp [Dex.Run.empty], by simp [Dex.Run.empty], by simp [Dex.Run.empty]⟩
  attemptsHaveRecords := by simp
  attemptsMatch := by simp [Dex.Run.empty]
  argumentsHaveRecords := by
    intro key arguments hasArguments
    rw [noArguments] at hasArguments
    cases hasArguments

/-- Every record with the ID of a found record has its invocation's method. -/
theorem wpf3_currentMethod (base : Dex.Run σ) (shape : WPF3RunShape base)
    (step : Dex.StepExecId σ) (record : Dex.StepRecord σ)
    (isRecord : base.findStep? step = some record) (invocation : Dex.Invocation)
    (isInvocation : record.invocation = some invocation) :
    ∀ candidate, candidate ∈ base.steps → candidate.id = step →
      candidate.invocation.map (fun invocation => invocation.method) = some invocation.method := by
  intro candidate isCandidate isCandidateStep
  obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? base step record isRecord
  have isSame := eq_of_nodup_map (fun record => record.id) base.steps shape.idsNodup isCandidate
    isMember (isCandidateStep.trans isId.symm)
  rw [isSame, isInvocation]
  rfl

theorem wpf3_fireRetryTimer_facts (run : Run σ) (timer : RetryTimerId σ) :
    (run.fireRetryTimer timer).base = run.base ∧
    WPF3AttemptsEvolve run.attempts (run.fireRetryTimer timer).attempts := by
  unfold Run.fireRetryTimer
  split
  · dsimp only
    split
    · rename_i record attempt _ isAttempt
      split
      · refine ⟨rfl, ?_⟩
        exact wpf3_attemptsEvolve_set _ _ attempt _ (by
          unfold AssocList.get? at isAttempt ⊢
          exact isAttempt) rfl rfl
      · exact ⟨rfl, WPF3AttemptsEvolve.refl _⟩
    · exact ⟨rfl, WPF3AttemptsEvolve.refl _⟩
  · exact ⟨rfl, WPF3AttemptsEvolve.refl _⟩

theorem wpf3_fireDueTimers_facts (timers : List (DueTimer σ)) (run : Run σ)
    (shape : WPF3RunShape run.base) :
    WPF3Evolution run.base (timers.foldl Run.fireDueTimer run).base ∧
    WPF3AttemptsEvolve run.attempts (timers.foldl Run.fireDueTimer run).attempts := by
  induction timers generalizing run with
  | nil => exact ⟨⟨WPF3RunEvolves.refl _, shape⟩, WPF3AttemptsEvolve.refl _⟩
  | cons timer laterTimers inductionHypothesis =>
    simp only [List.foldl_cons]
    have firstStep : WPF3Evolution run.base (run.fireDueTimer timer).base ∧
        WPF3AttemptsEvolve run.attempts (run.fireDueTimer timer).attempts := by
      cases timer with
      | wait waitTimer =>
        exact ⟨wpf3_evolution_fireTimer run.base waitTimer shape, WPF3AttemptsEvolve.refl _⟩
      | retry retryTimer =>
        obtain ⟨sameBase, attemptsEvolve⟩ := wpf3_fireRetryTimer_facts run retryTimer
        refine ⟨?_, attemptsEvolve⟩
        show WPF3Evolution run.base (run.fireRetryTimer retryTimer).base
        rw [sameBase]
        exact ⟨WPF3RunEvolves.refl _, shape⟩
    obtain ⟨laterEvolution, laterAttempts⟩ := inductionHypothesis _ firstStep.1.2
    exact ⟨wpf3_evolution_trans firstStep.1 laterEvolution, firstStep.2.trans laterAttempts⟩

/-! ## [STEP-START] -/

/-- The run part after a [STEP-START]: the attempt holds the snapshot and
`par(e, m)` is set, from before (a retained snapshot) or now. -/
theorem wpf3_runGood_start (ghost ghostAfter : Ghost σ) (run changed : Run σ)
    (good : WPF3RunGood ghost run) (stepId : Dex.StepExecId σ) (record : Dex.StepRecord σ)
    (isRecord : run.base.findStep? stepId = some record) (method : Dex.Method)
    (isInvocation : record.invocation = some { method := method, state := .pending })
    (taskId : Nat) (attempt startedAttempt : Attempt σ)
    (isAttempt : run.findAttempt? stepId = some attempt) (isMethod : attempt.method = method)
    (arguments : Arguments σ)
    (isBase : changed.base = (wpf3Bumped run).base.updateStep stepId (fun current =>
      { current with invocation := some { method := method, state := .running taskId } }))
    (isAttempts : changed.attempts = run.attempts.set stepId startedAttempt)
    (startedMethod : startedAttempt.method = attempt.method)
    (startedSnapshot : startedAttempt.snapshot = some arguments.snapshot)
    (ghostAt : ghostAfter.argumentsOf (stepId, method) = some arguments)
    (ghostElse : ∀ key, key ≠ (stepId, method) → ghostAfter.argumentsOf key = ghost.argumentsOf key)
    (sameInput : arguments.input = record.input)
    (sameResults : arguments.conditionResults = record.conditionResults)
    (sameContext : arguments.context =
      { stepExecutionId := stepId, fromStepExecutionId := record.origin }) :
    WPF3RunGood ghostAfter changed.reconcileAttempts := by
  obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? _ _ _ isRecord
  have currentMethod := wpf3_currentMethod run.base good.shape stepId record isRecord _ isInvocation
  have evolution : WPF3Evolution run.base changed.base := by
    rw [isBase]
    exact wpf3_evolution_trans (wpf3_evolution_bump run good.shape)
      (wpf3_evolution_setInvocation _ stepId (wpf3_evolution_bump run good.shape).2 method _
        currentMethod)
  obtain ⟨evolves, shape⟩ := evolution
  refine ⟨shape, ?_, ?_, ?_⟩
  · intro entry isEntryMember
    obtain ⟨next, invocation, isNextMember, isNextId, isNextInvocation, sameMethod⟩ :=
      wpf3_mem_reconcileAttempts changed entry isEntryMember
    exact ⟨next, isNextMember, isNextId, by rw [isNextInvocation, sameMethod]; rfl⟩
  · intro next isNextMember invocation isNextInvocation
    rw [wpf3_findAttempt?_reconcile changed shape.idsNodup next isNextMember]
    unfold wpf3ReconciledAttempt
    rw [isNextInvocation]
    simp only [Option.map_some, Option.some.injEq, exists_eq_left']
    have isNextBase : next ∈ ((wpf3Bumped run).base.updateStep stepId (fun current =>
        { current with invocation := some { method := method, state := .running taskId } })).steps :=
      isBase ▸ isNextMember
    obtain ⟨original, isOriginalMember, isNext⟩ := (Dex.Run.mem_updateStep _ _ _ _).mp isNextBase
    by_cases isStep : original.id = stepId
    · rw [if_pos isStep] at isNext
      have isOriginal : original = record := eq_of_nodup_map (fun record => record.id)
        run.base.steps good.shape.idsNodup isOriginalMember isMember (isStep.trans isId.symm)
      subst isOriginal
      subst isNext
      simp only [Option.some.injEq] at isNextInvocation
      subst isNextInvocation
      rw [isAttempts]
      simp only
      rw [isStep, AssocList.get?_set_self]
      dsimp only
      rw [if_pos (startedMethod.trans isMethod)]
      refine ⟨startedMethod.trans isMethod, ?_⟩
      show WPF3SnapshotMatches ghostAfter _ method startedAttempt.snapshot
      rw [startedSnapshot]
      exact ⟨arguments, ghostAt, rfl, sameInput, sameResults, sameContext⟩
    · rw [if_neg isStep] at isNext
      subst isNext
      rw [isAttempts]
      rw [AssocList.get?_set_other _ _ _ _ isStep]
      obtain ⟨previous, isPrevious, samePreviousMethod, isMatching⟩ :=
        good.attemptsMatch next isOriginalMember invocation isNextInvocation
      unfold Run.findAttempt? at isPrevious
      rw [isPrevious]
      dsimp only
      rw [if_pos samePreviousMethod]
      refine ⟨samePreviousMethod, ?_⟩
      have isOtherKey : (next.id, invocation.method) ≠ (stepId, method) := by
        intro isSame
        exact isStep (congrArg Prod.fst isSame)
      cases isSnapshot : previous.snapshot with
      | none =>
        rw [isSnapshot] at isMatching
        show ghostAfter.argumentsOf _ = none
        rw [ghostElse _ isOtherKey]
        exact isMatching
      | some snapshot =>
        rw [isSnapshot] at isMatching
        obtain ⟨found, hasFound, rest⟩ := isMatching
        exact ⟨found, by rw [ghostElse _ isOtherKey]; exact hasFound, rest⟩
  · intro key found hasFound
    by_cases isKey : key = (stepId, method)
    · subst isKey
      obtain ⟨next, isNextMember, isNextId⟩ := evolves.keepsIds record isMember
      refine ⟨next, isNextMember, isNextId.trans isId, ?_⟩
      rcases evolves.evolves next isNextMember with ⟨original, isOriginalMember, originalEvolves⟩ |
        isNew
      · have isOriginal : original = record := eq_of_nodup_map (fun record => record.id)
          run.base.steps good.shape.idsNodup isOriginalMember isMember
          (originalEvolves.sameId.symm.trans isNextId)
        subst isOriginal
        rcases originalEvolves.invocationExit _ isInvocation with
          ⟨current, isCurrent, sameCurrentMethod⟩ | isBeyond
        · exact Or.inl ⟨current, isCurrent, sameCurrentMethod⟩
        · exact Or.inr isBeyond
      · exact absurd isNextId.symm (isNew record isMember)
    · rw [ghostElse _ isKey] at hasFound
      obtain ⟨original, isOriginalMember, isOriginalId, isCurrentOrBeyond⟩ :=
        good.argumentsHaveRecords key found hasFound
      obtain ⟨next, isNextMember, isNextId⟩ := evolves.keepsIds original isOriginalMember
      refine ⟨next, isNextMember, isNextId.trans isOriginalId, ?_⟩
      rcases evolves.evolves next isNextMember with ⟨source, isSourceMember, sourceEvolves⟩ |
        isNew
      · have isSource : source = original := eq_of_nodup_map (fun record => record.id)
          run.base.steps good.shape.idsNodup isSourceMember isOriginalMember
          (sourceEvolves.sameId.symm.trans isNextId)
        subst isSource
        rcases isCurrentOrBeyond with ⟨invocation, isOriginalInvocation, sameMethod⟩ | isBeyond
        · rcases sourceEvolves.invocationExit invocation isOriginalInvocation with
            ⟨current, isCurrent, sameCurrentMethod⟩ | isNextBeyond
          · exact Or.inl ⟨current, isCurrent, sameCurrentMethod.trans sameMethod⟩
          · exact Or.inr (sameMethod ▸ isNextBeyond)
        · exact Or.inr (wpf3_isBeyond_mono isBeyond sourceEvolves.rankGrows)
      · exact absurd isNextId.symm (isNew original isOriginalMember)

end DexExpressiveness.DexF
