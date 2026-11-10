import DexExpressiveness.Failure.Observation
import DexExpressiveness.Dex.RunLemmas

/-!
# WP-F2: failure steps are stutters under α

`failureStep_isStutter_proof`: every failure step other than
[ATTEMPT-EXHAUSTED] maps to no Dex action, produces no projected event, and
leaves `abstractConfig` unchanged.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## List and association-list facts -/

theorem WPF2_any_congr_mem {α : Type} (first second : List α) (predicate : α → Bool)
    (sameMembers : ∀ element, element ∈ first ↔ element ∈ second) :
    first.any predicate = second.any predicate := by
  apply Bool.eq_iff_iff.mpr
  simp only [List.any_eq_true]
  constructor
  · rintro ⟨element, isMember, holds⟩
    exact ⟨element, (sameMembers element).mp isMember, holds⟩
  · rintro ⟨element, isMember, holds⟩
    exact ⟨element, (sameMembers element).mpr isMember, holds⟩

theorem WPF2_get?_of_mem_nodup {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν)
    (keysNodup : (map.map (fun entry => entry.1)).Nodup) (entry : κ × ν)
    (isMember : entry ∈ map) : AssocList.get? map entry.1 = some entry.2 := by
  induction map with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    obtain ⟨key, value⟩ := head
    simp only [List.map_cons, List.nodup_cons, List.mem_map] at keysNodup
    rcases List.mem_cons.mp isMember with isHead | isInRest
    · subst isHead
      simp [AssocList.get?]
    · have isOther : key ≠ entry.1 := by
        intro isSame
        exact keysNodup.1 ⟨entry, isInRest, isSame.symm⟩
      simp [AssocList.get?, isOther, inductionHypothesis keysNodup.2 isInRest]

theorem WPF2_mem_of_get? {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ) (value : ν)
    (isFound : AssocList.get? map key = some value) : ∃ entry, entry ∈ map ∧ entry.1 = key := by
  induction map with
  | nil => simp [AssocList.get?] at isFound
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    by_cases isHead : headKey = key
    · exact ⟨(headKey, headValue), List.mem_cons_self, isHead⟩
    · simp only [AssocList.get?, isHead, ite_false] at isFound
      obtain ⟨entry, isMember, isKey⟩ := inductionHypothesis isFound
      exact ⟨entry, List.mem_cons_of_mem _ isMember, isKey⟩

theorem WPF2_get?_filterMap {α κ ν : Type} [DecidableEq κ] (makeEntry : α → Option (κ × ν))
    (keyOf : α → κ) (entryKey : ∀ element entry, makeEntry element = some entry → entry.1 = keyOf element)
    (elements : List α) (keysNodup : (elements.map keyOf).Nodup) (element : α)
    (isMember : element ∈ elements) :
    AssocList.get? (elements.filterMap makeEntry) (keyOf element) =
      (makeEntry element).map Prod.snd := by
  induction elements with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    simp only [List.map_cons, List.nodup_cons, List.mem_map] at keysNodup
    rcases List.mem_cons.mp isMember with isHead | isInRest
    · subst isHead
      cases isEntry : makeEntry element with
      | none =>
        simp only [List.filterMap_cons, isEntry, Option.map_none]
        cases findsRest : AssocList.get? (rest.filterMap makeEntry) (keyOf element) with
        | none => rfl
        | some value =>
          exfalso
          obtain ⟨entry, isEntryMember, isKey⟩ := WPF2_mem_of_get? _ _ _ findsRest
          obtain ⟨source, isSource, makesEntry⟩ := List.mem_filterMap.mp isEntryMember
          exact keysNodup.1 ⟨source, isSource, (entryKey source entry makesEntry).symm.trans isKey⟩
      | some entry =>
        have sameKey := entryKey element entry isEntry
        obtain ⟨key, value⟩ := entry
        simp only at sameKey
        simp [isEntry, AssocList.get?, sameKey]
    · have isOther : keyOf head ≠ keyOf element := by
        intro isSame
        exact keysNodup.1 ⟨element, isInRest, isSame.symm⟩
      cases isEntry : makeEntry head with
      | none =>
        simp only [List.filterMap_cons, isEntry]
        exact inductionHypothesis keysNodup.2 isInRest
      | some entry =>
        have sameKey := entryKey head entry isEntry
        obtain ⟨key, value⟩ := entry
        simp only at sameKey
        subst sameKey
        simp only [List.filterMap_cons, isEntry, AssocList.get?, isOther, ite_false]
        exact inductionHypothesis keysNodup.2 isInRest

/-! ## Attempt states after `reconcileAttempts` -/

theorem WPF2_findAttempt?_reconcileAttempts (run : Run σ)
    (idsNodup : (run.base.steps.map (fun record => record.id)).Nodup) (record : Dex.StepRecord σ)
    (isMember : record ∈ run.base.steps) (invocation : Dex.Invocation)
    (hasInvocation : record.invocation = some invocation) (attempt : Attempt σ)
    (isAttempt : run.attempts.get? record.id = some attempt)
    (sameMethod : attempt.method = invocation.method) :
    run.reconcileAttempts.findAttempt? record.id = some attempt := by
  unfold Run.findAttempt? Run.reconcileAttempts
  simp only
  rw [WPF2_get?_filterMap _ (fun record => record.id) ?_ _ idsNodup record isMember]
  · simp [hasInvocation, isAttempt, sameMethod]
  · intro element entry makesEntry
    cases isInvocation : element.invocation with
    | none => simp [isInvocation] at makesEntry
    | some current =>
      cases isCurrent : run.attempts.get? element.id with
      | none =>
        simp [isInvocation, isCurrent] at makesEntry
        rw [← makesEntry]
      | some found =>
        by_cases isSame : found.method = current.method
        · simp [isInvocation, isCurrent, isSame] at makesEntry
          rw [← makesEntry]
        · simp [isInvocation, isCurrent, isSame] at makesEntry
          rw [← makesEntry]

/-! ## `Ghost.reconcile` of an unchanged set of current invocations -/

/-- The current invocations of a run, as `Ghost.reconcile` lists them. -/
def WPF2_currentKeys (base : Dex.Run σ) : List (InvocationKey σ) :=
  base.steps.filterMap (fun record => record.invocation.map (fun invocation =>
    (record.id, invocation.method)))

theorem WPF2_foldl_set_of_contains {κ ν : Type} [DecidableEq κ] (fresh : ν) (keys : List κ)
    (entries : AssocList κ ν) (contained : ∀ key, key ∈ keys → AssocList.contains entries key = true) :
    keys.foldl (fun (current : AssocList κ ν) key =>
      if AssocList.contains current key then current else AssocList.set current key fresh) entries =
      entries := by
  induction keys with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    simp only [List.foldl_cons, contained head List.mem_cons_self, ite_true]
    exact inductionHypothesis (fun key isMember => contained key (List.mem_cons_of_mem _ isMember))

theorem WPF2_reconcile_eq (ghost : Ghost σ) (base : Dex.Run σ)
    (keysNodup : (ghost.invocations.map (fun entry => entry.1)).Nodup)
    (liveIsCurrent : ∀ key history, ghost.findInvocation? key = some history →
      history.outcome = .live →
      ∃ record state, record ∈ base.steps ∧ record.id = key.1 ∧
        record.invocation = some { method := key.2, state := state })
    (currentHasHistory : ∀ record invocation, record ∈ base.steps →
      record.invocation = some invocation →
      ∃ history, ghost.findInvocation? (record.id, invocation.method) = some history) :
    ghost.reconcile none base = ghost := by
  have isCurrent : ∀ entry, entry ∈ ghost.invocations → entry.2.outcome = .live →
      entry.1 ∈ WPF2_currentKeys base := by
    intro entry isMember isLive
    obtain ⟨record, state, isRecord, sameId, hasInvocation⟩ :=
      liveIsCurrent entry.1 entry.2 (WPF2_get?_of_mem_nodup _ keysNodup entry isMember) isLive
    unfold WPF2_currentKeys
    refine List.mem_filterMap.mpr ⟨record, isRecord, ?_⟩
    rw [hasInvocation, sameId]
    rfl
  have marked : ghost.invocations.map (fun entry =>
      if (entry.2.outcome = .live && !(WPF2_currentKeys base).contains entry.1) = true then
        (entry.1, { entry.2 with
          outcome := if (none : Option (InvocationKey σ)) = some entry.1 then .committed
            else .canceled })
      else entry) = ghost.invocations := by
    conv => rhs; rw [← List.map_id ghost.invocations]
    apply List.map_congr_left
    intro entry isMember
    by_cases isLive : entry.2.outcome = .live
    · have isIn := isCurrent entry isMember isLive
      simp [isIn]
    · simp [isLive]
  have contained : ∀ key, key ∈ WPF2_currentKeys base →
      AssocList.contains ghost.invocations key = true := by
    intro key isMember
    unfold WPF2_currentKeys at isMember
    obtain ⟨record, isRecord, makesKey⟩ := List.mem_filterMap.mp isMember
    cases hasInvocation : record.invocation with
    | none => simp [hasInvocation] at makesKey
    | some invocation =>
      simp only [hasInvocation, Option.map_some, Option.some.injEq] at makesKey
      obtain ⟨history, isHistory⟩ := currentHasHistory record invocation isRecord hasInvocation
      rw [← makesKey]
      unfold AssocList.contains
      unfold Ghost.findInvocation? at isHistory
      rw [isHistory]
      rfl
  unfold Ghost.reconcile
  simp only
  rw [show (base.steps.filterMap (fun record => record.invocation.map (fun invocation =>
      (record.id, invocation.method)))) = WPF2_currentKeys base from rfl, marked,
    WPF2_foldl_set_of_contains _ _ _ contained]

/-! ## Congruence of α -/

theorem WPF2_filterMap_congr {α β : Type} (first second : α → Option β) (elements : List α)
    (agree : ∀ element, element ∈ elements → first element = second element) :
    elements.filterMap first = elements.filterMap second := by
  induction elements with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    simp only [List.filterMap_cons, agree head List.mem_cons_self,
      inductionHypothesis (fun element isMember => agree element (List.mem_cons_of_mem _ isMember))]

theorem WPF2_abstractInvocation_congr (first second : Run σ) (ghost : Ghost σ)
    (firstRecord secondRecord : Dex.StepRecord σ) (sameId : secondRecord.id = firstRecord.id)
    (sameMethod : secondRecord.invocation.map (fun invocation => invocation.method) =
      firstRecord.invocation.map (fun invocation => invocation.method))
    (sameSnapshot : ∀ invocation, firstRecord.invocation = some invocation →
      (second.findAttempt? secondRecord.id).map (fun attempt => attempt.snapshot.isSome) =
        (first.findAttempt? firstRecord.id).map (fun attempt => attempt.snapshot.isSome)) :
    second.abstractInvocation ghost secondRecord = first.abstractInvocation ghost firstRecord := by
  unfold Run.abstractInvocation
  cases isFirst : firstRecord.invocation with
  | none =>
    cases isSecond : secondRecord.invocation with
    | none => rfl
    | some secondInvocation => simp [isFirst, isSecond] at sameMethod
  | some firstInvocation =>
    cases isSecond : secondRecord.invocation with
    | none => simp [isFirst, isSecond] at sameMethod
    | some secondInvocation =>
      simp only [isFirst, isSecond, Option.map_some, Option.some.injEq] at sameMethod
      have snapshots := sameSnapshot firstInvocation isFirst
      rw [sameId] at snapshots ⊢
      simp only [sameMethod]
      cases isFirstAttempt : first.findAttempt? firstRecord.id with
      | none =>
        cases isSecondAttempt : second.findAttempt? firstRecord.id with
        | none => rfl
        | some secondAttempt => simp [isFirstAttempt, isSecondAttempt] at snapshots
      | some firstAttempt =>
        cases isSecondAttempt : second.findAttempt? firstRecord.id with
        | none => simp [isFirstAttempt, isSecondAttempt] at snapshots
        | some secondAttempt =>
          simp only [isFirstAttempt, isSecondAttempt, Option.map_some,
            Option.some.injEq] at snapshots
          simp only [snapshots]

theorem WPF2_abstract_eq (ghost : Ghost σ) (run after : Run σ)
    (transform : Dex.StepRecord σ → Dex.StepRecord σ) (seq : Nat)
    (isBase : after.base = { run.base with seq := seq, steps := run.base.steps.map transform })
    (onlyInvocation : ∀ record, record ∈ run.base.steps →
      transform record = { record with invocation := (transform record).invocation })
    (sameInvocation : ∀ record, record ∈ run.base.steps →
      after.abstractInvocation ghost (transform record) = run.abstractInvocation ghost record) :
    Run.abstract ghost after = Run.abstract ghost run := by
  unfold Run.abstract
  rw [isBase]
  simp only [List.map_map]
  congr 1
  apply List.map_congr_left
  intro record isMember
  simp only [Function.comp]
  rw [sameInvocation record isMember, onlyInvocation record isMember]

theorem WPF2_abstractConfig_eq (first second : Config σ) (sameClock : second.clock = first.clock)
    (sameGhost : second.ghost = first.ghost)
    (sameRun : second.run.map (Run.abstract first.ghost) = first.run.map (Run.abstract first.ghost))
    (sameTasks : ∀ entry, entry ∈ first.ghost.invocations → entry.2.outcome = .canceled →
      second.realTasks.any (fun task => decide (task.key = entry.1)) =
        first.realTasks.any (fun task => decide (task.key = entry.1))) :
    abstractConfig second = abstractConfig first := by
  unfold abstractConfig Config.virtualTasks
  rw [sameClock, sameGhost, sameRun]
  congr 2
  apply WPF2_filterMap_congr
  intro entry isMember
  cases entry.2.arguments with
  | none => rfl
  | some arguments =>
    unfold Config.hasVirtualTask
    cases isOutcome : entry.2.outcome with
    | live => rfl
    | committed => rfl
    | canceled => simp only [sameTasks entry isMember isOutcome]

/-! ## Seq-taking failure commits -/

/-- The attempt state and live history that the invariant gives a Step with an invocation. -/
theorem WPF2_attempt_of_invariant (program : Program σ) (config : Config σ) (run : Run σ)
    (invariant : Invariant program config) (isRun : config.run = some run)
    (record : Dex.StepRecord σ) (isMember : record ∈ run.base.steps) (invocation : Dex.Invocation)
    (hasInvocation : record.invocation = some invocation) :
    ∃ attempt history, run.findAttempt? record.id = some attempt ∧
      config.ghost.findInvocation? (record.id, invocation.method) = some history ∧
      history.outcome = .live ∧ attempt.method = invocation.method ∧
      (∀ taskId, invocation.state = .running taskId → attempt.snapshot.isSome = true) ∧
      (attempt.snapshot.isSome = true → history.arguments.isSome = true) := by
  have isMatched := (invariant.runInvariant run isRun).attemptsMatch record isMember
  unfold AttemptMatchesRecord at isMatched
  rw [hasInvocation] at isMatched
  obtain ⟨attempt, history, isAttempt, isHistory, isLive, sameMethod, _, isRunning, snapshots, _⟩ :=
    isMatched
  refine ⟨attempt, history, isAttempt, isHistory, isLive, sameMethod,
    fun taskId isState => (isRunning taskId isState).2, ?_⟩
  intro hasSnapshot
  cases isSnapshot : attempt.snapshot with
  | none => simp [isSnapshot] at hasSnapshot
  | some snapshot =>
    cases isArguments : history.arguments with
    | none => simp [isSnapshot, isArguments] at snapshots
    | some arguments => rfl

theorem WPF2_commitRun_stutter (program : Program σ) (config moved : Config σ) (run : Run σ)
    (change : Run σ → Run σ) (transform : Dex.StepRecord σ → Dex.StepRecord σ)
    (invariant : Invariant program config) (isRun : config.run = some run)
    (movedGhost : moved.ghost = config.ghost) (movedClock : moved.clock = config.clock)
    (movedTasks : ∀ entry, entry ∈ config.ghost.invocations → entry.2.outcome = .canceled →
      moved.realTasks.any (fun task => decide (task.key = entry.1)) =
        config.realTasks.any (fun task => decide (task.key = entry.1)))
    (isBase : (change { run with base := { run.base with seq := run.base.seq + 1 } }).base =
      { run.base with seq := run.base.seq + 1, steps := run.base.steps.map transform })
    (onlyInvocation : ∀ record, record ∈ run.base.steps →
      transform record = { record with invocation := (transform record).invocation })
    (sameMethod : ∀ record, record ∈ run.base.steps →
      (transform record).invocation.map (fun invocation => invocation.method) =
        record.invocation.map (fun invocation => invocation.method))
    (sameAttempt : ∀ record, record ∈ run.base.steps → ∀ invocation,
      record.invocation = some invocation → ∀ attempt, run.findAttempt? record.id = some attempt →
      attempt.method = invocation.method →
      ∃ changedAttempt,
        (change { run with base := { run.base with seq := run.base.seq + 1 } }).attempts.get?
            record.id = some changedAttempt ∧
          changedAttempt.method = attempt.method ∧
          changedAttempt.snapshot.isSome = attempt.snapshot.isSome) :
    (moved.commitRun run false none [] change).1 = [] ∧
      abstractConfig (moved.commitRun run false none [] change).2 = abstractConfig config := by
  have runInvariant := invariant.runInvariant run isRun
  generalize isChanged : change { run with base := { run.base with seq := run.base.seq + 1 } } =
    changed at isBase sameAttempt
  have sameId : ∀ record, record ∈ run.base.steps → (transform record).id = record.id := by
    intro record isMember
    rw [onlyInvocation record isMember]
  have changedIdsNodup : (changed.base.steps.map (fun record => record.id)).Nodup := by
    rw [isBase]
    simp only [List.map_map]
    have sameIds : run.base.steps.map ((fun record => record.id) ∘ transform) =
        run.base.steps.map (fun record => record.id) :=
      List.map_congr_left (fun record isMember => sameId record isMember)
    rw [sameIds]
    exact runInvariant.stepIdsNodup
  have transformedInvocation : ∀ record, record ∈ run.base.steps → ∀ invocation,
      (transform record).invocation = some invocation →
      ∃ original, record.invocation = some original ∧ original.method = invocation.method := by
    intro record isMember invocation hasInvocation
    have methods := sameMethod record isMember
    rw [hasInvocation] at methods
    cases isOriginal : record.invocation with
    | none => simp [isOriginal] at methods
    | some original =>
      simp only [isOriginal, Option.map_some, Option.some.injEq] at methods
      exact ⟨original, rfl, methods.symm⟩
  have sameGhost : config.ghost.reconcile none changed.reconcileAttempts.base = config.ghost := by
    apply WPF2_reconcile_eq _ _ invariant.ghostKeysNodup
    · intro key history isHistory isLive
      obtain ⟨record, state, isMember, isId, hasInvocation⟩ :=
        runInvariant.liveIsCurrent key history isHistory isLive
      have methods := sameMethod record isMember
      rw [hasInvocation] at methods
      cases isTransformed : (transform record).invocation with
      | none => simp [isTransformed] at methods
      | some transformed =>
        simp only [isTransformed, Option.map_some, Option.some.injEq] at methods
        refine ⟨transform record, transformed.state, ?_, ?_, ?_⟩
        · show transform record ∈ changed.base.steps
          rw [isBase]
          exact List.mem_map_of_mem isMember
        · rw [sameId record isMember, isId]
        · rw [isTransformed, ← methods]
    · intro transformed invocation isMember hasInvocation
      have isMember' : transformed ∈ changed.base.steps := isMember
      rw [isBase] at isMember'
      obtain ⟨record, isRecord, isTransformed⟩ := List.mem_map.mp isMember'
      subst isTransformed
      obtain ⟨original, hasOriginal, methods⟩ :=
        transformedInvocation record isRecord invocation hasInvocation
      obtain ⟨_, history, _, isHistory, _⟩ :=
        WPF2_attempt_of_invariant program config run invariant isRun record isRecord original
          hasOriginal
      rw [sameId record isRecord, ← methods]
      exact ⟨history, isHistory⟩
  have sameRun : Run.abstract config.ghost changed.reconcileAttempts =
      Run.abstract config.ghost run := by
    apply WPF2_abstract_eq config.ghost run changed.reconcileAttempts transform (run.base.seq + 1)
      isBase onlyInvocation
    intro record isMember
    apply WPF2_abstractInvocation_congr run changed.reconcileAttempts config.ghost record
      (transform record) (sameId record isMember) (sameMethod record isMember)
    intro invocation hasInvocation
    obtain ⟨attempt, _, isAttempt, _, _, attemptMethod, _, _⟩ :=
      WPF2_attempt_of_invariant program config run invariant isRun record isMember invocation
        hasInvocation
    obtain ⟨changedAttempt, isChangedAttempt, changedMethod, changedSnapshot⟩ :=
      sameAttempt record isMember invocation hasInvocation attempt isAttempt attemptMethod
    have methods := sameMethod record isMember
    rw [hasInvocation] at methods
    cases isTransformed : (transform record).invocation with
    | none => simp [isTransformed] at methods
    | some transformed =>
      simp only [isTransformed, Option.map_some, Option.some.injEq] at methods
      have transformedMember : transform record ∈ changed.base.steps := by
        rw [isBase]
        exact List.mem_map_of_mem isMember
      have found := WPF2_findAttempt?_reconcileAttempts changed changedIdsNodup (transform record)
        transformedMember transformed isTransformed changedAttempt
        (by rw [sameId record isMember]; exact isChangedAttempt)
        (by rw [changedMethod, attemptMethod, methods])
      rw [found, isAttempt]
      simp [changedSnapshot]
  have sameStatus : changed.reconcileAttempts.base.status = run.base.status := by
    show changed.base.status = run.base.status
    rw [isBase]
  unfold Config.commitRun
  simp only [isChanged, Bool.false_eq_true, ite_false]
  constructor
  · unfold Config.closeEvents
    rw [sameStatus]
    cases run.base.status <;> rfl
  · apply WPF2_abstractConfig_eq
    · exact movedClock
    · rw [movedGhost]; exact sameGhost
    · rw [isRun]
      simp only [Option.map_some]
      rw [sameRun]
    · exact movedTasks

end DexExpressiveness.DexF
