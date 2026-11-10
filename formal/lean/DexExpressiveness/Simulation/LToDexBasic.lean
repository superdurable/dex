import DexExpressiveness.Simulation.LToDexBasicLemmas

/-!
# L to Dex: ticks, requests, timer fires, late emits, and the begin of an action

Work package 2 of the safety proof. Each lemma shows that one kind of L step
is simulated by the translated Flow (`IsSimulatedByDex`):

| L step | Dex actions |
| --- | --- |
| `tick d` | `tick d` |
| `request (start v)` | `request (startFlow start enc(v))` |
| `request (input c v)`, running or closed | `request (publish c enc(v))` |
| `fire count` | `timerFire count` |
| `lateDeliver i` | `workerRun` if needed, then `workerEffect` of a task that still holds that effect |
| `lateDrop i` | `workerAbandon` of a task that still holds that effect |
| `thread t` from `bootPending`, `ready`, or `woken` | `stepStart` of the Step that carries `t` |
| `thread t` from `emitting` | `workerRun` if needed, then `workerEffect` of that Step's task |

`l_tick_simulated` is proved. The others are `sorry`; the README lists them.
The proofs may add lemmas to this file. They may not change
`Simulation/Relation.lean`, `L/Invariant.lean`, or any model file.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- An idle tick is simulated by [TICK]. -/
theorem l_tick_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (duration : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.tick duration) = some (lEvents, lNext)) :
    IsSimulatedByDex program dexConfig (.tick duration) lEvents lNext := by
  simp only [L.system, L.step] at isStep
  split at isStep
  · rename_i isPositive
    injection isStep with isPair
    injection isPair with isEvents isNext
    subst isEvents
    subst isNext
    refine ⟨[.tick duration], [], { dexConfig with clock := dexConfig.clock + duration }, ?_,
      rfl, rfl, ?_⟩
    · simp [TransitionSystem.exec, Dex.system, Dex.step, isPositive]
    · exact
        { clock := by simp [state.isRelated.clock]
          stamp := state.isRelated.stamp
          status := state.isRelated.status
          notStarted := state.isRelated.notStarted
          running := fun run hasRun isRunning =>
            let related := state.isRelated.running run hasRun isRunning
            { shared := related.shared
              cvQueues := related.cvQueues
              inputQueues := related.inputQueues
              mutexTokens := related.mutexTokens
              joinTokens := related.joinTokens
              cancelRequestsAreFlags := related.cancelRequestsAreFlags
              cancelFlagsAreRequests := related.cancelFlagsAreRequests
              cvWakes := related.cvWakes
              timers := related.timers
              timerCounter := related.timerCounter
              outputsEmpty := related.outputsEmpty
              threadsHaveSteps := related.threadsHaveSteps
              stepsHaveThreads := related.stepsHaveThreads
              activeStepsHaveDistinctThreads := related.activeStepsHaveDistinctThreads
              invocationsAreActive := related.invocationsAreActive
              blockingOrder := related.blockingOrder
              tasksAreCurrent := related.tasksAreCurrent }
          closed := fun run status hasRun isClosed =>
            let related := state.isRelated.closed run status hasRun isClosed
            { lateEmits := related.lateEmits
              tasksAreStale := related.tasksAreStale
              noInvocation := related.noInvocation
              noTimer := related.noTimer } }
  · cases isStep

/-- `begin(v)` is simulated by [FLOW-START]. -/
theorem l_start_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (input : Literal) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.request (.start input)) = some (lEvents, lNext)) :
    IsSimulatedByDex program dexConfig (.request (.start input)) lEvents lNext := by
  simp only [L.system, L.step] at isStep
  split at isStep
  · rename_i isNotStarted
    split at isStep
    · rename_i canStamp
      injection isStep with isPair
      injection isPair with isEvents isNext
      subst isEvents
      subst isNext
      have hasNoRun : dexConfig.run = none := by
        have isStatus := state.isRelated.status
        cases hasRun : dexConfig.run with
        | none => rfl
        | some run => simp [isNotStarted, hasRun] at isStatus
      have hasNoTask : dexConfig.tasks = [] := state.isRelated.notStarted hasNoRun
      have canCommit := wp2_canCommit_of_canStamp state.isRelated canStamp
      obtain ⟨noThread, noShared, noMutex, noQueue, noWake, noInput, noFlag, noJoinable,
        noTimer⟩ := state.lInvariant.notStartedIsInitial isNotStarted
      let startRecord : Dex.StepRecord signature :=
        { id := { stepType := StepType.start, number := 1 }
          input := some (DexVal.value input.toVal)
          options := { skipWaitFor := true }
          origin := .flowStart
          phase := .execute
          invocation := some { method := .execute, state := .pending }
          waitState := none
          conditionResults := none }
      let startedRun : Dex.Run signature :=
        { status := .running, outputs := [], steps := [startRecord]
          counters := [(StepType.start, 1)], attributes := [], channels := [], timers := []
          seq := 1 }
      refine ⟨[.request (.startFlow StepType.start (DexVal.value input.toVal))],
        [Dex.Obs.start StepType.start (DexVal.value input.toVal)],
        { dexConfig with run := some startedRun, lastCommitAt := some dexConfig.clock },
        ?_, rfl, rfl, ?_⟩
      · simp [TransitionSystem.exec, Dex.system, Dex.step, hasNoRun, canCommit,
          Dex.Config.commit, Dex.Config.closeEvents, Dex.Run.newStep, Dex.Run.empty,
          AssocList.get?, AssocList.set, translate, startStepDef, startedRun, startRecord]
      · refine
          { clock := state.isRelated.clock
            stamp := by simp [L.Config.stamp, L.Config.begun, state.isRelated.clock]
            status := by simp [L.Config.stamp, L.Config.begun, startedRun]
            notStarted := fun hasRun => by cases hasRun
            running := ?_
            closed := fun run status hasRun isClosed => by
              cases hasRun
              cases isClosed }
        intro run hasRun _
        cases hasRun
        exact
          { shared := fun name => by
              simp [readShared, L.Config.readShared, L.Config.stamp, L.Config.begun, noShared,
                AssocList.get?, startedRun]
            cvQueues := fun cv => by
              simp [readWaiters, L.Config.stamp, L.Config.begun, noQueue, AssocList.get?,
                startedRun]
            inputQueues := fun chan => by
              simp [Dex.Run.pendingMessages, L.Config.pendingInput, L.Config.stamp,
                L.Config.begun, noInput, AssocList.get?, startedRun]
            mutexTokens := fun areBooted => by
              have isBooted := (areBooted _ (List.mem_singleton.mpr rfl)).1
              simp at isBooted
            joinTokens := fun thread => by
              simp [Dex.Run.pendingMessages, L.Config.stamp, L.Config.begun, noJoinable,
                AssocList.get?, startedRun]
            cancelRequestsAreFlags := fun thread isPending => by
              simp [Dex.Run.pendingMessages, AssocList.get?, startedRun] at isPending
            cancelFlagsAreRequests := fun thread isFlagged => by
              simp [L.Config.stamp, L.Config.begun, noFlag] at isFlagged
            cvWakes := fun cv waiter => by
              simp [Dex.Run.pendingMessages, L.Config.stamp, L.Config.begun, noWake,
                AssocList.get?, startedRun]
            timers := fun timer fireTime => by
              simp [L.Config.stamp, L.Config.begun, AssocList.get?, startedRun]
            timerCounter := by
              simp [L.Config.stamp, L.Config.begun, noTimer, AssocList.get?, startedRun]
            outputsEmpty := rfl
            threadsHaveSteps := fun thread isMember _ => by
              simp only [L.Config.stamp, L.Config.begun, List.mem_singleton] at isMember
              subst isMember
              exact ⟨startRecord, List.mem_singleton.mpr rfl, rfl,
                ⟨rfl, input.toVal, rfl, rfl⟩, rfl⟩
            stepsHaveThreads := fun record isMember _ => by
              simp only [startedRun, List.mem_singleton] at isMember
              subst isMember
              exact ⟨_, List.mem_singleton.mpr rfl, by simp, ⟨rfl, input.toVal, rfl, rfl⟩⟩
            activeStepsHaveDistinctThreads := fun first isFirst second isSecond _ _ _ => by
              simp only [startedRun, List.mem_singleton] at isFirst isSecond
              rw [isFirst, isSecond]
            invocationsAreActive := fun record isMember _ => by
              simp only [startedRun, List.mem_singleton] at isMember
              subst isMember
              rfl
            blockingOrder := fun first second firstRecord secondRecord firstOn firstSince
                secondOn secondSince firstWait secondWait isFirst _ _ _ _ _ _ _ isBlocked => by
              simp only [L.Config.stamp, L.Config.begun, List.mem_singleton] at isFirst
              subst isFirst
              cases isBlocked
            tasksAreCurrent := fun task isMember => by
              simp [hasNoTask] at isMember }
    · cases isStep
  · cases isStep

/-- `in(c, v)` and `refused(c, v)` are simulated by [PUBLISH] and its rejection. -/
theorem l_input_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (chan : ExtChan) (value : Literal) (lEvents : List Event) (lNext : L.Config)
    (isStep :
      (L.system program).step lConfig (.request (.input chan value)) = some (lEvents, lNext)) :
    IsSimulatedByDex program dexConfig (.request (.input chan value)) lEvents lNext := by
  sorry

/-- A batch of `fire` actions is simulated by one [TIMER-FIRE] of the same
number of due timers: both sides order the due timers by `(deadline, timer
number)` (spec §8.1, §8.2). -/
theorem l_fire_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (count : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.fire count) = some (lEvents, lNext)) :
    IsSimulatedByDex program dexConfig (.fire count) lEvents lNext := by
  sorry

/-- The named-sequence form of the delivery of a late emit, under the
hypothesis that every task holds at most one effect: a task that holds the
effect runs `wp2_deliverActions`, and the result is related.

`singleEffects` is not part of `IsSimulationState` at the base commit of
this package, and `l_lateDeliver_simulated` is false without it. -/
theorem wp2_lateDeliver_exec (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (singleEffects : ∀ task, task ∈ dexConfig.tasks →
      (pendingEffects ⟦program⟧ task).length ≤ 1)
    (index : Nat) (effect : Val) (isClosed : lConfig.status = .closed)
    (isLate : lConfig.lateEmits[index]? = some effect) :
    ∃ task, task ∈ dexConfig.tasks ∧
      (Dex.system ⟦program⟧).exec dexConfig (wp2_deliverActions task) =
        some ([Dex.Obs.fx task.step task.method effect],
          dexConfig.updateTask task.id (fun _ => wp2_deliveredTask program task)) ∧
      Related program { lConfig with lateEmits := removeAt lConfig.lateEmits index }
        (dexConfig.updateTask task.id (fun _ => wp2_deliveredTask program task)) := by
  obtain ⟨run, status, hasRun, isClosedRun⟩ := wp2_run_of_closed state.isRelated isClosed
  have closedRelated := state.isRelated.closed run status hasRun isClosedRun
  obtain ⟨task, before, after, isTask, hasEffect, isSplit, beforeOther, afterOther, isPerm⟩ :=
    wp2_late_task state singleEffects hasRun isClosedRun index effect isLate
  refine ⟨task, isTask,
    wp2_exec_deliver program dexConfig state.tasksDistinct task isTask
      (state.dexInvariant.tasksComputed task isTask) effect hasEffect, ?_⟩
  have hasTasks := wp2_updateTask_of_split dexConfig task
    (fun _ => wp2_deliveredTask program task) before after isSplit beforeOther afterOther
  exact
    { clock := state.isRelated.clock
      stamp := state.isRelated.stamp
      status := state.isRelated.status
      notStarted := fun hasNoRun => by
        have isAbsent : some run = none := hasRun.symm.trans hasNoRun
        cases isAbsent
      running := fun newRun hasNewRun isRunning => by
        obtain rfl : run = newRun := Option.some.inj (hasRun.symm.trans hasNewRun)
        rw [isClosedRun] at isRunning
        cases isRunning
      closed := fun newRun newStatus hasNewRun _ => by
        obtain rfl : run = newRun := Option.some.inj (hasRun.symm.trans hasNewRun)
        exact
          { lateEmits := by
              rw [hasTasks]
              simpa [List.flatMap_append, wp2_deliveredTask, pendingEffects] using isPerm
            tasksAreStale := fun other isOther => by
              obtain ⟨original, isOriginal, isChanged⟩ := mem_updateTask _ _ _ _ isOther
              have wasStale := closedRelated.tasksAreStale original isOriginal
              by_cases sameId : original.id = task.id
              · have isTaskOriginal : original = task :=
                  eq_of_nodup_map (fun current => current.id) dexConfig.tasks
                    state.tasksDistinct.1 isOriginal isTask sameId
                rw [isTaskOriginal] at wasStale
                simp only [sameId, ↓reduceIte] at isChanged
                rw [isChanged]
                exact wasStale
              · simp only [sameId, ↓reduceIte] at isChanged
                rw [isChanged]
                exact wasStale
            noInvocation := closedRelated.noInvocation
            noTimer := closedRelated.noTimer } }

/-- `l_lateDeliver_simulated` under the hypothesis that every task holds at
most one effect. -/
theorem wp2_lateDeliver_simulated_of_singleEffects (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (singleEffects : ∀ task, task ∈ dexConfig.tasks →
      (pendingEffects ⟦program⟧ task).length ≤ 1)
    (index : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.lateDeliver index) = some (lEvents, lNext)) :
    IsSimulatedByDex program dexConfig (.lateDeliver index) lEvents lNext := by
  simp only [L.system, L.step] at isStep
  split at isStep
  · rename_i effect isClosed isLate
    injection isStep with isPair
    injection isPair with isEvents isNext
    subst isEvents
    subst isNext
    obtain ⟨task, _, isExec, isRelated⟩ := wp2_lateDeliver_exec program lConfig dexConfig state
      singleEffects index effect isClosed isLate
    exact ⟨_, _, _, isExec, rfl, wp2_deliverActions_requests program task, isRelated⟩
  · cases isStep

/-- The named-sequence form of the drop of a late emit, under the hypothesis
that every task holds at most one effect: [WORKER-ABANDON] of a task that
holds the effect, and the result is related.

`singleEffects` is not part of `IsSimulationState` at the base commit of
this package, and `l_lateDrop_simulated` is false without it. -/
theorem wp2_lateDrop_exec (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (singleEffects : ∀ task, task ∈ dexConfig.tasks →
      (pendingEffects ⟦program⟧ task).length ≤ 1)
    (index : Nat) (effect : Val) (isClosed : lConfig.status = .closed)
    (isLate : lConfig.lateEmits[index]? = some effect) :
    ∃ task, task ∈ dexConfig.tasks ∧
      (Dex.system ⟦program⟧).exec dexConfig [.workerAbandon task.id] =
        some ([], dexConfig.removeTask task.id) ∧
      Related program { lConfig with lateEmits := removeAt lConfig.lateEmits index }
        (dexConfig.removeTask task.id) := by
  obtain ⟨run, status, hasRun, isClosedRun⟩ := wp2_run_of_closed state.isRelated isClosed
  have closedRelated := state.isRelated.closed run status hasRun isClosedRun
  obtain ⟨task, before, after, isTask, _, isSplit, beforeOther, afterOther, isPerm⟩ :=
    wp2_late_task state singleEffects hasRun isClosedRun index effect isLate
  have isFound := wp2_findTask?_of_mem state.tasksDistinct isTask
  have isStale := closedRelated.tasksAreStale task isTask
  refine ⟨task, isTask, ?_, ?_⟩
  · simp [TransitionSystem.exec, Dex.system, Dex.step, isFound, isStale]
  · have hasTasks := wp2_removeTask_of_split dexConfig task before after isSplit beforeOther
      afterOther
    exact
      { clock := state.isRelated.clock
        stamp := state.isRelated.stamp
        status := state.isRelated.status
        notStarted := fun hasNoRun => by
          have isAbsent : some run = none := hasRun.symm.trans hasNoRun
          cases isAbsent
        running := fun newRun hasNewRun isRunning => by
          obtain rfl : run = newRun := Option.some.inj (hasRun.symm.trans hasNewRun)
          rw [isClosedRun] at isRunning
          cases isRunning
        closed := fun newRun newStatus hasNewRun _ => by
          obtain rfl : run = newRun := Option.some.inj (hasRun.symm.trans hasNewRun)
          exact
            { lateEmits := by
                rw [hasTasks]
                exact isPerm
              tasksAreStale := fun other isOther =>
                closedRelated.tasksAreStale other (List.mem_filter.mp isOther).1
              noInvocation := closedRelated.noInvocation
              noTimer := closedRelated.noTimer } }

/-- `l_lateDrop_simulated` under the hypothesis that every task holds at most
one effect. -/
theorem wp2_lateDrop_simulated_of_singleEffects (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (singleEffects : ∀ task, task ∈ dexConfig.tasks →
      (pendingEffects ⟦program⟧ task).length ≤ 1)
    (index : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.lateDrop index) = some (lEvents, lNext)) :
    IsSimulatedByDex program dexConfig (.lateDrop index) lEvents lNext := by
  simp only [L.system, L.step] at isStep
  split at isStep
  · rename_i effect isClosed isLate
    injection isStep with isPair
    injection isPair with isEvents isNext
    subst isEvents
    subst isNext
    obtain ⟨task, _, isExec, isRelated⟩ := wp2_lateDrop_exec program lConfig dexConfig state
      singleEffects index effect isClosed isLate
    exact ⟨_, _, _, isExec, rfl, rfl, isRelated⟩
  · cases isStep

/-- The delivery of a late emit is simulated by the [WORKER-EFFECT] of a
task of a canceled Step. -/
theorem l_lateDeliver_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (index : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.lateDeliver index) = some (lEvents, lNext)) :
    IsSimulatedByDex program dexConfig (.lateDeliver index) lEvents lNext := by
  sorry

/-- The drop of a late emit is simulated by [WORKER-ABANDON]. -/
theorem l_lateDrop_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (index : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.lateDrop index) = some (lEvents, lNext)) :
    IsSimulatedByDex program dexConfig (.lateDrop index) lEvents lNext := by
  sorry

/-- The begin of an action is simulated by [STEP-START] of the Step that
carries the thread: the start action of `main`, the begin of a statement or
of a wait, and the begin of the continuation after a wait was decided. A
shared read happens here on both sides (spec §8.2: "a read linearizes at
[STEP-START]"). -/
theorem l_thread_begin_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (tid : Tid) (thread : L.Thread) (isFound : lConfig.findThread? tid = some thread)
    (isPending : thread.phase = .bootPending ∨ thread.phase = .ready ∨
      ∃ outcome, thread.phase = .woken outcome)
    (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.thread tid) = some (lEvents, lNext)) :
    IsSimulatedByDex program dexConfig (.thread tid) lEvents lNext := by
  obtain ⟨isMember, hasTid⟩ := wp2_mem_of_findThread? isFound
  obtain ⟨_, run, hasRun, isRunning⟩ := wp2_run_of_thread state thread isMember
  have related := state.isRelated.running run hasRun isRunning
  have idsNodup := (state.dexInvariant.runInvariant run hasRun).idsNodup
  have fitsCode := state.lInvariant.phasesFitCode thread isMember
  have wasNotBlocked : ∀ on since, thread.phase ≠ .blocked on since := by
    intro on since
    rcases isPending with phaseCase | phaseCase | ⟨outcome, phaseCase⟩ <;> simp [phaseCase]
  obtain ⟨record, isRecord, isActive, carries, isMatch⟩ :=
    related.threadsHaveSteps thread isMember (by
      rcases isPending with phaseCase | phaseCase | ⟨outcome, phaseCase⟩ <;> simp [phaseCase])
  have isFoundRecord : run.findStep? record.id = some record :=
    Dex.Run.findStep?_of_mem_nodup run record isRecord idsNodup
  -- [STEP-START] of the Step that carries the thread simulates the begin.
  have finish : ∀ (method : Dex.Method) (newPhase : Phase),
      record.invocation = some { method := method, state := .pending } →
      lConfig.canStamp = true →
      wp2_isBootPhase newPhase = wp2_isBootPhase thread.phase →
      (∀ on since, newPhase ≠ .blocked on since) → newPhase ≠ .halted →
      (thread.phase = .woken .canceled ∨ thread.phase = .resuming .canceled →
        newPhase = .woken .canceled ∨ newPhase = .resuming .canceled) →
      PhaseMatchesStep program lConfig (wp2_startedConfig dexConfig run record method)
        { thread with phase := newPhase } (wp2_startedRecord dexConfig record method) →
      IsSimulatedByDex program dexConfig (.thread tid) []
        (lConfig.setPhase tid newPhase).stamp := by
    intro method newPhase isPendingInvocation canStamp sameBoot isNotBlocked isNotHalted
      keepsCanceled newMatch
    exact ⟨[.stepStart record.id], [], _,
      wp2_exec_stepStart ⟦program⟧ dexConfig run hasRun isRunning record isFoundRecord method
        isPendingInvocation (wp2_canCommit_of_canStamp state.isRelated canStamp),
      rfl, rfl,
      wp2_stepStart_related state isFound hasRun isRunning isRecord isActive carries method
        isPendingInvocation newPhase sameBoot wasNotBlocked isNotBlocked isNotHalted
        keepsCanceled newMatch⟩
  simp only [L.system, L.step, isFound, stepThread, hasTid] at isStep
  unfold PhaseMatchesStep at isMatch
  rw [wp2_stepCarriesThread_iff] at carries
  rcases isPending with phaseCase | phaseCase | ⟨outcome, phaseCase⟩
  · -- The start action of `main` begins.
    simp only [phaseCase] at isStep isMatch
    simp only [phaseCase, wp2_isBootPhase, ↓reduceIte] at carries
    obtain ⟨isStart, _⟩ := carries
    by_cases canStamp : lConfig.canStamp = true
    · simp only [canStamp, ↓reduceIte, Option.some.injEq, Prod.mk.injEq] at isStep
      obtain ⟨rfl, rfl⟩ := isStep
      refine finish .execute .bootRunning isMatch canStamp (by rw [phaseCase]; rfl) (by simp)
        (by simp) (by simp [phaseCase]) ?_
      unfold PhaseMatchesStep
      refine wp2_hasRunningTask_started _ _ _ _ _ ?_
      rw [wp2_pendingEffects_started_execute, isStart]
      exact wp2_startStep_effects program _ _ _ _
    · simp [canStamp] at isStep
  · -- A statement, or the wait of a blocking statement, begins.
    simp only [phaseCase] at isStep isMatch
    simp only [phaseCase, wp2_isBootPhase] at carries
    obtain ⟨noResults, isPendingInvocation⟩ := isMatch
    by_cases waitsFirst : (thread.timer.isSome || isBlockingHead thread.code) = true
    · simp only [waitsFirst, ↓reduceIte] at isStep isPendingInvocation
      by_cases canStamp : lConfig.canStamp = true
      · simp only [canStamp, ↓reduceIte, Option.some.injEq, Prod.mk.injEq] at isStep
        obtain ⟨rfl, rfl⟩ := isStep
        refine finish .waitFor .waitStarted isPendingInvocation canStamp (by rw [phaseCase]; rfl)
          (by simp) (by simp) (by simp [phaseCase]) ?_
        -- The WaitFor task holds no effect; the relation may or may not ask for it.
        have hasTask : HasRunningTask (wp2_startedConfig dexConfig run record .waitFor)
            (wp2_startedRecord dexConfig record .waitFor) .waitFor
            (fun task => pendingEffects ⟦program⟧ task = []) :=
          wp2_hasRunningTask_started _ _ _ _ _
            (wp2_compute_waitFor_effects program _ rfl)
        unfold PhaseMatchesStep
        first
          | exact hasTask
          | exact wp2_hasRunningTask_imp (fun _ _ => trivial) hasTask
      · simp [canStamp] at isStep
    · simp only [waitsFirst] at isStep isPendingInvocation
      have isNotTimed : thread.timer = none := by
        cases timerCase : thread.timer with
        | none => rfl
        | some timer => simp [timerCase] at waitsFirst
      have isNotBlocking : isBlockingHead thread.code = false := by
        cases blockingCase : isBlockingHead thread.code with
        | false => rfl
        | true => simp [blockingCase] at waitsFirst
      simp only [isNotTimed, Bool.false_eq_true, ↓reduceIte] at carries
      obtain ⟨isPoint, hasInput, hasSelection⟩ := carries
      -- The effects of the started Execute task.
      have hasEffects : pendingEffects ⟦program⟧ (wp2_startedTask dexConfig run record .execute) =
          match beginKind thread.core thread.code with
          | .emit effect => [effect]
          | _ => [] := by
        rw [wp2_pendingEffects_started_execute, isPoint, hasInput]
        exact wp2_pointStep_effects program thread.core thread.code isNotBlocking _ _ _
      have hasWaiters : ∀ cv, cvOperand thread.core thread.code = some cv →
          readWaiters (wp2_startedTask dexConfig run record .execute).snapshot cv =
            (lConfig.cvQueues.get? cv).getD [] := by
        intro cv isOperand
        exact (wp2_readWaiters_snapshot run thread.core thread.code record.options hasSelection
          cv isOperand).trans (related.cvQueues cv)
      by_cases holdsMutex : lConfig.holdsRequiredMutex thread = true
      · simp only [holdsMutex, ↓reduceIte] at isStep
        cases kindCase : beginKind thread.core thread.code with
        | plain =>
          simp only [kindCase] at isStep hasEffects
          by_cases canStamp : lConfig.canStamp = true
          · simp only [canStamp, ↓reduceIte] at isStep
            obtain ⟨rfl, rfl⟩ := isStep
            refine finish .execute (.running .null) isPendingInvocation canStamp
              (by rw [phaseCase]; rfl) (by simp) (by simp) (by simp [phaseCase]) ?_
            unfold PhaseMatchesStep
            refine wp2_hasRunningTask_started _ _ _ _ _ ⟨hasEffects, noResults, ?_, hasWaiters⟩
            simp only [kindCase]
          · simp [canStamp] at isStep
        | load source =>
          simp only [kindCase] at isStep hasEffects
          by_cases canStamp : lConfig.canStamp = true
          · simp only [canStamp, ↓reduceIte] at isStep
            obtain ⟨rfl, rfl⟩ := isStep
            refine finish .execute (.running (lConfig.readShared source)) isPendingInvocation
              canStamp (by rw [phaseCase]; rfl) (by simp) (by simp) (by simp [phaseCase]) ?_
            unfold PhaseMatchesStep
            refine wp2_hasRunningTask_started _ _ _ _ _ ⟨hasEffects, noResults, ?_, hasWaiters⟩
            simp only [kindCase]
            exact (readShared_snapshot run record.options .execute source).trans
              (related.shared source)
          · simp [canStamp] at isStep
        | emit effect =>
          simp only [kindCase] at isStep hasEffects
          by_cases canStamp : lConfig.canStamp = true
          · simp only [canStamp, ↓reduceIte] at isStep
            obtain ⟨rfl, rfl⟩ := isStep
            refine finish .execute (.emitting effect) isPendingInvocation canStamp
              (by rw [phaseCase]; rfl) (by simp) (by simp) (by simp [phaseCase]) ?_
            unfold PhaseMatchesStep
            exact wp2_hasRunningTask_started _ _ _ _ _ ⟨hasEffects, noResults⟩
          · simp [canStamp] at isStep
      · simp [holdsMutex] at isStep
  · -- The continuation after a decided wait begins.
    simp only [phaseCase] at isStep isMatch
    simp only [phaseCase, wp2_isBootPhase] at carries
    obtain ⟨_, isPendingInvocation, isOutcome⟩ := isMatch
    by_cases canStamp : lConfig.canStamp = true
    · simp only [canStamp, ↓reduceIte, Option.some.injEq, Prod.mk.injEq] at isStep
      obtain ⟨rfl, rfl⟩ := isStep
      refine finish .execute (.resuming outcome) isPendingInvocation canStamp
        (by rw [phaseCase]; rfl) (by simp) (by simp) (by simp [phaseCase]) ?_
      have hasEffects :
          pendingEffects ⟦program⟧ (wp2_startedTask dexConfig run record .execute) = [] := by
        rw [wp2_pendingEffects_started_execute]
        cases timerCase : thread.timer with
        | some timer =>
          obtain ⟨number, seconds⟩ := timer
          simp only [timerCase, Bool.false_eq_true, ↓reduceIte] at carries
          rw [carries.1]
          exact wp2_timedStep_effects _ _ _ _
        | none =>
          simp only [timerCase, Bool.false_eq_true, ↓reduceIte] at carries
          have isBlocking : isBlockingHead thread.code = true := by
            unfold L.Thread.PhaseFitsCode at fitsCode
            simpa [phaseCase, timerCase] using fitsCode
          rw [carries.1]
          exact wp2_pointStep_effects_of_blocking program thread.code isBlocking _ _ _ _
      unfold PhaseMatchesStep
      exact wp2_hasRunningTask_started _ _ _ _ _ ⟨hasEffects, isOutcome⟩
    · simp [canStamp] at isStep

/-- The named-sequence form of `l_thread_deliver_simulated`: the task of the
emit Step runs `wp2_deliverActions` (its handler, if it has not run, then
[WORKER-EFFECT]), and the result is related. -/
theorem wp2_thread_deliver_exec (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (tid : Tid) (thread : L.Thread) (isFound : lConfig.findThread? tid = some thread)
    (effect : Val) (isEmitting : thread.phase = .emitting effect) :
    ∃ task, task ∈ dexConfig.tasks ∧
      (Dex.system ⟦program⟧).exec dexConfig (wp2_deliverActions task) =
        some ([Dex.Obs.fx task.step task.method effect],
          dexConfig.updateTask task.id (fun _ => wp2_deliveredTask program task)) ∧
      Related program (lConfig.setPhase tid .emitted)
        (dexConfig.updateTask task.id (fun _ => wp2_deliveredTask program task)) := by
  obtain ⟨isMember, hasTid⟩ := wp2_mem_of_findThread? isFound
  obtain ⟨_, run, hasRun, isRunning⟩ := wp2_run_of_thread state thread isMember
  have related := state.isRelated.running run hasRun isRunning
  have idsNodup := (state.dexInvariant.runInvariant run hasRun).idsNodup
  obtain ⟨record, isRecord, isActive, carries, isMatch⟩ :=
    related.threadsHaveSteps thread isMember (by simp [isEmitting])
  unfold PhaseMatchesStep at isMatch
  simp only [isEmitting] at isMatch
  obtain ⟨task, isTask, hasInvocation, isTaskStep, isMethod, sameInput, hasEffect, noResults⟩ :=
    isMatch
  refine ⟨task, isTask,
    wp2_exec_deliver program dexConfig state.tasksDistinct task isTask
      (state.dexInvariant.tasksComputed task isTask) effect hasEffect, ?_⟩
  have sameTask : ∀ other, other ∈ dexConfig.tasks → other.id = task.id → other = task :=
    fun other isOther sameId =>
      eq_of_nodup_map (fun current => current.id) dexConfig.tasks state.tasksDistinct.1 isOther
        isTask sameId
  have frame : RunningRelated program (lConfig.setPhase tid .emitted)
      (dexConfig.updateTask task.id (fun _ => wp2_deliveredTask program task)) run := by
    refine wp2_runningRelated_setPhase related state.lInvariant idsNodup isFound isRecord
      isActive carries .emitted (by rw [isEmitting]; rfl) (by simp [isEmitting]) (by simp)
      (by simp) (by simp [isEmitting]) id (List.map_id _).symm
      (fun _ => ⟨rfl, rfl, rfl, rfl, rfl⟩) (fun _ _ => rfl) rfl rfl rfl rfl rfl ?_ ?_ ?_
    · intro other isOther otherStep
      refine List.mem_map.mpr ⟨other, isOther, ?_⟩
      have otherId : other.id ≠ task.id := by
        intro sameId
        rw [sameTask other isOther sameId] at otherStep
        exact otherStep isTaskStep
      simp [otherId]
    · unfold PhaseMatchesStep
      exact ⟨wp2_deliveredTask program task, List.mem_map.mpr ⟨task, isTask, by simp⟩,
        hasInvocation, isTaskStep, isMethod, sameInput, rfl, noResults⟩
    · intro other isOther
      obtain ⟨original, isOriginal, isChanged⟩ := mem_updateTask _ _ _ _ isOther
      have wasCurrent := related.tasksAreCurrent original isOriginal
      by_cases sameId : original.id = task.id
      · rw [sameTask original isOriginal sameId] at wasCurrent
        simp only [sameId, ↓reduceIte] at isChanged
        rw [isChanged]
        exact wasCurrent
      · simp only [sameId, ↓reduceIte] at isChanged
        rw [isChanged]
        exact wasCurrent
  exact
    { clock := state.isRelated.clock
      stamp := state.isRelated.stamp
      status := state.isRelated.status
      notStarted := fun hasNoRun => by
        have isAbsent : some run = none := hasRun.symm.trans hasNoRun
        cases isAbsent
      running := fun newRun hasNewRun _ => by
        obtain rfl : run = newRun := Option.some.inj (hasRun.symm.trans hasNewRun)
        exact frame
      closed := fun newRun status hasNewRun isClosed => by
        obtain rfl : run = newRun := Option.some.inj (hasRun.symm.trans hasNewRun)
        rw [isRunning] at isClosed
        cases isClosed }

/-- `out(a)` is simulated by [WORKER-EFFECT] of the emit Step's task. -/
theorem l_thread_deliver_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (tid : Tid) (thread : L.Thread) (isFound : lConfig.findThread? tid = some thread)
    (effect : Val) (isEmitting : thread.phase = .emitting effect)
    (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.thread tid) = some (lEvents, lNext)) :
    IsSimulatedByDex program dexConfig (.thread tid) lEvents lNext := by
  simp only [L.system, L.step, isFound, stepThread, isEmitting] at isStep
  injection isStep with isPair
  injection isPair with isEvents isNext
  subst isEvents
  subst isNext
  obtain ⟨task, _, isExec, isRelated⟩ :=
    wp2_thread_deliver_exec program lConfig dexConfig state tid thread isFound effect isEmitting
  rw [(wp2_mem_of_findThread? isFound).2]
  exact ⟨_, _, _, isExec, rfl, wp2_deliverActions_requests program task, isRelated⟩

end DexExpressiveness
