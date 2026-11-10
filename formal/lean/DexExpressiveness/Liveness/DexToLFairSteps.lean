import DexExpressiveness.Simulation.Frame

/-!
# Liveness, work package L4: what one Dex transition keeps

The fairness lemmas of `Liveness/DexToLFair.lean` follow one Dex object
along the Dex run: a pending invocation (F3) or a task (F4, F5). This file
says what one transition does to each.

- A Step with a pending invocation that does not wait keeps its record,
  unless its invocation starts or the run closes (`pendingInvocation_step`).
- A task keeps its ID, Step, method, and input; a `Returned` task stays
  `Returned`; and a transition that drops a task adds none
  (`findTask?_step`).
- After the close the run does not change and no Step starts
  (`closed_step`), so the effects that the tasks hold only shrink
  (`pendingWeight_step`).

Everything here is proved.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

namespace Dex

variable {σ : Signature}

namespace Run

/-! ## A Step that does not wait keeps its record -/

/-- An update of `S[e]` keeps what the lookup of another Step finds. -/
theorem findStep?_updateStep_of_ne (run : Run σ) (step kept : StepExecId σ)
    (change : StepRecord σ → StepRecord σ) (record : StepRecord σ)
    (keepsId : ∀ candidate, (change candidate).id = candidate.id) (isOther : kept ≠ step)
    (isFound : run.findStep? kept = some record) :
    (run.updateStep step change).findStep? kept = some record :=
  (findStep?_updateStep_other run step kept change keepsId isOther).trans isFound

/-- `eval(e)` keeps the record of a Step that does not wait. -/
theorem findStep?_evalStep_of_not_waiting (run : Run σ) (step kept : StepExecId σ)
    (record : StepRecord σ) (isFound : run.findStep? kept = some record)
    (isNotWaiting : record.phase ≠ .waiting) :
    (run.evalStep step).findStep? kept = some record := by
  by_cases isSame : kept = step
  · subst isSame
    rw [evalStep_of_not_waiting run kept record isFound isNotWaiting]
    exact isFound
  · rw [findStep?_evalStep_other run step kept isSame]
    exact isFound

/-- A sequence of `eval` keeps the record of a Step that does not wait. -/
theorem findStep?_foldl_evalStep_of_not_waiting (steps : List (StepExecId σ)) (run : Run σ)
    (kept : StepExecId σ) (record : StepRecord σ) (isFound : run.findStep? kept = some record)
    (isNotWaiting : record.phase ≠ .waiting) :
    (steps.foldl evalStep run).findStep? kept = some record := by
  induction steps generalizing run with
  | nil => exact isFound
  | cons step laterSteps inductionHypothesis =>
    simp only [List.foldl_cons]
    exact inductionHypothesis _
      (findStep?_evalStep_of_not_waiting run step kept record isFound isNotWaiting)

/-- `wake(C)` keeps the record of a Step that does not wait. -/
theorem findStep?_wake_of_not_waiting (run : Run σ) (changed : List σ.Chan)
    (kept : StepExecId σ) (record : StepRecord σ) (isFound : run.findStep? kept = some record)
    (isNotWaiting : record.phase ≠ .waiting) :
    (run.wake changed).findStep? kept = some record :=
  findStep?_foldl_evalStep_of_not_waiting _ run kept record isFound isNotWaiting

/-- Publications keep the Step table. -/
theorem findStep?_applyPublications (run : Run σ) (publications : List (σ.Chan × σ.Val))
    (kept : StepExecId σ) :
    (run.applyPublications publications).findStep? kept = run.findStep? kept := by
  unfold findStep?
  rw [(applyPublications_frame publications run).1]

/-- Writes keep the Step table. -/
theorem findStep?_applyWrites (run : Run σ) (writes : List (σ.Key × Option σ.Val))
    (kept : StepExecId σ) : (run.applyWrites writes).findStep? kept = run.findStep? kept := by
  unfold findStep?
  rw [(applyWrites_frame writes run).1]

/-- `effects` keeps the record of a Step that does not wait. -/
theorem findStep?_applyEffects_of_not_waiting (run : Run σ) (effects : Effects σ)
    (kept : StepExecId σ) (record : StepRecord σ) (isFound : run.findStep? kept = some record)
    (isNotWaiting : record.phase ≠ .waiting) :
    (run.applyEffects effects).findStep? kept = some record := by
  unfold applyEffects
  apply findStep?_wake_of_not_waiting _ _ _ _ _ isNotWaiting
  rw [findStep?_applyPublications, findStep?_applyWrites]
  exact isFound

/-- `register(e, W)` keeps the record of another Step. -/
theorem findStep?_registerWait_other (run : Run σ) (step kept : StepExecId σ) (wait : Wait σ)
    (now : Nat) (isOther : kept ≠ step) :
    (run.registerWait step wait now).findStep? kept = run.findStep? kept := by
  unfold registerWait
  split
  · exact findStep?_updateStep_other _ _ _ _ (fun _ => rfl) isOther
  · dsimp only
    rw [findStep?_evalStep_other _ _ _ isOther]
    exact findStep?_updateStep_other _ _ _ _ (fun _ => rfl) isOther

/-- `finish(e, φ')` keeps the record of another Step. -/
theorem findStep?_finishStep_other (run : Run σ) (step kept : StepExecId σ)
    (finalPhase : Phase) (isOther : kept ≠ step) :
    (run.finishStep step finalPhase).findStep? kept = run.findStep? kept := by
  unfold finishStep
  exact findStep?_updateStep_other _ _ _ _ (fun _ => rfl) isOther

/-- `new(T, v, o, from)` keeps every record. -/
theorem findStep?_newStep (run : Run σ) (stepType : σ.StepType) (input : Option σ.Val)
    (options : StepOptions σ) (origin : Origin σ) (kept : StepExecId σ) (record : StepRecord σ)
    (isFound : run.findStep? kept = some record) :
    (run.newStep stepType input options origin).findStep? kept = some record := by
  have isFirst : run.steps.find? (fun candidate => decide (candidate.id = kept)) = some record :=
    isFound
  simp [newStep, findStep?, List.find?_append, isFirst]

/-- The moves of a decision keep every record. -/
theorem findStep?_foldl_newStep (moves : List (Move σ)) (origin : Origin σ) (run : Run σ)
    (kept : StepExecId σ) (record : StepRecord σ) (isFound : run.findStep? kept = some record) :
    (moves.foldl (fun current move =>
      current.newStep move.stepType move.input move.options origin) run).findStep? kept =
        some record := by
  induction moves generalizing run with
  | nil => exact isFound
  | cons move laterMoves inductionHypothesis =>
    simp only [List.foldl_cons]
    exact inductionHypothesis _ (findStep?_newStep run _ _ _ _ kept record isFound)

/-- After `close(κ)` the run is closed. -/
theorem closeRun_isClosed (run : Run σ) (status : CloseStatus) :
    ∃ final, (run.closeRun status).status = .closed final := by
  unfold closeRun
  split
  · rename_i final isClosed
    exact ⟨final, isClosed⟩
  · exact ⟨status, rfl⟩

/-- `decide(e, D)` keeps the record of another Step, or closes the run. -/
theorem findStep?_applyDecision_other (run : Run σ) (step kept : StepExecId σ)
    (decision : Decision σ) (record : StepRecord σ) (isOther : kept ≠ step)
    (isFound : run.findStep? kept = some record) :
    (run.applyDecision step decision).findStep? kept = some record ∨
      ∃ final, (run.applyDecision step decision).status = .closed final := by
  have isFinished : (run.finishStep step .completed).findStep? kept = some record := by
    rw [findStep?_finishStep_other _ _ _ _ isOther]
    exact isFound
  unfold applyDecision
  dsimp only
  split
  · exact Or.inr (closeRun_isClosed _ _)
  · exact Or.inr (closeRun_isClosed _ _)
  · exact Or.inl isFinished
  · exact Or.inl (findStep?_foldl_newStep _ _ _ kept record isFinished)

/-- [TIMER-FIRE] of one timer keeps the record of a Step that does not wait. -/
theorem findStep?_fireTimer_of_not_waiting (run : Run σ) (timer : TimerId σ)
    (kept : StepExecId σ) (record : StepRecord σ) (isFound : run.findStep? kept = some record)
    (isNotWaiting : record.phase ≠ .waiting) :
    (run.fireTimer timer).findStep? kept = some record := by
  unfold fireTimer
  split
  · have isDisarmedFound :
        ({ run with timers := run.timers.erase timer } : Run σ).findStep? kept = some record :=
      isFound
    dsimp only
    split
    · exact isDisarmedFound
    · rename_i found foundCase
      split
      · rename_i waitState isWaiting hasWait
        split
        · exact isDisarmedFound
        · have isOther : kept ≠ timer.step := by
            intro isSame
            rw [isSame, foundCase] at isDisarmedFound
            cases isDisarmedFound
            exact isNotWaiting isWaiting
          rw [findStep?_evalStep_other _ _ _ isOther]
          exact findStep?_updateStep_of_ne _ _ _ _ _ (fun _ => rfl) isOther isDisarmedFound
      · exact isDisarmedFound
  · exact isFound

/-- [TIMER-FIRE] keeps the record of a Step that does not wait. -/
theorem findStep?_foldl_fireTimer_of_not_waiting (timers : List (TimerId σ)) (run : Run σ)
    (kept : StepExecId σ) (record : StepRecord σ) (isFound : run.findStep? kept = some record)
    (isNotWaiting : record.phase ≠ .waiting) :
    (timers.foldl fireTimer run).findStep? kept = some record := by
  induction timers generalizing run with
  | nil => exact isFound
  | cons timer laterTimers inductionHypothesis =>
    simp only [List.foldl_cons]
    exact inductionHypothesis _
      (findStep?_fireTimer_of_not_waiting run timer kept record isFound isNotWaiting)

end Run

/-- The Step of a current task has the task's running invocation. -/
theorem Config.findStep?_of_isTaskCurrent (config : Config σ) (task : Task σ) (run : Run σ)
    (hasRun : config.run = some run) (isCurrent : config.isTaskCurrent task = true) :
    ∃ record, run.findStep? task.step = some record ∧
      record.invocation = some { method := task.method, state := .running task.id } := by
  unfold Config.isTaskCurrent at isCurrent
  rw [hasRun] at isCurrent
  dsimp only at isCurrent
  split at isCurrent
  · cases isCurrent
  · rename_i record isFound
    exact ⟨record, isFound, by simpa using isCurrent⟩

/-- One transition and a Step with a pending invocation that does not wait:
the record stays as it is, or the run closes, or the invocation starts. -/
theorem pendingInvocation_step (flow : FlowDef σ) (config next : Config σ) (action : Action σ)
    (events : List (Obs σ)) (isStep : step flow config action = some (events, next))
    (run : Run σ) (hasRun : config.run = some run) (stepId : StepExecId σ)
    (record : StepRecord σ) (isFound : run.findStep? stepId = some record) (method : Method)
    (isPending : record.invocation = some { method := method, state := .pending })
    (isNotWaiting : record.phase ≠ .waiting) :
    ∃ after, next.run = some after ∧
      (after.findStep? stepId = some record ∨
        (∃ status, after.status = .closed status) ∨
        ∃ taskId, after.findStep? stepId =
          some { record with invocation := some { method := method, state := .running taskId } }) := by
  cases action with
  | tick duration =>
    simp only [step] at isStep
    split at isStep <;> cases isStep
    exact ⟨run, hasRun, Or.inl isFound⟩
  | request request =>
    cases request with
    | startFlow stepType input =>
      simp only [step, hasRun] at isStep
      cases isStep
    | publish chan value =>
      simp only [step, hasRun] at isStep
      split at isStep
      · cases isStep
        exact ⟨run, hasRun, Or.inl isFound⟩
      · split at isStep <;> cases isStep
        refine ⟨_, rfl, Or.inl ?_⟩
        apply Run.findStep?_wake_of_not_waiting _ _ _ _ _ isNotWaiting
        rw [Run.findStep?_applyPublications]
        exact isFound
  | stepStart started =>
    simp only [step, hasRun] at isStep
    split at isStep
    · cases isStep
    · rename_i startedRecord startedFound
      split at isStep
      · rename_i startedMethod hasInvocation
        split at isStep <;> cases isStep
        refine ⟨_, rfl, ?_⟩
        by_cases isSame : stepId = started
        · subst isSame
          rw [isFound] at startedFound
          cases startedFound
          rw [isPending] at hasInvocation
          cases hasInvocation
          exact Or.inr (Or.inr ⟨config.nextTaskId,
            Run.findStep?_updateStep_self _ _ _ _ isFound rfl⟩)
        · left
          exact Run.findStep?_updateStep_of_ne _ _ _ _ _ (fun _ => rfl) isSame isFound
      · cases isStep
  | workerRun taskId =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact ⟨run, hasRun, Or.inl isFound⟩
  | workerEffect taskId =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact ⟨run, hasRun, Or.inl isFound⟩
  | workerReturn taskId =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact ⟨run, hasRun, Or.inl isFound⟩
  | workerAbandon taskId =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact ⟨run, hasRun, Or.inl isFound⟩
  | stepCompleteStale taskId =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · split at isStep <;> cases isStep
        exact ⟨run, hasRun, Or.inl isFound⟩
      · cases isStep
  | stepComplete taskId =>
    simp only [step] at isStep
    split at isStep
    · rename_i current task hasCurrent taskFound
      rw [hasRun] at hasCurrent
      cases hasCurrent
      split at isStep
      · rename_i isEnabled
        simp only [Bool.and_eq_true] at isEnabled
        obtain ⟨taskRecord, taskRecordFound, taskInvocation⟩ :=
          config.findStep?_of_isTaskCurrent task run hasRun isEnabled.1
        have isOther : stepId ≠ task.step := by
          intro isSame
          rw [isSame, taskRecordFound] at isFound
          cases isFound
          rw [isPending] at taskInvocation
          cases taskInvocation
        have isKept : ∀ effects : Effects σ,
            ((({ run with seq := run.seq + 1 } : Run σ).updateStep task.step
              (fun record => { record with invocation := none })).applyEffects
                effects).findStep? stepId = some record := by
          intro effects
          apply Run.findStep?_applyEffects_of_not_waiting _ _ _ _ _ isNotWaiting
          exact Run.findStep?_updateStep_of_ne _ _ _ _ _ (fun _ => rfl) isOther isFound
        split at isStep <;> cases isStep
        · rename_i response _ _
          refine ⟨_, rfl, Or.inl ?_⟩
          rw [Run.findStep?_registerWait_other _ _ _ _ _ isOther]
          exact isKept response.effects
        · rename_i response _ _
          rcases Run.findStep?_applyDecision_other _ task.step stepId response.decision record
            isOther (isKept response.effects) with isSame | isClosed
          · exact ⟨_, rfl, Or.inl isSame⟩
          · exact ⟨_, rfl, Or.inr (Or.inl isClosed)⟩
      · cases isStep
    · cases isStep
  | timerFire count =>
    simp only [step, hasRun] at isStep
    split at isStep <;> cases isStep
    exact ⟨_, rfl, Or.inl
      (Run.findStep?_foldl_fireTimer_of_not_waiting _ _ _ _ isFound isNotWaiting)⟩

end Dex

/-! ## Tasks -/

/-- How `Θ` changes in one transition: not at all; a [STEP-START] adds a
task; a Worker step changes the phase of a task that has not returned, and
the task then holds no more effects than before; or a task is dropped. -/
theorem tasks_step_cases (flow : Dex.FlowDef signature) (config next : Dex.Config signature)
    (action : Dex.Action signature) (events : List (Dex.Obs signature))
    (isStep : Dex.step flow config action = some (events, next)) :
    next.tasks = config.tasks ∨
    (∃ stepId task, action = .stepStart stepId ∧ next.tasks = config.tasks ++ [task]) ∨
    (∃ taskId task phase, config.findTask? taskId = some task ∧
      (∀ response, task.phase ≠ .returned response) ∧
      (∀ current : Dex.Task signature,
        (pendingEffects flow { current with phase := phase }).length ≤
          (pendingEffects flow task).length) ∧
      next.tasks =
        (config.updateTask taskId (fun current => { current with phase := phase })).tasks) ∨
    (∃ taskId, next.tasks = (config.removeTask taskId).tasks) := by
  cases action with
  | tick duration =>
    simp only [Dex.step] at isStep
    split at isStep <;> cases isStep
    exact Or.inl rfl
  | request request =>
    cases request with
    | startFlow stepType input =>
      simp only [Dex.step] at isStep
      split at isStep
      · cases isStep
      · split at isStep <;> cases isStep
        exact Or.inl rfl
    | publish chan value =>
      simp only [Dex.step] at isStep
      split at isStep
      · cases isStep
        exact Or.inl rfl
      · split at isStep
        · cases isStep
          exact Or.inl rfl
        · split at isStep <;> cases isStep
          exact Or.inl rfl
  | stepStart stepId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · cases isStep
      · split at isStep
        · split at isStep
          · cases isStep
            exact Or.inr (Or.inl ⟨stepId, _, rfl, rfl⟩)
          · cases isStep
        · cases isStep
  | workerRun taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i task isFound
      split at isStep <;> cases isStep
      rename_i isPhase
      refine Or.inr (Or.inr (Or.inl ⟨taskId, task, _, isFound, ?_, ?_, rfl⟩))
      · intro response isReturned
        rw [isPhase] at isReturned
        cases isReturned
      · intro current
        simp [pendingEffects, isPhase]
  | workerEffect taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i task isFound
      split at isStep <;> cases isStep
      rename_i isPhase
      refine Or.inr (Or.inr (Or.inl ⟨taskId, task, _, isFound, ?_, ?_, rfl⟩))
      · intro response isReturned
        rw [isPhase] at isReturned
        cases isReturned
      · intro current
        simp [pendingEffects, isPhase]
  | workerReturn taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i task isFound
      split at isStep <;> cases isStep
      rename_i isPhase
      refine Or.inr (Or.inr (Or.inl ⟨taskId, task, _, isFound, ?_, ?_, rfl⟩))
      · intro response isReturned
        rw [isPhase] at isReturned
        cases isReturned
      · intro current
        simp [pendingEffects]
  | workerAbandon taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact Or.inr (Or.inr (Or.inr ⟨taskId, rfl⟩))
  | stepCompleteStale taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · split at isStep <;> cases isStep
        exact Or.inr (Or.inr (Or.inr ⟨taskId, rfl⟩))
      · cases isStep
  | stepComplete taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · split at isStep
      · split at isStep <;> cases isStep
        · exact Or.inr (Or.inr (Or.inr ⟨taskId, rfl⟩))
        · exact Or.inr (Or.inr (Or.inr ⟨taskId, rfl⟩))
      · cases isStep
    · cases isStep
  | timerFire count =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact Or.inl rfl

/-- One transition and a task of `Θ`: the task stays, with its ID, Step,
method, and input, and a `Returned` task stays `Returned`; or the task is
dropped, and then the transition adds no task. -/
theorem findTask?_step (flow : Dex.FlowDef signature) (config next : Dex.Config signature)
    (action : Dex.Action signature) (events : List (Dex.Obs signature))
    (isStep : Dex.step flow config action = some (events, next))
    (taskId : Nat) (task : Dex.Task signature) (isFound : config.findTask? taskId = some task) :
    (∃ phase, next.findTask? taskId = some { task with phase := phase } ∧
      ∀ response, task.phase = .returned response → phase = .returned response) ∨
    (next.findTask? taskId = none ∧ ∀ other, other ∈ next.tasks → other ∈ config.tasks) := by
  have hasId := (mem_of_findTask? config taskId task isFound).2
  rcases tasks_step_cases flow config next action events isStep with
    isSame | ⟨stepId, added, _, isAdded⟩ |
    ⟨changedId, changed, phase, changedFound, isNotReturned, _, isChanged⟩ |
    ⟨removedId, isRemoved⟩
  · left
    refine ⟨task.phase, ?_, fun response isReturned => isReturned⟩
    rw [findTask?_congr config next taskId isSame]
    exact isFound
  · left
    refine ⟨task.phase, ?_, fun response isReturned => isReturned⟩
    have isFirst :
        config.tasks.find? (fun candidate => decide (candidate.id = taskId)) = some task := isFound
    simp [Dex.Config.findTask?, isAdded, List.find?_append, isFirst]
  · have isLookup : next.findTask? taskId =
        (config.updateTask changedId (fun current => { current with phase := phase })).findTask?
          taskId := findTask?_congr _ next taskId isChanged
    rw [findTask?_updatePhase, isFound] at isLookup
    left
    by_cases isSameId : task.id = changedId
    · refine ⟨phase, by simpa [isSameId] using isLookup, ?_⟩
      intro response isReturned
      have isChangedTask : changed = task := by
        rw [← hasId, isSameId, changedFound] at isFound
        exact Option.some.inj isFound
      subst isChangedTask
      exact absurd isReturned (isNotReturned response)
    · exact ⟨task.phase, by simpa [isSameId] using isLookup,
        fun response isReturned => isReturned⟩
  · by_cases isSameId : taskId = removedId
    · right
      subst isSameId
      refine ⟨?_, ?_⟩
      · rw [findTask?_congr _ next taskId isRemoved]
        exact findTask?_removeTask_self config taskId
      · intro other isMember
        rw [isRemoved] at isMember
        exact (List.mem_filter.mp isMember).1
    · left
      refine ⟨task.phase, ?_, fun response isReturned => isReturned⟩
      rw [findTask?_congr _ next taskId isRemoved,
        findTask?_removeTask_other config removedId taskId isSameId]
      exact isFound

/-! ## After the close -/

/-- After the close the run does not change, and no Step starts: no Step has
an invocation, no task is current, and no timer is armed. -/
theorem closed_step (flow : Dex.FlowDef signature) (config next : Dex.Config signature)
    (action : Dex.Action signature) (events : List (Dex.Obs signature))
    (isStep : Dex.step flow config action = some (events, next))
    (run : Dex.Run signature) (hasRun : config.run = some run) (status : Dex.CloseStatus)
    (isClosed : run.status = .closed status)
    (noInvocation : ∀ record, record ∈ run.steps → record.invocation = none)
    (tasksAreStale : ∀ task, task ∈ config.tasks → config.isTaskCurrent task = false)
    (noTimer : run.timers = []) :
    next.run = some run ∧ ∀ stepId, action ≠ .stepStart stepId := by
  cases action with
  | tick duration =>
    simp only [Dex.step] at isStep
    split at isStep <;> cases isStep
    exact ⟨hasRun, nofun⟩
  | request request =>
    cases request with
    | startFlow stepType input =>
      simp only [Dex.step, hasRun] at isStep
      cases isStep
    | publish chan value =>
      simp only [Dex.step, hasRun, isClosed] at isStep
      cases isStep
      exact ⟨hasRun, nofun⟩
  | stepStart stepId =>
    simp only [Dex.step, hasRun] at isStep
    split at isStep
    · cases isStep
    · rename_i record isRecordFound
      simp [noInvocation record (Dex.Run.mem_of_findStep? run _ record isRecordFound).1] at isStep
  | workerRun taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact ⟨hasRun, nofun⟩
  | workerEffect taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact ⟨hasRun, nofun⟩
  | workerReturn taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact ⟨hasRun, nofun⟩
  | workerAbandon taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep <;> cases isStep
      exact ⟨hasRun, nofun⟩
  | stepCompleteStale taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · cases isStep
    · split at isStep
      · split at isStep <;> cases isStep
        exact ⟨hasRun, nofun⟩
      · cases isStep
  | stepComplete taskId =>
    simp only [Dex.step] at isStep
    split at isStep
    · rename_i current task hasCurrent taskFound
      simp [tasksAreStale task (mem_of_findTask? config taskId task taskFound).1] at isStep
    · cases isStep
  | timerFire count =>
    simp only [Dex.step, hasRun] at isStep
    have noDue : run.dueTimers config.clock = [] := by
      simp [Dex.Run.dueTimers, noTimer, sortStable]
    split at isStep
    · rename_i isEnabled
      simp only [noDue, List.length_nil, Bool.and_eq_true, decide_eq_true_eq] at isEnabled
      omega
    · cases isStep

/-! ## The effects that the tasks hold -/

/-- Changing elements without adding weight does not add weight. -/
theorem length_flatMap_map_le {α β : Type} (weigh : α → List β) (change : α → α)
    (elements : List α)
    (isLower : ∀ element, element ∈ elements →
      (weigh (change element)).length ≤ (weigh element).length) :
    ((elements.map change).flatMap weigh).length ≤ (elements.flatMap weigh).length := by
  induction elements with
  | nil => simp
  | cons head rest inductionHypothesis =>
    have isHeadLower := isLower head (by simp)
    have isRestLower := inductionHypothesis
      (fun element isMember => isLower element (by simp [isMember]))
    simp only [List.map_cons, List.flatMap_cons, List.length_append]
    omega

/-- Changing elements without adding weight, and one with less weight, lowers the weight. -/
theorem length_flatMap_map_lt {α β : Type} (weigh : α → List β) (change : α → α)
    (elements : List α)
    (isLower : ∀ element, element ∈ elements →
      (weigh (change element)).length ≤ (weigh element).length)
    (changed : α) (isMember : changed ∈ elements)
    (isStrict : (weigh (change changed)).length < (weigh changed).length) :
    ((elements.map change).flatMap weigh).length < (elements.flatMap weigh).length := by
  induction elements with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    have isHeadLower := isLower head (by simp)
    have isRestLower := length_flatMap_map_le weigh change rest
      (fun element isRestMember => isLower element (by simp [isRestMember]))
    simp only [List.map_cons, List.flatMap_cons, List.length_append]
    rcases List.mem_cons.mp isMember with isHead | isRest
    · subst isHead
      omega
    · have isRestStrict := inductionHypothesis
        (fun element isRestMember => isLower element (by simp [isRestMember])) isRest
      omega

/-- Removing elements does not add weight. -/
theorem length_flatMap_filter_le {α β : Type} (weigh : α → List β) (isKept : α → Bool)
    (elements : List α) :
    ((elements.filter isKept).flatMap weigh).length ≤ (elements.flatMap weigh).length := by
  induction elements with
  | nil => simp
  | cons head rest inductionHypothesis =>
    by_cases keepsHead : isKept head = true
    · simp only [List.filter_cons_of_pos keepsHead, List.flatMap_cons, List.length_append]
      omega
    · simp only [List.filter_cons_of_neg keepsHead, List.flatMap_cons, List.length_append]
      omega

/-- Removing an element that has weight lowers the weight. -/
theorem length_flatMap_filter_lt {α β : Type} (weigh : α → List β) (isKept : α → Bool)
    (elements : List α) (removed : α) (isMember : removed ∈ elements)
    (isRemoved : isKept removed = false) (isHeavy : weigh removed ≠ []) :
    ((elements.filter isKept).flatMap weigh).length < (elements.flatMap weigh).length := by
  induction elements with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    have isRestLower := length_flatMap_filter_le weigh isKept rest
    rcases List.mem_cons.mp isMember with isHead | isRest
    · subst isHead
      have isPositive : 0 < (weigh removed).length := List.length_pos_iff.mpr isHeavy
      have dropsHead : ¬ isKept removed = true := by simp [isRemoved]
      simp only [List.filter_cons_of_neg dropsHead, List.flatMap_cons, List.length_append]
      omega
    · have isRestStrict := inductionHypothesis isRest
      by_cases keepsHead : isKept head = true
      · simp only [List.filter_cons_of_pos keepsHead, List.flatMap_cons, List.length_append]
        omega
      · simp only [List.filter_cons_of_neg keepsHead, List.flatMap_cons, List.length_append]
        omega

/-- One transition that starts no Step: the tasks hold no more effects than
before; and a task that holds an effect still holds one, or the tasks hold
fewer effects than before. -/
theorem heldEffects_step (flow : Dex.FlowDef signature) (config next : Dex.Config signature)
    (action : Dex.Action signature) (events : List (Dex.Obs signature))
    (isStep : Dex.step flow config action = some (events, next))
    (tasksDistinct : TasksAreDistinct config)
    (isNotStart : ∀ stepId, action ≠ .stepStart stepId) :
    (next.tasks.flatMap (pendingEffects flow)).length ≤
        (config.tasks.flatMap (pendingEffects flow)).length ∧
    ∀ taskId task, config.findTask? taskId = some task → pendingEffects flow task ≠ [] →
      (∃ later, next.findTask? taskId = some later ∧ pendingEffects flow later ≠ []) ∨
      (next.tasks.flatMap (pendingEffects flow)).length <
        (config.tasks.flatMap (pendingEffects flow)).length := by
  rcases tasks_step_cases flow config next action events isStep with
    isSame | ⟨stepId, added, isStart, _⟩ |
    ⟨changedId, changed, phase, changedFound, _, isLower, isChanged⟩ |
    ⟨removedId, isRemoved⟩
  · rw [isSame]
    refine ⟨Nat.le_refl _, fun taskId task isFound isHolding => Or.inl ⟨task, ?_, isHolding⟩⟩
    rw [findTask?_congr config next taskId isSame]
    exact isFound
  · exact absurd isStart (isNotStart stepId)
  · have isChangedList : next.tasks = config.tasks.map (fun current =>
        if current.id = changedId then { current with phase := phase } else current) := isChanged
    have isPointwise : ∀ element, element ∈ config.tasks →
        (pendingEffects flow
          (if element.id = changedId then { element with phase := phase } else element)).length ≤
        (pendingEffects flow element).length := by
      intro element isMember
      by_cases isSameId : element.id = changedId
      · have isChangedTask : element = changed :=
          eq_of_isFound config changedId element changed tasksDistinct isMember isSameId
            changedFound
        simp only [isSameId, ↓reduceIte]
        rw [← isChangedTask] at isLower
        exact isLower element
      · simp [isSameId]
    rw [isChangedList]
    refine ⟨length_flatMap_map_le _ _ _ isPointwise, ?_⟩
    intro taskId task isFound isHolding
    obtain ⟨isMember, hasId⟩ := mem_of_findTask? config taskId task isFound
    have isLookup : next.findTask? taskId =
        (config.updateTask changedId (fun current => { current with phase := phase })).findTask?
          taskId := findTask?_congr _ next taskId isChanged
    rw [findTask?_updatePhase, isFound] at isLookup
    by_cases isSameId : task.id = changedId
    · by_cases isEmptied : pendingEffects flow { task with phase := phase } = []
      · right
        apply length_flatMap_map_lt _ _ _ isPointwise task isMember
        show (pendingEffects flow
          (if task.id = changedId then { task with phase := phase } else task)).length < _
        split
        · rw [isEmptied]
          exact List.length_pos_iff.mpr isHolding
        · rename_i isOtherId
          exact absurd isSameId isOtherId
      · exact Or.inl ⟨_, by simpa [isSameId] using isLookup, isEmptied⟩
    · exact Or.inl ⟨task, by simpa [isSameId] using isLookup, isHolding⟩
  · have isFiltered : next.tasks =
        config.tasks.filter (fun candidate => !decide (candidate.id = removedId)) := isRemoved
    rw [isFiltered]
    refine ⟨length_flatMap_filter_le _ _ _, ?_⟩
    intro taskId task isFound isHolding
    obtain ⟨isMember, hasId⟩ := mem_of_findTask? config taskId task isFound
    by_cases isSameId : taskId = removedId
    · right
      exact length_flatMap_filter_lt _ _ _ task isMember (by simp [hasId, isSameId]) isHolding
    · left
      refine ⟨task, ?_, isHolding⟩
      rw [findTask?_congr _ next taskId isRemoved,
        findTask?_removeTask_other config removedId taskId isSameId]
      exact isFound

end DexExpressiveness
