import DexExpressiveness.Liveness.DexToLRunLemmas

/-!
# Liveness, work package L3: the L run of a Dex run

From an infinite run of the translated Flow for an admissible word, this
package builds an L run: the L labels that simulate each Dex step, in order
(`LFollowsDex`), flattened (`Core/BlockRun.lean`). It then shows that the L
run attempts the same word, has the same trace, and has the same marker
(spec §5.5). Fairness is work package L4.

`exists_lRun_of_dexRun` is proved here from the other lemmas of this file and
from `exists_flattening`. Everything here is proved: the proofs use the
lemmas of L1 (`Core/BlockRun.lean`) and L2 (`Liveness/Silent.lean`).
Their helpers are in `Liveness/DexToLRunLemmas.lean`.
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
  have isAdmissibleAt :=
    isAdmissibleAction_of_isRunFor program word isAdmissible dexRun isRun isRunFor
  obtain ⟨states, blocks, startsAtInit, holds⟩ :=
    exists_stepwise
      (fun index lConfig => IsSimulationState program lConfig (dexRun.states index) ∧
        (L.system program).Reachable lConfig)
      (fun index lConfig labels lNext => labels.length ≤ 1 ∧
        (L.system program).exec lConfig labels =
          some (((Dex.system ⟦program⟧).eventsAt dexRun index).filterMap project, lNext))
      (L.system program).init
      ⟨by rw [isRun.1]; exact isSimulationState_initial program, [], [], rfl⟩
      (by
        intro index lConfig ⟨state, isReachable⟩
        obtain ⟨dexEvents, isStep⟩ := isRun.2 index
        obtain ⟨labels, lNext, atMostOne, isExec, nextRelated⟩ :=
          dex_step_simulated program lConfig (dexRun.states index) state
            (isWellFormed lConfig isReachable) (dexRun.labels index) (isAdmissibleAt index)
            dexEvents (dexRun.states (index + 1)) isStep
        refine ⟨labels, lNext, ⟨atMostOne, ?_⟩, ?_, isReachable.exec labels _ isExec⟩
        · rw [(Dex.system ⟦program⟧).eventsAt_of_step dexRun index dexEvents _ isStep]
          exact isExec
        · exact
            { isRelated := nextRelated
              lInvariant := L.Invariant.of_exec program labels lConfig _ lNext
                state.lInvariant isExec
              dexInvariant := ConfigInvariant.step program (dexRun.states index)
                (dexRun.labels index) dexEvents (dexRun.states (index + 1)) state.dexInvariant
                state.tasksDistinct isStep
              tasksDistinct := TasksAreDistinct.step ⟦program⟧ (dexRun.states index)
                (dexRun.labels index) dexEvents (dexRun.states (index + 1))
                state.tasksDistinct isStep
              timersDistinct := TimersAreDistinct.step ⟦program⟧ (dexRun.states index)
                (dexRun.labels index) dexEvents (dexRun.states (index + 1))
                state.timersDistinct isStep })
  exact ⟨{ states := states, blocks := blocks
           events := fun index =>
             ((Dex.system ⟦program⟧).eventsAt dexRun index).filterMap project },
    { isBlockRun := ⟨startsAtInit, fun index => (holds index).2.2⟩
      isState := fun index => (holds index).1.1
      isAdmissible := isAdmissibleAt
      atMostOne := fun index => (holds index).2.1
      sameEvents := fun _ => rfl }⟩

/-- When time diverges in the Dex run (F1), the L blocks hold infinitely many
labels: every clock tick is simulated by one. -/
theorem LFollowsDex.hasInfinitelyManySteps {program : Program}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)}
    {lBlocks : BlockRun L.Config L.Label Event} (follows : LFollowsDex program dexRun lBlocks)
    (isRun : (Dex.system ⟦program⟧).IsRun dexRun)
    (timeDiverges : Dex.Fairness.TimeDiverges dexRun) : lBlocks.HasInfinitelyManySteps := by
  -- `isRun` is not needed: an empty block keeps the L configuration and its clock.
  have _ := isRun
  intro bound
  apply Classical.byContradiction
  intro hasNoLaterStep
  have isEmpty : ∀ index, bound ≤ index → lBlocks.blocks index = [] := by
    intro index isLater
    apply Classical.byContradiction
    intro isNonempty
    exact hasNoLaterStep ⟨index, isLater, isNonempty⟩
  -- No L label after `bound`: the L configuration, and so the clock, stays.
  have isConstant : ∀ offset, lBlocks.states (bound + offset) = lBlocks.states bound := by
    intro offset
    induction offset with
    | zero => rfl
    | succ offset inductionHypothesis =>
      rw [← Nat.add_assoc,
        follows.states_succ_of_nil (bound + offset) (isEmpty _ (Nat.le_add_right _ _)),
        inductionHypothesis]
  obtain ⟨maximum, isMaximum⟩ :=
    exists_bound_upTo (fun index => (dexRun.states index).clock) bound
  obtain ⟨index, exceeds⟩ := timeDiverges maximum
  by_cases isEarly : index ≤ bound
  · exact Nat.lt_irrefl _ (Nat.lt_of_lt_of_le exceeds (isMaximum index isEarly))
  · have isLater : bound + (index - bound) = index := by omega
    have sameClock : (dexRun.states index).clock = (dexRun.states bound).clock := by
      rw [← (follows.isState index).isRelated.clock, ← (follows.isState bound).isRelated.clock,
        ← isLater, isConstant (index - bound)]
    rw [sameClock] at exceeds
    exact Nat.lt_irrefl _ (Nat.lt_of_lt_of_le exceeds (isMaximum bound (Nat.le_refl _)))

/-- The L run attempts the word whose Dex form the Dex run attempts. -/
theorem LRunOfDexRun.isRunFor {program : Program}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)}
    {lBlocks : BlockRun L.Config L.Label Event} {lRun : InfiniteRun L.Config L.Label}
    {boundary : Nat → Nat} (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (word : Sequence L.Request)
    (isRunFor : (Dex.system ⟦program⟧).IsRunFor dexRun (word.map encodeRequest)) :
    (L.system program).IsRunFor lRun word := by
  apply ((L.system program).flattening_isRunFor lBlocks lRun boundary built.isFlattened
    built.isInfinite word).mp
  apply isConcatenation_of_map encodeRequest encodeRequest_injective
  have sameChunks :
      (fun index =>
        ((lBlocks.blocks index).filterMap (L.system program).requestOf).map encodeRequest) =
      (Dex.system ⟦program⟧).requestsAt dexRun := by
    funext index
    exact (built.follows.labels_at built.isDexRun index).2.2.2
  rw [sameChunks]
  exact isRunFor

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
  apply ((L.system program).flattening_isConcatenation_events lBlocks lRun boundary
    built.follows.isBlockRun built.isLRun built.isFlattened built.isInfinite some trace).mp
  have sameChunks :
      (fun index => (lBlocks.events index).filterMap some) =
      fun index => ((Dex.system ⟦program⟧).eventsAt dexRun index).filterMap project := by
    funext index
    rw [List.filterMap_some, built.follows.sameEvents index]
  rw [sameChunks]
  exact isTrace

/-- The L run has the marker of the Dex run (spec §5.5). For `∞` this uses
the silent weight: a Dex run cannot take infinitely many progress steps that
L does not see without one that L sees. -/
theorem LRunOfDexRun.hasMarker {program : Program}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)}
    {lBlocks : BlockRun L.Config L.Label Event} {lRun : InfiniteRun L.Config L.Label}
    {boundary : Nat → Nat} (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (marker : Marker) (hasMarker : (Dex.system ⟦program⟧).HasMarker dexRun marker) :
    (L.system program).HasMarker lRun marker := by
  cases marker with
  | terminated =>
    obtain ⟨isFinite, index, isClosed⟩ := hasMarker
    refine ⟨built.hasFinitelyManyProgressSteps isFinite, boundary index, ?_⟩
    rw [built.isFlattened.stateAtBoundary index, (built.follows.isState index).isRelated.isClosed]
    exact isClosed
  | deadlocked =>
    obtain ⟨isFinite, isOpen⟩ := hasMarker
    refine ⟨built.hasFinitelyManyProgressSteps isFinite, fun position => ?_⟩
    obtain ⟨index, isState⟩ := built.exists_block_state position
    rw [isState, (built.follows.isState index).isRelated.isClosed]
    exact isOpen index
  | divergent =>
    intro isFinite
    exact hasMarker (built.dex_hasFinitelyManyProgressSteps isFinite)

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
