import DexExpressiveness.Failure.Examples.ReviewProbes

/-!
# Failure transparency, Stage 3: why the backward inclusion needs `HasNoRetry`

`liveness_failure` states its backward inclusion only for outcome words
without retries (`HasNoRetry`). This file checks the parts of the argument
that the Lean model can state. The last step, that no DexF run for a word
with 1,000,001 retries between two publications has the failure-free trace,
quantifies over every DexF run of a million-label word and stays an argument
by hand (README, "Failure transparency, Stage 3").

1. **Every commit moves the stamp floor.** In every DexF step the clock does
   not go back, and the last commit stamp stays or becomes the clock, which
   was strictly above it (`step_lastCommitAt`). [START-RETRY] is such a
   commit, with no event (`startRetry_stamps`).
2. **Retries add up.** A finite DexF run from a configuration whose stamps
   are consistent ends with a clock at least the number of its [START-RETRY]
   steps above the stamp it started from (`exec_clock_ge_retries`). So a word
   with `n` retries between two publications puts at least `n` microseconds
   between their commits.
3. **The delay changes the failure-free trace.** In the failure-free model,
   the timing Flow of `Failure/Examples/ReviewProbes.lean` emits `20` from B
   without a delay (`retryTiming_freeTrace`) and, after a delay of 1,000,001
   microseconds before B registers its wait, emits `30` from A instead and
   leaves B waiting forever (`retryTiming_delayedTrace`).
-/

namespace DexExpressiveness.DexF.Examples.RetryTiming

open DexExpressiveness DexExpressiveness.DexF DexExpressiveness.DexF.Examples.ReviewProbes

variable {σ : Dex.Signature}

/-! ## Stamps -/

/-- The least stamp the next log commit may take: one above the last commit
stamp, or `0` before the first commit. -/
def stampFloor (config : Config σ) : Nat :=
  match config.lastCommitAt with
  | none => 0
  | some last => last + 1

/-- Whether a label is [START-RETRY]. -/
def isStartRetryLabel : Action σ → Bool
  | .startRetry _ _ _ => true
  | _ => false

/-- A commit that may stamp now: the stamp floor is at most the clock. -/
theorem stampFloor_le_clock_of_canCommit (config : Config σ) (canCommit : config.canCommit = true) :
    stampFloor config ≤ config.clock := by
  unfold Config.canCommit at canCommit
  unfold stampFloor
  split at canCommit
  · rename_i isNone
    rw [isNone]
    exact Nat.zero_le _
  · rename_i last isLast
    rw [isLast]
    exact Nat.succ_le_of_lt (by simpa using canCommit)

/-- **Every DexF step** keeps the clock or advances it, and keeps the last
commit stamp or commits at the clock with the clock strictly above the last
stamp. -/
theorem step_lastCommitAt (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    config.clock ≤ next.clock ∧
      (next.lastCommitAt = config.lastCommitAt ∨
        (config.canCommit = true ∧ next.lastCommitAt = some config.clock ∧
          next.clock = config.clock)) := by
  cases action with
  | tick duration =>
    simp only [step] at isStep
    split at isStep
    · cases isStep
      exact ⟨Nat.le_add_right _ _, Or.inl rfl⟩
    · cases isStep
  | _ =>
    simp only [step, Config.failAttempt] at isStep
    repeat' (split at isStep)
    all_goals (first | (cases isStep; done) | skip)
    all_goals (cases isStep)
    all_goals (simp_all [Config.commitRun, Config.stampOnly, Config.canServeCommit,
      Config.moveToZombies, Config.removeRealTask, Config.updateRealTask])

/-- [START-RETRY] is a log commit with no event: it stamps the clock, and the
next commit needs a later clock. -/
theorem startRetry_stamps (program : Program σ) (config : Config σ) (requestId : Nat)
    (stepType : σ.StepType) (input : σ.Val) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config (.startRetry requestId stepType input) = some (events, next)) :
    events = [] ∧ next.clock = config.clock ∧ next.lastCommitAt = some config.clock ∧
      config.canCommit = true ∧ next.canCommit = false := by
  simp only [step] at isStep
  repeat' (split at isStep)
  all_goals (first | (cases isStep; done) | skip)
  cases isStep
  simp_all [Config.stampOnly, Config.canServeCommit, Config.canCommit]

/-- A step keeps the stamp floor at most one above the clock when it was. -/
theorem step_stampFloor (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next))
    (isConsistent : stampFloor config ≤ config.clock + 1) :
    stampFloor config + (if isStartRetryLabel action then 1 else 0) ≤ stampFloor next ∧
      stampFloor next ≤ next.clock + 1 := by
  obtain ⟨isClockLater, isStamp⟩ := step_lastCommitAt program config action events next isStep
  have retryCommits : isStartRetryLabel action = true →
      config.canCommit = true ∧ next.lastCommitAt = some config.clock := by
    intro isRetry
    cases action with
    | startRetry requestId stepType input =>
      obtain ⟨_, _, isLast, canCommit, _⟩ :=
        startRetry_stamps program config requestId stepType input events next isStep
      exact ⟨canCommit, isLast⟩
    | _ => simp [isStartRetryLabel] at isRetry
  rcases isStamp with isSame | ⟨canCommit, isLast, isSameClock⟩
  · have isSameFloor : stampFloor next = stampFloor config := by
      simp only [stampFloor, isSame]
    by_cases isRetry : isStartRetryLabel action = true
    · obtain ⟨canCommit, isLast⟩ := retryCommits isRetry
      have isFloor := stampFloor_le_clock_of_canCommit config canCommit
      have isNextFloor : stampFloor next = config.clock + 1 := by simp [stampFloor, isLast]
      omega
    · simp only [isRetry, Bool.false_eq_true, ite_false]
      omega
  · have isFloor := stampFloor_le_clock_of_canCommit config canCommit
    have isNextFloor : stampFloor next = config.clock + 1 := by simp [stampFloor, isLast]
    split <;> omega

/-- **Retries add up.** A finite DexF run from a configuration with a
consistent stamp ends with a clock at least the number of its [START-RETRY]
steps above the stamp floor it started from, less one. -/
theorem exec_clock_ge_retries (program : Program σ) (labels : List (Action σ))
    (config : Config σ) (events : List (Obs σ)) (final : Config σ)
    (isExec : (system program).exec config labels = some (events, final))
    (isConsistent : stampFloor config ≤ config.clock + 1) :
    stampFloor config + labels.countP isStartRetryLabel ≤ stampFloor final ∧
      stampFloor final ≤ final.clock + 1 := by
  induction labels generalizing config events with
  | nil =>
    simp only [TransitionSystem.exec, Option.some.injEq, Prod.mk.injEq] at isExec
    obtain ⟨_, rfl⟩ := isExec
    simpa using isConsistent
  | cons label laterLabels inductionHypothesis =>
    obtain ⟨stepEvents, next, laterEvents, isStep, isLater, _⟩ :=
      (system program).exec_cons config label laterLabels events final isExec
    obtain ⟨isStepFloor, isNextConsistent⟩ :=
      step_stampFloor program config label stepEvents next isStep isConsistent
    obtain ⟨isLaterFloor, isFinalConsistent⟩ :=
      inductionHypothesis next laterEvents isLater isNextConsistent
    refine ⟨?_, isFinalConsistent⟩
    rw [List.countP_cons]
    by_cases isRetry : isStartRetryLabel label = true
    · simp only [isRetry, ite_true] at isStepFloor ⊢
      omega
    · simp only [isRetry, Bool.false_eq_true, ite_false] at isStepFloor ⊢
      omega

/-- Every reachable DexF configuration has a consistent stamp, and its clock
is at least the number of [START-RETRY] steps of the run, less one. -/
theorem reachable_clock_ge_retries (program : Program σ) (labels : List (Action σ))
    (events : List (Obs σ)) (final : Config σ)
    (isExec : (system program).exec (system program).init labels = some (events, final)) :
    labels.countP isStartRetryLabel ≤ final.clock + 1 := by
  have isBound := exec_clock_ge_retries program labels (Config.initial σ) events final isExec
    (by simp [stampFloor, Config.initial])
  have isInitialFloor : stampFloor (Config.initial σ) = 0 := rfl
  omega

/-! ## The delay changes the failure-free trace -/

/-- After the delay: fire A's timer, which is due first and takes the only
message of Channel 0; fire B's timer, whose wait stays unsatisfied; run A. -/
def timingDelayedFinish : List (Dex.Action timingSignature) :=
  [.tick 1, .timerFire 1, .tick 1, .timerFire 1, .tick 1, .stepStart ⟨1, 1⟩, .workerRun 6,
   .workerEffect 6, .workerReturn 6, .tick 1, .stepComplete 6]

/-- **The delay changes the trace.** With 1,000,001 microseconds before B
registers its wait, A takes the message and emits `30`, and B waits forever
with no task, timer, or message left: the failure-free trace of
`retryTiming_freeTrace`, which ends with B's `20`, is gone from this
schedule. -/
theorem retryTiming_delayedTrace :
    ((Dex.system timingFlow).exec (Dex.Config.initial timingSignature)
        (timingPrefix ++ [.tick 1000001] ++ timingUntilB ++ timingDelayedFinish)).map Prod.fst =
      some [.start 0 0, .fx ⟨2, 1⟩ .execute 10, .pub 0 7, .pub 1 8, .fx ⟨1, 1⟩ .execute 30] ∧
    ((Dex.system timingFlow).exec (Dex.Config.initial timingSignature)
        (timingPrefix ++ [.tick 1000001] ++ timingUntilB ++ timingDelayedFinish)).map
        (fun result => result.2.tasks.isEmpty &&
          (result.2.run.map (fun flowRun => flowRun.timers.isEmpty &&
            (flowRun.pendingMessages 0).isEmpty &&
            flowRun.steps.any (fun record => record.id.stepType == 4 &&
              record.phase == .waiting))).getD false) = some true := by
  decide +kernel

end DexExpressiveness.DexF.Examples.RetryTiming
