import DexExpressiveness.Failure.Proofs.WPF3EffectHistory
import DexExpressiveness.Failure.Proofs.WPF3RunEvolution

/-!
# WP-F3: an inductive invariant for the effect history

`WPF3Good` is the part of the invariants I2 to I4 of spec §11.6 that the
effect history needs, with the run structure that makes it inductive: every
real task follows `par` of its invocation and has emitted a prefix of
`fx⃗(e, m)` of length at most `h(e, m)`; an invocation's attempt holds a
snapshot exactly when the invocation has `par`, and then the snapshot,
input, condition results, and context of `par` are its Step's; and every
`par` belongs to a Step whose invocation is still current or that moved past
the method's phase, so the invocation is never pended again.

It is self-contained: `Failure/Statements/Leaf.lean` declares
`effectHistory_of_exec` before `invariant_step`, and a proof file cannot
import it, so the effect history cannot rest on WP-F4. Everything here is
proved.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## The invariant -/

/-- The attempt's snapshot of an invocation isMatching `par` (I4). -/
def WPF3SnapshotMatches (ghost : Ghost σ) (record : Dex.StepRecord σ) (method : Dex.Method) :
    Option (Dex.Snapshot σ) → Prop
  | none => ghost.argumentsOf (record.id, method) = none
  | some snapshot => ∃ arguments, ghost.argumentsOf (record.id, method) = some arguments ∧
      arguments.snapshot = snapshot ∧ arguments.input = record.input ∧
      arguments.conditionResults = record.conditionResults ∧
      arguments.context = { stepExecutionId := record.id, fromStepExecutionId := record.origin }

/-- The run part of the invariant. -/
structure WPF3RunGood (ghost : Ghost σ) (run : Run σ) : Prop where
  shape : WPF3RunShape run.base
  attemptsHaveRecords : ∀ entry, entry ∈ run.attempts → ∃ record, record ∈ run.base.steps ∧
    record.id = entry.1 ∧
    record.invocation.map (fun invocation => invocation.method) = some entry.2.method
  attemptsMatch : ∀ record, record ∈ run.base.steps → ∀ invocation,
    record.invocation = some invocation → ∃ attempt, run.findAttempt? record.id = some attempt ∧
      attempt.method = invocation.method ∧
      WPF3SnapshotMatches ghost record invocation.method attempt.snapshot
  argumentsHaveRecords : ∀ key arguments, ghost.argumentsOf key = some arguments →
    ∃ record, record ∈ run.base.steps ∧ record.id = key.1 ∧
      ((∃ invocation, record.invocation = some invocation ∧ invocation.method = key.2) ∨
        wpf3IsBeyond record.phase key.2)

/-- I2 and I3 for one real task. -/
def WPF3TaskFollows (program : Program σ) (ghost : Ghost σ) (task : Task σ) : Prop :=
  ∃ arguments, ghost.argumentsOf task.key = some arguments ∧
    task.base.context = arguments.context ∧ task.base.input = arguments.input ∧
    task.base.snapshot = arguments.snapshot ∧
    task.base.conditionResults = arguments.conditionResults ∧
    task.emittedCount ≤ ghost.emittedCountOf task.key ∧
    (task.base.phase = .started → task.emittedCount = 0) ∧
    ∀ remaining response, task.base.phase = .running remaining response →
      remaining = (arguments.compute program.failureFreeFlow task.key).1.drop task.emittedCount

/-- The invariant of the effect history. -/
structure WPF3Good (program : Program σ) (config : Config σ) : Prop where
  realTaskIdsNodup : (config.realTasks.map (fun task => task.base.id)).Nodup
  realTaskIdsBelow : ∀ task, task ∈ config.realTasks → task.base.id < config.nextTaskId
  tasksFollow : ∀ task, task ∈ config.realTasks → WPF3TaskFollows program config.ghost task
  notStarted : config.run = none → ∀ key, config.ghost.argumentsOf key = none
  runGood : ∀ run, config.run = some run → WPF3RunGood config.ghost run

theorem wpf3_good_initial (program : Program σ) : WPF3Good program (Config.initial σ) where
  realTaskIdsNodup := by simp [Config.initial, Config.realTasks]
  realTaskIdsBelow := by simp [Config.initial, Config.realTasks]
  tasksFollow := by simp [Config.initial, Config.realTasks]
  notStarted := by
    intro _ key
    simp [Config.initial, Ghost.initial, Ghost.argumentsOf, Ghost.findInvocation?, AssocList.get?]
  runGood := by simp [Config.initial]

/-- The facts the effect history needs. -/
theorem wpf3_facts_of_good (program : Program σ) (config : Config σ)
    (good : WPF3Good program config) : WPF3EffectFacts program config where
  tasksFollow := by
    intro task isMember
    obtain ⟨arguments, hasArguments, _, _, _, _, emittedAtMost, _, remainingFacts⟩ :=
      good.tasksFollow task isMember
    exact ⟨arguments, hasArguments, emittedAtMost, remainingFacts⟩
  freshStartsHaveNoArguments := by
    intro run stepId record attempt method isRun isRecord isAttempt isInvocation isNoSnapshot
    obtain ⟨isMember, isId⟩ := Dex.Run.mem_of_findStep? _ _ _ isRecord
    obtain ⟨found, isFound, _, isMatching⟩ :=
      (good.runGood run isRun).attemptsMatch record isMember _ isInvocation
    rw [isId, isAttempt] at isFound
    cases isFound
    rw [isNoSnapshot] at isMatching
    rw [← isId]
    exact isMatching

/-! ## Stability under ghost changes -/

theorem wpf3_runGood_congr (ghost other : Ghost σ) (run : Run σ) (good : WPF3RunGood ghost run)
    (sameArguments : ∀ key, other.argumentsOf key = ghost.argumentsOf key) :
    WPF3RunGood other run where
  shape := good.shape
  attemptsHaveRecords := good.attemptsHaveRecords
  attemptsMatch := by
    intro record isMember invocation isInvocation
    obtain ⟨attempt, isAttempt, sameMethod, isMatching⟩ :=
      good.attemptsMatch record isMember invocation isInvocation
    refine ⟨attempt, isAttempt, sameMethod, ?_⟩
    cases isSnapshot : attempt.snapshot with
    | none => rw [isSnapshot] at isMatching; show other.argumentsOf _ = none; rw [sameArguments]; exact isMatching
    | some snapshot =>
      rw [isSnapshot] at isMatching
      obtain ⟨arguments, hasArguments, rest⟩ := isMatching
      exact ⟨arguments, by rw [sameArguments]; exact hasArguments, rest⟩
  argumentsHaveRecords := by
    intro key arguments hasArguments
    rw [sameArguments] at hasArguments
    exact good.argumentsHaveRecords key arguments hasArguments

theorem wpf3_taskFollows_mono (program : Program σ) (ghost other : Ghost σ) (task : Task σ)
    (follows : WPF3TaskFollows program ghost task)
    (sameArguments : other.argumentsOf task.key = ghost.argumentsOf task.key)
    (countGrows : ghost.emittedCountOf task.key ≤ other.emittedCountOf task.key) :
    WPF3TaskFollows program other task := by
  obtain ⟨arguments, hasArguments, sameContext, sameInput, sameSnapshot, sameResults,
    emittedAtMost, startedFacts, runningFacts⟩ := follows
  exact ⟨arguments, by rw [sameArguments]; exact hasArguments, sameContext, sameInput,
    sameSnapshot, sameResults, Nat.le_trans emittedAtMost countGrows, startedFacts, runningFacts⟩

/-! ## Reconciling attempt states -/

/-- Lookup of a key that no element has, in a list of entries made one per element. -/
theorem wpf3_get?_filterMap_absent {α κ ν : Type} [DecidableEq κ] (elements : List α)
    (keyOf : α → κ) (valueOf : α → Option ν) (key : κ)
    (isAbsent : ∀ element, element ∈ elements → keyOf element ≠ key) :
    AssocList.get? (elements.filterMap (fun candidate =>
      (valueOf candidate).map (fun value => (keyOf candidate, value)))) key = none := by
  induction elements with
  | nil => rfl
  | cons head rest inductionHypothesis =>
    have restAbsent := fun element isMember => isAbsent element (List.mem_cons_of_mem _ isMember)
    rw [List.filterMap_cons]
    cases valueOf head with
    | none => exact inductionHypothesis restAbsent
    | some value =>
      simp only [Option.map_some, AssocList.get?, isAbsent head (List.mem_cons_self ..),
        ↓reduceIte]
      exact inductionHypothesis restAbsent

/-- Lookup in a list of entries made one per element, when the keys are distinct. -/
theorem wpf3_get?_filterMap_of_mem {α κ ν : Type} [DecidableEq κ] (elements : List α)
    (keyOf : α → κ) (valueOf : α → Option ν) (keysNodup : (elements.map keyOf).Nodup)
    (element : α) (isMember : element ∈ elements) :
    AssocList.get? (elements.filterMap (fun candidate =>
      (valueOf candidate).map (fun value => (keyOf candidate, value)))) (keyOf element) =
      valueOf element := by
  induction elements with
  | nil => cases isMember
  | cons head rest inductionHypothesis =>
    simp only [List.map_cons, List.nodup_cons, List.mem_map] at keysNodup
    rcases List.mem_cons.mp isMember with isHead | isInRest
    · subst isHead
      rw [List.filterMap_cons]
      cases isValue : valueOf element with
      | none =>
        exact wpf3_get?_filterMap_absent rest keyOf valueOf (keyOf element)
          (fun candidate isCandidate isSame => keysNodup.1 ⟨candidate, isCandidate, isSame⟩)
      | some value => simp [AssocList.get?]
    · have isOther : keyOf head ≠ keyOf element := by
        intro isSame
        exact keysNodup.1 ⟨element, isInRest, isSame.symm⟩
      rw [List.filterMap_cons]
      cases valueOf head with
      | none => exact inductionHypothesis keysNodup.2 isInRest
      | some value =>
        simp only [Option.map_some, AssocList.get?, isOther, ↓reduceIte]
        exact inductionHypothesis keysNodup.2 isInRest

/-- The attempt state that `reconcileAttempts` gives one invocation. -/
def wpf3ReconciledAttempt (run : Run σ) (record : Dex.StepRecord σ) : Option (Attempt σ) :=
  record.invocation.map (fun invocation =>
    match run.attempts.get? record.id with
    | some attempt =>
      if attempt.method = invocation.method then attempt else Attempt.fresh invocation.method
    | none => Attempt.fresh invocation.method)

theorem wpf3_reconcileAttempts_eq (run : Run σ) :
    run.reconcileAttempts.attempts = run.base.steps.filterMap (fun record =>
      (wpf3ReconciledAttempt run record).map (fun attempt => (record.id, attempt))) := by
  unfold Run.reconcileAttempts wpf3ReconciledAttempt
  dsimp only
  congr 1
  funext record
  cases record.invocation with
  | none => rfl
  | some invocation =>
    simp only [Option.map_some]
    cases run.attempts.get? record.id with
    | none => rfl
    | some attempt => by_cases isSame : attempt.method = invocation.method <;> simp [isSame]

theorem wpf3_findAttempt?_reconcile (run : Run σ)
    (idsNodup : (run.base.steps.map (fun record => record.id)).Nodup)
    (record : Dex.StepRecord σ) (isMember : record ∈ run.base.steps) :
    run.reconcileAttempts.findAttempt? record.id = wpf3ReconciledAttempt run record := by
  unfold Run.findAttempt?
  rw [wpf3_reconcileAttempts_eq]
  exact wpf3_get?_filterMap_of_mem run.base.steps (fun record => record.id)
    (wpf3ReconciledAttempt run) idsNodup record isMember

theorem wpf3_mem_reconcileAttempts (run : Run σ) (entry : Dex.StepExecId σ × Attempt σ)
    (isMember : entry ∈ run.reconcileAttempts.attempts) :
    ∃ record invocation, record ∈ run.base.steps ∧ record.id = entry.1 ∧
      record.invocation = some invocation ∧ entry.2.method = invocation.method := by
  rw [wpf3_reconcileAttempts_eq] at isMember
  obtain ⟨record, isRecordMember, isEntry⟩ := List.mem_filterMap.mp isMember
  unfold wpf3ReconciledAttempt at isEntry
  cases isInvocation : record.invocation with
  | none => rw [isInvocation] at isEntry; cases isEntry
  | some invocation =>
    rw [isInvocation] at isEntry
    simp only [Option.map_some, Option.some.injEq] at isEntry
    subst isEntry
    refine ⟨record, invocation, isRecordMember, rfl, isInvocation, ?_⟩
    dsimp only
    split
    · split
      · assumption
      · rfl
    · rfl

/-! ## One commit -/

/-- How a commit may change attempt states: methods and snapshots stay, and
no attempt state is dropped before reconciling. -/
structure WPF3AttemptsEvolve (before after : AssocList (Dex.StepExecId σ) (Attempt σ)) : Prop where
  kept : ∀ id attempt, after.get? id = some attempt → ∃ previous, before.get? id = some previous ∧
    attempt.method = previous.method ∧ attempt.snapshot = previous.snapshot
  stay : ∀ id previous, before.get? id = some previous → ∃ attempt, after.get? id = some attempt

theorem WPF3AttemptsEvolve.refl (attempts : AssocList (Dex.StepExecId σ) (Attempt σ)) :
    WPF3AttemptsEvolve attempts attempts where
  kept := fun _ attempt isFound => ⟨attempt, isFound, rfl, rfl⟩
  stay := fun _ previous isFound => ⟨previous, isFound⟩

theorem WPF3AttemptsEvolve.trans {first second third : AssocList (Dex.StepExecId σ) (Attempt σ)}
    (firstEvolves : WPF3AttemptsEvolve first second)
    (secondEvolves : WPF3AttemptsEvolve second third) : WPF3AttemptsEvolve first third where
  kept := by
    intro id attempt isFound
    obtain ⟨middle, isMiddle, sameMethod, sameSnapshot⟩ := secondEvolves.kept id attempt isFound
    obtain ⟨previous, isPrevious, samePreviousMethod, samePreviousSnapshot⟩ :=
      firstEvolves.kept id middle isMiddle
    exact ⟨previous, isPrevious, sameMethod.trans samePreviousMethod,
      sameSnapshot.trans samePreviousSnapshot⟩
  stay := by
    intro id previous isFound
    obtain ⟨middle, isMiddle⟩ := firstEvolves.stay id previous isFound
    exact secondEvolves.stay id middle isMiddle

/-- Setting one attempt state to a change of its old one with the same method
and snapshot. -/
theorem wpf3_attemptsEvolve_set (attempts : AssocList (Dex.StepExecId σ) (Attempt σ))
    (step : Dex.StepExecId σ) (previous attempt : Attempt σ)
    (isPrevious : attempts.get? step = some previous)
    (sameMethod : attempt.method = previous.method)
    (sameSnapshot : attempt.snapshot = previous.snapshot) :
    WPF3AttemptsEvolve attempts (attempts.set step attempt) where
  kept := by
    intro id found isFound
    by_cases isStep : id = step
    · subst isStep
      rw [AssocList.get?_set_self] at isFound
      cases isFound
      exact ⟨previous, isPrevious, sameMethod, sameSnapshot⟩
    · rw [AssocList.get?_set_other _ _ _ _ isStep] at isFound
      exact ⟨found, isFound, rfl, rfl⟩
  stay := by
    intro id found isFound
    by_cases isStep : id = step
    · subst isStep
      exact ⟨attempt, AssocList.get?_set_self _ _ _⟩
    · exact ⟨found, by rw [AssocList.get?_set_other _ _ _ _ isStep]; exact isFound⟩

/-- **One commit keeps the run part of the invariant**, when its change of
the Step table is an evolution and it keeps methods and snapshots of attempt
states. -/
theorem wpf3_runGood_reconcile (ghost ghostAfter : Ghost σ) (run changed : Run σ)
    (good : WPF3RunGood ghost run)
    (sameArguments : ∀ key, ghostAfter.argumentsOf key = ghost.argumentsOf key)
    (evolution : WPF3Evolution run.base changed.base)
    (attemptsEvolve : WPF3AttemptsEvolve run.attempts changed.attempts) :
    WPF3RunGood ghostAfter changed.reconcileAttempts := by
  obtain ⟨evolves, shape⟩ := evolution
  refine ⟨shape, ?_, ?_, ?_⟩
  · intro entry isMember
    obtain ⟨record, invocation, isRecordMember, isId, isInvocation, sameMethod⟩ :=
      wpf3_mem_reconcileAttempts changed entry isMember
    exact ⟨record, isRecordMember, isId, by rw [isInvocation, sameMethod]; rfl⟩
  · intro next isMember invocation isInvocation
    rw [show changed.reconcileAttempts.findAttempt? next.id = wpf3ReconciledAttempt changed next from
      wpf3_findAttempt?_reconcile changed shape.idsNodup next isMember]
    unfold wpf3ReconciledAttempt
    rw [isInvocation]
    simp only [Option.map_some, Option.some.injEq, exists_eq_left']
    have freshCase : ghostAfter.argumentsOf (next.id, invocation.method) = none →
        WPF3SnapshotMatches ghostAfter next invocation.method
          (Attempt.fresh invocation.method : Attempt σ).snapshot := fun isNone => isNone
    -- The fresh case: no attempt state of this method survived the commit.
    have freshHasNoArguments :
        (∀ attempt, changed.attempts.get? next.id = some attempt →
          attempt.method ≠ invocation.method) →
        ghostAfter.argumentsOf (next.id, invocation.method) = none := by
      intro hasNoKept
      rw [sameArguments]
      cases hasArguments : ghost.argumentsOf (next.id, invocation.method) with
      | none => rfl
      | some arguments =>
        exfalso
        obtain ⟨record, isRecordMember, isRecordId, isCurrentOrBeyond⟩ :=
          good.argumentsHaveRecords _ arguments hasArguments
        rcases evolves.evolves next isMember with ⟨original, isOriginalMember, originalEvolves⟩ |
          isNew
        · have isSame : original = record := eq_of_nodup_map (fun record => record.id)
            run.base.steps good.shape.idsNodup isOriginalMember isRecordMember
            (originalEvolves.sameId.symm.trans isRecordId.symm)
          subst isSame
          rcases originalEvolves.invocationOrigin invocation isInvocation with
            ⟨previous, isPrevious, samePreviousMethod, _, _, _⟩ | ⟨isExecute, isEarly⟩
          · obtain ⟨previousAttempt, isPreviousAttempt, samePreviousAttemptMethod, _⟩ :=
              good.attemptsMatch original isOriginalMember previous isPrevious
            obtain ⟨attempt, isAttempt⟩ := attemptsEvolve.stay _ _ isPreviousAttempt
            obtain ⟨keptPrevious, isKeptPrevious, sameKeptMethod, _⟩ :=
              attemptsEvolve.kept _ _ isAttempt
            unfold Run.findAttempt? at isPreviousAttempt
            rw [isPreviousAttempt] at isKeptPrevious
            cases isKeptPrevious
            rw [← originalEvolves.sameId] at isAttempt
            exact hasNoKept attempt isAttempt (sameKeptMethod.trans
              (samePreviousAttemptMethod.trans samePreviousMethod))
          · rcases isCurrentOrBeyond with ⟨current, isCurrent, sameCurrentMethod⟩ | isBeyond
            · have isPhase := good.shape.methodPhase original isOriginalMember current isCurrent
              rw [sameCurrentMethod, isExecute] at isPhase
              rw [isPhase] at isEarly
              simp [wpf3PhaseOf, wpf3PhaseRank] at isEarly
            · rw [isExecute] at isBeyond
              unfold wpf3IsBeyond at isBeyond
              simp only [wpf3PhaseOf, wpf3PhaseRank] at isBeyond
              revert isEarly isBeyond
              cases original.phase <;> simp [wpf3PhaseRank]
        · exact isNew record isRecordMember isRecordId
    cases isChangedAttempt : changed.attempts.get? next.id with
    | none =>
      refine ⟨rfl, freshCase (freshHasNoArguments ?_)⟩
      intro attempt isAttempt
      rw [isChangedAttempt] at isAttempt
      cases isAttempt
    | some attempt =>
      dsimp only
      split
      · rename_i isSameMethod
        refine ⟨isSameMethod, ?_⟩
        obtain ⟨previous, isPrevious, samePreviousMethod, samePreviousSnapshot⟩ :=
          attemptsEvolve.kept _ _ isChangedAttempt
        obtain ⟨record, isRecordMember, isRecordId, isRecordMethod⟩ :=
          good.attemptsHaveRecords _ (wpf3_mem_of_get? _ _ _ isPrevious)
        rcases evolves.evolves next isMember with ⟨original, isOriginalMember, originalEvolves⟩ |
          isNew
        · have isSame : original = record := eq_of_nodup_map (fun record => record.id)
            run.base.steps good.shape.idsNodup isOriginalMember isRecordMember
            (originalEvolves.sameId.symm.trans isRecordId.symm)
          subst isSame
          rcases originalEvolves.invocationOrigin invocation isInvocation with
            ⟨recordInvocation, isRecordInvocation, sameRecordMethod, sameInput, sameResults,
              sameOrigin⟩ | ⟨isExecute, isEarly⟩
          · obtain ⟨recordAttempt, isRecordAttempt, _, isMatching⟩ :=
              good.attemptsMatch original isOriginalMember recordInvocation isRecordInvocation
            have isSameAttempt : recordAttempt = previous := by
              unfold Run.findAttempt? at isRecordAttempt
              rw [isRecordId, isPrevious] at isRecordAttempt
              exact (Option.some.inj isRecordAttempt).symm
            subst isSameAttempt
            rw [← samePreviousSnapshot] at isMatching
            rw [sameRecordMethod] at isMatching
            cases isSnapshot : attempt.snapshot with
            | none =>
              rw [isSnapshot] at isMatching
              show ghostAfter.argumentsOf _ = none
              rw [sameArguments, originalEvolves.sameId]
              exact isMatching
            | some snapshot =>
              rw [isSnapshot] at isMatching
              obtain ⟨arguments, hasArguments, sameSnapshot, isInput, isResults, isContext⟩ :=
                isMatching
              refine ⟨arguments, ?_, sameSnapshot, ?_, ?_, ?_⟩
              · rw [sameArguments, originalEvolves.sameId]; exact hasArguments
              · rw [sameInput]; exact isInput
              · rw [sameResults]; exact isResults
              · rw [sameOrigin, originalEvolves.sameId]; exact isContext
          · exfalso
            simp only [Option.map_eq_some_iff] at isRecordMethod
            obtain ⟨recordInvocation, isRecordInvocation, isRecordInvocationMethod⟩ :=
              isRecordMethod
            have isPhase := good.shape.methodPhase original isOriginalMember recordInvocation
              isRecordInvocation
            rw [isRecordInvocationMethod, ← samePreviousMethod, isSameMethod, isExecute] at isPhase
            rw [isPhase] at isEarly
            simp [wpf3PhaseOf, wpf3PhaseRank] at isEarly
        · exact absurd isRecordId (isNew record isRecordMember)
      · rename_i isOtherMethod
        refine ⟨rfl, freshCase (freshHasNoArguments ?_)⟩
        intro found isFound
        rw [isChangedAttempt] at isFound
        cases isFound
        exact isOtherMethod
  · intro key arguments hasArguments
    rw [sameArguments] at hasArguments
    obtain ⟨record, isRecordMember, isRecordId, isCurrentOrBeyond⟩ :=
      good.argumentsHaveRecords key arguments hasArguments
    obtain ⟨next, isNextMember, isNextId⟩ := evolves.keepsIds record isRecordMember
    refine ⟨next, isNextMember, isNextId.trans isRecordId, ?_⟩
    rcases evolves.evolves next isNextMember with ⟨original, isOriginalMember, originalEvolves⟩ |
      isNew
    · have isSame : original = record := eq_of_nodup_map (fun record => record.id)
        run.base.steps good.shape.idsNodup isOriginalMember isRecordMember
        (originalEvolves.sameId.symm.trans isNextId)
      subst isSame
      rcases isCurrentOrBeyond with ⟨invocation, isInvocation, sameMethod⟩ | isBeyond
      · rcases originalEvolves.invocationExit invocation isInvocation with
          ⟨current, isCurrent, sameCurrentMethod⟩ | isNextBeyond
        · exact Or.inl ⟨current, isCurrent, sameCurrentMethod.trans sameMethod⟩
        · exact Or.inr (sameMethod ▸ isNextBeyond)
      · exact Or.inr (wpf3_isBeyond_mono isBeyond originalEvolves.rankGrows)
    · exact absurd isNextId.symm (isNew record isRecordMember)

end DexExpressiveness.DexF
