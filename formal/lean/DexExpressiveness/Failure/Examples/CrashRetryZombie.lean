import DexExpressiveness.Failure.Examples.Script

/-!
# Example: a crash, a retry, and a zombie effect, checked in the kernel

The program is `main(x): emit(1); exit(0)`, translated (spec §8.2). The DexF
run below has every kind of failure the theorem is about:

1. The first attempt of the `emit` Step runs its handler and fails
   ([ATTEMPT-LOST]); its task becomes a zombie, which then emits `out(1)`
   with index 1, the first occurrence.
2. The retry timer fires ([RETRY-FIRE]), and the retry starts on the retained
   snapshot. It emits `out(1)` again, a repeat, and returns.
3. The server crashes and recovers; the interrupt fails the running attempt
   ([RECOVER-INTERRUPT]). A second retry emits `out(1)` a third time and
   completes.
4. The first zombie's result is ignored ([RESULT-STALE]); the second zombie is
   dropped. Earlier, the client re-sent the StartFlow ([START-RETRY]).
5. The `exit` Step runs and the Flow completes.

The checks, all by kernel evaluation:

- every label is enabled, every step is simulated (`IsStepSimulated`), and
  the invariants hold after every step (`crashRetryZombie_passes`);
- the run has a crash, a retry fire, a start on a retained snapshot, and an
  effect of a zombie task;
- its trace is in `Tr_fail` (`IsFailureFiniteTrace`);
- `erase` of the trace is a Dex trace for `w↓`, the instance of
  `safety_failure_forward`, and the trace is a repetition of it;
- end to end: its L projection `begin(7), out(1), out(1), out(1), exit(0)`
  is the L trace `begin(7), out(1), exit(0)` with two inserted `out(1)`.
-/

namespace DexExpressiveness.DexF.Examples

open DexExpressiveness DexExpressiveness.L DexExpressiveness.Translation

/-- `main(x): emit(1); exit(0)`. -/
def emitProgram : L.Program :=
  { procs := []
    mainParam := 0
    mainBody := .seq (.emit (.lit (.int 1))) (.exit (.lit (.int 0)))
    staticMutexCount := 0 }

/-- The DexF program of its translation. -/
def emitFailureProgram : Program σ := Program.ofFlow ⟦emitProgram⟧

/-- The start request: request ID 0, input `7`. -/
def startRequest : Request σ := .startFlow 0 StepType.start (DexVal.value (.int 7))

/-- The script of the run. -/
def crashRetryZombieScript : List Instruction :=
  [.act (.request startRequest),
   .act (.tick 1), .startPending, .workerRun 0, .workerReturn 0,
   .act (.tick 1), .complete 0,
   .act (.tick 1), .act (.startRetry 0 StepType.start (DexVal.value (.int 7))),
   .act (.tick 1), .startPending, .workerRun 0,
   .act (.tick 1), .failRunning 5,
   .workerEffect 0,
   .act (.tick 10), .fireFirst,
   .act (.tick 1), .startPending, .workerRun 0, .workerEffect 0, .workerReturn 0,
   .act .crash, .act .recover,
   .act (.tick 1), .interruptFirst 5,
   .act (.tick 10), .fireFirst,
   .act (.tick 1), .startPending, .workerRun 0, .workerEffect 0, .workerReturn 0,
   .act (.tick 1), .complete 0,
   .workerReturn 0,
   .act (.tick 1), .stale 0, .abandon 0,
   .act (.tick 1), .startPending, .workerRun 0, .workerReturn 0,
   .act (.tick 1), .complete 0]

/-- The labels of the run. -/
def crashRetryZombieLabels : List (Action σ) :=
  scriptLabels emitFailureProgram crashRetryZombieScript

/-- The DexF trace of the run, with ghost indexes. -/
def crashRetryZombieTrace : List (Obs σ) :=
  failureTraceOf emitFailureProgram crashRetryZombieLabels

/-- The requests the run gives outcomes to: the start and its retry. -/
def crashRetryZombieRequests : List (Request σ) :=
  crashRetryZombieLabels.filterMap Action.requestOf

/-- Every label is enabled, every step is simulated, and the invariants hold
after every step. -/
theorem crashRetryZombie_passes :
    isScriptPassing emitFailureProgram crashRetryZombieScript = true := by
  decide +kernel

/-- The run has a crash, a retry fire that fires no wait timer, a start that
reuses a retained snapshot, and an effect of a zombie task. -/
theorem crashRetryZombie_hasFailures :
    scriptHasStep emitFailureProgram crashRetryZombieScript
        (fun scriptStep => match scriptStep.action with | .crash => true | _ => false) = true ∧
      scriptHasStep emitFailureProgram crashRetryZombieScript
        (fun scriptStep => match scriptStep.action with
          | .timerFire _ isWaitTimerFired => !isWaitTimerFired | _ => false) = true ∧
      scriptHasStep emitFailureProgram crashRetryZombieScript
        (fun scriptStep => match scriptStep.action with
          | .stepStart _ isSnapshotReused => isSnapshotReused | _ => false) = true ∧
      scriptHasStep emitFailureProgram crashRetryZombieScript
        (fun scriptStep => match scriptStep.action with
          | .workerEffect _ _ => scriptStep.isZombieStep | _ => false) = true := by
  decide +kernel

/-- The requests are the start and one retry; `w↓` keeps only the start. -/
theorem crashRetryZombie_requests :
    crashRetryZombieRequests = [startRequest, startRequest] ∧
      eraseRetries crashRetryZombieRequests = [startRequest.toDex] := by
  decide +kernel

/-- The trace is in `Tr_fail`. -/
theorem crashRetryZombie_isFailureFiniteTrace :
    IsFailureFiniteTrace emitFailureProgram environment crashRetryZombieRequests
      crashRetryZombieTrace := by
  cases isExec : (system emitFailureProgram).exec (system emitFailureProgram).init
      crashRetryZombieLabels with
  | none =>
    have isSome : ((system emitFailureProgram).exec (system emitFailureProgram).init
        crashRetryZombieLabels).isSome = true := by decide +kernel
    rw [isExec] at isSome
    cases isSome
  | some result =>
    refine ⟨crashRetryZombieLabels, result.2, ?_, ?_, ?_, rfl, ?_⟩
    · simp only [crashRetryZombieTrace, failureTraceOf, isExec]
    · intro label isMember
      have allPass : crashRetryZombieLabels.all (fun label => !label.isExhausting) = true := by
        decide +kernel
      simpa using List.all_eq_true.mp allPass label isMember
    · intro label isMember
      have allPass :
          crashRetryZombieLabels.all (fun label => !label.isNonRetryableFailure) = true := by
        decide +kernel
      simpa using List.all_eq_true.mp allPass label isMember
    · exact isAdmissibleOutcomePrefix_of_check _ (by decide +kernel)

/-- The L projection of the DexF trace: the effect occurs three times. -/
theorem crashRetryZombie_projection :
    crashRetryZombieTrace.filterMap (fun event => project event.toDex) =
      [.begin (.int 7), .out (.int 1), .out (.int 1), .out (.int 1), .exit (.int 0)] := by
  decide +kernel

/-- `erase` of the trace keeps one `out(1)`. -/
theorem crashRetryZombie_erasedProjection :
    (eraseRepeats crashRetryZombieTrace).filterMap project =
      [.begin (.int 7), .out (.int 1), .exit (.int 0)] := by
  decide +kernel

/-- The Dex schedule that the transition map gives the run. -/
def crashRetryZombieDexSchedule : List (Dex.Action σ) :=
  dexScheduleOf emitFailureProgram crashRetryZombieLabels

/-- The instance of `safety_failure_forward`: `erase` of the trace is a finite
Dex trace for `w↓`, reached by the Dex actions of the transition map. -/
theorem crashRetryZombie_erasesToFreeTrace :
    IsFreeFiniteTrace ⟦emitProgram⟧ environment (eraseRetries crashRetryZombieRequests)
      (eraseRepeats crashRetryZombieTrace) := by
  cases isExec : (Dex.system ⟦emitProgram⟧).exec (Dex.system ⟦emitProgram⟧).init
      crashRetryZombieDexSchedule with
  | none =>
    have isSome : ((Dex.system ⟦emitProgram⟧).exec (Dex.system ⟦emitProgram⟧).init
        crashRetryZombieDexSchedule).isSome = true := by decide +kernel
    rw [isExec] at isSome
    cases isSome
  | some result =>
    have isTrace : result.1 = eraseRepeats crashRetryZombieTrace := by
      have isMapped : ((Dex.system ⟦emitProgram⟧).exec (Dex.system ⟦emitProgram⟧).init
          crashRetryZombieDexSchedule).map (fun final => final.1) =
            some (eraseRepeats crashRetryZombieTrace) := by
        decide +kernel
      rw [isExec] at isMapped
      exact Option.some.inj isMapped
    have isRequests : eraseRetries crashRetryZombieRequests =
        crashRetryZombieDexSchedule.filterMap (Dex.system ⟦emitProgram⟧).requestOf := by
      decide +kernel
    refine ⟨crashRetryZombieDexSchedule, result.2, ?_, isRequests, ?_⟩
    · rw [isExec, ← isTrace]
    · rw [isRequests]
      exact DexExpressiveness.Examples.isAdmissiblePrefix_of_check _ (by decide +kernel)

/-- The trace is a repetition of its erasure (spec §11.4, real labels). -/
theorem crashRetryZombie_isRepetition :
    IsRepetition (crashRetryZombieTrace.map Obs.toDex) (eraseRepeats crashRetryZombieTrace) := by
  have passes := isRepetitionAfter_sound
    (fun event : Dex.Obs σ => match event with | .fx _ _ _ => true | _ => false) []
    (crashRetryZombieTrace.map Obs.toDex) (eraseRepeats crashRetryZombieTrace) (by decide +kernel)
  unfold IsRepetition
  refine IsRepetitionAfter.mono ?_ passes
  intro event isRepeatable
  cases event <;> simp_all [Dex.Obs.IsFx]

/-- The end-to-end instance: the L projection is an L trace of the program
with inserted `out(1)` events, each equal to an earlier `out(1)`. -/
theorem crashRetryZombie_endToEnd :
    IsLFiniteTrace emitProgram [.begin (.int 7), .out (.int 1), .exit (.int 0)] ∧
      IsOutRepetition (crashRetryZombieTrace.filterMap (fun event => project event.toDex))
        [.begin (.int 7), .out (.int 1), .exit (.int 0)] := by
  constructor
  · exact DexExpressiveness.Examples.isLFiniteTrace_of_traceOf emitProgram
      (DexExpressiveness.Examples.lExtend emitProgram [] 40 [.request (.start (.int 7))]) _
      (by decide +kernel)
  · rw [crashRetryZombie_projection]
    have passes := isRepetitionAfter_sound
      (fun event : L.Event => match event with | .out _ => true | _ => false) []
      [.begin (.int 7), .out (.int 1), .out (.int 1), .out (.int 1), .exit (.int 0)]
      [.begin (.int 7), .out (.int 1), .exit (.int 0)] (by decide +kernel)
    unfold IsOutRepetition
    refine IsRepetitionAfter.mono ?_ passes
    intro event isRepeatable
    cases event <;> simp_all [L.Event.IsOut]

/-! ## A zombie's result commits before its interrupt

Spec §11.3, Complete rules, extended: "A zombie can pass the fence only
while its invocation is still running with its task ID: after [CRASH] and
before its [RECOVER-INTERRUPT]. [decision] The model allows this order." The
run below takes it: the `emit` attempt emits, the server crashes and
recovers, the zombie returns and its result commits, and its interrupt is
then ignored. -/

/-- The script of the run. -/
def zombieCommitScript : List Instruction :=
  [.act (.request startRequest),
   .act (.tick 1), .startPending, .workerRun 0, .workerReturn 0,
   .act (.tick 1), .complete 0,
   .act (.tick 1), .startPending, .workerRun 0, .workerEffect 0,
   .act .crash, .act .recover,
   .workerReturn 0,
   .act (.tick 1), .complete 0,
   .act (.tick 1), .interruptFirst 5,
   .act (.tick 1), .startPending, .workerRun 0, .workerReturn 0,
   .act (.tick 1), .complete 0]

/-- Every label is enabled, every step is simulated with the progress rule
of spec §11.5, and the invariants hold after every step. -/
theorem zombieCommit_passes : isScriptPassing emitFailureProgram zombieCommitScript = true := by
  decide +kernel

/-- The run has a Complete rule of a zombie task, and a [RECOVER-INTERRUPT]
that apply ignores: it takes no run `seq`. -/
theorem zombieCommit_hasZombieCommit :
    scriptHasStep emitFailureProgram zombieCommitScript
        (fun scriptStep => match scriptStep.action with
          | .stepComplete _ => scriptStep.isZombieStep | _ => false) = true ∧
      (match runScript emitFailureProgram (Config.initial σ)
          (zombieCommitScript.take 17) with
        | some (_, final) =>
          match step emitFailureProgram final ((zombieCommitScript[17]?).getD (Instruction.act (.tick 0))
              |>.resolve final) with
          | some (events, next) =>
            events.isEmpty && (next.run.map (fun run => run.base.seq)) ==
              (final.run.map (fun run => run.base.seq)) && next.interrupts.isEmpty &&
              !final.interrupts.isEmpty
          | none => false
        | none => false) = true := by
  decide +kernel

/-- The L projection of the run: the effect occurs once. -/
theorem zombieCommit_projection :
    (failureTraceOf emitFailureProgram (scriptLabels emitFailureProgram zombieCommitScript)).filterMap
        (fun event => project event.toDex) =
      [.begin (.int 7), .out (.int 1), .exit (.int 0)] := by
  decide +kernel

end DexExpressiveness.DexF.Examples
