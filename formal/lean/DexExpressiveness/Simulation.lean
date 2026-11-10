import DexExpressiveness.L.InvariantStep
import DexExpressiveness.Simulation.DexToL

/-!
# The simulation between an L program and its translated Flow

This file gathers the proof of the step simulations.

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
simulations and the invariants. `Liveness.lean` holds the two inclusions of
liveness.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

end DexExpressiveness
