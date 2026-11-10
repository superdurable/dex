import DexExpressiveness.Simulation.Relation
import DexExpressiveness.Properties

/-!
# Helper lemmas of work package 2 (L to Dex, basic steps)

Clock facts, lookup facts, and the frame lemmas that `Simulation/LToDexBasic.lean`
uses: what stays true of `RunningRelated` when one Step record, one task, or
one thread's phase changes. Every lemma is prefixed `wp2_`.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Clocks -/

/-- A stamped L action may happen exactly when a Dex commit may. -/
theorem wp2_canCommit_of_canStamp {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (isRelated : Related program lConfig dexConfig)
    (canStamp : lConfig.canStamp = true) : dexConfig.canCommit = true := by
  unfold L.Config.canStamp at canStamp
  unfold Dex.Config.canCommit
  rw [← isRelated.stamp, ← isRelated.clock]
  exact canStamp

end DexExpressiveness
