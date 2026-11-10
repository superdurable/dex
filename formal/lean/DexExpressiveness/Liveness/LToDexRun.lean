import DexExpressiveness.Liveness.Silent

/-!
# Liveness, work package L5: the Dex run of an L run

From an infinite run of the L program, this package builds a run of the
translated Flow: `fairActions` of each L label, in order (`DexFollowsL`),
flattened (`Core/BlockRun.lean`). `fairActions` is the simulating actions of
the label (`l_step_simulatedByActions`) followed by the [WORKER-ABANDON] of
the canceled tasks that hold no effect. The package then shows that the Dex
run attempts the Dex form of the same word, has the same projected trace,
and has the same marker (spec §5.5). Fairness is work package L6.

`exists_dexRun_of_lRun` is proved here from the other lemmas of this file and
from `exists_flattening`. The other lemmas are `sorry`; the README lists
them. The proofs may add lemmas to this file. They may not change
`Liveness/Statements.lean`, `Core/BlockRun.lean`, or any file of Stage 2a.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- An L run is followed by a Dex run in blocks of `fairActions`: by
`l_step_simulatedByActions` at every step, then the cleanup, which keeps the
relation and leaves no canceled task without an effect. -/
theorem exists_dexFollowsL (program : Program) (lRun : InfiniteRun L.Config L.Label)
    (isRun : (L.system program).IsRun lRun) : ∃ dexBlocks, DexFollowsL program lRun dexBlocks := by
  sorry

/-- When time diverges in the L run (F1), the Dex blocks hold infinitely many
actions: the block of a clock tick is a clock tick. -/
theorem DexFollowsL.hasInfinitelyManySteps {program : Program}
    {lRun : InfiniteRun L.Config L.Label}
    {dexBlocks : BlockRun (Dex.Config signature) (Dex.Action signature) (Dex.Obs signature)}
    (follows : DexFollowsL program lRun dexBlocks) (isRun : (L.system program).IsRun lRun)
    (timeDiverges : L.Fairness.TimeDiverges lRun) : dexBlocks.HasInfinitelyManySteps := by
  sorry

/-- The Dex run attempts the Dex form of the word the L run attempts. -/
theorem DexRunOfLRun.isRunFor {program : Program} {lRun : InfiniteRun L.Config L.Label}
    {dexBlocks : BlockRun (Dex.Config signature) (Dex.Action signature) (Dex.Obs signature)}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)} {boundary : Nat → Nat}
    (built : DexRunOfLRun program lRun dexBlocks dexRun boundary) (word : Sequence L.Request)
    (isRunFor : (L.system program).IsRunFor lRun word) :
    (Dex.system ⟦program⟧).IsRunFor dexRun (word.map encodeRequest) := by
  sorry

/-- The Dex run has, after projection, the trace of the L run. -/
theorem DexRunOfLRun.isConcatenation_events {program : Program}
    {lRun : InfiniteRun L.Config L.Label}
    {dexBlocks : BlockRun (Dex.Config signature) (Dex.Action signature) (Dex.Obs signature)}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)} {boundary : Nat → Nat}
    (built : DexRunOfLRun program lRun dexBlocks dexRun boundary) (trace : Sequence Event)
    (isTrace : IsConcatenation
      (fun position => ((L.system program).eventsAt lRun position).filterMap some) trace) :
    IsConcatenation
      (fun index => ((Dex.system ⟦program⟧).eventsAt dexRun index).filterMap project) trace := by
  sorry

/-- The Dex run has the marker of the L run (spec §5.5). -/
theorem DexRunOfLRun.hasMarker {program : Program} {lRun : InfiniteRun L.Config L.Label}
    {dexBlocks : BlockRun (Dex.Config signature) (Dex.Action signature) (Dex.Obs signature)}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)} {boundary : Nat → Nat}
    (built : DexRunOfLRun program lRun dexBlocks dexRun boundary) (marker : Marker)
    (hasMarker : (L.system program).HasMarker lRun marker) :
    (Dex.system ⟦program⟧).HasMarker dexRun marker := by
  sorry

/-- An L run in which time diverges has a Dex run built from it. -/
theorem exists_dexRun_of_lRun (program : Program) (lRun : InfiniteRun L.Config L.Label)
    (isRun : (L.system program).IsRun lRun) (timeDiverges : L.Fairness.TimeDiverges lRun) :
    ∃ dexBlocks dexRun boundary, DexRunOfLRun program lRun dexBlocks dexRun boundary := by
  obtain ⟨dexBlocks, follows⟩ := exists_dexFollowsL program lRun isRun
  have isInfinite := follows.hasInfinitelyManySteps isRun timeDiverges
  obtain ⟨dexRun, boundary, isDexRun, isFlattened⟩ :=
    (Dex.system ⟦program⟧).exists_flattening dexBlocks follows.isBlockRun isInfinite
  exact ⟨dexBlocks, dexRun, boundary,
    { isLRun := isRun, follows := follows, isInfinite := isInfinite, isDexRun := isDexRun
      isFlattened := isFlattened }⟩

end DexExpressiveness
