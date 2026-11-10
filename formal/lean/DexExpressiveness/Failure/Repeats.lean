import DexExpressiveness.Failure.Rules
import DexExpressiveness.L.Syntax

/-!
# Repeated external effects (spec §11.4, §11.5)

Two notions of "repeated", as spec §11.4 keeps them apart:

- **With the ghost index.** "An event `fx(f, src, x)ʲ` of a trace is
  **repeated** when an earlier event of the same trace has the same source
  `src` and the same index `j`." `erase(t)` removes every repeated `fx`
  (spec §11.5, "Erasure"): `eraseRepeats` for finite traces,
  `IsRepeatErasure` for finite or infinite ones.
- **With real labels.** "A trace `t` is a **repetition** of a trace `u` when
  `t` is `u` with events inserted, and each inserted event equals, in source
  and label, an event of `u` that comes before it in `t`." `IsRepetition`
  is this relation on Dex traces. Only `fx` events may be inserted, and each
  must equal an earlier event that is not itself inserted, so the relation
  admits no arbitrary extra event. `IsOutRepetition` is the L form of the
  end-to-end corollary: "an L trace of `S` with inserted `out(x)` events,
  each equal in value to an earlier `out(x)`".

The definitions of this file are fixed for Stage 2.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## Erasure with the ghost index -/

/-- Whether `event` is a repeated `fx` after the events `earlier`: an earlier
`fx` has the same Step execution, method, and index. -/
def Obs.isRepeatAfter (earlier : List (Obs σ)) : Obs σ → Bool
  | .fx step method _ index =>
    earlier.any (fun previous =>
      match previous with
      | .fx previousStep previousMethod _ previousIndex =>
        decide (previousStep = step) && decide (previousMethod = method) &&
          decide (previousIndex = index)
      | _ => false)
  | _ => false

/-- `erase` after the events `earlier`: drop each repeated `fx`, and erase
the ghost index of every other event. -/
def eraseRepeatsAfter : List (Obs σ) → List (Obs σ) → List (Dex.Obs σ)
  | _, [] => []
  | earlier, event :: laterEvents =>
    (if event.isRepeatAfter earlier then [] else [event.toDex]) ++
      eraseRepeatsAfter (earlier ++ [event]) laterEvents

/-- `erase(t)` of a finite DexF trace (spec §11.5): "removes every repeated
`fx` (the ghost form of Section 11.4)". -/
def eraseRepeats (trace : List (Obs σ)) : List (Dex.Obs σ) :=
  eraseRepeatsAfter [] trace

/-- The event at `position` of a finite or infinite DexF trace is a repeated
`fx`: an earlier event has the same Step execution, method, and index. -/
def IsRepeatedAt (trace : Sequence (Obs σ)) (position : Nat) : Prop :=
  ∃ earlier step method firstEffect effect index, earlier < position ∧
    trace earlier = some (.fx step method firstEffect index) ∧
    trace position = some (.fx step method effect index)

/-- `erased` is `erase(trace)` (spec §11.5), for finite or infinite traces:
the concatenation, position by position, of nothing for a repeated `fx` and
of the event with its ghost index erased otherwise. -/
def IsRepeatErasure (trace : Sequence (Obs σ)) (erased : Sequence (Dex.Obs σ)) : Prop :=
  ∃ chunks : Nat → List (Dex.Obs σ), IsConcatenation chunks erased ∧
    ∀ position,
      (IsRepeatedAt trace position → chunks position = []) ∧
      (¬ IsRepeatedAt trace position →
        chunks position = ((trace position).map Obs.toDex).toList)

/-- `projected` is `π(erase(trace))` for a projection `view` with `ε` erased:
the repeated `fx` are removed, then every other event is projected. -/
def IsErasedProjection {Visible : Type} (view : Dex.Obs σ → Option Visible)
    (trace : Sequence (Obs σ)) (projected : Sequence Visible) : Prop :=
  ∃ chunks : Nat → List Visible, IsConcatenation chunks projected ∧
    ∀ position,
      (IsRepeatedAt trace position → chunks position = []) ∧
      (¬ IsRepeatedAt trace position →
        chunks position = ((trace position).bind (fun event => view event.toDex)).toList)

end DexExpressiveness.DexF

namespace DexExpressiveness

/-! ## Repetition with real labels -/

/-- `trace` is `kept`'s continuation `original` with insertions: reading
`trace` from the left, each event either is the next event of `original`
(it is kept and joins `kept`), or is repeatable and equal to an event already
kept. `kept` is the part of the original trace read so far. -/
inductive IsRepetitionAfter {α : Type} (IsRepeatable : α → Prop) :
    List α → List α → List α → Prop
  /-- Both traces are done. -/
  | done (kept : List α) : IsRepetitionAfter IsRepeatable kept [] []
  /-- The next event is the next event of the original trace. -/
  | keep (kept : List α) (event : α) (trace original : List α) :
      IsRepetitionAfter IsRepeatable (kept ++ [event]) trace original →
      IsRepetitionAfter IsRepeatable kept (event :: trace) (event :: original)
  /-- The next event is inserted: it is repeatable and equals an event kept
  before it. -/
  | insert (kept : List α) (event : α) (trace original : List α) :
      IsRepeatable event → event ∈ kept →
      IsRepetitionAfter IsRepeatable kept trace original →
      IsRepetitionAfter IsRepeatable kept (event :: trace) original

/-- A repetition stays one under a weaker condition on inserted events. -/
theorem IsRepetitionAfter.mono {α : Type} {IsRepeatable IsAllowed : α → Prop}
    (weaker : ∀ event, IsRepeatable event → IsAllowed event) {kept trace original : List α}
    (isRepetition : IsRepetitionAfter IsRepeatable kept trace original) :
    IsRepetitionAfter IsAllowed kept trace original := by
  induction isRepetition with
  | done kept => exact .done kept
  | keep kept event trace original _ inductionHypothesis =>
    exact .keep kept event trace original inductionHypothesis
  | insert kept event trace original isEventRepeatable isKept _ inductionHypothesis =>
    exact .insert kept event trace original (weaker event isEventRepeatable) isKept
      inductionHypothesis

/-- Whether a Dex event is an external effect `fx`. -/
def Dex.Obs.IsFx {σ : Dex.Signature} : Dex.Obs σ → Prop
  | .fx _ _ _ => True
  | _ => False

/-- `trace` is a **repetition** of `original` (spec §11.4, real labels):
`original` with `fx` events inserted, each equal in source and label to an
event of `original` that comes before it. -/
def IsRepetition {σ : Dex.Signature} (trace original : List (Dex.Obs σ)) : Prop :=
  IsRepetitionAfter Dex.Obs.IsFx [] trace original

/-- Whether an L event is an output `out(x)`. -/
def L.Event.IsOut : L.Event → Prop
  | .out _ => True
  | _ => False

/-- The L form of repetition for the end-to-end corollary (spec §11.5):
`original` with inserted `out(x)` events, "each equal in value to an earlier
`out(x)`". -/
def IsOutRepetition (trace original : List L.Event) : Prop :=
  IsRepetitionAfter L.Event.IsOut [] trace original

/-- A Boolean check of `IsRepetitionAfter` that keeps an event whenever it
matches the next event of the original trace. -/
def isRepetitionAfter {α : Type} [DecidableEq α] (isRepeatable : α → Bool) :
    List α → List α → List α → Bool
  | _, [], [] => true
  | _, [], _ :: _ => false
  | kept, event :: trace, [] =>
    isRepeatable event && kept.contains event && isRepetitionAfter isRepeatable kept trace []
  | kept, event :: trace, next :: original =>
    if event = next then isRepetitionAfter isRepeatable (kept ++ [event]) trace original
    else
      isRepeatable event && kept.contains event &&
        isRepetitionAfter isRepeatable kept trace (next :: original)

/-- The Boolean check is sound: a pass is a repetition. -/
theorem isRepetitionAfter_sound {α : Type} [DecidableEq α] (isRepeatable : α → Bool)
    (kept trace original : List α)
    (passes : isRepetitionAfter isRepeatable kept trace original = true) :
    IsRepetitionAfter (fun event => isRepeatable event = true) kept trace original := by
  induction trace generalizing kept original with
  | nil =>
    cases original with
    | nil => exact .done kept
    | cons next original => simp [isRepetitionAfter] at passes
  | cons event trace inductionHypothesis =>
    cases original with
    | nil =>
      simp only [isRepetitionAfter, Bool.and_eq_true, List.contains_iff_mem] at passes
      exact .insert kept event trace [] passes.1.1 passes.1.2
        (inductionHypothesis kept [] passes.2)
    | cons next original =>
      simp only [isRepetitionAfter] at passes
      split at passes
      · rename_i isNext
        subst isNext
        exact .keep kept event trace original (inductionHypothesis _ _ passes)
      · simp only [Bool.and_eq_true, List.contains_iff_mem] at passes
        exact .insert kept event trace (next :: original) passes.1.1 passes.1.2
          (inductionHypothesis kept _ passes.2)

end DexExpressiveness
