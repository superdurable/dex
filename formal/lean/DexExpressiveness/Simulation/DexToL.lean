import DexExpressiveness.Simulation.LToDex
import DexExpressiveness.Simulation.DexToLLemmas

/-!
# Dex to L: every Dex step is simulated by the L program

Work package 4 of the safety proof. `dex_step_simulated` is `sorry`; the
README lists it. `dex_step_simulated_of_observed` reduces it to the steps of
`IsObservedWhileRunning`: every other enabled step is proved simulated in
`Simulation/DexToLLemmas.lean` (a tick, the handler steps, the stale drops,
and every step after the close).

The plan for the steps of `IsObservedWhileRunning` uses the L to Dex lemmas
and determinism instead of a second case analysis of the relation. Each of
these Dex actions has one L label that it simulates: the start of the Flow,
and, while the Flow runs, a publication, [STEP-START], [WORKER-EFFECT], a
Step completion, and [TIMER-FIRE]. For each:

1. Show that the L label is enabled. This is where
   `¬ BreaksMutexRequirement` is needed, for [STEP-START].
2. Apply `l_step_simulatedByActions` to it. It names the simulating Dex
   actions (`simulatingActions`).
3. Show that the simulating actions are the given action alone. The lemmas
   `simulatingActions_begin`, `simulatingActions_deliver`, and
   `simulatingActions_complete` of `Simulation/RelationLemmas.lean` compute
   the list; for [WORKER-EFFECT] and a Step completion the given action is
   enabled only when the Worker steps before it have happened
   (`runTaskActions` and `returnTaskActions` are then empty).
4. `Dex.step` is a function, so the given step is the simulating step, and
   its result is the related configuration of step 2.

The proof may add lemmas to this file and to `Simulation/DexToLLemmas.lean`.
It may not change `Simulation/Relation.lean`, `L/Invariant.lean`,
`Invariants.lean`, or any model file.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- The Dex steps that `Simulation/DexToLLemmas.lean` does not settle: the
start of the Flow, and, while the Flow runs, a publication, [STEP-START],
[WORKER-EFFECT], a Step completion, and [TIMER-FIRE]. -/
def IsObservedWhileRunning (config : Dex.Config signature) : Dex.Action signature → Prop
  | .request (.startFlow _ _) => config.run = none
  | .request (.publish _ _) => ∃ run, config.run = some run ∧ run.status = .running
  | .stepStart _ => ∃ run, config.run = some run ∧ run.status = .running
  | .workerEffect _ => ∃ run, config.run = some run ∧ run.status = .running
  | .stepComplete _ => ∃ run, config.run = some run ∧ run.status = .running
  | .timerFire _ => ∃ run, config.run = some run ∧ run.status = .running
  | _ => False

/-- `dex_step_simulated` reduced to the steps of `IsObservedWhileRunning`.
Every other enabled step is simulated: a tick, the handler steps, the stale
drops, and every step after the close. -/
theorem dex_step_simulated_of_observed (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (action : Dex.Action signature) (isAdmissible : IsAdmissibleAction dexConfig action)
    (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig action = some (dexEvents, dexNext))
    (observedSimulated : IsObservedWhileRunning dexConfig action →
      IsSimulatedByL program lConfig dexEvents dexNext) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  cases action with
  | tick duration => exact dex_tick_simulated program lConfig dexConfig state duration _ _ isStep
  | request request =>
    cases request with
    | startFlow stepType input =>
      apply observedSimulated
      simp only [Dex.system, Dex.step] at isStep
      split at isStep
      · cases isStep
      · rename_i hasNoRun
        exact hasNoRun
    | publish chan message =>
      cases hasRun : dexConfig.run with
      | none => exact absurd hasRun isAdmissible.1
      | some run =>
        cases isStatus : run.status with
        | running => exact observedSimulated ⟨run, hasRun, isStatus⟩
        | closed status =>
          exact dex_publish_closed_simulated program lConfig dexConfig state run status hasRun
            isStatus chan message isAdmissible _ _ isStep
  | stepStart stepId =>
    apply observedSimulated
    simp only [Dex.system, Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i run hasRun
      refine ⟨run, hasRun, ?_⟩
      cases isStatus : run.status with
      | running => rfl
      | closed status =>
        exfalso
        split at isStep
        · cases isStep
        · rename_i record isFoundStep
          have noInvocation := (state.isRelated.closed run status hasRun isStatus).noInvocation
            record (Dex.Run.mem_of_findStep? run stepId record isFoundStep).1
          rw [noInvocation] at isStep
          cases isStep
  | workerRun taskId =>
    exact dex_workerRun_simulated program lConfig dexConfig state taskId _ _ isStep
  | workerEffect taskId =>
    cases hasRun : dexConfig.run with
    | none =>
      exfalso
      simp [Dex.system, Dex.step, Dex.Config.findTask?, state.isRelated.notStarted hasRun]
        at isStep
    | some run =>
      cases isStatus : run.status with
      | running => exact observedSimulated ⟨run, hasRun, isStatus⟩
      | closed status =>
        exact dex_workerEffect_closed_simulated program lConfig dexConfig state run status hasRun
          isStatus taskId _ _ isStep
  | workerReturn taskId =>
    exact dex_workerReturn_simulated program lConfig dexConfig state taskId _ _ isStep
  | workerAbandon taskId =>
    exact dex_workerAbandon_simulated program lConfig dexConfig state
      state.holdsAtMostOneEffect taskId _ _ isStep
  | stepComplete taskId =>
    apply observedSimulated
    simp only [Dex.system, Dex.step] at isStep
    split at isStep
    · rename_i run task hasRun isFound
      refine ⟨run, hasRun, ?_⟩
      cases isStatus : run.status with
      | running => rfl
      | closed status =>
        exfalso
        have isStale := (state.isRelated.closed run status hasRun isStatus).tasksAreStale task
          (mem_of_findTask? dexConfig taskId task isFound).1
        simp [isStale] at isStep
    · cases isStep
  | stepCompleteStale taskId =>
    exact dex_stepCompleteStale_simulated program lConfig dexConfig state taskId _ _ isStep
  | timerFire count =>
    apply observedSimulated
    simp only [Dex.system, Dex.step] at isStep
    split at isStep
    · cases isStep
    · rename_i run hasRun
      refine ⟨run, hasRun, ?_⟩
      cases isStatus : run.status with
      | running => rfl
      | closed status =>
        exfalso
        have noTimer := (state.isRelated.closed run status hasRun isStatus).noTimer
        split at isStep
        · rename_i isEnabled
          simp [Dex.Run.dueTimers, noTimer, sortStable] at isEnabled
          omega
        · cases isStep

/-- Dex to L: every admissible Dex step from related configurations that
satisfy their invariants is simulated by at most one L label with the same
projected events, and the results are related. (Running and returning a
handler are simulated by no L label.) No thread of the L configuration is
about to break a mutex requirement, which holds for the reachable
configurations of a well-formed program. -/
theorem dex_step_simulated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (breaksNoRequirement : ¬ BreaksMutexRequirement lConfig)
    (action : Dex.Action signature) (isAdmissible : IsAdmissibleAction dexConfig action)
    (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig action = some (dexEvents, dexNext)) :
    IsSimulatedByL program lConfig dexEvents dexNext := by
  sorry

end DexExpressiveness
