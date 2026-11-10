import DexExpressiveness.Failure.Proofs.WPF1Relabel
import DexExpressiveness.Failure.Relation

/-!
# WP-F1: α as a relabeling, the reconcile steps, and the virtual tasks

`Run.abstract` is `relabelRun` with the view `attemptView`, when attempt
methods match their invocations. After a commit, `reconcileAttempts` and
`Ghost.reconcile` keep that view on every invocation the commit did not
write. The virtual tasks are a sorted list with distinct IDs, so two of them
are equal when they have the same members.
-/

namespace DexExpressiveness.DexF.WPF1

variable {σ : Dex.Signature}

/-! ## The view of α -/

/-- The invocation state α gives `(step, method)`: running as its virtual task
when the attempt of that method holds a snapshot and `par` is set, else
pending. -/
def attemptView (run : Run σ) (ghost : Ghost σ) : InvocationView σ := fun step method =>
  match run.findAttempt? step with
  | some attempt =>
    if attempt.method = method ∧ attempt.snapshot.isSome = true then
      match ghost.argumentsOf (step, method) with
      | some arguments => .running arguments.virtualTaskId
      | none => .pending
    else .pending
  | none => .pending

/-- `Run.abstract` is `relabelRun` with any view that agrees with
`abstractInvocation` on every invocation. -/
theorem abstract_eq_relabelRun (ghost : Ghost σ) (run : Run σ) (view : InvocationView σ)
    (agrees : ∀ record, record ∈ run.base.steps → ∀ invocation,
      record.invocation = some invocation →
      run.abstractInvocation ghost record =
        some { invocation with state := view record.id invocation.method }) :
    Run.abstract ghost run =
      { relabelRun view ghost.normalIndexOf run.base with seq := ghost.normalCount } := by
  simp only [Run.abstract, relabelRun]
  congr 1
  apply List.map_congr_left
  intro record isMember
  simp only [relabelRecord]
  cases hasInvocation : record.invocation with
  | none => simp [Run.abstractInvocation, hasInvocation]
  | some invocation =>
    rw [agrees record isMember invocation hasInvocation]
    rfl

/-- Before a commit: α is the relabeling by the view of the configuration,
when every attempt has the method of its invocation. -/
theorem abstractInvocation_eq_attemptView (ghost : Ghost σ) (run : Run σ)
    (record : Dex.StepRecord σ) (invocation : Dex.Invocation)
    (hasInvocation : record.invocation = some invocation)
    (methodsMatch : ∀ attempt, run.findAttempt? record.id = some attempt →
      attempt.method = invocation.method) :
    run.abstractInvocation ghost record =
      some { invocation with state := attemptView run ghost record.id invocation.method } := by
  unfold Run.abstractInvocation attemptView
  rw [hasInvocation]
  dsimp only
  cases isFound : run.findAttempt? record.id with
  | none => rfl
  | some attempt =>
    have isMethod := methodsMatch attempt isFound
    dsimp only
    cases hasSnapshot : attempt.snapshot.isSome <;>
      cases ghost.argumentsOf (record.id, invocation.method) <;> simp [isMethod]

/-- `get?` of a `filterMap` whose entries are keyed by an injective key. -/
theorem get?_filterMap_of_key {α κ ν : Type} [DecidableEq κ] (entryOf : α → Option (κ × ν))
    (key : α → κ) (keyed : ∀ element entry, entryOf element = some entry → entry.1 = key element)
    (elements : List α) (element : α) (isMember : element ∈ elements)
    (keysNodup : (elements.map key).Nodup) :
    AssocList.get? (elements.filterMap entryOf) (key element) =
      (entryOf element).map (fun entry => entry.2) := by
  have absent : ∀ (rest : List α), (∀ other, other ∈ rest → key other ≠ key element) →
      AssocList.get? (rest.filterMap entryOf) (key element) = none := by
    intro rest notInRest
    induction rest with
    | nil => rfl
    | cons head tail inductionHypothesis =>
      have headOther := notInRest head (List.mem_cons_self ..)
      have tailOther : ∀ other, other ∈ tail → key other ≠ key element :=
        fun other isOther => notInRest other (List.mem_cons_of_mem _ isOther)
      simp only [List.filterMap_cons]
      cases isEntry : entryOf head with
      | none => exact inductionHypothesis tailOther
      | some entry =>
        have isKey := keyed head entry isEntry
        simp only [AssocList.get?]
        rw [ite_eq_right (by rw [isKey]; exact headOther)]
        exact inductionHypothesis tailOther
  induction elements with
  | nil => cases isMember
  | cons head tail inductionHypothesis =>
    rw [List.map_cons, List.nodup_cons] at keysNodup
    simp only [List.filterMap_cons]
    by_cases isHead : head = element
    · subst isHead
      have notInTail : ∀ other, other ∈ tail → key other ≠ key head := by
        intro other isOther sameKey
        exact keysNodup.1 (List.mem_map.mpr ⟨other, isOther, sameKey⟩)
      cases isEntry : entryOf head with
      | none => exact absent tail notInTail
      | some entry =>
        have isKey := keyed head entry isEntry
        simp only [AssocList.get?, isKey, ↓reduceIte, Option.map_some]
    · have inTail : element ∈ tail := by
        rcases List.mem_cons.mp isMember with isSame | isTail
        · exact absurd isSame.symm isHead
        · exact isTail
      have keyOther : key head ≠ key element := by
        intro sameKey
        exact keysNodup.1 (List.mem_map.mpr ⟨element, inTail, sameKey.symm⟩)
      cases isEntry : entryOf head with
      | none => exact inductionHypothesis inTail keysNodup.2
      | some entry =>
        have isKey := keyed head entry isEntry
        simp only [AssocList.get?]
        rw [ite_eq_right (by rw [isKey]; exact keyOther)]
        exact inductionHypothesis inTail keysNodup.2

/-- The attempt state of a Step after `reconcileAttempts`. -/
theorem findAttempt?_reconcileAttempts (run : Run σ) (record : Dex.StepRecord σ)
    (isMember : record ∈ run.base.steps)
    (idsNodup : (run.base.steps.map (fun candidate => candidate.id)).Nodup) :
    run.reconcileAttempts.findAttempt? record.id =
      record.invocation.map (fun invocation =>
        match run.attempts.get? record.id with
        | some attempt =>
          if attempt.method = invocation.method then attempt else Attempt.fresh invocation.method
        | none => Attempt.fresh invocation.method) := by
  unfold Run.reconcileAttempts Run.findAttempt?
  dsimp only
  rw [get?_filterMap_of_key _ (fun candidate => candidate.id) _ run.base.steps record isMember
    idsNodup]
  · cases record.invocation with
    | none => rfl
    | some invocation =>
      dsimp only
      cases run.attempts.get? record.id with
      | none => rfl
      | some attempt =>
        dsimp only
        split <;> simp [*]
  · intro candidate entry isEntry
    cases hasInvocation : candidate.invocation with
    | none => simp [hasInvocation] at isEntry
    | some invocation =>
      simp only [hasInvocation] at isEntry
      cases hasAttempt : run.attempts.get? candidate.id with
      | none =>
        simp only [hasAttempt, Option.some.injEq] at isEntry
        rw [← isEntry]
      | some attempt =>
        simp only [hasAttempt] at isEntry
        split at isEntry <;>
          · simp only [Option.some.injEq] at isEntry
            rw [← isEntry]

/-- After a commit: α of the reconciled run is the relabeling by the view of
the changed run. -/
theorem abstractInvocation_reconcileAttempts (run : Run σ) (ghost : Ghost σ)
    (record : Dex.StepRecord σ) (isMember : record ∈ run.base.steps)
    (idsNodup : (run.base.steps.map (fun candidate => candidate.id)).Nodup)
    (invocation : Dex.Invocation) (hasInvocation : record.invocation = some invocation) :
    run.reconcileAttempts.abstractInvocation ghost record =
      some { invocation with state := attemptView run ghost record.id invocation.method } := by
  have isFound := findAttempt?_reconcileAttempts run record isMember idsNodup
  rw [hasInvocation] at isFound
  unfold Run.abstractInvocation attemptView
  rw [hasInvocation]
  dsimp only
  rw [isFound]
  simp only [Run.findAttempt?, Option.map_some]
  cases hasAttempt : run.attempts.get? record.id with
  | none => simp [Attempt.fresh]
  | some attempt =>
    dsimp only
    by_cases isMethod : attempt.method = invocation.method
    · simp only [isMethod, ↓reduceIte, true_and]
      cases attempt.snapshot.isSome <;>
        cases ghost.argumentsOf (record.id, invocation.method) <;> rfl
    · simp [isMethod, Attempt.fresh]

/-- The view depends on the attempt states only through their methods and
whether they hold a snapshot, and on the ghost only through `par`. -/
theorem attemptView_congr (run other : Run σ) (ghost otherGhost : Ghost σ)
    (sameAttempts : ∀ step, (run.findAttempt? step).map
        (fun attempt => (attempt.method, attempt.snapshot.isSome)) =
      (other.findAttempt? step).map (fun attempt => (attempt.method, attempt.snapshot.isSome)))
    (sameArguments : ∀ key, ghost.argumentsOf key = otherGhost.argumentsOf key) :
    attemptView run ghost = attemptView other otherGhost := by
  funext step method
  unfold attemptView
  have isSame := sameAttempts step
  rw [sameArguments]
  cases isFirst : run.findAttempt? step with
  | none =>
    cases isSecond : other.findAttempt? step with
    | none => rfl
    | some second => rw [isFirst, isSecond] at isSame; cases isSame
  | some first =>
    cases isSecond : other.findAttempt? step with
    | none => rw [isFirst, isSecond] at isSame; cases isSame
    | some second =>
      rw [isFirst, isSecond] at isSame
      simp only [Option.map_some, Option.some.injEq, Prod.mk.injEq] at isSame
      simp only [isSame.1, isSame.2]

/-! ## `Ghost.reconcile` -/

/-- The invocations that are current in a run. -/
def currentKeys (base : Dex.Run σ) : List (InvocationKey σ) :=
  base.steps.filterMap (fun record => record.invocation.map (fun invocation =>
    (record.id, invocation.method)))

/-- How `Ghost.reconcile` marks one history. -/
def markHistory (completed : Option (InvocationKey σ)) (current : List (InvocationKey σ))
    (entry : InvocationKey σ × InvocationHistory σ) : InvocationKey σ × InvocationHistory σ :=
  if entry.2.outcome = .live && !current.contains entry.1 then
    (entry.1, { entry.2 with
      outcome := if completed = some entry.1 then .committed else .canceled })
  else entry

theorem set_of_not_contains {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ)
    (value : ν) (isAbsent : map.contains key = false) : map.set key value = map ++ [(key, value)] := by
  induction map with
  | nil => rfl
  | cons entry rest inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    have isOther : entryKey ≠ key := by
      intro isKey
      simp [AssocList.contains, AssocList.get?, isKey] at isAbsent
    have restAbsent : AssocList.contains rest key = false := by
      simpa [AssocList.contains, AssocList.get?, isOther] using isAbsent
    simp [AssocList.set, isOther, inductionHypothesis restAbsent]

/-- `Ghost.reconcile` marks the histories in place and appends fresh ones. -/
theorem reconcile_invocations (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) :
    ∃ extra, (ghost.reconcile completed base).invocations =
        ghost.invocations.map (markHistory completed (currentKeys base)) ++ extra ∧
      ∀ entry, entry ∈ extra → entry.2 = InvocationHistory.fresh ghost.normalCount := by
  have step : ∀ (keys : List (InvocationKey σ)) (start : AssocList (InvocationKey σ)
      (InvocationHistory σ)) (extra : AssocList (InvocationKey σ) (InvocationHistory σ)),
      (∀ entry, entry ∈ extra → entry.2 = InvocationHistory.fresh ghost.normalCount) →
      ∃ moreExtra, keys.foldl (fun (entries : AssocList (InvocationKey σ) (InvocationHistory σ))
          key => if entries.contains key then entries
            else entries.set key (InvocationHistory.fresh ghost.normalCount)) (start ++ extra) =
        start ++ moreExtra ∧
        ∀ entry, entry ∈ moreExtra → entry.2 = InvocationHistory.fresh ghost.normalCount := by
    intro keys
    induction keys with
    | nil => intro start extra isFresh; exact ⟨extra, rfl, isFresh⟩
    | cons key laterKeys inductionHypothesis =>
      intro start extra isFresh
      simp only [List.foldl_cons]
      by_cases isContained : (start ++ extra).contains key = true
      · rw [ite_eq_left isContained]
        exact inductionHypothesis start extra isFresh
      · rw [ite_eq_right isContained, set_of_not_contains _ _ _ (by simpa using isContained),
          List.append_assoc]
        apply inductionHypothesis
        intro entry isMember
        rcases List.mem_append.mp isMember with isOld | isNew
        · exact isFresh entry isOld
        · simp at isNew
          rw [isNew]
  obtain ⟨extra, isEq, isFresh⟩ := step (currentKeys base)
    (ghost.invocations.map (markHistory completed (currentKeys base))) [] (by simp)
  refine ⟨extra, ?_, isFresh⟩
  rw [List.append_nil] at isEq
  exact isEq

theorem get?_map_of_keeps_key {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν)
    (change : κ × ν → κ × ν) (keepsKey : ∀ entry, (change entry).1 = entry.1) (key : κ) :
    AssocList.get? (map.map change) key =
      (map.find? (fun entry => decide (entry.1 = key))).map (fun entry => (change entry).2) := by
  induction map with
  | nil => rfl
  | cons entry rest inductionHypothesis =>
    rw [List.map_cons, List.find?_cons,
      show change entry = ((change entry).1, (change entry).2) from rfl]
    simp only [AssocList.get?]
    by_cases isWanted : entry.1 = key
    · rw [ite_eq_left ((keepsKey entry).trans isWanted)]
      simp [isWanted]
    · rw [ite_eq_right (by rw [keepsKey]; exact isWanted), inductionHypothesis]
      simp [isWanted]

theorem get?_eq_find? {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ) :
    AssocList.get? map key = (map.find? (fun entry => decide (entry.1 = key))).map (fun entry => entry.2) := by
  simpa using get?_map_of_keeps_key map id (fun _ => rfl) key

theorem get?_append {κ ν : Type} [DecidableEq κ] (first second : AssocList κ ν) (key : κ) :
    AssocList.get? (first ++ second) key =
      match AssocList.get? first key with
      | some value => some value
      | none => AssocList.get? second key := by
  induction first with
  | nil => rfl
  | cons entry rest inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    by_cases isKey : entryKey = key <;> simp [AssocList.get?, isKey, inductionHypothesis]

/-- The history of an invocation after `Ghost.reconcile`: a marked history, a
fresh one, or none. -/
theorem findInvocation?_reconcile (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) (key : InvocationKey σ) :
    (∃ history, ghost.findInvocation? key = some history ∧
      (ghost.reconcile completed base).findInvocation? key =
        some (markHistory completed (currentKeys base) (key, history)).2) ∨
    (ghost.findInvocation? key = none ∧
      ((ghost.reconcile completed base).findInvocation? key = none ∨
        (ghost.reconcile completed base).findInvocation? key =
          some (InvocationHistory.fresh ghost.normalCount))) := by
  obtain ⟨extra, isEq, isFresh⟩ := reconcile_invocations ghost completed base
  unfold Ghost.findInvocation?
  rw [isEq, get?_append]
  have markedKey : ∀ entry, (markHistory completed (currentKeys base) entry).1 = entry.1 := by
    intro entry
    unfold markHistory
    split <;> rfl
  rw [get?_map_of_keeps_key _ _ markedKey, get?_eq_find? ghost.invocations key]
  cases isFound : ghost.invocations.find? (fun entry => decide (entry.1 = key)) with
  | some entry =>
    left
    have isKey : entry.1 = key := by simpa using List.find?_some isFound
    refine ⟨entry.2, rfl, ?_⟩
    obtain ⟨entryKey, entryValue⟩ := entry
    simp only at isKey
    subst isKey
    rfl
  | none =>
    right
    refine ⟨rfl, ?_⟩
    cases isExtra : AssocList.get? extra key with
    | none => left; rfl
    | some history =>
      right
      rw [get?_eq_find?] at isExtra
      cases isExtraFound : extra.find? (fun entry => decide (entry.1 = key)) with
      | none => simp [isExtraFound] at isExtra
      | some entry =>
        simp only [isExtraFound, Option.map_some, Option.some.injEq] at isExtra
        rw [← isExtra, isFresh entry (List.mem_of_find?_eq_some isExtraFound)]
        rfl

/-- `Ghost.reconcile` keeps `par`. -/
theorem argumentsOf_reconcile (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) (key : InvocationKey σ) :
    (ghost.reconcile completed base).argumentsOf key = ghost.argumentsOf key := by
  unfold Ghost.argumentsOf
  rcases findInvocation?_reconcile ghost completed base key with
    ⟨history, isFound, isMarked⟩ | ⟨isAbsent, isNone | isFresh⟩
  · rw [isFound, isMarked]
    simp only [Option.bind_some, markHistory]
    try (split <;> rfl)
  · rw [isAbsent, isNone]
  · rw [isAbsent, isFresh]
    try rfl

/-- `Ghost.reconcile` changes nothing but the histories. -/
@[simp] theorem reconcile_normalCount (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) : (ghost.reconcile completed base).normalCount = ghost.normalCount := rfl

@[simp] theorem reconcile_normalIndex (ghost : Ghost σ) (completed : Option (InvocationKey σ))
    (base : Dex.Run σ) : (ghost.reconcile completed base).normalIndex = ghost.normalIndex := rfl

@[simp] theorem reconcile_lastNormalCommitAt (ghost : Ghost σ)
    (completed : Option (InvocationKey σ)) (base : Dex.Run σ) :
    (ghost.reconcile completed base).lastNormalCommitAt = ghost.lastNormalCommitAt := rfl

@[simp] theorem reconcile_nextVirtualTaskId (ghost : Ghost σ)
    (completed : Option (InvocationKey σ)) (base : Dex.Run σ) :
    (ghost.reconcile completed base).nextVirtualTaskId = ghost.nextVirtualTaskId := rfl

/-- When every live history stays current, `Ghost.reconcile` marks nothing. -/
theorem map_markHistory_of_current (invocations : AssocList (InvocationKey σ) (InvocationHistory σ))
    (completed : Option (InvocationKey σ)) (current : List (InvocationKey σ))
    (staysCurrent : ∀ entry, entry ∈ invocations → entry.2.outcome = .live →
      current.contains entry.1 = true) :
    invocations.map (markHistory completed current) = invocations := by
  conv => rhs; rw [← List.map_id invocations]
  apply List.map_congr_left
  intro entry isMember
  unfold markHistory
  by_cases isLive : entry.2.outcome = .live
  · have isCurrent : entry.1 ∈ current := by simpa using staysCurrent entry isMember isLive
    simp [isLive, isCurrent]
  · simp [isLive]

theorem mem_currentKeys (base : Dex.Run σ) (key : InvocationKey σ) :
    key ∈ currentKeys base ↔ ∃ record invocation, record ∈ base.steps ∧
      record.invocation = some invocation ∧ key = (record.id, invocation.method) := by
  unfold currentKeys
  simp only [List.mem_filterMap]
  constructor
  · rintro ⟨record, isMember, isKey⟩
    cases hasInvocation : record.invocation with
    | none => simp [hasInvocation] at isKey
    | some invocation =>
      simp only [hasInvocation, Option.map_some, Option.some.injEq] at isKey
      exact ⟨record, invocation, isMember, hasInvocation, isKey.symm⟩
  · rintro ⟨record, invocation, isMember, hasInvocation, isKey⟩
    exact ⟨record, isMember, by simp [hasInvocation, isKey]⟩

/-! ## Sorting by ID -/

theorem insertSorted_perm {α : Type} (isAtMost : α → α → Bool) (newElement : α)
    (elements : List α) : (insertSorted isAtMost newElement elements).Perm (newElement :: elements) := by
  induction elements with
  | nil => exact List.Perm.refl _
  | cons head rest inductionHypothesis =>
    simp only [insertSorted]
    split
    · exact List.Perm.refl _
    · exact (List.Perm.cons head inductionHypothesis).trans (List.Perm.swap newElement head rest)

theorem sortStable_perm {α : Type} (isAtMost : α → α → Bool) (elements : List α) :
    (sortStable isAtMost elements).Perm elements := by
  induction elements with
  | nil => exact List.Perm.refl _
  | cons head rest inductionHypothesis =>
    exact (insertSorted_perm isAtMost head _).trans (List.Perm.cons head inductionHypothesis)

/-- A list sorted by a key with distinct keys is strictly sorted. -/
theorem pairwise_lt_of_le_of_nodup {α : Type} (key : α → Nat) (elements : List α)
    (isSorted : elements.Pairwise (fun first second => key first ≤ key second))
    (keysNodup : (elements.map key).Nodup) :
    elements.Pairwise (fun first second => key first < key second) := by
  have isDistinct : elements.Pairwise (fun first second => key first ≠ key second) :=
    List.pairwise_map.mp keysNodup
  exact (isSorted.and isDistinct).imp (fun both => Nat.lt_of_le_of_ne both.1 both.2)

/-- `sortStable` by a key with distinct keys gives a strictly sorted list. -/
theorem sortStable_pairwise_lt {α : Type} (key : α → Nat) (elements : List α)
    (keysNodup : (elements.map key).Nodup) :
    (sortStable (fun first second => decide (key first ≤ key second)) elements).Pairwise
      (fun first second => key first < key second) :=
  pairwise_lt_of_le_of_nodup key _ (sortStable_pairwise_key key elements)
    (((sortStable_perm _ elements).map key).nodup_iff.mpr keysNodup)

/-! ## Virtual tasks -/

/-- The virtual task of one history, as `Config.virtualTasks` builds it. -/
def virtualTaskOf (config : Config σ) (entry : InvocationKey σ × InvocationHistory σ) :
    Option (Dex.Task σ) :=
  match entry.2.arguments with
  | some arguments =>
    if config.hasVirtualTask entry.1 entry.2 then
      some (arguments.toTask entry.1 entry.2.virtualPhase)
    else none
  | none => none

theorem virtualTasks_eq (config : Config σ) :
    config.virtualTasks = sortStable (fun first second => decide (first.id ≤ second.id))
      (config.ghost.invocations.filterMap (virtualTaskOf config)) := rfl

theorem mem_virtualTasks (config : Config σ) (task : Dex.Task σ) :
    task ∈ config.virtualTasks ↔
      ∃ entry, entry ∈ config.ghost.invocations ∧ virtualTaskOf config entry = some task := by
  rw [virtualTasks_eq, mem_sortStable', List.mem_filterMap]

/-- The virtual task IDs, as the invariant `virtualTaskIdsNodup` lists them. -/
def virtualTaskIds (invocations : AssocList (InvocationKey σ) (InvocationHistory σ)) : List Nat :=
  (invocations.filterMap (fun entry => entry.2.arguments)).map
    (fun arguments => arguments.virtualTaskId)

theorem virtualTaskOf_eq_some (config : Config σ) (entry : InvocationKey σ × InvocationHistory σ)
    (task : Dex.Task σ) (isTask : virtualTaskOf config entry = some task) :
    ∃ arguments, entry.2.arguments = some arguments ∧
      config.hasVirtualTask entry.1 entry.2 = true ∧
      task = arguments.toTask entry.1 entry.2.virtualPhase := by
  unfold virtualTaskOf at isTask
  split at isTask
  · rename_i arguments hasArguments
    split at isTask
    · rename_i hasTask
      cases isTask
      exact ⟨arguments, hasArguments, hasTask, rfl⟩
    · cases isTask
  · cases isTask

theorem sublist_virtualTaskIds (config : Config σ)
    (invocations : AssocList (InvocationKey σ) (InvocationHistory σ)) :
    ((invocations.filterMap (virtualTaskOf config)).map (fun task => task.id)).Sublist
      (virtualTaskIds invocations) := by
  induction invocations with
  | nil => exact List.Sublist.slnil
  | cons entry rest inductionHypothesis =>
    unfold virtualTaskIds at inductionHypothesis ⊢
    rw [List.filterMap_cons, List.filterMap_cons]
    cases isTask : virtualTaskOf config entry with
    | none =>
      cases entry.2.arguments with
      | none => exact inductionHypothesis
      | some arguments => exact List.Sublist.cons _ inductionHypothesis
    | some task =>
      obtain ⟨arguments, hasArguments, _, isTaskEq⟩ :=
        virtualTaskOf_eq_some config entry task isTask
      rw [hasArguments, isTaskEq]
      exact List.Sublist.cons_cons _ inductionHypothesis

theorem virtualTasks_pairwise (config : Config σ)
    (idsNodup : (virtualTaskIds config.ghost.invocations).Nodup) :
    config.virtualTasks.Pairwise (fun first second => first.id < second.id) := by
  rw [virtualTasks_eq]
  exact sortStable_pairwise_lt (fun task : Dex.Task σ => task.id) _
    (idsNodup.sublist (sublist_virtualTaskIds config config.ghost.invocations))

/-- Two virtual task lists, each strictly sorted, are equal when they have
the same members. -/
theorem eq_of_same_tasks (first second : List (Dex.Task σ))
    (firstSorted : first.Pairwise (fun a b => a.id < b.id))
    (secondSorted : second.Pairwise (fun a b => a.id < b.id))
    (sameMembers : ∀ task, task ∈ first ↔ task ∈ second) : first = second :=
  eq_of_pairwise_lt_of_mem_iff (fun task => task.id) first second firstSorted secondSorted
    sameMembers

end DexExpressiveness.DexF.WPF1
