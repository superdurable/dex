/-!
# Finite maps and sorting used by both models

The Dex and L models keep every finite map as an association list, so that
every model function is structurally recursive and evaluates inside the Lean
kernel. This is what lets the example files check concrete runs by `decide`.

No definition here comes from the semantics specification; they are the
plumbing for its `M[k ↦ v]` notation (spec §0, Notation).
-/

namespace DexExpressiveness

/-- A finite map as an association list. The first binding of a key wins. -/
abbrev AssocList (κ ν : Type) := List (κ × ν)

namespace AssocList

variable {κ ν : Type} [DecidableEq κ]

/-- `M[k]`, or `none` when `k` is absent (spec §0: `⊥` means absent). -/
def get? : AssocList κ ν → κ → Option ν
  | [], _ => none
  | (key, value) :: rest, wanted => if key = wanted then some value else get? rest wanted

/-- `M[k ↦ v]` (spec §0). An existing binding is replaced in place, so the
list order stays the insertion order; a new key is appended. -/
def set : AssocList κ ν → κ → ν → AssocList κ ν
  | [], wanted, newValue => [(wanted, newValue)]
  | (key, value) :: rest, wanted, newValue =>
    if key = wanted then (key, newValue) :: rest else (key, value) :: set rest wanted newValue

/-- `M` without `k`. -/
def erase : AssocList κ ν → κ → AssocList κ ν
  | [], _ => []
  | (key, value) :: rest, wanted =>
    if key = wanted then erase rest wanted else (key, value) :: erase rest wanted

/-- Whether `k ∈ dom(M)`. -/
def contains (map : AssocList κ ν) (wanted : κ) : Bool :=
  (get? map wanted).isSome

end AssocList

/-- Insert `newElement` into a list sorted by the total preorder `isAtMost`,
before the first element that is not strictly before it. -/
def insertSorted {α : Type} (isAtMost : α → α → Bool) (newElement : α) : List α → List α
  | [] => [newElement]
  | head :: rest =>
    if isAtMost newElement head then newElement :: head :: rest
    else head :: insertSorted isAtMost newElement rest

/-- Stable insertion sort by the total preorder `isAtMost`: elements that
compare equal keep their original order. -/
def sortStable {α : Type} (isAtMost : α → α → Bool) : List α → List α
  | [] => []
  | head :: rest => insertSorted isAtMost head (sortStable isAtMost rest)

/-- Remove the element at `index`; the list is unchanged when `index` is out of range. -/
def removeAt {α : Type} : List α → Nat → List α
  | [], _ => []
  | _ :: rest, 0 => rest
  | head :: rest, index + 1 => head :: removeAt rest index

end DexExpressiveness
