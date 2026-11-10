import DexExpressiveness.Failure.Composite.WPF6Abstraction

/-!
# WP-F6: a fair DexF run settles

Facts along an infinite DexF run with the fairness assumptions of spec
§11.5:

- every configuration satisfies the invariants;
- there is a quiet point after which the server is up, `I` is empty, and no
  failure that FF counts and no [RECOVER] occurs ("The server is eventually
  up for good");
- every invocation eventually stops being live, every real task (also a
  zombie) eventually leaves for good, and so every virtual task of α
  eventually leaves and every pending invocation of α eventually stops
  being pending.

These give F3, F4, and F5 of the Dex run that α maps the DexF run to.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- Every configuration of a DexF run satisfies the invariants. -/
theorem wpf6_invariant_at (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (index : Nat) : Invariant program (run.states index) := by
  sorry

/-- The quiet point of a fair run: from it on the server is up, `I` is empty,
and no [CRASH], [ATTEMPT-LOST], [ATTEMPT-EXHAUSTED], [ATTEMPT-RETURNED], or
[RECOVER] occurs. -/
theorem wpf6_exists_quietPoint (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (isFair : IsFair run) :
    ∃ quiet, ∀ index, quiet ≤ index →
      (run.states index).isUp = true ∧ (run.states index).interrupts = [] ∧
      Fairness.Action.isCountedFailure (run.labels index) = false ∧
      run.labels index ≠ .recover := by
  sorry

/-- In a fair run every invocation eventually stops being live: it starts,
returns, and commits, or a cancel or a close ends it. -/
theorem wpf6_live_eventually_ends (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (isFair : IsFair run) (key : InvocationKey σ) (start : Nat) :
    ∃ index, start ≤ index ∧ ∀ history,
      (run.states index).ghost.findInvocation? key = some history → history.outcome ≠ .live := by
  sorry

/-- In a fair run every real task ID, of `Θ` or `Z`, is eventually gone for
good ("zombies settle"). -/
theorem wpf6_realTask_eventually_gone (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (isFair : IsFair run) (taskId start : Nat) :
    ∃ index, start ≤ index ∧ ∀ later, index ≤ later →
      ∀ task, task ∈ (run.states later).realTasks → task.base.id ≠ taskId := by
  sorry

/-- In a fair run no virtual task of α stays forever. -/
theorem wpf6_virtualTask_eventually_gone (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (isFair : IsFair run) (virtualTaskId start : Nat) :
    ∃ index, start ≤ index ∧ (abstractConfig (run.states index)).findTask? virtualTaskId = none := by
  sorry

/-- In a fair run no invocation stays pending under α forever. -/
theorem wpf6_abstractPending_eventually_ends (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program)
    (run : InfiniteRun (Config σ) (Action σ)) (isRun : (system program).IsRun run)
    (isFair : IsFair run) (stepId : Dex.StepExecId σ) (method : Dex.Method) (start : Nat) :
    ∃ index, start ≤ index ∧ ∀ dexRun record,
      (abstractConfig (run.states index)).run = some dexRun → dexRun.findStep? stepId = some record →
      record.invocation ≠ some { method := method, state := .pending } := by
  sorry

end DexExpressiveness.DexF
