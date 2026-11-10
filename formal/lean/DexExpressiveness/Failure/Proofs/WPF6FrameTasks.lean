import DexExpressiveness.Failure.Observation

/-!
# WP-F6: one-step frame lemmas of DexF, clock, run, availability, and tasks

What one DexF step (`step`) can change: the clock, the run's existence,
`up`, `I`, the real tasks, `Θ`, the ghost histories, the next task ID, and
the number of `fx` events. `liveness_failure` uses them to follow a fair run
past its last failure.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ### Association lists and ghost histories -/

/-- `get?` after `set`: the set key reads the new value, other keys the old one. -/
theorem wpf6a2_get?_set {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key wanted : κ)
    (value : ν) :
    (map.set key value).get? wanted = if key = wanted then some value else map.get? wanted := by
  induction map with
  | nil => simp only [AssocList.set, AssocList.get?]
  | cons entry rest inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    by_cases isEntryKey : entryKey = key
    · subst isEntryKey
      by_cases isWanted : entryKey = wanted <;> simp [AssocList.set, AssocList.get?, isWanted]
    · by_cases isWanted : entryKey = wanted
      · subst isWanted
        simp [AssocList.set, AssocList.get?, isEntryKey, Ne.symm isEntryKey]
      · simp [AssocList.set, AssocList.get?, isEntryKey, isWanted, inductionHypothesis]

/-- A key-preserving `map` reads the changed value at a present key. -/
theorem wpf6a2_get?_map {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν)
    (change : κ × ν → κ × ν) (keepsKey : ∀ entry, (change entry).1 = entry.1) (wanted : κ)
    (value : ν) (isPresent : map.get? wanted = some value) :
    AssocList.get? (map.map change) wanted = some (change (wanted, value)).2 := by
  induction map with
  | nil => cases isPresent
  | cons entry rest inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    by_cases isWanted : entryKey = wanted
    · subst isWanted
      simp only [AssocList.get?, ite_true, Option.some.injEq] at isPresent
      subst isPresent
      simp [AssocList.get?, keepsKey]
    · simp only [AssocList.get?, isWanted, ite_false] at isPresent
      simp [AssocList.get?, keepsKey, isWanted, inductionHypothesis isPresent]

/-- Setting only absent keys keeps the value of a present key. -/
theorem wpf6a2_get?_foldl_setAbsent {κ ν : Type} [DecidableEq κ] (keys : List κ)
    (entries : AssocList κ ν) (fresh : ν) (wanted : κ) (value : ν)
    (isPresent : entries.get? wanted = some value) :
    (keys.foldl (fun (current : AssocList κ ν) key =>
      if current.contains key then current else current.set key fresh) entries).get? wanted =
      some value := by
  induction keys generalizing entries with
  | nil => exact isPresent
  | cons key laterKeys inductionHypothesis =>
    apply inductionHypothesis
    dsimp only
    split
    · exact isPresent
    · rename_i isAbsent
      rw [wpf6a2_get?_set]
      split
      · rename_i isKey
        subst isKey
        simp [AssocList.contains, isPresent] at isAbsent
      · exact isPresent

/-- `Ghost.reconcile` keeps a history: an ended outcome and the arguments stay. -/
theorem wpf6a2_findInvocation?_reconcile (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) (key : InvocationKey σ) (history : InvocationHistory σ)
    (isHistory : ghost.findInvocation? key = some history) :
    ∃ nextHistory, (ghost.reconcile completed base).findInvocation? key = some nextHistory ∧
      (history.outcome ≠ .live → nextHistory.outcome = history.outcome) ∧
      nextHistory.arguments = history.arguments := by
  unfold Ghost.reconcile Ghost.findInvocation?
  dsimp only
  generalize (List.filterMap _ base.steps : List (InvocationKey σ)) = current
  have isMarked := wpf6a2_get?_map ghost.invocations
    (fun entry => if entry.2.outcome = .live && !current.contains entry.1 then
        (entry.1, { entry.2 with
          outcome := if completed = some entry.1 then .committed else .canceled })
      else entry)
    (by intro entry; split <;> rfl) key history isHistory
  refine ⟨_, wpf6a2_get?_foldl_setAbsent current _ _ key _ isMarked, ?_, ?_⟩
  · intro isEnded
    have isNotLive : (history.outcome = .live) = False := propext ⟨isEnded, False.elim⟩
    simp [isNotLive]
  · split <;> rfl


/-- An `updateInvocation` changes only its own key, and only a present one. -/
theorem wpf6a2_findInvocation?_updateInvocation (ghost : Ghost σ) (changedKey key : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ) (history : InvocationHistory σ)
    (isHistory : ghost.findInvocation? key = some history) :
    (ghost.updateInvocation changedKey change).findInvocation? key =
      some (if changedKey = key then change history else history) := by
  unfold Ghost.updateInvocation
  split
  · rename_i isAbsent
    split
    · rename_i isKey
      subst isKey
      rw [isAbsent] at isHistory
      cases isHistory
    · exact isHistory
  · rename_i changedHistory isChanged
    simp only [Ghost.findInvocation?] at isChanged isHistory ⊢
    rw [wpf6a2_get?_set]
    split
    · rename_i isKey
      subst isKey
      rw [isChanged] at isHistory
      cases isHistory
      rfl
    · exact isHistory

/-- A commit keeps a history: an ended outcome and the arguments stay. -/
theorem wpf6a2_findInvocation?_commitRun_of_eq (config : Config σ) (run : Run σ) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) (committed : List (Obs σ) × Config σ)
    (isCommitted : config.commitRun run isNormal completed inputEvents change = committed)
    (key : InvocationKey σ) (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? key = some history) :
    ∃ nextHistory, committed.2.ghost.findInvocation? key = some nextHistory ∧
      (history.outcome ≠ .live → nextHistory.outcome = history.outcome) ∧
      nextHistory.arguments = history.arguments := by
  subst isCommitted
  exact wpf6a2_findInvocation?_reconcile _ completed _ key history
    (by cases isNormal <;> exact isHistory)

/-- `wpf6a2_findInvocation?_commitRun_of_eq`, read on the commit itself. -/
theorem wpf6a2_findInvocation?_commitRun (config : Config σ) (run : Run σ) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) (key : InvocationKey σ) (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? key = some history) :
    ∃ nextHistory,
      (config.commitRun run isNormal completed inputEvents change).2.ghost.findInvocation? key =
        some nextHistory ∧
      (history.outcome ≠ .live → nextHistory.outcome = history.outcome) ∧
      nextHistory.arguments = history.arguments :=
  wpf6a2_findInvocation?_commitRun_of_eq config run isNormal completed inputEvents change _ rfl key
    history isHistory

/-- A failed attempt keeps a history: an ended outcome and the arguments stay. -/
theorem wpf6a2_findInvocation?_failAttempt (config : Config σ) (run : Run σ)
    (step : Dex.StepExecId σ) (method : Dex.Method) (attempt : Attempt σ) (cause : FailureCause)
    (lostTaskId : Option Nat) (decision : RetryDecision) (key : InvocationKey σ)
    (history : InvocationHistory σ) (isHistory : config.ghost.findInvocation? key = some history) :
    ∃ nextHistory,
      (config.failAttempt run step method attempt cause lostTaskId decision).2.ghost.findInvocation?
        key = some nextHistory ∧
      (history.outcome ≠ .live → nextHistory.outcome = history.outcome) ∧
      nextHistory.arguments = history.arguments := by
  cases lostTaskId <;> cases decision <;>
    exact wpf6a2_findInvocation?_commitRun _ run false none [] _ key history isHistory

/-- A step's result, taken apart. -/
theorem wpf6a2_some_inj {result : List (Obs σ) × Config σ} {events : List (Obs σ)}
    {next : Config σ} (isSome : some result = some (events, next)) :
    events = result.1 ∧ next = result.2 := by
  cases isSome
  exact ⟨rfl, rfl⟩

/-- A failed attempt keeps the clock, `up`, `I`, the next task ID, and the run. -/
theorem wpf6a2_failAttempt_frame (config : Config σ) (run : Run σ) (step : Dex.StepExecId σ)
    (method : Dex.Method) (attempt : Attempt σ) (cause : FailureCause)
    (lostTaskId : Option Nat) (decision : RetryDecision) :
    (config.failAttempt run step method attempt cause lostTaskId decision).2.clock = config.clock ∧
    (config.failAttempt run step method attempt cause lostTaskId decision).2.isUp = config.isUp ∧
    (config.failAttempt run step method attempt cause lostTaskId decision).2.interrupts =
      config.interrupts ∧
    (config.failAttempt run step method attempt cause lostTaskId decision).2.nextTaskId =
      config.nextTaskId ∧
    (config.failAttempt run step method attempt cause lostTaskId decision).2.run.isSome = true := by
  cases lostTaskId <;> cases decision <;> exact ⟨rfl, rfl, rfl, rfl, rfl⟩

/-- A `closed` event is not an `fx` event. -/
theorem wpf6a2_closeEvents_no_fx (before after : Dex.Run σ) (stepId : Dex.StepExecId σ)
    (method : Dex.Method) (effect : σ.Fx) (index : Nat) :
    Obs.fx stepId method effect index ∉ Config.closeEvents before after := by
  unfold Config.closeEvents
  split <;> simp

/-- A failed attempt emits no `fx` event. -/
theorem wpf6a2_failAttempt_no_fx (config : Config σ) (run : Run σ) (step : Dex.StepExecId σ)
    (method : Dex.Method) (attempt : Attempt σ) (cause : FailureCause)
    (lostTaskId : Option Nat) (decision : RetryDecision) (stepId : Dex.StepExecId σ)
    (fxMethod : Dex.Method) (effect : σ.Fx) (index : Nat) :
    Obs.fx stepId fxMethod effect index ∉
      (config.failAttempt run step method attempt cause lostTaskId decision).1 := by
  cases lostTaskId <;> cases decision <;>
    simp [Config.failAttempt, Config.commitRun, wpf6a2_closeEvents_no_fx]

/-! ### Real tasks -/

/-- Two real tasks with the same ID are the same task. -/
theorem wpf6a2_eq_of_same_id (tasks : List (Task σ))
    (idsNodup : (tasks.map (fun task => task.base.id)).Nodup) {first second : Task σ}
    (isFirstMember : first ∈ tasks) (isSecondMember : second ∈ tasks)
    (sameId : first.base.id = second.base.id) : first = second := by
  induction tasks with
  | nil => cases isFirstMember
  | cons head rest inductionHypothesis =>
    simp only [List.map_cons, List.nodup_cons, List.mem_map, not_exists, not_and] at idsNodup
    rcases List.mem_cons.mp isFirstMember with isHead | isInRest <;>
      rcases List.mem_cons.mp isSecondMember with isSecondHead | isSecondInRest
    · rw [isHead, isSecondHead]
    · subst isHead
      exact absurd sameId.symm (idsNodup.1 second isSecondInRest)
    · subst isSecondHead
      exact absurd sameId (idsNodup.1 first isInRest)
    · exact inductionHypothesis idsNodup.2 isInRest isSecondInRest

/-- With distinct IDs, finding a member's ID finds the member. -/
theorem wpf6a2_find?_of_mem (tasks : List (Task σ))
    (idsNodup : (tasks.map (fun task => task.base.id)).Nodup) (task : Task σ)
    (isMember : task ∈ tasks) :
    tasks.find? (fun candidate => decide (candidate.base.id = task.base.id)) = some task := by
  cases isFound : tasks.find? (fun candidate => decide (candidate.base.id = task.base.id)) with
  | none =>
    rw [List.find?_eq_none] at isFound
    exact absurd (decide_eq_true rfl) (isFound task isMember)
  | some found =>
    have isFoundMember := List.mem_of_find?_eq_some isFound
    have isSameId : found.base.id = task.base.id :=
      of_decide_eq_true (List.find?_some (p := fun (candidate : Task σ) => decide (candidate.base.id = task.base.id))
        isFound)
    rw [wpf6a2_eq_of_same_id tasks idsNodup isFoundMember isMember isSameId]

/-- A real task found by its ID is a real task with that ID. -/
theorem wpf6a2_findRealTask?_spec (config : Config σ) (taskId : Nat) (task : Task σ)
    (isFound : config.findRealTask? taskId = some task) :
    task ∈ config.realTasks ∧ task.base.id = taskId := by
  have isFoundInList :
      config.realTasks.find? (fun candidate => decide (candidate.base.id = taskId)) = some task :=
    isFound
  exact ⟨List.mem_of_find?_eq_some isFoundInList,
    of_decide_eq_true (List.find?_some (p := fun (candidate : Task σ) => decide (candidate.base.id = taskId))
      isFoundInList)⟩

/-- Removing a task by its ID keeps the other real tasks. -/
theorem wpf6a2_mem_removeRealTask (config : Config σ) (taskId : Nat) (task : Task σ) :
    task ∈ (config.removeRealTask taskId).realTasks ↔
      task ∈ config.realTasks ∧ ¬task.base.id = taskId := by
  simp only [Config.removeRealTask, Config.realTasks, List.mem_append, List.mem_filter,
    Bool.not_eq_true', decide_eq_false_iff_not]
  constructor
  · rintro (⟨isMember, isOther⟩ | ⟨isMember, isOther⟩)
    · exact ⟨Or.inl isMember, isOther⟩
    · exact ⟨Or.inr isMember, isOther⟩
  · rintro ⟨isMember | isMember, isOther⟩
    · exact Or.inl ⟨isMember, isOther⟩
    · exact Or.inr ⟨isMember, isOther⟩

/-- Moving a task to `Z` keeps the real tasks. -/
theorem wpf6a2_mem_moveToZombies (config : Config σ) (taskId : Nat) (task : Task σ) :
    task ∈ (config.moveToZombies taskId).realTasks ↔ task ∈ config.realTasks := by
  simp only [Config.moveToZombies, Config.realTasks, List.mem_append, List.mem_filter,
    Bool.not_eq_true', decide_eq_false_iff_not, decide_eq_true_eq]
  constructor
  · rintro (⟨isMember, _⟩ | isMember | ⟨isMember, _⟩)
    · exact Or.inl isMember
    · exact Or.inr isMember
    · exact Or.inl isMember
  · rintro (isMember | isMember)
    · by_cases isSame : task.base.id = taskId
      · exact Or.inr (Or.inr ⟨isMember, isSame⟩)
      · exact Or.inl ⟨isMember, isSame⟩
    · exact Or.inr (Or.inl isMember)

/-- A failed attempt keeps the real tasks. -/
theorem wpf6a2_mem_failAttempt (config : Config σ) (run : Run σ) (step : Dex.StepExecId σ)
    (method : Dex.Method) (attempt : Attempt σ) (cause : FailureCause)
    (lostTaskId : Option Nat) (decision : RetryDecision) (task : Task σ) :
    task ∈ (config.failAttempt run step method attempt cause lostTaskId decision).2.realTasks ↔
      task ∈ config.realTasks := by
  cases lostTaskId <;> cases decision
  all_goals first
    | exact Iff.rfl
    | exact wpf6a2_mem_moveToZombies config _ task

/-- A commit keeps the real tasks. -/
theorem wpf6a2_realTasks_commitRun (config : Config σ) (run : Run σ) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) :
    (config.commitRun run isNormal completed inputEvents change).2.realTasks = config.realTasks :=
  rfl

/-- A stamp keeps the real tasks. -/
theorem wpf6a2_realTasks_stampOnly (config : Config σ) :
    config.stampOnly.realTasks = config.realTasks :=
  rfl

/-- A ghost change keeps the real tasks. -/
theorem wpf6a2_realTasks_ghost (config : Config σ) (ghost : Ghost σ) :
    (Config.mk config.clock config.lastCommitAt config.run config.tasks config.zombies
      config.nextTaskId config.interrupts config.isUp ghost).realTasks = config.realTasks :=
  rfl

/-- A change of `up` and `I` keeps the real tasks. -/
theorem wpf6a2_realTasks_recover (config : Config σ) (interrupts : List (Interrupt σ))
    (isUp : Bool) :
    (Config.mk config.clock config.lastCommitAt config.run config.tasks config.zombies
      config.nextTaskId interrupts isUp config.ghost).realTasks = config.realTasks :=
  rfl

/-- [CRASH] keeps the real tasks. -/
theorem wpf6a2_mem_crash (config : Config σ) (task : Task σ) :
    task ∈ (Config.mk config.clock config.lastCommitAt config.run [] (config.zombies ++ config.tasks)
      config.nextTaskId [] false config.ghost).realTasks ↔ task ∈ config.realTasks := by
  simp only [Config.realTasks, List.nil_append, List.mem_append]
  exact Or.comm

/-- Updating a found task that is not `Returned` keeps each real task's ID, key,
and `Returned` phase. -/
theorem wpf6a2_realTask_updateRealTask (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (task : Task σ)
    (isFound : config.findRealTask? taskId = some task)
    (isNotReturned : task.base.phase = .started ∨
      ∃ effects response, task.base.phase = .running effects response)
    (change : Task σ → Task σ)
    (keepsTask : ∀ current, (change current).base.id = current.base.id ∧
      (change current).key = current.key)
    (nextTask : Task σ) (isMember : nextTask ∈ (config.updateRealTask taskId change).realTasks) :
    ∃ previous, previous ∈ config.realTasks ∧ previous.base.id = nextTask.base.id ∧
      previous.key = nextTask.key ∧
      ∀ response, previous.base.phase = .returned response →
        nextTask.base.phase = .returned response := by
  have isMapped : nextTask ∈ config.realTasks.map
      (fun current => if current.base.id = taskId then change current else current) := by
    simpa only [Config.updateRealTask, Config.realTasks, List.map_append] using isMember
  obtain ⟨previous, isPrevious, isNext⟩ := List.mem_map.mp isMapped
  refine ⟨previous, isPrevious, ?_, ?_, ?_⟩
  · rw [← isNext]
    split
    · exact (keepsTask previous).1.symm
    · rfl
  · rw [← isNext]
    split
    · exact (keepsTask previous).2.symm
    · rfl
  · intro response isReturned
    split at isNext
    · rename_i isSame
      obtain ⟨isTaskMember, isTaskId⟩ := wpf6a2_findRealTask?_spec config taskId task isFound
      have isTask := wpf6a2_eq_of_same_id config.realTasks invariant.realTaskIdsNodup isPrevious
        isTaskMember (isSame.trans isTaskId.symm)
      subst isTask
      rcases isNotReturned with isStarted | ⟨effects, runningResponse, isRunning⟩
      · rw [isStarted] at isReturned
        cases isReturned
      · rw [isRunning] at isReturned
        cases isReturned
    · rw [← isNext]
      exact isReturned

/-! ### Live tasks -/

/-- Removing another task keeps a task of `Θ`. -/
theorem wpf6a2_tasks_removeRealTask (config : Config σ) (taskId : Nat) (task : Task σ)
    (isMember : task ∈ config.tasks) (isOther : task.base.id ≠ taskId) :
    task ∈ (config.removeRealTask taskId).tasks := by
  simp only [Config.removeRealTask, List.mem_filter]
  exact ⟨isMember, by simp [isOther]⟩

/-- `updateRealTask` keeps a task of `Θ`, with its ID and invocation. -/
theorem wpf6a2_tasks_updateRealTask (config : Config σ) (taskId : Nat) (change : Task σ → Task σ)
    (keepsTask : ∀ current, (change current).base.id = current.base.id ∧
      (change current).key = current.key)
    (task : Task σ) (isMember : task ∈ config.tasks) :
    ∃ nextTask, nextTask ∈ (config.updateRealTask taskId change).tasks ∧
      nextTask.base.id = task.base.id ∧ nextTask.key = task.key := by
  refine ⟨if task.base.id = taskId then change task else task, ?_, ?_, ?_⟩
  · exact List.mem_map.mpr ⟨task, isMember, rfl⟩
  · split
    · exact (keepsTask task).1
    · rfl
  · split
    · exact (keepsTask task).2
    · rfl

/-- A commit keeps a history, in the form of `wpf6_history_step`. -/
theorem wpf6a2_history_commitRun (config : Config σ) (run : Run σ) (isNormal : Bool)
    (completed : Option (InvocationKey σ)) (inputEvents : List (Obs σ))
    (change : Run σ → Run σ) (key : InvocationKey σ) (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? key = some history) :
    ∃ nextHistory,
      (config.commitRun run isNormal completed inputEvents change).2.ghost.findInvocation? key =
        some nextHistory ∧
      (history.outcome ≠ .live → nextHistory.outcome = history.outcome) ∧
      (∀ arguments, history.arguments = some arguments →
        nextHistory.arguments = some arguments) := by
  obtain ⟨nextHistory, isNextHistory, keepsOutcome, keepsArguments⟩ :=
    wpf6a2_findInvocation?_commitRun config run isNormal completed inputEvents change key history
      isHistory
  exact ⟨nextHistory, isNextHistory, keepsOutcome,
    fun _ hasArguments => keepsArguments.trans hasArguments⟩

/-- A failed attempt keeps a history, in the form of `wpf6_history_step`. -/
theorem wpf6a2_history_failAttempt (config : Config σ) (run : Run σ)
    (step : Dex.StepExecId σ) (method : Dex.Method) (attempt : Attempt σ) (cause : FailureCause)
    (lostTaskId : Option Nat) (decision : RetryDecision) (key : InvocationKey σ)
    (history : InvocationHistory σ) (isHistory : config.ghost.findInvocation? key = some history) :
    ∃ nextHistory,
      (config.failAttempt run step method attempt cause lostTaskId decision).2.ghost.findInvocation?
        key = some nextHistory ∧
      (history.outcome ≠ .live → nextHistory.outcome = history.outcome) ∧
      (∀ arguments, history.arguments = some arguments →
        nextHistory.arguments = some arguments) := by
  obtain ⟨nextHistory, isNextHistory, keepsOutcome, keepsArguments⟩ :=
    wpf6a2_findInvocation?_failAttempt config run step method attempt cause lostTaskId decision key
      history isHistory
  exact ⟨nextHistory, isNextHistory, keepsOutcome,
    fun _ hasArguments => keepsArguments.trans hasArguments⟩

/-- A Worker's ghost change keeps a history, in the form of `wpf6_history_step`. -/
theorem wpf6a2_history_updateInvocation (ghost : Ghost σ) (changedKey : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ)
    (keepsHistory : ∀ current, (change current).outcome = current.outcome ∧
      (change current).arguments = current.arguments)
    (key : InvocationKey σ) (history : InvocationHistory σ)
    (isHistory : ghost.findInvocation? key = some history) :
    ∃ nextHistory, (ghost.updateInvocation changedKey change).findInvocation? key =
        some nextHistory ∧
      (history.outcome ≠ .live → nextHistory.outcome = history.outcome) ∧
      (∀ arguments, history.arguments = some arguments →
        nextHistory.arguments = some arguments) := by
  refine ⟨_, wpf6a2_findInvocation?_updateInvocation ghost changedKey key change history isHistory,
    ?_, ?_⟩
  · intro _
    split
    · exact (keepsHistory history).1
    · rfl
  · intro _ hasArguments
    split
    · exact (keepsHistory history).2.trans hasArguments
    · exact hasArguments

/-- The real tasks after [STEP-START]: the old ones and the minted task, of a
live invocation. -/
theorem wpf6a2_realTask_stepStart (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (stepId : Dex.StepExecId σ) (isSnapshotReused : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.stepStart stepId isSnapshotReused) = some (events, next))
    (nextTask : Task σ) (isMember : nextTask ∈ next.realTasks) :
    (∃ task, task ∈ config.realTasks ∧ task.base.id = nextTask.base.id ∧
      task.key = nextTask.key ∧
      ∀ response, task.base.phase = .returned response →
        nextTask.base.phase = .returned response) ∨
    (nextTask.base.id = config.nextTaskId ∧
      ∃ history, config.ghost.findInvocation? nextTask.key = some history ∧
        history.outcome = .live) := by
  simp only [step] at isStep
  split at isStep
  · split at isStep
    · cases isStep
    · rename_i run isRun
      split at isStep
      · rename_i record attempt isRecord isAttempt
        split at isStep
        · rename_i method isInvocation
          split at isStep
          · cases isStep
          · generalize isCommitted : Config.commitRun config run _ none [] _ = committed at isStep
            obtain ⟨rfl, rfl⟩ := wpf6a2_some_inj isStep
            have isZombies : committed.2.zombies = config.zombies := by
              rw [← isCommitted]
              rfl
            simp only [Config.realTasks, List.mem_append, List.mem_singleton, isZombies] at isMember
            rcases isMember with (isOld | isNew) | isZombie
            · exact Or.inl ⟨nextTask, List.mem_append_left _ isOld, rfl, rfl,
                fun _ isReturned => isReturned⟩
            · right
              subst isNew
              refine ⟨rfl, ?_⟩
              have isRecordMember : record ∈ run.base.steps := List.mem_of_find?_eq_some isRecord
              have isRecordId : record.id = stepId :=
                of_decide_eq_true (List.find?_some
                  (p := fun (candidate : Dex.StepRecord σ) => decide (candidate.id = stepId)) isRecord)
              have isMatching := (invariant.runInvariant run isRun).attemptsMatch record isRecordMember
              unfold AttemptMatchesRecord at isMatching
              rw [isInvocation] at isMatching
              obtain ⟨_, history, _, isHistory, isLive, _⟩ := isMatching
              rw [isRecordId] at isHistory
              exact ⟨history, isHistory, isLive⟩
            · exact Or.inl ⟨nextTask, List.mem_append_right _ isZombie, rfl, rfl,
                fun _ isReturned => isReturned⟩
        · cases isStep
      · cases isStep
  · cases isStep

/-- [STEP-START] keeps a history; the arguments it sets were unset. -/
theorem wpf6a2_history_stepStart (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (stepId : Dex.StepExecId σ) (isSnapshotReused : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.stepStart stepId isSnapshotReused) = some (events, next))
    (key : InvocationKey σ) (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? key = some history) :
    ∃ nextHistory, next.ghost.findInvocation? key = some nextHistory ∧
      (history.outcome ≠ .live → nextHistory.outcome = history.outcome) ∧
      (∀ arguments, history.arguments = some arguments →
        nextHistory.arguments = some arguments) := by
  simp only [step] at isStep
  split at isStep
  · split at isStep
    · cases isStep
    · rename_i run isRun
      split at isStep
      · rename_i record attempt isRecord isAttempt
        split at isStep
        · rename_i method isInvocation
          split at isStep
          · cases isStep
          · rename_i isGuard
            generalize isCommitted : Config.commitRun config run _ none [] _ = committed at isStep
            obtain ⟨rfl, rfl⟩ := wpf6a2_some_inj isStep
            obtain ⟨committedHistory, isCommittedHistory, keepsOutcome, keepsArguments⟩ :=
              wpf6a2_findInvocation?_commitRun_of_eq config run _ none [] _ committed isCommitted
                key history isHistory
            cases isSnapshotReused with
            | true =>
              exact ⟨committedHistory, isCommittedHistory, keepsOutcome,
                fun _ hasArguments => keepsArguments.trans hasArguments⟩
            | false =>
              refine ⟨_, wpf6a2_findInvocation?_updateInvocation committed.2.ghost (stepId, method)
                key _ committedHistory isCommittedHistory, ?_, ?_⟩
              · intro isEnded
                split
                · exact keepsOutcome isEnded
                · exact keepsOutcome isEnded
              · intro arguments hasArguments
                split
                · rename_i isKey
                  subst isKey
                  have isSnapshotNone : attempt.snapshot = none := by
                    cases isSnapshot : attempt.snapshot with
                    | none => rfl
                    | some _ =>
                      rw [isSnapshot] at isGuard
                      simp at isGuard
                  have isRecordMember : record ∈ run.base.steps :=
                    List.mem_of_find?_eq_some isRecord
                  have isRecordId : record.id = stepId :=
                    of_decide_eq_true (List.find?_some
                      (p := fun (candidate : Dex.StepRecord σ) => decide (candidate.id = stepId))
                      isRecord)
                  have isMatching :=
                    (invariant.runInvariant run isRun).attemptsMatch record isRecordMember
                  unfold AttemptMatchesRecord at isMatching
                  rw [isInvocation] at isMatching
                  obtain ⟨matchedAttempt, matchedHistory, isMatchedAttempt, isMatchedHistory, _, _, _, _,
                    isSnapshotMatching, _⟩ := isMatching
                  rw [isRecordId] at isMatchedAttempt isMatchedHistory
                  rw [isAttempt] at isMatchedAttempt
                  cases isMatchedAttempt
                  rw [isHistory] at isMatchedHistory
                  cases isMatchedHistory
                  rw [isSnapshotNone, hasArguments] at isSnapshotMatching
                  exact isSnapshotMatching.elim
                · exact keepsArguments.trans hasArguments
        · cases isStep
      · cases isStep
  · cases isStep

/-- Split a DexF step of a known rule down to its result. -/
macro "wpf6a2_split_step " isStep:ident : tactic =>
  `(tactic| ((try cases ‹Request _›) <;> simp only [step] at $isStep:ident <;>
    (repeat' split at $isStep:ident) <;> (try (cases $isStep:ident; done)) <;>
    (obtain ⟨isEvents, isNext⟩ := wpf6a2_some_inj $isStep:ident; subst isEvents; subst isNext)))

/-- Only a tick moves the clock, and it moves it forward. -/
theorem wpf6_clock_step (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    config.clock ≤ next.clock ∧
      ((∀ duration, action ≠ .tick duration) → next.clock = config.clock) := by
  cases action <;> wpf6a2_split_step isStep
  all_goals simp [Config.commitRun, Config.stampOnly, Config.removeRealTask,
    Config.updateRealTask, Config.moveToZombies, wpf6a2_failAttempt_frame]

/-- A started run stays started; only [FLOW-START] starts it. -/
theorem wpf6_run_step (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    (config.run.isSome = true → next.run.isSome = true) ∧
    (config.run = none → next.run = none ∨
      ∃ requestId stepType input, action = .request (.startFlow requestId stepType input)) := by
  cases action <;> wpf6a2_split_step isStep
  all_goals simp_all [Config.commitRun, Config.stampOnly, Config.removeRealTask,
    Config.updateRealTask, Config.moveToZombies, wpf6a2_failAttempt_frame]

/-- Only [CRASH] and [RECOVER] change `up`; [RECOVER] needs the server down. -/
theorem wpf6_isUp_step (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    (action ≠ .crash → action ≠ .recover → next.isUp = config.isUp) ∧
    (action = .recover → config.isUp = false ∧ next.isUp = true) := by
  cases action <;> wpf6a2_split_step isStep
  all_goals simp_all [Config.commitRun, Config.stampOnly, Config.removeRealTask,
    Config.updateRealTask, Config.moveToZombies, wpf6a2_failAttempt_frame]

/-- Only [RECOVER] adds interrupts. -/
theorem wpf6_interrupts_step (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next))
    (isNotRecover : action ≠ .recover) :
    ∀ interrupt, interrupt ∈ next.interrupts → interrupt ∈ config.interrupts := by
  cases action <;> wpf6a2_split_step isStep
  all_goals simp_all [Config.commitRun, Config.stampOnly, Config.removeRealTask,
    Config.updateRealTask, Config.moveToZombies, wpf6a2_failAttempt_frame]
  all_goals exact fun _ isMember => List.mem_of_mem_erase isMember

/-- The next task ID never decreases. -/
theorem wpf6_nextTaskId_step (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    config.nextTaskId ≤ next.nextTaskId := by
  cases action <;> wpf6a2_split_step isStep
  all_goals simp [Config.commitRun, Config.stampOnly, Config.removeRealTask,
    Config.updateRealTask, Config.moveToZombies, wpf6a2_failAttempt_frame]

/-- A real task after a step was a real task before, with the same ID and
invocation, and a `Returned` one keeps its phase; or it is the task that
[STEP-START] just minted, of a live invocation. -/
theorem wpf6_realTask_step (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config action = some (events, next))
    (nextTask : Task σ) (isMember : nextTask ∈ next.realTasks) :
    (∃ task, task ∈ config.realTasks ∧ task.base.id = nextTask.base.id ∧
      task.key = nextTask.key ∧
      ∀ response, task.base.phase = .returned response →
        nextTask.base.phase = .returned response) ∨
    (nextTask.base.id = config.nextTaskId ∧
      ∃ history, config.ghost.findInvocation? nextTask.key = some history ∧
        history.outcome = .live) := by
  cases action
  case stepStart stepId isSnapshotReused =>
    exact wpf6a2_realTask_stepStart program config invariant stepId isSnapshotReused events next
      isStep nextTask isMember
  all_goals wpf6a2_split_step isStep
  all_goals try simp only [wpf6a2_realTasks_commitRun, wpf6a2_realTasks_stampOnly,
    wpf6a2_realTasks_ghost, wpf6a2_realTasks_recover, wpf6a2_mem_crash, wpf6a2_mem_failAttempt,
    wpf6a2_mem_removeRealTask, wpf6a2_mem_moveToZombies] at isMember
  all_goals first
    | exact Or.inl ⟨nextTask, isMember, rfl, rfl, fun _ isReturned => isReturned⟩
    | exact Or.inl ⟨nextTask, isMember.1, rfl, rfl, fun _ isReturned => isReturned⟩
    | exact Or.inl (wpf6a2_realTask_updateRealTask program config invariant _ _ ‹_› (Or.inl ‹_›)
        _ (by intro _; exact ⟨rfl, rfl⟩) nextTask isMember)
    | exact Or.inl (wpf6a2_realTask_updateRealTask program config invariant _ _ ‹_›
        (Or.inr ⟨_, _, ‹_›⟩) _ (by intro _; exact ⟨rfl, rfl⟩) nextTask isMember)

/-- A task of `Θ` stays in `Θ`, with its invocation, unless the step removes
it by its ID or is a failure. -/
theorem wpf6_liveTask_step (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config action = some (events, next))
    (task : Task σ) (isMember : task ∈ config.tasks)
    (isNotComplete : action ≠ .stepComplete task.base.id)
    (isNotAbandon : ∀ isLastOfCanceled, action ≠ .workerAbandon task.base.id isLastOfCanceled)
    (isNotStale : ∀ isLastOfCanceled, action ≠ .resultStale task.base.id isLastOfCanceled)
    (isNotFailure : Fairness.Action.isCountedFailure action = false)
    (isNotInterrupt : ∀ interrupt decision, action ≠ .recoverInterrupt interrupt decision) :
    ∃ nextTask, nextTask ∈ next.tasks ∧ nextTask.base.id = task.base.id ∧
      nextTask.key = task.key := by
  cases action
  case crash => cases isNotFailure
  case attemptFailed => cases isNotFailure
  case attemptReturned => cases isNotFailure
  case recoverInterrupt interrupt decision => exact absurd rfl (isNotInterrupt interrupt decision)
  all_goals wpf6a2_split_step isStep
  all_goals first
    | exact ⟨task, isMember, rfl, rfl⟩
    | exact ⟨task, List.mem_append_left _ isMember, rfl, rfl⟩
    | exact wpf6a2_tasks_updateRealTask config _ _ (by intro _; exact ⟨rfl, rfl⟩) task isMember
    | exact ⟨task, wpf6a2_tasks_removeRealTask config _ task isMember (fun isSame => by
        subst isSame
        first
          | exact isNotAbandon _ rfl
          | exact isNotStale _ rfl
          | exact isNotComplete rfl), rfl, rfl⟩

/-- Real task IDs of `Θ` are distinct. -/
theorem wpf6a2_liveTaskIdsNodup (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) :
    (config.tasks.map (fun task => task.base.id)).Nodup := by
  have idsNodup := invariant.realTaskIdsNodup
  simp only [Config.realTasks, List.map_append] at idsNodup
  exact (List.nodup_append.mp idsNodup).1

/-- A task of `Θ` that passes the fence cannot be abandoned. -/
theorem wpf6_workerAbandon_disabled (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (task : Task σ) (isMember : task ∈ config.tasks)
    (isPassed : config.isFencePassed task = true) (isLastOfCanceled : Bool) :
    step program config (.workerAbandon task.base.id isLastOfCanceled) = none := by
  have isFound : config.findLiveTask? task.base.id = some task :=
    wpf6a2_find?_of_mem config.tasks (wpf6a2_liveTaskIdsNodup program config invariant) task
      isMember
  simp [step, isFound, isPassed]

/-- The result of a task of `Θ` that passes the fence is not stale. -/
theorem wpf6_resultStale_disabled (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (task : Task σ) (isMember : task ∈ config.tasks)
    (isPassed : config.isFencePassed task = true) (isLastOfCanceled : Bool) :
    step program config (.resultStale task.base.id isLastOfCanceled) = none := by
  have isFound : config.findRealTask? task.base.id = some task :=
    wpf6a2_find?_of_mem config.realTasks invariant.realTaskIdsNodup task
      (List.mem_append_left _ isMember)
  simp only [step]
  split
  · rw [isFound]
    dsimp only
    split <;> simp [isPassed]
  · rfl

/-- A ghost history stays: an ended invocation keeps its outcome, and `par`
once set never changes. -/
theorem wpf6_history_step (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config action = some (events, next))
    (key : InvocationKey σ) (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? key = some history) :
    ∃ nextHistory, next.ghost.findInvocation? key = some nextHistory ∧
      (history.outcome ≠ .live → nextHistory.outcome = history.outcome) ∧
      (∀ arguments, history.arguments = some arguments →
        nextHistory.arguments = some arguments) := by
  cases action
  case stepStart stepId isSnapshotReused =>
    exact wpf6a2_history_stepStart program config invariant stepId isSnapshotReused events next
      isStep key history isHistory
  all_goals wpf6a2_split_step isStep
  all_goals first
    | exact ⟨history, isHistory, fun _ => rfl, fun _ hasArguments => hasArguments⟩
    | exact wpf6a2_history_commitRun _ _ _ _ _ _ key history isHistory
    | exact wpf6a2_history_failAttempt _ _ _ _ _ _ _ _ key history isHistory
    | exact wpf6a2_history_updateInvocation _ _ _ (by intro _; exact ⟨rfl, rfl⟩) key history
        isHistory

/-- A step emits at most one `fx` event: either at most one event, or no `fx`. -/
theorem wpf6_events_step (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    events.length ≤ 1 ∨
      ∀ stepId method effect index, Obs.fx stepId method effect index ∉ events := by
  cases action <;> wpf6a2_split_step isStep
  all_goals first
    | (left; simp; done)
    | (right
       intro stepId method effect index
       simp [Config.commitRun, wpf6a2_closeEvents_no_fx, wpf6a2_failAttempt_no_fx])

end DexExpressiveness.DexF
