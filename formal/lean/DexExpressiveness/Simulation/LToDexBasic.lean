import DexExpressiveness.Simulation.RelationLemmas

/-!
# L to Dex: ticks, requests, timer fires, late emits, and the begin of an action

Work package 2 of the safety proof. Each lemma shows that one kind of L step
is simulated by exactly the Dex actions that `simulatingActions` names
(`IsSimulatedByActions`):

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
The proofs may add lemmas to this file and to
`Simulation/RelationLemmas.lean`. They may not change
`Simulation/Relation.lean`, `L/Invariant.lean`, `Invariants.lean`, or any
model file.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- An idle tick is simulated by [TICK]. -/
theorem l_tick_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (duration : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.tick duration) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.tick duration) lEvents lNext := by
  simp only [L.system, L.step] at isStep
  split at isStep
  · rename_i isPositive
    injection isStep with isPair
    injection isPair with isEvents isNext
    subst isEvents
    subst isNext
    refine ⟨[], { dexConfig with clock := dexConfig.clock + duration }, ?_, rfl, ?_⟩
    · simp [simulatingActions, TransitionSystem.exec, Dex.system, Dex.step, isPositive]
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
              mutexesBeforeBoot := related.mutexesBeforeBoot
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
    IsSimulatedByActions program lConfig dexConfig (.request (.start input)) lEvents lNext := by
  sorry

/-- `in(c, v)` and `refused(c, v)` are simulated by [PUBLISH] and its rejection. -/
theorem l_input_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (chan : ExtChan) (value : Literal) (lEvents : List Event) (lNext : L.Config)
    (isStep :
      (L.system program).step lConfig (.request (.input chan value)) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.request (.input chan value)) lEvents lNext := by
  sorry

/-- A batch of `fire` actions is simulated by one [TIMER-FIRE] of the same
number of due timers: both sides order the due timers by `(deadline, timer
number)` (spec §8.1, §8.2). -/
theorem l_fire_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (count : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.fire count) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.fire count) lEvents lNext := by
  sorry

/-- The delivery of a late emit is simulated by the [WORKER-EFFECT] of a
task of a canceled Step. -/
theorem l_lateDeliver_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (index : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.lateDeliver index) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.lateDeliver index) lEvents lNext := by
  sorry

/-- The drop of a late emit is simulated by [WORKER-ABANDON]. -/
theorem l_lateDrop_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (index : Nat) (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.lateDrop index) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.lateDrop index) lEvents lNext := by
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
    IsSimulatedByActions program lConfig dexConfig (.thread tid) lEvents lNext := by
  sorry

/-- `out(a)` is simulated by [WORKER-EFFECT] of the emit Step's task. -/
theorem l_thread_deliver_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (tid : Tid) (thread : L.Thread) (isFound : lConfig.findThread? tid = some thread)
    (effect : Val) (isEmitting : thread.phase = .emitting effect)
    (lEvents : List Event) (lNext : L.Config)
    (isStep : (L.system program).step lConfig (.thread tid) = some (lEvents, lNext)) :
    IsSimulatedByActions program lConfig dexConfig (.thread tid) lEvents lNext := by
  sorry

end DexExpressiveness
