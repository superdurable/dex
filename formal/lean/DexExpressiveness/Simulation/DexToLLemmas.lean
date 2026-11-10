import DexExpressiveness.Simulation.Frame

/-!
# Dex to L: the Dex actions that need no L→Dex lemma

Work package 4. These lemmas show `IsSimulatedByL` directly for the Dex
actions whose simulation does not rebuild the running part of the relation:

| Dex action | L labels |
| --- | --- |
| `tick d` | `tick d` |
| `workerRun`, `workerReturn` | none |
| `stepCompleteStale` | none |
| `workerAbandon` | none, or `lateDrop` of the effect the task still holds |
| `workerEffect` after the close | `lateDeliver` of the emitted effect |
| `request (publish c v)` after the close | `request (input c v)`, refused |

`dex_workerAbandon_simulated` takes the hypothesis that a task holds at most
one effect, which `IsSimulationState.holdsAtMostOneEffect` gives. Without it
the lemma is false: [WORKER-ABANDON] drops every effect of the task, and L
drops one late emit per label.

Nothing here depends on another work package.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Lists -/

/-- With unique IDs, the task list is the task with a given ID followed by
the tasks with other IDs, up to order. -/
theorem wp4_perm_cons_filter (tasks : List (Dex.Task signature)) (task : Dex.Task signature)
    (idsNodup : (tasks.map (fun candidate => candidate.id)).Nodup) (isMember : task ∈ tasks) :
    tasks.Perm (task :: tasks.filter (fun candidate => !decide (candidate.id = task.id))) := by
  induction tasks with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    rw [List.map_cons, List.nodup_cons] at idsNodup
    obtain ⟨headIsFresh, restNodup⟩ := idsNodup
    rcases List.mem_cons.mp isMember with isHead | isInRest
    · subst isHead
      have keepsRest : rest.filter (fun candidate => !decide (candidate.id = task.id)) = rest := by
        apply List.filter_eq_self.mpr
        intro candidate isCandidateMember
        have differs : candidate.id ≠ task.id := fun sameId =>
          headIsFresh (sameId ▸ List.mem_map_of_mem (f := fun candidate => candidate.id)
            isCandidateMember)
        simp [differs]
      simp [keepsRest]
    · have differs : head.id ≠ task.id := fun sameId =>
        headIsFresh (sameId ▸ List.mem_map_of_mem (f := fun candidate => candidate.id) isInRest)
      have keepsHead : (head :: rest).filter (fun candidate => !decide (candidate.id = task.id)) =
          head :: rest.filter (fun candidate => !decide (candidate.id = task.id)) := by
        simp [differs]
      rw [keepsHead]
      exact ((inductionHypothesis restNodup isInRest).cons head).trans (List.Perm.swap task head _)

/-- A member of a list sits at some index, and the list is the member
followed by the list without that index, up to order. -/
theorem wp4_exists_removeAt {α : Type} (elements : List α) (wanted : α)
    (isMember : wanted ∈ elements) :
    ∃ index, elements[index]? = some wanted ∧
      elements.Perm (wanted :: removeAt elements index) := by
  induction elements with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    rcases List.mem_cons.mp isMember with isHead | isInRest
    · subst isHead
      exact ⟨0, rfl, List.Perm.refl _⟩
    · obtain ⟨index, isAtIndex, isPerm⟩ := inductionHypothesis isInRest
      refine ⟨index + 1, by simpa using isAtIndex, ?_⟩
      exact (isPerm.cons head).trans (List.Perm.swap wanted head _)

/-- Mapping a list with a function that keeps `effectsOf` keeps the flattened effects. -/
theorem wp4_flatMap_map_of_agrees {α β : Type} (effectsOf : α → List β) (change : α → α)
    (elements : List α)
    (agrees : ∀ element, element ∈ elements → effectsOf (change element) = effectsOf element) :
    (elements.map change).flatMap effectsOf = elements.flatMap effectsOf := by
  induction elements with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    rw [List.map_cons, List.flatMap_cons, List.flatMap_cons, agrees head List.mem_cons_self,
      inductionHypothesis (fun element isMember => agrees element (List.mem_cons_of_mem _ isMember))]

/-! ## Tasks that the relation cannot tell apart -/

/-- Two tasks that `Related` cannot tell apart: they differ at most in a
phase that holds the same effects. -/
structure TaskAgrees (program : Program) (task changed : Dex.Task signature) : Prop where
  /-- The same ID. -/
  id : changed.id = task.id
  /-- The same Step. -/
  step : changed.step = task.step
  /-- The same method. -/
  method : changed.method = task.method
  /-- The same input. -/
  input : changed.input = task.input
  /-- The same snapshot. -/
  snapshot : changed.snapshot = task.snapshot
  /-- The same condition results. -/
  conditionResults : changed.conditionResults = task.conditionResults
  /-- The same effects still to emit. -/
  pendingEffects : pendingEffects ⟦program⟧ changed = pendingEffects ⟦program⟧ task

/-- A task agrees with itself. -/
theorem TaskAgrees.refl (program : Program) (task : Dex.Task signature) :
    TaskAgrees program task task :=
  ⟨rfl, rfl, rfl, rfl, rfl, rfl, rfl⟩

/-- Whether a task is current depends only on its ID, Step, and method. -/
theorem isTaskCurrent_of_agrees (program : Program) (config next : Dex.Config signature)
    (sameRun : next.run = config.run) (task changed : Dex.Task signature)
    (agrees : TaskAgrees program task changed) :
    next.isTaskCurrent changed = config.isTaskCurrent task :=
  isTaskCurrent_congr config next task changed sameRun agrees.step agrees.method agrees.id

/-- A running task is still there after tasks were replaced by tasks that agree. -/
theorem wp4_hasRunningTask_of_agrees (program : Program) (config next : Dex.Config signature)
    (record : Dex.StepRecord signature) (method : Dex.Method)
    (Holds : Dex.Task signature → Prop)
    (forward : ∀ task, task ∈ config.tasks →
      ∃ changed, changed ∈ next.tasks ∧ TaskAgrees program task changed)
    (holdsAgrees : ∀ task changed, TaskAgrees program task changed → Holds task → Holds changed)
    (hasTask : HasRunningTask config record method Holds) :
    HasRunningTask next record method Holds := by
  obtain ⟨task, isMember, isInvocation, isStep, isMethod, isInput, holds⟩ := hasTask
  obtain ⟨changed, isChangedMember, agrees⟩ := forward task isMember
  exact ⟨changed, isChangedMember, by rw [agrees.id]; exact isInvocation,
    agrees.step.trans isStep, agrees.method.trans isMethod, agrees.input.trans isInput,
    holdsAgrees task changed agrees holds⟩

/-- A thread's phase still matches its Step after tasks were replaced by
tasks that agree. -/
theorem wp4_phaseMatchesStep_of_agrees (program : Program) (lConfig : L.Config)
    (config next : Dex.Config signature) (thread : L.Thread)
    (record : Dex.StepRecord signature)
    (forward : ∀ task, task ∈ config.tasks →
      ∃ changed, changed ∈ next.tasks ∧ TaskAgrees program task changed)
    (isMatch : PhaseMatchesStep program lConfig config thread record) :
    PhaseMatchesStep program lConfig next thread record := by
  obtain ⟨core, code, timer, phase⟩ := thread
  cases phase <;> dsimp only [PhaseMatchesStep] at isMatch ⊢ <;>
    first
    | exact isMatch
    | (refine ⟨isMatch.1, wp4_hasRunningTask_of_agrees program config next record _ _ forward ?_ isMatch.2⟩
       intro task changed agrees holds
       first
       | exact holds
       | (simp only [agrees.pendingEffects, agrees.conditionResults, agrees.snapshot]
          exact holds))

/-- The relation is kept when the Dex tasks are replaced by tasks that agree
and nothing else changes. -/
theorem wp4_related_of_tasks_agree (program : Program) (lConfig : L.Config)
    (config next : Dex.Config signature) (isRelated : Related program lConfig config)
    (sameClock : next.clock = config.clock) (sameCommit : next.lastCommitAt = config.lastCommitAt)
    (sameRun : next.run = config.run)
    (forward : ∀ task, task ∈ config.tasks →
      ∃ changed, changed ∈ next.tasks ∧ TaskAgrees program task changed)
    (backward : ∀ changed, changed ∈ next.tasks →
      ∃ task, task ∈ config.tasks ∧ TaskAgrees program task changed)
    (sameEffects : next.tasks.flatMap (pendingEffects ⟦program⟧) =
      config.tasks.flatMap (pendingEffects ⟦program⟧)) :
    Related program lConfig next :=
  { clock := isRelated.clock.trans sameClock.symm
    stamp := isRelated.stamp.trans sameCommit.symm
    status := by rw [sameRun]; exact isRelated.status
    notStarted := fun hasNoRun => by
      have noTask := isRelated.notStarted (sameRun ▸ hasNoRun)
      apply List.eq_nil_iff_forall_not_mem.mpr
      intro changed isMember
      obtain ⟨task, isTaskMember, _⟩ := backward changed isMember
      rw [noTask] at isTaskMember
      cases isTaskMember
    running := fun run hasRun isRunning =>
      let related := isRelated.running run (sameRun ▸ hasRun) isRunning
      { related with
        threadsHaveSteps := fun thread isMember isNotHalted => by
          obtain ⟨record, isRecordMember, isActive, carries, isMatch⟩ :=
            related.threadsHaveSteps thread isMember isNotHalted
          exact ⟨record, isRecordMember, isActive, carries,
            wp4_phaseMatchesStep_of_agrees program lConfig config next thread record forward
              isMatch⟩
        tasksAreCurrent := fun changed isMember => by
          obtain ⟨task, isTaskMember, agrees⟩ := backward changed isMember
          rw [isTaskCurrent_of_agrees program config next sameRun task changed agrees]
          exact related.tasksAreCurrent task isTaskMember }
    closed := fun run status hasRun isClosed =>
      let related := isRelated.closed run status (sameRun ▸ hasRun) isClosed
      { related with
        lateEmits := sameEffects ▸ related.lateEmits
        tasksAreStale := fun changed isMember => by
          obtain ⟨task, isTaskMember, agrees⟩ := backward changed isMember
          rw [isTaskCurrent_of_agrees program config next sameRun task changed agrees]
          exact related.tasksAreStale task isTaskMember } }

/-- The relation is kept when one task is replaced by a task that agrees with it. -/
theorem wp4_related_of_task_updated (program : Program) (lConfig : L.Config)
    (config : Dex.Config signature) (isRelated : Related program lConfig config)
    (tasksDistinct : TasksAreDistinct config) (taskId : Nat) (task : Dex.Task signature)
    (isFound : config.findTask? taskId = some task)
    (change : Dex.Task signature → Dex.Task signature)
    (agrees : TaskAgrees program task (change task)) :
    Related program lConfig (config.updateTask taskId change) := by
  have agreesEach : ∀ candidate, candidate ∈ config.tasks →
      TaskAgrees program candidate (if candidate.id = taskId then change candidate else candidate) := by
    intro candidate isMember
    by_cases hasId : candidate.id = taskId
    · have isTask := eq_of_isFound config taskId candidate task tasksDistinct isMember hasId isFound
      subst isTask
      simpa [hasId] using agrees
    · simpa [hasId] using TaskAgrees.refl program candidate
  refine wp4_related_of_tasks_agree program lConfig config (config.updateTask taskId change)
    isRelated rfl rfl rfl ?_ ?_ ?_
  · intro candidate isMember
    exact ⟨_, List.mem_map_of_mem isMember, agreesEach candidate isMember⟩
  · intro changed isMember
    obtain ⟨candidate, isCandidateMember, isChanged⟩ := mem_updateTask config taskId change changed
      isMember
    exact ⟨candidate, isCandidateMember, isChanged ▸ agreesEach candidate isCandidateMember⟩
  · exact wp4_flatMap_map_of_agrees (pendingEffects ⟦program⟧) _ config.tasks
      (fun candidate isMember => (agreesEach candidate isMember).pendingEffects)

/-! ## Running and returning a handler -/

/-- Running a handler is simulated by no L label: the task holds the same
effects before and after. -/
theorem dex_workerRun_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (taskId : Nat) (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig (.workerRun taskId) =
      some (dexEvents, dexNext)) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  simp only [Dex.system, Dex.step] at isStep
  split at isStep
  · cases isStep
  · rename_i task isFound
    split at isStep
    · rename_i isStarted
      cases isStep
      refine ⟨[], lConfig, Nat.zero_le 1, rfl, ?_⟩
      apply wp4_related_of_task_updated program lConfig dexConfig state.isRelated
        state.tasksDistinct taskId task isFound
      exact ⟨rfl, rfl, rfl, rfl, rfl, rfl, by simp [pendingEffects, isStarted]⟩
    · cases isStep

/-- Returning a handler is simulated by no L label. -/
theorem dex_workerReturn_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (taskId : Nat) (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig (.workerReturn taskId) =
      some (dexEvents, dexNext)) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  simp only [Dex.system, Dex.step] at isStep
  split at isStep
  · cases isStep
  · rename_i task isFound
    split at isStep
    · rename_i response isRunning
      cases isStep
      refine ⟨[], lConfig, Nat.zero_le 1, rfl, ?_⟩
      apply wp4_related_of_task_updated program lConfig dexConfig state.isRelated
        state.tasksDistinct taskId task isFound
      exact ⟨rfl, rfl, rfl, rfl, rfl, rfl, by simp [pendingEffects, isRunning]⟩
    · cases isStep

/-! ## After the close -/

/-- A task that is not current exists only after the close. -/
theorem wp4_closed_of_stale_task (program : Program) (lConfig : L.Config)
    (config : Dex.Config signature) (isRelated : Related program lConfig config)
    (task : Dex.Task signature) (isMember : task ∈ config.tasks)
    (isStale : config.isTaskCurrent task = false) :
    ∃ run status, config.run = some run ∧ run.status = .closed status := by
  cases hasRun : config.run with
  | none =>
    rw [isRelated.notStarted hasRun] at isMember
    cases isMember
  | some run =>
    cases isStatus : run.status with
    | running =>
      rw [(isRelated.running run hasRun isStatus).tasksAreCurrent task isMember] at isStale
      cases isStale
    | closed status => exact ⟨run, status, rfl, isStatus⟩

/-- After the close, the relation holds again when the L side changed only
its late emits, the Dex side only its tasks, and the late emits are still the
effects the tasks hold. -/
theorem wp4_related_closed (program : Program) (lConfig lNext : L.Config)
    (config next : Dex.Config signature) (run : Dex.Run signature) (status : Dex.CloseStatus)
    (isRelated : Related program lConfig config) (hasRun : config.run = some run)
    (isClosed : run.status = .closed status)
    (sameLClock : lNext.clock = lConfig.clock) (sameStamp : lNext.lastStampAt = lConfig.lastStampAt)
    (sameStatus : lNext.status = lConfig.status)
    (sameClock : next.clock = config.clock) (sameCommit : next.lastCommitAt = config.lastCommitAt)
    (sameRun : next.run = config.run)
    (backward : ∀ changed, changed ∈ next.tasks →
      ∃ task, task ∈ config.tasks ∧ changed.id = task.id ∧ changed.step = task.step ∧
        changed.method = task.method)
    (lateEmits : (next.tasks.flatMap (pendingEffects ⟦program⟧)).Perm lNext.lateEmits) :
    Related program lNext next :=
  { clock := sameLClock.trans (isRelated.clock.trans sameClock.symm)
    stamp := sameStamp.trans (isRelated.stamp.trans sameCommit.symm)
    status := by rw [sameRun, sameStatus]; exact isRelated.status
    notStarted := fun hasNoRun => by
      rw [sameRun, hasRun] at hasNoRun
      cases hasNoRun
    running := fun nextRun hasNextRun isRunning => by
      rw [sameRun, hasRun] at hasNextRun
      cases hasNextRun
      rw [isClosed] at isRunning
      cases isRunning
    closed := fun nextRun nextStatus hasNextRun isNextClosed => by
      rw [sameRun, hasRun] at hasNextRun
      cases hasNextRun
      have related := isRelated.closed run status hasRun isClosed
      exact
        { lateEmits := lateEmits
          tasksAreStale := fun changed isMember => by
            obtain ⟨task, isTaskMember, sameId, sameStep, sameMethod⟩ := backward changed isMember
            have isStale := related.tasksAreStale task isTaskMember
            simp only [Dex.Config.isTaskCurrent, sameRun, sameId, sameStep, sameMethod] at isStale ⊢
            exact isStale
          noInvocation := related.noInvocation
          noTimer := related.noTimer } }

/-- The effects the tasks hold are those of one task and those of the others. -/
theorem wp4_perm_effects_of_found (program : Program) (config : Dex.Config signature)
    (tasksDistinct : TasksAreDistinct config) (taskId : Nat) (task : Dex.Task signature)
    (isFound : config.findTask? taskId = some task) :
    (config.tasks.flatMap (pendingEffects ⟦program⟧)).Perm
      (pendingEffects ⟦program⟧ task ++
        (config.removeTask taskId).tasks.flatMap (pendingEffects ⟦program⟧)) := by
  obtain ⟨isMember, hasId⟩ := mem_of_findTask? config taskId task isFound
  have isPerm := wp4_perm_cons_filter config.tasks task tasksDistinct.1 isMember
  rw [hasId] at isPerm
  have isFlat := List.Perm.flatMap_right (pendingEffects ⟦program⟧) isPerm
  simpa [Dex.Config.removeTask, List.flatMap_cons] using isFlat

/-- The same after one task was changed. -/
theorem wp4_perm_effects_of_updated (program : Program) (config : Dex.Config signature)
    (tasksDistinct : TasksAreDistinct config) (taskId : Nat) (task : Dex.Task signature)
    (isFound : config.findTask? taskId = some task)
    (change : Dex.Task signature → Dex.Task signature) :
    ((config.updateTask taskId change).tasks.flatMap (pendingEffects ⟦program⟧)).Perm
      (pendingEffects ⟦program⟧ (change task) ++
        (config.removeTask taskId).tasks.flatMap (pendingEffects ⟦program⟧)) := by
  obtain ⟨isMember, hasId⟩ := mem_of_findTask? config taskId task isFound
  have isPerm := wp4_perm_cons_filter config.tasks task tasksDistinct.1 isMember
  rw [hasId] at isPerm
  have keepsOthers : ∀ candidate,
      candidate ∈ config.tasks.filter (fun candidate => !decide (candidate.id = taskId)) →
      (if candidate.id = taskId then change candidate else candidate) = id candidate := by
    intro candidate isCandidateMember
    have differs : candidate.id ≠ taskId := by simpa using (List.mem_filter.mp isCandidateMember).2
    simp [differs]
  have isMapped := isPerm.map (fun candidate =>
    if candidate.id = taskId then change candidate else candidate)
  rw [List.map_cons, List.map_congr_left keepsOthers, List.map_id] at isMapped
  simp only [hasId, ↓reduceIte] at isMapped
  have isFlat := List.Perm.flatMap_right (pendingEffects ⟦program⟧) isMapped
  simpa [Dex.Config.updateTask, Dex.Config.removeTask, List.flatMap_cons] using isFlat

/-- Dropping a task that is not current is simulated by no L label when the
task holds no effect, and by the drop of a late emit when it holds one. -/
theorem wp4_removeTask_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (taskId : Nat) (task : Dex.Task signature)
    (isFound : dexConfig.findTask? taskId = some task)
    (isStale : dexConfig.isTaskCurrent task = false)
    (holdsAtMostOne : (pendingEffects ⟦program⟧ task).length ≤ 1) :
    IsSimulatedByL program lConfig [] (dexConfig.removeTask taskId) := by
  obtain ⟨isMember, _⟩ := mem_of_findTask? dexConfig taskId task isFound
  obtain ⟨run, status, hasRun, isClosed⟩ :=
    wp4_closed_of_stale_task program lConfig dexConfig state.isRelated task isMember isStale
  have related := state.isRelated.closed run status hasRun isClosed
  have isPerm := (wp4_perm_effects_of_found program dexConfig state.tasksDistinct taskId task
    isFound).symm.trans related.lateEmits
  have backward : ∀ changed, changed ∈ (dexConfig.removeTask taskId).tasks →
      ∃ original, original ∈ dexConfig.tasks ∧ changed.id = original.id ∧
        changed.step = original.step ∧ changed.method = original.method :=
    fun changed isChangedMember => ⟨changed, (List.mem_filter.mp isChangedMember).1, rfl, rfl, rfl⟩
  generalize pendingEffects ⟦program⟧ task = effects at holdsAtMostOne isPerm
  cases effects with
  | nil =>
    refine ⟨[], lConfig, Nat.zero_le 1, rfl, ?_⟩
    exact wp4_related_closed program lConfig lConfig dexConfig _ run status state.isRelated hasRun
      isClosed rfl rfl rfl rfl rfl rfl backward (by simpa using isPerm)
  | cons effect laterEffects =>
    cases laterEffects with
    | nil =>
      have isLateMember : effect ∈ lConfig.lateEmits := isPerm.mem_iff.mp (by simp)
      obtain ⟨index, isAtIndex, isLatePerm⟩ :=
        wp4_exists_removeAt lConfig.lateEmits effect isLateMember
      have isRest := List.Perm.cons_inv
        ((by simpa using isPerm : (effect :: _).Perm lConfig.lateEmits).trans isLatePerm)
      have lClosed := Related.status_of_closed_run program lConfig dexConfig state.isRelated run status hasRun
        isClosed
      refine ⟨[.lateDrop index], { lConfig with lateEmits := removeAt lConfig.lateEmits index },
        Nat.le_refl 1, ?_, ?_⟩
      · simp [TransitionSystem.exec, L.system, L.step, lClosed, isAtIndex]
      · exact wp4_related_closed program lConfig _ dexConfig _ run status state.isRelated hasRun
          isClosed rfl rfl rfl rfl rfl rfl backward isRest
    | cons second _ => simp at holdsAtMostOne

/-- [STEP-COMPLETE-STALE] is simulated by no L label: a `Returned` task holds no effect. -/
theorem dex_stepCompleteStale_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (taskId : Nat) (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig (.stepCompleteStale taskId) =
      some (dexEvents, dexNext)) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  simp only [Dex.system, Dex.step] at isStep
  split at isStep
  · cases isStep
  · rename_i task isFound
    split at isStep
    · rename_i response isReturned
      split at isStep
      · cases isStep
      · rename_i isNotCurrent
        cases isStep
        exact wp4_removeTask_simulated program lConfig dexConfig state taskId task isFound
          (by simpa using isNotCurrent) (by simp [pendingEffects, isReturned])
    · cases isStep

/-- [WORKER-ABANDON] is simulated by no L label or by the drop of a late
emit, provided no task holds more than one effect
(`IsSimulationState.holdsAtMostOneEffect`). -/
theorem dex_workerAbandon_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (holdsAtMostOne : ∀ task, task ∈ dexConfig.tasks →
      (pendingEffects ⟦program⟧ task).length ≤ 1)
    (taskId : Nat) (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig (.workerAbandon taskId) =
      some (dexEvents, dexNext)) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  simp only [Dex.system, Dex.step] at isStep
  split at isStep
  · cases isStep
  · rename_i task isFound
    split at isStep
    · cases isStep
    · rename_i isNotCurrent
      cases isStep
      exact wp4_removeTask_simulated program lConfig dexConfig state taskId task isFound
        (by simpa using isNotCurrent)
        (holdsAtMostOne task (mem_of_findTask? dexConfig taskId task isFound).1)

/-- After the close, [WORKER-EFFECT] is simulated by the delivery of a late emit. -/
theorem dex_workerEffect_closed_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (run : Dex.Run signature) (status : Dex.CloseStatus) (hasRun : dexConfig.run = some run)
    (isClosed : run.status = .closed status)
    (taskId : Nat) (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig (.workerEffect taskId) =
      some (dexEvents, dexNext)) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  simp only [Dex.system, Dex.step] at isStep
  split at isStep
  · cases isStep
  · rename_i task isFound
    split at isStep
    · rename_i effect laterEffects response isRunning
      cases isStep
      have related := state.isRelated.closed run status hasRun isClosed
      have beforePerm := (wp4_perm_effects_of_found program dexConfig state.tasksDistinct taskId
        task isFound).symm.trans related.lateEmits
      have isEffects : pendingEffects ⟦program⟧ task = effect :: laterEffects := by
        simp [pendingEffects, isRunning]
      rw [isEffects] at beforePerm
      have isLateMember : effect ∈ lConfig.lateEmits := beforePerm.mem_iff.mp (by simp)
      obtain ⟨index, isAtIndex, isLatePerm⟩ :=
        wp4_exists_removeAt lConfig.lateEmits effect isLateMember
      have afterPerm := wp4_perm_effects_of_updated program dexConfig state.tasksDistinct taskId
        task isFound (fun current => { current with phase := .running laterEffects response })
      have isRest := (by simpa [pendingEffects] using afterPerm :
          List.Perm _ (laterEffects ++ _)).trans (List.Perm.cons_inv
        ((by simpa using beforePerm : (effect :: _).Perm lConfig.lateEmits).trans isLatePerm))
      have lClosed := Related.status_of_closed_run program lConfig dexConfig state.isRelated run status hasRun
        isClosed
      have backward : ∀ changed, changed ∈ (dexConfig.updateTask taskId (fun current =>
            { current with phase := .running laterEffects response })).tasks →
          ∃ original, original ∈ dexConfig.tasks ∧ changed.id = original.id ∧
            changed.step = original.step ∧ changed.method = original.method := by
        intro changed isChangedMember
        obtain ⟨original, isOriginalMember, isChanged⟩ :=
          mem_updateTask dexConfig taskId _ changed isChangedMember
        refine ⟨original, isOriginalMember, ?_⟩
        by_cases hasId : original.id = taskId <;> simp [isChanged, hasId]
      refine ⟨[.lateDeliver index], { lConfig with lateEmits := removeAt lConfig.lateEmits index },
        Nat.le_refl 1, ?_, ?_⟩
      · simp [TransitionSystem.exec, L.system, L.step, lClosed, isAtIndex, project]
      · exact wp4_related_closed program lConfig _ dexConfig _ run status state.isRelated hasRun
          isClosed rfl rfl rfl rfl rfl rfl backward isRest
    · cases isStep

/-- After the close, a publication is rejected on both sides. -/
theorem dex_publish_closed_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (run : Dex.Run signature) (status : Dex.CloseStatus) (hasRun : dexConfig.run = some run)
    (isClosed : run.status = .closed status)
    (chan : Chan) (message : DexVal)
    (isAdmissible : IsAdmissibleAction dexConfig (.request (.publish chan message)))
    (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig (.request (.publish chan message)) =
      some (dexEvents, dexNext)) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  obtain ⟨_, externalChan, literal, isChan, isMessage⟩ := isAdmissible
  subst isChan
  subst isMessage
  simp only [Dex.system, Dex.step, hasRun, isClosed] at isStep
  cases isStep
  have lClosed := Related.status_of_closed_run program lConfig dexConfig state.isRelated run status hasRun isClosed
  refine ⟨[.request (.input externalChan literal)], lConfig, Nat.le_refl 1, ?_, state.isRelated⟩
  simp [TransitionSystem.exec, L.system, L.step, lClosed, project]

/-! ## The clock -/

/-- [TICK] is simulated by an idle tick of the same duration. -/
theorem dex_tick_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (duration : Nat) (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig (.tick duration) = some (dexEvents, dexNext)) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  simp only [Dex.system, Dex.step] at isStep
  split at isStep
  · rename_i isPositive
    cases isStep
    refine ⟨[.tick duration], { lConfig with clock := lConfig.clock + duration }, Nat.le_refl 1,
      ?_, ?_⟩
    · simp [TransitionSystem.exec, L.system, L.step, isPositive]
    · exact
        { clock := by simp [state.isRelated.clock]
          stamp := state.isRelated.stamp
          status := state.isRelated.status
          notStarted := state.isRelated.notStarted
          running := fun run hasRun isRunning =>
            let related := state.isRelated.running run hasRun isRunning
            { related with tasksAreCurrent := related.tasksAreCurrent }
          closed := fun run status hasRun isClosed =>
            let related := state.isRelated.closed run status hasRun isClosed
            { related with tasksAreStale := related.tasksAreStale } }
  · cases isStep

end DexExpressiveness
