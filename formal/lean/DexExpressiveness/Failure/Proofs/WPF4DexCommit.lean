import DexExpressiveness.Failure.Proofs.WPF4Base
import DexExpressiveness.Failure.Proofs.WPF4FailureRules

/-!
# WP-F4: `Invariant` after a commit that runs the meta-functions

[PUBLISH], the Complete rules, [TIMER-FIRE] with [RETRY-FIRE], and the
Exhausted branch of a failure change the Step table only through the
meta-functions of spec §4.2. `invariant_dexCommit` proves `Invariant` for
every such commit from a `StepsEvolve` of the Step table and facts about the
attempt states and retry timers.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- `isExecuteOrDone` as a disjunction. -/
theorem isExecuteOrDone_iff (phase : Dex.Phase) :
    isExecuteOrDone phase = true ↔
      phase = .execute ∨ phase = .completed ∨ phase = .canceled := by
  cases phase <;> simp [isExecuteOrDone]

/-- A Step before `EX` has no Execute history. -/
theorem noExecuteHistory {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    {record : Dex.StepRecord σ} (isRecord : record ∈ run.base.steps)
    (isBefore : isExecuteOrDone record.phase = false) :
    config.ghost.findInvocation? (record.id, .execute) = none := by
  have runInvariant := invariant.runInvariant run isRun
  cases isFound : config.ghost.findInvocation? (record.id, .execute) with
  | none => rfl
  | some history =>
    obtain ⟨other, isOther, isId, isExecute⟩ :=
      runInvariant.historiesHaveSteps _ (mem_of_get? _ _ _ isFound)
    rw [eq_of_id_eq runInvariant.stepIdsNodup isOther isRecord isId] at isExecute
    have := (isExecuteOrDone_iff record.phase).mpr (isExecute rfl)
    rw [isBefore] at this
    cases this

/-- The invocation of a Step before `EX` is a WaitFor. -/
theorem waitFor_of_before {program : Program σ} {config : Config σ} {run : Run σ}
    (invariant : Invariant program config) (isRun : config.run = some run)
    {record : Dex.StepRecord σ} (isRecord : record ∈ run.base.steps)
    (isBefore : isExecuteOrDone record.phase = false) {invocation : Dex.Invocation}
    (hasInvocation : record.invocation = some invocation) : invocation.method = .waitFor := by
  have phaseMatches := (invariant.runInvariant run isRun).invocationMatchesPhase record isRecord
  rw [hasInvocation] at phaseMatches
  simp only at phaseMatches
  cases isMethod : invocation.method with
  | waitFor => rfl
  | execute =>
    rw [isMethod] at phaseMatches
    rw [phaseMatches] at isBefore
    cases isBefore

/-- A new Step has an ID that no old Step has. -/
theorem newRecord_fresh {old new : Dex.Run σ} {record : Dex.StepRecord σ}
    (isNew : IsNewRecord old new record)
    (numbersCounted : ∀ other, other ∈ old.steps →
      other.id.number ≤ (old.counters.get? other.id.stepType).getD 0) :
    ∀ other, other ∈ old.steps → other.id ≠ record.id := by
  intro other isOther sameId
  have isCounted := numbersCounted other isOther
  rw [sameId] at isCounted
  exact absurd isNew.1 (Nat.not_lt.mpr isCounted)

/-- The attempt states that the meta-function commits leave: unchanged, or
with Backoff cleared by [RETRY-FIRE]. -/
def AttemptsKept (before after : AssocList (Dex.StepExecId σ) (Attempt σ)) : Prop :=
  ∀ step, after.get? step = before.get? step ∨
    ∃ attempt, before.get? step = some attempt ∧
      after.get? step = some { attempt with isBackoff := false }

/-- The reconciled attempt state of a Step whose invocation stayed. -/
theorem reconciled_of_kept {before after : AssocList (Dex.StepExecId σ) (Attempt σ)}
    (kept : AttemptsKept before after) {record : Dex.StepRecord σ} {invocation : Dex.Invocation}
    (hasInvocation : record.invocation = some invocation) {attempt : Attempt σ}
    (isAttempt : before.get? record.id = some attempt) (sameMethod : attempt.method = invocation.method) :
    ∃ changed, reconciledAttempt after record = some changed ∧
      after.get? record.id = some changed ∧ changed.method = attempt.method ∧
      changed.number = attempt.number ∧ changed.snapshot = attempt.snapshot ∧
      (changed.isBackoff = true → changed = attempt) := by
  unfold reconciledAttempt
  rw [hasInvocation]
  rcases kept record.id with isSame | ⟨found, isFound, isCleared⟩
  · refine ⟨attempt, ?_, by rw [isSame, isAttempt], rfl, rfl, rfl, fun _ => rfl⟩
    rw [isSame, isAttempt]
    simp [sameMethod]
  · rw [isAttempt] at isFound
    cases isFound
    refine ⟨{ attempt with isBackoff := false }, ?_, isCleared, rfl, rfl, rfl,
      fun isBackoff => by cases isBackoff⟩
    rw [isCleared]
    simp [sameMethod]

/-- The reconciled attempt state of a Step whose old attempt state, if any,
has another method. -/
theorem reconciled_fresh {before after : AssocList (Dex.StepExecId σ) (Attempt σ)}
    (kept : AttemptsKept before after) {record : Dex.StepRecord σ} {method : Dex.Method}
    (hasInvocation : record.invocation = some { method := method, state := .pending })
    (otherMethod : ∀ attempt, before.get? record.id = some attempt → attempt.method ≠ method) :
    reconciledAttempt after record = some (Attempt.fresh method) := by
  unfold reconciledAttempt
  rw [hasInvocation]
  simp only
  rcases kept record.id with isSame | ⟨found, isFound, isCleared⟩
  · rw [isSame]
    cases isOld : before.get? record.id with
    | none => rfl
    | some attempt =>
      simp only [otherMethod attempt isOld, ↓reduceIte]
  · rw [isCleared]
    simp only [otherMethod found isFound, ↓reduceIte]

end DexExpressiveness.DexF.WPF4
