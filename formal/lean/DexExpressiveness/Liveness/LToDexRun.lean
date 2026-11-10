import DexExpressiveness.Liveness.Silent
import DexExpressiveness.Liveness.LToDexRunStep

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
from `exists_flattening`. The other lemmas are proved from the statements of
work package L1 (`Core/BlockRun.lean`) and from two files of helpers:
`Liveness/LToDexRunDex.lean` (a closed run stays closed; which tasks are
`Returned` after a step) and `Liveness/LToDexRunStep.lean` (one block, the
blocks of an L run, the shape of a block). Everything here is proved.
Well-formedness of the program is not needed in this direction.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- An L run is followed by a Dex run in blocks of `fairActions`: by
`l_step_simulatedByActions` at every step, then the cleanup, which keeps the
relation and leaves no canceled task without an effect. -/
theorem exists_dexFollowsL (program : Program) (lRun : InfiniteRun L.Config L.Label)
    (isRun : (L.system program).IsRun lRun) : ∃ dexBlocks, DexFollowsL program lRun dexBlocks := by
  have settled := isSettledState_dexStateOfLRun program lRun isRun
  exact ⟨dexBlocksOfLRun program lRun,
    { isBlockRun := ⟨rfl, fun index => by
        obtain ⟨_, dexEvents, _, isExec, isEvents, _⟩ :=
          dexBlocksOfLRun_block program lRun isRun index
        rw [isEvents]
        exact isExec⟩
      isState := fun index => (settled index).isState
      isSettled := fun index => (settled index).isSettled
      noReturnedTask := fun index =>
        (hasNoReturnedTaskExcept_none_iff _).mp (settled index).noReturnedTask
      isFair := fun _ => rfl
      sameEvents := fun index => by
        obtain ⟨lEvents, dexEvents, isLStep, _, isEvents, sameEvents⟩ :=
          dexBlocksOfLRun_block program lRun isRun index
        rw [isEvents, sameEvents]
        simp only [TransitionSystem.eventsAt, isLStep] }⟩

/-- When time diverges in the L run (F1), the Dex blocks hold infinitely many
actions: the block of a clock tick is a clock tick. -/
theorem DexFollowsL.hasInfinitelyManySteps {program : Program}
    {lRun : InfiniteRun L.Config L.Label}
    {dexBlocks : BlockRun (Dex.Config signature) (Dex.Action signature) (Dex.Obs signature)}
    (follows : DexFollowsL program lRun dexBlocks) (isRun : (L.system program).IsRun lRun)
    (timeDiverges : L.Fairness.TimeDiverges lRun) : dexBlocks.HasInfinitelyManySteps := by
  -- The Dex clock is the L clock between two blocks, so `isRun` is not needed.
  have _ := isRun
  intro bound
  apply Classical.byContradiction
  intro hasNoStep
  have isEmpty : ∀ index, bound ≤ index → dexBlocks.blocks index = [] := by
    intro index isLater
    apply Classical.byContradiction
    intro isNotEmpty
    exact hasNoStep ⟨index, isLater, isNotEmpty⟩
  -- With empty blocks the Dex configuration, and so the L clock, stops changing.
  have sameState : ∀ offset, dexBlocks.states (bound + offset) = dexBlocks.states bound := by
    intro offset
    induction offset with
    | zero => rfl
    | succ offset inductionHypothesis =>
      have isExec := follows.isBlockRun.2 (bound + offset)
      rw [isEmpty (bound + offset) (Nat.le_add_right bound offset)] at isExec
      simp only [TransitionSystem.exec] at isExec
      injection isExec with isPair
      injection isPair with _ isNext
      show dexBlocks.states (bound + offset + 1) = dexBlocks.states bound
      rw [← isNext, inductionHypothesis]
  have isBounded : ∀ count, ∃ limit, ∀ index, index ≤ count →
      (lRun.states index).clock ≤ limit := by
    intro count
    induction count with
    | zero =>
      exact ⟨(lRun.states 0).clock, fun index isBelow => by
        rw [Nat.le_zero.mp isBelow]
        exact Nat.le_refl _⟩
    | succ count inductionHypothesis =>
      obtain ⟨limit, isLimit⟩ := inductionHypothesis
      refine ⟨max limit (lRun.states (count + 1)).clock, fun index isBelow => ?_⟩
      rcases Nat.lt_or_eq_of_le isBelow with isEarlier | isLast
      · exact Nat.le_trans (isLimit index (Nat.le_of_lt_succ isEarlier)) (Nat.le_max_left _ _)
      · rw [isLast]
        exact Nat.le_max_right _ _
  obtain ⟨limit, isLimit⟩ := isBounded bound
  obtain ⟨index, exceeds⟩ := timeDiverges limit
  rcases Nat.le_total index bound with isEarlier | isLater
  · exact absurd (isLimit index isEarlier) (Nat.not_le.mpr exceeds)
  · obtain ⟨offset, isIndex⟩ := Nat.exists_eq_add_of_le isLater
    subst isIndex
    have laterClock := (follows.isState (bound + offset)).isRelated.clock
    rw [sameState offset, ← (follows.isState bound).isRelated.clock] at laterClock
    rw [laterClock] at exceeds
    exact absurd (isLimit bound (Nat.le_refl bound)) (Nat.not_le.mpr exceeds)

/-- The Dex run attempts the Dex form of the word the L run attempts. -/
theorem DexRunOfLRun.isRunFor {program : Program} {lRun : InfiniteRun L.Config L.Label}
    {dexBlocks : BlockRun (Dex.Config signature) (Dex.Action signature) (Dex.Obs signature)}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)} {boundary : Nat → Nat}
    (built : DexRunOfLRun program lRun dexBlocks dexRun boundary) (word : Sequence L.Request)
    (isRunFor : (L.system program).IsRunFor lRun word) :
    (Dex.system ⟦program⟧).IsRunFor dexRun (word.map encodeRequest) := by
  apply ((Dex.system ⟦program⟧).flattening_isRunFor dexBlocks dexRun boundary built.isFlattened
    built.isInfinite _).mp
  have sameChunks : (fun index =>
        (dexBlocks.blocks index).filterMap (Dex.system ⟦program⟧).requestOf) =
      (fun index => ((L.system program).requestsAt lRun index).map encodeRequest) := by
    funext index
    rw [built.follows.isFair index, fairActions_requests]
    rfl
  rw [sameChunks]
  exact isConcatenation_map encodeRequest _ word isRunFor

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
  apply ((Dex.system ⟦program⟧).flattening_isConcatenation_events dexBlocks dexRun boundary
    built.follows.isBlockRun built.isDexRun built.isFlattened built.isInfinite project trace).mp
  have sameChunks : (fun index => (dexBlocks.events index).filterMap project) =
      (fun position => ((L.system program).eventsAt lRun position).filterMap some) := by
    funext index
    rw [built.follows.sameEvents index, List.filterMap_some]
  rw [sameChunks]
  exact isTrace

/-- The Dex run has the marker of the L run (spec §5.5). -/
theorem DexRunOfLRun.hasMarker {program : Program} {lRun : InfiniteRun L.Config L.Label}
    {dexBlocks : BlockRun (Dex.Config signature) (Dex.Action signature) (Dex.Obs signature)}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)} {boundary : Nat → Nat}
    (built : DexRunOfLRun program lRun dexBlocks dexRun boundary) (marker : Marker)
    (hasMarker : (L.system program).HasMarker lRun marker) :
    (Dex.system ⟦program⟧).HasMarker dexRun marker := by
  have follows := built.follows
  have isFlat := (Dex.system ⟦program⟧).flattening_hasFinitelyManyProgressSteps dexBlocks dexRun
    boundary built.isFlattened built.isInfinite
  -- A clock tick of L is a block that holds the clock tick only.
  have finite_of_finite : (L.system program).HasFinitelyManyProgressSteps lRun →
      (Dex.system ⟦program⟧).HasFinitelyManyProgressSteps dexRun := by
    rintro ⟨bound, isIdle⟩
    refine isFlat.mpr ⟨bound, fun index isLater action isMember => ?_⟩
    have isTick := isIdle index isLater
    rw [follows.isFair index] at isMember
    cases labelCase : lRun.labels index with
    | tick duration =>
      rw [labelCase, fairActions_tick program _ _ (follows.isSettled index) duration,
        List.mem_singleton] at isMember
      rw [isMember]
      rfl
    | _ =>
      rw [labelCase] at isTick
      cases isTick
  -- A progress step of L is a block that is not empty and holds progress steps only.
  have finite_of_dexFinite : (Dex.system ⟦program⟧).HasFinitelyManyProgressSteps dexRun →
      (L.system program).HasFinitelyManyProgressSteps lRun := by
    intro isFinite
    obtain ⟨bound, isIdle⟩ := isFlat.mp isFinite
    refine ⟨bound, fun index isLater => ?_⟩
    cases progressCase : (L.system program).isProgress (lRun.labels index) with
    | false => rfl
    | true =>
      obtain ⟨lEvents, isLStep⟩ := built.isLRun.2 index
      have isNotEmpty := simulatingActions_ne_nil program _ _ (follows.isState index) _ lEvents
        _ isLStep
      obtain ⟨dropActions, isShape, _⟩ := fairActions_shape program (lRun.states index)
        (dexBlocks.states index) (lRun.labels index)
      obtain ⟨action, isAction⟩ := List.exists_mem_of_ne_nil _ isNotEmpty
      have isFairMember : action ∈ fairActions program (lRun.states index)
          (dexBlocks.states index) (lRun.labels index) := by
        rw [isShape]
        exact List.mem_append_left _ isAction
      have isProgress := fairActions_isProgress program _ _ _ progressCase action isFairMember
      rw [isIdle index isLater action (follows.isFair index ▸ isFairMember)] at isProgress
      cases isProgress
  cases marker with
  | terminated =>
    obtain ⟨isFinite, index, isClosed⟩ := hasMarker
    refine ⟨finite_of_finite isFinite, boundary index, ?_⟩
    rw [built.isFlattened.stateAtBoundary index]
    have isLClosed : (lRun.states index).status = .closed := by
      simpa [L.system] using isClosed
    exact (Dex.isClosed_iff _ _).mpr ((follows.isState index).isRelated.run_of_closed isLClosed)
  | deadlocked =>
    obtain ⟨isFinite, isOpen⟩ := hasMarker
    refine ⟨finite_of_finite isFinite, fun position => ?_⟩
    cases closedCase : (Dex.system ⟦program⟧).isClosed (dexRun.states position) with
    | false => rfl
    | true =>
      -- A closed configuration inside a block gives a closed configuration at
      -- its end, which is related to an L configuration that is not closed.
      obtain ⟨index, offset, isInside, isPosition⟩ :=
        TransitionSystem.flattening_position dexBlocks dexRun boundary built.isFlattened
          built.isInfinite position
      obtain ⟨prefixEvents, isPrefix⟩ :=
        (Dex.system ⟦program⟧).flattening_exec_take dexBlocks dexRun boundary follows.isBlockRun
          built.isDexRun built.isFlattened index offset (Nat.le_of_lt isInside)
      have isBlock := follows.isBlockRun.2 index
      rw [← List.take_append_drop offset (dexBlocks.blocks index)] at isBlock
      obtain ⟨firstEvents, middle, secondEvents, isFirst, isSecond, _⟩ :=
        (Dex.system ⟦program⟧).exec_append_inv _ _ _ _ _ isBlock
      rw [isPrefix] at isFirst
      injection isFirst with isPair
      injection isPair with _ isMiddle
      rw [← isMiddle, ← isPosition] at isSecond
      have isNextClosed := Dex.exec_isClosed ⟦program⟧ _ _ _ _ isSecond closedCase
      obtain ⟨run, status, hasRun, isRunClosed⟩ := (Dex.isClosed_iff _ _).mp isNextClosed
      have isLClosed := Related.status_of_closed_run program _ _
        (follows.isState (index + 1)).isRelated run status hasRun isRunClosed
      have isLOpen := isOpen (index + 1)
      simp [L.system, isLClosed] at isLOpen
  | divergent => exact fun isFinite => hasMarker (finite_of_dexFinite isFinite)

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
