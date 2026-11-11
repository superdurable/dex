import DexExpressiveness.Failure.Proofs.WPF4SingleInvariant

/-!
# WP-F4: helpers for [STEP-START]

A [STEP-START] that computes a snapshot sets `par(e, m)` on a history whose
arguments were absent, and takes the next virtual task ID. The lemmas here
describe the arguments list after that update.
-/

namespace DexExpressiveness.DexF.WPF4

open DexExpressiveness

variable {σ : Dex.Signature}

/-- Setting arguments on a history without arguments adds them to the list of
arguments, up to order. -/
theorem filterMap_set_arguments_perm (map : AssocList (InvocationKey σ) (InvocationHistory σ))
    (key : InvocationKey σ) (history value : InvocationHistory σ) (arguments : Arguments σ)
    (isFound : AssocList.get? map key = some history) (noArguments : history.arguments = none)
    (hasArguments : value.arguments = some arguments) :
    ((AssocList.set map key value).filterMap (fun entry => entry.2.arguments)).Perm
      (arguments :: map.filterMap (fun entry => entry.2.arguments)) := by
  induction map with
  | nil => simp [AssocList.get?] at isFound
  | cons head rest inductionHypothesis =>
    obtain ⟨headKey, headValue⟩ := head
    by_cases isKey : headKey = key
    · simp only [AssocList.get?, isKey, ↓reduceIte, Option.some.injEq] at isFound
      subst isFound
      simp [AssocList.set, isKey, List.filterMap_cons, hasArguments, noArguments]
    · simp only [AssocList.get?, isKey, ↓reduceIte] at isFound
      simp only [AssocList.set, isKey, ↓reduceIte, List.filterMap_cons]
      cases headValue.arguments with
      | none => exact inductionHypothesis isFound
      | some headArguments =>
        exact ((inductionHypothesis isFound).cons headArguments).trans (List.Perm.swap _ _ _)

/-- Setting the arguments of a history keeps the virtual task IDs distinct
when the history had none and the new ID is fresh. -/
theorem virtualIds_setArguments (ghost : Ghost σ) (key : InvocationKey σ)
    (history : InvocationHistory σ) (arguments : Arguments σ)
    (isFound : ghost.findInvocation? key = some history) (noArguments : history.arguments = none)
    (idsNodup : ((ghost.invocations.filterMap (fun entry => entry.2.arguments)).map
      (fun arguments => arguments.virtualTaskId)).Nodup)
    (isFresh : ∀ other, other ∈ ghost.invocations.filterMap (fun entry => entry.2.arguments) →
      other.virtualTaskId ≠ arguments.virtualTaskId) :
    (((ghost.updateInvocation key (fun history => { history with arguments := some arguments })).invocations.filterMap
      (fun entry => entry.2.arguments)).map (fun arguments => arguments.virtualTaskId)).Nodup := by
  unfold Ghost.updateInvocation
  rw [isFound]
  have isPerm := filterMap_set_arguments_perm ghost.invocations key history
    { history with arguments := some arguments } arguments isFound noArguments rfl
  refine (isPerm.map _).nodup_iff.mpr ?_
  simp only [List.map_cons, List.nodup_cons]
  refine ⟨?_, idsNodup⟩
  intro isMember
  obtain ⟨other, isOther, isSame⟩ := List.mem_map.mp isMember
  exact isFresh other isOther isSame

end DexExpressiveness.DexF.WPF4
