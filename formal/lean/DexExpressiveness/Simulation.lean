import DexExpressiveness.L.InvariantStep
import DexExpressiveness.Simulation.DexToL

/-!
# The simulation between an L program and its translated Flow

This file gathers the proof of the step simulations and states the two
inclusions of liveness.

| File | Content |
| --- | --- |
| `Simulation/Relation.lean` | `Related` and the statements `IsSimulatedByDex`, `IsSimulatedByL`. Fixed. |
| `L/Invariant.lean` | `L.Invariant`. Fixed. |
| `L/InvariantStep.lean` | Work package 1: every L transition keeps `L.Invariant`. |
| `Simulation/LToDexBasic.lean` | Work package 2. |
| `Simulation/LToDexComplete.lean` | Work package 3. |
| `Simulation/LToDex.lean` | `l_step_simulated`, proved from packages 2 and 3. |
| `Simulation/DexToL.lean` | Work package 4: `dex_step_simulated`. |

`FiniteTraces.lean` proves the two inclusions of safety from the two step
simulations and the two invariants. The two inclusions of liveness below are
`sorry` until Stage 2b.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- Liveness, Dex to L: every fair maximal Dex trace with its marker, for an
admissible word, is one of L. Needs the transfer of F1 to F6 and of the marker
along the simulation, and the removal of the handler steps L does not see. -/
theorem fairMaximalTrace_dex_to_l (program : Program) (isWellFormed : program.IsWellFormed)
    (word : Sequence L.Request) (isAdmissible : L.IsAdmissibleWord word)
    (trace : Sequence Event) (marker : Marker)
    (isDexTrace : IsProjectedDexFairMaximalTrace program word trace marker) :
    IsLFairMaximalTrace program word trace marker := by
  sorry

/-- Liveness, L to Dex: every fair maximal L trace with its marker, for an
admissible word, is one of the translated Flow. Needs the same transfers in
the other direction, and a fair schedule of the inserted handler steps. -/
theorem fairMaximalTrace_l_to_dex (program : Program) (isWellFormed : program.IsWellFormed)
    (word : Sequence L.Request) (isAdmissible : L.IsAdmissibleWord word)
    (trace : Sequence Event) (marker : Marker)
    (isLTrace : IsLFairMaximalTrace program word trace marker) :
    IsProjectedDexFairMaximalTrace program word trace marker := by
  sorry

end DexExpressiveness
