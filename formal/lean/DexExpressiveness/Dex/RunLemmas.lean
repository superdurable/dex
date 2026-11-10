import DexExpressiveness.Dex.MetaFunctions

/-!
# Basic facts about the meta-functions of spec §4.2

Lookup after update, for association lists and for the Step table of a run.
The synchronization lemmas (`Dex/WaitLemmas.lean`) and the simulation proof
build on them. Everything here is proved.
-/

namespace DexExpressiveness

namespace AssocList

variable {κ ν : Type} [DecidableEq κ]

/-- `M[k ↦ v][k] = v`. -/
theorem get?_set_self (map : AssocList κ ν) (key : κ) (value : ν) :
    get? (set map key value) key = some value := by
  induction map with
  | nil => simp [set, get?]
  | cons entry rest inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    by_cases isKey : entryKey = key
    · simp [set, get?, isKey]
    · simp [set, get?, isKey, inductionHypothesis]

/-- `M[k ↦ v][k'] = M[k']` for `k' ≠ k`. -/
theorem get?_set_other (map : AssocList κ ν) (key other : κ) (value : ν)
    (isOther : other ≠ key) : get? (set map key value) other = get? map other := by
  induction map with
  | nil => simp [set, get?, Ne.symm isOther]
  | cons entry rest inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    by_cases isKey : entryKey = key
    · subst isKey
      simp [set, get?, Ne.symm isOther]
    · by_cases isOtherKey : entryKey = other
      · subst isOtherKey
        simp [set, get?, isOther]
      · simp [set, get?, isKey, isOtherKey, inductionHypothesis]

/-- A key removed from a map is absent. -/
theorem get?_erase_self (map : AssocList κ ν) (key : κ) : get? (erase map key) key = none := by
  induction map with
  | nil => simp [erase, get?]
  | cons entry rest inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    by_cases isKey : entryKey = key
    · simp [erase, isKey, inductionHypothesis]
    · simp [erase, get?, isKey, inductionHypothesis]

/-- Removing a key does not change the binding of another key. -/
theorem get?_erase_other (map : AssocList κ ν) (key other : κ) (isOther : other ≠ key) :
    get? (erase map key) other = get? map other := by
  induction map with
  | nil => simp [erase, get?]
  | cons entry rest inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    by_cases isKey : entryKey = key
    · subst isKey
      simp [erase, get?, Ne.symm isOther, inductionHypothesis]
    · by_cases isOtherKey : entryKey = other
      · subst isOtherKey
        simp [erase, get?, isOther]
      · simp [erase, get?, isKey, isOtherKey, inductionHypothesis]

/-- Setting a key that a key filter rejects does not change the filtered map. -/
theorem filter_set_of_rejected (isKept : κ → Bool) (map : AssocList κ ν) (key : κ) (value : ν)
    (isRejected : isKept key = false) :
    (set map key value).filter (fun entry => isKept entry.1) =
      map.filter (fun entry => isKept entry.1) := by
  induction map with
  | nil => simp [set, isRejected]
  | cons entry rest inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    by_cases isKey : entryKey = key
    · subst isKey
      simp [set, isRejected]
    · simp [set, isKey, List.filter_cons, inductionHypothesis]

/-- An entry of `M[k ↦ v]` is an entry of `M` or the new binding. -/
theorem mem_set (map : AssocList κ ν) (key : κ) (value : ν) (entry : κ × ν)
    (isMember : entry ∈ set map key value) : entry ∈ map ∨ entry = (key, value) := by
  induction map with
  | nil => simpa [set] using isMember
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    by_cases isKey : headKey = key
    · simp only [set, isKey, ↓reduceIte, List.mem_cons] at isMember
      rcases isMember with isNew | isInRest
      · exact Or.inr isNew
      · exact Or.inl (List.mem_cons_of_mem _ isInRest)
    · simp only [set, isKey, ↓reduceIte, List.mem_cons] at isMember
      rcases isMember with isHead | isInRest
      · exact Or.inl (by simp [isHead])
      · rcases inductionHypothesis isInRest with isOld | isNew
        · exact Or.inl (List.mem_cons_of_mem _ isOld)
        · exact Or.inr isNew

/-- An entry of a map after removing a key was an entry before. -/
theorem mem_of_mem_erase (map : AssocList κ ν) (key : κ) (entry : κ × ν)
    (isMember : entry ∈ erase map key) : entry ∈ map := by
  induction map with
  | nil => simp [erase] at isMember
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    by_cases isKey : headKey = key
    · simp only [erase, isKey, ↓reduceIte] at isMember
      exact List.mem_cons_of_mem _ (inductionHypothesis isMember)
    · simp only [erase, isKey, ↓reduceIte, List.mem_cons] at isMember
      rcases isMember with isHead | isInRest
      · simp [isHead]
      · exact List.mem_cons_of_mem _ (inductionHypothesis isInRest)

/-- A key in the domain of a map has an entry. -/
theorem mem_of_contains (map : AssocList κ ν) (key : κ) (isContained : contains map key = true) :
    ∃ value, (key, value) ∈ map := by
  induction map with
  | nil => simp [contains, get?] at isContained
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    by_cases isKey : headKey = key
    · subst isKey
      exact ⟨headValue, List.mem_cons_self ..⟩
    · have restContains : contains rest key = true := by
        simpa [contains, get?, isKey] using isContained
      obtain ⟨value, isMember⟩ := inductionHypothesis restContains
      exact ⟨value, List.mem_cons_of_mem _ isMember⟩

/-- Lookup in a map filtered by a predicate on keys. -/
theorem get?_filter (isKept : κ → Bool) (map : AssocList κ ν) (wanted : κ) :
    get? (map.filter (fun entry => isKept entry.1)) wanted =
      if isKept wanted then get? map wanted else none := by
  induction map with
  | nil => simp [get?]
  | cons entry rest inductionHypothesis =>
    obtain ⟨key, value⟩ := entry
    by_cases isWanted : key = wanted
    · subst isWanted
      by_cases keepsKey : isKept key = true
      · simp [keepsKey, get?]
      · simp [keepsKey, inductionHypothesis]
    · by_cases keepsKey : isKept key = true
      · simp [keepsKey, get?, isWanted, inductionHypothesis]
      · simp [keepsKey, get?, isWanted, inductionHypothesis]

/-- The key of an entry is bound. -/
theorem get?_isSome_of_mem (map : AssocList κ ν) (entry : κ × ν) (isMember : entry ∈ map) :
    ∃ value, get? map entry.1 = some value := by
  induction map with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    obtain ⟨key, value⟩ := head
    by_cases isKey : key = entry.1
    · exact ⟨value, by simp [get?, isKey]⟩
    · rcases List.mem_cons.mp isMember with isHead | isInRest
      · subst isHead
        exact absurd rfl isKey
      · obtain ⟨found, isFound⟩ := inductionHypothesis isInRest
        exact ⟨found, by simp [get?, isKey, isFound]⟩

end AssocList

/-- Two members with the same image are equal, when the images have no duplicates. -/
theorem eq_of_nodup_map {α β : Type} (image : α → β) (elements : List α)
    (imagesNodup : (elements.map image).Nodup) {first second : α}
    (isFirstMember : first ∈ elements) (isSecondMember : second ∈ elements)
    (sameImage : image first = image second) : first = second := by
  induction elements with
  | nil => cases isFirstMember
  | cons head rest inductionHypothesis =>
    simp only [List.map_cons, List.nodup_cons, List.mem_map, not_exists, not_and] at imagesNodup
    rcases List.mem_cons.mp isFirstMember with isHead | isInRest <;>
      rcases List.mem_cons.mp isSecondMember with isHead' | isInRest'
    · rw [isHead, isHead']
    · subst isHead
      exact absurd sameImage.symm (imagesNodup.1 second isInRest')
    · subst isHead'
      exact absurd sameImage (imagesNodup.1 first isInRest)
    · exact inductionHypothesis imagesNodup.2 isInRest isInRest'

/-- The elements of `insertSorted` are the new element and the old ones. -/
theorem mem_insertSorted {α : Type} (isAtMost : α → α → Bool) (newElement element : α)
    (elements : List α) :
    element ∈ insertSorted isAtMost newElement elements ↔
      element = newElement ∨ element ∈ elements := by
  induction elements with
  | nil => simp [insertSorted]
  | cons head rest inductionHypothesis =>
    by_cases isBefore : isAtMost newElement head = true
    · simp [insertSorted, isBefore]
    · simp [insertSorted, isBefore, inductionHypothesis]
      grind

/-- Sorting keeps the elements. -/
theorem mem_sortStable {α : Type} (isAtMost : α → α → Bool) (element : α) (elements : List α) :
    element ∈ sortStable isAtMost elements ↔ element ∈ elements := by
  induction elements with
  | nil => simp [sortStable]
  | cons head rest inductionHypothesis =>
    simp [sortStable, mem_insertSorted, inductionHypothesis]

/-- An element that is strictly before every other element comes first after sorting. -/
theorem head?_sortStable_of_least {α : Type} (isAtMost : α → α → Bool) (least : α)
    (elements : List α) (isMember : least ∈ elements)
    (isReflexive : isAtMost least least = true)
    (isLeast : ∀ other, other ∈ elements →
      other = least ∨ (isAtMost least other = true ∧ isAtMost other least = false)) :
    (sortStable isAtMost elements).head? = some least := by
  induction elements with
  | nil => simp at isMember
  | cons head rest inductionHypothesis =>
    simp only [sortStable]
    by_cases isInRest : least ∈ rest
    · have restHead := inductionHypothesis isInRest
        (fun other isOtherMember => isLeast other (List.mem_cons_of_mem _ isOtherMember))
      cases sortedCase : sortStable isAtMost rest with
      | nil => simp [sortedCase] at restHead
      | cons first laterSorted =>
        have isFirst : first = least := by simpa [sortedCase] using restHead
        subst isFirst
        rcases isLeast head (List.mem_cons_self ..) with isSame | ⟨_, isAfter⟩
        · subst isSame
          simp [insertSorted, isReflexive]
        · simp [insertSorted, isAfter]
    · have isHead : head = least := by
        rcases List.mem_cons.mp isMember with isSame | isLater
        · exact isSame.symm
        · exact absurd isLater isInRest
      subst isHead
      cases sortedCase : sortStable isAtMost rest with
      | nil => simp [insertSorted]
      | cons first laterSorted =>
        have isFirstMember : first ∈ rest := by
          have isSortedMember : first ∈ sortStable isAtMost rest := by simp [sortedCase]
          exact (mem_sortStable isAtMost first rest).mp isSortedMember
        rcases isLeast first (List.mem_cons_of_mem _ isFirstMember) with isSame | ⟨isBefore, _⟩
        · subst isSame
          simp [insertSorted, isReflexive]
        · simp [insertSorted, isBefore]

/-- The head after an insertion is the new element or the old head. -/
theorem head?_insertSorted {α : Type} (isAtMost : α → α → Bool) (newElement : α)
    (elements : List α) :
    (insertSorted isAtMost newElement elements).head? =
      some (match elements.head? with
        | none => newElement
        | some head => if isAtMost newElement head then newElement else head) := by
  cases elements with
  | nil => rfl
  | cons head rest =>
    by_cases isBefore : isAtMost newElement head = true <;> simp [insertSorted, isBefore]

/-- The head of a sorted list is at most every element. -/
theorem le_of_head?_sortStable {α : Type} (isAtMost : α → α → Bool)
    (isReflexive : ∀ element, isAtMost element element = true)
    (isTransitive : ∀ first second third, isAtMost first second = true →
      isAtMost second third = true → isAtMost first third = true)
    (isTotal : ∀ first second, isAtMost first second = true ∨ isAtMost second first = true)
    (elements : List α) (least : α)
    (isHead : (sortStable isAtMost elements).head? = some least) :
    ∀ other, other ∈ elements → isAtMost least other = true := by
  induction elements generalizing least with
  | nil => intro other isMember; cases isMember
  | cons head rest inductionHypothesis =>
    simp only [sortStable] at isHead
    rw [head?_insertSorted] at isHead
    cases restCase : (sortStable isAtMost rest).head? with
    | none =>
      simp only [restCase, Option.some.injEq] at isHead
      subst isHead
      intro other isMember
      rcases List.mem_cons.mp isMember with isSame | isInRest
      · subst isSame
        exact isReflexive _
      · have isSorted : other ∈ sortStable isAtMost rest := (mem_sortStable _ _ _).mpr isInRest
        cases sortedCase : sortStable isAtMost rest with
        | nil => rw [sortedCase] at isSorted; cases isSorted
        | cons first later => rw [sortedCase] at restCase; simp at restCase
    | some restLeast =>
      simp only [restCase, Option.some.injEq] at isHead
      have restIsLeast := inductionHypothesis restLeast restCase
      intro other isMember
      by_cases isBefore : isAtMost head restLeast = true
      · simp only [isBefore, ↓reduceIte] at isHead
        subst isHead
        rcases List.mem_cons.mp isMember with isSame | isInRest
        · subst isSame
          exact isReflexive _
        · exact isTransitive _ _ _ isBefore (restIsLeast other isInRest)
      · simp only [isBefore, Bool.false_eq_true, ↓reduceIte] at isHead
        subst isHead
        rcases List.mem_cons.mp isMember with isSame | isInRest
        · subst isSame
          rcases isTotal other restLeast with isFirst | isSecond
          · exact absurd isFirst isBefore
          · exact isSecond
        · exact restIsLeast other isInRest

/-- A sorted list with no head has no element. -/
theorem not_mem_of_head?_sortStable_none {α : Type} (isAtMost : α → α → Bool)
    (elements : List α) (isNone : (sortStable isAtMost elements).head? = none) (element : α) :
    element ∉ elements := by
  intro isMember
  have isSorted : element ∈ sortStable isAtMost elements := (mem_sortStable _ _ _).mpr isMember
  cases sortedCase : sortStable isAtMost elements with
  | nil => rw [sortedCase] at isSorted; cases isSorted
  | cons first later => rw [sortedCase] at isNone; simp at isNone

/-- Filtering a map by a predicate on keys removes a key the predicate rejects. -/
theorem AssocList.get?_filter_of_rejected {κ ν : Type} [DecidableEq κ] (isKept : κ → Bool)
    (map : AssocList κ ν) (wanted : κ) (isRejected : isKept wanted = false) :
    AssocList.get? (map.filter (fun entry => isKept entry.1)) wanted = none := by
  rw [AssocList.get?_filter]
  simp [isRejected]

namespace Dex

variable {σ : Signature}

/-- Looking up the Step that an update of the Step table changed: the first
record with the ID is the changed one, when the change keeps the ID. -/
theorem find?_map_update_self (steps : List (StepRecord σ)) (step : StepExecId σ)
    (change : StepRecord σ → StepRecord σ) (record : StepRecord σ)
    (isFound : steps.find? (fun candidate => decide (candidate.id = step)) = some record)
    (keepsId : (change record).id = record.id) :
    (steps.map (fun candidate => if candidate.id = step then change candidate else candidate)).find?
      (fun candidate => decide (candidate.id = step)) = some (change record) := by
  induction steps with
  | nil => simp at isFound
  | cons head rest inductionHypothesis =>
    by_cases isHead : head.id = step
    · have isRecord : head = record := by simpa [List.find?_cons, isHead] using isFound
      subst isRecord
      simp [isHead, keepsId]
    · have restFound : rest.find? (fun candidate => decide (candidate.id = step)) = some record := by
        simpa [List.find?_cons, isHead] using isFound
      simp [isHead, inductionHypothesis restFound]

/-- Looking up another Step after an update of the Step table, when the
change keeps IDs. -/
theorem find?_map_update_other (steps : List (StepRecord σ)) (step other : StepExecId σ)
    (change : StepRecord σ → StepRecord σ)
    (keepsId : ∀ candidate, (change candidate).id = candidate.id) (isOther : other ≠ step) :
    (steps.map (fun candidate => if candidate.id = step then change candidate else candidate)).find?
      (fun candidate => decide (candidate.id = other)) =
    steps.find? (fun candidate => decide (candidate.id = other)) := by
  induction steps with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    simp only [List.map_cons, List.find?_cons]
    by_cases isHead : head.id = step <;> by_cases isOtherHead : head.id = other <;> grind

namespace Run

/-- `S[e]` after `S[e] := change(S[e])`. -/
theorem findStep?_updateStep_self (run : Run σ) (step : StepExecId σ)
    (change : StepRecord σ → StepRecord σ) (record : StepRecord σ)
    (isFound : run.findStep? step = some record) (keepsId : (change record).id = record.id) :
    (run.updateStep step change).findStep? step = some (change record) :=
  find?_map_update_self run.steps step change record isFound keepsId

/-- `S[e']` after `S[e] := change(S[e])`, for `e' ≠ e`. -/
theorem findStep?_updateStep_other (run : Run σ) (step other : StepExecId σ)
    (change : StepRecord σ → StepRecord σ)
    (keepsId : ∀ candidate, (change candidate).id = candidate.id) (isOther : other ≠ step) :
    (run.updateStep step change).findStep? other = run.findStep? other :=
  find?_map_update_other run.steps step other change keepsId isOther

/-- The `Any` plan over Channels finds nothing when every Channel is empty. -/
theorem planAnyChannel_eq_none (pendingCount : σ.Chan → Nat) (chans : List σ.Chan)
    (areEmpty : ∀ chan, chan ∈ chans → pendingCount chan = 0) :
    planAnyChannel pendingCount chans = none := by
  induction chans with
  | nil => rfl
  | cons chan laterChans inductionHypothesis =>
    have isEmpty : pendingCount chan = 0 := areEmpty chan (List.mem_cons_self ..)
    have laterEmpty : ∀ later, later ∈ laterChans → pendingCount later = 0 :=
      fun later isMember => areEmpty later (List.mem_cons_of_mem _ isMember)
    simp [planAnyChannel, isEmpty, inductionHypothesis laterEmpty]

/-- Arming the timers of a Step and then disarming them leaves the timers of
the other Steps. -/
theorem filter_armTimers (step : StepExecId σ) (fireAt : List Nat) (index : Nat)
    (timers : AssocList (TimerId σ) Nat) :
    (armTimers step fireAt index timers).filter (fun entry => !decide (entry.1.step = step)) =
      timers.filter (fun entry => !decide (entry.1.step = step)) := by
  induction fireAt generalizing index timers with
  | nil => rfl
  | cons fireTime laterFireTimes inductionHypothesis =>
    simp only [armTimers]
    rw [inductionHypothesis]
    exact AssocList.filter_set_of_rejected (fun timer => !decide (timer.step = step)) timers
      { step := step, index := index } fireTime (by simp)

/-- Conditions that consume nothing leave the run unchanged and report "not done". -/
theorem consumeMessages_none (chans : List σ.Chan) (run : Run σ) :
    consumeMessages chans (chans.map (fun _ => false)) run = (run, chans.map (fun _ => none)) := by
  induction chans generalizing run with
  | nil => rfl
  | cons chan laterChans inductionHypothesis =>
    simp [consumeMessages, inductionHypothesis]

/-- Consuming messages does not touch the Step table. -/
theorem consumeMessages_steps (chans : List σ.Chan) (consumeFlags : List Bool) (run : Run σ) :
    (consumeMessages chans consumeFlags run).1.steps = run.steps := by
  induction chans generalizing consumeFlags run with
  | nil => rfl
  | cons chan laterChans inductionHypothesis =>
    simp only [consumeMessages]
    split
    · simp [inductionHypothesis, setPendingMessages]
    · simp [inductionHypothesis]

/-- Consuming messages leaves an empty Channel empty. -/
theorem consumeMessages_pending_nil (chans : List σ.Chan) (consumeFlags : List Bool)
    (run : Run σ) (chan : σ.Chan) (isEmpty : run.pendingMessages chan = []) :
    (consumeMessages chans consumeFlags run).1.pendingMessages chan = [] := by
  induction chans generalizing consumeFlags run with
  | nil => exact isEmpty
  | cons head laterChans inductionHypothesis =>
    simp only [consumeMessages]
    split
    · rename_i message remaining _ hasPending
      apply inductionHypothesis
      by_cases isHead : head = chan
      · subst isHead
        rw [isEmpty] at hasPending
        cases hasPending
      · simp only [pendingMessages, setPendingMessages] at isEmpty ⊢
        rw [AssocList.get?_set_other _ _ _ _ (Ne.symm isHead)]
        exact isEmpty
    · exact inductionHypothesis _ _ isEmpty

/-- The shape of `eval(e)`: nothing changes, or messages are consumed, the
timers of `e` are disarmed, and `S[e]` is updated without changing its ID. -/
theorem evalStep_shape (run : Run σ) (step : StepExecId σ) :
    run.evalStep step = run ∨
    ∃ chans consumeFlags change, (∀ candidate : StepRecord σ, (change candidate).id = candidate.id) ∧
      run.evalStep step =
        (consumeMessages chans consumeFlags (run.disarmTimersOf step)).1.updateStep step change := by
  unfold evalStep
  split
  · exact Or.inl rfl
  · split
    · split
      · exact Or.inl rfl
      · refine Or.inr ⟨_, _, _, ?_, rfl⟩
        intro candidate
        rfl
    · exact Or.inl rfl

/-- `eval(e)` leaves `S[e']` as it is, for `e' ≠ e`. -/
theorem findStep?_evalStep_other (run : Run σ) (step other : StepExecId σ)
    (isOther : other ≠ step) : (run.evalStep step).findStep? other = run.findStep? other := by
  rcases evalStep_shape run step with isSame | ⟨chans, consumeFlags, change, keepsId, isEval⟩
  · rw [isSame]
  · rw [isEval, findStep?_updateStep_other _ _ _ _ keepsId isOther]
    simp only [findStep?, consumeMessages_steps, disarmTimersOf]

/-- `eval(e)` leaves an empty Channel empty. -/
theorem pendingMessages_evalStep_nil (run : Run σ) (step : StepExecId σ) (chan : σ.Chan)
    (isEmpty : run.pendingMessages chan = []) : (run.evalStep step).pendingMessages chan = [] := by
  rcases evalStep_shape run step with isSame | ⟨chans, consumeFlags, change, _, isEval⟩
  · rw [isSame]; exact isEmpty
  · rw [isEval]
    exact consumeMessages_pending_nil chans consumeFlags (run.disarmTimersOf step) chan isEmpty

/-- The first record with an ID is the record itself, when IDs are distinct. -/
theorem findStep?_of_mem (run : Run σ) (record : StepRecord σ) (isMember : record ∈ run.steps)
    (idsAreDistinct : ∀ first, first ∈ run.steps → ∀ second, second ∈ run.steps →
      first.id = second.id → first = second) :
    run.findStep? record.id = some record := by
  unfold findStep?
  cases foundCase : run.steps.find? (fun candidate => decide (candidate.id = record.id)) with
  | none =>
    have noMatch := List.find?_eq_none.mp foundCase record isMember
    simp at noMatch
  | some found =>
    have isFoundMember := List.mem_of_find?_eq_some foundCase
    have sameId : found.id = record.id := by simpa using List.find?_some foundCase
    rw [idsAreDistinct found isFoundMember record isMember sameId]

/-- A plan that consumes from the first Channel only takes its oldest message. -/
theorem consumeMessages_first (chan : σ.Chan) (laterChans : List σ.Chan) (run : Run σ)
    (message : σ.Val) (remaining : List σ.Val)
    (hasPending : run.pendingMessages chan = message :: remaining) :
    consumeMessages (chan :: laterChans) (true :: laterChans.map (fun _ => false)) run =
      (run.setPendingMessages chan remaining, some message :: laterChans.map (fun _ => none)) := by
  simp [consumeMessages, hasPending, consumeMessages_none]

/-- The `Any` plan picks the first Channel when it has a pending message. -/
theorem planAnyChannel_first (pendingCount : σ.Chan → Nat) (chan : σ.Chan)
    (laterChans : List σ.Chan) (hasPending : 0 < pendingCount chan) :
    planAnyChannel pendingCount (chan :: laterChans) =
      some (true :: laterChans.map (fun _ => false)) := by
  simp [planAnyChannel, hasPending]

end Run

end Dex

end DexExpressiveness
