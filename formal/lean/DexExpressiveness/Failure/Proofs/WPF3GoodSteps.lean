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
    (taskId : Nat) (attempt startedAttempt : Attempt σ) (isMethod : attempt.method = method)
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
    · rw [ite_eq_left isStep] at isNext
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
      rw [ite_eq_left (startedMethod.trans isMethod)]
      refine ⟨startedMethod.trans isMethod, ?_⟩
      show WPF3SnapshotMatches ghostAfter _ method startedAttempt.snapshot
      rw [startedSnapshot]
      exact ⟨arguments, ghostAt, rfl, sameInput, sameResults, sameContext⟩
    · rw [ite_eq_right isStep] at isNext
      subst isNext
      rw [isAttempts]
      rw [AssocList.get?_set_other _ _ _ _ isStep]
      obtain ⟨previous, isPrevious, samePreviousMethod, isMatching⟩ :=
        good.attemptsMatch next isOriginalMember invocation isNextInvocation
      unfold Run.findAttempt? at isPrevious
      rw [isPrevious]
      dsimp only
      rw [ite_eq_left samePreviousMethod]
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

/-! ## Worker steps -/

/-- A step that changes one real task, keeps the run and `par`, and keeps or
grows `h`. -/
theorem wpf3_good_updateRealTask (program : Program σ) (config next : Config σ)
    (good : WPF3Good program config) (taskId : Nat) (task : Task σ)
    (isFound : config.findRealTask? taskId = some task) (change : Task σ → Task σ)
    (keepsId : ∀ candidate, (change candidate).base.id = candidate.base.id)
    (isTasks : next.tasks =
      config.tasks.map (fun candidate => if candidate.base.id = taskId then change candidate
        else candidate))
    (isZombies : next.zombies =
      config.zombies.map (fun candidate => if candidate.base.id = taskId then change candidate
        else candidate))
    (sameRun : next.run = config.run) (sameNext : next.nextTaskId = config.nextTaskId)
    (sameArguments : ∀ key, next.ghost.argumentsOf key = config.ghost.argumentsOf key)
    (countsGrow : ∀ key, config.ghost.emittedCountOf key ≤ next.ghost.emittedCountOf key)
    (changedFollows : WPF3TaskFollows program next.ghost (change task)) :
    WPF3Good program next := by
  obtain ⟨isMember, isId⟩ := wpf3_mem_of_findRealTask? config taskId task isFound
  have isRealTasks : next.realTasks = config.realTasks.map (fun candidate =>
      if candidate.base.id = taskId then change candidate else candidate) := by
    simp only [Config.realTasks, isTasks, isZombies, List.map_append]
  have sameIds : next.realTasks.map (fun candidate => candidate.base.id) =
      config.realTasks.map (fun candidate => candidate.base.id) := by
    rw [isRealTasks, List.map_map]
    apply List.map_congr_left
    intro candidate _
    simp only [Function.comp_apply]
    split
    · exact keepsId candidate
    · rfl
  refine ⟨by rw [sameIds]; exact good.realTaskIdsNodup, ?_, ?_, ?_, ?_⟩
  · intro candidate isCandidate
    rw [sameNext]
    rw [isRealTasks] at isCandidate
    obtain ⟨original, isOriginalMember, isCandidateEq⟩ := List.mem_map.mp isCandidate
    rw [← isCandidateEq]
    split
    · rw [keepsId]; exact good.realTaskIdsBelow original isOriginalMember
    · exact good.realTaskIdsBelow original isOriginalMember
  · intro candidate isCandidate
    rw [isRealTasks] at isCandidate
    obtain ⟨original, isOriginalMember, isCandidateEq⟩ := List.mem_map.mp isCandidate
    rw [← isCandidateEq]
    split
    · rename_i isOriginalId
      have isOriginal : original = task := eq_of_nodup_map (fun task => task.base.id)
        config.realTasks good.realTaskIdsNodup isOriginalMember isMember
        (isOriginalId.trans isId.symm)
      rw [isOriginal]
      exact changedFollows
    · exact wpf3_taskFollows_mono program config.ghost next.ghost original
        (good.tasksFollow original isOriginalMember) (sameArguments _) (countsGrow _)
  · intro isNone key
    rw [sameArguments]
    exact good.notStarted (sameRun ▸ isNone) key
  · intro run isRun
    exact wpf3_runGood_congr config.ghost next.ghost run (good.runGood run (sameRun ▸ isRun))
      sameArguments

theorem wpf3_good_workerRun (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (good : WPF3Good program config) (taskId : Nat) (isFirstRun : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.workerRun taskId isFirstRun) = some (events, next)) :
    WPF3Good program next := by
  dsimp only [step] at isStep
  cases isFound : config.findRealTask? taskId with
  | none => rw [isFound] at isStep; cases isStep
  | some task =>
    rw [isFound] at isStep
    dsimp only at isStep
    obtain ⟨isMember, _⟩ := wpf3_mem_of_findRealTask? config taskId task isFound
    obtain ⟨arguments, hasArguments, sameContext, sameInput, sameSnapshot, sameResults,
      emittedAtMost, startedFacts, _⟩ := good.tasksFollow task isMember
    have hasHistory : ∃ found, config.ghost.findInvocation? task.key = some found := by
      unfold Ghost.argumentsOf at hasArguments
      cases isFoundHistory : config.ghost.findInvocation? task.key with
      | none => rw [isFoundHistory] at hasArguments; cases hasArguments
      | some found => exact ⟨found, rfl⟩
    obtain ⟨found, isFoundHistory⟩ := hasHistory
    cases isPhase : task.base.phase with
    | running _ _ => rw [isPhase] at isStep; cases isStep
    | returned _ => rw [isPhase] at isStep; cases isStep
    | started =>
      rw [isPhase, isFoundHistory] at isStep
      dsimp only at isStep
      split at isStep
      · cases isStep
      obtain ⟨_, isNext⟩ := Prod.mk.inj (Option.some.inj isStep)
      have isGhostKeeps : WPF3GhostKeeps config.ghost next.ghost := by
        rw [← isNext]
        cases isFirstRun
        · exact wpf3_ghostKeeps_refl _
        · exact wpf3_ghostKeeps_updateInvocation _ _ _ (fun _ => rfl) (fun _ => rfl)
      have isCompute := wpf3_task_compute program ignoresAttemptContext task arguments
        sameContext sameInput sameSnapshot sameResults
      refine wpf3_good_updateRealTask program config next good taskId task isFound
        (fun current => { current with
          base := { current.base with
            phase := .running (task.base.compute (program task.attemptContext)).1
              (task.base.compute (program task.attemptContext)).2 } })
        (fun _ => rfl) (by rw [← isNext]; rfl) (by rw [← isNext]; rfl) (by rw [← isNext]; rfl)
        (by rw [← isNext]; rfl)
        (fun key => (isGhostKeeps key).1) (fun key => Nat.le_of_eq (isGhostKeeps key).2.symm) ?_
      refine ⟨arguments, by rw [(isGhostKeeps _).1]; exact hasArguments, sameContext, sameInput,
        sameSnapshot, sameResults, by rw [(isGhostKeeps _).2]; exact emittedAtMost,
        (fun isStarted => by simp at isStarted), ?_⟩
      intro remaining response isRunning
      simp only [Dex.TaskPhase.running.injEq] at isRunning
      rw [← isRunning.1, isCompute, startedFacts isPhase, List.drop_zero]
      rfl

theorem wpf3_good_workerEffect (program : Program σ) (config : Config σ)
    (good : WPF3Good program config) (taskId : Nat) (isFirstOccurrence : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.workerEffect taskId isFirstOccurrence) = some (events, next)) :
    WPF3Good program next := by
  dsimp only [step] at isStep
  cases isFound : config.findRealTask? taskId with
  | none => rw [isFound] at isStep; cases isStep
  | some task =>
    rw [isFound] at isStep
    dsimp only at isStep
    obtain ⟨isMember, _⟩ := wpf3_mem_of_findRealTask? config taskId task isFound
    obtain ⟨arguments, hasArguments, sameContext, sameInput, sameSnapshot, sameResults,
      emittedAtMost, _, runningFacts⟩ := good.tasksFollow task isMember
    cases isPhase : task.base.phase with
    | started => rw [isPhase] at isStep; cases isStep
    | returned _ => rw [isPhase] at isStep; cases isStep
    | running remaining response =>
      cases remaining with
      | nil => rw [isPhase] at isStep; cases isStep
      | cons effect laterEffects =>
        have isRemaining := runningFacts _ _ isPhase
        rw [isPhase] at isStep
        dsimp only at isStep
        split at isStep
        · cases isStep
        rename_i isAgree
        have isFirstEq : isFirstOccurrence =
            decide (config.ghost.emittedCountOf task.key < task.emittedCount + 1) := by
          simpa using isAgree
        obtain ⟨_, isNext⟩ := Prod.mk.inj (Option.some.inj isStep)
        have hasHistory : ∃ found, config.ghost.findInvocation? task.key = some found := by
          unfold Ghost.argumentsOf at hasArguments
          cases isFoundHistory : config.ghost.findInvocation? task.key with
          | none => rw [isFoundHistory] at hasArguments; cases hasArguments
          | some found => exact ⟨found, rfl⟩
        obtain ⟨found, isFoundHistory⟩ := hasHistory
        have nextArguments : ∀ key, next.ghost.argumentsOf key = config.ghost.argumentsOf key := by
          intro key
          rw [← isNext]
          cases isFirstOccurrence
          · rfl
          · exact wpf3_argumentsOf_updateInvocation _ _ _ _ (fun _ => rfl)
        have nextCount : ∀ key, next.ghost.emittedCountOf key =
            if isFirstOccurrence && decide (key = task.key) then task.emittedCount + 1
            else config.ghost.emittedCountOf key := by
          intro key
          rw [← isNext]
          cases isFirstOccurrence
          · rfl
          · show (config.ghost.updateInvocation task.key _).emittedCountOf key = _
            rw [wpf3_emittedCountOf_updateInvocation]
            by_cases isKey : key = task.key
            · subst isKey
              simp [isFoundHistory]
            · simp [isKey]
        have countsGrow : ∀ key, config.ghost.emittedCountOf key ≤
            next.ghost.emittedCountOf key := by
          intro key
          rw [nextCount]
          split
          · rename_i isFirstAndKey
            simp only [Bool.and_eq_true, decide_eq_true_eq] at isFirstAndKey
            obtain ⟨isFirst, isKey⟩ := isFirstAndKey
            rw [isFirst] at isFirstEq
            simp only [Bool.true_eq, decide_eq_true_eq] at isFirstEq
            rw [isKey]; omega
          · exact Nat.le_refl _
        refine wpf3_good_updateRealTask program config next good taskId task isFound
          (fun current => { current with
            base := { current.base with phase := .running laterEffects response }
            emittedCount := task.emittedCount + 1 })
          (fun _ => rfl) (by rw [← isNext]; rfl) (by rw [← isNext]; rfl) (by rw [← isNext]; rfl)
          (by rw [← isNext]; rfl) nextArguments countsGrow ?_
        refine ⟨arguments, by rw [nextArguments]; exact hasArguments, sameContext, sameInput,
          sameSnapshot, sameResults, ?_, (fun isStarted => by simp at isStarted), ?_⟩
        · show task.emittedCount + 1 ≤ next.ghost.emittedCountOf task.key
          rw [nextCount]
          cases isFirstOccurrence
          · simp only [Bool.false_eq, decide_eq_false_iff_not, Nat.not_lt] at isFirstEq
            simpa using isFirstEq
          · simp
        · intro remaining laterResponse isRunning
          simp only [Dex.TaskPhase.running.injEq] at isRunning
          rw [← isRunning.1]
          show laterEffects = (arguments.compute program.failureFreeFlow task.key).1.drop
            (task.emittedCount + 1)
          rw [List.drop_add_one_eq_tail_drop, ← isRemaining]
          rfl

theorem wpf3_good_workerReturn (program : Program σ) (config : Config σ)
    (good : WPF3Good program config) (taskId : Nat) (isFirstReturn : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.workerReturn taskId isFirstReturn) = some (events, next)) :
    WPF3Good program next := by
  dsimp only [step] at isStep
  cases isFound : config.findRealTask? taskId with
  | none => rw [isFound] at isStep; cases isStep
  | some task =>
    rw [isFound] at isStep
    dsimp only at isStep
    obtain ⟨isMember, _⟩ := wpf3_mem_of_findRealTask? config taskId task isFound
    obtain ⟨arguments, hasArguments, sameContext, sameInput, sameSnapshot, sameResults,
      emittedAtMost, _, _⟩ := good.tasksFollow task isMember
    have hasHistory : ∃ found, config.ghost.findInvocation? task.key = some found := by
      unfold Ghost.argumentsOf at hasArguments
      cases isFoundHistory : config.ghost.findInvocation? task.key with
      | none => rw [isFoundHistory] at hasArguments; cases hasArguments
      | some found => exact ⟨found, rfl⟩
    obtain ⟨found, isFoundHistory⟩ := hasHistory
    cases isPhase : task.base.phase with
    | started => rw [isPhase] at isStep; cases isStep
    | returned _ => rw [isPhase] at isStep; cases isStep
    | running remaining response =>
      cases remaining with
      | cons _ _ => rw [isPhase] at isStep; cases isStep
      | nil =>
        rw [isPhase, isFoundHistory] at isStep
        dsimp only at isStep
        split at isStep
        · cases isStep
        obtain ⟨_, isNext⟩ := Prod.mk.inj (Option.some.inj isStep)
        have isGhostKeeps : WPF3GhostKeeps config.ghost next.ghost := by
          rw [← isNext]
          cases isFirstReturn
          · exact wpf3_ghostKeeps_refl _
          · exact wpf3_ghostKeeps_updateInvocation _ _ _ (fun _ => rfl) (fun _ => rfl)
        refine wpf3_good_updateRealTask program config next good taskId task isFound
          (fun current => { current with base := { current.base with phase := .returned response } })
          (fun _ => rfl) (by rw [← isNext]; rfl) (by rw [← isNext]; rfl) (by rw [← isNext]; rfl)
          (by rw [← isNext]; rfl) (fun key => (isGhostKeeps key).1)
          (fun key => Nat.le_of_eq (isGhostKeeps key).2.symm) ?_
        exact ⟨arguments, by rw [(isGhostKeeps _).1]; exact hasArguments, sameContext, sameInput,
          sameSnapshot, sameResults, by rw [(isGhostKeeps _).2]; exact emittedAtMost,
          (fun isStarted => by simp at isStarted), (fun _ _ isRunning => by simp at isRunning)⟩

/-! ## [STEP-START], the configuration -/

theorem wpf3_good_start (program : Program σ) (config next : Config σ)
    (good : WPF3Good program config) (run : Run σ) (isRun : config.run = some run)
    (stepId : Dex.StepExecId σ) (record : Dex.StepRecord σ)
    (isRecord : run.base.findStep? stepId = some record) (method : Dex.Method)
    (isInvocation : record.invocation = some { method := method, state := .pending })
    (attempt : Attempt σ)
    (isMethod : attempt.method = method) (arguments : Arguments σ) (newTask : Task σ)
    (newTaskFacts : newTask.base.id = config.nextTaskId ∧ newTask.base.step = stepId ∧
      newTask.base.method = method ∧ newTask.base.context = arguments.context ∧
      newTask.base.input = arguments.input ∧ newTask.base.snapshot = arguments.snapshot ∧
      newTask.base.conditionResults = arguments.conditionResults ∧
      newTask.base.phase = .started ∧ newTask.emittedCount = 0)
    (isRealTasks : next.realTasks = (config.tasks ++ [newTask]) ++ config.zombies)
    (isNextTaskId : next.nextTaskId = config.nextTaskId + 1) (changed : Run σ)
    (isNextRun : next.run = some changed.reconcileAttempts)
    (isBase : changed.base = (wpf3Bumped run).base.updateStep stepId (fun current =>
      { current with invocation := some { method := method, state := .running config.nextTaskId } }))
    (startedAttempt : Attempt σ) (isAttempts : changed.attempts = run.attempts.set stepId startedAttempt)
    (startedMethod : startedAttempt.method = attempt.method)
    (startedSnapshot : startedAttempt.snapshot = some arguments.snapshot)
    (ghostAt : next.ghost.argumentsOf (stepId, method) = some arguments)
    (ghostElse : ∀ key, key ≠ (stepId, method) →
      next.ghost.argumentsOf key = config.ghost.argumentsOf key)
    (sameCounts : ∀ key, next.ghost.emittedCountOf key = config.ghost.emittedCountOf key)
    (oldTasks : ∀ task, task ∈ config.realTasks → task.key = (stepId, method) →
      config.ghost.argumentsOf (stepId, method) = some arguments)
    (sameInput : arguments.input = record.input)
    (sameResults : arguments.conditionResults = record.conditionResults)
    (sameContext : arguments.context =
      { stepExecutionId := stepId, fromStepExecutionId := record.origin }) :
    WPF3Good program next := by
  obtain ⟨isNewId, isNewStep, isNewMethod, isNewContext, isNewInput, isNewSnapshot, isNewResults,
    isNewPhase, isNewCount⟩ := newTaskFacts
  have isPerm : next.realTasks.Perm (config.realTasks ++ [newTask]) := by
    rw [isRealTasks]
    unfold Config.realTasks
    rw [List.append_assoc, List.append_assoc]
    exact List.Perm.append_left _ List.perm_append_comm
  have isFresh : ∀ task, task ∈ config.realTasks → task.base.id ≠ newTask.base.id := by
    intro task isMember isSame
    have isBelow := good.realTaskIdsBelow task isMember
    rw [isSame, isNewId] at isBelow
    exact Nat.lt_irrefl _ isBelow
  have argumentsOld : ∀ task, task ∈ config.realTasks →
      next.ghost.argumentsOf task.key = config.ghost.argumentsOf task.key := by
    intro task isMember
    by_cases isKey : task.key = (stepId, method)
    · rw [isKey, ghostAt, oldTasks task isMember isKey]
    · exact ghostElse _ isKey
  refine ⟨?_, ?_, ?_, ?_, ?_⟩
  · apply wpf3_ids_nodup_of_perm isPerm
    rw [List.map_append, List.nodup_append]
    refine ⟨good.realTaskIdsNodup, by simp, ?_⟩
    intro first isFirst second isSecond isSame
    obtain ⟨task, isTask, isTaskId⟩ := List.mem_map.mp isFirst
    simp only [List.map_cons, List.map_nil, List.mem_singleton] at isSecond
    rw [← isTaskId, isSecond] at isSame
    exact isFresh task isTask isSame
  · intro task isMember
    rw [isNextTaskId]
    rcases List.mem_append.mp (isPerm.mem_iff.mp isMember) with isOld | isNew
    · exact Nat.lt_succ_of_lt (good.realTaskIdsBelow task isOld)
    · rw [List.mem_singleton] at isNew
      rw [isNew, isNewId]
      exact Nat.lt_succ_self _
  · intro task isMember
    rcases List.mem_append.mp (isPerm.mem_iff.mp isMember) with isOld | isNew
    · exact wpf3_taskFollows_mono program config.ghost next.ghost task
        (good.tasksFollow task isOld) (argumentsOld task isOld) (Nat.le_of_eq (sameCounts _).symm)
    · rw [List.mem_singleton] at isNew
      subst isNew
      have isKey : task.key = (stepId, method) := by
        unfold Task.key; rw [isNewStep, isNewMethod]
      refine ⟨arguments, by rw [isKey]; exact ghostAt, isNewContext, isNewInput, isNewSnapshot,
        isNewResults, by rw [isNewCount]; exact Nat.zero_le _, fun _ => isNewCount, ?_⟩
      intro remaining response isRunning
      rw [isNewPhase] at isRunning
      cases isRunning
  · intro isNone
    rw [isNextRun] at isNone
    cases isNone
  · intro after isAfter
    rw [isNextRun] at isAfter
    cases isAfter
    exact wpf3_runGood_start config.ghost next.ghost run changed (good.runGood run isRun) stepId
      record isRecord method isInvocation config.nextTaskId attempt startedAttempt
      isMethod arguments isBase isAttempts startedMethod startedSnapshot ghostAt ghostElse
      sameInput sameResults sameContext

theorem wpf3_commitRun_hasHistory (config : Config σ) (run : Run σ) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) (record : Dex.StepRecord σ) (invocation : Dex.Invocation)
    (isMember : record ∈ (change (wpf3Bumped run)).base.steps)
    (isInvocation : record.invocation = some invocation) :
    ((config.commitRun run isNormal completed inputEvents change).2.ghost.findInvocation?
      (record.id, invocation.method)).isSome = true := by
  unfold Config.commitRun
  dsimp only
  exact (wpf3_reconcile_keeps _ _ _).2 record invocation isMember isInvocation

theorem wpf3_good_stepStart (program : Program σ) (config : Config σ)
    (good : WPF3Good program config) (stepId : Dex.StepExecId σ) (isSnapshotReused : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.stepStart stepId isSnapshotReused) = some (events, next)) :
    WPF3Good program next := by
  dsimp only [step] at isStep
  split at isStep
  · cases isRun : config.run with
    | none => rw [isRun] at isStep; simp at isStep
    | some run =>
      rw [isRun] at isStep
      dsimp only at isStep
      cases isRecord : run.base.findStep? stepId with
      | none => rw [isRecord] at isStep; simp at isStep
      | some record =>
        cases isAttempt : run.findAttempt? stepId with
        | none => rw [isRecord, isAttempt] at isStep; simp at isStep
        | some attempt =>
          rw [isRecord, isAttempt] at isStep
          dsimp only at isStep
          cases isInvocation : record.invocation with
          | none => rw [isInvocation] at isStep; simp at isStep
          | some invocation =>
            obtain ⟨method, state⟩ := invocation
            cases state with
            | running _ => rw [isInvocation] at isStep; simp at isStep
            | pending =>
              rw [isInvocation] at isStep
              dsimp only at isStep
              split at isStep
              · cases isStep
              rename_i isAllowed
              obtain ⟨_, isMethod, isReusedEq⟩ : attempt.isBackoff = false ∧
                  attempt.method = method ∧ isSnapshotReused = attempt.snapshot.isSome := by
                revert isAllowed
                cases attempt.isBackoff <;> cases isSnapshotReused <;> cases attempt.snapshot <;>
                  by_cases isSame : attempt.method = method <;> simp [isSame]
              obtain ⟨_, isNext⟩ := Prod.mk.inj (Option.some.inj isStep)
              have runGood := good.runGood run isRun
              obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? _ _ _ isRecord
              obtain ⟨found, isFoundAttempt, _, matchesBefore⟩ :=
                runGood.attemptsMatch record isMember _ isInvocation
              rw [isId, isAttempt] at isFoundAttempt
              cases isFoundAttempt
              let startChange : Run σ → Run σ := fun current =>
                { (current.setAttempt stepId
                    { attempt with
                      snapshot := some (attempt.snapshot.getD
                        (run.base.snapshot record.options method))
                      firstAttemptTimestamp :=
                        some (attempt.context config.clock).firstAttemptTimestamp }) with
                  base := current.base.updateStep stepId (fun currentRecord =>
                    { currentRecord with
                      invocation :=
                        some { method := method, state := .running config.nextTaskId } }) }
              let newTask : Task σ :=
                { base :=
                    { id := config.nextTaskId, step := stepId, method := method,
                      context := { stepExecutionId := stepId, fromStepExecutionId := record.origin },
                      input := record.input,
                      snapshot := attempt.snapshot.getD (run.base.snapshot record.options method),
                      conditionResults := record.conditionResults, phase := .started }
                  attemptContext := attempt.context config.clock
                  emittedCount := 0 }
              have isRealTasks : next.realTasks = (config.tasks ++ [newTask]) ++ config.zombies := by
                subst isNext; rfl
              have isNextTaskId : next.nextTaskId = config.nextTaskId + 1 := by subst isNext; rfl
              have isNextRun : next.run = some (startChange (wpf3Bumped run)).reconcileAttempts := by
                subst isNext; rfl
              have ghostKeeps := wpf3_commitRun_ghostKeeps config run (!isSnapshotReused) none []
                startChange
              cases isSnapshot : attempt.snapshot with
              | some snapshot =>
                have isReused : isSnapshotReused = true := by rw [isReusedEq, isSnapshot]; rfl
                subst isReused
                rw [isSnapshot] at matchesBefore
                obtain ⟨arguments, hasArguments, isArgumentsSnapshot, sameInput, sameResults,
                  sameContext⟩ := matchesBefore
                rw [isId] at hasArguments sameContext
                have isGhost : next.ghost =
                    (config.commitRun run (!true) none [] startChange).2.ghost := by
                  subst isNext; rfl
                have isNewSnapshot : attempt.snapshot.getD (run.base.snapshot record.options method) =
                    arguments.snapshot := by
                  rw [isSnapshot, isArgumentsSnapshot]; rfl
                refine wpf3_good_start program config next good run isRun stepId record isRecord
                  method isInvocation attempt isMethod arguments newTask ?_ isRealTasks
                  isNextTaskId _ isNextRun rfl _ rfl rfl
                  (by show some _ = _; rw [isNewSnapshot])
                  (by rw [isGhost, (ghostKeeps _).1]; exact hasArguments)
                  (fun key _ => by rw [isGhost, (ghostKeeps _).1])
                  (fun key => by rw [isGhost, (ghostKeeps _).2])
                  (fun _ _ _ => hasArguments) sameInput sameResults sameContext
                exact ⟨rfl, rfl, rfl, sameContext.symm, sameInput.symm, isNewSnapshot,
                  sameResults.symm, rfl, rfl⟩
              | none =>
                have isReused : isSnapshotReused = false := by rw [isReusedEq, isSnapshot]; rfl
                subst isReused
                rw [isSnapshot] at matchesBefore
                have hasNoArguments : config.ghost.argumentsOf (stepId, method) = none := by
                  rw [← isId]; exact matchesBefore
                let committedGhost := (config.commitRun run (!false) none [] startChange).2.ghost
                have hasHistory : (committedGhost.findInvocation? (stepId, method)).isSome = true := by
                  have hasHistoryRecord := wpf3_commitRun_hasHistory config run (!false) none []
                    startChange
                    { record with
                      invocation := some { method := method, state := .running config.nextTaskId } }
                    { method := method, state := .running config.nextTaskId }
                    ((Dex.Run.mem_updateStep _ _ _ _).mpr ⟨record, isMember, by rw [ite_eq_left isId]⟩)
                    rfl
                  have hasHistoryById : (committedGhost.findInvocation? (record.id, method)).isSome =
                      true := hasHistoryRecord
                  rw [isId] at hasHistoryById
                  exact hasHistoryById
                let newArguments : Arguments σ :=
                  { context := { stepExecutionId := stepId, fromStepExecutionId := record.origin },
                    input := record.input,
                    snapshot := attempt.snapshot.getD (run.base.snapshot record.options method),
                    conditionResults := record.conditionResults,
                    virtualTaskId := committedGhost.nextVirtualTaskId }
                obtain ⟨freshCounts, freshOthers⟩ := wpf3_freshStartGhost committedGhost
                  (stepId, method) newArguments (committedGhost.nextVirtualTaskId + 1)
                have isGhost : next.ghost =
                    { (committedGhost.updateInvocation (stepId, method) (fun history =>
                        { history with arguments := some newArguments })) with
                      nextVirtualTaskId := committedGhost.nextVirtualTaskId + 1 } := by
                  subst isNext; rfl
                refine wpf3_good_start program config next good run isRun stepId record isRecord
                  method isInvocation attempt isMethod newArguments newTask
                  ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩ isRealTasks isNextTaskId _
                  isNextRun rfl _ rfl rfl rfl ?_ ?_ ?_ ?_ rfl rfl rfl
                · rw [isGhost]
                  show (committedGhost.updateInvocation (stepId, method) _).argumentsOf
                    (stepId, method) = _
                  unfold Ghost.argumentsOf
                  rw [wpf3_findInvocation?_updateInvocation]
                  simp only [↓reduceIte]
                  cases isFoundHistory : committedGhost.findInvocation? (stepId, method) with
                  | none => rw [isFoundHistory] at hasHistory; cases hasHistory
                  | some history => rfl
                · intro key isOther
                  rw [isGhost]
                  exact (freshOthers key isOther).trans ((ghostKeeps key).1)
                · intro key
                  rw [isGhost]
                  exact (freshCounts key).trans ((ghostKeeps key).2)
                · intro task isTaskMember isKey
                  exfalso
                  obtain ⟨taskArguments, hasTaskArguments, _⟩ := good.tasksFollow task isTaskMember
                  rw [isKey, hasNoArguments] at hasTaskArguments
                  cases hasTaskArguments
  · cases isStep

/-! ## Failures of an attempt -/

theorem wpf3_good_failAttempt (program : Program σ) (committing : Config σ)
    (good : WPF3Good program committing) (run : Run σ) (isRun : committing.run = some run)
    (stepId : Dex.StepExecId σ) (record : Dex.StepRecord σ)
    (isRecord : run.base.findStep? stepId = some record) (method : Dex.Method)
    (state : Dex.InvocationState)
    (isInvocation : record.invocation = some { method := method, state := state })
    (attempt : Attempt σ) (isAttempt : run.findAttempt? stepId = some attempt)
    (cause : FailureCause) (lostTaskId : Option Nat) (decision : RetryDecision) :
    WPF3Good program
      (committing.failAttempt run stepId method attempt cause lostTaskId decision).2 := by
  have runGood := good.runGood run isRun
  have currentMethod := wpf3_currentMethod run.base runGood.shape stepId record isRecord _
    isInvocation
  have movedGood : WPF3Good program (match lostTaskId with
      | some taskId => committing.moveToZombies taskId
      | none => committing) := by
    cases lostTaskId with
    | some taskId => exact wpf3_good_moveToZombies program committing good taskId
    | none => exact good
  have movedRunGood : WPF3RunGood (match lostTaskId with
      | some taskId => committing.moveToZombies taskId
      | none => committing).ghost run := by
    cases lostTaskId <;> exact runGood
  unfold Config.failAttempt
  cases decision with
  | retryAfter backoffMicros =>
    dsimp only
    refine wpf3_good_commitRun program _ movedGood run movedRunGood false none [] _ ?_ ?_
    · exact wpf3_evolution_trans (wpf3_evolution_bump run runGood.shape)
        (wpf3_evolution_setInvocation _ stepId (wpf3_evolution_bump run runGood.shape).2 method
          .pending currentMethod)
    · exact wpf3_attemptsEvolve_set run.attempts stepId attempt _ isAttempt rfl rfl
  | exhausted _ =>
    dsimp only
    refine wpf3_good_commitRun program _ movedGood run movedRunGood false none [] _ ?_
      (WPF3AttemptsEvolve.refl _)
    exact wpf3_evolution_trans (wpf3_evolution_bump run runGood.shape)
      (wpf3_evolution_closeRun _ _ (wpf3_evolution_bump run runGood.shape).2)

/-- The fence of a started-attempt command finds the running invocation. -/
theorem wpf3_fence_facts (config : Config σ) (run : Run σ) (isRun : config.run = some run)
    (task : Task σ) (isFence : config.isFencePassed task = true) :
    ∃ record, run.base.findStep? task.base.step = some record ∧
      record.invocation = some { method := task.base.method, state := .running task.base.id } := by
  unfold Config.isFencePassed at isFence
  rw [isRun] at isFence
  dsimp only at isFence
  cases isRecordCase : run.base.findStep? task.base.step with
  | none => rw [isRecordCase] at isFence; simp at isFence
  | some record =>
    cases isAttemptCase : run.findAttempt? task.base.step with
    | none => rw [isRecordCase, isAttemptCase] at isFence; simp at isFence
    | some attempt =>
      rw [isRecordCase, isAttemptCase] at isFence
      simp only [Bool.and_eq_true, decide_eq_true_eq] at isFence
      exact ⟨record, rfl, isFence.1⟩

end DexExpressiveness.DexF
