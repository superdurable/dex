import DexExpressiveness.Failure.Statements.Leaf

/-!
# WP-F6: concatenations of chunk families

Generic facts about `IsConcatenation` that the liveness proof needs to move
between one chunk per trace position, one chunk per DexF step, and one chunk
per Dex step.
-/

namespace DexExpressiveness

/-- A chunk family has at most one concatenation. -/
theorem wpf6_isConcatenation_unique {α : Type} (chunks : Nat → List α)
    (first second : Sequence α) (isFirst : IsConcatenation chunks first)
    (isSecond : IsConcatenation chunks second) : first = second := by
  sorry

/-- Every chunk family has a concatenation. -/
theorem wpf6_exists_isConcatenation {α : Type} (chunks : Nat → List α) :
    ∃ sequence, IsConcatenation chunks sequence := by
  sorry

/-- A concatenation stays absent from its first absent position on. -/
theorem wpf6_isConcatenation_none_succ {α : Type} (chunks : Nat → List α)
    (sequence : Sequence α) (isConcatenation : IsConcatenation chunks sequence)
    (position : Nat) (isNone : sequence position = none) : sequence (position + 1) = none := by
  sorry

/-- The element at a position of a finite concatenation is the element of the
concatenation. -/
theorem wpf6_isConcatenation_getElem {α : Type} (chunks : Nat → List α)
    (sequence : Sequence α) (isConcatenation : IsConcatenation chunks sequence)
    (count position : Nat) (isInside : position < (concatUpTo chunks count).length) :
    sequence position = (concatUpTo chunks count)[position]? := by
  sorry

/-- Regrouping a concatenation: if every finite concatenation of `blocks` is
a finite concatenation of `chunks`, and these reach every finite
concatenation of `chunks` in length, then both families have the same
concatenation. -/
theorem wpf6_isConcatenation_of_regroup {α : Type} (chunks blocks : Nat → List α)
    (boundary : Nat → Nat)
    (agree : ∀ index, concatUpTo blocks index = concatUpTo chunks (boundary index))
    (covers : ∀ count, ∃ index,
      (concatUpTo chunks count).length ≤ (concatUpTo chunks (boundary index)).length)
    (sequence : Sequence α) (isConcatenation : IsConcatenation chunks sequence) :
    IsConcatenation blocks sequence := by
  sorry

/-- A sequence that stays absent from its first absent position on is the
concatenation of its positions. -/
theorem wpf6_isConcatenation_pointwise {α : Type} (sequence : Sequence α)
    (isClosed : ∀ position, sequence position = none → sequence (position + 1) = none) :
    IsConcatenation (fun position => (sequence position).toList) sequence := by
  sorry

/-- A non-decreasing bounded sequence of naturals reaches its supremum: either
it exceeds every bound or some index carries a maximum. -/
theorem wpf6_exists_max_or_unbounded (values : Nat → Nat)
    (isMonotone : ∀ index, values index ≤ values (index + 1)) :
    (∀ bound, ∃ index, bound ≤ values index) ∨
      ∃ maximum, ∀ index, values index ≤ values maximum := by
  sorry

end DexExpressiveness
