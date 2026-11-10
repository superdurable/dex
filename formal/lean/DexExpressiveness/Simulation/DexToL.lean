import DexExpressiveness.Simulation.LToDex

/-!
# Dex to L: every Dex step is simulated by the L program

Work package 4 of the safety proof. `dex_step_simulated` is `sorry`; the
README lists it.

The plan uses `l_step_simulated` and determinism instead of a second case
analysis of the relation:

1. Six kinds of Dex action have an L label that they simulate: `tick`,
   `request`, `stepStart`, `workerEffect`, `stepComplete`, `timerFire`. For
   each, show that the L label is enabled (this is where
   `¬ BreaksMutexRequirement` is needed, for `stepStart`), apply
   `l_step_simulated` to it, and show that the simulating Dex actions are the
   given action, so that the given step is the simulating step
   (`step_deterministic`).
2. Four kinds have no L label or only a late one: `workerRun`,
   `workerReturn`, `stepCompleteStale`, and `workerAbandon`. For these, show
   directly that `Related` is kept (`workerRun`, `workerReturn`, and the
   stale drop change only a task's phase, which `Related` reads through
   `pendingEffects`), or that the step is a `lateDrop`.

The proof may add lemmas to this file. It may not change
`Simulation/Relation.lean`, `L/Invariant.lean`, or any model file.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

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
