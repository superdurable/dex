import DexExpressiveness.Liveness.Silent

/-!
# Liveness, work package L3: the L run of a Dex run

From an infinite run of the translated Flow for an admissible word, this
package builds an L run: the L labels that simulate each Dex step, in order
(`LFollowsDex`), flattened (`Core/BlockRun.lean`). It then shows that the L
run attempts the same word, has the same trace, and has the same marker
(spec §5.5). Fairness is work package L4.

`exists_lRun_of_dexRun` is proved here from the other lemmas of this file and
from `exists_flattening`. The other lemmas are `sorry`; the README lists
them. The proofs may add lemmas to this file. They may not change
`Liveness/Statements.lean`, `Core/BlockRun.lean`, or any file of Stage 2a.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- A Dex run for an admissible word is followed by an L run in blocks: by
`dex_step_simulated` at every step, from the initial simulation state. The
program is well formed, so no L configuration on the way has a thread about
to break a mutex requirement. -/
theorem exists_lFollowsDex (program : Program) (isWellFormed : program.IsWellFormed)
    (word : Sequence L.Request) (isAdmissible : L.IsAdmissibleWord word)
    (dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature))
    (isRun : (Dex.system ⟦program⟧).IsRun dexRun)
    (isRunFor : (Dex.system ⟦program⟧).IsRunFor dexRun (word.map encodeRequest)) :
    ∃ lBlocks, LFollowsDex program dexRun lBlocks := by
  sorry

/-- When time diverges in the Dex run (F1), the L blocks hold infinitely many
labels: every clock tick is simulated by one. -/
theorem LFollowsDex.hasInfinitelyManySteps {program : Program}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)}
    {lBlocks : BlockRun L.Config L.Label Event} (follows : LFollowsDex program dexRun lBlocks)
    (isRun : (Dex.system ⟦program⟧).IsRun dexRun)
    (timeDiverges : Dex.Fairness.TimeDiverges dexRun) : lBlocks.HasInfinitelyManySteps := by
  sorry

/-- The L run attempts the word whose Dex form the Dex run attempts. -/
theorem LRunOfDexRun.isRunFor {program : Program}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)}
    {lBlocks : BlockRun L.Config L.Label Event} {lRun : InfiniteRun L.Config L.Label}
    {boundary : Nat → Nat} (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (word : Sequence L.Request)
    (isRunFor : (Dex.system ⟦program⟧).IsRunFor dexRun (word.map encodeRequest)) :
    (L.system program).IsRunFor lRun word := by
  sorry

/-- The L run has the projected trace of the Dex run. -/
theorem LRunOfDexRun.isConcatenation_events {program : Program}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)}
    {lBlocks : BlockRun L.Config L.Label Event} {lRun : InfiniteRun L.Config L.Label}
    {boundary : Nat → Nat} (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (trace : Sequence Event)
    (isTrace : IsConcatenation
      (fun index => ((Dex.system ⟦program⟧).eventsAt dexRun index).filterMap project) trace) :
    IsConcatenation
      (fun position => ((L.system program).eventsAt lRun position).filterMap some) trace := by
  sorry

/-- The L run has the marker of the Dex run (spec §5.5). For `∞` this uses
the silent weight: a Dex run cannot take infinitely many progress steps that
L does not see without one that L sees. -/
theorem LRunOfDexRun.hasMarker {program : Program}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)}
    {lBlocks : BlockRun L.Config L.Label Event} {lRun : InfiniteRun L.Config L.Label}
    {boundary : Nat → Nat} (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (marker : Marker) (hasMarker : (Dex.system ⟦program⟧).HasMarker dexRun marker) :
    (L.system program).HasMarker lRun marker := by
  sorry

/-- A Dex run for an admissible word in which time diverges has an L run
built from it. -/
theorem exists_lRun_of_dexRun (program : Program) (isWellFormed : program.IsWellFormed)
    (word : Sequence L.Request) (isAdmissible : L.IsAdmissibleWord word)
    (dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature))
    (isRun : (Dex.system ⟦program⟧).IsRun dexRun)
    (isRunFor : (Dex.system ⟦program⟧).IsRunFor dexRun (word.map encodeRequest))
    (timeDiverges : Dex.Fairness.TimeDiverges dexRun) :
    ∃ lBlocks lRun boundary, LRunOfDexRun program dexRun lBlocks lRun boundary := by
  obtain ⟨lBlocks, follows⟩ :=
    exists_lFollowsDex program isWellFormed word isAdmissible dexRun isRun isRunFor
  have isInfinite := follows.hasInfinitelyManySteps isRun timeDiverges
  obtain ⟨lRun, boundary, isLRun, isFlattened⟩ :=
    (L.system program).exists_flattening lBlocks follows.isBlockRun isInfinite
  exact ⟨lBlocks, lRun, boundary,
    { isDexRun := isRun, follows := follows, isInfinite := isInfinite, isLRun := isLRun
      isFlattened := isFlattened }⟩

end DexExpressiveness
