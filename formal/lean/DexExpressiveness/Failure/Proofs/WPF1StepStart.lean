import DexExpressiveness.Failure.Proofs.WPF1Steps

/-!
# WP-F1: [STEP-START] that computes a snapshot

The commit runs the invocation as a fresh task and sets `par`. Under α the
Step's invocation turns from pending to running as the new virtual task, whose
ID is the next virtual task ID, and the virtual task joins the end of `Θ`.
-/

namespace DexExpressiveness.DexF.WPF1

variable {σ : Dex.Signature}

/-! ## Association lists -/

theorem stepStart_set_append {κ ν : Type} [DecidableEq κ] (map rest : AssocList κ ν) (key : κ)
    (value : ν) (isBound : (AssocList.get? map key).isSome = true) :
    AssocList.set (map ++ rest) key value = AssocList.set map key value ++ rest := by
  induction map with
  | nil => simp [AssocList.get?] at isBound
  | cons entry later inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    by_cases isKey : entryKey = key
    · simp [AssocList.set, isKey]
    · simp only [AssocList.get?, isKey, ↓reduceIte] at isBound
      simp [AssocList.set, isKey, inductionHypothesis isBound]

theorem stepStart_mem_set_self {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ)
    (value : ν) (isBound : (AssocList.get? map key).isSome = true) :
    (key, value) ∈ AssocList.set map key value := by
  induction map with
  | nil => simp [AssocList.get?] at isBound
  | cons entry later inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    by_cases isKey : entryKey = key
    · simp [AssocList.set, isKey]
    · simp only [AssocList.get?, isKey, ↓reduceIte] at isBound
      simp only [AssocList.set, isKey, ↓reduceIte]
      exact List.mem_cons_of_mem _ (inductionHypothesis isBound)

theorem stepStart_mem_set_of_ne {κ ν : Type} [DecidableEq κ] (map : AssocList κ ν) (key : κ)
    (value : ν) (entry : κ × ν) (isMember : entry ∈ map) (isOther : entry.1 ≠ key) :
    entry ∈ AssocList.set map key value := by
  induction map with
  | nil => cases isMember
  | cons head later inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    by_cases isKey : headKey = key
    · simp only [AssocList.set, isKey, ↓reduceIte]
      rcases List.mem_cons.mp isMember with isHead | inLater
      · subst isHead
        exact absurd isKey isOther
      · exact List.mem_cons_of_mem _ inLater
    · simp only [AssocList.set, isKey, ↓reduceIte]
      rcases List.mem_cons.mp isMember with isHead | inLater
      · rw [isHead]
        exact List.mem_cons_self ..
      · exact List.mem_cons_of_mem _ (inductionHypothesis inLater)

/-- Setting `par` of one history that had none adds its virtual task ID. -/
theorem stepStart_virtualTaskIds_set (invocations : AssocList (InvocationKey σ) (InvocationHistory σ))
    (key : InvocationKey σ) (history newHistory : InvocationHistory σ) (arguments : Arguments σ)
    (isFound : AssocList.get? invocations key = some history) (noArguments : history.arguments = none)
    (newArguments : newHistory.arguments = some arguments) :
    (virtualTaskIds (AssocList.set invocations key newHistory)).Perm
      (arguments.virtualTaskId :: virtualTaskIds invocations) := by
  induction invocations with
  | nil => cases isFound
  | cons entry later inductionHypothesis =>
    obtain ⟨entryKey, entryValue⟩ := entry
    by_cases isKey : entryKey = key
    · simp only [AssocList.get?, isKey, ↓reduceIte, Option.some.injEq] at isFound
      subst isFound
      simp [AssocList.set, isKey, virtualTaskIds, noArguments, newArguments]
    · simp only [AssocList.get?, isKey, ↓reduceIte] at isFound
      have isLater := inductionHypothesis isFound
      simp only [AssocList.set, isKey, ↓reduceIte]
      unfold virtualTaskIds at isLater ⊢
      simp only [List.filterMap_cons]
      cases hasArguments : entryValue.arguments with
      | none => exact isLater
      | some entryArguments =>
        simp only [List.map_cons]
        exact (List.Perm.cons _ isLater).trans (List.Perm.swap _ _ _)

/-! ## Relabeling -/

/-- Running an invocation commutes with a relabeling whose view runs it as `virtualTaskId`. -/
theorem stepStart_relabel_updateStep (view otherView : InvocationView σ) (relabel : Nat → Nat)
    (run : Dex.Run σ) (step : Dex.StepExecId σ) (method : Dex.Method) (taskId virtualTaskId : Nat)
    (sameOther : ∀ other otherMethod, other ≠ step → view other otherMethod = otherView other otherMethod)
    (atStep : view step method = .running virtualTaskId) :
    relabelRun view relabel (run.updateStep step (fun record =>
        { record with invocation := some { method := method, state := .running taskId } })) =
      (relabelRun otherView relabel run).updateStep step (fun record =>
        { record with invocation := some { method := method, state := .running virtualTaskId } }) := by
  simp only [relabelRun, Dex.Run.updateStep, List.map_map]
  congr 1
  apply List.map_congr_left
  intro record _
  by_cases isStep : record.id = step
  · simp [isStep, relabelRecord, atStep]
  · simp only [Function.comp, isStep, ↓reduceIte, relabelRecord_id]
    simp only [relabelRecord]
    congr 1
    cases record.invocation with
    | none => rfl
    | some invocation => simp [sameOther _ _ isStep]

theorem stepStart_findStep?_abstract (ghost : Ghost σ) (run : Run σ) (step : Dex.StepExecId σ) :
    (Run.abstract ghost run).findStep? step =
      (run.base.findStep? step).map (fun record =>
        { record with
          invocation := run.abstractInvocation ghost record
          waitState := record.waitState.map (fun waitState =>
            { waitState with registeredSeq := ghost.normalIndexOf waitState.registeredSeq }) }) := by
  simp only [Dex.Run.findStep?, Run.abstract, List.find?_map]
  rfl

theorem stepStart_updateInvocation (ghost : Ghost σ) (key : InvocationKey σ)
    (change : InvocationHistory σ → InvocationHistory σ) (history : InvocationHistory σ)
    (isFound : ghost.findInvocation? key = some history) :
    ghost.updateInvocation key change =
      { ghost with invocations := ghost.invocations.set key (change history) } := by
  unfold Ghost.updateInvocation
  rw [isFound]

/-! ## The rule's result -/

/-- The snapshot the started attempt holds. -/
def startSnapshot (run : Run σ) (record : Dex.StepRecord σ) (attempt : Attempt σ)
    (method : Dex.Method) : Dex.Snapshot σ :=
  attempt.snapshot.getD (run.base.snapshot record.options method)

/-- The handler context of the started attempt. -/
def startContext (stepId : Dex.StepExecId σ) (record : Dex.StepRecord σ) : Dex.Context σ :=
  { stepExecutionId := stepId, fromStepExecutionId := record.origin }

/-- The attempt state after the start. -/
def startedAttempt (config : Config σ) (run : Run σ) (record : Dex.StepRecord σ)
    (attempt : Attempt σ) (method : Dex.Method) : Attempt σ :=
  { attempt with
    snapshot := some (startSnapshot run record attempt method)
    firstAttemptTimestamp := some (attempt.context config.clock).firstAttemptTimestamp }

/-- The change of the start commit. -/
def startChange (config : Config σ) (run : Run σ) (stepId : Dex.StepExecId σ)
    (record : Dex.StepRecord σ) (attempt : Attempt σ) (method : Dex.Method) : Run σ → Run σ :=
  fun current =>
    { (current.setAttempt stepId (startedAttempt config run record attempt method)) with
      base := current.base.updateStep stepId (fun currentRecord =>
        { currentRecord with
          invocation := some { method := method, state := .running config.nextTaskId } }) }

/-- The start commit. -/
def startCommitted (config : Config σ) (run : Run σ) (stepId : Dex.StepExecId σ)
    (record : Dex.StepRecord σ) (attempt : Attempt σ) (method : Dex.Method) :
    List (Obs σ) × Config σ :=
  config.commitRun run true none [] (startChange config run stepId record attempt method)

/-- The real task handed to the Worker. -/
def startTask (config : Config σ) (run : Run σ) (stepId : Dex.StepExecId σ)
    (record : Dex.StepRecord σ) (attempt : Attempt σ) (method : Dex.Method) : Task σ :=
  { base :=
      { id := config.nextTaskId, step := stepId, method := method,
        context := startContext stepId record, input := record.input,
        snapshot := startSnapshot run record attempt method,
        conditionResults := record.conditionResults, phase := .started }
    attemptContext := attempt.context config.clock
    emittedCount := 0 }

/-- `par` set by the start. -/
def startArguments (run : Run σ) (stepId : Dex.StepExecId σ) (record : Dex.StepRecord σ)
    (attempt : Attempt σ) (method : Dex.Method) (virtualTaskId : Nat) : Arguments σ :=
  { context := startContext stepId record, input := record.input,
    snapshot := startSnapshot run record attempt method,
    conditionResults := record.conditionResults, virtualTaskId := virtualTaskId }

/-- The ghost after the start. -/
def startGhost (ghost : Ghost σ) (key : InvocationKey σ) (arguments : Arguments σ) : Ghost σ :=
  { (ghost.updateInvocation key (fun history => { history with arguments := some arguments })) with
    nextVirtualTaskId := ghost.nextVirtualTaskId + 1 }

/-- The configuration after the start. -/
def startNext (config : Config σ) (run : Run σ) (stepId : Dex.StepExecId σ)
    (record : Dex.StepRecord σ) (attempt : Attempt σ) (method : Dex.Method) : Config σ :=
  { (startCommitted config run stepId record attempt method).2 with
    tasks := config.tasks ++ [startTask config run stepId record attempt method]
    nextTaskId := config.nextTaskId + 1
    ghost := startGhost (startCommitted config run stepId record attempt method).2.ghost
      (stepId, method)
      (startArguments run stepId record attempt method
        (startCommitted config run stepId record attempt method).2.ghost.nextVirtualTaskId) }

theorem stepStart_core (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (run : Run σ) (hasRun : config.run = some run)
    (canCommit : config.canCommit = true) (stepId : Dex.StepExecId σ) (record : Dex.StepRecord σ)
    (attempt : Attempt σ) (method : Dex.Method)
    (isRecord : run.base.findStep? stepId = some record)
    (isAttempt : run.findAttempt? stepId = some attempt)
    (hasInvocation : record.invocation = some { method := method, state := .pending })
    (isMethod : attempt.method = method) (noSnapshot : attempt.snapshot = none) :
    Dex.step program.failureFreeFlow (abstractConfig config) (.stepStart stepId) =
      some (eraseStepEvents config (startCommitted config run stepId record attempt method).1,
        abstractConfig (startNext config run stepId record attempt method)) := by
  have runInvariant := invariant.runInvariant run hasRun
  obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? _ _ _ isRecord
  subst isId
  -- The history of the invocation: live, without `par`, not run.
  have isMatch := runInvariant.attemptsMatch record isMember
  unfold AttemptMatchesRecord at isMatch
  rw [hasInvocation] at isMatch
  obtain ⟨found, history, isFound, isHistory, isLive, -, -, -, snapshotMatch, -⟩ := isMatch
  rw [isAttempt] at isFound
  cases isFound
  have noArguments : history.arguments = none := by
    cases hasArguments : history.arguments with
    | none => rfl
    | some _ => simp [noSnapshot, hasArguments] at snapshotMatch
  have isNotRun : history.progress = .notRun := by
    have isConsistent := invariant.historiesConsistent _
      (mem_of_get? _ _ _ isHistory)
    unfold HistoryIsConsistent at isConsistent
    simp only [noArguments] at isConsistent
    cases hasProgress : history.progress with
    | notRun => rfl
    | ran _ _ => simp [hasProgress, VirtualProgress.isNotRun] at isConsistent
    | returned _ => simp [hasProgress, VirtualProgress.isNotRun] at isConsistent
  have snapshotEq : startSnapshot run record attempt method =
      run.base.snapshot record.options method := by
    simp [startSnapshot, noSnapshot]
  have idsNodup := runInvariant.stepIdsNodup
  -- Names for the commit.
  let changed := startChange config run record.id record attempt method (bumpedRun run)
  let changeRecord : Dex.StepRecord σ → Dex.StepRecord σ := fun currentRecord =>
    { currentRecord with
      invocation := some { method := method, state := .running config.nextTaskId } }
  have changedBase : changed.base = (bumpedRun run).base.updateStep record.id changeRecord := rfl
  have changedNodup : (changed.base.steps.map (fun candidate => candidate.id)).Nodup := by
    rw [changedBase, Dex.Run.ids_updateStep _ record.id changeRecord (fun _ => rfl)]
    exact idsNodup
  -- Every live history stays current, so `Ghost.reconcile` marks nothing.
  have staysCurrent : ∀ entry, entry ∈ config.ghost.invocations → entry.2.outcome = .live →
      (currentKeys changed.base).contains entry.1 = true := by
    intro entry isEntry isEntryLive
    obtain ⟨other, otherInvocation, isOther, isOtherId, hasOther, isOtherMethod⟩ :=
      live_isCurrent invariant runInvariant entry isEntry isEntryLive
    have isCurrent : entry.1 ∈ currentKeys changed.base := by
      apply (mem_currentKeys _ _).mpr
      by_cases isStep : other.id = record.id
      · have isSame : other = record :=
          eq_of_nodup_map (fun candidate => candidate.id) run.base.steps idsNodup isOther
            isMember isStep
        subst isSame
        rw [hasInvocation] at hasOther
        cases hasOther
        refine ⟨changeRecord other, { method := method, state := .running config.nextTaskId },
          ?_, rfl, ?_⟩
        · rw [changedBase]
          exact (Dex.Run.mem_updateStep _ _ _ _).mpr ⟨other, isOther, by simp⟩
        · exact Prod.ext isOtherId.symm isOtherMethod.symm
      · refine ⟨other, otherInvocation, ?_, hasOther, ?_⟩
        · rw [changedBase]
          exact (Dex.Run.mem_updateStep _ _ _ _).mpr ⟨other, isOther, by simp [isStep]⟩
        · exact Prod.ext isOtherId.symm isOtherMethod.symm
    simpa using isCurrent
  obtain ⟨extra, isInvocations, isFresh⟩ := reconcile_invocations
    (countedGhost config.ghost (run.base.seq + 1) config.clock) none changed.base
  rw [map_markHistory_of_current (countedGhost config.ghost (run.base.seq + 1) config.clock).invocations
    none _ staysCurrent] at isInvocations
  have isOldHistory : AssocList.get? config.ghost.invocations (record.id, method) = some history :=
    isHistory
  have committedHistory : ((countedGhost config.ghost (run.base.seq + 1) config.clock).reconcile
      none changed.base).findInvocation? (record.id, method) = some history := by
    unfold Ghost.findInvocation?
    rw [isInvocations, get?_append]
    have isCounted : AssocList.get? (countedGhost config.ghost (run.base.seq + 1)
        config.clock).invocations (record.id, method) = some history := isHistory
    rw [isCounted]
  -- The ghost after the step.
  let arguments := startArguments run record.id record attempt method config.ghost.nextVirtualTaskId
  let newHistory : InvocationHistory σ := { history with arguments := some arguments }
  let next := startNext config run record.id record attempt method
  have nextGhost : next.ghost =
      { (countedGhost config.ghost (run.base.seq + 1) config.clock).reconcile none changed.base with
        invocations := AssocList.set ((countedGhost config.ghost (run.base.seq + 1)
          config.clock).reconcile none changed.base).invocations (record.id, method) newHistory
        nextVirtualTaskId := config.ghost.nextVirtualTaskId + 1 } := by
    show startGhost _ (record.id, method) _ = _
    unfold startGhost
    rw [stepStart_updateInvocation (startCommitted config run record.id record attempt method).2.ghost _ _ history committedHistory]
    rfl
  have nextInvocations : next.ghost.invocations =
      AssocList.set config.ghost.invocations (record.id, method) newHistory ++ extra := by
    rw [nextGhost]
    show AssocList.set ((countedGhost config.ghost (run.base.seq + 1) config.clock).reconcile none
      changed.base).invocations (record.id, method) newHistory = _
    rw [isInvocations]
    exact stepStart_set_append _ _ _ _ (by show (AssocList.get? config.ghost.invocations (record.id, method)).isSome = true; rw [isOldHistory]; rfl)
  -- The run after the step.
  have findOther : ∀ other, other ≠ record.id → changed.findAttempt? other = run.findAttempt? other :=
    fun other isOther => AssocList.get?_set_other _ _ _ _ isOther
  have findSelf : changed.findAttempt? record.id =
      some (startedAttempt config run record attempt method) :=
    AssocList.get?_set_self _ _ _
  have argumentsOther : ∀ other otherMethod, other ≠ record.id →
      next.ghost.argumentsOf (other, otherMethod) = config.ghost.argumentsOf (other, otherMethod) := by
    intro other otherMethod isOther
    rw [nextGhost]
    unfold Ghost.argumentsOf Ghost.findInvocation?
    dsimp only
    rw [AssocList.get?_set_other _ _ _ _ (fun isSame => isOther (congrArg Prod.fst isSame))]
    exact argumentsOf_reconcile _ _ _ (other, otherMethod)
  have argumentsSelf : next.ghost.argumentsOf (record.id, method) = some arguments := by
    rw [nextGhost]
    unfold Ghost.argumentsOf Ghost.findInvocation?
    dsimp only
    rw [AssocList.get?_set_self]
    rfl
  have viewOther : ∀ other otherMethod, other ≠ record.id →
      attemptView changed next.ghost other otherMethod = commitView config run other otherMethod := by
    intro other otherMethod isOther
    unfold commitView attemptView
    rw [findOther other isOther, argumentsOther other otherMethod isOther]
  have viewSelf : attemptView changed next.ghost record.id method =
      .running config.ghost.nextVirtualTaskId := by
    unfold attemptView
    rw [findSelf, argumentsSelf]
    simp [startedAttempt, isMethod, arguments, startArguments]
  have relabelEq : next.ghost.normalIndexOf = commitRelabel config run := by
    rw [nextGhost]
    rfl
  have countEq : next.ghost.normalCount = config.ghost.normalCount + 1 := by
    rw [nextGhost]
    rfl
  have runEq : Run.abstract next.ghost changed.reconcileAttempts =
      ({ Run.abstract config.ghost run with seq := config.ghost.normalCount + 1 } : Dex.Run σ).updateStep
        record.id (fun currentRecord =>
          { currentRecord with
            invocation := some
              { method := method, state := .running config.ghost.nextVirtualTaskId } }) := by
    rw [abstract_eq_relabelRun next.ghost changed.reconcileAttempts (attemptView changed next.ghost)
      (fun candidate isCandidate invocation hasCandidate =>
        abstractInvocation_reconcileAttempts changed next.ghost candidate isCandidate changedNodup invocation
          hasCandidate)]
    rw [← relabel_bumpedRun runInvariant, ← stepStart_relabel_updateStep _ (commitView config run)
      (commitRelabel config run) (bumpedRun run).base record.id method config.nextTaskId _ viewOther
      viewSelf, relabelEq, countEq]
    show { relabelRun (attemptView changed next.ghost) (commitRelabel config run) changed.base with
      seq := config.ghost.normalCount + 1 } = relabelRun (attemptView changed next.ghost)
        (commitRelabel config run) changed.base
    simp only [relabelRun]
    rw [show changed.base.seq = run.base.seq + 1 from rfl, commitRelabel_seq]
  -- The virtual tasks after the step: the old ones and the new one.
  let newTask : Dex.Task σ :=
    { id := config.ghost.nextVirtualTaskId, step := record.id, method := method,
      context := { stepExecutionId := record.id, fromStepExecutionId := record.origin },
      input := record.input, snapshot := run.base.snapshot record.options method,
      conditionResults := record.conditionResults, phase := .started }
  have newTaskOf : virtualTaskOf next ((record.id, method), newHistory) = some newTask := by
    unfold virtualTaskOf Config.hasVirtualTask
    simp only [newHistory, isLive, Option.isSome_some, Bool.true_and, ↓reduceIte]
    simp only [Arguments.toTask, InvocationHistory.virtualPhase, isNotRun, arguments,
      startArguments, startContext, snapshotEq, newTask]
  have anyEq : ∀ key, key ≠ (record.id, method) →
      next.realTasks.any (fun task => decide (task.key = key)) =
        config.realTasks.any (fun task => decide (task.key = key)) := by
    intro key isOther
    show (config.tasks ++ [startTask config run record.id record attempt method] ++
      config.zombies).any _ = (config.tasks ++ config.zombies).any _
    have isNotNew : decide ((startTask config run record.id record attempt method).key = key) =
        false := by
      simp only [decide_eq_false_iff_not]
      exact fun isSame => isOther isSame.symm
    simp only [List.any_append, List.any_cons, List.any_nil, isNotNew,
      Bool.or_false]
  have sameTaskOf : ∀ entry, entry.1 ≠ (record.id, method) →
      virtualTaskOf next entry = virtualTaskOf config entry := by
    intro entry isOther
    unfold virtualTaskOf Config.hasVirtualTask
    rw [anyEq entry.1 isOther]
  have noTaskAtKey : ∀ entry, entry ∈ config.ghost.invocations → entry.1 = (record.id, method) →
      ∀ someConfig : Config σ, virtualTaskOf someConfig entry = none := by
    intro entry isEntry isKey someConfig
    have isFoundEntry := get?_of_mem_nodup _ entry isEntry invariant.ghostKeysNodup
    rw [isKey, isOldHistory] at isFoundEntry
    have isEntryHistory : entry.2 = history := (Option.some.inj isFoundEntry).symm
    unfold virtualTaskOf
    rw [isEntryHistory, noArguments]
  have extraNone : ∀ entry, entry ∈ extra → ∀ someConfig : Config σ,
      virtualTaskOf someConfig entry = none := by
    intro entry isEntry someConfig
    unfold virtualTaskOf
    rw [isFresh entry isEntry]
    rfl
  have sameMembers : ∀ task, task ∈ next.virtualTasks ↔ task ∈ config.virtualTasks ++ [newTask] := by
    intro task
    rw [mem_virtualTasks, nextInvocations, List.mem_append, mem_virtualTasks, List.mem_singleton]
    constructor
    · rintro ⟨entry, isEntry, isTask⟩
      rcases List.mem_append.mp isEntry with inSet | inExtra
      · rcases AssocList.mem_set _ _ _ _ inSet with inOld | isNew
        · by_cases isKey : entry.1 = (record.id, method)
          · rw [noTaskAtKey entry inOld isKey] at isTask
            cases isTask
          · exact Or.inl ⟨entry, inOld, by rw [← sameTaskOf entry isKey]; exact isTask⟩
        · subst isNew
          rw [newTaskOf] at isTask
          exact Or.inr (Option.some.inj isTask).symm
      · rw [extraNone entry inExtra] at isTask
        cases isTask
    · rintro (⟨entry, isEntry, isTask⟩ | isNew)
      · have isKey : entry.1 ≠ (record.id, method) := by
          intro isKey
          rw [noTaskAtKey entry isEntry isKey] at isTask
          cases isTask
        exact ⟨entry, List.mem_append_left _ (stepStart_mem_set_of_ne _ _ _ _ isEntry isKey),
          by rw [sameTaskOf entry isKey]; exact isTask⟩
      · subst isNew
        exact ⟨((record.id, method), newHistory),
          List.mem_append_left _ (stepStart_mem_set_self _ _ _ (by rw [isOldHistory]; rfl)),
          newTaskOf⟩
  have idsBelow : ∀ taskId, taskId ∈ virtualTaskIds config.ghost.invocations →
      taskId < config.ghost.nextVirtualTaskId := by
    intro taskId isTaskId
    simp only [virtualTaskIds, List.mem_map, List.mem_filterMap] at isTaskId
    obtain ⟨entryArguments, ⟨entry, isEntry, hasArguments⟩, isIdEq⟩ := isTaskId
    rw [← isIdEq]
    exact invariant.virtualTaskIdsBelow entry entryArguments isEntry hasArguments
  have nextIdsNodup : (virtualTaskIds next.ghost.invocations).Nodup := by
    have appendIds : ∀ first second : AssocList (InvocationKey σ) (InvocationHistory σ),
        virtualTaskIds (first ++ second) = virtualTaskIds first ++ virtualTaskIds second := by
      intro first second
      simp [virtualTaskIds, List.filterMap_append]
    have extraIds : virtualTaskIds extra = [] := by
      unfold virtualTaskIds
      rw [List.filterMap_eq_nil_iff.mpr (fun entry isEntry => by rw [isFresh entry isEntry]; rfl)]
      rfl
    rw [nextInvocations, appendIds, extraIds, List.append_nil]
    apply ((stepStart_virtualTaskIds_set _ _ history newHistory arguments isOldHistory noArguments
      rfl).nodup_iff).mpr
    exact List.nodup_cons.mpr ⟨fun isIn => Nat.lt_irrefl _ (idsBelow _ isIn),
      invariant.virtualTaskIdsNodup⟩
  have tasksEq : next.virtualTasks = config.virtualTasks ++ [newTask] := by
    apply eq_of_same_tasks _ _ (virtualTasks_pairwise next nextIdsNodup) _ sameMembers
    apply List.pairwise_append.mpr
    refine ⟨virtualTasks_pairwise config invariant.virtualTaskIdsNodup, List.pairwise_singleton _ _,
      ?_⟩
    intro oldTask isOld other isOther
    rw [List.mem_singleton] at isOther
    subst isOther
    obtain ⟨entry, isEntry, isTask⟩ := (mem_virtualTasks config oldTask).mp isOld
    obtain ⟨entryArguments, hasArguments, _, isTaskEq⟩ :=
      virtualTaskOf_eq_some config entry oldTask isTask
    subst isTaskEq
    exact invariant.virtualTaskIdsBelow entry entryArguments isEntry hasArguments
  -- The Dex step.
  have absRun : (abstractConfig config).run = some (Run.abstract config.ghost run) := by
    simp [abstractConfig, hasRun]
  have absInvocation : run.abstractInvocation config.ghost record =
      some { method := method, state := .pending } := by
    unfold Run.abstractInvocation
    rw [hasInvocation]
    dsimp only
    rw [isAttempt]
    simp [noSnapshot]
  have absCommit := canCommit_abstract invariant canCommit
  simp only [Dex.step, absRun, stepStart_findStep?_abstract, isRecord, Option.map_some,
    absInvocation, absCommit, ↓reduceIte, Dex.Config.commit]
  have eventsEq := eraseStepEvents_closeEvents config [] run.base changed.reconcileAttempts.base
    (Run.abstract config.ghost run)
    (({ Run.abstract config.ghost run with seq := config.ghost.normalCount + 1 } : Dex.Run σ).updateStep
      record.id (fun currentRecord =>
        { currentRecord with
          invocation := some { method := method, state := .running config.ghost.nextVirtualTaskId } }))
    rfl rfl rfl
  have absNext : abstractConfig next =
      { clock := config.clock, lastCommitAt := some config.clock,
        run := some (({ Run.abstract config.ghost run with seq := config.ghost.normalCount + 1 } :
          Dex.Run σ).updateStep record.id (fun currentRecord =>
            { currentRecord with
              invocation := some { method := method, state := .running config.ghost.nextVirtualTaskId } }))
        tasks := config.virtualTasks ++ [newTask]
        nextTaskId := config.ghost.nextVirtualTaskId + 1 } := by
    unfold abstractConfig
    rw [show next.run = some changed.reconcileAttempts from rfl, Option.map_some, runEq, tasksEq,
      nextGhost]
    rfl
  rw [show (startCommitted config run record.id record attempt method).1 =
    [] ++ Config.closeEvents run.base changed.reconcileAttempts.base from rfl, eventsEq]
  rw [show startNext config run record.id record attempt method = next from rfl, absNext]
  rfl

theorem stepStart_simulated (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (stepId : Dex.StepExecId σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.stepStart stepId false) = some (events, next)) :
    IsStepSimulated program config (.stepStart stepId false) events next := by
  unfold IsStepSimulated
  simp only [dexActionsOf, Bool.false_eq_true, ↓reduceIte, exec_singleton]
  simp only [step] at isStep
  split at isStep
  · rename_i canServe
    split at isStep
    · cases isStep
    · rename_i run hasRun
      split at isStep
      · rename_i record attempt isRecord isAttempt
        split at isStep
        · rename_i method hasInvocation
          split at isStep
          · cases isStep
          · rename_i isAllowed
            simp only [Bool.or_eq_true, Bool.not_eq_true', decide_eq_false_iff_not,
              bne_iff_ne, ne_eq, not_or, Decidable.not_not] at isAllowed
            obtain ⟨⟨_, isMethod⟩, isNotReused⟩ := isAllowed
            have noSnapshot : attempt.snapshot = none := by
              cases hasSnapshot : attempt.snapshot with
              | none => rfl
              | some _ => rw [hasSnapshot] at isNotReused; cases isNotReused
            have canCommit : config.canCommit = true := by
              simp only [Config.canServeCommit, Bool.and_eq_true] at canServe
              exact canServe.2
            injection isStep with isPair
            injection isPair with isEventsEq isNextEq
            subst isEventsEq isNextEq
            exact stepStart_core program config invariant run hasRun canCommit stepId record
              attempt method isRecord isAttempt hasInvocation isMethod noSnapshot
        · cases isStep
      · cases isStep
  · cases isStep

end DexExpressiveness.DexF.WPF1
