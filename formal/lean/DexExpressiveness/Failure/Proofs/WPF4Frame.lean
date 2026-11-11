import DexExpressiveness.Failure.Proofs.WPF4Lemmas

/-!
# WP-F4: the invariants under changes that keep the run

Transfer lemmas for `RunInvariant` and `Invariant` when the run stays and
only the tasks, the interrupts, availability, stamps, or the progress part of
the ghost change.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- The outcome and the arguments of a history: what `RunInvariant` reads of it. -/
def outcomeAndArguments (history : InvocationHistory σ) :
    InvocationOutcome × Option (Arguments σ) :=
  (history.outcome, history.arguments)

/-- Two ghosts that `RunInvariant` cannot tell apart. -/
structure GhostFrame (before after : Ghost σ) : Prop where
  /-- The same keys, in order. -/
  keys : after.invocations.map (fun entry => entry.1) = before.invocations.map (fun entry => entry.1)
  /-- The same outcome and arguments for every key. -/
  find : ∀ key, (after.findInvocation? key).map outcomeAndArguments =
    (before.findInvocation? key).map outcomeAndArguments
  /-- The same `n`. -/
  normalCount : after.normalCount = before.normalCount
  /-- The same `ζ`. -/
  normalIndex : after.normalIndex = before.normalIndex

/-- A ghost frames itself. -/
theorem GhostFrame.refl (ghost : Ghost σ) : GhostFrame ghost ghost :=
  ⟨rfl, fun _ => rfl, rfl, rfl⟩

/-- A history found after a ghost frame has a history before, with the same
outcome and arguments. -/
theorem GhostFrame.find_after {before after : Ghost σ} (frame : GhostFrame before after)
    {key : InvocationKey σ} {history : InvocationHistory σ}
    (isFound : after.findInvocation? key = some history) :
    ∃ original, before.findInvocation? key = some original ∧
      original.outcome = history.outcome ∧ original.arguments = history.arguments := by
  have sameFind := frame.find key
  rw [isFound] at sameFind
  cases isOriginal : before.findInvocation? key with
  | none => rw [isOriginal] at sameFind; cases sameFind
  | some original =>
    rw [isOriginal] at sameFind
    simp only [Option.map_some, Option.some.injEq, outcomeAndArguments, Prod.mk.injEq] at sameFind
    exact ⟨original, rfl, sameFind.1.symm, sameFind.2.symm⟩

/-- A history found before a ghost frame has a history after, with the same
outcome and arguments. -/
theorem GhostFrame.find_before {before after : Ghost σ} (frame : GhostFrame before after)
    {key : InvocationKey σ} {history : InvocationHistory σ}
    (isFound : before.findInvocation? key = some history) :
    ∃ changed, after.findInvocation? key = some changed ∧
      changed.outcome = history.outcome ∧ changed.arguments = history.arguments := by
  have sameFind := frame.find key
  rw [isFound] at sameFind
  cases isChanged : after.findInvocation? key with
  | none => rw [isChanged] at sameFind; cases sameFind
  | some changed =>
    rw [isChanged] at sameFind
    simp only [Option.map_some, Option.some.injEq, outcomeAndArguments, Prod.mk.injEq] at sameFind
    exact ⟨changed, rfl, sameFind.1, sameFind.2⟩

/-- `AttemptMatchesRecord` reads only the outcome and arguments of the ghost. -/
theorem attemptMatchesRecord_transfer {run : Run σ} {before after : Ghost σ}
    (frame : GhostFrame before after) {record : Dex.StepRecord σ}
    (isMatching : AttemptMatchesRecord run before record) : AttemptMatchesRecord run after record := by
  unfold AttemptMatchesRecord at isMatching ⊢
  split at isMatching
  · exact isMatching
  · obtain ⟨attempt, history, isAttempt, isHistory, isLive, sameMethod, isPositive, isRunning,
      snapshotMatches, isBackoff⟩ := isMatching
    obtain ⟨changed, isChanged, sameOutcome, sameArguments⟩ := frame.find_before isHistory
    refine ⟨attempt, changed, isAttempt, isChanged, by rw [sameOutcome]; exact isLive, sameMethod,
      isPositive, isRunning, ?_, isBackoff⟩
    rw [sameArguments]
    exact snapshotMatches

/-- `RunInvariant` under a change of the tasks, the interrupts, availability,
and the progress part of the ghost, when the run stays. -/
theorem runInvariant_transfer {config next : Config σ} {run : Run σ}
    (invariant : RunInvariant config run) (frame : GhostFrame config.ghost next.ghost)
    (taskIdsGrow : config.nextTaskId ≤ next.nextTaskId)
    (realTasksFrom : ∀ task, task ∈ next.realTasks → ∃ original, original ∈ config.realTasks ∧
      original.base.id = task.base.id ∧ original.key = task.key ∧
      original.attemptContext.attempt = task.attemptContext.attempt)
    (stillTracked : ∀ record method taskId, record ∈ run.base.steps →
      record.invocation = some { method := method, state := .running taskId } →
      (∃ task, task ∈ next.tasks ∧ task.base.id = taskId) ∨ next.isUp = false ∨
        ∃ attempt, run.findAttempt? record.id = some attempt ∧
          ({ step := record.id, method := method, attempt := attempt.number, taskId := taskId } :
            Interrupt σ) ∈ next.interrupts) :
    RunInvariant next run where
  stepIdsNodup := invariant.stepIdsNodup
  stepNumbersCounted := invariant.stepNumbersCounted
  invocationMatchesPhase := invariant.invocationMatchesPhase
  closedRunIsQuiet := invariant.closedRunIsQuiet
  historiesHaveSteps := by
    intro entry isMember
    have isKey : entry.1 ∈ config.ghost.invocations.map (fun entry => entry.1) := by
      rw [← frame.keys]
      exact List.mem_map.mpr ⟨entry, isMember, rfl⟩
    obtain ⟨original, isOriginal, sameKey⟩ := List.mem_map.mp isKey
    rw [← sameKey]
    exact invariant.historiesHaveSteps original isOriginal
  retryTimersHaveHistories := by
    intro entry isMember
    obtain ⟨history, isHistory⟩ := invariant.retryTimersHaveHistories entry isMember
    obtain ⟨changed, isChanged, _, _⟩ := frame.find_before isHistory
    exact ⟨changed, isChanged⟩
  retryTimersNotAhead := invariant.retryTimersNotAhead
  waitTimersNodup := invariant.waitTimersNodup
  retryTimersNodup := invariant.retryTimersNodup
  attemptKeysNodup := invariant.attemptKeysNodup
  attemptsHaveInvocations := invariant.attemptsHaveInvocations
  normalIndexValues := by rw [frame.normalIndex, frame.normalCount]; exact invariant.normalIndexValues
  normalIndexKeysIncrease := by rw [frame.normalIndex]; exact invariant.normalIndexKeysIncrease
  normalIndexKeysTaken := by rw [frame.normalIndex]; exact invariant.normalIndexKeysTaken
  waitSeqsIndexed := by rw [frame.normalIndex]; exact invariant.waitSeqsIndexed
  attemptsMatch := fun record isMember =>
    attemptMatchesRecord_transfer frame (invariant.attemptsMatch record isMember)
  liveIsCurrent := by
    intro key history isFound isLive
    obtain ⟨original, isOriginal, sameOutcome, _⟩ := frame.find_after isFound
    exact invariant.liveIsCurrent key original isOriginal (by rw [sameOutcome]; exact isLive)
  runningTaskIdsBelow := by
    intro record method taskId isMember hasInvocation
    exact Nat.lt_of_lt_of_le (invariant.runningTaskIdsBelow record method taskId isMember
      hasInvocation) taskIdsGrow
  runningTaskMatches := by
    intro record method taskId task isMember hasInvocation isTask isId
    obtain ⟨original, isOriginal, sameId, sameKey, sameAttempt⟩ := realTasksFrom task isTask
    obtain ⟨isKey, attempt, isAttempt, isNumber⟩ :=
      invariant.runningTaskMatches record method taskId original isMember hasInvocation isOriginal
        (by rw [sameId]; exact isId)
    exact ⟨by rw [← sameKey]; exact isKey, attempt, isAttempt, by rw [← sameAttempt]; exact isNumber⟩
  runningIsTracked := stillTracked

/-! ## Task lists -/

/-- The real tasks after `updateRealTask`. -/
theorem realTasks_updateRealTask (config : Config σ) (taskId : Nat) (change : Task σ → Task σ) :
    (config.updateRealTask taskId change).realTasks =
      config.realTasks.map (fun task => if task.base.id = taskId then change task else task) := by
  simp [Config.updateRealTask, Config.realTasks]

/-- The real tasks after `removeRealTask`. -/
theorem realTasks_removeRealTask (config : Config σ) (taskId : Nat) :
    (config.removeRealTask taskId).realTasks =
      config.realTasks.filter (fun task => !decide (task.base.id = taskId)) := by
  simp [Config.removeRealTask, Config.realTasks, List.filter_append]

/-- `moveToZombies` permutes the real tasks. -/
theorem realTasks_moveToZombies (config : Config σ) (taskId : Nat) :
    (config.moveToZombies taskId).realTasks.Perm config.realTasks := by
  unfold Config.moveToZombies Config.realTasks
  simp only
  have split := List.filter_append_perm (fun task : Task σ => !decide (task.base.id = taskId))
    config.tasks
  have reorder : (config.tasks.filter (fun task => !decide (task.base.id = taskId)) ++
      (config.zombies ++ config.tasks.filter (fun task => decide (task.base.id = taskId)))).Perm
      (config.tasks.filter (fun task => !decide (task.base.id = taskId)) ++
        config.tasks.filter (fun task => decide (task.base.id = taskId)) ++ config.zombies) := by
    rw [List.append_assoc]
    exact List.Perm.append_left _ List.perm_append_comm
  refine reorder.trans (List.Perm.append_right _ ?_)
  have sameFilter : config.tasks.filter (fun task => decide (task.base.id = taskId)) =
      config.tasks.filter (fun task => !!decide (task.base.id = taskId)) := by simp
  rw [sameFilter]
  exact split

/-- The tasks of `Θ` after `moveToZombies` are tasks of `Θ`. -/
theorem tasks_moveToZombies (config : Config σ) (taskId : Nat) :
    (config.moveToZombies taskId).tasks =
      config.tasks.filter (fun task => !decide (task.base.id = taskId)) := rfl

/-- What `findRealTask?` finds is a real task with that ID. -/
theorem mem_of_findRealTask? {config : Config σ} {taskId : Nat} {task : Task σ}
    (isFound : config.findRealTask? taskId = some task) :
    task ∈ config.realTasks ∧ task.base.id = taskId :=
  ⟨List.mem_of_find?_eq_some isFound, by simpa using List.find?_some isFound⟩

/-- What `findLiveTask?` finds is a task of `Θ` with that ID. -/
theorem mem_of_findLiveTask? {config : Config σ} {taskId : Nat} {task : Task σ}
    (isFound : config.findLiveTask? taskId = some task) :
    task ∈ config.tasks ∧ task.base.id = taskId :=
  ⟨List.mem_of_find?_eq_some isFound, by simpa using List.find?_some isFound⟩

/-- What `findZombie?` finds is a task of `Z` with that ID. -/
theorem mem_of_findZombie? {config : Config σ} {taskId : Nat} {task : Task σ}
    (isFound : config.findZombie? taskId = some task) :
    task ∈ config.zombies ∧ task.base.id = taskId :=
  ⟨List.mem_of_find?_eq_some isFound, by simpa using List.find?_some isFound⟩

/-- With distinct real task IDs, two real tasks with one ID are equal. -/
theorem task_eq_of_id_eq {config : Config σ}
    (idsNodup : (config.realTasks.map (fun task => task.base.id)).Nodup) {first second : Task σ}
    (isFirst : first ∈ config.realTasks) (isSecond : second ∈ config.realTasks)
    (sameId : first.base.id = second.base.id) : first = second :=
  eq_of_nodup_map (fun task => task.base.id) config.realTasks idsNodup isFirst isSecond sameId

/-! ## The fence -/

/-- The fence, unfolded for a Step of the run, when Step IDs are distinct. -/
theorem isFencePassed_iff {config : Config σ} {run : Run σ} (isRun : config.run = some run)
    (idsNodup : (run.base.steps.map (fun record => record.id)).Nodup) (task : Task σ) :
    config.isFencePassed task = true ↔
      ∃ record attempt, record ∈ run.base.steps ∧ record.id = task.base.step ∧
        run.findAttempt? record.id = some attempt ∧
        record.invocation = some { method := task.base.method, state := .running task.base.id } ∧
        attempt.number = task.attemptContext.attempt := by
  unfold Config.isFencePassed
  rw [isRun]
  simp only
  constructor
  · intro isPassed
    split at isPassed
    · rename_i record attempt isRecord isAttempt
      obtain ⟨isMember, isId⟩ := mem_of_findStep? isRecord
      simp only [Bool.and_eq_true, decide_eq_true_eq] at isPassed
      exact ⟨record, attempt, isMember, isId, by rw [isId]; exact isAttempt, isPassed.1, isPassed.2⟩
    · cases isPassed
  · rintro ⟨record, attempt, isMember, isId, isAttempt, hasInvocation, isNumber⟩
    have isRecord := findStep?_of_mem idsNodup isMember
    rw [isId] at isRecord isAttempt
    rw [isRecord, isAttempt]
    simp [hasInvocation, isNumber]

/-- A real task of a running invocation passes the fence. -/
theorem isFencePassed_of_running {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    {record : Dex.StepRecord σ} {method : Dex.Method} {taskId : Nat} {task : Task σ}
    (isMember : record ∈ run.base.steps)
    (hasInvocation : record.invocation = some { method := method, state := .running taskId })
    (isTask : task ∈ config.realTasks) (isId : task.base.id = taskId) :
    config.isFencePassed task = true := by
  have runInvariant := invariant.runInvariant run isRun
  obtain ⟨isKey, attempt, isAttempt, isNumber⟩ :=
    runInvariant.runningTaskMatches record method taskId task isMember hasInvocation isTask isId
  simp only [Task.key, Prod.mk.injEq] at isKey
  rw [isFencePassed_iff isRun runInvariant.stepIdsNodup]
  refine ⟨record, attempt, isMember, isKey.1.symm, isAttempt, ?_, isNumber.symm⟩
  rw [hasInvocation, isKey.2, isId]

end DexExpressiveness.DexF.WPF4
