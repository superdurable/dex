import DexExpressiveness.Failure.Statements.Leaf
import DexExpressiveness.Theorems

/-!
# WP-F7: helper lemmas of the end-to-end corollary (spec §11.5)

`endToEnd_safety` and `endToEnd_liveness` (`Failure/Statements/Main.lean`)
compose the failure theorems of WP-F5 and WP-F6 with the expressiveness
theorem (`Theorems.lean`). This file cannot import `Main.lean`, so each
composition lemma takes the conclusion of a WP-F5 or WP-F6 statement as a
hypothesis, specialized to `Program.ofFlow ⟦program⟧` and `environment`.
`Main.lean` applies the lemmas to those statements.

The file has three parts:

- the translated program satisfies H2, H3, and SYNC;
- generic facts: repetition projects to `out(x)` repetition, every chunk
  family has a concatenation, and filtering a concatenation does not depend
  on how it is chunked;
- the two composition lemmas.
-/

namespace DexExpressiveness

/-! ## Repetition projects to `out(x)` repetition -/

/-- A Dex repetition projects to an L repetition: an inserted `fx(f, src, x)`
projects to an inserted `out(x)` that equals the projection of the earlier
kept `fx`. -/
theorem wpf7_isRepetitionAfter_project {kept trace original : List (Dex.Obs Translation.signature)}
    (isRepetition : IsRepetitionAfter Dex.Obs.IsFx kept trace original) :
    IsRepetitionAfter L.Event.IsOut (kept.filterMap Translation.project)
      (trace.filterMap Translation.project) (original.filterMap Translation.project) := by
  induction isRepetition with
  | done kept => exact .done _
  | keep kept event trace original _ inductionHypothesis =>
    rw [List.filterMap_append] at inductionHypothesis
    cases isProjected : Translation.project event with
    | none =>
      simp only [List.filterMap_cons, isProjected]
      simpa [isProjected] using inductionHypothesis
    | some projected =>
      simp only [List.filterMap_cons, isProjected]
      refine .keep _ projected _ _ ?_
      simpa [isProjected] using inductionHypothesis
  | insert kept event trace original isFx isKept _ inductionHypothesis =>
    cases event with
    | fx step method effect =>
      simp only [List.filterMap_cons, Translation.project]
      refine .insert _ (.out effect) _ _ trivial ?_ inductionHypothesis
      exact List.mem_filterMap.mpr ⟨_, isKept, rfl⟩
    | _ => exact isFx.elim

/-- A Dex repetition projects to an `out(x)` repetition. -/
theorem wpf7_isOutRepetition_of_isRepetition {trace original : List (Dex.Obs Translation.signature)}
    (isRepetition : IsRepetition trace original) :
    IsOutRepetition (trace.filterMap Translation.project)
      (original.filterMap Translation.project) :=
  wpf7_isRepetitionAfter_project isRepetition

/-! ## Concatenations -/

/-- Every chunk family has a concatenation. -/
theorem wpf7_exists_isConcatenation {α : Type} (chunks : Nat → List α) :
    ∃ sequence : Sequence α, IsConcatenation chunks sequence := by
  classical
  refine ⟨fun position =>
    if isFound : ∃ count, position < (concatUpTo chunks count).length then
      (concatUpTo chunks (Classical.choose isFound))[position]?
    else none, fun position => ?_⟩
  by_cases isFound : ∃ count, position < (concatUpTo chunks count).length
  · refine Or.inl ⟨Classical.choose isFound, Classical.choose_spec isFound, ?_⟩
    simp only [isFound, ↓reduceDIte]
  · refine Or.inr ⟨fun count => ?_, ?_⟩
    · exact Nat.le_of_not_lt (fun isInside => isFound ⟨count, isInside⟩)
    · simp only [isFound, ↓reduceDIte]

/-- Two chunk families with the same concatenation: every finite
concatenation of the first extends to a finite concatenation of the second. -/
theorem wpf7_concatUpTo_extends {α : Type} {first second : Nat → List α} {sequence : Sequence α}
    (isFirst : IsConcatenation first sequence) (isSecond : IsConcatenation second sequence)
    (count : Nat) :
    ∃ otherCount rest, concatUpTo second otherCount = concatUpTo first count ++ rest := by
  have isCovered : ∃ otherCount,
      (concatUpTo first count).length ≤ (concatUpTo second otherCount).length := by
    cases isEmpty : (concatUpTo first count).length with
    | zero => exact ⟨0, Nat.zero_le _⟩
    | succ lastPosition =>
      have isInside : lastPosition < (concatUpTo first count).length := by omega
      have isElement := isFirst.getElem?_concatUpTo count lastPosition isInside
      rw [List.getElem?_eq_getElem isInside] at isElement
      rcases isSecond lastPosition with ⟨otherCount, isInsideOther, _⟩ | ⟨_, isMissing⟩
      · exact ⟨otherCount, isInsideOther⟩
      · rw [isMissing] at isElement
        cases isElement
  obtain ⟨otherCount, isLonger⟩ := isCovered
  refine ⟨otherCount, (concatUpTo second otherCount).drop (concatUpTo first count).length, ?_⟩
  have isTake : (concatUpTo second otherCount).take (concatUpTo first count).length =
      concatUpTo first count := by
    apply List.ext_getElem?
    intro position
    by_cases isInside : position < (concatUpTo first count).length
    · rw [List.getElem?_take_of_lt isInside,
        ← isSecond.getElem?_concatUpTo otherCount position (by omega),
        isFirst.getElem?_concatUpTo count position isInside]
    · rw [List.getElem?_eq_none (Nat.le_of_not_lt isInside), List.getElem?_eq_none]
      rw [List.length_take]
      omega
  conv => lhs; rw [← List.take_append_drop (concatUpTo first count).length
    (concatUpTo second otherCount)]
  rw [isTake]

/-- Filtering a concatenation does not depend on how it is chunked: two chunk
families with the same concatenation have the same filtered
concatenation. -/
theorem wpf7_isConcatenation_filterMap_rechunk {α β : Type} (view : α → Option β)
    {first second : Nat → List α} {sequence : Sequence α} {filtered : Sequence β}
    (isFirst : IsConcatenation first sequence) (isSecond : IsConcatenation second sequence)
    (isFiltered : IsConcatenation (fun index => (first index).filterMap view) filtered) :
    IsConcatenation (fun index => (second index).filterMap view) filtered := by
  intro position
  rcases isFiltered position with ⟨count, isInside, isElement⟩ | ⟨isOutside, isMissing⟩
  · obtain ⟨otherCount, rest, isExtended⟩ := wpf7_concatUpTo_extends isFirst isSecond count
    rw [concatUpTo_filterMap] at isInside isElement
    refine Or.inl ⟨otherCount, ?_, ?_⟩
    · rw [concatUpTo_filterMap, isExtended, List.filterMap_append, List.length_append]
      omega
    · rw [concatUpTo_filterMap, isExtended, List.filterMap_append,
        List.getElem?_append_left isInside]
      exact isElement
  · refine Or.inr ⟨fun otherCount => ?_, isMissing⟩
    obtain ⟨count, rest, isExtended⟩ := wpf7_concatUpTo_extends isSecond isFirst otherCount
    have isShort := isOutside count
    rw [concatUpTo_filterMap, isExtended, List.filterMap_append, List.length_append] at isShort
    rw [concatUpTo_filterMap]
    omega

/-- Filtering with `some` keeps every element. -/
theorem wpf7_filterMap_some {α : Type} (elements : List α) :
    elements.filterMap some = elements := by
  induction elements with
  | nil => rfl
  | cons element elements inductionHypothesis => simp [inductionHypothesis]

end DexExpressiveness

namespace DexExpressiveness.DexF

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## The translated program satisfies H2, H3, and SYNC -/

/-- The failure-free Flow of a translated program is the translation. -/
theorem wpf7_failureFreeFlow_ofFlow {σ : Dex.Signature} (flow : Dex.FlowDef σ) :
    (Program.ofFlow flow).failureFreeFlow = flow := rfl

/-- H2 holds for every program (`HasNoFailurePolicy` is always true). -/
theorem wpf7_hasNoFailurePolicy_ofFlow {σ : Dex.Signature} (flow : Dex.FlowDef σ) :
    HasNoFailurePolicy (Program.ofFlow flow) := trivial

/-- SYNC holds for every program (`IsSyncOnly` is always true). -/
theorem wpf7_isSyncOnly_ofFlow {σ : Dex.Signature} (flow : Dex.FlowDef σ) :
    IsSyncOnly (Program.ofFlow flow) := trivial

/-- H3: a failure-free Flow definition ignores the attempt context. -/
theorem wpf7_ignoresAttemptContext_ofFlow {σ : Dex.Signature} (flow : Dex.FlowDef σ) :
    IgnoresAttemptContext (Program.ofFlow flow) := fun _ _ => rfl

/-! ## The end-to-end corollary -/

/-- Projecting an event with its ghost index erased: the chunk of
`IsRepeatErasure`, filtered by `π`, is the chunk of `IsErasedProjection`. -/
theorem wpf7_toList_filterMap_project (event : Option (Obs signature)) :
    ((event.map Obs.toDex).toList).filterMap project =
      (event.bind (fun element => project element.toDex)).toList := by
  cases event with
  | none => rfl
  | some element =>
    simp only [Option.map_some, Option.toList_some, Option.bind_some, List.filterMap_cons,
      List.filterMap_nil]
    cases project element.toDex <;> rfl

/-- `endToEnd_safety` from the Lemma (no failures) and the repetition form
of safety, each specialized to the translated program. -/
theorem wpf7_endToEnd_safety_of (program : L.Program) (isWellFormed : program.IsWellFormed)
    (noFailures : ∀ requests trace,
      IsFreeFiniteTrace (Program.ofFlow ⟦program⟧).failureFreeFlow environment requests trace →
      ∃ failureRequests failureTrace,
        IsFailureFiniteTrace (Program.ofFlow ⟦program⟧) environment failureRequests
          failureTrace ∧
        eraseRetries failureRequests = requests ∧ failureTrace.map Obs.toDex = trace)
    (repetition : ∀ requests trace,
      IsFailureFiniteTrace (Program.ofFlow ⟦program⟧) environment requests trace →
      ∃ freeTrace,
        IsFreeFiniteTrace (Program.ofFlow ⟦program⟧).failureFreeFlow environment
          (eraseRetries requests) freeTrace ∧
        IsRepetition (trace.map Obs.toDex) freeTrace) :
    (∀ lTrace, IsLFiniteTrace program lTrace →
      ∃ requests failureTrace,
        IsFailureFiniteTrace (Program.ofFlow ⟦program⟧) environment requests failureTrace ∧
        failureTrace.filterMap (fun event => project event.toDex) = lTrace) ∧
    (∀ requests failureTrace,
      IsFailureFiniteTrace (Program.ofFlow ⟦program⟧) environment requests failureTrace →
      ∃ lTrace, IsLFiniteTrace program lTrace ∧
        IsOutRepetition (failureTrace.filterMap (fun event => project event.toDex)) lTrace) := by
  constructor
  · intro lTrace isLTrace
    obtain ⟨labels, events, final, isExec, isAdmissible, isTrace⟩ :=
      (safety_of_isWellFormed program isWellFormed lTrace).mpr isLTrace
    obtain ⟨failureRequests, failureTrace, isFailureTrace, _, isErased⟩ :=
      noFailures _ events ⟨labels, final, isExec, rfl, isAdmissible⟩
    refine ⟨failureRequests, failureTrace, isFailureTrace, ?_⟩
    rw [isTrace, ← isErased, List.filterMap_map]
    rfl
  · intro requests failureTrace isFailureTrace
    obtain ⟨freeTrace, ⟨labels, final, isExec, isRequests, isAdmissible⟩, isRepetition⟩ :=
      repetition requests failureTrace isFailureTrace
    refine ⟨freeTrace.filterMap project,
      (safety_of_isWellFormed program isWellFormed _).mp
        ⟨labels, freeTrace, final, isExec, isRequests ▸ isAdmissible, rfl⟩, ?_⟩
    have isOutRepetition := wpf7_isOutRepetition_of_isRepetition isRepetition
    rw [List.filterMap_map] at isOutRepetition
    exact isOutRepetition

/-- `endToEnd_liveness` from the liveness theorem for DexF, specialized to
the translated program, the word, and the marker, for every Dex trace. -/
theorem wpf7_endToEnd_liveness_of (program : L.Program) (isWellFormed : program.IsWellFormed)
    (lWord : Sequence L.Request) (isAdmissible : L.IsAdmissibleWord lWord)
    (word : Sequence (Request signature)) (trace : Sequence L.Event) (marker : Marker)
    (liveness : ∀ dexTrace : Sequence (Dex.Obs signature),
      ((∃ failureTrace,
          IsFailureFairMaximalTrace (Program.ofFlow ⟦program⟧) word failureTrace marker ∧
          IsRepeatErasure failureTrace dexTrace) →
        (Dex.system (Program.ofFlow ⟦program⟧).failureFreeFlow).IsFairMaximalTrace Dex.IsFair
          some (lWord.map encodeRequest) dexTrace marker) ∧
      (HasNoRetry word →
        (Dex.system (Program.ofFlow ⟦program⟧).failureFreeFlow).IsFairMaximalTrace Dex.IsFair
          some (lWord.map encodeRequest) dexTrace marker →
        ∃ failureTrace,
          IsFailureFairMaximalTrace (Program.ofFlow ⟦program⟧) word failureTrace marker ∧
          IsRepeatErasure failureTrace dexTrace)) :
    ((∃ failureTrace,
        IsFailureFairMaximalTrace (Program.ofFlow ⟦program⟧) word failureTrace marker ∧
        IsErasedProjection project failureTrace trace) →
      IsLFairMaximalTrace program lWord trace marker) ∧
    (HasNoRetry word → IsLFairMaximalTrace program lWord trace marker →
      ∃ failureTrace,
        IsFailureFairMaximalTrace (Program.ofFlow ⟦program⟧) word failureTrace marker ∧
        IsErasedProjection project failureTrace trace) := by
  classical
  constructor
  · rintro ⟨failureTrace, isFailureTrace, projectedChunks, isProjected, isProjectedChunk⟩
    let erasedChunks : Nat → List (Dex.Obs signature) := fun position =>
      if IsRepeatedAt failureTrace position then []
      else ((failureTrace position).map Obs.toDex).toList
    obtain ⟨dexTrace, isDexTrace⟩ := wpf7_exists_isConcatenation erasedChunks
    have isErasure : IsRepeatErasure failureTrace dexTrace :=
      ⟨erasedChunks, isDexTrace, fun position =>
        ⟨fun isRepeated => by simp [erasedChunks, isRepeated],
          fun isNotRepeated => by simp [erasedChunks, isNotRepeated]⟩⟩
    obtain ⟨run, isRun, isRunFor, requestsArrive, isFair, isEvents, hasMarker⟩ :=
      (liveness dexTrace).1 ⟨failureTrace, isFailureTrace, isErasure⟩
    have isProjectedChunks :
        projectedChunks = fun position => (erasedChunks position).filterMap project := by
      funext position
      by_cases isRepeated : IsRepeatedAt failureTrace position
      · rw [(isProjectedChunk position).1 isRepeated]
        simp [erasedChunks, isRepeated]
      · rw [(isProjectedChunk position).2 isRepeated]
        simp only [erasedChunks, isRepeated, ↓reduceIte]
        exact (wpf7_toList_filterMap_project (failureTrace position)).symm
    rw [isProjectedChunks] at isProjected
    have isProjectedEvents := wpf7_isConcatenation_filterMap_rechunk project isDexTrace isEvents
      isProjected
    simp only [wpf7_filterMap_some] at isProjectedEvents
    exact (liveness_of_isWellFormed program isWellFormed lWord isAdmissible trace marker).mp
      ⟨run, isRun, isRunFor, requestsArrive, isFair, isProjectedEvents, hasMarker⟩
  · intro hasNoRetry isLTrace
    obtain ⟨run, isRun, isRunFor, requestsArrive, isFair, isEvents, hasMarker⟩ :=
      (liveness_of_isWellFormed program isWellFormed lWord isAdmissible trace marker).mpr isLTrace
    obtain ⟨dexTrace, isDexTrace⟩ :=
      wpf7_exists_isConcatenation (fun index => (Dex.system ⟦program⟧).eventsAt run index)
    have isDexEvents : IsConcatenation
        (fun index => ((Dex.system ⟦program⟧).eventsAt run index).filterMap some) dexTrace := by
      simpa only [wpf7_filterMap_some] using isDexTrace
    obtain ⟨failureTrace, isFailureTrace, erasedChunks, isErased, isErasedChunk⟩ :=
      (liveness dexTrace).2 hasNoRetry
        ⟨run, isRun, isRunFor, requestsArrive, isFair, isDexEvents, hasMarker⟩
    refine ⟨failureTrace, isFailureTrace, fun position => (erasedChunks position).filterMap project,
      wpf7_isConcatenation_filterMap_rechunk project isDexTrace isErased isEvents,
      fun position => ⟨fun isRepeated => ?_, fun isNotRepeated => ?_⟩⟩
    · simp only [(isErasedChunk position).1 isRepeated, List.filterMap_nil]
    · simp only [(isErasedChunk position).2 isNotRepeated]
      exact wpf7_toList_filterMap_project (failureTrace position)

end DexExpressiveness.DexF
