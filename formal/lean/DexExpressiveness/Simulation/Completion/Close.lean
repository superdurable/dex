import DexExpressiveness.Simulation.Completion.Assemble

/-!
# `exit` and `fail`: the close of the run

`FC(v)` and `FF(s)` cancel every active Step (spec §4.2 `close`). On the L
side the program ends and every other thread stops; the emits that were
issued and not delivered stay as late emits (spec §8.1 "Exit").

- `wp3b_pendingEffects_perm`: before the close, the effects the tasks have
  not emitted are the emits the threads have issued and not delivered.
- `wp3b_cancelAll`: what canceling a list of Steps does.
- `wp3b_close_related`: the configurations after the close are related.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- A WaitFor task has no effect left to emit: no WaitFor of a translated
Flow has an effect (`pendingEffects_eq_nil_of_waitFor`). -/
def WaitForTasksHaveNoEffects (program : Program) (config : Dex.Config signature) : Prop :=
  ∀ task, task ∈ config.tasks → task.method = .waitFor → pendingEffects ⟦program⟧ task = []

/-! ## Lists -/

/-- `flatMap` after `filterMap`. -/
theorem wp3b_flatMap_filterMap {α β γ : Type} (select : α → Option β) (expand : β → List γ)
    (elements : List α) :
    (elements.filterMap select).flatMap expand =
      elements.flatMap (fun element => (select element).toList.flatMap expand) := by
  induction elements with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    cases selectCase : select head with
    | none => simp [List.filterMap_cons, selectCase, inductionHypothesis]
    | some selected => simp [List.filterMap_cons, selectCase, inductionHypothesis]

/-- `filterMap` as a `flatMap`. -/
theorem wp3b_flatMap_toList {α β : Type} (select : α → Option β) (elements : List α) :
    elements.flatMap (fun element => (select element).toList) = elements.filterMap select := by
  induction elements with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    cases selectCase : select head with
    | none => simp [List.filterMap_cons, selectCase, inductionHypothesis]
    | some selected => simp [List.filterMap_cons, selectCase, inductionHypothesis]

/-- Removing elements that expand to nothing does not change a `flatMap`. -/
theorem wp3b_flatMap_filter {α β : Type} (isKept : α → Bool) (expand : α → List β)
    (elements : List α)
    (droppedAreEmpty : ∀ element, element ∈ elements → isKept element = false →
      expand element = []) :
    (elements.filter isKept).flatMap expand = elements.flatMap expand := by
  induction elements with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    have restEmpty : ∀ element, element ∈ rest → isKept element = false → expand element = [] :=
      fun element isMember => droppedAreEmpty element (List.mem_cons_of_mem _ isMember)
    cases keptCase : isKept head with
    | true => simp [keptCase, inductionHypothesis restEmpty]
    | false =>
      simp [keptCase, inductionHypothesis restEmpty,
        droppedAreEmpty head (List.mem_cons_self ..) keptCase]

/-! ## Tasks and threads -/

/-- The task that runs the invocation of a Step that matches a thread's
phase: it has the Step's input, and the effects it has not emitted are the
emit the thread has issued and not delivered. -/
theorem wp3b_task_of_match (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (thread : L.Thread) (record : Dex.StepRecord signature)
    (method : Dex.Method) (taskId : Nat)
    (waitForQuiet : WaitForTasksHaveNoEffects program dexConfig)
    (isMatch : PhaseMatchesStep program lConfig dexConfig thread record)
    (hasInvocation : record.invocation = some { method := method, state := .running taskId })
    (emitOf : L.Thread → Option Val)
    (emitSpec : ∀ candidate effect,
      emitOf candidate = some effect ↔ candidate.phase = .emitting effect) :
    ∃ task, task ∈ dexConfig.tasks ∧ task.id = taskId ∧ task.input = record.input ∧
      pendingEffects ⟦program⟧ task = (emitOf thread).toList := by
  have ofRunning : ∀ (expected : Dex.Method) (Holds : Dex.Task signature → Prop),
      HasRunningTask dexConfig record expected Holds →
      ∃ task, task ∈ dexConfig.tasks ∧ task.id = taskId ∧ task.input = record.input ∧
        task.method = expected ∧ Holds task := by
    rintro expected Holds ⟨task, isTask, isRunning, _, hasMethod, hasInput, holds⟩
    rw [hasInvocation] at isRunning
    simp only [Option.some.injEq, Dex.Invocation.mk.injEq,
      Dex.InvocationState.running.injEq] at isRunning
    exact ⟨task, isTask, isRunning.2.symm, hasInput, hasMethod, holds⟩
  have noEmit : (∀ effect, thread.phase ≠ .emitting effect) → emitOf thread = none := by
    intro isNotEmitting
    cases emitCase : emitOf thread with
    | none => rfl
    | some effect => exact absurd ((emitSpec thread effect).mp emitCase) (isNotEmitting effect)
  unfold PhaseMatchesStep at isMatch
  cases phaseCase : thread.phase with
  | bootPending =>
    simp only [phaseCase] at isMatch
    rw [hasInvocation] at isMatch
    simp at isMatch
  | ready =>
    simp only [phaseCase] at isMatch
    rw [hasInvocation] at isMatch
    simp at isMatch
  | blocked on since =>
    simp only [phaseCase] at isMatch
    rw [hasInvocation] at isMatch
    simp at isMatch
  | woken outcome =>
    simp only [phaseCase] at isMatch
    rw [hasInvocation] at isMatch
    simp at isMatch
  | halted => simp only [phaseCase] at isMatch
  | bootRunning =>
    simp only [phaseCase] at isMatch
    obtain ⟨task, isTask, hasId, hasInput, _, noEffects⟩ := ofRunning _ _ isMatch.2
    exact ⟨task, isTask, hasId, hasInput, by
      rw [noEffects, noEmit (by simp [phaseCase])]
      rfl⟩
  | running loaded =>
    simp only [phaseCase] at isMatch
    obtain ⟨task, isTask, hasId, hasInput, _, noEffects, _⟩ := ofRunning _ _ isMatch.2
    exact ⟨task, isTask, hasId, hasInput, by
      rw [noEffects, noEmit (by simp [phaseCase])]
      rfl⟩
  | emitting effect =>
    simp only [phaseCase] at isMatch
    obtain ⟨task, isTask, hasId, hasInput, _, hasEffect, _⟩ := ofRunning _ _ isMatch.2
    exact ⟨task, isTask, hasId, hasInput, by
      rw [hasEffect, (emitSpec thread effect).mpr phaseCase]
      rfl⟩
  | emitted =>
    simp only [phaseCase] at isMatch
    obtain ⟨task, isTask, hasId, hasInput, _, noEffects, _⟩ := ofRunning _ _ isMatch.2
    exact ⟨task, isTask, hasId, hasInput, by
      rw [noEffects, noEmit (by simp [phaseCase])]
      rfl⟩
  | waitStarted =>
    simp only [phaseCase] at isMatch
    obtain ⟨task, isTask, hasId, hasInput, isWaitFor, _⟩ := ofRunning _ _ isMatch.2
    exact ⟨task, isTask, hasId, hasInput, by
      rw [waitForQuiet task isTask isWaitFor, noEmit (by simp [phaseCase])]
      rfl⟩
  | resuming outcome =>
    simp only [phaseCase] at isMatch
    obtain ⟨task, isTask, hasId, hasInput, _, noEffects, _⟩ := ofRunning _ _ isMatch.2
    exact ⟨task, isTask, hasId, hasInput, by
      rw [noEffects, noEmit (by simp [phaseCase])]
      rfl⟩

/-- While the program runs, the effects that the tasks have not emitted are
the emits that the threads have issued and not delivered (spec §8.1: "issued
emits may still deliver"). -/
theorem wp3b_pendingEffects_perm (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (run : Dex.Run signature) (hasRun : dexConfig.run = some run)
    (isRunning : run.status = .running)
    (waitForQuiet : WaitForTasksHaveNoEffects program dexConfig)
    (emitOf : L.Thread → Option Val)
    (emitSpec : ∀ candidate effect,
      emitOf candidate = some effect ↔ candidate.phase = .emitting effect) :
    (dexConfig.tasks.flatMap (pendingEffects ⟦program⟧)).Perm
      (lConfig.threads.filterMap emitOf) := by
  have related := state.isRelated.running run hasRun isRunning
  have lInvariant := state.lInvariant
  have threadTid : ∀ other, other ∈ lConfig.threads → ∀ candidate,
      StepCarriesThread program other candidate →
      threadOfInput candidate.input = other.core.tid := fun other isOther candidate =>
    wp3b_threadOfInput_of_invariant program lConfig lInvariant other isOther candidate
  have sameThread : ∀ first, first ∈ lConfig.threads → ∀ second, second ∈ lConfig.threads →
      first.core.tid = second.core.tid → first = second := fun first isFirst second isSecond =>
    eq_of_nodup_map (fun other => other.core.tid) lConfig.threads lInvariant.threadIdsNodup
      isFirst isSecond
  -- Every task runs the invocation of the Step of one thread.
  have taskFacts : ∀ task, task ∈ dexConfig.tasks →
      ∃ other candidate, other ∈ lConfig.threads ∧ candidate ∈ run.steps ∧
        candidate.phase.isActive = true ∧ StepCarriesThread program other candidate ∧
        candidate.invocation = some { method := task.method, state := .running task.id } ∧
        threadOfInput task.input = other.core.tid ∧
        pendingEffects ⟦program⟧ task = (emitOf other).toList := by
    intro task isTask
    have isCurrent := related.tasksAreCurrent task isTask
    unfold Dex.Config.isTaskCurrent at isCurrent
    rw [hasRun] at isCurrent
    simp only at isCurrent
    cases foundCase : run.findStep? task.step with
    | none => simp [foundCase] at isCurrent
    | some candidate =>
      simp only [foundCase, decide_eq_true_eq] at isCurrent
      have isCandidate := (Dex.Run.mem_of_findStep? run task.step candidate foundCase).1
      have isActive := related.invocationsAreActive candidate isCandidate (by simp [isCurrent])
      obtain ⟨other, isOther, isLive, carries⟩ :=
        related.stepsHaveThreads candidate isCandidate isActive
      obtain ⟨found, isFound, foundActive, foundCarries, isMatch⟩ :=
        related.threadsHaveSteps other isOther isLive
      have isSame := related.activeStepsHaveDistinctThreads candidate isCandidate found isFound
        isActive foundActive ((threadTid other isOther candidate carries).trans
          (threadTid other isOther found foundCarries).symm)
      subst isSame
      obtain ⟨running, isRunningTask, sameId, hasInput, hasEffects⟩ :=
        wp3b_task_of_match program lConfig dexConfig other candidate task.method task.id
          waitForQuiet isMatch isCurrent emitOf emitSpec
      have isTaskSame : running = task :=
        eq_of_nodup_map (fun candidateTask => candidateTask.id) dexConfig.tasks
          state.tasksDistinct.1 isRunningTask isTask sameId
      subst isTaskSame
      exact ⟨other, candidate, isOther, isCandidate, isActive, carries, isCurrent, by
        rw [hasInput]
        exact threadTid other isOther candidate carries, hasEffects⟩
  -- Two tasks of the same thread are the same task.
  have taskUnique : ∀ first, first ∈ dexConfig.tasks → ∀ second, second ∈ dexConfig.tasks →
      threadOfInput first.input = threadOfInput second.input → first = second := by
    intro first isFirst second isSecond sameInput
    obtain ⟨firstThread, firstRecord, isFirstThread, isFirstRecord, firstActive, firstCarries,
      firstInvocation, firstTid, _⟩ := taskFacts first isFirst
    obtain ⟨secondThread, secondRecord, isSecondThread, isSecondRecord, secondActive,
      secondCarries, secondInvocation, secondTid, _⟩ := taskFacts second isSecond
    have isSameThread := sameThread firstThread isFirstThread secondThread isSecondThread
      (firstTid.symm.trans (sameInput.trans secondTid))
    subst isSameThread
    have isSameRecord := related.activeStepsHaveDistinctThreads firstRecord isFirstRecord
      secondRecord isSecondRecord firstActive secondActive
      ((threadTid firstThread isFirstThread firstRecord firstCarries).trans
        (threadTid firstThread isFirstThread secondRecord secondCarries).symm)
    subst isSameRecord
    rw [firstInvocation] at secondInvocation
    simp only [Option.some.injEq, Dex.Invocation.mk.injEq,
      Dex.InvocationState.running.injEq] at secondInvocation
    exact eq_of_nodup_map (fun candidateTask => candidateTask.id) dexConfig.tasks
      state.tasksDistinct.1 isFirst isSecond secondInvocation.2
  let taskOf : L.Thread → Option (Dex.Task signature) := fun other =>
    dexConfig.tasks.find? (fun task => decide (threadOfInput task.input = other.core.tid))
  have taskOfSpec : ∀ other task, taskOf other = some task →
      task ∈ dexConfig.tasks ∧ threadOfInput task.input = other.core.tid := by
    intro other task isFound
    exact ⟨List.mem_of_find?_eq_some isFound, by simpa using List.find?_some isFound⟩
  have taskOfThread : ∀ task, task ∈ dexConfig.tasks → ∀ other,
      threadOfInput task.input = other.core.tid → taskOf other = some task := by
    intro task isTask other hasTid
    cases foundCase : taskOf other with
    | none =>
      have noMatch := List.find?_eq_none.mp foundCase task isTask
      simp [hasTid] at noMatch
    | some found =>
      obtain ⟨isFound, foundTid⟩ := taskOfSpec other found foundCase
      rw [taskUnique found isFound task isTask (foundTid.trans hasTid.symm)]
  -- The tasks are the tasks of the threads.
  have tasksArePerm : dexConfig.tasks.Perm (lConfig.threads.filterMap taskOf) := by
    apply (List.perm_ext_iff_of_nodup ?_ ?_).mpr
    · intro task
      constructor
      · intro isTask
        obtain ⟨other, _, isOther, _, _, _, _, hasTid, _⟩ := taskFacts task isTask
        exact List.mem_filterMap.mpr ⟨other, isOther, taskOfThread task isTask other hasTid⟩
      · intro isTask
        obtain ⟨other, _, isFound⟩ := List.mem_filterMap.mp isTask
        exact (taskOfSpec other task isFound).1
    · exact List.Pairwise.of_map (fun candidateTask : Dex.Task signature => candidateTask.id)
        (fun first second isDistinct isSame => isDistinct (by rw [isSame]))
        state.tasksDistinct.1
    · apply List.Pairwise.filterMap taskOf ?_ (List.pairwise_map.mp lInvariant.threadIdsNodup)
      intro first second isDistinct firstTask isFirstTask secondTask isSecondTask isSame
      apply isDistinct
      rw [← (taskOfSpec first firstTask isFirstTask).2,
        ← (taskOfSpec second secondTask isSecondTask).2, isSame]
  -- The task of a thread has not emitted exactly the emit the thread has issued.
  have effectsOfThread : ∀ other, other ∈ lConfig.threads →
      (taskOf other).toList.flatMap (pendingEffects ⟦program⟧) = (emitOf other).toList := by
    intro other isOther
    cases foundCase : taskOf other with
    | none =>
      cases emitCase : emitOf other with
      | none => rfl
      | some effect =>
        exfalso
        have isEmitting := (emitSpec other effect).mp emitCase
        obtain ⟨candidate, isCandidate, _, carries, isMatch⟩ :=
          related.threadsHaveSteps other isOther (by simp [isEmitting])
        unfold PhaseMatchesStep at isMatch
        simp only [isEmitting] at isMatch
        obtain ⟨_, task, isTask, _, _, _, hasInput, _⟩ := isMatch
        have isFound := taskOfThread task isTask other (by
          rw [hasInput]
          exact threadTid other isOther candidate carries)
        rw [foundCase] at isFound
        cases isFound
    | some task =>
      obtain ⟨isTask, hasTid⟩ := taskOfSpec other task foundCase
      obtain ⟨owner, _, isOwner, _, _, _, _, ownerTid, hasEffects⟩ := taskFacts task isTask
      have isSameThread := sameThread owner isOwner other isOther (ownerTid.symm.trans hasTid)
      subst isSameThread
      simp [hasEffects]
  have isFirst := tasksArePerm.flatMap_right (pendingEffects ⟦program⟧)
  rw [wp3b_flatMap_filterMap] at isFirst
  have isSecond : lConfig.threads.flatMap (fun other =>
      (taskOf other).toList.flatMap (pendingEffects ⟦program⟧)) =
      lConfig.threads.filterMap emitOf := by
    rw [← wp3b_flatMap_toList]
    exact congrArg List.flatten (List.map_congr_left effectsOfThread)
  rw [isSecond] at isFirst
  exact isFirst

/-! ## Canceling Steps -/

/-- What canceling a list of Steps does (spec §4.2 `close`): the canceled
Steps have no invocation, the other Steps are unchanged, and the timers of
the canceled Steps are disarmed. -/
theorem wp3b_cancelAll (steps : List (Dex.StepExecId signature)) (run : Dex.Run signature) :
    (∀ record, record ∈ (steps.foldl (fun current step =>
        current.finishStep step .canceled) run).steps →
      ∃ original, original ∈ run.steps ∧
        (original.id ∈ steps → record.invocation = none) ∧
        (original.id ∉ steps → record = original)) ∧
    (steps.foldl (fun current step => current.finishStep step .canceled) run).timers =
      run.timers.filter (fun entry => !steps.contains entry.1.step) := by
  induction steps generalizing run with
  | nil =>
    refine ⟨fun record isRecord => ⟨record, isRecord, by simp, fun _ => rfl⟩, ?_⟩
    exact (List.filter_eq_self.mpr (by simp)).symm
  | cons step laterSteps inductionHypothesis =>
    simp only [List.foldl_cons]
    obtain ⟨laterRecords, laterTimers⟩ := inductionHypothesis (run.finishStep step .canceled)
    refine ⟨?_, ?_⟩
    · intro record isRecord
      obtain ⟨finished, isFinished, whenCanceled, whenKept⟩ := laterRecords record isRecord
      obtain ⟨original, isOriginal, isChanged⟩ :=
        (Dex.Run.mem_updateStep (run.disarmTimersOf step) step _ finished).mp isFinished
      refine ⟨original, isOriginal, ?_, ?_⟩
      · intro isCanceled
        by_cases isStep : original.id = step
        · simp only [isStep, ↓reduceIte] at isChanged
          by_cases isLater : finished.id ∈ laterSteps
          · exact whenCanceled isLater
          · rw [whenKept isLater, isChanged]
        · simp only [isStep, ↓reduceIte] at isChanged
          rw [isChanged] at whenCanceled
          rcases List.mem_cons.mp isCanceled with isHead | isLater
          · exact absurd isHead isStep
          · exact whenCanceled isLater
      · intro isKept
        have isOtherStep : original.id ≠ step := fun isStep =>
          isKept (by rw [isStep]; exact List.mem_cons_self ..)
        simp only [isOtherStep, ↓reduceIte] at isChanged
        rw [isChanged] at whenKept
        exact whenKept (fun isLater => isKept (List.mem_cons_of_mem _ isLater))
    · rw [laterTimers]
      show (run.timers.filter (fun entry => !decide (entry.1.step = step))).filter
        (fun entry => !laterSteps.contains entry.1.step) = _
      rw [List.filter_filter]
      apply List.filter_congr
      intro entry _
      by_cases isStep : entry.1.step = step <;> simp [isStep]

/-! ## The close -/

/-- The configurations after `exit` or `fail` are related: every active Step
is canceled, no thread is left, and the late emits are the effects the stale
tasks still hold. `finished` is the run after `finish(e, CO)` and, for
`exit`, after the exit value was collected. -/
theorem wp3b_close_related (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (run : Dex.Run signature) (thread : L.Thread) (record : Dex.StepRecord signature)
    (task : Dex.Task signature)
    (start : Completing program lConfig dexConfig run thread record task)
    (waitForQuiet : WaitForTasksHaveNoEffects program dexConfig)
    (finished : Dex.Run signature) (closeStatus : Dex.CloseStatus)
    (sameSteps : finished.steps =
      ((releasedRun run record.id).finishStep record.id .completed).steps)
    (sameTimers : finished.timers =
      ((releasedRun run record.id).finishStep record.id .completed).timers)
    (finishedRunning : finished.status = .running) :
    Related program lConfig.closeProgram.stamp
      (withRun (releasedConfig dexConfig task.id) (finished.closeRun closeStatus)) := by
  have related := state.isRelated.running run start.hasRun start.isRunning
  have lInvariant := state.lInvariant
  -- The Steps before the close.
  have finishedCases : ∀ candidate, candidate ∈ finished.steps →
      (candidate ∈ run.steps ∧ candidate.id ≠ record.id) ∨ candidate.invocation = none := by
    intro candidate isCandidate
    rw [sameSteps] at isCandidate
    obtain ⟨released, isReleased, isFinished⟩ := (Dex.Run.mem_updateStep
      ((releasedRun run record.id).disarmTimersOf record.id) record.id _ candidate).mp isCandidate
    obtain ⟨original, isOriginal, isChanged⟩ := (Dex.Run.mem_updateStep
      ({ run with seq := run.seq + 1 } : Dex.Run signature) record.id _ released).mp isReleased
    by_cases isOwn : original.id = record.id
    · simp only [isOwn, ↓reduceIte] at isChanged
      rw [isChanged] at isFinished
      simp only [↓reduceIte] at isFinished
      right
      rw [isFinished]
    · simp only [isOwn, ↓reduceIte] at isChanged
      rw [isChanged] at isFinished
      simp only [isOwn, ↓reduceIte] at isFinished
      left
      rw [isFinished]
      exact ⟨isOriginal, isOwn⟩
  have keptStep : ∀ candidate, candidate ∈ run.steps → candidate.id ≠ record.id →
      candidate ∈ finished.steps := by
    intro candidate isCandidate isOther
    rw [sameSteps]
    apply (Dex.Run.mem_updateStep ((releasedRun run record.id).disarmTimersOf record.id) record.id
      _ candidate).mpr
    refine ⟨candidate, ?_, by simp [isOther]⟩
    exact (Dex.Run.mem_updateStep ({ run with seq := run.seq + 1 } : Dex.Run signature) record.id
      _ candidate).mpr ⟨candidate, isCandidate, by simp [isOther]⟩
  have finishedTimers : ∀ entry, entry ∈ finished.timers →
      entry ∈ run.timers ∧ entry.1.step ≠ record.id := by
    intro entry isEntry
    rw [sameTimers] at isEntry
    have isFiltered : entry ∈ run.timers.filter (fun candidate =>
        !decide (candidate.1.step = record.id)) := isEntry
    obtain ⟨isOld, isOther⟩ := List.mem_filter.mp isFiltered
    exact ⟨isOld, by simpa using isOther⟩
  -- The Steps after the close.
  have isClosed : finished.closeRun closeStatus =
      { (((finished.steps.filter (fun candidate => candidate.phase.isActive)).map
          (fun candidate => candidate.id)).foldl
          (fun current step => current.finishStep step .canceled) finished) with
        status := .closed closeStatus } := by
    simp [Dex.Run.closeRun, finishedRunning]
  obtain ⟨canceledSteps, canceledTimers⟩ := wp3b_cancelAll
    ((finished.steps.filter (fun candidate => candidate.phase.isActive)).map
      (fun candidate => candidate.id)) finished
  have noInvocation : ∀ candidate, candidate ∈ (finished.closeRun closeStatus).steps →
      candidate.invocation = none := by
    intro candidate isCandidate
    rw [isClosed] at isCandidate
    obtain ⟨original, isOriginal, whenCanceled, whenKept⟩ := canceledSteps candidate isCandidate
    by_cases isCanceled : original.id ∈ (finished.steps.filter
        (fun other => other.phase.isActive)).map (fun other => other.id)
    · exact whenCanceled isCanceled
    · rw [whenKept isCanceled]
      have isInactive : original.phase.isActive ≠ true := fun isActive =>
        isCanceled (List.mem_map.mpr ⟨original, List.mem_filter.mpr ⟨isOriginal, isActive⟩, rfl⟩)
      rcases finishedCases original isOriginal with ⟨isOld, _⟩ | isNone
      · cases invocationCase : original.invocation with
        | none => rfl
        | some invocation =>
          exact absurd (related.invocationsAreActive original isOld (by simp [invocationCase]))
            isInactive
      · exact isNone
  have noTimer : (finished.closeRun closeStatus).timers = [] := by
    rw [isClosed]
    show (((finished.steps.filter (fun candidate => candidate.phase.isActive)).map
      (fun candidate => candidate.id)).foldl
      (fun current step => current.finishStep step .canceled) finished).timers = []
    rw [canceledTimers]
    apply List.filter_eq_nil_iff.mpr
    intro entry isEntry
    obtain ⟨isOld, isOther⟩ := finishedTimers entry isEntry
    obtain ⟨fireTime, isArmed⟩ := AssocList.wp3b_get?_isSome_of_mem run.timers entry isOld
    obtain ⟨blocked, chan, since, isBlockedThread, isBlocked, isTimed, _⟩ :=
      (related.timers entry.1 fireTime).mp isArmed
    obtain ⟨candidate, isCandidate, isActive, carries, _⟩ :=
      related.threadsHaveSteps blocked isBlockedThread (by simp [isBlocked])
    have fits := lInvariant.phasesFitCode blocked isBlockedThread
    unfold Thread.PhaseFitsCode at fits
    simp only [isBlocked] at fits
    obtain ⟨⟨seconds, hasTimer⟩, _⟩ := fits
    have shape := (wp3b_carries_iff program blocked candidate (by simp [isBlocked])
      (by simp [isBlocked])).mp carries
    simp only [hasTimer] at shape
    have hasId : candidate.id = entry.1.step := by
      rw [shape.1, ← isTimed]
    have isCanceled : entry.1.step ∈ (finished.steps.filter
        (fun other => other.phase.isActive)).map (fun other => other.id) :=
      List.mem_map.mpr ⟨candidate, List.mem_filter.mpr
        ⟨keptStep candidate isCandidate (by rw [hasId]; exact isOther), isActive⟩, hasId⟩
    simp [isCanceled]
  have lStatus := wp3b_status_running program lConfig dexConfig state.isRelated run start.hasRun
    start.isRunning
  have closedStatus : (finished.closeRun closeStatus).status = .closed closeStatus := by
    rw [isClosed]
  exact
    { clock := state.isRelated.clock
      stamp := by
        show some lConfig.clock = some dexConfig.clock
        rw [state.isRelated.clock]
      status := by
        show ∃ status, (finished.closeRun closeStatus).status = .closed status
        exact ⟨closeStatus, closedStatus⟩
      notStarted := fun noRun => by cases noRun
      running := fun found isFound isRunning => by
        cases isFound
        rw [closedStatus] at isRunning
        cases isRunning
      closed := fun found status isFound _ => by
        cases isFound
        exact
          { lateEmits := by
              show ((dexConfig.tasks.filter (fun other => !decide (other.id = task.id))).flatMap
                (pendingEffects ⟦program⟧)).Perm _
              rw [wp3b_flatMap_filter _ _ dexConfig.tasks (by
                intro other isOther isDropped
                have sameId : other.id = task.id := by simpa using isDropped
                rw [eq_of_nodup_map (fun candidateTask => candidateTask.id) dexConfig.tasks
                  state.tasksDistinct.1 isOther start.isTask sameId]
                exact start.noEffects)]
              exact wp3b_pendingEffects_perm program lConfig dexConfig state run start.hasRun
                start.isRunning waitForQuiet _ (by
                  intro candidate effect
                  cases candidate.phase <;> simp)
            tasksAreStale := by
              intro other _
              show Dex.Config.isTaskCurrent
                (withRun (releasedConfig dexConfig task.id) (finished.closeRun closeStatus))
                other = false
              unfold Dex.Config.isTaskCurrent
              simp only [withRun]
              cases foundCase : (finished.closeRun closeStatus).findStep? other.step with
              | none => rfl
              | some candidate =>
                have isCandidate := (Dex.Run.mem_of_findStep? _ other.step candidate foundCase).1
                simp [noInvocation candidate isCandidate]
            noInvocation := noInvocation
            noTimer := noTimer } }

end DexExpressiveness
