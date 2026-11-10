import DexExpressiveness.Failure.Proofs.WPF4DexRules

/-!
# WP-F4: [TIMER-FIRE] with [RETRY-FIRE] keeps the invariants

The commit folds `fireDueTimer` over the fired timers, wait and retry, in
index order. `FoldState` is the invariant of the fold: the Step table is kept,
Steps with an invocation are untouched, attempt states only lose Backoff, a
Backoff attempt state keeps its retry timer, and retry timers only go away.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- An update of another Step keeps a record. -/
theorem mem_updateStep_of_ne {run : Dex.Run σ} {record : Dex.StepRecord σ}
    (isRecord : record ∈ run.steps) {step : Dex.StepExecId σ} (isOther : record.id ≠ step)
    (change : Dex.StepRecord σ → Dex.StepRecord σ) : record ∈ (run.updateStep step change).steps :=
  (Dex.Run.mem_updateStep _ _ _ _).mpr ⟨record, isRecord, by simp [isOther]⟩

/-- `eval` of another Step keeps a record. -/
theorem mem_evalStep_of_ne {run : Dex.Run σ} {record : Dex.StepRecord σ}
    (isRecord : record ∈ run.steps) {step : Dex.StepExecId σ} (isOther : record.id ≠ step) :
    record ∈ (run.evalStep step).steps := by
  rcases Dex.Run.evalStep_cases run step with ⟨isSame, _⟩ | ⟨_, _, _, _, _, _, isEval⟩
  · rw [isSame]; exact isRecord
  · rw [isEval]
    apply mem_updateStep_of_ne _ isOther
    rw [(Dex.Run.consumeMessages_frame _ _ _).1]
    exact isRecord

/-- Firing a wait timer keeps every Step with an invocation, when Step IDs are
distinct and waiting Steps have no invocation. -/
theorem fireTimer_keeps_invoked (run : Dex.Run σ) (timer : Dex.TimerId σ)
    (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (waitingNone : ∀ record, record ∈ run.steps → record.phase = .waiting →
      record.invocation = none)
    (record : Dex.StepRecord σ) (isRecord : record ∈ run.steps)
    (hasInvocation : record.invocation.isSome = true) : record ∈ (run.fireTimer timer).steps := by
  unfold Dex.Run.fireTimer
  split
  · simp only
    split
    · exact isRecord
    · rename_i found isFound
      split
      · split
        · exact isRecord
        · rename_i isWaiting _ _
          obtain ⟨isFoundMember, isFoundId⟩ := mem_of_findStep? isFound
          have isOther : record.id ≠ timer.step := by
            intro sameId
            have isSame := eq_of_id_eq idsNodup isRecord isFoundMember (sameId.trans isFoundId.symm)
            rw [isSame, waitingNone found isFoundMember isWaiting] at hasInvocation
            cases hasInvocation
          exact mem_evalStep_of_ne (mem_updateStep_of_ne (run := { run with timers := run.timers.erase timer }) isRecord isOther _) isOther
      · exact isRecord
  · exact isRecord

/-- The invariant of the timer fold. -/
structure FoldState (run current : Run σ) : Prop where
  /-- The Step table is kept. -/
  keep : StepsKeep none (bumpedRun run).base current.base
  /-- Steps with an invocation are untouched. -/
  invokedKept : ∀ record, record ∈ run.base.steps → record.invocation.isSome = true →
    record ∈ current.base.steps
  /-- Attempt states only lose Backoff. -/
  attemptsKept : AttemptsKept run.attempts current.attempts
  /-- A Backoff attempt state has its retry timer. -/
  backoffArmed : ∀ step attempt, current.attempts.get? step = some attempt →
    attempt.isBackoff = true →
    current.retryTimers.contains
      { step := step, method := attempt.method, attempt := attempt.number } = true
  /-- Retry timers only go away. -/
  retrySublist : current.retryTimers.Sublist run.retryTimers

/-- The fold starts in its invariant. -/
theorem foldState_initial {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run) :
    FoldState run (bumpedRun run) where
  keep := StepsKeep.refl none _
  invokedKept := fun _ isRecord _ => isRecord
  attemptsKept := fun _ => Or.inl rfl
  backoffArmed := backoffArmed_of_invariant (run := run) invariant isRun
  retrySublist := List.Sublist.refl _

/-- In the fold, Step IDs are distinct and waiting Steps have no invocation. -/
theorem FoldState.facts {program : Program σ} {config : Config σ} {run current : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    (state : FoldState run current) :
    (current.base.steps.map (fun record => record.id)).Nodup ∧
      ∀ record, record ∈ current.base.steps → record.phase = .waiting →
        record.invocation = none := by
  have runInvariant := invariant.runInvariant run isRun
  refine ⟨by rw [state.keep.ids]; exact runInvariant.stepIdsNodup, ?_⟩
  intro changed isChanged isWaiting
  obtain ⟨record, isRecord, isStep⟩ := state.keep.backward changed isChanged
  rcases isStep.invocation with ⟨sameInvocation, _, _, samePhase⟩ | ⟨noInvocation, _⟩ |
    ⟨_, executePhase, _⟩
  · rw [sameInvocation]
    rw [samePhase] at isWaiting
    have phaseMatches := runInvariant.invocationMatchesPhase record isRecord
    cases hInvocation : record.invocation with
    | none => rfl
    | some invocation =>
      rw [hInvocation] at phaseMatches
      simp only at phaseMatches
      rw [phaseMatches] at isWaiting
      cases hMethod : invocation.method <;> simp [hMethod] at isWaiting
  · exact noInvocation
  · rw [executePhase] at isWaiting
    cases isWaiting

/-- A Backoff attempt state in the fold belongs to a Step whose invocation is
pending with its method. -/
theorem FoldState.backoffRecord {program : Program σ} {config : Config σ} {run current : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    (state : FoldState run current) {step : Dex.StepExecId σ} {attempt : Attempt σ}
    (isAttempt : current.attempts.get? step = some attempt) (isBackoff : attempt.isBackoff = true) :
    ∃ record, current.base.findStep? step = some record ∧
      record.invocation = some { method := attempt.method, state := .pending } := by
  have runInvariant := invariant.runInvariant run isRun
  have isOldAttempt : run.attempts.get? step = some attempt := by
    rcases state.attemptsKept step with isSame | ⟨found, _, isCleared⟩
    · rw [← isSame]; exact isAttempt
    · rw [isAttempt] at isCleared
      cases isCleared
      cases isBackoff
  obtain ⟨record, isRecord, isId, hasInvocation⟩ :=
    runInvariant.attemptsHaveInvocations _ (mem_of_get? _ _ _ isOldAttempt)
  simp only at isId
  have isMatching := runInvariant.attemptsMatch record isRecord
  unfold AttemptMatchesRecord at isMatching
  cases hInvocation : record.invocation with
  | none => rw [hInvocation] at hasInvocation; cases hasInvocation
  | some invocation =>
    rw [hInvocation] at isMatching
    obtain ⟨found, _, isFound, _, _, sameMethod, _, _, _, backoffClause⟩ := isMatching
    unfold Run.findAttempt? at isFound
    rw [isId, isOldAttempt] at isFound
    cases isFound
    have isPending := (backoffClause isBackoff).1
    have isKept := state.invokedKept record isRecord (by rw [hInvocation]; rfl)
    refine ⟨record, ?_, ?_⟩
    · rw [← isId]
      exact findStep?_of_mem (state.facts invariant isRun).1 isKept
    · rw [hInvocation, sameMethod, ← isPending]

/-- Firing a wait timer keeps the fold invariant. -/
theorem FoldState.fireWait {program : Program σ} {config : Config σ} {run current : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    (state : FoldState run current) (timer : Dex.TimerId σ) :
    FoldState run (current.fireDueTimer (.wait timer)) := by
  obtain ⟨idsNodup, waitingNone⟩ := state.facts invariant isRun
  exact
    { keep := state.keep.trans (stepsKeep_fireTimer current.base timer idsNodup)
      invokedKept := fun record isRecord hasInvocation =>
        fireTimer_keeps_invoked current.base timer idsNodup waitingNone record
          (state.invokedKept record isRecord hasInvocation) hasInvocation
      attemptsKept := state.attemptsKept
      backoffArmed := state.backoffArmed
      retrySublist := state.retrySublist }

/-- Firing a retry timer keeps the fold invariant. -/
theorem FoldState.fireRetry {program : Program σ} {config : Config σ} {run current : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    (state : FoldState run current) (timer : RetryTimerId σ) :
    FoldState run (current.fireDueTimer (.retry timer)) := by
  show FoldState run (current.fireRetryTimer timer)
  unfold Run.fireRetryTimer
  split
  · rename_i isContained
    have erasedSublist : (current.retryTimers.erase timer).Sublist run.retryTimers :=
      (AssocList.erase_sublist _ _).trans state.retrySublist
    -- The retry timers after the erase keep every other key.
    have keepsOther : ∀ other : RetryTimerId σ, other ≠ timer →
        current.retryTimers.contains other = true →
        (current.retryTimers.erase timer).contains other = true := by
      intro other isOther isOtherContained
      rw [contains_erase_other _ _ _ isOther]
      exact isOtherContained
    -- A Backoff attempt state whose timer is the fired one passes the guard.
    have guardOf : ∀ step attempt, current.attempts.get? step = some attempt →
        attempt.isBackoff = true →
        ({ step := step, method := attempt.method, attempt := attempt.number } : RetryTimerId σ) =
          timer →
        ∃ record, current.base.findStep? timer.step = some record ∧
          current.attempts.get? timer.step = some attempt ∧
          (decide (record.invocation = some { method := timer.method, state := .pending }) &&
            decide (attempt.method = timer.method) && attempt.isBackoff &&
            decide (attempt.number = timer.attempt)) = true := by
      intro step attempt isAttempt isBackoff isTimer
      obtain ⟨record, isFound, hasPending⟩ := state.backoffRecord invariant isRun isAttempt isBackoff
      subst isTimer
      refine ⟨record, isFound, isAttempt, ?_⟩
      simp [hasPending, isBackoff]
    have disarmedState : (∀ step attempt, current.attempts.get? step = some attempt →
        attempt.isBackoff = true →
        ({ step := step, method := attempt.method, attempt := attempt.number } : RetryTimerId σ) ≠
          timer) →
        FoldState run { current with retryTimers := current.retryTimers.erase timer } := by
      intro notFired
      exact
        { keep := state.keep
          invokedKept := state.invokedKept
          attemptsKept := state.attemptsKept
          backoffArmed := fun step attempt isAttempt isBackoff =>
            keepsOther _ (notFired step attempt isAttempt isBackoff)
              (state.backoffArmed step attempt isAttempt isBackoff)
          retrySublist := erasedSublist }
    simp only
    split
    · rename_i record attempt isFound isAttempt
      split
      · rename_i isGuard
        simp only [Bool.and_eq_true, decide_eq_true_eq] at isGuard
        obtain ⟨⟨⟨_, _⟩, _⟩, _⟩ := isGuard
        exact
          { keep := state.keep
            invokedKept := state.invokedKept
            attemptsKept := by
              intro step
              simp only [Run.setAttempt]
              by_cases isStep : step = timer.step
              · subst isStep
                rw [AssocList.get?_set_self]
                right
                rcases state.attemptsKept timer.step with isSame | ⟨found, isOld, isCleared⟩
                · exact ⟨attempt, by rw [← isSame]; exact isAttempt, rfl⟩
                · have isAttemptCleared : attempt = { found with isBackoff := false } := by
                    have := isAttempt
                    rw [isCleared] at this
                    cases this
                    rfl
                  exact ⟨found, isOld, by rw [isAttemptCleared]⟩
              · rw [AssocList.get?_set_other _ _ _ _ isStep]
                exact state.attemptsKept step
            backoffArmed := by
              intro step found isFound isBackoff
              show (current.retryTimers.erase timer).contains _ = true
              have isFoundOld : current.attempts.get? step = some found := by
                by_cases isStep : step = timer.step
                · subst isStep
                  have := isFound
                  simp only [Run.setAttempt, AssocList.get?_set_self, Option.some.injEq] at this
                  rw [← this] at isBackoff
                  cases isBackoff
                · have := isFound
                  simp only [Run.setAttempt] at this
                  rwa [AssocList.get?_set_other _ _ _ _ isStep] at this
              have isStepOther : step ≠ timer.step := by
                intro isStep
                subst isStep
                have := isFound
                simp only [Run.setAttempt, AssocList.get?_set_self, Option.some.injEq] at this
                rw [← this] at isBackoff
                cases isBackoff
              refine keepsOther _ (fun isTimer => isStepOther (by rw [← isTimer])) ?_
              exact state.backoffArmed step found isFoundOld isBackoff
            retrySublist := erasedSublist }
      · rename_i isGuard
        apply disarmedState
        intro step found isFoundStep isBackoff isTimer
        obtain ⟨guardRecord, isGuardFound, isGuardAttempt, guardHolds⟩ :=
          guardOf step found isFoundStep isBackoff isTimer
        have isRecordFound : current.base.findStep? timer.step = some record := isFound
        have isAttemptFound : current.attempts.get? timer.step = some attempt := isAttempt
        rw [isGuardFound] at isRecordFound
        cases isRecordFound
        rw [isGuardAttempt] at isAttemptFound
        cases isAttemptFound
        exact isGuard guardHolds
    · rename_i notBoth
      apply disarmedState
      intro step found isFound isBackoff isTimer
      obtain ⟨guardRecord, isGuardFound, isGuardAttempt, _⟩ :=
        guardOf step found isFound isBackoff isTimer
      exact notBoth guardRecord found isGuardFound isGuardAttempt
  · exact state

/-- The whole fold keeps its invariant. -/
theorem foldState_foldl {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    (fired : List (DueTimer σ)) (current : Run σ) (state : FoldState run current) :
    FoldState run (fired.foldl Run.fireDueTimer current) := by
  induction fired generalizing current with
  | nil => exact state
  | cons timer laterTimers inductionHypothesis =>
    simp only [List.foldl_cons]
    apply inductionHypothesis
    cases timer with
    | wait waitTimer => exact state.fireWait invariant isRun waitTimer
    | retry retryTimer => exact state.fireRetry invariant isRun retryTimer

/-- The commit of [TIMER-FIRE] with [RETRY-FIRE] keeps the invariants, for any
list of fired timers. -/
theorem invariant_timerFireCommit {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    (canCommit : config.canCommit = true) (isUp : config.isUp = true)
    (fired : List (DueTimer σ)) (isNormal : Bool) :
    Invariant program (config.commitRun run isNormal none [] (fun current =>
      fired.foldl Run.fireDueTimer current)).2 := by
  rw [commitRun_eq]
  have runInvariant := invariant.runInvariant run isRun
  have state := foldState_foldl invariant isRun fired (bumpedRun run)
    (foldState_initial invariant isRun)
  exact invariant_dexCommit invariant isRun canCommit isUp config rfl rfl rfl rfl
    (fun _ isTask => isTask) invariant.realTaskIdsNodup (fun _ isTask => isTask)
    (fun _ isInterrupt => isInterrupt) _ none isNormal none state.keep.toEvolve
    (fun _ isNone => by cases isNone) (closedQuiet_of_keep state.keep runInvariant.closedRunIsQuiet)
    state.attemptsKept state.backoffArmed (fun _ isEntry => state.retrySublist.subset isEntry)
    (List.Nodup.sublist (state.retrySublist.map _) runInvariant.retryTimersNodup)
    (fun _ _ isNone => by cases isNone)
    (fun _ isTask isNot => absurd isTask isNot) (fun _ isInterrupt isNot => absurd isInterrupt isNot)

end DexExpressiveness.DexF.WPF4
