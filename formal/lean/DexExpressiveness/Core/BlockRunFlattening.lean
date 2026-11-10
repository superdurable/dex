import DexExpressiveness.Core.TransitionSystem

/-!
# Flattening blocks: the lemmas behind work package L1

`Core/BlockRun.lean` states what the flattening of a run in blocks keeps. This
file proves the facts those statements need. `Core/BlockRun.lean` imports this
file, so nothing here mentions `BlockRun`: the blocks are a function `blocks`,
and the block boundaries are a function `boundary` with the two equations of
`BlockRun.IsFlattenedTo`.

The file has four parts: `concatUpTo` and `IsConcatenation`, the block
boundaries, the labels of a flattened run, and the runs of a transition system
(`exec` along a run, and the construction of the flattened run).
-/

namespace DexExpressiveness

/-! ## `concatUpTo` and `IsConcatenation` -/

/-- Mapping every chunk maps the concatenation. -/
theorem concatUpTo_map {α β : Type} (transform : α → β) (chunks : Nat → List α) (count : Nat) :
    concatUpTo (fun index => (chunks index).map transform) count =
      (concatUpTo chunks count).map transform := by
  induction count with
  | zero => rfl
  | succ count inductionHypothesis => simp [concatUpTo, inductionHypothesis]

/-- Filtering every chunk filters the concatenation. -/
theorem concatUpTo_filterMap {α β : Type} (transform : α → Option β) (chunks : Nat → List α)
    (count : Nat) :
    concatUpTo (fun index => (chunks index).filterMap transform) count =
      (concatUpTo chunks count).filterMap transform := by
  induction count with
  | zero => rfl
  | succ count inductionHypothesis => simp [concatUpTo, inductionHypothesis]

/-- The first `first + second` chunks are the first `first` chunks and then
the next `second` chunks. -/
theorem concatUpTo_add {α : Type} (chunks : Nat → List α) (first second : Nat) :
    concatUpTo chunks (first + second) =
      concatUpTo chunks first ++ concatUpTo (fun offset => chunks (first + offset)) second := by
  induction second with
  | zero => simp [concatUpTo]
  | succ second inductionHypothesis =>
    show concatUpTo chunks (first + second + 1) = _
    simp [concatUpTo, inductionHypothesis]

/-- A longer concatenation is at least as long. -/
theorem concatUpTo_length_le {α : Type} (chunks : Nat → List α) {first second : Nat}
    (isBefore : first ≤ second) :
    (concatUpTo chunks first).length ≤ (concatUpTo chunks second).length := by
  obtain ⟨extra, rfl⟩ := Nat.exists_eq_add_of_le isBefore
  rw [concatUpTo_add, List.length_append]
  exact Nat.le_add_right _ _

/-- A longer concatenation keeps the elements of a shorter one. -/
theorem concatUpTo_getElem?_of_le {α : Type} (chunks : Nat → List α) {first second : Nat}
    (isBefore : first ≤ second) (position : Nat)
    (isInside : position < (concatUpTo chunks first).length) :
    (concatUpTo chunks second)[position]? = (concatUpTo chunks first)[position]? := by
  obtain ⟨extra, rfl⟩ := Nat.exists_eq_add_of_le isBefore
  rw [concatUpTo_add, List.getElem?_append_left isInside]

/-- Two chunk families have the same concatenations when every finite
concatenation of the first is a finite concatenation of the second, at
boundaries that grow without bound. -/
theorem isConcatenation_iff_of_cofinal {α : Type} (blockChunks stepChunks : Nat → List α)
    (boundary : Nat → Nat) (isCofinal : ∀ bound, ∃ index, bound ≤ boundary index)
    (agree : ∀ index, concatUpTo blockChunks index = concatUpTo stepChunks (boundary index))
    (sequence : Sequence α) :
    IsConcatenation blockChunks sequence ↔ IsConcatenation stepChunks sequence := by
  constructor
  · intro isConcatenation position
    cases isConcatenation position with
    | inl found =>
      obtain ⟨count, isInside, isElement⟩ := found
      rw [agree] at isInside isElement
      exact Or.inl ⟨boundary count, isInside, isElement⟩
    | inr missing =>
      refine Or.inr ⟨fun count => ?_, missing.2⟩
      obtain ⟨index, isBefore⟩ := isCofinal count
      have isShorter := concatUpTo_length_le stepChunks isBefore
      have isShort := missing.1 index
      rw [agree] at isShort
      omega
  · intro isConcatenation position
    cases isConcatenation position with
    | inl found =>
      obtain ⟨count, isInside, isElement⟩ := found
      obtain ⟨index, isBefore⟩ := isCofinal count
      have isShorter := concatUpTo_length_le stepChunks isBefore
      refine Or.inl ⟨index, ?_, ?_⟩
      · rw [agree]
        omega
      · rw [agree, concatUpTo_getElem?_of_le stepChunks isBefore position isInside]
        exact isElement
    | inr missing =>
      refine Or.inr ⟨fun index => ?_, missing.2⟩
      rw [agree]
      exact missing.1 (boundary index)

/-- An injective map gives an injective map on optional values. -/
theorem option_map_injective {α β : Type} (transform : α → β)
    (isInjective : ∀ first second, transform first = transform second → first = second)
    (first second : Option α) (isEqual : first.map transform = second.map transform) :
    first = second := by
  cases first with
  | none =>
    cases second with
    | none => rfl
    | some secondValue => simp at isEqual
  | some firstValue =>
    cases second with
    | none => simp at isEqual
    | some secondValue =>
      simp only [Option.map_some, Option.some.injEq] at isEqual
      rw [isInjective firstValue secondValue isEqual]

/-! ## Block boundaries

`boundary index` is the number of labels in the blocks before block `index`. -/

/-- The boundary of a block is the length of the blocks before it. -/
theorem boundary_eq_length {Label : Type} (blocks : Nat → List Label) (boundary : Nat → Nat)
    (startsAtZero : boundary 0 = 0)
    (nextBoundary : ∀ index, boundary (index + 1) = boundary index + (blocks index).length)
    (index : Nat) : boundary index = (concatUpTo blocks index).length := by
  induction index with
  | zero => simpa [concatUpTo] using startsAtZero
  | succ index inductionHypothesis => simp [concatUpTo, nextBoundary, inductionHypothesis]

/-- A later block starts later, or at the same position. -/
theorem boundary_le_of_le {Label : Type} (blocks : Nat → List Label) (boundary : Nat → Nat)
    (nextBoundary : ∀ index, boundary (index + 1) = boundary index + (blocks index).length)
    {first second : Nat} (isBefore : first ≤ second) : boundary first ≤ boundary second := by
  induction second with
  | zero =>
    have isZero : first = 0 := by omega
    subst isZero
    exact Nat.le_refl _
  | succ second inductionHypothesis =>
    by_cases isEqual : first = second + 1
    · subst isEqual
      exact Nat.le_refl _
    · have isEarlier := inductionHypothesis (by omega)
      have isNext := nextBoundary second
      omega

/-- With infinitely many non-empty blocks, the boundaries grow without bound. -/
theorem boundary_cofinal {Label : Type} (blocks : Nat → List Label) (boundary : Nat → Nat)
    (nextBoundary : ∀ index, boundary (index + 1) = boundary index + (blocks index).length)
    (isInfinite : ∀ bound, ∃ index, bound ≤ index ∧ blocks index ≠ []) (bound : Nat) :
    ∃ index, bound ≤ boundary index := by
  induction bound with
  | zero => exact ⟨0, Nat.zero_le _⟩
  | succ bound inductionHypothesis =>
    obtain ⟨index, isReached⟩ := inductionHypothesis
    obtain ⟨later, isLater, isNonEmpty⟩ := isInfinite index
    have isMonotone := boundary_le_of_le blocks boundary nextBoundary isLater
    have isNext := nextBoundary later
    have isPositive : 0 < (blocks later).length := List.length_pos_iff.mpr isNonEmpty
    exact ⟨later + 1, by omega⟩

/-- A position before a boundary lies in one of the blocks before it. -/
theorem exists_block_of_lt_boundary {Label : Type} (blocks : Nat → List Label)
    (boundary : Nat → Nat) (startsAtZero : boundary 0 = 0)
    (nextBoundary : ∀ index, boundary (index + 1) = boundary index + (blocks index).length)
    (position index : Nat) (isBefore : position < boundary index) :
    ∃ block offset, offset < (blocks block).length ∧ position = boundary block + offset := by
  induction index with
  | zero => omega
  | succ index inductionHypothesis =>
    by_cases isEarlier : position < boundary index
    · exact inductionHypothesis isEarlier
    · have isNext := nextBoundary index
      exact ⟨index, position - boundary index, by omega, by omega⟩

/-- With infinitely many non-empty blocks, every position lies in a block. -/
theorem exists_block_of_position {Label : Type} (blocks : Nat → List Label)
    (boundary : Nat → Nat) (startsAtZero : boundary 0 = 0)
    (nextBoundary : ∀ index, boundary (index + 1) = boundary index + (blocks index).length)
    (isInfinite : ∀ bound, ∃ index, bound ≤ index ∧ blocks index ≠ []) (position : Nat) :
    ∃ block offset, offset < (blocks block).length ∧ position = boundary block + offset := by
  obtain ⟨index, isReached⟩ := boundary_cofinal blocks boundary nextBoundary isInfinite (position + 1)
  exact exists_block_of_lt_boundary blocks boundary startsAtZero nextBoundary position index
    (by omega)

/-- A position inside a block starts no later block. -/
theorem block_not_lt_of_position_eq {Label : Type} (blocks : Nat → List Label)
    (boundary : Nat → Nat)
    (nextBoundary : ∀ index, boundary (index + 1) = boundary index + (blocks index).length)
    {firstIndex firstOffset secondIndex secondOffset : Nat}
    (isFirstInside : firstOffset < (blocks firstIndex).length)
    (isSamePosition : boundary firstIndex + firstOffset = boundary secondIndex + secondOffset) :
    ¬ firstIndex < secondIndex := by
  intro isBefore
  have isMonotone := boundary_le_of_le blocks boundary nextBoundary
    (show firstIndex + 1 ≤ secondIndex from isBefore)
  have isNext := nextBoundary firstIndex
  omega

/-- A position lies in one block only, at one offset. -/
theorem block_unique {Label : Type} (blocks : Nat → List Label) (boundary : Nat → Nat)
    (nextBoundary : ∀ index, boundary (index + 1) = boundary index + (blocks index).length)
    {firstIndex firstOffset secondIndex secondOffset : Nat}
    (isFirstInside : firstOffset < (blocks firstIndex).length)
    (isSecondInside : secondOffset < (blocks secondIndex).length)
    (isSamePosition : boundary firstIndex + firstOffset = boundary secondIndex + secondOffset) :
    firstIndex = secondIndex ∧ firstOffset = secondOffset := by
  have isNotBefore :=
    block_not_lt_of_position_eq blocks boundary nextBoundary isFirstInside isSamePosition
  have isNotAfter :=
    block_not_lt_of_position_eq blocks boundary nextBoundary isSecondInside isSamePosition.symm
  have isSameIndex : firstIndex = secondIndex := by omega
  subst isSameIndex
  exact ⟨rfl, by omega⟩

/-! ## The labels of a flattened run -/

/-- A list whose elements are given by `labelAt` is `labelAt` on a range. -/
theorem list_eq_range_map {Label : Type} (labels : List Label) (labelAt : Nat → Label)
    (isLabel : ∀ offset label, labels[offset]? = some label → labelAt offset = label) :
    labels = (List.range labels.length).map labelAt := by
  apply List.ext_getElem
  · simp
  · intro offset isInside isInsideRange
    simp only [List.getElem_map, List.getElem_range]
    exact (isLabel offset labels[offset] (List.getElem?_eq_getElem isInside)).symm

/-- The first `offset` elements of such a list are `labelAt` on a range. -/
theorem take_eq_range_map {Label : Type} (labels : List Label) (labelAt : Nat → Label)
    (isLabel : ∀ offset label, labels[offset]? = some label → labelAt offset = label)
    (count : Nat) (isInside : count ≤ labels.length) :
    labels.take count = (List.range count).map labelAt := by
  have isTaken := list_eq_range_map (labels.take count) labelAt (by
    intro offset label isElement
    rw [List.getElem?_take] at isElement
    split at isElement
    · exact isLabel offset label isElement
    · cases isElement)
  rw [List.length_take, Nat.min_eq_left isInside] at isTaken
  exact isTaken

/-- The blocks before a boundary are the labels of the flattened run before it. -/
theorem concatUpTo_eq_range_map {Label : Type} (blocks : Nat → List Label)
    (boundary : Nat → Nat) (startsAtZero : boundary 0 = 0)
    (nextBoundary : ∀ index, boundary (index + 1) = boundary index + (blocks index).length)
    (labelAt : Nat → Label)
    (labelInBlock : ∀ index offset label, (blocks index)[offset]? = some label →
      labelAt (boundary index + offset) = label)
    (count : Nat) : concatUpTo blocks count = (List.range (boundary count)).map labelAt := by
  induction count with
  | zero => simp [concatUpTo, startsAtZero]
  | succ count inductionHypothesis =>
    rw [nextBoundary, List.range_add, List.map_append, List.map_map]
    simp only [concatUpTo]
    rw [inductionHypothesis]
    congr 1
    exact list_eq_range_map (blocks count) (fun offset => labelAt (boundary count + offset))
      (labelInBlock count)

/-- The optional values of the first `count` labels, one chunk per label. -/
theorem concatUpTo_toList_eq_filterMap {Label Request : Type} (requestOf : Label → Option Request)
    (labelAt : Nat → Label) (count : Nat) :
    concatUpTo (fun position => (requestOf (labelAt position)).toList) count =
      ((List.range count).map labelAt).filterMap requestOf := by
  induction count with
  | zero => rfl
  | succ count inductionHypothesis =>
    simp only [concatUpTo, List.range_succ, List.map_append, List.filterMap_append,
      inductionHypothesis]
    cases isRequest : requestOf (labelAt count) <;> simp [isRequest]

/-- A property holds for every label of the flattened run from some position
on exactly when it holds for every label of every block from some block on. -/
theorem eventually_labels_iff {Label : Type} (blocks : Nat → List Label) (boundary : Nat → Nat)
    (startsAtZero : boundary 0 = 0)
    (nextBoundary : ∀ index, boundary (index + 1) = boundary index + (blocks index).length)
    (isInfinite : ∀ bound, ∃ index, bound ≤ index ∧ blocks index ≠ [])
    (labelAt : Nat → Label)
    (labelInBlock : ∀ index offset label, (blocks index)[offset]? = some label →
      labelAt (boundary index + offset) = label)
    (property : Label → Prop) :
    (∃ bound, ∀ position, bound ≤ position → property (labelAt position)) ↔
      ∃ bound, ∀ index, bound ≤ index → ∀ label, label ∈ blocks index → property label := by
  constructor
  · intro isEventual
    obtain ⟨bound, holdsFrom⟩ := isEventual
    obtain ⟨firstBlock, isReached⟩ := boundary_cofinal blocks boundary nextBoundary isInfinite bound
    refine ⟨firstBlock, fun index isLater label isMember => ?_⟩
    obtain ⟨offset, isElement⟩ := List.mem_iff_getElem?.mp isMember
    have isMonotone := boundary_le_of_le blocks boundary nextBoundary isLater
    rw [← labelInBlock index offset label isElement]
    exact holdsFrom (boundary index + offset) (by omega)
  · intro isEventual
    obtain ⟨firstBlock, holdsFrom⟩ := isEventual
    refine ⟨boundary firstBlock, fun position isLater => ?_⟩
    obtain ⟨index, offset, isInside, isPosition⟩ :=
      exists_block_of_position blocks boundary startsAtZero nextBoundary isInfinite position
    have isNotBefore : ¬ index < firstBlock := by
      intro isBefore
      have isMonotone := boundary_le_of_le blocks boundary nextBoundary
        (show index + 1 ≤ firstBlock from isBefore)
      have isNext := nextBoundary index
      omega
    have isElement : (blocks index)[offset]? = some (blocks index)[offset] :=
      List.getElem?_eq_getElem isInside
    rw [isPosition, labelInBlock index offset _ isElement]
    exact holdsFrom index (by omega) _ (List.getElem_mem isInside)

namespace TransitionSystem

variable {State Label Event Request : Type}
variable (system : TransitionSystem State Label Event Request)

/-! ## `exec` along a run -/

/-- The step of a run at `position` produces `eventsAt`. -/
theorem step_eq_eventsAt (run : InfiniteRun State Label)
    (stepsFrom : ∀ index, ∃ events,
      system.step (run.states index) (run.labels index) = some (events, run.states (index + 1)))
    (position : Nat) :
    system.step (run.states position) (run.labels position) =
      some (system.eventsAt run position, run.states (position + 1)) := by
  obtain ⟨events, isStep⟩ := stepsFrom position
  simp only [eventsAt, isStep]

/-- The `count` labels of a run from `start` run from the state at `start` to
the state at `start + count`, with the events of these transitions. -/
theorem exec_range (run : InfiniteRun State Label)
    (stepsFrom : ∀ index, ∃ events,
      system.step (run.states index) (run.labels index) = some (events, run.states (index + 1)))
    (start count : Nat) :
    system.exec (run.states start)
        ((List.range count).map (fun offset => run.labels (start + offset))) =
      some (concatUpTo (fun offset => system.eventsAt run (start + offset)) count,
        run.states (start + count)) := by
  induction count with
  | zero => rfl
  | succ count inductionHypothesis =>
    rw [List.range_succ, List.map_append]
    exact system.exec_snoc (run.states start) _ _ _ _ _ _ inductionHypothesis
      (system.step_eq_eventsAt run stepsFrom (start + count))

/-- A run of `first ++ second` splits into a run of `first` and a run of
`second`. -/
theorem exec_append_inv (state : State) (firstLabels secondLabels : List Label)
    (events : List Event) (final : State)
    (isExec : system.exec state (firstLabels ++ secondLabels) = some (events, final)) :
    ∃ firstEvents middle secondEvents,
      system.exec state firstLabels = some (firstEvents, middle) ∧
      system.exec middle secondLabels = some (secondEvents, final) ∧
      events = firstEvents ++ secondEvents := by
  induction firstLabels generalizing state events with
  | nil => exact ⟨[], state, events, rfl, isExec, rfl⟩
  | cons label laterLabels inductionHypothesis =>
    obtain ⟨stepEvents, next, laterEvents, isStep, isLater, isEvents⟩ :=
      system.exec_cons state label (laterLabels ++ secondLabels) events final isExec
    obtain ⟨firstEvents, middle, secondEvents, isFirst, isSecond, isSplit⟩ :=
      inductionHypothesis next laterEvents isLater
    refine ⟨stepEvents ++ firstEvents, middle, secondEvents, ?_, isSecond, ?_⟩
    · simp [exec, isStep, isFirst]
    · rw [isEvents, isSplit, List.append_assoc]

/-- A prefix of a finite run is a finite run. -/
theorem exec_take_of_exec (state : State) (labels : List Label) (events : List Event)
    (final : State) (isExec : system.exec state labels = some (events, final)) (count : Nat) :
    ∃ takenEvents middle, system.exec state (labels.take count) = some (takenEvents, middle) := by
  rw [← List.take_append_drop count labels] at isExec
  obtain ⟨firstEvents, middle, _, isFirst, _, _⟩ :=
    system.exec_append_inv state _ _ events final isExec
  exact ⟨firstEvents, middle, isFirst⟩

/-! ## A run that is the blocks, one label after the other

`states`, `blocks`, and `events` are the fields of a run in blocks, and
`blockExec` says that every block is a finite run. -/

/-- The state inside a block of the flattened run is the state after the
labels of the block taken so far, with the events of these transitions. -/
theorem exec_take_of_flattened (states : Nat → State) (blocks : Nat → List Label)
    (run : InfiniteRun State Label)
    (stepsFrom : ∀ index, ∃ events,
      system.step (run.states index) (run.labels index) = some (events, run.states (index + 1)))
    (boundary : Nat → Nat)
    (stateAtBoundary : ∀ index, run.states (boundary index) = states index)
    (labelInBlock : ∀ index offset label, (blocks index)[offset]? = some label →
      run.labels (boundary index + offset) = label)
    (index offset : Nat) (isInside : offset ≤ (blocks index).length) :
    system.exec (states index) ((blocks index).take offset) =
      some (concatUpTo (fun inner => system.eventsAt run (boundary index + inner)) offset,
        run.states (boundary index + offset)) := by
  rw [take_eq_range_map (blocks index) (fun inner => run.labels (boundary index + inner))
    (labelInBlock index) offset isInside, ← stateAtBoundary]
  exact system.exec_range run stepsFrom (boundary index) offset

/-- The events of a block are the events of its transitions in the flattened
run. -/
theorem blockEvents_eq_concatUpTo (states : Nat → State) (blocks : Nat → List Label)
    (events : Nat → List Event)
    (blockExec : ∀ index,
      system.exec (states index) (blocks index) = some (events index, states (index + 1)))
    (run : InfiniteRun State Label)
    (stepsFrom : ∀ index, ∃ events,
      system.step (run.states index) (run.labels index) = some (events, run.states (index + 1)))
    (boundary : Nat → Nat)
    (stateAtBoundary : ∀ index, run.states (boundary index) = states index)
    (labelInBlock : ∀ index offset label, (blocks index)[offset]? = some label →
      run.labels (boundary index + offset) = label)
    (index : Nat) :
    events index =
      concatUpTo (fun offset => system.eventsAt run (boundary index + offset))
        (blocks index).length := by
  have isExec := system.exec_take_of_flattened states blocks run stepsFrom boundary
    stateAtBoundary labelInBlock index (blocks index).length (Nat.le_refl _)
  rw [List.take_length, blockExec] at isExec
  simp only [Option.some.injEq, Prod.mk.injEq] at isExec
  exact isExec.1

/-- The events of the blocks before a boundary are the events of the
flattened run before it. -/
theorem concatUpTo_events_eq (states : Nat → State) (blocks : Nat → List Label)
    (events : Nat → List Event)
    (blockExec : ∀ index,
      system.exec (states index) (blocks index) = some (events index, states (index + 1)))
    (run : InfiniteRun State Label)
    (stepsFrom : ∀ index, ∃ events,
      system.step (run.states index) (run.labels index) = some (events, run.states (index + 1)))
    (boundary : Nat → Nat) (startsAtZero : boundary 0 = 0)
    (nextBoundary : ∀ index, boundary (index + 1) = boundary index + (blocks index).length)
    (stateAtBoundary : ∀ index, run.states (boundary index) = states index)
    (labelInBlock : ∀ index offset label, (blocks index)[offset]? = some label →
      run.labels (boundary index + offset) = label)
    (count : Nat) :
    concatUpTo events count = concatUpTo (system.eventsAt run) (boundary count) := by
  induction count with
  | zero => simp [concatUpTo, startsAtZero]
  | succ count inductionHypothesis =>
    rw [nextBoundary, concatUpTo_add (system.eventsAt run), ← inductionHypothesis,
      ← system.blockEvents_eq_concatUpTo states blocks events blockExec run stepsFrom boundary
        stateAtBoundary labelInBlock count]
    rfl

/-- The blocks between two equal boundaries are empty, so the states before
them are equal. -/
theorem blockStates_eq_of_boundary_eq (states : Nat → State) (blocks : Nat → List Label)
    (events : Nat → List Event)
    (blockExec : ∀ index,
      system.exec (states index) (blocks index) = some (events index, states (index + 1)))
    (boundary : Nat → Nat)
    (nextBoundary : ∀ index, boundary (index + 1) = boundary index + (blocks index).length)
    (first extra : Nat) (isSameBoundary : boundary first = boundary (first + extra)) :
    states first = states (first + extra) := by
  induction extra with
  | zero => rfl
  | succ extra inductionHypothesis =>
    have isSame : boundary first = boundary (first + extra + 1) := isSameBoundary
    have isMonotone :=
      boundary_le_of_le blocks boundary nextBoundary (Nat.le_add_right first extra)
    have isNext := nextBoundary (first + extra)
    have isEmpty : blocks (first + extra) = [] := List.eq_nil_of_length_eq_zero (by omega)
    have isExec := blockExec (first + extra)
    rw [isEmpty] at isExec
    simp only [exec, Option.some.injEq, Prod.mk.injEq] at isExec
    rw [inductionHypothesis (by omega)]
    exact isExec.2

/-- Some run takes, at every position inside a block, the label of the block
there, from the state after the labels of the block taken so far. Each
position lies in one block, so the label and the state are chosen per
position. -/
theorem exists_run_inside_blocks (states : Nat → State) (blocks : Nat → List Label)
    (events : Nat → List Event)
    (blockExec : ∀ index,
      system.exec (states index) (blocks index) = some (events index, states (index + 1)))
    (boundary : Nat → Nat) (startsAtZero : boundary 0 = 0)
    (nextBoundary : ∀ index, boundary (index + 1) = boundary index + (blocks index).length)
    (isInfinite : ∀ bound, ∃ index, bound ≤ index ∧ blocks index ≠ []) :
    ∃ run : InfiniteRun State Label, ∀ index offset, offset < (blocks index).length →
      (blocks index)[offset]? = some (run.labels (boundary index + offset)) ∧
      ∃ takenEvents, system.exec (states index) ((blocks index).take offset) =
        some (takenEvents, run.states (boundary index + offset)) := by
  have atPosition : ∀ position, ∃ chosen : Label × State,
      ∀ index offset, offset < (blocks index).length → position = boundary index + offset →
        (blocks index)[offset]? = some chosen.1 ∧
        ∃ takenEvents, system.exec (states index) ((blocks index).take offset) =
          some (takenEvents, chosen.2) := by
    intro position
    obtain ⟨block, blockOffset, isBlockInside, isBlockPosition⟩ :=
      exists_block_of_position blocks boundary startsAtZero nextBoundary isInfinite position
    obtain ⟨takenEvents, middle, isTaken⟩ :=
      system.exec_take_of_exec _ _ _ _ (blockExec block) blockOffset
    refine ⟨((blocks block)[blockOffset], middle), ?_⟩
    intro index offset isInside isPosition
    obtain ⟨isSameIndex, isSameOffset⟩ :=
      block_unique blocks boundary nextBoundary isInside isBlockInside (by omega)
    subst isSameIndex
    subst isSameOffset
    exact ⟨List.getElem?_eq_getElem isInside, takenEvents, isTaken⟩
  obtain ⟨chosenAt, isChosen⟩ := Classical.axiomOfChoice atPosition
  exact ⟨⟨fun position => (chosenAt position).2, fun position => (chosenAt position).1⟩,
    fun index offset isInside => isChosen _ index offset isInside rfl⟩

/-- Such a run is in a block's state where the block starts, also when the
block is empty. -/
theorem stateAtBoundary_of_inside_blocks (states : Nat → State) (blocks : Nat → List Label)
    (events : Nat → List Event)
    (blockExec : ∀ index,
      system.exec (states index) (blocks index) = some (events index, states (index + 1)))
    (boundary : Nat → Nat) (startsAtZero : boundary 0 = 0)
    (nextBoundary : ∀ index, boundary (index + 1) = boundary index + (blocks index).length)
    (isInfinite : ∀ bound, ∃ index, bound ≤ index ∧ blocks index ≠ [])
    (run : InfiniteRun State Label)
    (isInsideBlocks : ∀ index offset, offset < (blocks index).length →
      (blocks index)[offset]? = some (run.labels (boundary index + offset)) ∧
      ∃ takenEvents, system.exec (states index) ((blocks index).take offset) =
        some (takenEvents, run.states (boundary index + offset)))
    (index : Nat) : run.states (boundary index) = states index := by
  obtain ⟨block, offset, isInside, isPosition⟩ :=
    exists_block_of_position blocks boundary startsAtZero nextBoundary isInfinite
      (boundary index)
  have isNotBefore : ¬ block < index :=
    block_not_lt_of_position_eq blocks boundary nextBoundary (secondOffset := 0) isInside
      (by omega)
  have isMonotone :=
    boundary_le_of_le blocks boundary nextBoundary (show index ≤ block by omega)
  have isZero : offset = 0 := by omega
  subst isZero
  obtain ⟨_, takenEvents, isTaken⟩ := isInsideBlocks block 0 isInside
  simp only [List.take_zero, exec, Option.some.injEq, Prod.mk.injEq] at isTaken
  obtain ⟨extra, rfl⟩ := Nat.exists_eq_add_of_le (show index ≤ block by omega)
  rw [isPosition, ← isTaken.2]
  exact (system.blockStates_eq_of_boundary_eq states blocks events blockExec boundary
    nextBoundary index extra (by omega)).symm

/-- Such a run takes a transition of the system at every position. -/
theorem stepsFrom_of_inside_blocks (states : Nat → State) (blocks : Nat → List Label)
    (events : Nat → List Event)
    (blockExec : ∀ index,
      system.exec (states index) (blocks index) = some (events index, states (index + 1)))
    (boundary : Nat → Nat) (startsAtZero : boundary 0 = 0)
    (nextBoundary : ∀ index, boundary (index + 1) = boundary index + (blocks index).length)
    (isInfinite : ∀ bound, ∃ index, bound ≤ index ∧ blocks index ≠ [])
    (run : InfiniteRun State Label)
    (isInsideBlocks : ∀ index offset, offset < (blocks index).length →
      (blocks index)[offset]? = some (run.labels (boundary index + offset)) ∧
      ∃ takenEvents, system.exec (states index) ((blocks index).take offset) =
        some (takenEvents, run.states (boundary index + offset)))
    (position : Nat) :
    ∃ stepEvents, system.step (run.states position) (run.labels position) =
      some (stepEvents, run.states (position + 1)) := by
  obtain ⟨index, offset, isInside, rfl⟩ :=
    exists_block_of_position blocks boundary startsAtZero nextBoundary isInfinite position
  obtain ⟨isLabel, takenEvents, isTaken⟩ := isInsideBlocks index offset isInside
  have isTakeSucc : (blocks index).take (offset + 1) =
      (blocks index).take offset ++ [run.labels (boundary index + offset)] := by
    rw [List.take_add_one, isLabel]
    rfl
  have isNextState : ∃ nextEvents,
      system.exec (states index) ((blocks index).take (offset + 1)) =
        some (nextEvents, run.states (boundary index + offset + 1)) := by
    by_cases isLast : offset + 1 < (blocks index).length
    · exact (isInsideBlocks index (offset + 1) isLast).2
    · have isWhole : (blocks index).take (offset + 1) = blocks index :=
        List.take_of_length_le (by omega)
      have isNextBlock := system.stateAtBoundary_of_inside_blocks states blocks events
        blockExec boundary startsAtZero nextBoundary isInfinite run isInsideBlocks (index + 1)
      have isEnd : boundary index + offset + 1 = boundary (index + 1) := by
        rw [nextBoundary]
        omega
      rw [isWhole, blockExec index, isEnd, isNextBlock]
      exact ⟨events index, rfl⟩
  obtain ⟨nextEvents, isNext⟩ := isNextState
  rw [isTakeSucc] at isNext
  obtain ⟨firstEvents, middle, lastEvents, isFirst, isLastStep, _⟩ :=
    system.exec_append_inv _ _ _ _ _ isNext
  rw [isTaken] at isFirst
  simp only [Option.some.injEq, Prod.mk.injEq] at isFirst
  obtain ⟨_, rfl⟩ := isFirst
  obtain ⟨stepEvents, next, laterEvents, isStep, isLater, _⟩ :=
    system.exec_cons _ _ _ _ _ isLastStep
  simp only [exec, Option.some.injEq, Prod.mk.injEq] at isLater
  exact ⟨stepEvents, by rw [isStep, isLater.2]⟩

/-- A run in blocks with infinitely many non-empty blocks flattens into an
infinite run. The boundaries are the lengths of the concatenated blocks. -/
theorem exists_flattened_run (states : Nat → State) (blocks : Nat → List Label)
    (events : Nat → List Event) (startsAtInit : states 0 = system.init)
    (blockExec : ∀ index,
      system.exec (states index) (blocks index) = some (events index, states (index + 1)))
    (isInfinite : ∀ bound, ∃ index, bound ≤ index ∧ blocks index ≠ []) :
    ∃ (run : InfiniteRun State Label) (boundary : Nat → Nat),
      system.IsRun run ∧
      boundary 0 = 0 ∧
      (∀ index, boundary (index + 1) = boundary index + (blocks index).length) ∧
      (∀ index, run.states (boundary index) = states index) ∧
      (∀ index offset label, (blocks index)[offset]? = some label →
        run.labels (boundary index + offset) = label) := by
  have startsAtZero : (concatUpTo blocks 0).length = 0 := rfl
  have nextBoundary : ∀ index, (concatUpTo blocks (index + 1)).length =
      (concatUpTo blocks index).length + (blocks index).length :=
    fun index => List.length_append
  obtain ⟨run, isInsideBlocks⟩ := system.exists_run_inside_blocks states blocks events blockExec
    (fun index => (concatUpTo blocks index).length) startsAtZero nextBoundary isInfinite
  have stateAtBoundary := system.stateAtBoundary_of_inside_blocks states blocks events
    blockExec _ startsAtZero nextBoundary isInfinite run isInsideBlocks
  refine ⟨run, fun index => (concatUpTo blocks index).length, ⟨?_, ?_⟩, startsAtZero,
    nextBoundary, stateAtBoundary, ?_⟩
  · rw [← startsAtInit, ← stateAtBoundary 0]
    rfl
  · exact system.stepsFrom_of_inside_blocks states blocks events blockExec _ startsAtZero
      nextBoundary isInfinite run isInsideBlocks
  · intro index offset label isElement
    have isInside : offset < (blocks index).length := (List.getElem?_eq_some_iff.mp isElement).1
    have isLabel := (isInsideBlocks index offset isInside).1
    rw [isElement] at isLabel
    exact (Option.some.inj isLabel).symm

end TransitionSystem

end DexExpressiveness
