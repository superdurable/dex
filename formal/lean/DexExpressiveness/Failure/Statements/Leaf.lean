import DexExpressiveness.Failure.Observation
import DexExpressiveness.Failure.Proofs

/-!
# Failure transparency: the leaf statements (spec §11.5, plan §5)

The step lemmas that the work packages WP-F1 to WP-F4 and WP-F6 prove in
parallel, each in its own section:

| Package | Statements |
| --- | --- |
| WP-F1, normal steps simulate | `normalStep_simulated` |
| WP-F2, failure steps are stutters under α | `failureStep_isStutter` |
| WP-F3, zombie and first-occurrence effects | `workerStep_simulated`, `effectHistory_of_exec` |
| WP-F4, invariants are preserved | `invariant_initial` (proved), `invariant_step` |
| WP-F6, the progress rule | `isProgress_eq_dexProgress` |

`step_simulated` combines WP-F1 to WP-F3 and is proved from them.

A work package proves a statement `name` in a new file under
`Failure/Proofs/` as `theorem name_proof` with the same type. That file
imports definitions only, never this file. The package then adds one import
line to `Failure/Proofs.lean` and replaces the `sorry` of `name` here by
`exact name_proof ...`. It never changes a definition or the text of a
statement. If a statement is false, it stops and reports a counterexample.

Every step lemma assumes H3 (`IgnoresAttemptContext`): I2 and I3 are false
without it, because two attempts with different `ctxA` may compute different
effect lists.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

section NormalStepSimulated

/-- **WP-F1.** A normal step maps to its Dex counterpart (spec §11.6,
transition map): [TICK], a request, [ENGINE-REJECT], a [STEP-START] that
computes a snapshot, a Complete rule followed by the abandonment of the
virtual tasks it leaves without a real task, and a TimersFired that fires a
wait timer, as [TIMER-FIRE] of its wait-timer sub-list. -/
theorem normalStep_simulated (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ)
    (isNormal : action.simulationKind = .normal) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    IsStepSimulated program config action events next := by
  exact normalStep_simulated_proof program ignoresAttemptContext config invariant action isNormal
    events next isStep

end NormalStepSimulated

section FailureStepIsStutter

/-- **WP-F2.** A failure step maps to a stutter (spec §11.6: "[CRASH],
[RECOVER], [RECOVER-INTERRUPT], [ATTEMPT-LOST], [ATTEMPT-RETURNED],
[ATTEMPT-HEARTBEAT], [RESULT-STALE], [START-RETRY]: stutter"; also a
[STEP-START] that reuses a retained snapshot, a TimersFired that fires only
retry timers, and an internal commit): no Dex action, no event, and the same
abstraction. [ATTEMPT-EXHAUSTED] is excluded (H1). -/
theorem failureStep_isStutter (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ)
    (isFailure : action.simulationKind = .failure) (isNotExhausting : action.isExhausting = false)
    (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    dexActionsOf program config action = [] ∧ eraseStepEvents config events = [] ∧
      abstractConfig next = abstractConfig config := by
  exact failureStep_isStutter_proof program ignoresAttemptContext config invariant action isFailure
    isNotExhausting events next isStep

end FailureStepIsStutter

section WorkerStepSimulated

/-- **WP-F3.** A Worker step of a task in `Θ` or `Z` maps to the same Worker
step of the virtual task when it is a first occurrence, and to a stutter
otherwise (spec §11.6: "[WORKER-EFFECT] or [ZOMBIE-EFFECT] with `j > h(e, m)`
(so `j = h + 1`): [WORKER-EFFECT] of the virtual task. The same with `j ≤
h(e, m)`: stutter; the event is repeated"). The drop or stale result of the
last real task of a canceled invocation maps to [WORKER-ABANDON] or
[STEP-COMPLETE-STALE] of the virtual task. -/
theorem workerStep_simulated (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ)
    (isWorker : action.simulationKind = .worker) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    IsStepSimulated program config action events next := by
  sorry

end WorkerStepSimulated

section EffectHistoryOfExec

/-- **WP-F3.** The ghost `h` and the trace agree on every finite run (spec
§11.4, "First occurrences come in order 1, 2, 3, …"): the indexes of the
`fx(e, m)` events of the trace are exactly `1, …, h(e, m)`, and each carries
the label `fx⃗(e, m)[j]`. This ties `eraseStepEvents` to `erase` of spec
§11.5. -/
theorem effectHistory_of_exec (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (labels : List (Action σ))
    (events : List (Obs σ)) (config : Config σ)
    (isExec : (system program).exec (system program).init labels = some (events, config)) :
    HasEffectHistory program config events := by
  sorry

end EffectHistoryOfExec

section InvariantInitial

/-- **WP-F4.** The initial configuration satisfies the invariants. -/
theorem invariant_initial (program : Program σ) : Invariant program (Config.initial σ) := by
  refine
    { realTaskIdsNodup := by simp [Config.initial, Config.realTasks]
      realTaskIdsBelow := by simp [Config.initial, Config.realTasks]
      interruptsNotInTasks := by simp [Config.initial]
      interruptsBelow := by simp [Config.initial]
      downIsEmpty := by simp [Config.initial]
      lastNormalCommitAtMost := by simp [Config.initial, Ghost.initial, IsStampAtMost]
      notStarted := by simp [Config.initial, Config.realTasks, Ghost.initial]
      ghostKeysNodup := by simp [Config.initial, Ghost.initial]
      virtualTaskIdsNodup := by simp [Config.initial, Ghost.initial]
      virtualTaskIdsBelow := by simp [Config.initial, Ghost.initial]
      historiesConsistent := by simp [Config.initial, Ghost.initial]
      realTasksFollowHistory := by simp [Config.initial, Config.realTasks]
      liveTasksPassFence := by simp [Config.initial]
      runInvariant := by simp [Config.initial] }

end InvariantInitial

section InvariantStep

/-- **WP-F4.** Every DexF step keeps the invariants, for a program that
satisfies H3. -/
theorem invariant_step (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config action = some (events, next)) :
    Invariant program next := by
  sorry

end InvariantStep

section IsProgressEqDexProgress

/-- **WP-F6.** The progress Boolean that a label carries is the progress rule
of spec §11.5: "a DexF step is a **progress step** if and only if its image
under the transition map of Section 11.6 is a Dex progress step, that is, a
non-empty Dex step other than [TICK]." So the DexF marker of
`TransitionSystem.HasMarker` is the one the spec defines. -/
theorem isProgress_eq_dexProgress (program : Program σ) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ)
    (isNotExhausting : action.isExhausting = false) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    action.isProgress =
      (dexActionsOf program config action).any (Dex.system program.failureFreeFlow).isProgress := by
  exact isProgress_eq_dexProgress_proof program config invariant action isNotExhausting events
    next isStep

end IsProgressEqDexProgress

section StepSimulated

/-- Every DexF step other than [ATTEMPT-EXHAUSTED] is simulated: WP-F1 to
WP-F3 together. -/
theorem step_simulated (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ)
    (isNotExhausting : action.isExhausting = false) (events : List (Obs σ)) (next : Config σ)
    (isStep : step program config action = some (events, next)) :
    IsStepSimulated program config action events next := by
  cases isKind : action.simulationKind with
  | normal =>
    exact normalStep_simulated program ignoresAttemptContext config invariant action isKind
      events next isStep
  | failure =>
    obtain ⟨noActions, noEvents, sameAbstraction⟩ :=
      failureStep_isStutter program ignoresAttemptContext config invariant action isKind
        isNotExhausting events next isStep
    unfold IsStepSimulated
    rw [noActions, noEvents, sameAbstraction]
    rfl
  | worker =>
    exact workerStep_simulated program ignoresAttemptContext config invariant action isKind
      events next isStep

end StepSimulated

end DexExpressiveness.DexF
