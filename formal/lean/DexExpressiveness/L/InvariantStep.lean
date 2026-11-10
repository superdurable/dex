import DexExpressiveness.L.Invariant

/-!
# Every L transition keeps the L invariant

Work package 1 of the safety proof. `Invariant.step` is `sorry`; the README
lists it. `Invariant.of_exec` and `Invariant.of_reachable` are proved from it.

The proof of `Invariant.step` is a case analysis on the label, the thread's
phase, and the `Intent`, on the L side only. It may add lemmas to this file.
It may not change `L/Invariant.lean`, `L/Semantics.lean`, or
`L/ThreadLocal.lean`.
-/

namespace DexExpressiveness.L

/-- Every transition of an L program keeps the invariant. -/
theorem Invariant.step (program : Program) (config : Config) (label : Label)
    (events : List Event) (next : Config) (invariant : Invariant program config)
    (isStep : step program config label = some (events, next)) : Invariant program next := by
  sorry

/-- Every finite run keeps the invariant. -/
theorem Invariant.of_exec (program : Program) (labels : List Label) :
    ∀ (config : Config) (events : List Event) (final : Config),
      Invariant program config → (system program).exec config labels = some (events, final) →
      Invariant program final := by
  induction labels with
  | nil =>
    intro config events final invariant isExec
    simp only [TransitionSystem.exec] at isExec
    cases isExec
    exact invariant
  | cons label laterLabels inductionHypothesis =>
    intro config events final invariant isExec
    simp only [TransitionSystem.exec] at isExec
    split at isExec
    · cases isExec
    · rename_i stepEvents next isStep
      split at isExec
      · cases isExec
      · rename_i laterEvents reached isLater
        cases isExec
        exact inductionHypothesis next laterEvents _
          (Invariant.step program config label stepEvents next invariant isStep) isLater

/-- Every reachable configuration satisfies the invariant. -/
theorem Invariant.of_reachable (program : Program) (config : Config)
    (isReachable : (system program).Reachable config) : Invariant program config := by
  obtain ⟨labels, events, isExec⟩ := isReachable
  exact Invariant.of_exec program labels _ events config (Invariant.initial program) isExec

end DexExpressiveness.L
