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
  sorry

/-- `out(a)` is simulated by [WORKER-EFFECT] of the emit Step's task. -/
theorem l_thread_deliver_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (tid : Tid) (thread : L.Thread) (isFound : lConfig.findThread? tid = some thread)
    (effect : Val) (isEmitting : thread.phase = .emitting effect)
    (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.thread tid) = some (lEvents, lNext)) :
    IsSimulatedByDex program dexConfig (.thread tid) lEvents lNext := by
  sorry

end DexExpressiveness
