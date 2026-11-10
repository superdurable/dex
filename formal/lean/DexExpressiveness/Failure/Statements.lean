import DexExpressiveness.Failure.Observation
import DexExpressiveness.Theorems

/-!
# Failure transparency: the statements (spec §11.5, plan §5)

"Failures are transparent: they change no observable event except that
external effects may repeat" (spec §11). This file states the theorem and
the step lemmas that Stage 2 proves, one group per work package of plan §5:

| Package | Statements |
| --- | --- |
| WP-F1, normal steps simulate | `normalStep_simulated` |
| WP-F2, failure steps are stutters under α | `failureStep_isStutter` |
| WP-F3, zombie and first-occurrence effects | `workerStep_simulated`, `effectHistory_of_exec` |
| WP-F4, invariants are preserved | `invariant_initial` (proved), `invariant_step` |
| WP-F5, safety and `Tr_free ⊆ Tr_fail` | `freeFiniteTrace_isFailureFiniteTrace`, `safety_failure_forward`, `safety_failure_repetition` |
| WP-F6, liveness | `isProgress_eq_dexProgress`, `liveness_failure` |
| WP-F7, the end-to-end corollary | `endToEnd_safety`, `endToEnd_liveness` |

`step_simulated` combines WP-F1 to WP-F3 and is proved from them. Every
other statement of this file ends in `sorry`, and no other file of the
development has one. Stage 2 proves these statements without changing them
or the definitions they use (`Failure/Config.lean`, `Failure/Rules.lean`,
`Failure/Repeats.lean`, `Failure/Abstraction.lean`, `Failure/Relation.lean`,
`Failure/Observation.lean`).

Every step lemma assumes H3 (`IgnoresAttemptContext`): I2 and I3 are false
without it, because two attempts with different `ctxA` may compute different
effect lists.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## WP-F1: normal steps -/

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
  sorry

/-! ## WP-F2: failure steps are stutters -/

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
  sorry

/-! ## WP-F3: zombie and first-occurrence effects -/

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

/-! ## WP-F4: the invariants -/

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

/-- **WP-F4.** Every DexF step keeps the invariants, for a program that
satisfies H3. -/
theorem invariant_step (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (config : Config σ)
    (invariant : Invariant program config) (action : Action σ) (events : List (Obs σ))
    (next : Config σ) (isStep : step program config action = some (events, next)) :
    Invariant program next := by
  sorry

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

/-! ## WP-F5: safety -/

/-- **WP-F5, the Lemma (no failures)** (spec §11.5): `Tr_free ⊆ Tr_fail`.
"Every Dex run is a DexF run with no failure transition. This is not
literally trivial, because DexF carries more state." The DexF run gives the
start the request ID it likes; its trace erases to the Dex trace. -/
theorem freeFiniteTrace_isFailureFiniteTrace (program : Program σ)
    (ignoresAttemptContext : IgnoresAttemptContext program) (environment : Dex.Environment σ)
    (requests : List (Dex.Request σ)) (trace : List (Dex.Obs σ))
    (isFree : IsFreeFiniteTrace program.failureFreeFlow environment requests trace) :
    ∃ failureRequests failureTrace,
      IsFailureFiniteTrace program environment failureRequests failureTrace ∧
      eraseRetries failureRequests = requests ∧ failureTrace.map Obs.toDex = trace := by
  sorry

/-- **WP-F5, safety, forward** (spec §11.5): "For every program `P` of the
fragment and every finite DexF trace `t` for an outcome word `w`, `erase(t)`
is a finite Dex trace for `w↓`." -/
theorem safety_failure_forward (program : Program σ)
    (hasNoFailurePolicy : HasNoFailurePolicy program) (isSyncOnly : IsSyncOnly program)
    (ignoresAttemptContext : IgnoresAttemptContext program) (environment : Dex.Environment σ)
    (requests : List (Request σ)) (trace : List (Obs σ))
    (isTrace : IsFailureFiniteTrace program environment requests trace) :
    IsFreeFiniteTrace program.failureFreeFlow environment (eraseRetries requests)
      (eraseRepeats trace) := by
  sorry

/-- **WP-F5, safety, the repetition form** (spec §11.5): "Hence `t` is a
repetition of a finite Dex trace." Stated over DexF runs and under H3: the
inserted events equal earlier ones in label only because every attempt
computes the same effect list. -/
theorem safety_failure_repetition (program : Program σ)
    (hasNoFailurePolicy : HasNoFailurePolicy program) (isSyncOnly : IsSyncOnly program)
    (ignoresAttemptContext : IgnoresAttemptContext program) (environment : Dex.Environment σ)
    (requests : List (Request σ)) (trace : List (Obs σ))
    (isTrace : IsFailureFiniteTrace program environment requests trace) :
    ∃ freeTrace,
      IsFreeFiniteTrace program.failureFreeFlow environment (eraseRetries requests) freeTrace ∧
      IsRepetition (trace.map Obs.toDex) freeTrace := by
  sorry

/-! ## WP-F6: liveness -/

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
  sorry

/-- **WP-F6, liveness** (spec §11.5), as two inclusions.

- **Forward, for every admissible outcome word `w`:** `erase(t)` of every
  fair maximal DexF trace `t` for `w`, with its marker, is a fair maximal Dex
  trace for `w↓` with the same marker. The DexF marker is decided by the
  progress rule of spec §11.5 (`Action.isProgress`).
- **Backward, for words without retries:** every fair maximal Dex trace for
  `w↓` is `erase(t)` of a fair maximal DexF trace `t` for `w` with the same
  marker; the run with no failure gives it.

Spec §11.5 states an equality for every admissible word. The backward
inclusion is false for words with retries: a deduplicated StartFlow retry
is a log commit whose stamp strictly advances the partition clock (spec
§11.3, "Log commit versus run `seq`"), so a word with enough retries between
two publications forces time past a wait timer's fire time. A failure-free
run that fires a timer armed later first, because the other is not yet due,
then has no DexF counterpart for that word. The retries cannot be modeled
without stamps, because the code stamps them. -/
theorem liveness_failure (program : Program σ)
    (hasNoFailurePolicy : HasNoFailurePolicy program) (isSyncOnly : IsSyncOnly program)
    (ignoresAttemptContext : IgnoresAttemptContext program) (environment : Dex.Environment σ)
    (word : Sequence (Request σ)) (isAdmissible : IsAdmissibleOutcomeWord environment word)
    (erasedWord : Sequence (Dex.Request σ)) (isErasure : IsRetryErasure word erasedWord)
    (trace : Sequence (Dex.Obs σ)) (marker : Marker) :
    ((∃ failureTrace, IsFailureFairMaximalTrace program word failureTrace marker ∧
        IsRepeatErasure failureTrace trace) →
      (Dex.system program.failureFreeFlow).IsFairMaximalTrace Dex.IsFair some erasedWord trace
        marker) ∧
    (HasNoRetry word →
      (Dex.system program.failureFreeFlow).IsFairMaximalTrace Dex.IsFair some erasedWord trace
        marker →
      ∃ failureTrace, IsFailureFairMaximalTrace program word failureTrace marker ∧
        IsRepeatErasure failureTrace trace) := by
  sorry

/-! ## WP-F7: the end-to-end corollary -/

open DexExpressiveness.L DexExpressiveness.Translation

/-- **WP-F7, end to end, safety** (spec §11.5, "End-to-end corollary"): for
every well-formed L program `S`, "every L trace of `S` is in
`π(Tr_fail(⟦S⟧))`", and "every trace in `π(Tr_fail(⟦S⟧))` is an L trace of
`S` with inserted `out(x)` events, each equal in value to an earlier
`out(x)`". It composes the failure theorem with `expressiveness_of_isWellFormed`
(`Theorems.lean`). -/
theorem endToEnd_safety (program : L.Program) (isWellFormed : program.IsWellFormed) :
    (∀ lTrace, IsLFiniteTrace program lTrace →
      ∃ requests failureTrace,
        IsFailureFiniteTrace (Program.ofFlow ⟦program⟧) environment requests failureTrace ∧
        failureTrace.filterMap (fun event => project event.toDex) = lTrace) ∧
    (∀ requests failureTrace,
      IsFailureFiniteTrace (Program.ofFlow ⟦program⟧) environment requests failureTrace →
      ∃ lTrace, IsLFiniteTrace program lTrace ∧
        IsOutRepetition (failureTrace.filterMap (fun event => project event.toDex)) lTrace) := by
  sorry

/-- **WP-F7, end to end, liveness**: for every well-formed L program `S`,
every admissible L word, and every admissible outcome word `w` whose `w↓` is
the encoded L word: `π(erase(t))` of every fair maximal DexF trace `t` of
`⟦S⟧` for `w`, with its marker, is a fair maximal L trace of `S`; and, when
`w` has no retry, every fair maximal L trace of `S` is such a `π(erase(t))`.
It composes `liveness_failure` with `liveness_of_isWellFormed`. The backward
part needs a word without retries for the reason `liveness_failure` gives. -/
theorem endToEnd_liveness (program : L.Program) (isWellFormed : program.IsWellFormed)
    (lWord : Sequence L.Request) (isAdmissible : L.IsAdmissibleWord lWord)
    (word : Sequence (Request signature))
    (isAdmissibleOutcome : IsAdmissibleOutcomeWord environment word)
    (isErasure : IsRetryErasure word (lWord.map encodeRequest))
    (trace : Sequence L.Event) (marker : Marker) :
    ((∃ failureTrace,
        IsFailureFairMaximalTrace (Program.ofFlow ⟦program⟧) word failureTrace marker ∧
        IsErasedProjection project failureTrace trace) →
      IsLFairMaximalTrace program lWord trace marker) ∧
    (HasNoRetry word → IsLFairMaximalTrace program lWord trace marker →
      ∃ failureTrace,
        IsFailureFairMaximalTrace (Program.ofFlow ⟦program⟧) word failureTrace marker ∧
        IsErasedProjection project failureTrace trace) := by
  sorry

end DexExpressiveness.DexF
