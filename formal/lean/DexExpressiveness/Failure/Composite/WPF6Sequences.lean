import DexExpressiveness.Failure.Statements.Leaf

/-!
# WP-F6: concatenations of chunk families

Generic facts about `IsConcatenation` that the liveness proof needs to move
between one chunk per trace position, one chunk per DexF step, and one chunk
per Dex step.
-/

namespace DexExpressiveness

/-- A longer concatenation is at least as long. -/
theorem wpf6c_concatUpTo_length_le {α : Type} (chunks : Nat → List α) :
    ∀ second first, first ≤ second →
      (concatUpTo chunks first).length ≤ (concatUpTo chunks second).length := by
  intro second
  induction second with
  | zero =>
    intro first isBefore
    have isZero : first = 0 := by omega
    subst isZero
    exact Nat.le_refl _
  | succ second inductionHypothesis =>
    intro first isBefore
    by_cases isEqual : first = second + 1
    · subst isEqual
      exact Nat.le_refl _
    · have isShorter := inductionHypothesis first (by omega)
      simp only [concatUpTo, List.length_append]
      omega

/-- A longer concatenation keeps the elements of a shorter one. -/
theorem wpf6c_concatUpTo_getElem?_of_le {α : Type} (chunks : Nat → List α) {first second : Nat}
    (isBefore : first ≤ second) (position : Nat)
    (isInside : position < (concatUpTo chunks first).length) :
    (concatUpTo chunks second)[position]? = (concatUpTo chunks first)[position]? := by
  induction second with
  | zero =>
    have isZero : first = 0 := by omega
    subst isZero
    rfl
  | succ second inductionHypothesis =>
    by_cases isEqual : first = second + 1
    · subst isEqual
      rfl
    · have isShorter := wpf6c_concatUpTo_length_le chunks second first (by omega)
      simp only [concatUpTo]
      rw [List.getElem?_append_left (by omega)]
      exact inductionHypothesis (by omega)

/-- Two finite concatenations agree at every position both contain. -/
theorem wpf6c_concatUpTo_getElem?_agree {α : Type} (chunks : Nat → List α)
    (first second position : Nat) (isInsideFirst : position < (concatUpTo chunks first).length)
    (isInsideSecond : position < (concatUpTo chunks second).length) :
    (concatUpTo chunks first)[position]? = (concatUpTo chunks second)[position]? := by
  cases Nat.le_total first second with
  | inl isBefore => exact (wpf6c_concatUpTo_getElem?_of_le chunks isBefore position isInsideFirst).symm
  | inr isAfter => exact wpf6c_concatUpTo_getElem?_of_le chunks isAfter position isInsideSecond

/-- A sequence closed under absence stays absent after an absent position. -/
theorem wpf6c_none_of_le {α : Type} (sequence : Sequence α)
    (isClosed : ∀ position, sequence position = none → sequence (position + 1) = none)
    {first second : Nat} (isNone : sequence first = none) (isBefore : first ≤ second) :
    sequence second = none := by
  induction second with
  | zero =>
    have isZero : first = 0 := by omega
    rw [isZero] at isNone
    exact isNone
  | succ second inductionHypothesis =>
    by_cases isEqual : first = second + 1
    · rw [← isEqual]
      exact isNone
    · exact isClosed second (inductionHypothesis (by omega))

/-- The pointwise concatenation lists the sequence up to its length, which is
the count or the first absent position. -/
theorem wpf6c_pointwise_concatUpTo {α : Type} (sequence : Sequence α)
    (isClosed : ∀ position, sequence position = none → sequence (position + 1) = none)
    (count : Nat) :
    let concatenation := concatUpTo (fun position => (sequence position).toList) count
    concatenation.length ≤ count ∧
      (∀ position, position < concatenation.length → concatenation[position]? = sequence position) ∧
      (concatenation.length = count ∨ sequence concatenation.length = none) := by
  induction count with
  | zero => exact ⟨Nat.le_refl _, fun position isInside => by simp [concatUpTo] at isInside,
      Or.inl rfl⟩
  | succ count inductionHypothesis =>
    obtain ⟨isShort, isElement, isFullOrClosed⟩ := inductionHypothesis
    simp only [concatUpTo]
    cases isFullOrClosed with
    | inl isFull =>
      cases hasValue : sequence count with
      | none =>
        rw [Option.toList_none, List.append_nil]
        refine ⟨by omega, isElement, Or.inr ?_⟩
        rw [isFull]
        exact hasValue
      | some value =>
        rw [Option.toList_some, List.length_append, List.length_singleton]
        refine ⟨by omega, fun position isInside => ?_, Or.inl (by omega)⟩
        by_cases isOld : position < (concatUpTo (fun position => (sequence position).toList)
            count).length
        · rw [List.getElem?_append_left isOld]
          exact isElement position isOld
        · have isLast : position = count := by omega
          rw [List.getElem?_append_right (by omega), isLast, hasValue, isFull, Nat.sub_self]
          rfl
    | inr isNoneAtLength =>
      have isNoneAtCount := wpf6c_none_of_le sequence isClosed isNoneAtLength isShort
      rw [isNoneAtCount, Option.toList_none, List.append_nil]
      exact ⟨by omega, isElement, Or.inr isNoneAtLength⟩

/-- A sequence of naturals below a bound has a maximum, found by climbing. -/
theorem wpf6c_exists_max_of_bounded (values : Nat → Nat) (bound : Nat)
    (isBelow : ∀ index, values index < bound) :
    ∀ gap start, bound - values start ≤ gap → ∃ maximum, ∀ index, values index ≤ values maximum := by
  intro gap
  induction gap with
  | zero =>
    intro start isGap
    have isStartBelow := isBelow start
    omega
  | succ gap inductionHypothesis =>
    intro start isGap
    by_cases isHigher : ∃ index, values start < values index
    · obtain ⟨index, isLarger⟩ := isHigher
      have isIndexBelow := isBelow index
      exact inductionHypothesis index (by omega)
    · exact ⟨start, fun index => Nat.le_of_not_lt fun isLarger => isHigher ⟨index, isLarger⟩⟩

/-- The element at a position of a finite concatenation is the element of the
concatenation. -/
theorem wpf6c_isConcatenation_getElem {α : Type} (chunks : Nat → List α)
    (sequence : Sequence α) (isConcatenation : IsConcatenation chunks sequence)
    (count position : Nat) (isInside : position < (concatUpTo chunks count).length) :
    sequence position = (concatUpTo chunks count)[position]? := by
  cases isConcatenation position with
  | inl found =>
    obtain ⟨otherCount, isOtherInside, isElement⟩ := found
    rw [isElement]
    exact wpf6c_concatUpTo_getElem?_agree chunks otherCount count position isOtherInside isInside
  | inr missing =>
    have isShort := missing.1 count
    omega

/-- A chunk family has at most one concatenation. -/
theorem wpf6_isConcatenation_unique {α : Type} (chunks : Nat → List α)
    (first second : Sequence α) (isFirst : IsConcatenation chunks first)
    (isSecond : IsConcatenation chunks second) : first = second := by
  funext position
  cases isFirst position with
  | inl foundFirst =>
    obtain ⟨count, isInside, isElement⟩ := foundFirst
    cases isSecond position with
    | inl foundSecond =>
      obtain ⟨otherCount, isOtherInside, isOtherElement⟩ := foundSecond
      rw [isElement, isOtherElement]
      exact wpf6c_concatUpTo_getElem?_agree chunks count otherCount position isInside isOtherInside
    | inr missing =>
      have isShort := missing.1 count
      omega
  | inr missing =>
    rw [missing.2]
    cases isSecond position with
    | inl foundSecond =>
      obtain ⟨count, isInside, _⟩ := foundSecond
      have isShort := missing.1 count
      omega
    | inr otherMissing => exact otherMissing.2.symm

/-- Every chunk family has a concatenation. -/
theorem wpf6_exists_isConcatenation {α : Type} (chunks : Nat → List α) :
    ∃ sequence, IsConcatenation chunks sequence := by
  classical
  refine ⟨fun position =>
    if isPresent : ∃ count, position < (concatUpTo chunks count).length then
      (concatUpTo chunks (Classical.choose isPresent))[position]?
    else none, ?_⟩
  intro position
  by_cases isPresent : ∃ count, position < (concatUpTo chunks count).length
  · refine Or.inl ⟨Classical.choose isPresent, Classical.choose_spec isPresent, ?_⟩
    simp [isPresent]
  · refine Or.inr ⟨fun count => ?_, ?_⟩
    · exact Nat.le_of_not_lt fun isInside => isPresent ⟨count, isInside⟩
    · simp [isPresent]

/-- A concatenation stays absent from its first absent position on. -/
theorem wpf6_isConcatenation_none_succ {α : Type} (chunks : Nat → List α)
    (sequence : Sequence α) (isConcatenation : IsConcatenation chunks sequence)
    (position : Nat) (isNone : sequence position = none) : sequence (position + 1) = none := by
  cases isConcatenation (position + 1) with
  | inl found =>
    obtain ⟨count, isInside, _⟩ := found
    have isElement := wpf6c_isConcatenation_getElem chunks sequence isConcatenation count position
      (by omega)
    rw [isNone, List.getElem?_eq_getElem (by omega)] at isElement
    cases isElement
  | inr missing => exact missing.2

/-- The element at a position of a finite concatenation is the element of the
concatenation. -/
theorem wpf6_isConcatenation_getElem {α : Type} (chunks : Nat → List α)
    (sequence : Sequence α) (isConcatenation : IsConcatenation chunks sequence)
    (count position : Nat) (isInside : position < (concatUpTo chunks count).length) :
    sequence position = (concatUpTo chunks count)[position]? :=
  wpf6c_isConcatenation_getElem chunks sequence isConcatenation count position isInside

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
  intro position
  by_cases isPresent : ∃ count, position < (concatUpTo chunks count).length
  · obtain ⟨count, isInside⟩ := isPresent
    obtain ⟨index, isCovered⟩ := covers count
    have isBlockInside : position < (concatUpTo blocks index).length := by
      rw [agree]
      omega
    refine Or.inl ⟨index, isBlockInside, ?_⟩
    rw [agree]
    rw [agree] at isBlockInside
    exact wpf6c_isConcatenation_getElem chunks sequence isConcatenation (boundary index) position
      isBlockInside
  · refine Or.inr ⟨fun index => ?_, ?_⟩
    · rw [agree]
      exact Nat.le_of_not_lt fun isInside => isPresent ⟨boundary index, isInside⟩
    · cases isConcatenation position with
      | inl found =>
        obtain ⟨count, isInside, _⟩ := found
        exact absurd ⟨count, isInside⟩ isPresent
      | inr missing => exact missing.2

/-- A sequence that stays absent from its first absent position on is the
concatenation of its positions. -/
theorem wpf6_isConcatenation_pointwise {α : Type} (sequence : Sequence α)
    (isClosed : ∀ position, sequence position = none → sequence (position + 1) = none) :
    IsConcatenation (fun position => (sequence position).toList) sequence := by
  intro position
  by_cases isPresent : ∃ count,
      position < (concatUpTo (fun position => (sequence position).toList) count).length
  · obtain ⟨count, isInside⟩ := isPresent
    exact Or.inl ⟨count, isInside,
      ((wpf6c_pointwise_concatUpTo sequence isClosed count).2.1 position isInside).symm⟩
  · refine Or.inr ⟨fun count => Nat.le_of_not_lt fun isInside => isPresent ⟨count, isInside⟩, ?_⟩
    have isShort : (concatUpTo (fun position => (sequence position).toList)
        (position + 1)).length ≤ position :=
      Nat.le_of_not_lt fun isInside => isPresent ⟨position + 1, isInside⟩
    obtain ⟨_, _, isFullOrClosed⟩ := wpf6c_pointwise_concatUpTo sequence isClosed (position + 1)
    cases isFullOrClosed with
    | inl isFull => omega
    | inr isNoneAtLength => exact wpf6c_none_of_le sequence isClosed isNoneAtLength isShort

/-- A non-decreasing bounded sequence of naturals reaches its supremum: either
it exceeds every bound or some index carries a maximum. -/
theorem wpf6_exists_max_or_unbounded (values : Nat → Nat)
    (isMonotone : ∀ index, values index ≤ values (index + 1)) :
    (∀ bound, ∃ index, bound ≤ values index) ∨
      ∃ maximum, ∀ index, values index ≤ values maximum := by
  -- Monotonicity is not needed: a bounded sequence of naturals has a maximum.
  have _ := isMonotone
  by_cases isUnbounded : ∀ bound, ∃ index, bound ≤ values index
  · exact Or.inl isUnbounded
  · refine Or.inr ?_
    have isBounded : ∃ bound, ∀ index, values index < bound :=
      Classical.byContradiction fun noBound =>
        isUnbounded fun bound => Classical.byContradiction fun noIndex =>
          noBound ⟨bound, fun index => Nat.lt_of_not_le fun isAbove => noIndex ⟨index, isAbove⟩⟩
    obtain ⟨bound, isBelow⟩ := isBounded
    exact wpf6c_exists_max_of_bounded values bound isBelow (bound - values 0) 0 (Nat.le_refl _)

end DexExpressiveness
