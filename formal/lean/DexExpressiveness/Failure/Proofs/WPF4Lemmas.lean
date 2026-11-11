import DexExpressiveness.Failure.Relation
import DexExpressiveness.Dex.TimerLemmas

/-!
# WP-F4: lemmas about the bookkeeping of a commit

The ghost reconcile, the attempt reconcile, association lists, and the task
lists. `Failure/Proofs/WPF4Invariant.lean` uses them to prove `invariant_step`.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-! ## Association lists -/

section AssocLists

variable {κ ν : Type} [DecidableEq κ]

/-- Setting an absent key appends it. -/
theorem set_of_contains_false (map : AssocList κ ν) (key : κ) (value : ν)
    (isAbsent : AssocList.contains map key = false) :
    AssocList.set map key value = map ++ [(key, value)] := by
  induction map with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    have isOther : headKey ≠ key := by
      intro isSame
      simp [AssocList.contains, AssocList.get?, isSame] at isAbsent
    have restAbsent : AssocList.contains rest key = false := by
      simpa [AssocList.contains, AssocList.get?, isOther] using isAbsent
    simp [AssocList.set, isOther, inductionHypothesis restAbsent]

/-- A member's key has a binding. -/
theorem contains_of_mem (map : AssocList κ ν) (entry : κ × ν) (isMember : entry ∈ map) :
    AssocList.contains map entry.1 = true := by
  obtain ⟨value, isFound⟩ := AssocList.get?_isSome_of_mem map entry isMember
  simp [AssocList.contains, isFound]

/-- With distinct keys, the binding of a member's key is the member's value. -/
theorem get?_of_mem_nodup (map : AssocList κ ν) (entry : κ × ν) (isMember : entry ∈ map)
    (keysNodup : (map.map (fun entry => entry.1)).Nodup) :
    AssocList.get? map entry.1 = some entry.2 := by
  induction map with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    simp only [List.map_cons, List.nodup_cons] at keysNodup
    rcases List.mem_cons.mp isMember with isHead | isRest
    · subst isHead
      simp [AssocList.get?]
    · have isOther : headKey ≠ entry.1 := by
        intro isSame
        exact keysNodup.1 (isSame ▸ List.mem_map.mpr ⟨entry, isRest, rfl⟩)
      simp [AssocList.get?, isOther, inductionHypothesis isRest keysNodup.2]

/-- What lookup finds is a member. -/
theorem mem_of_get? (map : AssocList κ ν) (key : κ) (value : ν)
    (isFound : AssocList.get? map key = some value) : (key, value) ∈ map := by
  induction map with
  | nil => simp [AssocList.get?] at isFound
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    by_cases isKey : headKey = key
    · subst isKey
      simp only [AssocList.get?, ↓reduceIte, Option.some.injEq] at isFound
      subst isFound
      exact List.mem_cons_self
    · simp only [AssocList.get?, isKey, ↓reduceIte] at isFound
      exact List.mem_cons_of_mem _ (inductionHypothesis isFound)

/-- `contains` after `set`. -/
theorem contains_set (map : AssocList κ ν) (key other : κ) (value : ν) :
    AssocList.contains (AssocList.set map key value) other =
      (decide (other = key) || AssocList.contains map other) := by
  by_cases isKey : other = key
  · subst isKey
    simp [AssocList.contains, AssocList.get?_set_self]
  · simp [AssocList.contains, AssocList.get?_set_other _ _ _ _ isKey, isKey]

/-- `contains` after `erase`. -/
theorem contains_erase_other (map : AssocList κ ν) (key other : κ) (isOther : other ≠ key) :
    AssocList.contains (AssocList.erase map key) other = AssocList.contains map other := by
  simp [AssocList.contains, AssocList.get?_erase_other _ _ _ isOther]

/-- `erase` keeps distinct keys. -/
theorem nodup_keys_erase (map : AssocList κ ν) (key : κ)
    (keysNodup : (map.map (fun entry => entry.1)).Nodup) :
    ((AssocList.erase map key).map (fun entry => entry.1)).Nodup :=
  List.Nodup.sublist ((AssocList.erase_sublist map key).map _) keysNodup

end AssocLists

/-! ## Step lookup -/

/-- With distinct IDs, two members with one ID are equal. -/
theorem eq_of_id_eq {steps : List (Dex.StepRecord σ)}
    (idsNodup : (steps.map (fun record => record.id)).Nodup) {first second : Dex.StepRecord σ}
    (isFirst : first ∈ steps) (isSecond : second ∈ steps) (sameId : first.id = second.id) :
    first = second :=
  eq_of_nodup_map (fun record => record.id) steps idsNodup isFirst isSecond sameId

/-- With distinct IDs, lookup finds the member. -/
theorem findStep?_of_mem {run : Dex.Run σ} {record : Dex.StepRecord σ}
    (idsNodup : (run.steps.map (fun record => record.id)).Nodup) (isMember : record ∈ run.steps) :
    run.findStep? record.id = some record :=
  Dex.Run.findStep?_of_mem_nodup run record isMember idsNodup

/-- Lookup finds a member with the ID. -/
theorem mem_of_findStep? {run : Dex.Run σ} {step : Dex.StepExecId σ} {record : Dex.StepRecord σ}
    (isFound : run.findStep? step = some record) : record ∈ run.steps ∧ record.id = step :=
  Dex.Run.mem_of_findStep? run step record isFound

/-! ## The keys of the current invocations -/

/-- The current invocations of a run, as `Ghost.reconcile` computes them. -/
def currentKeys (base : Dex.Run σ) : List (InvocationKey σ) :=
  base.steps.filterMap (fun record => record.invocation.map (fun invocation =>
    (record.id, invocation.method)))

/-- A key is current exactly when a Step with that ID has an invocation of
that method. -/
theorem mem_currentKeys (base : Dex.Run σ) (key : InvocationKey σ) :
    key ∈ currentKeys base ↔
      ∃ record invocation, record ∈ base.steps ∧ record.invocation = some invocation ∧
        record.id = key.1 ∧ invocation.method = key.2 := by
  unfold currentKeys
  simp only [List.mem_filterMap, Option.map_eq_some_iff]
  constructor
  · rintro ⟨record, isMember, invocation, hasInvocation, isKey⟩
    subst isKey
    exact ⟨record, invocation, isMember, hasInvocation, rfl, rfl⟩
  · rintro ⟨record, invocation, isMember, hasInvocation, isId, isMethod⟩
    refine ⟨record, isMember, invocation, hasInvocation, ?_⟩
    obtain ⟨first, second⟩ := key
    simp only at isId isMethod
    rw [isId, isMethod]

/-! ## The ghost reconcile -/

/-- The history of an ended invocation: committed when the commit completed
it, canceled otherwise. -/
def endHistory (completed : Option (InvocationKey σ)) (key : InvocationKey σ)
    (history : InvocationHistory σ) : InvocationHistory σ :=
  { history with outcome := if completed = some key then .committed else .canceled }

/-- The marking step of `Ghost.reconcile` on one history. -/
def markHistory (completed : Option (InvocationKey σ)) (current : List (InvocationKey σ))
    (key : InvocationKey σ) (history : InvocationHistory σ) : InvocationHistory σ :=
  if history.outcome = .live && !current.contains key then endHistory completed key history
  else history

/-- The adding step of `Ghost.reconcile`. -/
def addFresh (count : Nat) (entries : AssocList (InvocationKey σ) (InvocationHistory σ))
    (key : InvocationKey σ) : AssocList (InvocationKey σ) (InvocationHistory σ) :=
  if AssocList.contains entries key then entries
  else AssocList.set entries key (InvocationHistory.fresh count)

/-- `Ghost.reconcile` unfolded. -/
theorem reconcile_invocations (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) :
    (ghost.reconcile completed base).invocations =
      (currentKeys base).foldl (addFresh ghost.normalCount)
        (ghost.invocations.map (fun entry =>
          (entry.1, markHistory completed (currentKeys base) entry.1 entry.2))) := by
  unfold Ghost.reconcile
  simp only [currentKeys]
  congr 1
  apply List.map_congr_left
  intro entry _
  unfold markHistory endHistory
  split <;> simp_all

/-- The other fields of the ghost are unchanged by the reconcile. -/
theorem reconcile_other (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) :
    (ghost.reconcile completed base).normalCount = ghost.normalCount ∧
    (ghost.reconcile completed base).normalIndex = ghost.normalIndex ∧
    (ghost.reconcile completed base).lastNormalCommitAt = ghost.lastNormalCommitAt ∧
    (ghost.reconcile completed base).nextVirtualTaskId = ghost.nextVirtualTaskId :=
  ⟨rfl, rfl, rfl, rfl⟩

/-- Lookup after the marking step. -/
theorem get?_marked (entries : AssocList (InvocationKey σ) (InvocationHistory σ))
    (change : InvocationKey σ → InvocationHistory σ → InvocationHistory σ) (key : InvocationKey σ) :
    AssocList.get? (entries.map (fun entry => (entry.1, change entry.1 entry.2))) key =
      (AssocList.get? entries key).map (change key) := by
  induction entries with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    by_cases isKey : headKey = key
    · subst isKey
      simp [AssocList.get?]
    · simp [AssocList.get?, isKey, inductionHypothesis]

/-- Lookup after the adding fold. -/
theorem get?_foldl_addFresh (count : Nat) (keys : List (InvocationKey σ))
    (entries : AssocList (InvocationKey σ) (InvocationHistory σ)) (key : InvocationKey σ) :
    AssocList.get? (keys.foldl (addFresh count) entries) key =
      match AssocList.get? entries key with
      | some history => some history
      | none => if key ∈ keys then some (InvocationHistory.fresh count) else none := by
  induction keys generalizing entries with
  | nil =>
    cases isFound : AssocList.get? entries key <;> simp [isFound]
  | cons head rest inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [inductionHypothesis]
    unfold addFresh
    by_cases isContained : AssocList.contains entries head = true
    · simp only [isContained, ↓reduceIte]
      cases isFound : AssocList.get? entries key with
      | some history => rfl
      | none =>
        have isOther : key ≠ head := by
          intro isSame
          subst isSame
          simp [AssocList.contains, isFound] at isContained
        simp [isOther]
    · simp only [isContained, Bool.false_eq_true, ↓reduceIte]
      by_cases isKey : key = head
      · subst isKey
        have isAbsent : AssocList.get? entries key = none := by
          simpa [AssocList.contains] using isContained
        simp [AssocList.get?_set_self, isAbsent]
      · rw [AssocList.get?_set_other _ _ _ _ isKey]
        cases AssocList.get? entries key <;> simp [isKey]

/-- Lookup after the reconcile: a pended invocation keeps its history, with
its outcome marked; a current invocation without history gets a fresh one. -/
theorem findInvocation?_reconcile (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) (key : InvocationKey σ) :
    (ghost.reconcile completed base).findInvocation? key =
      match ghost.findInvocation? key with
      | some history => some (markHistory completed (currentKeys base) key history)
      | none =>
        if key ∈ currentKeys base then some (InvocationHistory.fresh ghost.normalCount)
        else none := by
  unfold Ghost.findInvocation?
  rw [reconcile_invocations, get?_foldl_addFresh, get?_marked]
  cases AssocList.get? ghost.invocations key <;> rfl

/-- The members after the adding fold. -/
theorem mem_foldl_addFresh (count : Nat) (keys : List (InvocationKey σ))
    (entries : AssocList (InvocationKey σ) (InvocationHistory σ))
    (entry : InvocationKey σ × InvocationHistory σ)
    (isMember : entry ∈ keys.foldl (addFresh count) entries) :
    entry ∈ entries ∨ (entry.1 ∈ keys ∧ entry.2 = InvocationHistory.fresh count ∧
      AssocList.contains entries entry.1 = false) := by
  induction keys generalizing entries with
  | nil => exact Or.inl isMember
  | cons head rest inductionHypothesis =>
    simp only [List.foldl_cons] at isMember
    rcases inductionHypothesis _ isMember with isOld | ⟨isKey, isFresh, isAbsent⟩
    · unfold addFresh at isOld
      split at isOld
      · exact Or.inl isOld
      · rcases AssocList.mem_set _ _ _ _ isOld with isPrevious | isNew
        · exact Or.inl isPrevious
        · right
          subst isNew
          rename_i isNotContained
          exact ⟨List.mem_cons_self, rfl, by simpa using isNotContained⟩
    · right
      refine ⟨List.mem_cons_of_mem _ isKey, isFresh, ?_⟩
      unfold addFresh at isAbsent
      split at isAbsent
      · exact isAbsent
      · rw [contains_set] at isAbsent
        simp only [Bool.or_eq_false_iff, decide_eq_false_iff_not] at isAbsent
        exact isAbsent.2

/-- The adding fold keeps distinct keys. -/
theorem nodup_foldl_addFresh (count : Nat) (keys : List (InvocationKey σ))
    (entries : AssocList (InvocationKey σ) (InvocationHistory σ))
    (keysNodup : (entries.map (fun entry => entry.1)).Nodup) :
    ((keys.foldl (addFresh count) entries).map (fun entry => entry.1)).Nodup := by
  induction keys generalizing entries with
  | nil => exact keysNodup
  | cons head rest inductionHypothesis =>
    simp only [List.foldl_cons]
    apply inductionHypothesis
    unfold addFresh
    split
    · exact keysNodup
    · exact AssocList.nodup_keys_set _ _ _ keysNodup

/-- The adding fold appends histories without arguments. -/
theorem filterMap_foldl_addFresh (count : Nat) (keys : List (InvocationKey σ))
    (entries : AssocList (InvocationKey σ) (InvocationHistory σ)) :
    (keys.foldl (addFresh count) entries).filterMap (fun entry => entry.2.arguments) =
      entries.filterMap (fun entry => entry.2.arguments) := by
  induction keys generalizing entries with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    simp only [List.foldl_cons]
    rw [inductionHypothesis]
    unfold addFresh
    split
    · rfl
    · rename_i isNotContained
      rw [set_of_contains_false _ _ _ (by simpa using isNotContained)]
      simp [InvocationHistory.fresh]

/-- The members after the reconcile. -/
theorem mem_reconcile (ghost : Ghost σ) (completed : Option (InvocationKey σ)) (base : Dex.Run σ)
    (entry : InvocationKey σ × InvocationHistory σ)
    (isMember : entry ∈ (ghost.reconcile completed base).invocations) :
    (∃ original, original ∈ ghost.invocations ∧ entry.1 = original.1 ∧
      entry.2 = markHistory completed (currentKeys base) original.1 original.2) ∨
    (entry.1 ∈ currentKeys base ∧ entry.2 = InvocationHistory.fresh ghost.normalCount ∧
      ghost.findInvocation? entry.1 = none) := by
  rw [reconcile_invocations] at isMember
  rcases mem_foldl_addFresh _ _ _ _ isMember with isMarked | ⟨isKey, isFresh, isAbsent⟩
  · obtain ⟨original, isOriginal, isEntry⟩ := List.mem_map.mp isMarked
    left
    exact ⟨original, isOriginal, by rw [← isEntry], by rw [← isEntry]⟩
  · right
    refine ⟨isKey, isFresh, ?_⟩
    have isNone : AssocList.get? (ghost.invocations.map (fun entry =>
        (entry.1, markHistory completed (currentKeys base) entry.1 entry.2))) entry.1 = none := by
      simpa [AssocList.contains] using isAbsent
    rw [get?_marked] at isNone
    unfold Ghost.findInvocation?
    cases isFound : AssocList.get? ghost.invocations entry.1 with
    | none => rfl
    | some history => simp [isFound] at isNone

/-- The reconcile keeps distinct keys. -/
theorem nodup_reconcile (ghost : Ghost σ) (completed : Option (InvocationKey σ)) (base : Dex.Run σ)
    (keysNodup : (ghost.invocations.map (fun entry => entry.1)).Nodup) :
    ((ghost.reconcile completed base).invocations.map (fun entry => entry.1)).Nodup := by
  rw [reconcile_invocations]
  apply nodup_foldl_addFresh
  simpa [List.map_map, Function.comp_def] using keysNodup

/-- The reconcile keeps the arguments of every history. -/
theorem filterMap_reconcile (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) :
    (ghost.reconcile completed base).invocations.filterMap (fun entry => entry.2.arguments) =
      ghost.invocations.filterMap (fun entry => entry.2.arguments) := by
  rw [reconcile_invocations, filterMap_foldl_addFresh, List.filterMap_map]
  congr 1
  funext entry
  unfold markHistory endHistory
  simp only [Function.comp_def]
  split <;> rfl

/-- Marking changes only the outcome. -/
theorem markHistory_fields (completed : Option (InvocationKey σ))
    (current : List (InvocationKey σ)) (key : InvocationKey σ) (history : InvocationHistory σ) :
    (markHistory completed current key history).pendedAt = history.pendedAt ∧
    (markHistory completed current key history).emittedCount = history.emittedCount ∧
    (markHistory completed current key history).progress = history.progress ∧
    (markHistory completed current key history).arguments = history.arguments := by
  unfold markHistory endHistory
  split <;> exact ⟨rfl, rfl, rfl, rfl⟩

/-- A marked history is live only when it was live and is current. -/
theorem markHistory_live (completed : Option (InvocationKey σ))
    (current : List (InvocationKey σ)) (key : InvocationKey σ) (history : InvocationHistory σ)
    (isLive : (markHistory completed current key history).outcome = .live) :
    history.outcome = .live ∧ key ∈ current := by
  unfold markHistory endHistory at isLive
  split at isLive
  · split at isLive <;> cases isLive
  · rename_i notMarked
    refine ⟨isLive, ?_⟩
    simp only [isLive, decide_true, Bool.true_and, Bool.not_eq_eq_eq_not, Bool.not_true,
      Bool.not_eq_false, List.contains_iff_mem] at notMarked
    simpa using notMarked

/-- A current live history stays as it is. -/
theorem markHistory_of_current (completed : Option (InvocationKey σ))
    (current : List (InvocationKey σ)) (key : InvocationKey σ) (history : InvocationHistory σ)
    (isCurrent : key ∈ current) : markHistory completed current key history = history := by
  unfold markHistory
  simp only [List.contains_iff_mem.mpr isCurrent, Bool.not_true, Bool.and_false,
    Bool.false_eq_true, ↓reduceIte]

/-- A committed marked history was committed, or this commit completed it. -/
theorem markHistory_committed (completed : Option (InvocationKey σ))
    (current : List (InvocationKey σ)) (key : InvocationKey σ) (history : InvocationHistory σ)
    (isCommitted : (markHistory completed current key history).outcome = .committed) :
    history.outcome = .committed ∨ completed = some key := by
  unfold markHistory endHistory at isCommitted
  split at isCommitted
  · split at isCommitted
    · rename_i isCompleted
      exact Or.inr isCompleted
    · cases isCommitted
  · exact Or.inl isCommitted

/-- Lookup after the reconcile keeps the presence of a history. -/
theorem findInvocation?_reconcile_of_some (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) (key : InvocationKey σ) (history : InvocationHistory σ)
    (isFound : ghost.findInvocation? key = some history) :
    (ghost.reconcile completed base).findInvocation? key =
      some (markHistory completed (currentKeys base) key history) := by
  rw [findInvocation?_reconcile, isFound]

/-- Lookup after the reconcile of a key without history. -/
theorem findInvocation?_reconcile_of_none (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) (key : InvocationKey σ) (isAbsent : ghost.findInvocation? key = none) :
    (ghost.reconcile completed base).findInvocation? key =
      if key ∈ currentKeys base then some (InvocationHistory.fresh ghost.normalCount)
      else none := by
  rw [findInvocation?_reconcile, isAbsent]

/-- After the reconcile every live history is current. -/
theorem live_current_reconcile (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) (key : InvocationKey σ) (history : InvocationHistory σ)
    (isFound : (ghost.reconcile completed base).findInvocation? key = some history)
    (isLive : history.outcome = .live) : key ∈ currentKeys base := by
  rw [findInvocation?_reconcile] at isFound
  cases isOld : ghost.findInvocation? key with
  | some old =>
    rw [isOld] at isFound
    cases isFound
    exact (markHistory_live _ _ _ _ isLive).2
  | none =>
    rw [isOld] at isFound
    simp only at isFound
    split at isFound
    · assumption
    · cases isFound

/-! ## The attempt reconcile -/

/-- Lookup in a list of keyed values, by the key of a member, when keys are distinct. -/
theorem get?_filterMap_keyed {α κ ν : Type} [DecidableEq κ] (elements : List α) (key : α → κ)
    (value : α → Option ν) (keysNodup : (elements.map key).Nodup) (element : α)
    (isMember : element ∈ elements) :
    AssocList.get? (elements.filterMap (fun candidate =>
      (value candidate).map (fun found => (key candidate, found)))) (key element) =
      value element := by
  induction elements with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    simp only [List.map_cons, List.nodup_cons] at keysNodup
    rw [List.filterMap_cons]
    rcases List.mem_cons.mp isMember with isHead | isRest
    · subst isHead
      cases value element <;> simp [AssocList.get?]
      have absent : ∀ other, other ∈ rest → key other ≠ key element := by
        intro other isOther isSame
        exact keysNodup.1 (isSame ▸ List.mem_map.mpr ⟨other, isOther, rfl⟩)
      clear inductionHypothesis isMember keysNodup
      induction rest with
      | nil => rfl
      | cons next later laterHypothesis =>
        rw [List.filterMap_cons]
        cases value next with
        | none => exact laterHypothesis (fun other isOther => absent other (List.mem_cons_of_mem _ isOther))
        | some found =>
          simp only [Option.map_some, AssocList.get?]
          simp only [absent next List.mem_cons_self, ↓reduceIte]
          exact laterHypothesis (fun other isOther => absent other (List.mem_cons_of_mem _ isOther))
    · have isOther : key head ≠ key element := by
        intro isSame
        exact keysNodup.1 (isSame ▸ List.mem_map.mpr ⟨element, isRest, rfl⟩)
      cases value head with
      | none => exact inductionHypothesis keysNodup.2 isRest
      | some found =>
        simp only [Option.map_some, AssocList.get?, isOther, ↓reduceIte]
        exact inductionHypothesis keysNodup.2 isRest

/-- The attempt state that `reconcileAttempts` keeps for a Step record. -/
def reconciledAttempt (attempts : AssocList (Dex.StepExecId σ) (Attempt σ))
    (record : Dex.StepRecord σ) : Option (Attempt σ) :=
  match record.invocation with
  | none => none
  | some invocation =>
    match attempts.get? record.id with
    | some attempt =>
      if attempt.method = invocation.method then some attempt
      else some (Attempt.fresh invocation.method)
    | none => some (Attempt.fresh invocation.method)

/-- `reconcileAttempts` as a keyed list. -/
theorem reconcileAttempts_attempts (run : Run σ) :
    run.reconcileAttempts.attempts = run.base.steps.filterMap (fun record =>
      (reconciledAttempt run.attempts record).map (fun found => (record.id, found))) := by
  unfold Run.reconcileAttempts
  show List.filterMap _ _ = _
  congr 1
  funext record
  unfold reconciledAttempt
  cases record.invocation with
  | none => rfl
  | some invocation =>
    simp only
    cases run.attempts.get? record.id with
    | none => rfl
    | some attempt =>
      simp only
      split <;> rfl

/-- Lookup after `reconcileAttempts`, for a Step of the run, when Step IDs
are distinct. -/
theorem findAttempt?_reconcileAttempts (run : Run σ) (record : Dex.StepRecord σ)
    (isMember : record ∈ run.base.steps)
    (idsNodup : (run.base.steps.map (fun record => record.id)).Nodup) :
    run.reconcileAttempts.findAttempt? record.id = reconciledAttempt run.attempts record := by
  unfold Run.findAttempt?
  rw [reconcileAttempts_attempts]
  exact get?_filterMap_keyed run.base.steps (fun record => record.id)
    (reconciledAttempt run.attempts) idsNodup record isMember

/-- After `reconcileAttempts` every attempt state belongs to a Step with an
invocation. -/
theorem mem_reconcileAttempts (run : Run σ) (entry : Dex.StepExecId σ × Attempt σ)
    (isMember : entry ∈ run.reconcileAttempts.attempts) :
    ∃ record, record ∈ run.base.steps ∧ record.id = entry.1 ∧ record.invocation.isSome = true := by
  rw [reconcileAttempts_attempts] at isMember
  obtain ⟨record, isRecord, isEntry⟩ := List.mem_filterMap.mp isMember
  refine ⟨record, isRecord, ?_, ?_⟩
  · cases isFound : reconciledAttempt run.attempts record with
    | none => simp [isFound] at isEntry
    | some found =>
      simp only [isFound, Option.map_some, Option.some.injEq] at isEntry
      rw [← isEntry]
  · cases hasInvocation : record.invocation with
    | none => simp [reconciledAttempt, hasInvocation] at isEntry
    | some invocation => rfl

/-- After `reconcileAttempts` the attempt keys are the IDs of the Steps with
an invocation, so they are distinct when Step IDs are. -/
theorem nodup_reconcileAttempts (run : Run σ)
    (idsNodup : (run.base.steps.map (fun record => record.id)).Nodup) :
    (run.reconcileAttempts.attempts.map (fun entry => entry.1)).Nodup := by
  rw [reconcileAttempts_attempts]
  generalize run.base.steps = steps at idsNodup
  induction steps with
  | nil => simp
  | cons head rest inductionHypothesis =>
    simp only [List.map_cons, List.nodup_cons] at idsNodup
    rw [List.filterMap_cons]
    cases reconciledAttempt run.attempts head with
    | none => exact inductionHypothesis idsNodup.2
    | some found =>
      simp only [Option.map_some, List.map_cons, List.nodup_cons]
      refine ⟨?_, inductionHypothesis idsNodup.2⟩
      intro isMember
      obtain ⟨entry, isEntry, isKey⟩ := List.mem_map.mp isMember
      obtain ⟨record, isRecord, isMapped⟩ := List.mem_filterMap.mp isEntry
      cases isFound : reconciledAttempt run.attempts record with
      | none => simp [isFound] at isMapped
      | some other =>
        simp only [isFound, Option.map_some, Option.some.injEq] at isMapped
        subst isMapped
        exact idsNodup.1 (isKey ▸ List.mem_map.mpr ⟨record, isRecord, rfl⟩)

end DexExpressiveness.DexF.WPF4
