import DexExpressiveness.Failure.Rules

/-!
# WP-F1: list and sorting lemmas

Generic facts about `insertSorted`, `sortStable`, `filter`, and `take`, and
the due-timer order of DexF against the failure-free order.
-/

namespace DexExpressiveness.DexF.WPF1

variable {σ : Dex.Signature}

/-- Sorting a mapped list is mapping the sorted list, when the comparisons
agree on the list's elements. -/
theorem sortStable_map {α β : Type} (isAtMost : β → β → Bool) (isAtMost' : α → α → Bool)
    (image : α → β) (elements : List α)
    (agrees : ∀ first, first ∈ elements → ∀ second, second ∈ elements →
      isAtMost (image first) (image second) = isAtMost' first second) :
    sortStable isAtMost (elements.map image) = (sortStable isAtMost' elements).map image := by
  sorry

/-- `sortStable` by a natural-number key returns a list sorted by that key. -/
theorem sortStable_pairwise_key {α : Type} (key : α → Nat) (elements : List α) :
    (sortStable (fun first second => decide (key first ≤ key second)) elements).Pairwise
      (fun first second => key first ≤ key second) := by
  sorry

/-- Two lists strictly sorted by a natural-number key with the same members
are equal. -/
theorem eq_of_pairwise_lt_of_mem_iff {α : Type} (key : α → Nat) (first second : List α)
    (firstSorted : first.Pairwise (fun a b => key a < key b))
    (secondSorted : second.Pairwise (fun a b => key a < key b))
    (sameMembers : ∀ element, element ∈ first ↔ element ∈ second) : first = second := by
  sorry

/-- The filter of a prefix is a prefix of the filter. -/
theorem filter_take {α : Type} (isKept : α → Bool) (elements : List α) (count : Nat) :
    (elements.take count).filter isKept =
      (elements.filter isKept).take ((elements.take count).filter isKept).length := by
  sorry

/-- The wait timer of a due timer, if it is one. -/
def waitTimerOf? : DueTimer σ → Option (Dex.TimerId σ)
  | .wait timer => some timer
  | .retry _ => none

/-- The wait timers of the mixed sort, in order, are the failure-free sort of
the wait timers. It holds for any `σ.isTimerIdAtMost`: the retries are
inserted first, and a wait passes a retry only when it fires strictly later. -/
theorem sortStable_dueEntries_waits (waits : List (Dex.TimerId σ × Nat))
    (retries : List (RetryTimerId σ × Nat)) :
    (sortStable isDueEntryAtMost
        (waits.map (fun entry => (DueTimer.wait entry.1, entry.2)) ++
          retries.map (fun entry => (DueTimer.retry entry.1, entry.2)))).filterMap
      (fun entry => (waitTimerOf? entry.1).map (fun timer => (timer, entry.2))) =
      sortStable Dex.Run.isTimerEntryAtMost waits := by
  sorry

/-- The wait timers among the DexF due timers are the failure-free due timers. -/
theorem dueTimers_filterMap_waitTimerOf (run : Run σ) (clock : Nat) :
    (run.dueTimers clock).filterMap waitTimerOf? = run.base.dueTimers clock := by
  sorry

/-- The wait timers among the first `count` DexF due timers are the first
`firedWaitTimerCount` failure-free due timers. -/
theorem take_dueTimers_filterMap_waitTimerOf (run : Run σ) (clock count : Nat) :
    ((run.dueTimers clock).take count).filterMap waitTimerOf? =
      (run.base.dueTimers clock).take
        (((run.dueTimers clock).take count).filter DueTimer.isWait).length := by
  sorry

end DexExpressiveness.DexF.WPF1
