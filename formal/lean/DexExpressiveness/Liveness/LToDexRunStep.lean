import DexExpressiveness.Liveness.Silent
import DexExpressiveness.Liveness.LToDexRunDex

/-!
# Liveness, work package L5: one block of the Dex run of an L run

One L step is followed by `fairActions`: the simulating actions of the label
(`l_step_simulatedByActions`), then the [WORKER-ABANDON] of every canceled
task that holds no effect (`cleanupActions`). This file proves what one such
block does (`fair_step`): it is enabled, has the events of the L step, and
ends in a simulation state that is settled again: no canceled task without an
effect is left, and no task is `Returned`.

`dexBlocksOfLRun` repeats the block along an L run. The file also gives the
shape of a block that `Liveness/LToDexRun.lean` needs for the word and the
marker: the block of a clock tick is the clock tick, and the
block of any other label is not empty and holds progress steps only.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## The cleanup -/

/-- Whether the cleanup drops a task: its invocation was canceled and it
holds no effect. -/
def isCleanedUp (program : Program) (config : Dex.Config signature)
    (task : Dex.Task signature) : Bool :=
  !config.isTaskCurrent task && (pendingEffects ⟦program⟧ task).isEmpty

/-- The configuration after the cleanup: the tasks it drops are gone. -/
def settledConfig (program : Program) (config : Dex.Config signature) : Dex.Config signature :=
  { config with tasks := config.tasks.filter (fun task => !isCleanedUp program config task) }

/-- [WORKER-ABANDON] of tasks with distinct IDs that are not current removes them. -/
theorem exec_workerAbandon (flow : Dex.FlowDef signature) (taskIds : List Nat) :
    ∀ config : Dex.Config signature, taskIds.Nodup →
      (∀ taskId, taskId ∈ taskIds →
        ∃ task, config.findTask? taskId = some task ∧ config.isTaskCurrent task = false) →
      (Dex.system flow).exec config (taskIds.map Dex.Action.workerAbandon) =
        some ([], { config with
          tasks := config.tasks.filter (fun task => !taskIds.contains task.id) }) := by
  induction taskIds with
  | nil =>
    intro config _ _
    have sameTasks : config.tasks.filter (fun task => !([] : List Nat).contains task.id) =
        config.tasks := List.filter_eq_self.mpr (fun _ _ => rfl)
    simp only [List.map_nil, TransitionSystem.exec, sameTasks]
  | cons taskId laterIds inductionHypothesis =>
    intro config idsNodup areStale
    obtain ⟨isFresh, laterNodup⟩ := List.nodup_cons.mp idsNodup
    obtain ⟨task, isFound, isStale⟩ := areStale taskId (List.mem_cons_self ..)
    have isStep : (Dex.system flow).step config (.workerAbandon taskId) =
        some ([], config.removeTask taskId) := by
      simp [Dex.system, Dex.step, isFound, isStale]
    have laterStale : ∀ laterId, laterId ∈ laterIds →
        ∃ later, (config.removeTask taskId).findTask? laterId = some later ∧
          (config.removeTask taskId).isTaskCurrent later = false := by
      intro laterId isLaterMember
      obtain ⟨later, isLaterFound, isLaterStale⟩ :=
        areStale laterId (List.mem_cons_of_mem _ isLaterMember)
      refine ⟨later, ?_, isLaterStale⟩
      have differs : laterId ≠ taskId := fun isSame => isFresh (isSame ▸ isLaterMember)
      rw [← isLaterFound]
      simp only [Dex.Config.findTask?, Dex.Config.removeTask, List.find?_filter]
      congr 1
      funext candidate
      by_cases isLater : candidate.id = laterId
      · simp [isLater, differs]
      · simp [isLater]
    have isLater := inductionHypothesis (config.removeTask taskId) laterNodup laterStale
    have sameTasks : (config.removeTask taskId).tasks.filter
          (fun candidate => !laterIds.contains candidate.id) =
        config.tasks.filter (fun candidate => !(taskId :: laterIds).contains candidate.id) := by
      simp only [Dex.Config.removeTask, List.filter_filter]
      apply List.filter_congr
      intro candidate _
      by_cases isHead : candidate.id = taskId <;> simp [isHead]
    simp only [List.map_cons, TransitionSystem.exec, isStep, isLater, List.append_nil]
    rw [sameTasks]
    rfl

/-- The cleanup is enabled, silent, and ends in the settled configuration. -/
theorem exec_cleanupActions (program : Program) (config : Dex.Config signature)
    (tasksDistinct : TasksAreDistinct config) :
    (Dex.system ⟦program⟧).exec config (cleanupActions program config) =
      some ([], settledConfig program config) := by
  have isActions : cleanupActions program config =
      ((config.tasks.filter (isCleanedUp program config)).map (fun task => task.id)).map
        Dex.Action.workerAbandon := by
    simp only [cleanupActions, List.map_map]
    rfl
  have idsNodup : ((config.tasks.filter (isCleanedUp program config)).map
      (fun task => task.id)).Nodup :=
    tasksDistinct.1.sublist (List.Sublist.map _ List.filter_sublist)
  rw [isActions, exec_workerAbandon ⟦program⟧ _ config idsNodup]
  · have sameTasks : config.tasks.filter (fun task =>
          !((config.tasks.filter (isCleanedUp program config)).map
            (fun task => task.id)).contains task.id) =
        config.tasks.filter (fun task => !isCleanedUp program config task) := by
      apply List.filter_congr
      intro task isMember
      congr 1
      cases dropCase : isCleanedUp program config task with
      | true =>
        simp only [List.contains_iff_mem, List.mem_map, List.mem_filter]
        exact ⟨task, ⟨isMember, dropCase⟩, rfl⟩
      | false =>
        apply Bool.eq_false_iff.mpr
        intro isListed
        simp only [List.contains_iff_mem, List.mem_map, List.mem_filter] at isListed
        obtain ⟨dropped, ⟨isDroppedMember, isDropped⟩, sameId⟩ := isListed
        have isSame : dropped = task :=
          eq_of_nodup_map (fun candidate => candidate.id) config.tasks tasksDistinct.1
            isDroppedMember isMember sameId
        rw [isSame, dropCase] at isDropped
        cases isDropped
    simp only [settledConfig, sameTasks]
  · intro taskId isListed
    obtain ⟨task, isDroppedMember, hasId⟩ := List.mem_map.mp isListed
    obtain ⟨isMember, isDropped⟩ := List.mem_filter.mp isDroppedMember
    refine ⟨task, hasId ▸ findTask?_of_mem tasksDistinct isMember, ?_⟩
    simp only [isCleanedUp, Bool.and_eq_true, Bool.not_eq_true'] at isDropped
    exact isDropped.1

/-- The cleanup drops no effect. -/
theorem flatMap_pendingEffects_settled (program : Program) (config : Dex.Config signature)
    (tasks : List (Dex.Task signature)) :
    (tasks.filter (fun task => !isCleanedUp program config task)).flatMap
        (pendingEffects ⟦program⟧) =
      tasks.flatMap (pendingEffects ⟦program⟧) := by
  induction tasks with
  | nil => rfl
  | cons task laterTasks inductionHypothesis =>
    cases dropCase : isCleanedUp program config task with
    | true =>
      have noEffect : pendingEffects ⟦program⟧ task = [] := by
        simp only [isCleanedUp, Bool.and_eq_true] at dropCase
        exact List.isEmpty_iff.mp dropCase.2
      simp [dropCase, noEffect, inductionHypothesis]
    | false => simp [dropCase, inductionHypothesis]

/-- The cleanup keeps the relation: it drops tasks only after the close, and
only tasks that hold no late emit. -/
theorem related_settledConfig (program : Program) (lConfig : L.Config)
    (config : Dex.Config signature) (isRelated : Related program lConfig config) :
    Related program lConfig (settledConfig program config) := by
  by_cases hasDropped : ∃ task, task ∈ config.tasks ∧ isCleanedUp program config task = true
  · obtain ⟨task, isMember, isDropped⟩ := hasDropped
    simp only [isCleanedUp, Bool.and_eq_true, Bool.not_eq_true'] at isDropped
    obtain ⟨run, status, hasRun, isClosed⟩ :=
      wp4_closed_of_stale_task program lConfig config isRelated task isMember isDropped.1
    have related := isRelated.closed run status hasRun isClosed
    refine wp4_related_closed program lConfig lConfig config (settledConfig program config) run
      status isRelated hasRun isClosed rfl rfl rfl rfl rfl rfl ?_ ?_
    · intro changed isChangedMember
      exact ⟨changed, (List.mem_filter.mp isChangedMember).1, rfl, rfl, rfl⟩
    · have sameEffects := flatMap_pendingEffects_settled program config config.tasks
      simp only [settledConfig]
      rw [sameEffects]
      exact related.lateEmits
  · have sameTasks : config.tasks.filter (fun task => !isCleanedUp program config task) =
        config.tasks := by
      apply List.filter_eq_self.mpr
      intro task isMember
      cases dropCase : isCleanedUp program config task with
      | true => exact absurd ⟨task, isMember, dropCase⟩ hasDropped
      | false => rfl
    have isSame : settledConfig program config = config := by
      simp only [settledConfig, sameTasks]
    rw [isSame]
    exact isRelated

/-- After the cleanup no canceled task without an effect is left. -/
theorem cleanupActions_settledConfig (program : Program) (config : Dex.Config signature) :
    cleanupActions program (settledConfig program config) = [] := by
  have noTask : (settledConfig program config).tasks.filter
      (isCleanedUp program (settledConfig program config)) = [] := by
    apply List.filter_eq_nil_iff.mpr
    intro task isMember
    have isKept := (List.mem_filter.mp isMember).2
    intro isDropped
    have isSame : isCleanedUp program (settledConfig program config) task =
        isCleanedUp program config task := rfl
    rw [isSame] at isDropped
    simp [isDropped] at isKept
  show ((settledConfig program config).tasks.filter
    (isCleanedUp program (settledConfig program config))).map _ = []
  rw [noTask]
  rfl

/-! ## The `Returned` tasks of a block -/

/-- No task is `Returned`, in the form of `DexFollowsL.noReturnedTask`. -/
theorem hasNoReturnedTaskExcept_none_iff (config : Dex.Config signature) :
    config.HasNoReturnedTaskExcept none ↔
      ∀ task, task ∈ config.tasks → ∀ response, task.phase ≠ .returned response := by
  constructor
  · intro holds task isMember response isReturned
    cases holds task isMember response isReturned
  · intro noReturned task isMember response isReturned
    exact absurd isReturned (noReturned task isMember response)

/-- The simulating actions of a label return no handler, or they are the
return of one task followed by its Complete. -/
theorem simulatingActions_returns (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (label : L.Label) :
    (∀ action, action ∈ simulatingActions program lConfig dexConfig label →
      ∀ taskId, action ≠ .workerReturn taskId) ∨
    ∃ task, simulatingActions program lConfig dexConfig label =
      returnTaskActions task ++ [.stepComplete task.id] := by
  have runIsQuiet : ∀ (task : Dex.Task signature) (last : Dex.Action signature),
      (∀ taskId, last ≠ .workerReturn taskId) →
      ∀ action, action ∈ runTaskActions task ++ [last] →
        ∀ taskId, action ≠ .workerReturn taskId := by
    intro task last lastIsQuiet action isMember taskId
    unfold runTaskActions at isMember
    split at isMember <;> simp only [List.cons_append, List.nil_append, List.mem_cons,
      List.not_mem_nil, or_false] at isMember
    · rcases isMember with isRun | isLast
      · rw [isRun]
        nofun
      · rw [isLast]
        exact lastIsQuiet taskId
    · rw [isMember]
      exact lastIsQuiet taskId
  cases label with
  | tick duration => exact Or.inl (by simp [simulatingActions])
  | request request => exact Or.inl (by simp [simulatingActions])
  | fire count => exact Or.inl (by simp [simulatingActions])
  | thread thread =>
    simp only [simulatingActions]
    split
    · split
      all_goals first
        | exact Or.inr ⟨_, rfl⟩
        | exact Or.inl (runIsQuiet _ _ (fun _ => nofun))
        | exact Or.inl (by simp)
    · exact Or.inl (by simp)
  | lateDeliver index =>
    simp only [simulatingActions]
    split
    · exact Or.inl (by simp)
    · split
      · exact Or.inl (runIsQuiet _ _ (fun _ => nofun))
      · exact Or.inl (by simp)
  | lateDrop index =>
    simp only [simulatingActions]
    split
    · exact Or.inl (by simp)
    · split <;> exact Or.inl (by simp)

/-- The return of a task followed by its Complete leaves no `Returned` task. -/
theorem exec_returnThenComplete (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (task : Dex.Task signature) (events : List (Dex.Obs signature))
    (final : Dex.Config signature) (holds : config.HasNoReturnedTaskExcept none)
    (isExec : (Dex.system flow).exec config (returnTaskActions task ++ [.stepComplete task.id]) =
      some (events, final)) : final.HasNoReturnedTaskExcept none := by
  have complete : ∀ (middle : Dex.Config signature) (laterEvents : List (Dex.Obs signature)),
      middle.HasNoReturnedTaskExcept (some task.id) →
      (Dex.system flow).exec middle [.stepComplete task.id] = some (laterEvents, final) →
      final.HasNoReturnedTaskExcept none := by
    intro middle laterEvents middleHolds isLast
    obtain ⟨stepEvents, next, restEvents, isStep, isRest, _⟩ :=
      (Dex.system flow).exec_cons middle _ [] laterEvents final isLast
    simp only [TransitionSystem.exec] at isRest
    cases isRest
    exact middleHolds.stepComplete flow stepEvents _ isStep
  have weaken : config.HasNoReturnedTaskExcept (some task.id) := by
    intro candidate isMember response isReturned
    cases holds candidate isMember response isReturned
  unfold returnTaskActions at isExec
  split at isExec
  · obtain ⟨runEvents, afterRun, laterEvents, isRun, isLater, _⟩ :=
      (Dex.system flow).exec_cons config _ _ events final isExec
    obtain ⟨returnEvents, afterReturn, lastEvents, isReturn, isLast, _⟩ :=
      (Dex.system flow).exec_cons afterRun _ _ laterEvents final isLater
    exact complete afterReturn lastEvents
      ((holds.step flow (.workerRun task.id) runEvents afterRun isRun (fun _ => nofun)).workerReturn
        flow task.id
        returnEvents afterReturn isReturn) isLast
  · obtain ⟨returnEvents, afterReturn, lastEvents, isReturn, isLast, _⟩ :=
      (Dex.system flow).exec_cons config _ _ events final isExec
    exact complete afterReturn lastEvents
      (holds.workerReturn flow task.id returnEvents afterReturn isReturn) isLast
  · exact complete config events weaken isExec

/-- The simulating actions of a label leave no `Returned` task. -/
theorem exec_simulatingActions_noReturned (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (label : L.Label) (events : List (Dex.Obs signature))
    (final : Dex.Config signature) (holds : dexConfig.HasNoReturnedTaskExcept none)
    (isExec : (Dex.system ⟦program⟧).exec dexConfig
      (simulatingActions program lConfig dexConfig label) = some (events, final)) :
    final.HasNoReturnedTaskExcept none := by
  rcases simulatingActions_returns program lConfig dexConfig label with isQuiet | ⟨task, isShape⟩
  · exact Dex.Config.HasNoReturnedTaskExcept.exec ⟦program⟧ _ dexConfig events final holds isExec
      isQuiet
  · rw [isShape] at isExec
    exact exec_returnThenComplete ⟦program⟧ dexConfig task events final holds isExec

/-! ## One block -/

/-- The state between two blocks of the Dex run of an L run: a simulation
state with no canceled task without an effect and no `Returned` task. -/
structure IsSettledState (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) : Prop where
  /-- The configurations are a simulation state. -/
  isState : IsSimulationState program lConfig dexConfig
  /-- No canceled task without an effect is left. -/
  isSettled : cleanupActions program dexConfig = []
  /-- No task is `Returned`. -/
  noReturnedTask : dexConfig.HasNoReturnedTaskExcept none

/-- The initial configurations are settled. -/
theorem isSettledState_initial (program : Program) :
    IsSettledState program (L.system program).init (Dex.system ⟦program⟧).init :=
  { isState := isSimulationState_initial program
    isSettled := rfl
    noReturnedTask := fun task isMember => by cases isMember }

/-- The fair actions of a label are its simulating actions followed by the
cleanup of the configuration they end in. -/
theorem fairActions_eq (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (label : L.Label) (events : List (Dex.Obs signature))
    (middle : Dex.Config signature)
    (isExec : (Dex.system ⟦program⟧).exec dexConfig
      (simulatingActions program lConfig dexConfig label) = some (events, middle)) :
    fairActions program lConfig dexConfig label =
      simulatingActions program lConfig dexConfig label ++ cleanupActions program middle := by
  simp only [fairActions, isExec]

/-- One block: from a settled state, the fair actions of an enabled L label
are enabled, have the events of the L step after projection, and end in a
settled state. -/
theorem fair_step (program : Program) (lConfig : L.Config) (dexConfig : Dex.Config signature)
    (settled : IsSettledState program lConfig dexConfig) (label : L.Label)
    (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig label = some (lEvents, lNext)) :
    ∃ dexEvents dexNext,
      (Dex.system ⟦program⟧).exec dexConfig (fairActions program lConfig dexConfig label) =
        some (dexEvents, dexNext) ∧
      dexEvents.filterMap project = lEvents ∧
      IsSettledState program lNext dexNext := by
  have state := settled.isState
  obtain ⟨middleEvents, middle, isExec, sameEvents, middleRelated⟩ :=
    l_step_simulatedByActions program lConfig dexConfig state label lEvents lNext isStep
  have middleInvariants := dexInvariants_of_exec program _ dexConfig middleEvents middle
    state.dexInvariant state.tasksDistinct state.timersDistinct isExec
  have isCleanup := exec_cleanupActions program middle middleInvariants.2.1
  have finalInvariants := dexInvariants_of_exec program _ middle [] _ middleInvariants.1
    middleInvariants.2.1 middleInvariants.2.2 isCleanup
  have middleNoReturned := exec_simulatingActions_noReturned program lConfig dexConfig label
    middleEvents middle settled.noReturnedTask isExec
  refine ⟨middleEvents ++ [], settledConfig program middle, ?_, by simpa using sameEvents, ?_⟩
  · rw [fairActions_eq program lConfig dexConfig label middleEvents middle isExec]
    exact (Dex.system ⟦program⟧).exec_append dexConfig _ _ middleEvents [] middle _ isExec
      isCleanup
  · exact
      { isState :=
          { isRelated := related_settledConfig program lNext middle middleRelated
            lInvariant := L.Invariant.step program lConfig label lEvents lNext state.lInvariant
              isStep
            dexInvariant := finalInvariants.1
            tasksDistinct := finalInvariants.2.1
            timersDistinct := finalInvariants.2.2 }
        isSettled := cleanupActions_settledConfig program middle
        noReturnedTask := fun task isMember response isReturned =>
          middleNoReturned task (List.mem_filter.mp isMember).1 response isReturned }

/-! ## The blocks of an L run -/

/-- The Dex configuration before block `index` of the Dex run of `lRun`:
the fair actions of each L label, from the initial configuration. -/
def dexStateOfLRun (program : Program) (lRun : InfiniteRun L.Config L.Label) :
    Nat → Dex.Config signature
  | 0 => (Dex.system ⟦program⟧).init
  | index + 1 =>
    match (Dex.system ⟦program⟧).exec (dexStateOfLRun program lRun index)
        (fairActions program (lRun.states index) (dexStateOfLRun program lRun index)
          (lRun.labels index)) with
    | some (_, next) => next
    | none => dexStateOfLRun program lRun index

/-- The Dex run of `lRun` in blocks: block `index` is the fair actions of
the L label `index`. -/
def dexBlocksOfLRun (program : Program) (lRun : InfiniteRun L.Config L.Label) :
    BlockRun (Dex.Config signature) (Dex.Action signature) (Dex.Obs signature) :=
  { states := dexStateOfLRun program lRun
    blocks := fun index =>
      fairActions program (lRun.states index) (dexStateOfLRun program lRun index)
        (lRun.labels index)
    events := fun index =>
      match (Dex.system ⟦program⟧).exec (dexStateOfLRun program lRun index)
          (fairActions program (lRun.states index) (dexStateOfLRun program lRun index)
            (lRun.labels index)) with
      | some (events, _) => events
      | none => [] }

/-- The state before every block of the Dex run of an L run is settled. -/
theorem isSettledState_dexStateOfLRun (program : Program) (lRun : InfiniteRun L.Config L.Label)
    (isRun : (L.system program).IsRun lRun) (index : Nat) :
    IsSettledState program (lRun.states index) (dexStateOfLRun program lRun index) := by
  induction index with
  | zero =>
    rw [isRun.1]
    exact isSettledState_initial program
  | succ index inductionHypothesis =>
    obtain ⟨lEvents, isLStep⟩ := isRun.2 index
    obtain ⟨dexEvents, dexNext, isExec, _, nextSettled⟩ :=
      fair_step program _ _ inductionHypothesis _ lEvents _ isLStep
    have isNext : dexStateOfLRun program lRun (index + 1) = dexNext := by
      simp only [dexStateOfLRun, isExec]
    rw [isNext]
    exact nextSettled

/-- Block `index` of the Dex run of an L run is enabled, ends in the next
state, and has the events of the L step `index` after projection. -/
theorem dexBlocksOfLRun_block (program : Program) (lRun : InfiniteRun L.Config L.Label)
    (isRun : (L.system program).IsRun lRun) (index : Nat) :
    ∃ lEvents dexEvents,
      (L.system program).step (lRun.states index) (lRun.labels index) =
        some (lEvents, lRun.states (index + 1)) ∧
      (Dex.system ⟦program⟧).exec ((dexBlocksOfLRun program lRun).states index)
          ((dexBlocksOfLRun program lRun).blocks index) =
        some (dexEvents, (dexBlocksOfLRun program lRun).states (index + 1)) ∧
      (dexBlocksOfLRun program lRun).events index = dexEvents ∧
      dexEvents.filterMap project = lEvents := by
  obtain ⟨lEvents, isLStep⟩ := isRun.2 index
  obtain ⟨dexEvents, dexNext, isExec, sameEvents, _⟩ :=
    fair_step program _ _ (isSettledState_dexStateOfLRun program lRun isRun index) _ lEvents _
      isLStep
  have isNext : dexStateOfLRun program lRun (index + 1) = dexNext := by
    simp only [dexStateOfLRun, isExec]
  refine ⟨lEvents, dexEvents, isLStep, ?_, ?_, sameEvents⟩
  · simp only [dexBlocksOfLRun, isNext]
    exact isExec
  · simp only [dexBlocksOfLRun, isExec]

/-! ## The shape of a block -/

/-- The fair actions of a label are its simulating actions followed by
[WORKER-ABANDON] actions. -/
theorem fairActions_shape (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (label : L.Label) :
    ∃ dropActions, fairActions program lConfig dexConfig label =
        simulatingActions program lConfig dexConfig label ++ dropActions ∧
      ∀ action, action ∈ dropActions → ∃ taskId, action = .workerAbandon taskId := by
  simp only [fairActions]
  split
  · refine ⟨_, rfl, ?_⟩
    intro action isMember
    obtain ⟨task, _, isAction⟩ := List.mem_map.mp isMember
    exact ⟨task.id, isAction.symm⟩
  · exact ⟨[], by simp, fun action isMember => by cases isMember⟩

/-- The only request of a block is the encoded request of the L label. -/
theorem fairActions_requests (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (label : L.Label) :
    (fairActions program lConfig dexConfig label).filterMap (Dex.system ⟦program⟧).requestOf =
      ((L.system program).requestOf label).toList.map encodeRequest := by
  obtain ⟨dropActions, isShape, areDrops⟩ := fairActions_shape program lConfig dexConfig label
  have noRequest : dropActions.filterMap (Dex.system ⟦program⟧).requestOf = [] := by
    apply List.filterMap_eq_nil_iff.mpr
    intro action isMember
    obtain ⟨taskId, isAction⟩ := areDrops action isMember
    rw [isAction]
    rfl
  rw [isShape, List.filterMap_append, noRequest, List.append_nil,
    simulatingActions_requests]

/-- From a settled configuration, the block of a clock tick is the clock tick. -/
theorem fairActions_tick (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (isSettled : cleanupActions program dexConfig = [])
    (duration : Nat) :
    fairActions program lConfig dexConfig (.tick duration) = [.tick duration] := by
  have sameCleanup : cleanupActions program
      { dexConfig with clock := dexConfig.clock + duration } = cleanupActions program dexConfig :=
    rfl
  by_cases isPositive : 0 < duration
  · have isExec : (Dex.system ⟦program⟧).exec dexConfig
          (simulatingActions program lConfig dexConfig (.tick duration)) =
        some ([], { dexConfig with clock := dexConfig.clock + duration }) := by
      simp [simulatingActions, TransitionSystem.exec, Dex.system, Dex.step, isPositive]
    rw [fairActions_eq program lConfig dexConfig (.tick duration) [] _ isExec, sameCleanup,
      isSettled]
    rfl
  · have isExec : (Dex.system ⟦program⟧).exec dexConfig
          (simulatingActions program lConfig dexConfig (.tick duration)) = none := by
      simp [simulatingActions, TransitionSystem.exec, Dex.system, Dex.step, isPositive]
    simp only [fairActions, isExec]
    rfl

/-- The block of a progress step holds progress steps only. -/
theorem fairActions_isProgress (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (label : L.Label)
    (isProgress : (L.system program).isProgress label = true) :
    ∀ action, action ∈ fairActions program lConfig dexConfig label →
      (Dex.system ⟦program⟧).isProgress action = true := by
  have isNotTick : ∀ action : Dex.Action signature, (∀ duration, action ≠ .tick duration) →
      (Dex.system ⟦program⟧).isProgress action = true := by
    intro action differs
    cases action with
    | tick duration => exact absurd rfl (differs duration)
    | _ => rfl
  obtain ⟨dropActions, isShape, areDrops⟩ := fairActions_shape program lConfig dexConfig label
  intro action isMember
  rw [isShape] at isMember
  rcases List.mem_append.mp isMember with isSimulating | isDrop
  · apply isNotTick
    intro duration isTick
    subst isTick
    cases label with
    | tick tickDuration => cases isProgress
    | request request => simp [simulatingActions] at isSimulating
    | fire count => simp [simulatingActions] at isSimulating
    | thread thread =>
      simp only [simulatingActions] at isSimulating
      split at isSimulating
      · split at isSimulating <;>
          simp [runTaskActions, returnTaskActions] at isSimulating <;>
          (try split at isSimulating) <;> simp at isSimulating
      · cases isSimulating
    | lateDeliver index =>
      simp only [simulatingActions] at isSimulating
      split at isSimulating
      · cases isSimulating
      · split at isSimulating
        · simp [runTaskActions] at isSimulating
          split at isSimulating <;> simp at isSimulating
        · cases isSimulating
    | lateDrop index =>
      simp only [simulatingActions] at isSimulating
      split at isSimulating
      · cases isSimulating
      · split at isSimulating <;> simp at isSimulating
  · obtain ⟨taskId, isAction⟩ := areDrops action isDrop
    rw [isAction]
    rfl

/-- The simulating actions of an enabled label are not empty: from a
simulation state the two configurations fit. -/
theorem simulatingActions_ne_nil (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (label : L.Label) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig label = some (lEvents, lNext)) :
    simulatingActions program lConfig dexConfig label ≠ [] := by
  cases label with
  | tick duration => simp [simulatingActions]
  | request request => simp [simulatingActions]
  | fire count => simp [simulatingActions]
  | lateDeliver index =>
    simp only [L.system, L.step] at isStep
    split at isStep
    · rename_i effect isClosed isLate
      obtain ⟨run, status, hasRun, isRunClosed⟩ := state.isRelated.run_of_closed isClosed
      obtain ⟨task, isPicked, _, _⟩ :=
        (state.isRelated.closed run status hasRun isRunClosed).exists_lateTask isLate
      rw [simulatingActions_lateDeliver isLate isPicked]
      simp
    · cases isStep
  | lateDrop index =>
    simp only [L.system, L.step] at isStep
    split at isStep
    · rename_i effect isClosed isLate
      obtain ⟨run, status, hasRun, isRunClosed⟩ := state.isRelated.run_of_closed isClosed
      obtain ⟨task, isPicked, _, _⟩ :=
        (state.isRelated.closed run status hasRun isRunClosed).exists_lateTask isLate
      rw [simulatingActions_lateDrop isLate isPicked]
      simp
    · cases isStep
  | thread tid =>
    cases foundCase : lConfig.findThread? tid with
    | none => simp [L.system, L.step, foundCase] at isStep
    | some thread =>
      obtain ⟨isMember, hasTid⟩ := mem_of_findThread? foundCase
      obtain ⟨_, run, hasRun, isRunning⟩ := state.run_of_thread thread isMember
      have related := state.isRelated.running run hasRun isRunning
      have isNotHalted : thread.phase ≠ .halted := by
        intro isHalted
        simp [L.system, L.step, foundCase, stepThread, isHalted] at isStep
      obtain ⟨record, isRecord, isActive, carries, isMatch⟩ :=
        related.threadsHaveSteps thread isMember isNotHalted
      have isThread : threadOfInput record.input = tid :=
        (threadOfInput_of_mem state.lInvariant isMember carries).trans hasTid
      have begins : (thread.phase = .bootPending ∨ thread.phase = .ready ∨
          ∃ outcome, thread.phase = .woken outcome) →
          simulatingActions program lConfig dexConfig (.thread tid) ≠ [] := by
        intro isPending
        rw [simulatingActions_begin related hasRun foundCase record isRecord isActive isThread
          isPending]
        simp
      have completes : (thread.phase = .bootRunning ∨ (∃ loaded, thread.phase = .running loaded) ∨
          thread.phase = .emitted ∨ thread.phase = .waitStarted ∨
          ∃ outcome, thread.phase = .resuming outcome) →
          ∀ method, HasRunningTask dexConfig record method (fun _ => True) →
          simulatingActions program lConfig dexConfig (.thread tid) ≠ [] := by
        intro isCompleting method hasTask
        obtain ⟨task, isTask, hasInvocation, _⟩ := hasTask
        rw [simulatingActions_complete related hasRun state.tasksDistinct foundCase record
          isRecord isActive isThread method task isTask hasInvocation isCompleting]
        simp
      unfold PhaseMatchesStep at isMatch
      cases phaseCase : thread.phase with
      | bootPending => exact begins (Or.inl phaseCase)
      | ready => exact begins (Or.inr (Or.inl phaseCase))
      | woken outcome => exact begins (Or.inr (Or.inr ⟨outcome, phaseCase⟩))
      | emitting effect =>
        simp only [phaseCase] at isMatch
        obtain ⟨_, task, isTask, hasInvocation, _⟩ := isMatch
        rw [simulatingActions_deliver related hasRun state.tasksDistinct foundCase record
          isRecord isActive isThread _ task isTask hasInvocation effect phaseCase]
        simp
      | bootRunning =>
        simp only [phaseCase] at isMatch
        exact completes (Or.inl phaseCase) _ (hasRunningTask_imp (fun _ _ => trivial) isMatch.2)
      | running loaded =>
        simp only [phaseCase] at isMatch
        exact completes (Or.inr (Or.inl ⟨loaded, phaseCase⟩)) _
          (hasRunningTask_imp (fun _ _ => trivial) isMatch.2)
      | emitted =>
        simp only [phaseCase] at isMatch
        exact completes (Or.inr (Or.inr (Or.inl phaseCase))) _
          (hasRunningTask_imp (fun _ _ => trivial) isMatch.2)
      | waitStarted =>
        simp only [phaseCase] at isMatch
        exact completes (Or.inr (Or.inr (Or.inr (Or.inl phaseCase)))) _
          (hasRunningTask_imp (fun _ _ => trivial) isMatch.2)
      | resuming outcome =>
        simp only [phaseCase] at isMatch
        exact completes (Or.inr (Or.inr (Or.inr (Or.inr ⟨outcome, phaseCase⟩)))) _
          (hasRunningTask_imp (fun _ _ => trivial) isMatch.2)
      | blocked on since =>
        simp [L.system, L.step, foundCase, stepThread, phaseCase] at isStep
      | halted => exact absurd phaseCase isNotHalted

end DexExpressiveness
