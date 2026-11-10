import DexExpressiveness.Failure.Rules

/-!
# WP-F1: list and sorting lemmas

Generic facts about `insertSorted`, `sortStable`, `filter`, and `take`, and
the due-timer order of DexF against the failure-free order.
-/

namespace DexExpressiveness.DexF.WPF1

variable {σ : Dex.Signature}

theorem mem_insertSorted' {α : Type} (isAtMost : α → α → Bool) (newElement element : α)
    (elements : List α) :
    element ∈ insertSorted isAtMost newElement elements ↔
      element = newElement ∨ element ∈ elements := by
  induction elements with
  | nil => simp [insertSorted]
  | cons head rest ih =>
    simp only [insertSorted]
    split
    · simp
    · simp only [List.mem_cons, ih]
      constructor
      · rintro (h | h | h) <;> simp [h]
      · rintro (h | h | h) <;> simp [h]

theorem mem_sortStable' {α : Type} (isAtMost : α → α → Bool) (element : α)
    (elements : List α) :
    element ∈ sortStable isAtMost elements ↔ element ∈ elements := by
  induction elements with
  | nil => simp [sortStable]
  | cons head rest ih => simp [sortStable, mem_insertSorted', ih]

theorem insertSorted_map {α β : Type} (isAtMost : β → β → Bool) (isAtMost' : α → α → Bool)
    (image : α → β) (newElement : α) (elements : List α)
    (agrees : ∀ second, second ∈ elements →
      isAtMost (image newElement) (image second) = isAtMost' newElement second) :
    insertSorted isAtMost (image newElement) (elements.map image) =
      (insertSorted isAtMost' newElement elements).map image := by
  induction elements with
  | nil => simp [insertSorted]
  | cons head rest ih =>
    simp only [List.map_cons, insertSorted]
    rw [agrees head (by simp)]
    split
    · simp
    · simp only [List.map_cons]
      rw [ih (fun second h => agrees second (by simp [h]))]

/-- Sorting a mapped list is mapping the sorted list, when the comparisons
agree on the list's elements. -/
theorem sortStable_map {α β : Type} (isAtMost : β → β → Bool) (isAtMost' : α → α → Bool)
    (image : α → β) (elements : List α)
    (agrees : ∀ first, first ∈ elements → ∀ second, second ∈ elements →
      isAtMost (image first) (image second) = isAtMost' first second) :
    sortStable isAtMost (elements.map image) = (sortStable isAtMost' elements).map image := by
  induction elements with
  | nil => simp [sortStable]
  | cons head rest ih =>
    simp only [List.map_cons, sortStable]
    rw [ih (fun first h1 second h2 => agrees first (by simp [h1]) second (by simp [h2]))]
    exact insertSorted_map isAtMost isAtMost' image head _
      (fun second h => agrees head (by simp) second
        (by simp [(mem_sortStable' isAtMost' second rest).mp h]))

theorem insertSorted_pairwise_key {α : Type} (key : α → Nat) (newElement : α)
    (elements : List α) (sorted : elements.Pairwise (fun first second => key first ≤ key second)) :
    (insertSorted (fun first second => decide (key first ≤ key second)) newElement elements).Pairwise
      (fun first second => key first ≤ key second) := by
  induction elements with
  | nil => simp [insertSorted]
  | cons head rest ih =>
    rw [List.pairwise_cons] at sorted
    simp only [insertSorted]
    split
    · rename_i isAtMost
      have isAtMost' : key newElement ≤ key head := of_decide_eq_true isAtMost
      refine List.Pairwise.cons ?_ (List.Pairwise.cons sorted.1 sorted.2)
      intro element h
      simp only [List.mem_cons] at h
      rcases h with h | h
      · subst h; exact isAtMost'
      · have := sorted.1 element h
        omega
    · rename_i isNotAtMost
      have isNotAtMost' : ¬ key newElement ≤ key head := by simpa using isNotAtMost
      refine List.Pairwise.cons ?_ (ih sorted.2)
      intro element h
      rcases (mem_insertSorted' _ _ _ _).mp h with h | h
      · subst h; omega
      · exact sorted.1 element h

/-- `sortStable` by a natural-number key returns a list sorted by that key. -/
theorem sortStable_pairwise_key {α : Type} (key : α → Nat) (elements : List α) :
    (sortStable (fun first second => decide (key first ≤ key second)) elements).Pairwise
      (fun first second => key first ≤ key second) := by
  induction elements with
  | nil => simp [sortStable]
  | cons head rest ih =>
    simp only [sortStable]
    exact insertSorted_pairwise_key key head _ ih

/-- Two lists strictly sorted by a natural-number key with the same members
are equal. -/
theorem eq_of_pairwise_lt_of_mem_iff {α : Type} (key : α → Nat) (first second : List α)
    (firstSorted : first.Pairwise (fun a b => key a < key b))
    (secondSorted : second.Pairwise (fun a b => key a < key b))
    (sameMembers : ∀ element, element ∈ first ↔ element ∈ second) : first = second := by
  induction first generalizing second with
  | nil =>
    cases second with
    | nil => rfl
    | cons head rest => exact absurd ((sameMembers head).mpr (by simp)) (by simp)
  | cons head rest ih =>
    cases second with
    | nil => exact absurd ((sameMembers head).mp (by simp)) (by simp)
    | cons head' rest' =>
      rw [List.pairwise_cons] at firstSorted secondSorted
      have headsEqual : head = head' := by
        have h1 := (sameMembers head).mp (by simp)
        have h2 := (sameMembers head').mpr (by simp)
        simp only [List.mem_cons] at h1 h2
        rcases h1 with h1 | h1
        · exact h1
        · rcases h2 with h2 | h2
          · exact h2.symm
          · have := firstSorted.1 head' h2
            have := secondSorted.1 head h1
            omega
      subst headsEqual
      congr 1
      apply ih rest' firstSorted.2 secondSorted.2
      intro element
      constructor
      · intro h
        have h' := (sameMembers element).mp (by simp [h])
        simp only [List.mem_cons] at h'
        rcases h' with h' | h'
        · subst h'
          have := firstSorted.1 element h
          omega
        · exact h'
      · intro h
        have h' := (sameMembers element).mpr (by simp [h])
        simp only [List.mem_cons] at h'
        rcases h' with h' | h'
        · subst h'
          have := secondSorted.1 element h
          omega
        · exact h'

/-- The filter of a prefix is a prefix of the filter. -/
theorem filter_take {α : Type} (isKept : α → Bool) (elements : List α) (count : Nat) :
    (elements.take count).filter isKept =
      (elements.filter isKept).take ((elements.take count).filter isKept).length := by
  have split : elements.filter isKept =
      (elements.take count).filter isKept ++ (elements.drop count).filter isKept := by
    rw [← List.filter_append, List.take_append_drop]
  rw [split, List.take_left' rfl]

/-- The wait timer of a due timer, if it is one. -/
def waitTimerOf? : DueTimer σ → Option (Dex.TimerId σ)
  | .wait timer => some timer
  | .retry _ => none

/-- The wait entry of a due entry, with its fire time, if it is one. -/
def waitEntryOf? (entry : DueTimer σ × Nat) : Option (Dex.TimerId σ × Nat) :=
  (waitTimerOf? entry.1).map (fun timer => (timer, entry.2))

theorem filterMap_waitEntryOf_wait (timer : Dex.TimerId σ) (time : Nat)
    (rest : List (DueTimer σ × Nat)) :
    ((DueTimer.wait timer, time) :: rest).filterMap waitEntryOf? =
      (timer, time) :: rest.filterMap waitEntryOf? := rfl

theorem filterMap_waitEntryOf_retry (timer : RetryTimerId σ) (time : Nat)
    (rest : List (DueTimer σ × Nat)) :
    ((DueTimer.retry timer, time) :: rest).filterMap waitEntryOf? =
      rest.filterMap waitEntryOf? := rfl

/-- Every wait entry after a retry entry fires strictly later. -/
def RetriesBeforeLaterWaits (first second : DueTimer σ × Nat) : Prop :=
  first.1.isWait = false → second.1.isWait = true → first.2 < second.2

theorem retriesOnly_invariant (entries : List (DueTimer σ × Nat))
    (allRetries : ∀ entry, entry ∈ entries → entry.1.isWait = false) :
    entries.Pairwise RetriesBeforeLaterWaits ∧ entries.filterMap waitEntryOf? = [] := by
  induction entries with
  | nil => simp
  | cons head rest ih =>
    obtain ⟨restPairwise, restWaits⟩ := ih (fun entry h => allRetries entry (by simp [h]))
    have headRetry := allRetries head (by simp)
    refine ⟨List.Pairwise.cons ?_ restPairwise, ?_⟩
    · intro entry h _ isWait
      rw [allRetries entry (by simp [h])] at isWait
      cases isWait
    · obtain ⟨timer, time⟩ := head
      cases timer with
      | wait timer => simp [DueTimer.isWait] at headRetry
      | retry timer => simpa [waitEntryOf?, waitTimerOf?] using restWaits

theorem insertSorted_wait_invariant (timer : Dex.TimerId σ) (time : Nat)
    (entries : List (DueTimer σ × Nat))
    (invariant : entries.Pairwise RetriesBeforeLaterWaits) :
    (insertSorted isDueEntryAtMost (DueTimer.wait timer, time) entries).Pairwise
        RetriesBeforeLaterWaits ∧
      (insertSorted isDueEntryAtMost (DueTimer.wait timer, time) entries).filterMap
          waitEntryOf? =
        insertSorted Dex.Run.isTimerEntryAtMost (timer, time) (entries.filterMap waitEntryOf?) := by
  induction entries with
  | nil =>
    refine ⟨by simp [insertSorted], ?_⟩
    simp [insertSorted, waitEntryOf?, waitTimerOf?]
  | cons head rest ih =>
    rw [List.pairwise_cons] at invariant
    obtain ⟨restPairwise, restWaits⟩ := ih invariant.2
    obtain ⟨headTimer, headTime⟩ := head
    cases headTimer with
    | wait headTimer =>
      have compare : isDueEntryAtMost (DueTimer.wait timer, time) (DueTimer.wait headTimer, headTime) =
          Dex.Run.isTimerEntryAtMost (timer, time) (headTimer, headTime) := rfl
      simp only [insertSorted]
      by_cases isAtMost : Dex.Run.isTimerEntryAtMost (timer, time) (headTimer, headTime) = true
      · simp only [compare, isAtMost, ↓reduceIte]
        refine ⟨?_, ?_⟩
        · refine List.Pairwise.cons ?_ (List.Pairwise.cons invariant.1 invariant.2)
          intro _ _ isRetry
          simp [DueTimer.isWait] at isRetry
        · simp only [filterMap_waitEntryOf_wait, insertSorted, isAtMost, ↓reduceIte]
      · simp only [compare, isAtMost, Bool.false_eq_true, ↓reduceIte]
        refine ⟨?_, ?_⟩
        · refine List.Pairwise.cons ?_ restPairwise
          intro _ _ isRetry
          simp [DueTimer.isWait] at isRetry
        · rw [filterMap_waitEntryOf_wait, filterMap_waitEntryOf_wait, restWaits]
          simp only [insertSorted, isAtMost, Bool.false_eq_true, ↓reduceIte]
    | retry headTimer =>
      have retryHead : ∀ (tail : List (DueTimer σ × Nat)),
          ((DueTimer.retry headTimer, headTime) :: tail).filterMap waitEntryOf? =
            tail.filterMap waitEntryOf? := fun tail => filterMap_waitEntryOf_retry headTimer headTime tail
      have compare : isDueEntryAtMost (DueTimer.wait timer, time) (DueTimer.retry headTimer, headTime) =
          decide (time ≤ headTime) := rfl
      simp only [insertSorted]
      by_cases isAtMost : decide (time ≤ headTime) = true
      · simp only [compare, isAtMost, ↓reduceIte]
        have timeAtMost : time ≤ headTime := of_decide_eq_true isAtMost
        refine ⟨?_, ?_⟩
        · refine List.Pairwise.cons ?_ (List.Pairwise.cons invariant.1 invariant.2)
          intro _ _ isRetry
          simp [DueTimer.isWait] at isRetry
        · have laterWaits : ∀ wait, wait ∈ rest.filterMap waitEntryOf? → headTime < wait.2 := by
            intro wait h
            obtain ⟨entry, entryMem, entryWait⟩ := List.mem_filterMap.mp h
            obtain ⟨entryTimer, entryTime⟩ := entry
            cases entryTimer with
            | retry _ => simp [waitEntryOf?, waitTimerOf?] at entryWait
            | wait entryTimer =>
              simp only [waitEntryOf?, waitTimerOf?, Option.map_some, Option.some.injEq] at entryWait
              subst entryWait
              exact invariant.1 _ entryMem rfl rfl
          have firstWait : ∀ (waits : List (Dex.TimerId σ × Nat)),
              (∀ wait, wait ∈ waits → headTime < wait.2) →
              insertSorted Dex.Run.isTimerEntryAtMost (timer, time) waits = (timer, time) :: waits := by
            intro waits later
            cases waits with
            | nil => rfl
            | cons wait waitsRest =>
              have := later wait (by simp)
              have isLess : Dex.Run.isTimerEntryAtMost (timer, time) wait = true := by
                simp only [Dex.Run.isTimerEntryAtMost, Bool.or_eq_true, decide_eq_true_eq]
                left; omega
              simp [insertSorted, isLess]
          simp only [filterMap_waitEntryOf_wait, retryHead, firstWait _ laterWaits]
      · simp only [compare, isAtMost, Bool.false_eq_true, ↓reduceIte]
        have timeLater : headTime < time := by
          have := of_decide_eq_false (Bool.eq_false_iff.mpr isAtMost)
          omega
        refine ⟨?_, ?_⟩
        · refine List.Pairwise.cons ?_ restPairwise
          intro entry h _ isWait
          rcases (mem_insertSorted' _ _ _ _).mp h with h | h
          · subst h; exact timeLater
          · exact invariant.1 entry h rfl isWait
        · rw [retryHead, retryHead, restWaits]

theorem sortStable_dueEntries_invariant (waits : List (Dex.TimerId σ × Nat))
    (retries : List (RetryTimerId σ × Nat)) :
    (sortStable isDueEntryAtMost
        (waits.map (fun entry => (DueTimer.wait entry.1, entry.2)) ++
          retries.map (fun entry => (DueTimer.retry entry.1, entry.2)))).Pairwise
        RetriesBeforeLaterWaits ∧
      (sortStable isDueEntryAtMost
        (waits.map (fun entry => (DueTimer.wait entry.1, entry.2)) ++
          retries.map (fun entry => (DueTimer.retry entry.1, entry.2)))).filterMap waitEntryOf? =
        sortStable Dex.Run.isTimerEntryAtMost waits := by
  induction waits with
  | nil =>
    simp only [List.map_nil, List.nil_append, sortStable]
    apply retriesOnly_invariant
    intro entry h
    obtain ⟨retry, _, rfl⟩ := List.mem_map.mp ((mem_sortStable' _ _ _).mp h)
    rfl
  | cons head rest ih =>
    simp only [List.map_cons, List.cons_append, sortStable]
    obtain ⟨restPairwise, restWaits⟩ := ih
    obtain ⟨insertedPairwise, insertedWaits⟩ :=
      insertSorted_wait_invariant head.1 head.2 _ restPairwise
    exact ⟨insertedPairwise, by rw [insertedWaits, restWaits]⟩

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
  exact (sortStable_dueEntries_invariant waits retries).2

theorem map_fst_filterMap_waitTimerOf (entries : List (DueTimer σ × Nat)) :
    (entries.map (fun entry => entry.1)).filterMap waitTimerOf? =
      (entries.filterMap waitEntryOf?).map (fun entry => entry.1) := by
  induction entries with
  | nil => rfl
  | cons head rest ih =>
    obtain ⟨timer, time⟩ := head
    cases timer with
    | wait timer => rw [filterMap_waitEntryOf_wait]; simp only [List.map_cons]; rw [← ih]; rfl
    | retry timer => rw [filterMap_waitEntryOf_retry]; rw [← ih]; rfl

theorem filterMap_take {α β : Type} (image? : α → Option β) (elements : List α) (count : Nat) :
    (elements.take count).filterMap image? =
      (elements.filterMap image?).take ((elements.take count).filterMap image?).length := by
  have split : elements.filterMap image? =
      (elements.take count).filterMap image? ++ (elements.drop count).filterMap image? := by
    rw [← List.filterMap_append, List.take_append_drop]
  rw [split, List.take_left' rfl]

theorem length_filterMap_waitTimerOf (timers : List (DueTimer σ)) :
    (timers.filterMap waitTimerOf?).length = (timers.filter DueTimer.isWait).length := by
  induction timers with
  | nil => rfl
  | cons head rest ih =>
    cases head with
    | wait timer =>
      show (_ :: rest.filterMap waitTimerOf?).length = (_ :: rest.filter DueTimer.isWait).length
      simp only [List.length_cons, ih]
    | retry timer =>
      show (rest.filterMap waitTimerOf?).length = (rest.filter DueTimer.isWait).length
      exact ih

/-- The wait timers among the DexF due timers are the failure-free due timers. -/
theorem dueTimers_filterMap_waitTimerOf (run : Run σ) (clock : Nat) :
    (run.dueTimers clock).filterMap waitTimerOf? = run.base.dueTimers clock := by
  simp only [Run.dueTimers, Dex.Run.dueTimers]
  rw [map_fst_filterMap_waitTimerOf]
  exact congrArg (List.map _) (sortStable_dueEntries_invariant _ _).2

/-- The wait timers among the first `count` DexF due timers are the first
`firedWaitTimerCount` failure-free due timers. -/
theorem take_dueTimers_filterMap_waitTimerOf (run : Run σ) (clock count : Nat) :
    ((run.dueTimers clock).take count).filterMap waitTimerOf? =
      (run.base.dueTimers clock).take
        (((run.dueTimers clock).take count).filter DueTimer.isWait).length := by
  rw [filterMap_take, dueTimers_filterMap_waitTimerOf, length_filterMap_waitTimerOf]

end DexExpressiveness.DexF.WPF1
