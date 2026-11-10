import DexExpressiveness.Failure.Proofs.WPF4DexRules

/-!
# WP-F4: the Step table under a Complete rule

A Complete rule clears the invocation of its Step `e`, applies the effects,
and registers a wait or applies a decision. In between, `e` is in `WF` or
`EX` without an invocation, which no `RecordStep` allows. The lemmas here
show that the effects leave `e` alone and that the whole change is a
`StepsEvolve` when the last meta-function moves `e` out of its phase.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- `eval` leaves every Step with the ID of a non-waiting Step unchanged. -/
theorem evalStep_keeps_record (run : Dex.Run σ) (evaluated step : Dex.StepExecId σ)
    (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (notWaiting : ∀ record, record ∈ run.steps → record.id = step → record.phase ≠ .waiting) :
    ∀ record, record ∈ (run.evalStep evaluated).steps → record.id = step → record ∈ run.steps := by
  intro record isRecord isId
  rcases Dex.Run.evalStep_cases run evaluated with ⟨isSame, _⟩ |
    ⟨found, waitState, consumeFlags, isFound, isWaiting, _, isEval⟩
  · rw [isSame] at isRecord
    exact isRecord
  · rw [isEval] at isRecord
    obtain ⟨original, isOriginal, isMapped⟩ := (Dex.Run.mem_updateStep _ _ _ _).mp isRecord
    rw [(Dex.Run.consumeMessages_frame _ _ _).1] at isOriginal
    have isOriginalMember : original ∈ run.steps := isOriginal
    by_cases isEvaluated : original.id = evaluated
    · exfalso
      simp only [isEvaluated, ↓reduceIte] at isMapped
      have sameId : record.id = evaluated := by rw [isMapped] <;> exact isEvaluated
      obtain ⟨isFoundMember, isFoundId⟩ := mem_of_findStep? isFound
      exact notWaiting found isFoundMember (by rw [isFoundId, ← sameId, isId]) isWaiting
    · simp only [isEvaluated, ↓reduceIte] at isMapped
      rw [isMapped]
      exact isOriginalMember

/-- `effects` leaves every Step with the ID of a non-waiting Step unchanged. -/
theorem applyEffects_keeps_record (run : Dex.Run σ) (effects : Dex.Effects σ)
    (step : Dex.StepExecId σ) (idsNodup : (run.steps.map (fun record => record.id)).Nodup)
    (notWaiting : ∀ record, record ∈ run.steps → record.id = step → record.phase ≠ .waiting) :
    ∀ record, record ∈ (run.applyEffects effects).steps → record.id = step →
      record ∈ run.steps := by
  unfold Dex.Run.applyEffects Dex.Run.wake
  have sameSteps : ((run.applyWrites effects.writes).applyPublications effects.publications).steps =
      run.steps := by
    rw [(Dex.Run.applyPublications_frame _ _).1, (Dex.Run.applyWrites_frame _ _).1]
  generalize (run.applyWrites effects.writes).applyPublications effects.publications = published
    at sameSteps
  generalize Dex.Run.waitersOn published (effects.publications.map (fun publication => publication.1))
    = waiters
  rw [← sameSteps] at idsNodup notWaiting ⊢
  clear sameSteps
  induction waiters generalizing published with
  | nil => exact fun _ isRecord _ => isRecord
  | cons evaluated laterWaiters inductionHypothesis =>
    intro record isRecord isId
    simp only [List.foldl_cons] at isRecord
    have keptOnce := evalStep_keeps_record published evaluated step idsNodup notWaiting
    have laterNodup : ((published.evalStep evaluated).steps.map (fun record => record.id)).Nodup := by
      rw [(Dex.Run.evalStep_frame _ _).1]; exact idsNodup
    exact keptOnce record (inductionHypothesis (published.evalStep evaluated)
      (fun other isOther isOtherId => notWaiting other (keptOnce other isOther isOtherId) isOtherId)
      laterNodup record isRecord isId) isId

/-- Clear the invocation of a Step. -/
def clearInvocation (record : Dex.StepRecord σ) : Dex.StepRecord σ :=
  { record with invocation := none }

/-- The whole change of a Complete rule is a `StepsEvolve` when the last
meta-function moves the completed Step out of its phase. -/
theorem stepsEvolve_of_cleared {registered : Option Nat} (base : Dex.Run σ)
    (record : Dex.StepRecord σ) (isRecord : record ∈ base.steps)
    (idsNodup : (base.steps.map (fun record => record.id)).Nodup)
    (hasInvocation : record.invocation.isSome = true) (final : Dex.Run σ)
    (evolve : StepsEvolve registered (base.updateStep record.id clearInvocation) final)
    (leavesPhase : ∀ changed, changed ∈ final.steps → changed.id = record.id →
      changed.phase ≠ record.phase) :
    StepsEvolve registered base final := by
  have clearedMember : clearInvocation record ∈ (base.updateStep record.id clearInvocation).steps :=
    (Dex.Run.mem_updateStep _ _ _ _).mpr ⟨record, isRecord, by simp [clearInvocation]⟩
  have fromCleared : ∀ changed, changed ∈ final.steps →
      RecordStep registered (clearInvocation record) changed → RecordStep registered record changed := by
    intro changed isChanged isStep
    have leaves := leavesPhase changed isChanged isStep.id
    refine ⟨isStep.id, isStep.origin, isStep.executeKept, isStep.waitKept, ?_, ?_⟩
    · intro isQuiet
      rw [isQuiet.2] at hasInvocation
      cases hasInvocation
    · rcases isStep.invocation with ⟨_, _, _, samePhase⟩ | isCleared | isExecute
      · exact absurd samePhase leaves
      · exact Or.inr (Or.inl isCleared)
      · exact Or.inr (Or.inr isExecute)
  have otherMember : ∀ other, other ∈ base.steps → other.id ≠ record.id →
      other ∈ (base.updateStep record.id clearInvocation).steps :=
    fun other isOther isNot =>
      (Dex.Run.mem_updateStep _ _ _ _).mpr ⟨other, isOther, by simp [isNot]⟩
  refine ⟨?_, ?_, ?_, ?_, evolve.seq, evolve.timersNodup, evolve.status⟩
  · intro original isOriginal
    by_cases isRecordId : original.id = record.id
    · rw [eq_of_id_eq idsNodup isOriginal isRecord isRecordId]
      obtain ⟨changed, isChanged, isStep⟩ := evolve.forward _ clearedMember
      exact ⟨changed, isChanged, fromCleared changed isChanged isStep⟩
    · exact evolve.forward original (otherMember original isOriginal isRecordId)
  · intro changed isChanged
    rcases evolve.backward changed isChanged with ⟨middle, isMiddle, isStep⟩ | isNew
    · obtain ⟨original, isOriginal, isMapped⟩ := (Dex.Run.mem_updateStep _ _ _ _).mp isMiddle
      by_cases isRecordId : original.id = record.id
      · simp only [isRecordId, ↓reduceIte] at isMapped
        rw [eq_of_id_eq idsNodup isOriginal isRecord isRecordId] at isMapped
        rw [isMapped] at isStep
        exact Or.inl ⟨record, isRecord, fromCleared changed isChanged isStep⟩
      · simp only [isRecordId, ↓reduceIte] at isMapped
        rw [isMapped] at isStep
        exact Or.inl ⟨original, isOriginal, isStep⟩
    · exact Or.inr isNew
  · intro _
    apply evolve.idsNodup
    rw [Dex.Run.ids_updateStep base record.id clearInvocation (fun _ => rfl)]
    exact idsNodup
  · exact evolve.countersGrow

end DexExpressiveness.DexF.WPF4
