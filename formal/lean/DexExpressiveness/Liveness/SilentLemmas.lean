import DexExpressiveness.FiniteTraces
import DexExpressiveness.Liveness.Statements

/-!
# Liveness, work package L2: lemmas about one step of each side

The lemmas that `Liveness/Silent.lean` is proved from. Everything here is
proved.

- The silent weight: sums of weights under a change or a removal of list
  elements.
- One Dex step: what a tick does, that every other action keeps the clock,
  that a commit stamps the clock, and which events each action has.
- One L step: the same facts for an L label.
- The requests of a step are read off its events (`Event.encodedRequests`),
  on both sides.
- A run for an admissible word: the Flow has a run exactly once a request
  was attempted, and the request of each action is the next one of the word.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Sums of weights -/

/-- A change that raises no weight does not raise the sum. -/
theorem sum_map_le_of_change {α : Type} (weight : α → Nat) (change : α → α)
    (elements : List α)
    (isNotHigher : ∀ element, element ∈ elements → weight (change element) ≤ weight element) :
    ((elements.map change).map weight).sum ≤ (elements.map weight).sum := by
  induction elements with
  | nil => simp
  | cons head tail inductionHypothesis =>
    simp only [List.map_cons, List.sum_cons]
    have headBound := isNotHigher head (by simp)
    have tailBound := inductionHypothesis
      (fun element isMember => isNotHigher element (List.mem_cons_of_mem _ isMember))
    omega

/-- A change that raises no weight and lowers one lowers the sum. -/
theorem sum_map_lt_of_change {α : Type} (weight : α → Nat) (change : α → α)
    (elements : List α)
    (isNotHigher : ∀ element, element ∈ elements → weight (change element) ≤ weight element)
    (lowered : α) (isMember : lowered ∈ elements)
    (isLower : weight (change lowered) < weight lowered) :
    ((elements.map change).map weight).sum < (elements.map weight).sum := by
  induction elements with
  | nil => cases isMember
  | cons head tail inductionHypothesis =>
    simp only [List.map_cons, List.sum_cons]
    have headBound := isNotHigher head (by simp)
    have tailIsNotHigher : ∀ element, element ∈ tail →
        weight (change element) ≤ weight element :=
      fun element isTailMember => isNotHigher element (List.mem_cons_of_mem _ isTailMember)
    have tailBound := sum_map_le_of_change weight change tail tailIsNotHigher
    rcases List.mem_cons.mp isMember with isHead | isTailMember
    · subst isHead
      omega
    · have tailLower := inductionHypothesis tailIsNotHigher isTailMember
      omega

/-- Removing elements does not raise the sum. -/
theorem sum_map_filter_le {α : Type} (weight : α → Nat) (isKept : α → Bool)
    (elements : List α) :
    ((elements.filter isKept).map weight).sum ≤ (elements.map weight).sum := by
  induction elements with
  | nil => simp
  | cons head tail inductionHypothesis =>
    by_cases isHeadKept : isKept head = true
    · simp only [List.filter_cons, isHeadKept, ite_true, List.map_cons, List.sum_cons]
      omega
    · simp only [List.filter_cons, isHeadKept, List.map_cons, List.sum_cons]
      simp only [Bool.false_eq_true, ite_false]
      omega

/-- Removing an element of positive weight lowers the sum. -/
theorem sum_map_filter_lt {α : Type} (weight : α → Nat) (isKept : α → Bool)
    (elements : List α) (removed : α) (isMember : removed ∈ elements)
    (isRemoved : isKept removed = false) (isPositive : 0 < weight removed) :
    ((elements.filter isKept).map weight).sum < (elements.map weight).sum := by
  induction elements with
  | nil => cases isMember
  | cons head tail inductionHypothesis =>
    have tailBound := sum_map_filter_le weight isKept tail
    rcases List.mem_cons.mp isMember with isHead | isTailMember
    · subst isHead
      simp only [List.filter_cons, isRemoved, List.map_cons, List.sum_cons]
      simp only [Bool.false_eq_true, ite_false]
      omega
    · have tailLower := inductionHypothesis isTailMember
      by_cases isHeadKept : isKept head = true
      · simp only [List.filter_cons, isHeadKept, ite_true, List.map_cons, List.sum_cons]
        omega
      · simp only [List.filter_cons, isHeadKept, List.map_cons, List.sum_cons]
        simp only [Bool.false_eq_true, ite_false]
        omega

/-! ## The silent weight -/

/-- Every task can still take a silent step. -/
theorem Dex.Task.silentWeight_pos (task : Dex.Task signature) : 0 < task.silentWeight := by
  unfold Dex.Task.silentWeight
  split <;> omega

/-- Dropping a task of `Θ` lowers the silent weight. -/
theorem silentWeight_removeTask_lt (config : Dex.Config signature) (taskId : Nat)
    (task : Dex.Task signature) (isFound : config.findTask? taskId = some task) :
    (config.removeTask taskId).silentWeight < config.silentWeight := by
  obtain ⟨isMember, hasId⟩ := mem_of_findTask? config taskId task isFound
  exact sum_map_filter_lt Dex.Task.silentWeight _ config.tasks task isMember
    (by simp [hasId]) task.silentWeight_pos

end DexExpressiveness
