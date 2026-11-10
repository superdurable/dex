import DexExpressiveness.Liveness.Silent

/-!
# Liveness, work package L3: lemmas for the L run of a Dex run

The helpers of `Liveness/DexToLRun.lean`:

- `exists_stepwise` chooses an infinite sequence step by step;
- `eventually_idle_of_weight` is the descent on the silent weight;
- `encodeRequest_injective`, `Related.isClosed`, and the facts about one
  block of `LFollowsDex` (`LFollowsDex.labels_at`);
- the progress steps of the L run are finitely many exactly when those of
  the Dex run are.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Sequences of natural numbers and choices -/

/-- An infinite sequence chosen step by step: when every state that holds at
`index` has a step to a state that holds at `index + 1`, some sequence of
states and blocks takes such a step at every index. -/
theorem exists_stepwise {State Block : Type} (Holds : Nat → State → Prop)
    (IsStep : Nat → State → Block → State → Prop) (init : State) (holdsInitially : Holds 0 init)
    (hasStep : ∀ index state, Holds index state →
      ∃ block next, IsStep index state block next ∧ Holds (index + 1) next) :
    ∃ (states : Nat → State) (blocks : Nat → Block), states 0 = init ∧
      ∀ index, Holds index (states index) ∧
        IsStep index (states index) (blocks index) (states (index + 1)) := by
  let chain : (index : Nat) → { state : State // Holds index state } := fun index =>
    Nat.rec (motive := fun index => { state : State // Holds index state })
      ⟨init, holdsInitially⟩
      (fun index previous =>
        ⟨Classical.choose (Classical.choose_spec (hasStep index previous.1 previous.2)),
          (Classical.choose_spec (Classical.choose_spec
            (hasStep index previous.1 previous.2))).2⟩)
      index
  exact ⟨fun index => (chain index).1,
    fun index => Classical.choose (hasStep index (chain index).1 (chain index).2), rfl,
    fun index => ⟨(chain index).2,
      (Classical.choose_spec (Classical.choose_spec
        (hasStep index (chain index).1 (chain index).2))).1⟩⟩

/-- Finitely many values have an upper bound. -/
theorem exists_bound_upTo (value : Nat → Nat) (count : Nat) :
    ∃ maximum, ∀ index, index ≤ count → value index ≤ maximum := by
  induction count with
  | zero =>
    exact ⟨value 0, fun index isSmall => by rw [Nat.le_zero.mp isSmall]; exact Nat.le_refl _⟩
  | succ count inductionHypothesis =>
    obtain ⟨maximum, isMaximum⟩ := inductionHypothesis
    refine ⟨maximum + value (count + 1), fun index isSmall => ?_⟩
    by_cases isLast : index = count + 1
    · rw [isLast]
      omega
    · have isBelow := isMaximum index (by omega)
      omega

/-- From `bound` on, every step either is not a progress step and keeps the
weight, or lowers the weight. Then the progress steps are finitely many. -/
theorem eventually_idle_of_weight (weight : Nat → Nat) (isProgress : Nat → Bool) (bound : Nat)
    (isStep : ∀ index, bound ≤ index →
      (isProgress index = false ∧ weight (index + 1) = weight index) ∨
        weight (index + 1) < weight index) :
    ∃ last, ∀ index, last ≤ index → isProgress index = false := by
  have isMonotone : ∀ start, bound ≤ start → ∀ offset,
      weight (start + offset) ≤ weight start := by
    intro start isLater offset
    induction offset with
    | zero => exact Nat.le_refl _
    | succ offset inductionHypothesis =>
      have stepFact := isStep (start + offset) (by omega)
      rw [← Nat.add_assoc]
      rcases stepFact with ⟨_, isEqual⟩ | isLess <;> omega
  have isBelowStart : ∀ start index, bound ≤ start → start ≤ index →
      weight index ≤ weight start := by
    intro start index isLater isAfter
    have isSmall := isMonotone start isLater (index - start)
    rw [Nat.add_sub_of_le isAfter] at isSmall
    exact isSmall
  have main : ∀ limit start, bound ≤ start → weight start ≤ limit →
      ∃ last, ∀ index, last ≤ index → isProgress index = false := by
    intro limit
    induction limit with
    | zero =>
      intro start isLater isSmall
      refine ⟨start, fun index isAfter => ?_⟩
      rcases isStep index (by omega) with ⟨isIdle, _⟩ | isLess
      · exact isIdle
      · have isBelow := isBelowStart start index isLater isAfter
        omega
    | succ limit inductionHypothesis =>
      intro start isLater isSmall
      by_cases hasDrop : ∃ index, start ≤ index ∧ weight (index + 1) < weight index
      · obtain ⟨index, isAfter, isLess⟩ := hasDrop
        have isBelow := isBelowStart start index isLater isAfter
        exact inductionHypothesis (index + 1) (by omega) (by omega)
      · refine ⟨start, fun index isAfter => ?_⟩
        rcases isStep index (by omega) with ⟨isIdle, _⟩ | isLess
        · exact isIdle
        · exact absurd ⟨index, isAfter, isLess⟩ hasDrop
  exact main (weight bound) bound (Nat.le_refl _) (Nat.le_refl _)

/-! ## Requests, events, and the closed status -/

/-- Related configurations are closed together (spec §5.5, markers). -/
theorem Related.isClosed {program : Program} {lConfig : L.Config}
    {dexConfig : Dex.Config signature} (isRelated : Related program lConfig dexConfig) :
    (L.system program).isClosed lConfig = (Dex.system ⟦program⟧).isClosed dexConfig := by
  have status := isRelated.status
  simp only [L.system, Dex.system]
  cases isStatus : lConfig.status <;> cases hasRun : dexConfig.run <;>
    simp only [isStatus, hasRun] at status ⊢
  all_goals first
    | exact status.elim
    | rfl
    | (rw [status]; rfl)
    | (obtain ⟨closedStatus, isClosed⟩ := status
       rw [isClosed]
       rfl)

/-- A Dex action that is not a progress step is a clock tick. -/
theorem Dex.exists_tick_of_isProgress_eq_false (flow : Dex.FlowDef signature)
    (action : Dex.Action signature) (isIdle : (Dex.system flow).isProgress action = false) :
    ∃ duration, action = .tick duration := by
  cases action with
  | tick duration => exact ⟨duration, rfl⟩
  | _ => simp [Dex.system] at isIdle

/-! ## One block of an L run that follows a Dex run -/

/-- `dex_step_labels` for block `index` of an L run that follows a Dex run. -/
theorem LFollowsDex.labels_at {program : Program}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)}
    {lBlocks : BlockRun L.Config L.Label Event} (follows : LFollowsDex program dexRun lBlocks)
    (isRun : (Dex.system ⟦program⟧).IsRun dexRun) (index : Nat) :
    (∀ label, label ∈ lBlocks.blocks index →
      (L.system program).isProgress label =
        (Dex.system ⟦program⟧).isProgress (dexRun.labels index)) ∧
    ((Dex.system ⟦program⟧).isProgress (dexRun.labels index) = false →
      lBlocks.blocks index ≠ []) ∧
    (lBlocks.blocks index = [] → (dexRun.labels index).isSilentKind = true) ∧
    ((lBlocks.blocks index).filterMap (L.system program).requestOf).map encodeRequest =
      ((Dex.system ⟦program⟧).requestOf (dexRun.labels index)).toList := by
  obtain ⟨dexEvents, isStep⟩ := isRun.2 index
  have isExec := follows.isBlockRun.2 index
  rw [follows.sameEvents index,
    (Dex.system ⟦program⟧).eventsAt_of_step dexRun index dexEvents _ isStep] at isExec
  exact dex_step_labels program (lBlocks.states index) (dexRun.states index)
    (follows.isState index) (dexRun.labels index) (follows.isAdmissible index) dexEvents
    (dexRun.states (index + 1)) isStep (lBlocks.blocks index) (lBlocks.states (index + 1))
    (follows.atMostOne index) isExec (follows.isState (index + 1)).isRelated

/-- An empty block keeps the L configuration. -/
theorem LFollowsDex.states_succ_of_nil {program : Program}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)}
    {lBlocks : BlockRun L.Config L.Label Event} (follows : LFollowsDex program dexRun lBlocks)
    (index : Nat) (isEmpty : lBlocks.blocks index = []) :
    lBlocks.states (index + 1) = lBlocks.states index := by
  have isExec := follows.isBlockRun.2 index
  rw [isEmpty] at isExec
  simp only [TransitionSystem.exec] at isExec
  exact (Prod.mk.inj (Option.some.inj isExec)).2.symm

/-- From `bound` on, every Dex step of a run that L follows either is a clock
tick that keeps the silent weight, or lowers the silent weight, when no L
block from `bound` on holds a progress step. -/
theorem LFollowsDex.silentWeight_step {program : Program}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)}
    {lBlocks : BlockRun L.Config L.Label Event} (follows : LFollowsDex program dexRun lBlocks)
    (isRun : (Dex.system ⟦program⟧).IsRun dexRun) (index : Nat)
    (isIdle : ∀ label, label ∈ lBlocks.blocks index →
      (L.system program).isProgress label = false) :
    ((Dex.system ⟦program⟧).isProgress (dexRun.labels index) = false ∧
      (dexRun.states (index + 1)).silentWeight = (dexRun.states index).silentWeight) ∨
    (dexRun.states (index + 1)).silentWeight < (dexRun.states index).silentWeight := by
  obtain ⟨dexEvents, isStep⟩ := isRun.2 index
  obtain ⟨sameProgress, _, isSilentOfEmpty, _⟩ := follows.labels_at isRun index
  cases isBlock : lBlocks.blocks index with
  | nil =>
    have tasksDistinct := (follows.isState index).tasksDistinct
    refine Or.inr ?_
    exact silentWeight_lt_of_isSilentKind ⟦program⟧ (dexRun.states index) tasksDistinct
      (dexRun.labels index) dexEvents (dexRun.states (index + 1)) isStep
      (isSilentOfEmpty isBlock)
  | cons label laterLabels =>
    have isMember : label ∈ lBlocks.blocks index := by
      rw [isBlock]
      exact List.mem_cons_self
    have isDexIdle : (Dex.system ⟦program⟧).isProgress (dexRun.labels index) = false := by
      rw [← sameProgress label isMember]
      exact isIdle label isMember
    obtain ⟨duration, isTick⟩ :=
      Dex.exists_tick_of_isProgress_eq_false ⟦program⟧ (dexRun.labels index) isDexIdle
    rw [isTick] at isStep
    exact Or.inl ⟨isDexIdle, silentWeight_tick ⟦program⟧ (dexRun.states index) duration
      dexEvents (dexRun.states (index + 1)) isStep⟩

/-! ## The progress steps and the states of the flattened L run -/

/-- Finitely many progress steps of the Dex run give finitely many of the L
run: an L label is a progress step only when its Dex action is one. -/
theorem LRunOfDexRun.hasFinitelyManyProgressSteps {program : Program}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)}
    {lBlocks : BlockRun L.Config L.Label Event} {lRun : InfiniteRun L.Config L.Label}
    {boundary : Nat → Nat} (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (isFinite : (Dex.system ⟦program⟧).HasFinitelyManyProgressSteps dexRun) :
    (L.system program).HasFinitelyManyProgressSteps lRun := by
  apply ((L.system program).flattening_hasFinitelyManyProgressSteps lBlocks lRun boundary
    built.isFlattened built.isInfinite).mpr
  obtain ⟨bound, isIdle⟩ := isFinite
  refine ⟨bound, fun index isLater label isMember => ?_⟩
  rw [(built.follows.labels_at built.isDexRun index).1 label isMember]
  exact isIdle index isLater

/-- Finitely many progress steps of the L run give finitely many of the Dex
run: from some step on, a Dex progress step has no L label, so it is of a
silent kind and lowers the silent weight, which a clock tick keeps. -/
theorem LRunOfDexRun.dex_hasFinitelyManyProgressSteps {program : Program}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)}
    {lBlocks : BlockRun L.Config L.Label Event} {lRun : InfiniteRun L.Config L.Label}
    {boundary : Nat → Nat} (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (isFinite : (L.system program).HasFinitelyManyProgressSteps lRun) :
    (Dex.system ⟦program⟧).HasFinitelyManyProgressSteps dexRun := by
  obtain ⟨bound, isIdle⟩ :=
    ((L.system program).flattening_hasFinitelyManyProgressSteps lBlocks lRun boundary
      built.isFlattened built.isInfinite).mp isFinite
  exact eventually_idle_of_weight (fun index => (dexRun.states index).silentWeight)
    (fun index => (Dex.system ⟦program⟧).isProgress (dexRun.labels index)) bound
    (fun index isLater =>
      built.follows.silentWeight_step built.isDexRun index (isIdle index isLater))

/-- Every configuration of the flattened L run is the configuration before
a block: a block holds at most one label. -/
theorem LRunOfDexRun.exists_block_state {program : Program}
    {dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature)}
    {lBlocks : BlockRun L.Config L.Label Event} {lRun : InfiniteRun L.Config L.Label}
    {boundary : Nat → Nat} (built : LRunOfDexRun program dexRun lBlocks lRun boundary)
    (position : Nat) : ∃ index, lRun.states position = lBlocks.states index := by
  obtain ⟨index, offset, isInside, isPosition⟩ :=
    TransitionSystem.flattening_position lBlocks lRun boundary built.isFlattened
      built.isInfinite position
  have atMostOne := built.follows.atMostOne index
  have isFirst : offset = 0 := by omega
  rw [isFirst, Nat.add_zero] at isPosition
  exact ⟨index, by rw [isPosition]; exact built.isFlattened.stateAtBoundary index⟩

end DexExpressiveness
