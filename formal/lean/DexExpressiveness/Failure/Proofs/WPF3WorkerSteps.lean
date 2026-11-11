import DexExpressiveness.Failure.Proofs.WPF3Abstraction

/-!
# WP-F3: Worker steps are simulated

`workerStep_simulated_proof`: a Worker step of a real task, in `Θ` or `Z`,
maps to the same Worker step of its invocation's virtual task when it is a
first occurrence, and to a stutter otherwise; the drop or stale result of the
last real task of a canceled invocation maps to [WORKER-ABANDON] or
[STEP-COMPLETE-STALE] of the virtual task (spec §11.6). Everything here is
proved.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## Facts about a real task -/

/-- What the invariant says about a real task found by ID. -/
theorem wpf3_realTask_facts (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (task : Task σ)
    (isFound : config.findRealTask? taskId = some task) :
    ∃ history arguments,
      task ∈ config.realTasks ∧ task.base.id = taskId ∧
      config.ghost.findInvocation? task.key = some history ∧
      history.arguments = some arguments ∧
      TaskFollowsHistory program.failureFreeFlow task history ∧
      HistoryIsConsistent program.failureFreeFlow task.key history := by
  obtain ⟨isMember, isId⟩ := wpf3_mem_of_findRealTask? config taskId task isFound
  obtain ⟨history, isHistory, follows⟩ := invariant.realTasksFollowHistory task isMember
  obtain ⟨arguments, hasArguments, _⟩ := id follows
  exact ⟨history, arguments, isMember, isId, isHistory, hasArguments, follows,
    invariant.historiesConsistent _ (wpf3_mem_of_get? _ _ _ isHistory)⟩

/-- α keeps a virtual task for the invocation of a real task that did not commit. -/
theorem wpf3_hasVirtualTask_of_realTask (config : Config σ) (task : Task σ)
    (isMember : task ∈ config.realTasks) (history : InvocationHistory σ) (arguments : Arguments σ)
    (hasArguments : history.arguments = some arguments)
    (isNotCommitted : history.outcome ≠ .committed) :
    config.hasVirtualTask task.key history = true := by
  unfold Config.hasVirtualTask
  rw [hasArguments]
  cases isOutcome : history.outcome with
  | live => rfl
  | committed => exact absurd isOutcome isNotCommitted
  | canceled =>
    simp only [Option.isSome_some, Bool.true_and, List.any_eq_true, decide_eq_true_eq]
    exact ⟨task, isMember, rfl⟩

/-- A committed invocation has returned. -/
theorem wpf3_not_committed_of_not_returned (flow : Dex.FlowDef σ) (key : InvocationKey σ)
    (history : InvocationHistory σ) (arguments : Arguments σ)
    (hasArguments : history.arguments = some arguments)
    (isConsistent : HistoryIsConsistent flow key history)
    (isNotReturned : history.progress.isReturned = false) :
    history.outcome ≠ .committed := by
  unfold HistoryIsConsistent at isConsistent
  rw [hasArguments] at isConsistent
  intro isCommitted
  rw [isConsistent.2 isCommitted] at isNotReturned
  cases isNotReturned

/-- The virtual task IDs are distinct. -/
theorem wpf3_virtualTaskIds_nodup (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) :
    (wpf3VirtualTaskIds config.ghost.invocations).Nodup :=
  invariant.virtualTaskIdsNodup

/-- The transition map of a Worker label on a real task with arguments. -/
theorem wpf3_onVirtualTask (config : Config σ) (taskId : Nat) (task : Task σ)
    (isFound : config.findRealTask? taskId = some task) (history : InvocationHistory σ)
    (arguments : Arguments σ) (isHistory : config.ghost.findInvocation? task.key = some history)
    (hasArguments : history.arguments = some arguments) (action : Nat → Dex.Action σ) :
    config.onVirtualTask taskId action = [action arguments.virtualTaskId] := by
  simp [Config.onVirtualTask, Config.virtualTaskIdOf, isFound, Ghost.argumentsOf, isHistory,
    hasArguments]

/-- The keys of the real tasks survive changing one task's base phase and counter. -/
theorem wpf3_sameKeys_of_maps (config other : Config σ)
    (isTaskKeys : other.tasks.map Task.key = config.tasks.map Task.key)
    (isZombieKeys : other.zombies.map Task.key = config.zombies.map Task.key)
    (key : InvocationKey σ) :
    other.realTasks.any (fun task => decide (task.key = key)) =
      config.realTasks.any (fun task => decide (task.key = key)) := by
  have isKeys : other.realTasks.map Task.key = config.realTasks.map Task.key := by
    simp only [Config.realTasks, List.map_append, isTaskKeys, isZombieKeys]
  have anyOther := List.any_map (f := Task.key) (l := other.realTasks)
    (p := fun taskKey => decide (taskKey = key))
  have anyConfig := List.any_map (f := Task.key) (l := config.realTasks)
    (p := fun taskKey => decide (taskKey = key))
  rw [isKeys] at anyOther
  exact anyOther.symm.trans anyConfig

/-- Changing tasks with a key-preserving change keeps their keys. -/
theorem wpf3_map_key_update (tasks : List (Task σ)) (taskId : Nat) (change : Task σ → Task σ)
    (keepsKey : ∀ task, (change task).key = task.key) :
    (tasks.map (fun task => if task.base.id = taskId then change task else task)).map Task.key =
      tasks.map Task.key := by
  rw [List.map_map]
  apply List.map_congr_left
  intro task _
  simp only [Function.comp_apply]
  split
  · exact keepsKey task
  · rfl

/-- After the step, a stutter: the abstraction is unchanged when the ghost is. -/
theorem wpf3_stutter (program : Program σ) (config next : Config σ) (action : Action σ)
    (noActions : dexActionsOf program config action = [])
    (sameClock : next.clock = config.clock) (sameRun : next.run = config.run)
    (sameGhost : next.ghost = config.ghost)
    (isTaskKeys : next.tasks.map Task.key = config.tasks.map Task.key)
    (isZombieKeys : next.zombies.map Task.key = config.zombies.map Task.key)
    (events : List (Obs σ)) (noEvents : eraseStepEvents config events = []) :
    IsStepSimulated program config action events next := by
  unfold IsStepSimulated
  rw [noActions, noEvents, wpf3_abstractConfig_congr config next sameClock sameRun sameGhost
    (wpf3_sameKeys_of_maps config next isTaskKeys isZombieKeys)]
  rfl

/-! ## [WORKER-EFFECT], first part: the handler runs -/

theorem wpf3_workerRun_simulated (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (isFirstRun : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.workerRun taskId isFirstRun) = some (events, next)) :
    IsStepSimulated program config (.workerRun taskId isFirstRun) events next := by
  dsimp only [step] at isStep
  cases isFound : config.findRealTask? taskId with
  | none => rw [isFound] at isStep; cases isStep
  | some task =>
    rw [isFound] at isStep
    dsimp only at isStep
    obtain ⟨history, arguments, isMember, _, isHistory, hasArguments, follows, isConsistent⟩ :=
      wpf3_realTask_facts program config invariant taskId task isFound
    obtain ⟨followArguments, hasFollowArguments, sameContext, sameInput, sameSnapshot, sameCR,
      _, phaseFacts⟩ := follows
    rw [hasArguments] at hasFollowArguments
    cases hasFollowArguments
    cases isPhase : task.base.phase with
    | running _ _ => rw [isPhase] at isStep; cases isStep
    | returned _ => rw [isPhase] at isStep; cases isStep
    | started =>
      rw [isPhase, isHistory] at isStep
      dsimp only at isStep
      split at isStep
      · cases isStep
      rename_i isAgree
      have isFirstRunEq : isFirstRun = history.progress.isNotRun := by simpa using isAgree
      obtain ⟨isEvents, isNext⟩ := Prod.mk.inj (Option.some.inj isStep)
      subst isEvents
      have sameClock : next.clock = config.clock := by rw [← isNext]; rfl
      have sameRun : next.run = config.run := by rw [← isNext]; rfl
      have isTaskKeys : next.tasks.map Task.key = config.tasks.map Task.key := by
        rw [← isNext]; exact wpf3_map_key_update _ _ _ (fun _ => rfl)
      have isZombieKeys : next.zombies.map Task.key = config.zombies.map Task.key := by
        rw [← isNext]; exact wpf3_map_key_update _ _ _ (fun _ => rfl)
      cases isFirstRun with
      | false =>
        have sameGhost : next.ghost = config.ghost := by rw [← isNext]; rfl
        exact wpf3_stutter program config next _ (by simp [dexActionsOf]) sameClock sameRun
          sameGhost isTaskKeys isZombieKeys [] (by simp [eraseStepEvents])
      | true =>
        have isGhost : next.ghost = config.ghost.updateInvocation task.key (fun history =>
            { history with
              progress := .ran (task.base.compute (program task.attemptContext)).1
                (task.base.compute (program task.attemptContext)).2 }) := by
          rw [← isNext]; rfl
        have isProgress : history.progress = .notRun := by
          cases isProgressCase : history.progress with
          | notRun => rfl
          | ran _ _ => rw [isProgressCase] at isFirstRunEq; cases isFirstRunEq
          | returned _ => rw [isProgressCase] at isFirstRunEq; cases isFirstRunEq
        have isNotCommitted := wpf3_not_committed_of_not_returned _ _ history arguments
          hasArguments isConsistent (by rw [isProgress]; rfl)
        have isVirtual := wpf3_hasVirtualTask_of_realTask config task isMember history arguments
          hasArguments isNotCommitted
        have noEmitted : history.emittedCount = 0 := by
          unfold HistoryIsConsistent at isConsistent
          rw [hasArguments, isProgress] at isConsistent
          exact isConsistent.1
        have isCompute := wpf3_task_compute program ignoresAttemptContext task arguments
          sameContext sameInput sameSnapshot sameCR
        unfold IsStepSimulated
        rw [show dexActionsOf program config (.workerRun taskId true) =
            [.workerRun arguments.virtualTaskId] by
          simp only [dexActionsOf, ↓reduceIte]
          exact wpf3_onVirtualTask config taskId task isFound history arguments isHistory
            hasArguments _]
        rw [wpf3_exec_singleton]
        rw [wpf3_abstractConfig_updateInvocation config next
          (wpf3_virtualTaskIds_nodup program config invariant) invariant.ghostKeysNodup
          task.key history arguments isHistory hasArguments isVirtual _ (by intro; rfl)
          (by intro; rfl) sameClock sameRun isGhost
          (wpf3_sameKeys_of_maps config next isTaskKeys isZombieKeys)]
        show Dex.step program.failureFreeFlow (abstractConfig config)
          (.workerRun arguments.virtualTaskId) = _
        simp only [Dex.step, wpf3_findTask?_virtualTasks config
          (wpf3_virtualTaskIds_nodup program config invariant) task.key history arguments
          (wpf3_mem_of_get? _ _ _ isHistory) hasArguments isVirtual,
          InvocationHistory.virtualPhase, isProgress, eraseStepEvents, List.filterMap_nil,
          isCompute, noEmitted, List.drop_zero]
        rfl

/-! ## [WORKER-EFFECT] and [ZOMBIE-EFFECT]: one effect -/

theorem wpf3_workerEffect_simulated (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (isFirstOccurrence : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.workerEffect taskId isFirstOccurrence) = some (events, next)) :
    IsStepSimulated program config (.workerEffect taskId isFirstOccurrence) events next := by
  dsimp only [step] at isStep
  cases isFound : config.findRealTask? taskId with
  | none => rw [isFound] at isStep; cases isStep
  | some task =>
    rw [isFound] at isStep
    dsimp only at isStep
    obtain ⟨history, arguments, isMember, _, isHistory, hasArguments, follows, isConsistent⟩ :=
      wpf3_realTask_facts program config invariant taskId task isFound
    obtain ⟨followArguments, hasFollowArguments, sameContext, sameInput, sameSnapshot, sameCR,
      emittedAtMost, phaseFacts⟩ := follows
    rw [hasArguments] at hasFollowArguments
    cases hasFollowArguments
    have isEmittedCount : config.ghost.emittedCountOf task.key = history.emittedCount := by
      simp [Ghost.emittedCountOf, isHistory]
    cases isPhase : task.base.phase with
    | started => rw [isPhase] at isStep; cases isStep
    | returned _ => rw [isPhase] at isStep; cases isStep
    | running remaining response =>
      cases remaining with
      | nil => rw [isPhase] at isStep; cases isStep
      | cons effect laterEffects =>
        rw [isPhase] at phaseFacts
        obtain ⟨isRemaining, isResponse, _, isStartedRun⟩ := phaseFacts
        rw [isPhase] at isStep
        dsimp only at isStep
        split at isStep
        · cases isStep
        rename_i isAgree
        have isFirstEq : isFirstOccurrence =
            decide (config.ghost.emittedCountOf task.key < task.emittedCount + 1) := by
          simpa using isAgree
        rw [isEmittedCount] at isFirstEq
        obtain ⟨isEvents, isNext⟩ := Prod.mk.inj (Option.some.inj isStep)
        subst isEvents
        have sameClock : next.clock = config.clock := by rw [← isNext]; rfl
        have sameRun : next.run = config.run := by rw [← isNext]; rfl
        have isTaskKeys : next.tasks.map Task.key = config.tasks.map Task.key := by
          rw [← isNext]; exact wpf3_map_key_update _ _ _ (fun _ => rfl)
        have isZombieKeys : next.zombies.map Task.key = config.zombies.map Task.key := by
          rw [← isNext]; exact wpf3_map_key_update _ _ _ (fun _ => rfl)
        have isKeyEq : (task.base.step, task.base.method) = task.key := rfl
        cases isFirstOccurrence with
        | false =>
          have sameGhost : next.ghost = config.ghost := by rw [← isNext]; rfl
          have isRepeated : task.emittedCount + 1 ≤ history.emittedCount := by
            simp only [Bool.false_eq, decide_eq_false_iff_not, Nat.not_lt] at isFirstEq
            exact isFirstEq
          refine wpf3_stutter program config next _ (by simp [dexActionsOf]) sameClock sameRun
            sameGhost isTaskKeys isZombieKeys _ ?_
          simp [eraseStepEvents, isKeyEq, isEmittedCount, isRepeated]
        | true =>
          have isEmittedEq : history.emittedCount = task.emittedCount := by
            simp only [Bool.true_eq, decide_eq_true_eq] at isFirstEq
            omega
          have isGhost : next.ghost = config.ghost.updateInvocation task.key (fun history =>
              { history with emittedCount := task.emittedCount + 1 }) := by
            rw [← isNext]; rfl
          have isVirtualPhase : history.virtualPhase = .running (effect :: laterEffects) response ∧
              ({ history with emittedCount := task.emittedCount + 1 } :
                InvocationHistory σ).virtualPhase = .running laterEffects response := by
            unfold HistoryIsConsistent at isConsistent
            rw [hasArguments] at isConsistent
            cases isProgress : history.progress with
            | notRun => rw [isProgress] at isStartedRun; cases isStartedRun
            | returned _ =>
              rw [isProgress] at isConsistent
              have isEmptied := List.drop_of_length_le (l := (arguments.compute
                program.failureFreeFlow task.key).1) (i := task.emittedCount) (by omega)
              rw [← isRemaining] at isEmptied
              cases isEmptied
            | ran effects historyResponse =>
              rw [isProgress] at isConsistent
              obtain ⟨⟨isEffects, isHistoryResponse, _⟩, _⟩ := isConsistent
              unfold InvocationHistory.virtualPhase
              dsimp only
              rw [isProgress, isEmittedEq, isEffects, isHistoryResponse, ← isResponse]
              dsimp only
              refine ⟨by rw [← isRemaining], ?_⟩
              rw [List.drop_add_one_eq_tail_drop, ← isRemaining]
              rfl
          have isNotCommitted := wpf3_not_committed_of_not_returned _ _ history arguments
            hasArguments isConsistent (by
              unfold InvocationHistory.virtualPhase at isVirtualPhase
              cases isProgress : history.progress with
              | notRun => rw [isProgress] at isVirtualPhase; cases isVirtualPhase.1
              | ran _ _ => rfl
              | returned _ => rw [isProgress] at isVirtualPhase; cases isVirtualPhase.1)
          have isVirtual := wpf3_hasVirtualTask_of_realTask config task isMember history arguments
            hasArguments isNotCommitted
          unfold IsStepSimulated
          rw [show dexActionsOf program config (.workerEffect taskId true) =
              [.workerEffect arguments.virtualTaskId] by
            simp only [dexActionsOf, ↓reduceIte]
            exact wpf3_onVirtualTask config taskId task isFound history arguments isHistory
              hasArguments _]
          rw [wpf3_exec_singleton]
          rw [wpf3_abstractConfig_updateInvocation config next
            (wpf3_virtualTaskIds_nodup program config invariant) invariant.ghostKeysNodup
            task.key history arguments isHistory hasArguments isVirtual _ (by intro; rfl)
            (by intro; rfl) sameClock sameRun isGhost
            (wpf3_sameKeys_of_maps config next isTaskKeys isZombieKeys)]
          show Dex.step program.failureFreeFlow (abstractConfig config)
            (.workerEffect arguments.virtualTaskId) = _
          have isFresh : ¬ task.emittedCount + 1 ≤ config.ghost.emittedCountOf task.key := by
            rw [isEmittedCount]; omega
          simp only [Dex.step, wpf3_findTask?_virtualTasks config
            (wpf3_virtualTaskIds_nodup program config invariant) task.key history arguments
            (wpf3_mem_of_get? _ _ _ isHistory) hasArguments isVirtual, isVirtualPhase.1,
            isVirtualPhase.2, eraseStepEvents, List.filterMap_cons, List.filterMap_nil, isKeyEq,
            isFresh, ↓reduceIte]
          rfl

/-! ## [WORKER-EFFECT], last part: the task returns -/

theorem wpf3_workerReturn_simulated (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (isFirstReturn : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.workerReturn taskId isFirstReturn) = some (events, next)) :
    IsStepSimulated program config (.workerReturn taskId isFirstReturn) events next := by
  dsimp only [step] at isStep
  cases isFound : config.findRealTask? taskId with
  | none => rw [isFound] at isStep; cases isStep
  | some task =>
    rw [isFound] at isStep
    dsimp only at isStep
    obtain ⟨history, arguments, isMember, _, isHistory, hasArguments, follows, isConsistent⟩ :=
      wpf3_realTask_facts program config invariant taskId task isFound
    obtain ⟨followArguments, hasFollowArguments, _, _, _, _, emittedAtMost, phaseFacts⟩ := follows
    rw [hasArguments] at hasFollowArguments
    cases hasFollowArguments
    cases isPhase : task.base.phase with
    | started => rw [isPhase] at isStep; cases isStep
    | returned _ => rw [isPhase] at isStep; cases isStep
    | running remaining response =>
      cases remaining with
      | cons _ _ => rw [isPhase] at isStep; cases isStep
      | nil =>
        rw [isPhase] at phaseFacts
        obtain ⟨isRemaining, isResponse, emittedAtMostLength, isStartedRun⟩ := phaseFacts
        rw [isPhase, isHistory] at isStep
        dsimp only at isStep
        split at isStep
        · cases isStep
        rename_i isAgree
        have isFirstEq : isFirstReturn = !history.progress.isReturned := by
          revert isAgree
          cases isFirstReturn <;> cases history.progress.isReturned <;> simp
        obtain ⟨isEvents, isNext⟩ := Prod.mk.inj (Option.some.inj isStep)
        subst isEvents
        have sameClock : next.clock = config.clock := by rw [← isNext]; rfl
        have sameRun : next.run = config.run := by rw [← isNext]; rfl
        have isTaskKeys : next.tasks.map Task.key = config.tasks.map Task.key := by
          rw [← isNext]; exact wpf3_map_key_update _ _ _ (fun _ => rfl)
        have isZombieKeys : next.zombies.map Task.key = config.zombies.map Task.key := by
          rw [← isNext]; exact wpf3_map_key_update _ _ _ (fun _ => rfl)
        cases isFirstReturn with
        | false =>
          have sameGhost : next.ghost = config.ghost := by rw [← isNext]; rfl
          exact wpf3_stutter program config next _ (by simp [dexActionsOf]) sameClock sameRun
            sameGhost isTaskKeys isZombieKeys [] (by simp [eraseStepEvents])
        | true =>
          have isGhost : next.ghost = config.ghost.updateInvocation task.key (fun history =>
              { history with progress := .returned response }) := by
            rw [← isNext]; rfl
          have isNotReturned : history.progress.isReturned = false := by
            cases isReturnedCase : history.progress.isReturned
            · rfl
            · rw [isReturnedCase] at isFirstEq; cases isFirstEq
          have isVirtualPhase : history.virtualPhase = .running [] response := by
            unfold HistoryIsConsistent at isConsistent
            rw [hasArguments] at isConsistent
            have isAllEmitted : (arguments.compute program.failureFreeFlow task.key).1.length ≤
                task.emittedCount := List.drop_eq_nil_iff.mp isRemaining.symm
            cases isProgress : history.progress with
            | notRun => rw [isProgress] at isStartedRun; cases isStartedRun
            | returned _ => rw [isProgress] at isNotReturned; cases isNotReturned
            | ran effects historyResponse =>
              rw [isProgress] at isConsistent
              obtain ⟨⟨isEffects, isHistoryResponse, atMostLength⟩, _⟩ := isConsistent
              unfold InvocationHistory.virtualPhase
              rw [isProgress, isEffects, isHistoryResponse, ← isResponse]
              dsimp only
              rw [List.drop_of_length_le (by omega)]
          have isNotCommitted := wpf3_not_committed_of_not_returned _ _ history arguments
            hasArguments isConsistent isNotReturned
          have isVirtual := wpf3_hasVirtualTask_of_realTask config task isMember history arguments
            hasArguments isNotCommitted
          unfold IsStepSimulated
          rw [show dexActionsOf program config (.workerReturn taskId true) =
              [.workerReturn arguments.virtualTaskId] by
            simp only [dexActionsOf, ↓reduceIte]
            exact wpf3_onVirtualTask config taskId task isFound history arguments isHistory
              hasArguments _]
          rw [wpf3_exec_singleton]
          rw [wpf3_abstractConfig_updateInvocation config next
            (wpf3_virtualTaskIds_nodup program config invariant) invariant.ghostKeysNodup
            task.key history arguments isHistory hasArguments isVirtual _ (by intro; rfl)
            (by intro; rfl) sameClock sameRun isGhost
            (wpf3_sameKeys_of_maps config next isTaskKeys isZombieKeys)]
          show Dex.step program.failureFreeFlow (abstractConfig config)
            (.workerReturn arguments.virtualTaskId) = _
          simp only [Dex.step, wpf3_findTask?_virtualTasks config
            (wpf3_virtualTaskIds_nodup program config invariant) task.key history arguments
            (wpf3_mem_of_get? _ _ _ isHistory) hasArguments isVirtual, isVirtualPhase,
            eraseStepEvents, List.filterMap_nil]
          rfl

/-! ## A real task leaves: the drop and the stale result -/

/-- Removing a real task keeps the keys of the others, for a key other than its own. -/
theorem wpf3_any_key_remove_other (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (task : Task σ)
    (isMember : task ∈ config.realTasks) (isId : task.base.id = taskId)
    (key : InvocationKey σ) (isOtherKey : key ≠ task.key) :
    (config.realTasks.filter (fun other => !decide (other.base.id = taskId))).any
        (fun other => decide (other.key = key)) =
      config.realTasks.any (fun other => decide (other.key = key)) := by
  apply Bool.eq_iff_iff.mpr
  simp only [List.any_eq_true, List.mem_filter, decide_eq_true_eq, Bool.not_eq_true',
    decide_eq_false_iff_not]
  constructor
  · rintro ⟨other, ⟨isOtherMember, _⟩, isKey⟩
    exact ⟨other, isOtherMember, isKey⟩
  · rintro ⟨other, isOtherMember, isKey⟩
    refine ⟨other, ⟨isOtherMember, ?_⟩, isKey⟩
    intro isSameId
    have isSame := eq_of_nodup_map (fun task : Task σ => task.base.id) config.realTasks
      invariant.realTaskIdsNodup isOtherMember isMember (by simp [isSameId, isId])
    rw [isSame] at isKey
    exact isOtherKey isKey.symm

/-- Removing a real task that is not the last of a canceled invocation keeps α. -/
theorem wpf3_abstractConfig_removeNotLast (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (task : Task σ)
    (isFound : config.findRealTask? taskId = some task)
    (isNotLast : config.isLastOfCanceled task = false) (other : Config σ)
    (sameClock : other.clock = config.clock) (sameRun : other.run = config.run)
    (sameGhost : other.ghost = config.ghost)
    (isRealTasks : other.realTasks =
      config.realTasks.filter (fun other => !decide (other.base.id = taskId))) :
    abstractConfig other = abstractConfig config := by
  obtain ⟨history, arguments, isMember, isId, isHistory, hasArguments, _, _⟩ :=
    wpf3_realTask_facts program config invariant taskId task isFound
  unfold abstractConfig
  rw [sameClock, sameRun, sameGhost, wpf3_virtualTasks_eq, wpf3_virtualTasks_eq, sameGhost]
  congr 2
  apply wpf3_filterMap_congr
  intro entry isEntryMember
  have sameVirtual : other.hasVirtualTask entry.1 entry.2 = config.hasVirtualTask entry.1 entry.2 := by
    by_cases isKey : entry.1 = task.key
    · have isEntry : entry = (task.key, history) := by
        obtain ⟨entryKey, entryHistory⟩ := entry
        simp only at isKey
        subst isKey
        have isFoundEntry := wpf3_get?_of_mem_nodup _ _ _ invariant.ghostKeysNodup isEntryMember
        unfold Ghost.findInvocation? at isHistory
        rw [isHistory] at isFoundEntry
        cases isFoundEntry
        rfl
      subst isEntry
      dsimp only
      unfold Config.isLastOfCanceled at isNotLast
      rw [isHistory] at isNotLast
      dsimp only at isNotLast
      rw [hasArguments] at isNotLast
      unfold Config.hasVirtualTask
      rw [hasArguments]
      cases isOutcome : history.outcome with
      | live => rfl
      | committed => rfl
      | canceled =>
        rw [isOutcome] at isNotLast
        simp only [decide_true, Option.isSome_some, Bool.and_true, Bool.true_and,
          Bool.not_eq_false', List.any_eq_true, Bool.and_eq_true, decide_eq_true_eq,
          Bool.not_eq_true', decide_eq_false_iff_not] at isNotLast
        obtain ⟨sibling, isSiblingMember, isSiblingKey, isSiblingId⟩ := isNotLast
        simp only [Option.isSome_some, Bool.true_and]
        rw [isRealTasks]
        have siblingKept : sibling ∈ config.realTasks.filter
            (fun other => !decide (other.base.id = taskId)) := by
          simp only [List.mem_filter, Bool.not_eq_true', decide_eq_false_iff_not]
          exact ⟨isSiblingMember, by rw [← isId]; exact isSiblingId⟩
        have anyOther : (config.realTasks.filter
            (fun other => !decide (other.base.id = taskId))).any
            (fun other => decide (other.key = task.key)) = true :=
          List.any_eq_true.mpr ⟨sibling, siblingKept, by simp [isSiblingKey]⟩
        have anyConfig : config.realTasks.any (fun other => decide (other.key = task.key)) = true :=
          List.any_eq_true.mpr ⟨task, isMember, by simp⟩
        rw [anyOther, anyConfig]
    · unfold Config.hasVirtualTask
      rw [isRealTasks, wpf3_any_key_remove_other program config invariant taskId task isMember isId
        entry.1 isKey]
  unfold wpf3VirtualEntry
  rw [sameVirtual]

/-- The last real task of a canceled invocation leaving removes its virtual task. -/
theorem wpf3_removeLast_facts (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (task : Task σ)
    (isFound : config.findRealTask? taskId = some task)
    (isLast : config.isLastOfCanceled task = true) (other : Config σ)
    (sameClock : other.clock = config.clock) (sameRun : other.run = config.run)
    (sameGhost : other.ghost = config.ghost)
    (isRealTasks : other.realTasks =
      config.realTasks.filter (fun other => !decide (other.base.id = taskId))) :
    ∃ history arguments,
      config.ghost.findInvocation? task.key = some history ∧ history.arguments = some arguments ∧
      history.outcome = .canceled ∧ config.hasVirtualTask task.key history = true ∧
      abstractConfig other = (abstractConfig config).removeTask arguments.virtualTaskId := by
  obtain ⟨history, arguments, isMember, isId, isHistory, hasArguments, _, _⟩ :=
    wpf3_realTask_facts program config invariant taskId task isFound
  unfold Config.isLastOfCanceled at isLast
  rw [isHistory] at isLast
  dsimp only at isLast
  rw [hasArguments] at isLast
  simp only [Option.isSome_some, Bool.and_true, Bool.and_eq_true, decide_eq_true_eq,
    Bool.not_eq_true'] at isLast
  obtain ⟨isCanceled, noSiblingAny⟩ := isLast
  have noSibling : ∀ sibling, sibling ∈ config.realTasks → sibling.key = task.key →
      sibling.base.id = task.base.id := by
    intro sibling isSiblingMember isSiblingKey
    have isNotSibling := List.any_eq_false.mp noSiblingAny sibling isSiblingMember
    by_cases isSameId : sibling.base.id = task.base.id
    · exact isSameId
    · exfalso
      apply isNotSibling
      simp [isSiblingKey, isSameId]
  have isVirtual : config.hasVirtualTask task.key history = true :=
    wpf3_hasVirtualTask_of_realTask config task isMember history arguments hasArguments
      (by rw [isCanceled]; intro isCommitted; cases isCommitted)
  have isNotVirtual : other.hasVirtualTask task.key history = false := by
    unfold Config.hasVirtualTask
    rw [hasArguments, isCanceled, isRealTasks]
    simp only [Option.isSome_some, Bool.true_and, List.any_eq_false, List.mem_filter,
      Bool.not_eq_true', decide_eq_false_iff_not, decide_eq_true_eq, and_imp]
    intro sibling isSiblingMember isSiblingId isSiblingKey
    exact isSiblingId (by rw [← isId]; exact noSibling sibling isSiblingMember isSiblingKey)
  have sameOthers : ∀ otherKey, otherKey ≠ task.key →
      other.hasVirtualTask otherKey = config.hasVirtualTask otherKey := by
    intro otherKey isOtherKey
    funext otherHistory
    unfold Config.hasVirtualTask
    rw [isRealTasks, wpf3_any_key_remove_other program config invariant taskId task isMember isId
      otherKey isOtherKey]
  exact ⟨history, arguments, isHistory, hasArguments, isCanceled, isVirtual,
    wpf3_abstractConfig_removeLast config other
      (wpf3_virtualTaskIds_nodup program config invariant) invariant.ghostKeysNodup task.key
      history arguments isHistory hasArguments isVirtual isNotVirtual sameOthers sameClock sameRun
      sameGhost⟩

/-- A virtual task of an invocation that is no longer live is not current under α. -/
theorem wpf3_isTaskCurrent_abstract_false (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (key : InvocationKey σ) (history : InvocationHistory σ)
    (isHistory : config.ghost.findInvocation? key = some history)
    (isNotLive : history.outcome ≠ .live) (arguments : Arguments σ) (phase : Dex.TaskPhase σ) :
    (abstractConfig config).isTaskCurrent (arguments.toTask key phase) = false := by
  unfold Dex.Config.isTaskCurrent abstractConfig
  dsimp only
  cases isRun : config.run with
  | none => rfl
  | some run =>
    have runInvariant := invariant.runInvariant run isRun
    simp only [Option.map_some, Dex.Run.findStep?, Run.abstract, List.find?_map,
      Function.comp_def, Arguments.toTask]
    split
    · rfl
    · rename_i abstractRecord isAbstractRecord
      obtain ⟨record, isRecord, isAbstract⟩ := Option.map_eq_some_iff.mp isAbstractRecord
      subst isAbstract
      have isMember := List.mem_of_find?_eq_some isRecord
      have isFoundPredicate := List.find?_some isRecord
      have isId : record.id = key.1 := of_decide_eq_true isFoundPredicate
      dsimp only
      cases isInvocation : record.invocation with
      | none =>
        have isNone : run.abstractInvocation config.ghost record = none := by
          unfold Run.abstractInvocation; rw [isInvocation]
        rw [isNone]
        rfl
      | some invocation =>
        by_cases isMethod : invocation.method = key.2
        · exfalso
          have isMatching := runInvariant.attemptsMatch record isMember
          unfold AttemptMatchesRecord at isMatching
          rw [isInvocation] at isMatching
          obtain ⟨_, otherHistory, _, isOtherHistory, isLive, _⟩ := isMatching
          have isKey : (record.id, invocation.method) = key := by rw [isId, isMethod]
          rw [isKey, isHistory] at isOtherHistory
          cases isOtherHistory
          exact isNotLive isLive
        · have isMethodOf : (run.abstractInvocation config.ghost record).map Dex.Invocation.method =
              some invocation.method := by
            unfold Run.abstractInvocation
            rw [isInvocation]
            dsimp only
            split <;> rfl
          apply decide_eq_false
          intro isEqual
          rw [isEqual] at isMethodOf
          exact isMethod (Option.some.inj isMethodOf).symm

theorem wpf3_workerAbandon_simulated (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (isLastOfCanceled : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.workerAbandon taskId isLastOfCanceled) = some (events, next)) :
    IsStepSimulated program config (.workerAbandon taskId isLastOfCanceled) events next := by
  obtain ⟨task, isFound, isLastEq, isEvents, isNext⟩ :
      ∃ task, config.findRealTask? taskId = some task ∧
        isLastOfCanceled = config.isLastOfCanceled task ∧ events = [] ∧
        next = config.removeRealTask taskId := by
    dsimp only [step] at isStep
    cases isLive : config.findLiveTask? taskId with
    | some task =>
      rw [isLive] at isStep
      dsimp only at isStep
      split at isStep
      · cases isStep
      rename_i isAgree
      obtain ⟨isEvents, isNext⟩ := Prod.mk.inj (Option.some.inj isStep)
      refine ⟨task, wpf3_findRealTask?_of_findLiveTask? _ _ _ isLive, ?_, isEvents.symm,
        isNext.symm⟩
      revert isAgree
      cases isLastOfCanceled <;> cases config.isLastOfCanceled task <;> simp
    | none =>
      cases isZombie : config.findZombie? taskId with
      | none => rw [isLive, isZombie] at isStep; cases isStep
      | some task =>
        rw [isLive, isZombie] at isStep
        dsimp only at isStep
        split at isStep
        · cases isStep
        rename_i isAgree
        obtain ⟨isEvents, isNext⟩ := Prod.mk.inj (Option.some.inj isStep)
        refine ⟨task, wpf3_findRealTask?_of_findZombie? _ _ _ isLive isZombie, ?_,
          isEvents.symm, isNext.symm⟩
        revert isAgree
        cases isLastOfCanceled <;> cases config.isLastOfCanceled task <;> simp
  subst isEvents
  subst isNext
  have isRealTasks := wpf3_realTasks_removeRealTask config taskId
  cases isLastOfCanceled with
  | false =>
    unfold IsStepSimulated
    rw [show dexActionsOf program config (.workerAbandon taskId false) = [] by simp [dexActionsOf],
      wpf3_abstractConfig_removeNotLast program config invariant taskId task isFound isLastEq.symm
        (config.removeRealTask taskId) rfl rfl rfl isRealTasks]
    rfl
  | true =>
    obtain ⟨history, arguments, isHistory, hasArguments, isCanceled, isVirtual, isAbstract⟩ :=
      wpf3_removeLast_facts program config invariant taskId task isFound isLastEq.symm
        (config.removeRealTask taskId) rfl rfl rfl isRealTasks
    unfold IsStepSimulated
    rw [show dexActionsOf program config (.workerAbandon taskId true) =
        [.workerAbandon arguments.virtualTaskId] by
      simp only [dexActionsOf, ↓reduceIte]
      exact wpf3_onVirtualTask config taskId task isFound history arguments isHistory
        hasArguments _]
    rw [wpf3_exec_singleton, isAbstract]
    show Dex.step program.failureFreeFlow (abstractConfig config)
      (.workerAbandon arguments.virtualTaskId) = _
    simp only [Dex.step, wpf3_findTask?_virtualTasks config
      (wpf3_virtualTaskIds_nodup program config invariant) task.key history arguments
      (wpf3_mem_of_get? _ _ _ isHistory) hasArguments isVirtual,
      wpf3_isTaskCurrent_abstract_false program config invariant task.key history isHistory
        (by rw [isCanceled]; intro isLive; cases isLive),
      Bool.false_eq_true, ↓reduceIte, eraseStepEvents, List.filterMap_nil]

theorem wpf3_resultStale_simulated (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (isLastOfCanceled : Bool)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.resultStale taskId isLastOfCanceled) = some (events, next)) :
    IsStepSimulated program config (.resultStale taskId isLastOfCanceled) events next := by
  obtain ⟨task, response, isFound, isPhase, isLastEq, isEvents, isNext⟩ :
      ∃ task response, config.findRealTask? taskId = some task ∧
        task.base.phase = .returned response ∧
        isLastOfCanceled = config.isLastOfCanceled task ∧ events = [] ∧
        next = (config.removeRealTask taskId).stampOnly := by
    dsimp only [step] at isStep
    split at isStep
    · cases isFound : config.findRealTask? taskId with
      | none => rw [isFound] at isStep; cases isStep
      | some task =>
        rw [isFound] at isStep
        dsimp only at isStep
        cases isPhase : task.base.phase with
        | started => rw [isPhase] at isStep; cases isStep
        | running _ _ => rw [isPhase] at isStep; cases isStep
        | returned response =>
          rw [isPhase] at isStep
          dsimp only at isStep
          split at isStep
          · cases isStep
          rename_i isAgree
          obtain ⟨isEvents, isNext⟩ := Prod.mk.inj (Option.some.inj isStep)
          refine ⟨task, response, rfl, isPhase, ?_, isEvents.symm, isNext.symm⟩
          revert isAgree
          cases isLastOfCanceled <;> cases config.isLastOfCanceled task <;> simp
    · cases isStep
  subst isEvents
  subst isNext
  have isRealTasks : ((config.removeRealTask taskId).stampOnly).realTasks =
      config.realTasks.filter (fun other => !decide (other.base.id = taskId)) :=
    wpf3_realTasks_removeRealTask config taskId
  cases isLastOfCanceled with
  | false =>
    unfold IsStepSimulated
    rw [show dexActionsOf program config (.resultStale taskId false) = [] by simp [dexActionsOf],
      wpf3_abstractConfig_removeNotLast program config invariant taskId task isFound isLastEq.symm
        ((config.removeRealTask taskId).stampOnly) rfl rfl rfl isRealTasks]
    rfl
  | true =>
    obtain ⟨history, arguments, isHistory, hasArguments, isCanceled, isVirtual, isAbstract⟩ :=
      wpf3_removeLast_facts program config invariant taskId task isFound isLastEq.symm
        ((config.removeRealTask taskId).stampOnly) rfl rfl rfl isRealTasks
    obtain ⟨otherHistory, otherArguments, isMember, _, isOtherHistory, _, follows, isConsistent⟩ :=
      wpf3_realTask_facts program config invariant taskId task isFound
    rw [isHistory] at isOtherHistory
    cases isOtherHistory
    obtain ⟨followArguments, hasFollowArguments, _, _, _, _, _, phaseFacts⟩ := follows
    rw [isPhase] at phaseFacts
    obtain ⟨_, _, isReturned⟩ := phaseFacts
    have isVirtualPhase : ∃ returnedResponse,
        history.virtualPhase = .returned returnedResponse := by
      unfold InvocationHistory.virtualPhase
      cases isProgress : history.progress with
      | notRun => rw [isProgress] at isReturned; cases isReturned
      | ran _ _ => rw [isProgress] at isReturned; cases isReturned
      | returned returnedResponse => exact ⟨returnedResponse, rfl⟩
    obtain ⟨returnedResponse, isVirtualPhaseEq⟩ := isVirtualPhase
    unfold IsStepSimulated
    rw [show dexActionsOf program config (.resultStale taskId true) =
        [.stepCompleteStale arguments.virtualTaskId] by
      simp only [dexActionsOf, ↓reduceIte]
      exact wpf3_onVirtualTask config taskId task isFound history arguments isHistory
        hasArguments _]
    rw [wpf3_exec_singleton, isAbstract]
    show Dex.step program.failureFreeFlow (abstractConfig config)
      (.stepCompleteStale arguments.virtualTaskId) = _
    have isNotCurrent := wpf3_isTaskCurrent_abstract_false program config invariant task.key history
      isHistory (by rw [isCanceled]; intro isLive; cases isLive) arguments
      (.returned returnedResponse)
    simp only [Arguments.toTask] at isNotCurrent
    simp only [Dex.step, wpf3_findTask?_virtualTasks config
      (wpf3_virtualTaskIds_nodup program config invariant) task.key history arguments
      (wpf3_mem_of_get? _ _ _ isHistory) hasArguments isVirtual, isVirtualPhaseEq, Arguments.toTask,
      isNotCurrent, Bool.false_eq_true, ↓reduceIte, eraseStepEvents, List.filterMap_nil]

/-! ## The statement -/

/-- **WP-F3.** A Worker step of a task in `Θ` or `Z` maps to the same Worker
step of the virtual task when it is a first occurrence, and to a stutter
otherwise. The drop or stale result of the last real task of a canceled
invocation maps to [WORKER-ABANDON] or [STEP-COMPLETE-STALE] of the virtual
task. -/
theorem workerStep_simulated_proof (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ)
    (isWorker : action.simulationKind = .worker) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    IsStepSimulated program config action events next := by
  cases action with
  | workerRun taskId isFirstRun =>
    exact wpf3_workerRun_simulated program ignoresAttemptContext config invariant taskId
      isFirstRun events next isStep
  | workerEffect taskId isFirstOccurrence =>
    exact wpf3_workerEffect_simulated program config invariant taskId isFirstOccurrence events
      next isStep
  | workerReturn taskId isFirstReturn =>
    exact wpf3_workerReturn_simulated program config invariant taskId isFirstReturn events next
      isStep
  | workerAbandon taskId isLastOfCanceled =>
    exact wpf3_workerAbandon_simulated program config invariant taskId isLastOfCanceled events
      next isStep
  | resultStale taskId isLastOfCanceled =>
    exact wpf3_resultStale_simulated program config invariant taskId isLastOfCanceled events next
      isStep
  | stepStart _ isSnapshotReused =>
    cases isSnapshotReused <;> simp [Action.simulationKind] at isWorker
  | timerFire _ isWaitTimerFired =>
    cases isWaitTimerFired <;> simp [Action.simulationKind] at isWorker
  | tick _ => simp [Action.simulationKind] at isWorker
  | request _ => simp [Action.simulationKind] at isWorker
  | startRetry _ _ _ => simp [Action.simulationKind] at isWorker
  | engineReject _ => simp [Action.simulationKind] at isWorker
  | stepComplete _ => simp [Action.simulationKind] at isWorker
  | attemptFailed _ _ => simp [Action.simulationKind] at isWorker
  | attemptReturned _ => simp [Action.simulationKind] at isWorker
  | attemptHeartbeat _ _ => simp [Action.simulationKind] at isWorker
  | crash => simp [Action.simulationKind] at isWorker
  | recover => simp [Action.simulationKind] at isWorker
  | recoverInterrupt _ _ => simp [Action.simulationKind] at isWorker
  | internalCommit => simp [Action.simulationKind] at isWorker

end DexExpressiveness.DexF
