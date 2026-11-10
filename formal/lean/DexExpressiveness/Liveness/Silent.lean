import DexExpressiveness.FiniteTraces
import DexExpressiveness.Liveness.Statements

/-!
# Liveness, work package L2: silent Dex steps and the kinds of labels

A Dex step is simulated by at most one L label (`dex_step_simulated`). This
package says which L label, as far as the liveness proof needs:

- a clock tick is simulated by a clock tick, and a progress step by progress
  steps only (spec §5.5: "[TICK] steps are time passing, not progress");
- a Dex step with no L label is of a silent kind, and every action of a
  silent kind lowers `Dex.Config.silentWeight`;
- the L label attempts the request the Dex action attempts, and no other;
- in a run for an admissible word every Dex action is admissible.

Every lemma here is `sorry`; the README lists them. The proofs may add lemmas
to this file. They may not change `Liveness/Statements.lean`,
`Core/BlockRun.lean`, or any file of Stage 2a.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-- An action of a silent kind lowers the silent weight. -/
theorem silentWeight_lt_of_isSilentKind (flow : Dex.FlowDef signature)
    (config : Dex.Config signature) (action : Dex.Action signature)
    (events : List (Dex.Obs signature)) (next : Dex.Config signature)
    (isStep : Dex.step flow config action = some (events, next))
    (isSilent : action.isSilentKind = true) : next.silentWeight < config.silentWeight := by
  sorry

/-- A clock tick keeps the silent weight. -/
theorem silentWeight_tick (flow : Dex.FlowDef signature) (config : Dex.Config signature)
    (duration : Nat) (events : List (Dex.Obs signature)) (next : Dex.Config signature)
    (isStep : Dex.step flow config (.tick duration) = some (events, next)) :
    next.silentWeight = config.silentWeight := by
  sorry

/-- The L labels that simulate one Dex step: they are progress steps exactly
when the Dex action is one, a clock tick is simulated by a label, a step with
no label is of a silent kind, and the labels attempt the request of the Dex
action. `labels` and `lNext` are what `dex_step_simulated` gives. -/
theorem dex_step_labels (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (state : IsSimulationState program lConfig dexConfig)
    (action : Dex.Action signature) (isAdmissible : IsAdmissibleAction dexConfig action)
    (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature)
    (isStep : (Dex.system ⟦program⟧).step dexConfig action = some (dexEvents, dexNext))
    (labels : List L.Label) (lNext : L.Config) (atMostOne : labels.length ≤ 1)
    (isExec : (L.system program).exec lConfig labels = some (dexEvents.filterMap project, lNext))
    (isRelated : Related program lNext dexNext) :
    (∀ label, label ∈ labels →
      (L.system program).isProgress label = (Dex.system ⟦program⟧).isProgress action) ∧
    ((Dex.system ⟦program⟧).isProgress action = false → labels ≠ []) ∧
    (labels = [] → action.isSilentKind = true) ∧
    (labels.filterMap (L.system program).requestOf).map encodeRequest =
      ((Dex.system ⟦program⟧).requestOf action).toList := by
  sorry

/-- In a run for the Dex form of an admissible word, every action is one an
admissible run can take: the start comes first, so every publication finds
the Flow started (spec §5.3). -/
theorem isAdmissibleAction_of_isRunFor (program : Program) (word : Sequence L.Request)
    (isAdmissible : L.IsAdmissibleWord word)
    (dexRun : InfiniteRun (Dex.Config signature) (Dex.Action signature))
    (isRun : (Dex.system ⟦program⟧).IsRun dexRun)
    (isRunFor : (Dex.system ⟦program⟧).IsRunFor dexRun (word.map encodeRequest))
    (index : Nat) : IsAdmissibleAction (dexRun.states index) (dexRun.labels index) := by
  sorry

end DexExpressiveness
