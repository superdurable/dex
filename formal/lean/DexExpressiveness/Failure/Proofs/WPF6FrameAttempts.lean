import DexExpressiveness.Failure.Observation

/-!
# WP-F6: one-step frame lemmas of DexF, attempts, retry timers, and commits

Only the failure rules ([ATTEMPT-LOST], [ATTEMPT-EXHAUSTED], and an applied
[RECOVER-INTERRUPT]) put an invocation in Backoff or arm a retry timer. A
Complete rule ends the invocation it commits. `liveness_failure` uses these
to show that every invocation of a fair run eventually ends.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-- Outside the failure rules, an attempt in Backoff after a step was in
Backoff before, with the same attempt number. -/
theorem wpf6_backoff_step (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config action = some (events, next))
    (isNotFailed : ∀ stepId decision, action ≠ .attemptFailed stepId decision)
    (isNotInterrupt : ∀ interrupt decision, action ≠ .recoverInterrupt interrupt decision)
    (nextRun : Run σ) (hasNextRun : next.run = some nextRun) (stepId : Dex.StepExecId σ)
    (nextAttempt : Attempt σ) (isNextAttempt : nextRun.findAttempt? stepId = some nextAttempt)
    (isBackoff : nextAttempt.isBackoff = true) :
    ∃ run attempt, config.run = some run ∧ run.findAttempt? stepId = some attempt ∧
      attempt.isBackoff = true ∧ attempt.number = nextAttempt.number := by
  sorry

/-- Outside the failure rules, a retry timer armed after a step was armed
before, with the same fire time. -/
theorem wpf6_retryTimer_step (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config action = some (events, next))
    (isNotFailed : ∀ stepId decision, action ≠ .attemptFailed stepId decision)
    (isNotInterrupt : ∀ interrupt decision, action ≠ .recoverInterrupt interrupt decision)
    (nextRun : Run σ) (hasNextRun : next.run = some nextRun) (timer : RetryTimerId σ)
    (fireTime : Nat) (isArmed : nextRun.retryTimers.get? timer = some fireTime) :
    ∃ run, config.run = some run ∧ run.retryTimers.get? timer = some fireTime := by
  sorry

/-- A Complete rule ends the invocation it commits: after it, the
invocation's history is not live. -/
theorem wpf6_stepComplete_ends (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (taskId : Nat) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config (.stepComplete taskId) = some (events, next))
    (nextInvariant : Invariant program next) (task : Task σ)
    (isFound : config.findRealTask? taskId = some task) (history : InvocationHistory σ)
    (isHistory : next.ghost.findInvocation? task.key = some history) :
    history.outcome ≠ .live := by
  sorry

end DexExpressiveness.DexF
