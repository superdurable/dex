import DexExpressiveness.Failure.Proofs.WPF4Commit

/-!
# WP-F4: commits that change one invocation to Pending

[ATTEMPT-LOST] with RetryAt, [ATTEMPT-RETURNED], [ATTEMPT-HEARTBEAT], and an
applied [RECOVER-INTERRUPT] with RetryAt change only the current invocation
of one Step `e`: its state becomes Pending (or stays), its attempt state
changes, and a retry timer of `e` may be armed. `invariant_singleRecord`
proves `Invariant` for every such commit from facts about `e` alone.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- The reconcile changes no history when every live history is current and
every current invocation has a history. -/
theorem reconciled_find_eq (ghost : Ghost σ) (isNormal : Bool) (seq clock : Nat)
    (completed : Option (InvocationKey σ)) (base : Dex.Run σ)
    (liveCurrent : ∀ key history, ghost.findInvocation? key = some history →
      history.outcome = .live → key ∈ currentKeys base)
    (currentFound : ∀ key, key ∈ currentKeys base → ∃ history, ghost.findInvocation? key = some history)
    (key : InvocationKey σ) :
    ((countedGhost ghost isNormal seq clock).reconcile completed base).findInvocation? key =
      ghost.findInvocation? key := by
  cases isFound : ghost.findInvocation? key with
  | some history =>
    rw [reconciled_find_of_some _ _ _ _ _ _ _ _ isFound]
    congr 1
    unfold markHistory
    by_cases isLive : history.outcome = .live
    · have isCurrent := liveCurrent key history isFound isLive
      simp only [List.contains_iff_mem.mpr isCurrent, Bool.not_true, Bool.and_false,
        Bool.false_eq_true, ↓reduceIte]
    · simp [isLive]
  | none =>
    rw [reconciled_find_of_none _ _ _ _ _ _ _ isFound]
    by_cases isCurrent : key ∈ currentKeys base
    · obtain ⟨history, isHistory⟩ := currentFound key isCurrent
      rw [isFound] at isHistory
      cases isHistory
    · simp [isCurrent]

/-- The Step change of a single-record commit. -/
def pendInvocation (method : Dex.Method) (record : Dex.StepRecord σ) : Dex.StepRecord σ :=
  { record with invocation := some { method := method, state := .pending } }

/-- The members of the Step table after a single-record change. -/
theorem mem_singleRecord {steps : List (Dex.StepRecord σ)} {step : Dex.StepExecId σ}
    {method : Dex.Method} {changed : Dex.StepRecord σ}
    (isChanged : changed ∈ steps.map (fun record =>
      if record.id = step then pendInvocation method record else record)) :
    ∃ record, record ∈ steps ∧
      ((record.id = step ∧ changed = pendInvocation method record) ∨
        (record.id ≠ step ∧ changed = record)) := by
  obtain ⟨record, isRecord, isMapped⟩ := List.mem_map.mp isChanged
  refine ⟨record, isRecord, ?_⟩
  by_cases isStep : record.id = step
  · left
    simp only [isStep, ↓reduceIte] at isMapped
    exact ⟨isStep, isMapped.symm⟩
  · right
    simp only [isStep, ↓reduceIte] at isMapped
    exact ⟨isStep, isMapped.symm⟩

/-- The IDs after a single-record change. -/
theorem ids_singleRecord (steps : List (Dex.StepRecord σ)) (step : Dex.StepExecId σ)
    (method : Dex.Method) :
    (steps.map (fun record => if record.id = step then pendInvocation method record else record)).map
      (fun record => record.id) = steps.map (fun record => record.id) := by
  rw [List.map_map]
  apply List.map_congr_left
  intro record _
  by_cases isStep : record.id = step <;> simp [isStep, pendInvocation]

/-- The current keys after a single-record change of an invocation of the
same method. -/
theorem currentKeys_singleRecord (base : Dex.Run σ) (newBase : Dex.Run σ) (step : Dex.StepExecId σ)
    (method : Dex.Method)
    (isSteps : newBase.steps = base.steps.map (fun record =>
      if record.id = step then pendInvocation method record else record))
    (sameMethod : ∀ record, record ∈ base.steps → record.id = step →
      ∃ state, record.invocation = some { method := method, state := state })
    (key : InvocationKey σ) : key ∈ currentKeys newBase ↔ key ∈ currentKeys base := by
  rw [mem_currentKeys, mem_currentKeys, isSteps]
  constructor
  · rintro ⟨changed, invocation, isChanged, hasInvocation, isId, isMethod⟩
    obtain ⟨record, isRecord, ⟨isStep, isPended⟩ | ⟨_, isSame⟩⟩ := mem_singleRecord isChanged
    · obtain ⟨state, hasOld⟩ := sameMethod record isRecord isStep
      subst isPended
      simp only [pendInvocation, Option.some.injEq] at hasInvocation
      refine ⟨record, _, isRecord, hasOld, isId, ?_⟩
      rw [← hasInvocation] at isMethod
      exact isMethod
    · subst isSame
      exact ⟨changed, invocation, isRecord, hasInvocation, isId, isMethod⟩
  · rintro ⟨record, invocation, isRecord, hasInvocation, isId, isMethod⟩
    by_cases isStep : record.id = step
    · obtain ⟨state, hasOld⟩ := sameMethod record isRecord isStep
      rw [hasOld] at hasInvocation
      cases hasInvocation
      refine ⟨pendInvocation method record, { method := method, state := .pending },
        List.mem_map.mpr ⟨record, isRecord, by simp [isStep]⟩, rfl, isId, isMethod⟩
    · exact ⟨record, invocation, List.mem_map.mpr ⟨record, isRecord, by simp [isStep]⟩,
        hasInvocation, isId, isMethod⟩

end DexExpressiveness.DexF.WPF4
