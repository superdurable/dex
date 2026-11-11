import DexExpressiveness.Failure.Observation
import DexExpressiveness.Failure.Proofs.WPF1Publish
import DexExpressiveness.Failure.Proofs.WPF1StepStart
import DexExpressiveness.Failure.Proofs.WPF1TimerFire
import DexExpressiveness.Failure.Proofs.WPF1StepCompleteRule

/-!
# WP-F1: every normal step simulates

A normal step (`SimulationKind.normal`) is [TICK], a request, [ENGINE-REJECT],
a [STEP-START] that computes a snapshot, a Complete rule, or a TimersFired
that fires a wait timer. Each case is proved in its own file.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

open WPF1 in
/-- **WP-F1.** A normal step maps to its Dex counterpart (spec §11.6). -/
theorem normalStep_simulated_proof (program : Program σ)
    (_ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ)
    (isNormal : action.simulationKind = .normal) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    IsStepSimulated program config action events next := by
  cases action with
  | tick duration => exact tick_simulated program config duration events next isStep
  | request request =>
    cases request with
    | startFlow requestId stepType input =>
      exact flowStart_simulated program config invariant requestId stepType input events next isStep
    | publish chan value =>
      exact publish_simulated program config invariant chan value events next isStep
  | engineReject request => exact engineReject_simulated program config request events next isStep
  | stepStart stepId isSnapshotReused =>
    cases isSnapshotReused with
    | false => exact stepStart_simulated program config invariant stepId events next isStep
    | true => simp [Action.simulationKind] at isNormal
  | timerFire count isWaitTimerFired =>
    cases isWaitTimerFired with
    | true => exact timerFire_simulated program config invariant count events next isStep
    | false => simp [Action.simulationKind] at isNormal
  | stepComplete taskId =>
    exact stepComplete_simulated program config invariant taskId events next isStep
  | startRetry => simp [Action.simulationKind] at isNormal
  | workerRun => simp [Action.simulationKind] at isNormal
  | workerEffect => simp [Action.simulationKind] at isNormal
  | workerReturn => simp [Action.simulationKind] at isNormal
  | workerAbandon => simp [Action.simulationKind] at isNormal
  | resultStale => simp [Action.simulationKind] at isNormal
  | attemptFailed => simp [Action.simulationKind] at isNormal
  | attemptReturned => simp [Action.simulationKind] at isNormal
  | attemptHeartbeat => simp [Action.simulationKind] at isNormal
  | crash => simp [Action.simulationKind] at isNormal
  | recover => simp [Action.simulationKind] at isNormal
  | recoverInterrupt => simp [Action.simulationKind] at isNormal
  | internalCommit => simp [Action.simulationKind] at isNormal

end DexExpressiveness.DexF
