import DexExpressiveness.Failure.Proofs.WPF4WorkerEffects

/-!
# WP-F4: the rules that keep the run

[TICK], [WORKER-ABANDON] and the drop of a zombie, the commits that only
stamp ([START-RETRY], `internalCommit`, [RESULT-STALE], an ignored
[RECOVER-INTERRUPT]), [CRASH], and [RECOVER].
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- Every task is its own origin. -/
theorem task_from_self (task : Task σ) :
    ∃ original, original = task ∧ original.base.id = task.base.id ∧
      original.base.step = task.base.step ∧ original.base.method = task.base.method ∧
      original.attemptContext.attempt = task.attemptContext.attempt :=
  ⟨task, rfl, rfl, rfl, rfl, rfl⟩

/-- A stamp-only commit keeps the invariants. -/
theorem invariant_stampOnly {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) (canCommit : config.canCommit = true) :
    Invariant program config.stampOnly := by
  refine invariant_transfer invariant rfl rfl (GhostFrame.refl _) rfl rfl rfl
    (List.Sublist.refl _) (fun task isTask => ⟨task, isTask, rfl, rfl, rfl, rfl⟩)
    (fun task isTask => ⟨task, isTask, rfl, rfl, rfl, rfl⟩) (fun _ isMember => isMember)
    invariant.downIsEmpty ?_ invariant.historiesConsistent invariant.realTasksFollowHistory
    (fun run isRun => (invariant.runInvariant run isRun).runningIsTracked)
  have lastNormal := invariant.lastNormalCommitAtMost
  show IsStampAtMost config.ghost.lastNormalCommitAt (some config.clock)
  unfold Config.canCommit at canCommit
  revert lastNormal canCommit
  cases config.ghost.lastNormalCommitAt with
  | none => intro _ _; trivial
  | some stamp =>
    cases config.lastCommitAt with
    | none => intro _ lastNormal; exact False.elim lastNormal
    | some last =>
      intro lastNormal canCommit
      simp only [IsStampAtMost, decide_eq_true_eq] at lastNormal canCommit ⊢
      omega

/-- Removing a real task that tracks no running invocation keeps the invariants. -/
theorem invariant_removeRealTask {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) (taskId : Nat)
    (notTracking : ∀ task, task ∈ config.tasks → task.base.id = taskId →
      config.isFencePassed task = false) :
    Invariant program (config.removeRealTask taskId) := by
  have realFrom : ∀ task, task ∈ (config.removeRealTask taskId).realTasks →
      task ∈ config.realTasks := by
    intro task isTask
    rw [realTasks_removeRealTask] at isTask
    exact (List.mem_filter.mp isTask).1
  refine invariant_transfer invariant rfl rfl (GhostFrame.refl _) rfl rfl rfl ?_
    (fun task isTask => ⟨task, realFrom task isTask, rfl, rfl, rfl, rfl⟩)
    (fun task isTask => ⟨task, (List.mem_filter.mp isTask).1, rfl, rfl, rfl, rfl⟩)
    (fun _ isMember => isMember) ?_ invariant.lastNormalCommitAtMost invariant.historiesConsistent
    (fun task isTask => invariant.realTasksFollowHistory task (realFrom task isTask)) ?_
  · rw [realTasks_removeRealTask]
    exact List.Sublist.map _ List.filter_sublist
  · intro isDown
    obtain ⟨noTasks, noInterrupts⟩ := invariant.downIsEmpty isDown
    refine ⟨?_, noInterrupts⟩
    show config.tasks.filter _ = []
    rw [noTasks]
    rfl
  · intro run isRun record method runningTaskId isMember hasInvocation
    rcases (invariant.runInvariant run isRun).runningIsTracked record method runningTaskId isMember
        hasInvocation with ⟨task, isTask, isId⟩ | isDown | isInterrupt
    · left
      refine ⟨task, List.mem_filter.mpr ⟨isTask, ?_⟩, isId⟩
      by_cases isRemoved : task.base.id = taskId
      · have isPassed := isFencePassed_of_running invariant isRun isMember hasInvocation
          (List.mem_append_left _ isTask) isId
        rw [notTracking task isTask isRemoved] at isPassed
        cases isPassed
      · simp [isRemoved]
    · exact Or.inr (Or.inl isDown)
    · exact Or.inr (Or.inr isInterrupt)

/-- [TICK] keeps the invariants. -/
theorem invariant_tick (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (duration : Nat) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config (.tick duration) = some (events, next)) :
    Invariant program next := by
  simp only [step] at isStep
  split at isStep
  · simp only [Option.some.injEq, Prod.mk.injEq] at isStep
    obtain ⟨-, rfl⟩ := isStep
    exact invariant_transfer invariant rfl rfl (GhostFrame.refl _) rfl rfl rfl
      (List.Sublist.refl _) (fun task isTask => ⟨task, isTask, rfl, rfl, rfl, rfl⟩)
      (fun task isTask => ⟨task, isTask, rfl, rfl, rfl, rfl⟩) (fun _ isMember => isMember)
      invariant.downIsEmpty invariant.lastNormalCommitAtMost invariant.historiesConsistent
      invariant.realTasksFollowHistory
      (fun run isRun => (invariant.runInvariant run isRun).runningIsTracked)
  · cases isStep

/-- A task of `Θ` and a task of `Z` have different IDs. -/
theorem id_ne_of_task_zombie {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) {task zombie : Task σ} (isTask : task ∈ config.tasks)
    (isZombie : zombie ∈ config.zombies) : task.base.id ≠ zombie.base.id := by
  have idsNodup := invariant.realTaskIdsNodup
  unfold Config.realTasks at idsNodup
  rw [List.map_append, List.nodup_append] at idsNodup
  intro sameId
  exact idsNodup.2.2 _ (List.mem_map.mpr ⟨task, isTask, rfl⟩) _
    (List.mem_map.mpr ⟨zombie, isZombie, rfl⟩) sameId

/-- [WORKER-ABANDON] and the drop of a zombie keep the invariants. -/
theorem invariant_workerAbandon (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (isLastOfCanceled : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.workerAbandon taskId isLastOfCanceled) = some (events, next)) :
    Invariant program next := by
  simp only [step] at isStep
  cases isLive : config.findLiveTask? taskId with
  | some task =>
    obtain ⟨isTask, isTaskId⟩ := mem_of_findLiveTask? isLive
    simp only [isLive] at isStep
    split at isStep
    · cases isStep
    · rename_i isGuard
      simp only [Option.some.injEq, Prod.mk.injEq] at isStep
      obtain ⟨-, rfl⟩ := isStep
      have notPassed : config.isFencePassed task = false := by
        simp only [Bool.or_eq_true, not_or] at isGuard
        simpa using isGuard.1
      refine invariant_removeRealTask invariant taskId ?_
      intro other isOther isId
      rw [task_eq_of_id_eq invariant.realTaskIdsNodup (List.mem_append_left _ isOther)
        (List.mem_append_left _ isTask) (by rw [isId, isTaskId])]
      exact notPassed
  | none =>
    cases isZombie : config.findZombie? taskId with
    | none => simp [isLive, isZombie] at isStep
    | some task =>
      obtain ⟨isZombieMember, isTaskId⟩ := mem_of_findZombie? isZombie
      simp only [isLive, isZombie] at isStep
      split at isStep
      · cases isStep
      · simp only [Option.some.injEq, Prod.mk.injEq] at isStep
        obtain ⟨-, rfl⟩ := isStep
        refine invariant_removeRealTask invariant taskId ?_
        intro other isOther isId
        exact absurd (by rw [isId, isTaskId]) (id_ne_of_task_zombie invariant isOther isZombieMember)

/-- [CRASH] keeps the invariants. -/
theorem invariant_crash (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config .crash = some (events, next)) : Invariant program next := by
  simp only [step] at isStep
  split at isStep
  · simp only [Option.some.injEq, Prod.mk.injEq] at isStep
    obtain ⟨-, rfl⟩ := isStep
    have isPerm : ([] ++ (config.zombies ++ config.tasks) : List (Task σ)).Perm config.realTasks := by
      unfold Config.realTasks
      simpa using List.perm_append_comm
    have realFrom : ∀ task, task ∈ ([] ++ (config.zombies ++ config.tasks) : List (Task σ)) →
        task ∈ config.realTasks := fun task isTask => isPerm.mem_iff.mp isTask
    refine invariant_transfer_general invariant rfl rfl (GhostFrame.refl _) rfl rfl rfl ?_
      (fun task isTask => ⟨task, realFrom task isTask, rfl, rfl, rfl, rfl⟩)
      (fun task isTask => by cases isTask) (fun _ _ isInterrupt => by cases isInterrupt)
      (fun _ isInterrupt => by cases isInterrupt) (fun _ => rfl) (fun _ => ⟨rfl, rfl⟩)
      invariant.lastNormalCommitAtMost invariant.historiesConsistent
      (fun task isTask => invariant.realTasksFollowHistory task (realFrom task isTask))
      (fun _ _ _ _ _ _ _ => Or.inr (Or.inl rfl))
    exact (isPerm.map _).nodup_iff.mpr invariant.realTaskIdsNodup
  · cases isStep

/-- The members of `runningInterrupts`. -/
theorem mem_runningInterrupts (run : Run σ) (interrupt : Interrupt σ)
    (isMember : interrupt ∈ run.runningInterrupts) :
    ∃ record attempt, record ∈ run.base.steps ∧ record.id = interrupt.step ∧
      record.invocation = some { method := interrupt.method, state := .running interrupt.taskId } ∧
      run.attempts.get? record.id = some attempt ∧ attempt.number = interrupt.attempt := by
  unfold Run.runningInterrupts at isMember
  obtain ⟨record, isRecord, isMapped⟩ := List.mem_filterMap.mp isMember
  split at isMapped
  · rename_i method taskId attempt hasInvocation isAttempt
    cases isMapped
    exact ⟨record, attempt, isRecord, rfl, hasInvocation, isAttempt, rfl⟩
  · cases isMapped

/-- A running Step with an attempt state is in `runningInterrupts`. -/
theorem mem_runningInterrupts_of_running (run : Run σ) (record : Dex.StepRecord σ)
    (method : Dex.Method) (taskId : Nat) (attempt : Attempt σ) (isMember : record ∈ run.base.steps)
    (hasInvocation : record.invocation = some { method := method, state := .running taskId })
    (isAttempt : run.findAttempt? record.id = some attempt) :
    ({ step := record.id, method := method, attempt := attempt.number, taskId := taskId } :
      Interrupt σ) ∈ run.runningInterrupts := by
  unfold Run.runningInterrupts
  apply List.mem_filterMap.mpr
  refine ⟨record, isMember, ?_⟩
  unfold Run.findAttempt? at isAttempt
  rw [hasInvocation, isAttempt]

/-- [RECOVER] keeps the invariants. -/
theorem invariant_recover (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config .recover = some (events, next)) : Invariant program next := by
  simp only [step] at isStep
  split at isStep
  · cases isStep
  · rename_i isDown
    simp only [Option.some.injEq, Prod.mk.injEq] at isStep
    obtain ⟨-, rfl⟩ := isStep
    have isDownFalse : config.isUp = false := by simpa using isDown
    obtain ⟨noTasks, _⟩ := invariant.downIsEmpty isDownFalse
    refine invariant_transfer_general invariant rfl rfl (GhostFrame.refl _) rfl rfl rfl
      invariant.realTaskIdsNodup (fun task isTask => ⟨task, isTask, rfl, rfl, rfl, rfl⟩)
      (fun task isTask => ⟨task, isTask, rfl, rfl, rfl, rfl⟩) ?_ ?_ ?_ (fun isUp => by cases isUp)
      invariant.lastNormalCommitAtMost invariant.historiesConsistent
      invariant.realTasksFollowHistory ?_
    · intro interrupt task _ isTask
      have isOldTask : task ∈ config.tasks := isTask
      rw [noTasks] at isOldTask
      cases isOldTask
    · intro interrupt isInterrupt
      revert isInterrupt
      show interrupt ∈ (match config.run with
        | none => []
        | some run => run.runningInterrupts) → _
      cases isRun : config.run with
      | none => intro isInterrupt; cases isInterrupt
      | some run =>
        intro isInterrupt
        obtain ⟨record, _, isMember, isStepId, hasInvocation, _⟩ :=
          mem_runningInterrupts run interrupt isInterrupt
        exact (invariant.runInvariant run isRun).runningTaskIdsBelow record interrupt.method
          interrupt.taskId isMember hasInvocation
    · intro hasNoRun
      show (match config.run with
        | none => []
        | some run => run.runningInterrupts) = []
      rw [show config.run = none from hasNoRun]
    · intro run isRun record method taskId isMember hasInvocation
      right; right
      have isMatching := (invariant.runInvariant run isRun).attemptsMatch record isMember
      unfold AttemptMatchesRecord at isMatching
      rw [hasInvocation] at isMatching
      obtain ⟨attempt, _, isAttempt, _⟩ := isMatching
      refine ⟨attempt, isAttempt, ?_⟩
      show _ ∈ (match config.run with
        | none => []
        | some run => run.runningInterrupts)
      rw [isRun]
      exact mem_runningInterrupts_of_running run record method taskId attempt isMember
        hasInvocation isAttempt

/-- Removing an interrupt that tracks no running invocation keeps the invariants. -/
theorem invariant_eraseInterrupt {program : Program σ} {config : Config σ}
    (invariant : Invariant program config) (interrupt : Interrupt σ)
    (notTracking : ∀ run, config.run = some run → ∀ record method taskId attempt,
      record ∈ run.base.steps →
      record.invocation = some { method := method, state := .running taskId } →
      run.findAttempt? record.id = some attempt →
      ({ step := record.id, method := method, attempt := attempt.number, taskId := taskId } :
        Interrupt σ) ≠ interrupt) :
    Invariant program { config with interrupts := config.interrupts.erase interrupt } := by
  refine invariant_transfer invariant rfl rfl (GhostFrame.refl _) rfl rfl rfl
    (List.Sublist.refl _) (fun task isTask => ⟨task, isTask, rfl, rfl, rfl, rfl⟩)
    (fun task isTask => ⟨task, isTask, rfl, rfl, rfl, rfl⟩)
    (fun _ isMember => List.mem_of_mem_erase isMember) ?_ invariant.lastNormalCommitAtMost
    invariant.historiesConsistent invariant.realTasksFollowHistory ?_
  · intro isDown
    obtain ⟨noTasks, noInterrupts⟩ := invariant.downIsEmpty isDown
    refine ⟨noTasks, ?_⟩
    show config.interrupts.erase interrupt = []
    rw [noInterrupts]
    rfl
  · intro run isRun record method taskId isMember hasInvocation
    rcases (invariant.runInvariant run isRun).runningIsTracked record method taskId isMember
        hasInvocation with isTask | isDown | ⟨attempt, isAttempt, isInterrupt⟩
    · exact Or.inl isTask
    · exact Or.inr (Or.inl isDown)
    · right; right
      refine ⟨attempt, isAttempt, ?_⟩
      exact (List.mem_erase_of_ne (notTracking run isRun record method taskId attempt isMember
        hasInvocation isAttempt)).mpr isInterrupt

end DexExpressiveness.DexF.WPF4
