import DexExpressiveness.Dex.TimerLemmas
import DexExpressiveness.Dex.Observation
import DexExpressiveness.Translation.Projection

/-!
# Invariants of translated Flows

Every configuration that a finite run of a translated Flow reaches satisfies
`ConfigInvariant`: its run satisfies `Dex.Run.Invariant` with "every armed
timer is the index-0 timer of a `Timed` Step", every response a task holds is
the one its handler computes, and the effects a task still holds are the last
of those its handler computes. A handler of a translated Flow computes at most
one effect, and none in WaitFor (`translate_compute_effects`), so a task holds
at most one effect.

Two more invariants are separate predicates: task IDs are unique
(`TasksAreDistinct`), and armed timers have distinct IDs (`TimersAreDistinct`).

The three invariants that the FIFO arguments of spec §8.3 rest on follow:
`noWaiterOnNonEmptyChannel` (spec §7 item 4), `registeredSeq_isInjective`
(spec Appendix A, assumption 11), and `armedTimers_areTimedSteps` (spec
Appendix A, assumption 26). Everything here is proved.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- A property that every transition keeps holds after every finite run from
a state where it holds. -/
theorem TransitionSystem.invariant_of_exec {State Label Event Request : Type}
    (system : TransitionSystem State Label Event Request) (Holds : State → Prop)
    (isKept : ∀ state label events next, Holds state →
      system.step state label = some (events, next) → Holds next)
    (labels : List Label) (state : State) (events : List Event) (final : State)
    (holdsFirst : Holds state) (isExec : system.exec state labels = some (events, final)) :
    Holds final := by
  induction labels generalizing state events with
  | nil =>
    simp only [TransitionSystem.exec] at isExec
    cases isExec
    exact holdsFirst
  | cons label laterLabels inductionHypothesis =>
    simp only [TransitionSystem.exec] at isExec
    split at isExec
    · cases isExec
    · rename_i stepEvents next isStep
      split at isExec
      · cases isExec
      · rename_i laterEvents reached isLater
        cases isExec
        exact inductionHypothesis next laterEvents
          (isKept state label stepEvents next holdsFirst isStep) isLater

/-- The index-0 timer of a `Timed` Step: the only timers a translated Flow
arms (spec §8.2). -/
def IsTimedTimer (timer : Dex.TimerId signature) : Prop :=
  timer.step.stepType = StepType.timed ∧ timer.index = 0

/-- The response a task holds is the one its handler computes, and the
effects it still holds are the last of those its handler computes (spec §4.4
[WORKER-EFFECT]: "Each step of this rule emits the head of `fx⃗`"). -/
def IsTaskComputed (program : Program) (task : Dex.Task signature) : Prop :=
  match task.phase with
  | .started => True
  | .running remainingEffects response =>
    response = (task.compute ⟦program⟧).2 ∧
    ∃ emittedEffects, (task.compute ⟦program⟧).1 = emittedEffects ++ remainingEffects
  | .returned response => response = (task.compute ⟦program⟧).2

/-- The invariant of the configurations of a translated Flow. -/
structure ConfigInvariant (program : Program) (config : Dex.Config signature) : Prop where
  /-- The run, if any, satisfies the run invariant. -/
  runInvariant : ∀ run, config.run = some run →
    Dex.Run.Invariant IsTimedTimer (run.seq + 1) run
  /-- Every task holds the response its handler computes. -/
  tasksComputed : ∀ task, task ∈ config.tasks → IsTaskComputed program task

/-- The wait of every WaitFor of a translated Flow is supported, and it has a
timer only in the `Timed` Step, at index 0 (spec §8.2: "Its WaitFor returns a
wait with exactly one timer, at index 0"). -/
theorem translate_waitFor_wait (program : Program) (stepType : StepType)
    (waitFor : Dex.WaitForFn signature)
    (isDefined : (⟦program⟧.stepDef stepType).waitFor = some waitFor)
    (context : Dex.Context signature) (input : Option DexVal)
    (snapshot : Dex.Snapshot signature) :
    Dex.IsSupportedWait (waitFor context input snapshot).2.wait ∧
    ∀ index, index < (waitFor context input snapshot).2.wait.timerSeconds.length →
      stepType = StepType.timed ∧ index = 0 := by
  have emptyIsSupported : Dex.IsSupportedWait (Dex.Wait.empty signature) := Or.inl rfl
  cases stepType with
  | start => simp [translate, startStepDef] at isDefined
  | point code =>
    simp only [translate, pointStepDef] at isDefined
    split at isDefined
    · cases isDefined
      dsimp only
      split
      · rename_i core
        cases requestCase : waitRequest core code with
        | none => exact ⟨emptyIsSupported, fun index isBelow => by simp [encodeWaitRequest, Dex.Wait.empty] at isBelow⟩
        | some request =>
          cases request <;> simp [encodeWaitRequest, Dex.IsSupportedWait]
      · exact ⟨emptyIsSupported, fun index isBelow => by simp [Dex.Wait.empty] at isBelow⟩
    · cases isDefined
  | timed =>
    simp only [translate, timedStepDef] at isDefined
    cases isDefined
    dsimp only
    split
    · split
      · refine ⟨Or.inr (Or.inl rfl), fun index isBelow => ⟨rfl, ?_⟩⟩
        simp at isBelow
        exact isBelow
      · refine ⟨Or.inr (Or.inl rfl), fun index isBelow => ⟨rfl, ?_⟩⟩
        simp at isBelow
        exact isBelow
      · exact ⟨emptyIsSupported, fun index isBelow => by simp [Dex.Wait.empty] at isBelow⟩
    · exact ⟨emptyIsSupported, fun index isBelow => by simp [Dex.Wait.empty] at isBelow⟩

/-- The same for the WaitFor response that a task computes. -/
theorem translate_compute_waitFor (program : Program) (task : Dex.Task signature)
    (response : Dex.WaitForResponse signature)
    (isComputed : (task.compute ⟦program⟧).2 = .waitFor response) :
    Dex.IsSupportedWait response.wait ∧
    ∀ index, index < response.wait.timerSeconds.length →
      IsTimedTimer { step := task.step, index := index } := by
  unfold Dex.Task.compute at isComputed
  split at isComputed
  · split at isComputed
    · rename_i waitFor isDefined
      have isResponse : (waitFor task.context task.input task.snapshot).2 = response := by
        simpa using isComputed
      obtain ⟨isSupported, timersAreTimed⟩ := translate_waitFor_wait program task.step.stepType
        waitFor isDefined task.context task.input task.snapshot
      rw [isResponse] at isSupported timersAreTimed
      exact ⟨isSupported, fun index isBelow => timersAreTimed index isBelow⟩
    · have isResponse : ({ effects := {}, wait := Dex.Wait.empty signature } :
          Dex.WaitForResponse signature) = response := by simpa using isComputed
      subst isResponse
      exact ⟨Or.inl rfl, fun index isBelow => by simp [Dex.Wait.empty] at isBelow⟩
  · simp at isComputed

/-- A WaitFor of a translated Flow emits no effect. -/
theorem translate_waitFor_effects (program : Program) (stepType : StepType)
    (waitFor : Dex.WaitForFn signature)
    (isDefined : (⟦program⟧.stepDef stepType).waitFor = some waitFor)
    (context : Dex.Context signature) (input : Option DexVal)
    (snapshot : Dex.Snapshot signature) : (waitFor context input snapshot).1 = [] := by
  cases stepType with
  | start => simp [translate, startStepDef] at isDefined
  | point code =>
    simp only [translate, pointStepDef] at isDefined
    split at isDefined
    · cases isDefined
      dsimp only
      split <;> rfl
    · cases isDefined
  | timed =>
    simp only [translate, timedStepDef] at isDefined
    cases isDefined
    dsimp only
    split
    · split <;> rfl
    · rfl

/-- An Execute of a translated Flow emits at most one effect (spec §8.2:
"Each `emit` is the only effect of its own Execute"). -/
theorem translate_execute_effects (program : Program) (stepType : StepType)
    (context : Dex.Context signature) (input : Option DexVal)
    (snapshot : Dex.Snapshot signature)
    (results : Option (Dex.ConditionResults signature)) :
    ((⟦program⟧.stepDef stepType).execute context input snapshot results).1.length ≤ 1 := by
  cases stepType with
  | start =>
    simp only [translate, startStepDef]
    split <;> simp
  | point code =>
    simp only [translate, pointStepDef]
    split
    · split
      · simp
      · split <;> simp
    · simp
  | timed =>
    simp only [translate, timedStepDef]
    split <;> simp

/-- The handler of a task of a translated Flow computes at most one effect,
and none for WaitFor. -/
theorem translate_compute_effects (program : Program) (task : Dex.Task signature) :
    (task.compute ⟦program⟧).1.length ≤ 1 ∧
    (task.method = .waitFor → (task.compute ⟦program⟧).1 = []) := by
  unfold Dex.Task.compute
  split
  · split
    · rename_i waitFor isDefined
      have hasNoEffect := translate_waitFor_effects program task.step.stepType waitFor isDefined
        task.context task.input task.snapshot
      simp [hasNoEffect]
    · simp
  · rename_i isMethod
    refine ⟨translate_execute_effects program _ _ _ _ _, fun isWaitFor => ?_⟩
    rw [isMethod] at isWaitFor
    cases isWaitFor

/-- A task of a translated Flow holds at most one effect that it has not
emitted, and a WaitFor task holds none. -/
theorem IsTaskComputed.remainingEffects {program : Program} {task : Dex.Task signature}
    (isComputed : IsTaskComputed program task) (remainingEffects : List Val)
    (response : Dex.Response signature)
    (isPhase : task.phase = .running remainingEffects response) :
    remainingEffects.length ≤ 1 ∧ (task.method = .waitFor → remainingEffects = []) := by
  simp only [IsTaskComputed, isPhase] at isComputed
  obtain ⟨_, emittedEffects, isEmitted⟩ := isComputed
  obtain ⟨isShort, isEmptyForWaitFor⟩ := translate_compute_effects program task
  rw [isEmitted] at isShort isEmptyForWaitFor
  refine ⟨by simp only [List.length_append] at isShort; omega, fun isWaitFor => ?_⟩
  exact (List.append_eq_nil_iff.mp (isEmptyForWaitFor isWaitFor)).2

/-- A task of the task list after an update is an old task or the changed one. -/
theorem mem_updateTask (config : Dex.Config signature) (taskId : Nat)
    (change : Dex.Task signature → Dex.Task signature) (task : Dex.Task signature)
    (isMember : task ∈ (config.updateTask taskId change).tasks) :
    ∃ original, original ∈ config.tasks ∧
      task = if original.id = taskId then change original else original := by
  simp only [Dex.Config.updateTask, List.mem_map] at isMember
  obtain ⟨original, isOriginalMember, isTask⟩ := isMember
  exact ⟨original, isOriginalMember, isTask.symm⟩

/-- What the task lookup finds is a task of the configuration. -/
theorem mem_of_findTask? (config : Dex.Config signature) (taskId : Nat)
    (task : Dex.Task signature) (isFound : config.findTask? taskId = some task) :
    task ∈ config.tasks ∧ task.id = taskId :=
  ⟨List.mem_of_find?_eq_some isFound, by simpa using List.find?_some isFound⟩

/-- Task IDs are unique and below the next fresh ID. -/
def TasksAreDistinct (config : Dex.Config signature) : Prop :=
  (config.tasks.map (fun task => task.id)).Nodup ∧
  ∀ task, task ∈ config.tasks → task.id < config.nextTaskId

/-- With unique task IDs, a task with the looked-up ID is the task found. -/
theorem eq_of_isFound (config : Dex.Config signature) (taskId : Nat)
    (task found : Dex.Task signature) (tasksDistinct : TasksAreDistinct config)
    (isMember : task ∈ config.tasks) (hasId : task.id = taskId)
    (isFound : config.findTask? taskId = some found) : task = found := by
  obtain ⟨isFoundMember, foundId⟩ := mem_of_findTask? config taskId found isFound
  exact eq_of_nodup_map (fun candidate => candidate.id) config.tasks tasksDistinct.1 isMember
    isFoundMember (hasId.trans foundId.symm)

/-- Changing a task without changing its ID keeps task IDs distinct. -/
theorem TasksAreDistinct.updateTask {config : Dex.Config signature}
    (tasksDistinct : TasksAreDistinct config) (taskId : Nat)
    (change : Dex.Task signature → Dex.Task signature)
    (keepsId : ∀ task, (change task).id = task.id) :
    TasksAreDistinct (config.updateTask taskId change) := by
  have sameIds : (config.updateTask taskId change).tasks.map (fun task => task.id) =
      config.tasks.map (fun task => task.id) := by
    simp only [Dex.Config.updateTask, List.map_map]
    apply List.map_congr_left
    intro task _
    by_cases isChanged : task.id = taskId <;> simp [isChanged, keepsId]
  refine ⟨by rw [sameIds]; exact tasksDistinct.1, ?_⟩
  intro task isMember
  obtain ⟨original, isOriginalMember, isTask⟩ := mem_updateTask _ _ _ _ isMember
  have sameId : task.id = original.id := by
    rw [isTask]
    by_cases isChanged : original.id = taskId <;> simp [isChanged, keepsId]
  rw [sameId]
  exact tasksDistinct.2 original isOriginalMember

/-- Removing a task keeps task IDs distinct. -/
theorem TasksAreDistinct.removeTask {config : Dex.Config signature}
    (tasksDistinct : TasksAreDistinct config) (taskId : Nat) :
    TasksAreDistinct (config.removeTask taskId) :=
  ⟨tasksDistinct.1.sublist (List.Sublist.map _ List.filter_sublist),
   fun task isMember => tasksDistinct.2 task (List.mem_filter.mp isMember).1⟩

/-- Every transition keeps task IDs distinct. -/
theorem TasksAreDistinct.step (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (action : Dex.Action signature) (events : List (Dex.Obs signature))
    (next : Dex.Config signature) (tasksDistinct : TasksAreDistinct config)
    (isStep : Dex.step flow config action = some (events, next)) : TasksAreDistinct next := by
  cases action with
  | tick duration =>
    simp only [Dex.step] at isStep
    split at isStep <;> cases isStep
    exact tasksDistinct
  | request request =>
    cases request with
    | startFlow stepType input =>
      simp only [Dex.step] at isStep
      split at isStep
      · cases isStep
      · split at isStep <;> cases isStep
        exact tasksDistinct
    | publish chan value =>
      simp only [Dex.step] at isStep
      split at isStep
      · cases isStep; exact tasksDistinct
      · split at isStep
        · cases isStep; exact tasksDistinct
        · split at isStep <;> cases isStep
          exact tasksDistinct
  | stepStart stepId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
      · split at isStep
        · split at isStep
          · cases isStep
            refine ⟨?_, ?_⟩
            · simp only [List.map_append, List.map_cons, List.map_nil]
              apply List.nodup_append.mpr
              refine ⟨tasksDistinct.1, by simp, ?_⟩
              intro existingId isExisting newId isNew
              obtain ⟨task, isMember, isId⟩ := List.mem_map.mp isExisting
              have isBelow := tasksDistinct.2 task isMember
              simp only [List.mem_singleton] at isNew
              omega
            · intro task isMember
              simp only [List.mem_append, List.mem_singleton] at isMember
              rcases isMember with isOld | isNew
              · exact Nat.lt_succ_of_lt (tasksDistinct.2 task isOld)
              · subst isNew
                exact Nat.lt_succ_self _
          · cases isStep
        · cases isStep
  | workerRun taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact tasksDistinct.updateTask taskId _ (fun _ => rfl)
  | workerEffect taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact tasksDistinct.updateTask taskId _ (fun _ => rfl)
  | workerReturn taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact tasksDistinct.updateTask taskId _ (fun _ => rfl)
  | workerAbandon taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact tasksDistinct.removeTask taskId
  | stepCompleteStale taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · split at isStep <;> cases isStep
        exact tasksDistinct.removeTask taskId
      · cases isStep
  | stepComplete taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · split at isStep
      · split at isStep <;> cases isStep
        · exact tasksDistinct.removeTask taskId
        · exact tasksDistinct.removeTask taskId
      · cases isStep
    · cases isStep
  | timerFire count =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact tasksDistinct

/-- Resolve `step ... = some (events, next)`: close the goal when the step is
`none`, and otherwise substitute the events and the next configuration. -/
macro "resolve_step " equation:ident : tactic =>
  `(tactic| first
    | (cases $equation:ident; done)
    | (injection $equation with pairEquation
       injection pairEquation with eventsEquation nextEquation
       subst eventsEquation
       subst nextEquation))

/-- Name the run of the next configuration after a commit. -/
macro "resolve_run" : tactic =>
  `(tactic| (intro after isAfter
             injection isAfter with isAfter
             subst isAfter))

/-- Every transition of a translated Flow keeps the configuration invariant. -/
theorem ConfigInvariant.step (program : Program) (config : Dex.Config signature)
    (action : Dex.Action signature) (events : List (Dex.Obs signature))
    (next : Dex.Config signature) (invariant : ConfigInvariant program config)
    (tasksDistinct : TasksAreDistinct config)
    (isStep : Dex.step ⟦program⟧ config action = some (events, next)) :
    ConfigInvariant program next := by
  cases action with
  | tick duration =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
      exact ⟨invariant.runInvariant, invariant.tasksComputed⟩
    · resolve_step isStep
  | request request =>
    cases request with
    | startFlow stepType input =>
      simp only [Dex.step] at isStep
      split at isStep
      · resolve_step isStep
      · split at isStep
        · resolve_step isStep
          refine ⟨?_, invariant.tasksComputed⟩
          resolve_run
          have bumpedInvariant := (Dex.Run.Invariant.empty IsTimedTimer
            ((Dex.Run.empty signature).seq + 1)).bumpSeq
          generalize ({ Dex.Run.empty signature with seq := (Dex.Run.empty signature).seq + 1 } :
            Dex.Run signature) = bumped at bumpedInvariant ⊢
          dsimp only
          exact (bumpedInvariant.newStep _ _ _ _).mono (Nat.le_succ _)
        · resolve_step isStep
    | publish chan value =>
      simp only [Dex.step] at isStep
      split at isStep
      · resolve_step isStep
        exact invariant
      · rename_i run hasRun
        split at isStep
        · resolve_step isStep
          exact invariant
        · split at isStep
          · resolve_step isStep
            refine ⟨?_, invariant.tasksComputed⟩
            resolve_run
            have bumpedInvariant := (invariant.runInvariant run hasRun).bumpSeq
            generalize ({ run with seq := run.seq + 1 } : Dex.Run signature) = bumped
              at bumpedInvariant ⊢
            dsimp only
            have isEffects := bumpedInvariant.applyEffects { publications := [(chan, value)] }
            have sameSeq := Dex.Run.seq_applyEffects bumped { publications := [(chan, value)] }
            rw [← sameSeq] at isEffects
            exact isEffects.mono (Nat.le_succ _)
          · resolve_step isStep
  | stepStart stepId =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
    · rename_i run hasRun
      split at isStep
      · resolve_step isStep
      · split at isStep
        · split at isStep
          · resolve_step isStep
            refine ⟨?_, ?_⟩
            · resolve_run
              have bumpedInvariant := (invariant.runInvariant run hasRun).bumpSeq
              generalize ({ run with seq := run.seq + 1 } : Dex.Run signature) = bumped
                at bumpedInvariant ⊢
              dsimp only
              refine (Dex.Run.Invariant.updateStep bumpedInvariant stepId _ ?_ ?_).mono
                (Nat.le_succ _)
              · intro candidate
                rfl
              · intro candidate isPhase
                exact ⟨isPhase, rfl⟩
            · intro task isMember
              simp only [List.mem_append, List.mem_singleton] at isMember
              rcases isMember with isOld | isNew
              · exact invariant.tasksComputed task isOld
              · subst isNew
                trivial
          · resolve_step isStep
        · resolve_step isStep
  | workerRun taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
    · rename_i found isFound
      split at isStep
      · resolve_step isStep
        refine ⟨invariant.runInvariant, ?_⟩
        intro task isMember
        obtain ⟨original, isOriginalMember, isTask⟩ := mem_updateTask _ _ _ _ isMember
        by_cases isChanged : original.id = taskId
        · rw [if_pos isChanged] at isTask
          subst isTask
          have isSame : original = found :=
            eq_of_isFound config taskId original found tasksDistinct isOriginalMember isChanged
              isFound
          subst isSame
          simp only [IsTaskComputed]
          exact ⟨rfl, [], rfl⟩
        · rw [if_neg isChanged] at isTask
          subst isTask
          exact invariant.tasksComputed task isOriginalMember
      · resolve_step isStep
  | workerEffect taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
    · rename_i found isFound
      split at isStep
      · rename_i effect laterEffects response isPhase
        resolve_step isStep
        refine ⟨invariant.runInvariant, ?_⟩
        intro task isMember
        obtain ⟨original, isOriginalMember, isTask⟩ := mem_updateTask _ _ _ _ isMember
        have wasComputed := invariant.tasksComputed original isOriginalMember
        by_cases isChanged : original.id = taskId
        · rw [if_pos isChanged] at isTask
          subst isTask
          have isSame : original = found :=
            eq_of_isFound config taskId original found tasksDistinct isOriginalMember isChanged
              isFound
          subst isSame
          simp only [IsTaskComputed, isPhase] at wasComputed
          obtain ⟨isResponse, emittedEffects, isEmitted⟩ := wasComputed
          simp only [IsTaskComputed]
          exact ⟨isResponse, emittedEffects ++ [effect], by
            rw [List.append_assoc]
            exact isEmitted⟩
        · rw [if_neg isChanged] at isTask
          subst isTask
          exact wasComputed
      · resolve_step isStep
  | workerReturn taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
    · rename_i found isFound
      split at isStep
      · rename_i response isPhase
        resolve_step isStep
        refine ⟨invariant.runInvariant, ?_⟩
        intro task isMember
        obtain ⟨original, isOriginalMember, isTask⟩ := mem_updateTask _ _ _ _ isMember
        have wasComputed := invariant.tasksComputed original isOriginalMember
        by_cases isChanged : original.id = taskId
        · rw [if_pos isChanged] at isTask
          subst isTask
          have isSame : original = found :=
            eq_of_isFound config taskId original found tasksDistinct isOriginalMember isChanged
              isFound
          subst isSame
          simp only [IsTaskComputed, isPhase] at wasComputed
          exact wasComputed.1
        · rw [if_neg isChanged] at isTask
          subst isTask
          exact wasComputed
      · resolve_step isStep
  | workerAbandon taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
    · split at isStep
      · resolve_step isStep
      · resolve_step isStep
        exact ⟨invariant.runInvariant, fun task isMember =>
          invariant.tasksComputed task (List.mem_filter.mp isMember).1⟩
  | stepCompleteStale taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
    · split at isStep
      · split at isStep
        · resolve_step isStep
        · resolve_step isStep
          exact ⟨invariant.runInvariant, fun task isMember =>
            invariant.tasksComputed task (List.mem_filter.mp isMember).1⟩
      · resolve_step isStep
  | stepComplete taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · rename_i run task hasRun isFound
      have tasksKept : ∀ other, other ∈ (config.removeTask taskId).tasks →
          IsTaskComputed program other := fun other isMember =>
        invariant.tasksComputed other (List.mem_filter.mp isMember).1
      have wasComputed :=
        invariant.tasksComputed task (mem_of_findTask? config taskId task isFound).1
      split at isStep
      · split at isStep
        · rename_i response isMethod isPhase
          resolve_step isStep
          refine ⟨?_, tasksKept⟩
          resolve_run
          have bumpedInvariant := (invariant.runInvariant run hasRun).bumpSeq
          generalize ({ run with seq := run.seq + 1 } : Dex.Run signature) = bumped
            at bumpedInvariant ⊢
          dsimp only
          simp only [IsTaskComputed, isPhase] at wasComputed
          obtain ⟨isSupported, timersAreTimed⟩ :=
            translate_compute_waitFor program task response wasComputed.symm
          have cleared := Dex.Run.Invariant.updateStep bumpedInvariant task.step
            (fun record => { record with invocation := none }) (fun _ => rfl)
            (fun _ isWaiting => ⟨isWaiting, rfl⟩)
          have afterEffects := cleared.applyEffects response.effects
          have sameSeq : ((bumped.updateStep task.step
              (fun record => { record with invocation := none })).applyEffects
              response.effects).seq = bumped.seq := Dex.Run.seq_applyEffects _ _
          rw [← sameSeq] at afterEffects
          have registered := afterEffects.registerWait task.step response.wait config.clock
            isSupported timersAreTimed
          rw [Dex.Run.seq_registerWait]
          exact registered
        · rename_i response isMethod isPhase
          resolve_step isStep
          refine ⟨?_, tasksKept⟩
          resolve_run
          have bumpedInvariant := (invariant.runInvariant run hasRun).bumpSeq
          generalize ({ run with seq := run.seq + 1 } : Dex.Run signature) = bumped
            at bumpedInvariant ⊢
          dsimp only
          have cleared := Dex.Run.Invariant.updateStep bumpedInvariant task.step
            (fun record => { record with invocation := none }) (fun _ => rfl)
            (fun _ isWaiting => ⟨isWaiting, rfl⟩)
          have decided := (cleared.applyEffects response.effects).applyDecision task.step
            response.decision
          rw [Dex.Run.seq_applyDecision, Dex.Run.seq_applyEffects]
          exact decided.mono (Nat.le_succ _)
        · resolve_step isStep
      · resolve_step isStep
    · resolve_step isStep
  | timerFire count =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
    · rename_i run hasRun
      split at isStep
      · resolve_step isStep
        refine ⟨?_, invariant.tasksComputed⟩
        resolve_run
        have bumpedInvariant := (invariant.runInvariant run hasRun).bumpSeq
        generalize ({ run with seq := run.seq + 1 } : Dex.Run signature) = bumped
          at bumpedInvariant ⊢
        dsimp only
        rw [Dex.Run.seq_foldl_fireTimer]
        exact (Dex.Run.invariant_foldl_fireTimer _ bumped bumpedInvariant).mono (Nat.le_succ _)
      · resolve_step isStep

/-- The initial configuration satisfies the invariant. -/
theorem ConfigInvariant.initial (program : Program) :
    ConfigInvariant program (Dex.system ⟦program⟧).init :=
  { runInvariant := fun run hasRun => by simp [Dex.system, Dex.Config.initial] at hasRun
    tasksComputed := fun task isMember => by
      simp [Dex.system, Dex.Config.initial] at isMember }

/-- Every configuration that a finite run reaches satisfies the invariant. -/
theorem ConfigInvariant.of_exec (program : Program) (actions : List (Dex.Action signature))
    (events : List (Dex.Obs signature)) (config : Dex.Config signature)
    (isExec : (Dex.system ⟦program⟧).exec (Dex.system ⟦program⟧).init actions =
      some (events, config)) :
    ConfigInvariant program config ∧ TasksAreDistinct config :=
  TransitionSystem.invariant_of_exec (Dex.system ⟦program⟧)
    (fun state => ConfigInvariant program state ∧ TasksAreDistinct state)
    (fun state label stepEvents next holds isStep =>
      ⟨ConfigInvariant.step program state label stepEvents next holds.1 holds.2 isStep,
       TasksAreDistinct.step ⟦program⟧ state label stepEvents next holds.2 isStep⟩)
    actions _ events config
    ⟨ConfigInvariant.initial program, List.nodup_nil, fun task isMember => by cases isMember⟩
    isExec

/-! ## Armed timers have distinct IDs -/

/-- The armed timers of the run, if any, have distinct IDs: `Tm` is a finite
map (spec §3.2). `Related.timers` reads the first binding of a timer ID and
`Due_f` reads every binding, so the simulation needs this. -/
def TimersAreDistinct (config : Dex.Config signature) : Prop :=
  ∀ run, config.run = some run → run.HasDistinctTimers

/-- Every transition keeps the timer IDs distinct. -/
theorem TimersAreDistinct.step (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (action : Dex.Action signature) (events : List (Dex.Obs signature))
    (next : Dex.Config signature) (timersDistinct : TimersAreDistinct config)
    (isStep : Dex.step flow config action = some (events, next)) : TimersAreDistinct next := by
  cases action with
  | tick duration =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
      exact timersDistinct
    · resolve_step isStep
  | request request =>
    cases request with
    | startFlow stepType input =>
      simp only [Dex.step] at isStep
      split at isStep
      · resolve_step isStep
      · split at isStep
        · resolve_step isStep
          resolve_run
          exact List.nodup_nil
        · resolve_step isStep
    | publish chan value =>
      simp only [Dex.step] at isStep
      split at isStep
      · resolve_step isStep
        exact timersDistinct
      · rename_i run hasRun
        split at isStep
        · resolve_step isStep
          exact timersDistinct
        · split at isStep
          · resolve_step isStep
            resolve_run
            exact (timersDistinct run hasRun).of_sublist
              (Dex.Run.timers_applyEffects_sublist ({ run with seq := run.seq + 1 })
                { publications := [(chan, value)] })
          · resolve_step isStep
  | stepStart stepId =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
    · rename_i run hasRun
      split at isStep
      · resolve_step isStep
      · split at isStep
        · split at isStep
          · resolve_step isStep
            resolve_run
            exact timersDistinct run hasRun
          · resolve_step isStep
        · resolve_step isStep
  | workerRun taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
    · split at isStep
      · resolve_step isStep
        exact timersDistinct
      · resolve_step isStep
  | workerEffect taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
    · split at isStep
      · resolve_step isStep
        exact timersDistinct
      · resolve_step isStep
  | workerReturn taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
    · split at isStep
      · resolve_step isStep
        exact timersDistinct
      · resolve_step isStep
  | workerAbandon taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
    · split at isStep
      · resolve_step isStep
      · resolve_step isStep
        exact timersDistinct
  | stepCompleteStale taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
    · split at isStep
      · split at isStep
        · resolve_step isStep
        · resolve_step isStep
          exact timersDistinct
      · resolve_step isStep
  | stepComplete taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · rename_i run task hasRun isFound
      split at isStep
      · split at isStep
        · rename_i response isMethod isPhase
          resolve_step isStep
          resolve_run
          have afterEffects : (((({ run with seq := run.seq + 1 } : Dex.Run signature).updateStep
              task.step (fun record => { record with invocation := none })).applyEffects
              response.effects)).HasDistinctTimers :=
            (timersDistinct run hasRun).of_sublist (Dex.Run.timers_applyEffects_sublist _ _)
          exact afterEffects.registerWait _ _ _
        · rename_i response isMethod isPhase
          resolve_step isStep
          resolve_run
          exact (timersDistinct run hasRun).of_sublist
            ((Dex.Run.timers_applyDecision_sublist _ _ _).trans
              (Dex.Run.timers_applyEffects_sublist
                (({ run with seq := run.seq + 1 } : Dex.Run signature).updateStep task.step
                  (fun record => { record with invocation := none })) response.effects))
        · resolve_step isStep
      · resolve_step isStep
    · resolve_step isStep
  | timerFire count =>
    simp only [Dex.step] at isStep
    split at isStep
    · resolve_step isStep
    · rename_i run hasRun
      split at isStep
      · resolve_step isStep
        resolve_run
        exact (timersDistinct run hasRun).of_sublist
          (Dex.Run.timers_foldl_fireTimer_sublist _ ({ run with seq := run.seq + 1 }))
      · resolve_step isStep

/-- The initial configuration has no timer. -/
theorem TimersAreDistinct.initial (flow : Dex.FlowDef signature) :
    TimersAreDistinct (Dex.system flow).init :=
  fun run hasRun => by simp [Dex.system, Dex.Config.initial] at hasRun

/-- Every configuration that a finite run reaches has distinct timer IDs. -/
theorem TimersAreDistinct.of_exec (flow : Dex.FlowDef signature)
    (actions : List (Dex.Action signature)) (events : List (Dex.Obs signature))
    (config : Dex.Config signature)
    (isExec : (Dex.system flow).exec (Dex.system flow).init actions = some (events, config)) :
    TimersAreDistinct config :=
  TransitionSystem.invariant_of_exec (Dex.system flow) TimersAreDistinct
    (fun state label stepEvents next holds isStep =>
      TimersAreDistinct.step flow state label stepEvents next holds isStep)
    actions _ events config (TimersAreDistinct.initial flow) isExec

/-- A Dex configuration of `⟦program⟧` that a finite run with admissible
requests reaches (spec §5.3, §5.4). -/
def IsAdmissiblyReachable (program : Program) (config : Dex.Config signature) : Prop :=
  ∃ actions events,
    (Dex.system ⟦program⟧).exec (Dex.system ⟦program⟧).init actions = some (events, config) ∧
    environment.IsAdmissiblePrefix (actions.filterMap (Dex.system ⟦program⟧).requestOf)

/-- A reachable configuration satisfies the invariant. -/
theorem ConfigInvariant.of_isAdmissiblyReachable (program : Program)
    (config : Dex.Config signature) (isReachable : IsAdmissiblyReachable program config) :
    ConfigInvariant program config := by
  obtain ⟨actions, events, isExec, _⟩ := isReachable
  exact (ConfigInvariant.of_exec program actions events config isExec).1

/-- A pending message never coexists with a waiter on its Channel (spec §7
item 4: "a message present when a waiter registers means no earlier waiter
could take it"). The FIFO arguments of spec §8.3 rest on this. -/
theorem noWaiterOnNonEmptyChannel (program : Program) (config : Dex.Config signature)
    (isReachable : IsAdmissiblyReachable program config)
    (run : Dex.Run signature) (hasRun : config.run = some run)
    (record : Dex.StepRecord signature) (isMember : record ∈ run.steps)
    (waitState : Dex.WaitState signature)
    (isWaiting : record.phase = .waiting) (hasWaitState : record.waitState = some waitState)
    (chan : Chan) (isWaitedOn : chan ∈ waitState.wait.channels) :
    run.pendingMessages chan = [] :=
  ((ConfigInvariant.of_isAdmissiblyReachable program config isReachable).runInvariant run
    hasRun).waitersQuiet record waitState ⟨isMember, isWaiting, hasWaitState⟩ chan isWaitedOn

/-- Two waiters never share `sws` (spec §7 item 4; spec Appendix A, assumption
11: "Equal `sws` among waiters of one Channel does not occur"). So the
tie-break on the Step execution ID that `Run.waitersOn` leaves out is never
used. -/
theorem registeredSeq_isInjective (program : Program) (config : Dex.Config signature)
    (isReachable : IsAdmissiblyReachable program config)
    (run : Dex.Run signature) (hasRun : config.run = some run)
    (first second : Dex.StepRecord signature)
    (isFirstMember : first ∈ run.steps) (isSecondMember : second ∈ run.steps)
    (firstWait secondWait : Dex.WaitState signature)
    (isFirstWaiting : first.phase = .waiting) (hasFirstWait : first.waitState = some firstWait)
    (isSecondWaiting : second.phase = .waiting) (hasSecondWait : second.waitState = some secondWait)
    (sameSeq : firstWait.registeredSeq = secondWait.registeredSeq) :
    first.id = second.id :=
  ((ConfigInvariant.of_isAdmissiblyReachable program config isReachable).runInvariant run
    hasRun).seqInjective first second firstWait secondWait
    ⟨isFirstMember, isFirstWaiting, hasFirstWait⟩ ⟨isSecondMember, isSecondWaiting, hasSecondWait⟩
    sameSeq

/-- Every armed timer is the index-0 timer of a `Timed` Step (spec §8.2: "All
the program's timers are wait timers of one run ... at index 0, with IDs
`Timed-n`"). So the timer order of `signature` is the order spec §8.2 derives
(spec Appendix A, assumption 26). -/
theorem armedTimers_areTimedSteps (program : Program) (config : Dex.Config signature)
    (isReachable : IsAdmissiblyReachable program config)
    (run : Dex.Run signature) (hasRun : config.run = some run)
    (timer : Dex.TimerId signature) (isArmed : run.timers.contains timer = true) :
    timer.step.stepType = StepType.timed ∧ timer.index = 0 := by
  obtain ⟨fireTime, isEntry⟩ := AssocList.mem_of_contains run.timers timer isArmed
  exact ((ConfigInvariant.of_isAdmissiblyReachable program config isReachable).runInvariant run
    hasRun).timersAllowed (timer, fireTime) isEntry

end DexExpressiveness
