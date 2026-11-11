import DexExpressiveness.Failure.Examples.CrashRetryZombie
import DexExpressiveness.Failure.Examples.FairPrefixRuns
import DexExpressiveness.Examples.FairRuns

/-!
# Failure transparency, Stage 3: the main statements are not vacuous

Each theorem below shows that the hypotheses of a main statement of
`Failure/Statements/Main.lean` hold together for a run with failures, and
applies the statement to it. All are checked in the kernel.

| Statement | Witness | Run |
| --- | --- | --- |
| `safety_failure_repetition` | `safetyRepetition_isNonvacuous` | `main(x): emit(1); exit(0)` with a crash, two retry fires, a lost attempt, an interrupt, a zombie effect, and a StartFlow retry; `out(1)` three times |
| `liveness_failure`, forward, `✓` | `livenessForward_isNonvacuous_terminated` | the same run, then ticks forever |
| `liveness_failure`, forward, `δ` | `livenessForward_isNonvacuous_deadlocked` | `main(x): emit(1); recv(5)` with a crash, an interrupt, a lost attempt, two retry fires, and two stale zombie results; then blocked forever |
-/

namespace DexExpressiveness.DexF.Examples

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation

/-- Whether a label is a [RETRY-FIRE] that fires no wait timer. -/
def isRetryOnlyFireLabel : Action σ → Bool
  | .timerFire _ isWaitTimerFired => !isWaitTimerFired
  | _ => false

/-- Whether a Dex event is an external effect. -/
def isFxEvent : Dex.Obs σ → Bool
  | .fx _ _ _ => true
  | _ => false

/-! ## Safety -/

/-- **Non-vacuity of `safety_failure_repetition`.** The hypotheses H2, SYNC,
H3, and `Tr_fail` (with H1 and H4) hold for the run of
`Failure/Examples/CrashRetryZombie.lean`. The run has a [CRASH], a
[RETRY-FIRE], and an effect of a zombie task, and its trace has three `fx`
events of which `erase` keeps one. The statement then gives a failure-free
trace of which the trace is a repetition. -/
theorem safetyRepetition_isNonvacuous :
    HasNoFailurePolicy emitFailureProgram ∧ IsSyncOnly emitFailureProgram ∧
    IgnoresAttemptContext emitFailureProgram ∧
    IsFailureFiniteTrace emitFailureProgram environment crashRetryZombieRequests
      crashRetryZombieTrace ∧
    crashRetryZombieLabels.any isCrashLabel = true ∧
    crashRetryZombieLabels.any isRetryOnlyFireLabel = true ∧
    scriptHasStep emitFailureProgram crashRetryZombieScript
      (fun scriptStep => match scriptStep.action with
        | .workerEffect _ _ => scriptStep.isZombieStep | _ => false) = true ∧
    (crashRetryZombieTrace.map Obs.toDex).countP isFxEvent = 3 ∧
    (eraseRepeats crashRetryZombieTrace).countP isFxEvent = 1 ∧
    ∃ freeTrace,
      IsFreeFiniteTrace emitFailureProgram.failureFreeFlow environment
        (eraseRetries crashRetryZombieRequests) freeTrace ∧
      IsRepetition (crashRetryZombieTrace.map Obs.toDex) freeTrace := by
  have ignoresAttemptContext : IgnoresAttemptContext emitFailureProgram := fun _ _ => rfl
  refine ⟨trivial, trivial, ignoresAttemptContext, crashRetryZombie_isFailureFiniteTrace,
    by decide +kernel, by decide +kernel, crashRetryZombie_hasFailures.2.2.2, by decide +kernel,
    by decide +kernel, ?_⟩
  exact safety_failure_repetition emitFailureProgram trivial trivial ignoresAttemptContext
    environment crashRetryZombieRequests crashRetryZombieTrace
    crashRetryZombie_isFailureFiniteTrace

/-! ## Liveness, `✓` -/

/-- The outcome word of the run: the start and its retry. -/
def crashRetryZombieWord : Sequence (Request σ) := Sequence.ofList crashRetryZombieRequests

/-- `w↓`: the start. -/
def crashRetryZombieErasedWord : Sequence (Dex.Request σ) :=
  Sequence.ofList (eraseRetries crashRetryZombieRequests)

/-- The run is executable and its events are the trace of the example. -/
theorem crashRetryZombie_scheduleEvents :
    ((system emitFailureProgram).exec (system emitFailureProgram).init
      crashRetryZombieLabels).isSome = true ∧
    scheduleEvents emitFailureProgram crashRetryZombieLabels = crashRetryZombieTrace := by
  decide +kernel

/-- The run of `Failure/Examples/CrashRetryZombie.lean`, then ticks forever, is
a fair maximal DexF trace with marker `✓`. -/
theorem crashRetryZombie_isFailureFairMaximalTrace :
    IsFailureFairMaximalTrace emitFailureProgram crashRetryZombieWord
      (Sequence.ofList crashRetryZombieTrace) .terminated := by
  have isTrace := prefixThenTicks_isFailureFairMaximalTrace_of_checks emitFailureProgram
    crashRetryZombieLabels crashRetryZombie_scheduleEvents.1 (by decide +kernel)
    (by decide +kernel) (by decide +kernel) .terminated
    (prefixThenTicks_hasMarker_terminated _ _ (by decide +kernel))
  rw [crashRetryZombie_scheduleEvents.2] at isTrace
  exact isTrace

/-- **Non-vacuity of `liveness_failure`, forward, marker `✓`.** The outcome word
of the run is admissible and has a retry, the run has a [CRASH] and a
[RETRY-FIRE], and it is a fair maximal DexF trace with marker `✓` that
satisfies H1, H4, D6, and HP (with H2, SYNC, and H3 on the program). The
statement then gives a fair maximal Dex trace for `w↓` with marker `✓`. -/
theorem livenessForward_isNonvacuous_terminated :
    IsAdmissibleOutcomeWord environment crashRetryZombieWord ∧
    ¬ HasNoRetry crashRetryZombieWord ∧
    IsRetryErasure crashRetryZombieWord crashRetryZombieErasedWord ∧
    crashRetryZombieLabels.any isCrashLabel = true ∧
    crashRetryZombieLabels.any isRetryOnlyFireLabel = true ∧
    ∃ erasedTrace,
      (∃ failureTrace,
        IsFailureFairMaximalTrace emitFailureProgram crashRetryZombieWord failureTrace .terminated ∧
        IsRepeatErasure failureTrace erasedTrace) ∧
      (Dex.system emitFailureProgram.failureFreeFlow).IsFairMaximalTrace Dex.IsFair some
        crashRetryZombieErasedWord erasedTrace .terminated := by
  have isAdmissible : IsAdmissibleOutcomeWord environment crashRetryZombieWord :=
    isAdmissibleOutcomeWord_ofList environment crashRetryZombieRequests
      (isAdmissibleOutcomePrefix_of_check _ (by decide +kernel)) (by decide +kernel)
  have isErasure : IsRetryErasure crashRetryZombieWord crashRetryZombieErasedWord :=
    isRetryErasure_ofList crashRetryZombieRequests
  obtain ⟨erasedTrace, isRepeatErasure⟩ :=
    exists_isRepeatErasure (Sequence.ofList crashRetryZombieTrace)
  refine ⟨isAdmissible, ?_, isErasure, by decide +kernel, by decide +kernel, erasedTrace,
    ⟨_, crashRetryZombie_isFailureFairMaximalTrace, isRepeatErasure⟩, ?_⟩
  · intro hasNoRetry
    have isNotRetry := hasNoRetry 0 startRequest startRequest rfl (by decide +kernel)
    revert isNotRetry
    decide +kernel
  · exact (liveness_failure emitFailureProgram trivial trivial (fun _ _ => rfl) environment
      crashRetryZombieWord isAdmissible crashRetryZombieErasedWord isErasure erasedTrace
      .terminated).1 ⟨_, crashRetryZombie_isFailureFairMaximalTrace, isRepeatErasure⟩

/-! ## Liveness, `δ` -/

/-- The DexF program of `main(x): emit(1); x := recv(5)` translated. -/
def waitingFailureProgram : Program σ :=
  Program.ofFlow ⟦DexExpressiveness.Examples.FairRuns.waitingProgram⟧

/-- Its start request: request ID 0, input `0`. -/
def waitingStartRequest : Request σ := .startFlow 0 StepType.start (DexVal.value (.int 0))

/-- The script: the first Step runs; the `emit` attempt emits, the server
crashes and recovers, the interrupt fails the attempt, its retry fires and
emits again, and the zombie's result is stale; the `recv` Step's WaitFor
attempt is lost, its retry fires and registers the wait, and the zombie's
result is stale. No message ever arrives. -/
def waitingCrashRetryScript : List Instruction :=
  [.act (.request waitingStartRequest),
   .act (.tick 1), .startPending, .workerRun 0, .workerReturn 0, .act (.tick 1), .complete 0,
   .act (.tick 1), .startPending, .workerRun 0, .workerEffect 0,
   .act .crash, .act .recover,
   .act (.tick 1), .interruptFirst 5,
   .act (.tick 10), .fireFirst,
   .act (.tick 1), .startPending, .workerRun 0, .workerEffect 0, .workerReturn 0,
   .act (.tick 1), .complete 0,
   .workerReturn 0, .act (.tick 1), .stale 0,
   .act (.tick 1), .startPending, .workerRun 0, .act (.tick 1), .failRunning 5,
   .act (.tick 10), .fireFirst,
   .act (.tick 1), .startPending, .workerRun 0, .workerReturn 0, .act (.tick 1), .complete 0,
   .workerReturn 0, .act (.tick 1), .stale 0]

/-- The labels of the script. -/
def waitingCrashRetryLabels : List (Action σ) :=
  scriptLabels waitingFailureProgram waitingCrashRetryScript

/-- The outcome word: the start only. -/
def waitingWord : Sequence (Request σ) :=
  Sequence.ofList (waitingCrashRetryLabels.filterMap Action.requestOf)

/-- `w↓`: the start. -/
def waitingErasedWord : Sequence (Dex.Request σ) :=
  Sequence.ofList (eraseRetries (waitingCrashRetryLabels.filterMap Action.requestOf))

/-- Every label is enabled, every step is simulated, the invariants hold after
every step, the requests are the start only, and the L projection of the
trace is `begin(0), out(1), out(1)`. -/
theorem waitingCrashRetry_passes :
    isScriptPassing waitingFailureProgram waitingCrashRetryScript = true ∧
    waitingCrashRetryLabels.length = waitingCrashRetryScript.length ∧
    waitingCrashRetryLabels.filterMap Action.requestOf = [waitingStartRequest] ∧
    (scheduleEvents waitingFailureProgram waitingCrashRetryLabels).filterMap
        (fun event => project event.toDex) =
      [.begin (.int 0), .out (.int 1), .out (.int 1)] := by
  decide +kernel

/-- The run, then ticks forever, is a fair maximal DexF trace with marker
`δ`. -/
theorem waitingCrashRetry_isFailureFairMaximalTrace :
    IsFailureFairMaximalTrace waitingFailureProgram waitingWord
      (Sequence.ofList (scheduleEvents waitingFailureProgram waitingCrashRetryLabels))
      .deadlocked := by
  have isSome : ((system waitingFailureProgram).exec (system waitingFailureProgram).init
      waitingCrashRetryLabels).isSome = true := by
    decide +kernel
  exact prefixThenTicks_isFailureFairMaximalTrace_of_checks waitingFailureProgram
    waitingCrashRetryLabels isSome (by decide +kernel) (by decide +kernel) (by decide +kernel)
    .deadlocked
    (prefixThenTicks_hasMarker_deadlocked _ _ _ _ (exec_of_isSome _ _ isSome) (by decide +kernel))

/-- **Non-vacuity of `liveness_failure`, forward, marker `δ`.** The outcome word
is the start alone, admissible and without retries; the run has a [CRASH], a
[RETRY-FIRE], and a repeated effect, and it is a fair maximal DexF trace with
marker `δ` that satisfies H1, H4, D6, and HP. The statement then gives a fair
maximal Dex trace for `w↓` with marker `δ`. -/
theorem livenessForward_isNonvacuous_deadlocked :
    IsAdmissibleOutcomeWord environment waitingWord ∧ HasNoRetry waitingWord ∧
    IsRetryErasure waitingWord waitingErasedWord ∧
    waitingCrashRetryLabels.any isCrashLabel = true ∧
    waitingCrashRetryLabels.any isRetryOnlyFireLabel = true ∧
    ∃ erasedTrace,
      (∃ failureTrace,
        IsFailureFairMaximalTrace waitingFailureProgram waitingWord failureTrace .deadlocked ∧
        IsRepeatErasure failureTrace erasedTrace) ∧
      (Dex.system waitingFailureProgram.failureFreeFlow).IsFairMaximalTrace Dex.IsFair some
        waitingErasedWord erasedTrace .deadlocked := by
  have isRequests := waitingCrashRetry_passes.2.2.1
  have isAdmissible : IsAdmissibleOutcomeWord environment waitingWord :=
    isAdmissibleOutcomeWord_ofList environment _
      (isAdmissibleOutcomePrefix_of_check _ (by rw [isRequests]; decide +kernel))
      (by rw [isRequests]; simp)
  have isErasure : IsRetryErasure waitingWord waitingErasedWord := isRetryErasure_ofList _
  obtain ⟨erasedTrace, isRepeatErasure⟩ := exists_isRepeatErasure
    (Sequence.ofList (scheduleEvents waitingFailureProgram waitingCrashRetryLabels))
  refine ⟨isAdmissible, ?_, isErasure, by decide +kernel, by decide +kernel, erasedTrace,
    ⟨_, waitingCrashRetry_isFailureFairMaximalTrace, isRepeatErasure⟩, ?_⟩
  · intro position first request _ isLater
    simp [waitingWord, isRequests, Sequence.ofList] at isLater
  · exact (liveness_failure waitingFailureProgram trivial trivial (fun _ _ => rfl) environment
      waitingWord isAdmissible waitingErasedWord isErasure erasedTrace .deadlocked).1
      ⟨_, waitingCrashRetry_isFailureFairMaximalTrace, isRepeatErasure⟩

end DexExpressiveness.DexF.Examples
