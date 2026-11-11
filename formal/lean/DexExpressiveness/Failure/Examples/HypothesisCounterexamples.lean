import DexExpressiveness.Failure.Check
import DexExpressiveness.Failure.Examples.FreeRunFacts
import DexExpressiveness.Failure.Examples.FairPrefixRuns

/-!
# Failure transparency, Stage 3: each hypothesis is needed

Each theorem below drops one hypothesis of the safety statements of
`Failure/Statements/Main.lean`, keeps every other one, and shows a concrete
DexF trace for which the conclusion fails. The failure-free side is bounded
for **every** failure-free run by `Failure/Examples/FreeRunFacts.lean`. All
are checked in the kernel.

| Hypothesis dropped | Theorem | Run | What fails |
| --- | --- | --- | --- |
| H3, `IgnoresAttemptContext` | `attemptSensitive_isNotRepetition` | attempt `a` emits `a` copies of `a`; attempt 1 emits `1` and is lost, attempt 2 emits `2, 2` | `erase` keeps a `fx(2)`, and the trace is a repetition of no failure-free trace: no failure-free run emits `2`; both safety conclusions fail |
| H1, `HasNoExhaustion` | `exhaustion_isNotFailureFree` | the only attempt is lost with the decision Exhausted | `closed(failed "WORKER_API_FAIL")`, which no failure-free run emits; both safety conclusions fail |
| H4, `HasNoNonRetryableFailure` | `nonRetryableFailure_isNotFailureFree` | the same, with a non-retryable failure | as for H1: in the model H4 is a case of H1 (`isNonRetryableFailure_isExhausting`) |
| D7, PublishToChannel re-sent | `resentPublication_isTwoRequests` | the client sends one message twice | two `pub` events, two requests of `w↓`; no failure-free trace of the one-publication word is equal to it or has it as a repetition |
-/

namespace DexExpressiveness.DexF.Examples.Counterexamples

open DexExpressiveness DexExpressiveness.DexF DexExpressiveness.DexF.Examples

/-- A small signature with natural numbers for every name. -/
abbrev counterSignature : Dex.Signature :=
  { StepType := Nat, Chan := Nat, Key := Nat, Val := Nat, Fx := Nat, MapSelection := Unit
    isMapInstanceKey := fun _ => false, isSelectedBy := fun _ _ => false
    isTimerIdAtMost := fun firstType _ _ secondType _ _ => decide (firstType ≤ secondType) }

/-- The environment: entry Step `0`; every input and message is admissible. -/
def counterEnvironment : Dex.Environment counterSignature :=
  { entryStep := 0, IsProgramInput := fun _ => True, IsExternalMessage := fun _ _ => True }

/-- The Step `0-1`. -/
def firstStep : Dex.StepExecId counterSignature := { stepType := 0, number := 1 }

/-- A Flow whose only Step emits `effect` and completes the Flow. -/
def emitThenCompleteFlow (effect : Nat) : Dex.FlowDef counterSignature :=
  { stepDef := fun _ =>
      { waitFor := none
        execute := fun _ _ _ _ =>
          ([effect], { effects := {}, decision := { moves := [], close := some (.forceComplete none) } }) } }

/-- The Boolean form of `IsFailureFiniteTrace` premises that are label checks. -/
theorem hasNoExhaustion_of_check (labels : List (Action counterSignature))
    (passes : labels.all (fun label => !label.isExhausting) = true) : HasNoExhaustion labels := by
  intro label isMember
  simpa using List.all_eq_true.mp passes label isMember

/-- The Boolean form of H4 on labels. -/
theorem hasNoNonRetryableFailure_of_check (labels : List (Action counterSignature))
    (passes : labels.all (fun label => !label.isNonRetryableFailure) = true) :
    HasNoNonRetryableFailure labels := by
  intro label isMember
  simpa using List.all_eq_true.mp passes label isMember

/-- The start alone is an admissible outcome-word prefix. -/
theorem start_isAdmissibleOutcomePrefix (laterRequests : List (Request counterSignature))
    (arePublications : ∀ request, request ∈ laterRequests → ∃ chan value, request = .publish chan value) :
    IsAdmissibleOutcomePrefix counterEnvironment (.startFlow 0 0 0 :: laterRequests) := by
  refine Or.inr ⟨0, 0, laterRequests, rfl, trivial, fun request isMember => Or.inr ?_⟩
  obtain ⟨chan, value, isPublication⟩ := arePublications request isMember
  exact ⟨chan, value, isPublication, trivial⟩

/-! ## H3: a handler that reads its attempt number -/

/-- A Flow whose only Step emits `effects` and completes the Flow. -/
def emitAllThenCompleteFlow (effects : List Nat) : Dex.FlowDef counterSignature :=
  { stepDef := fun _ =>
      { waitFor := none
        execute := fun _ _ _ _ =>
          (effects, { effects := {}, decision := { moves := [], close := some (.forceComplete none) } }) } }

/-- Attempt `a` emits `a` copies of `a`: the program reads `ctxA` and breaks
H3. Its handler is an attempt-fenced effect, which spec §11.5 places
outside H3 (Review log, F37). -/
def attemptEchoProgram : Program counterSignature :=
  fun attemptContext =>
    emitAllThenCompleteFlow (List.replicate attemptContext.attempt attemptContext.attempt)

/-- Start; attempt 1 emits `1` and is lost ([ATTEMPT-LOST]), its task a
zombie; the retry timer fires ([RETRY-FIRE]); attempt 2 starts on the
retained snapshot and emits `2` twice: with ghost index 1, a repeat of
index 1 with a new label, and with ghost index 2, a first occurrence; then
it completes. -/
def attemptEchoLabels : List (Action counterSignature) :=
  [.request (.startFlow 0 0 0), .tick 1, .stepStart firstStep false, .workerRun 0 true,
   .workerEffect 0 true, .tick 1, .attemptFailed firstStep (.retryAfter 5), .tick 10,
   .timerFire 1 false, .tick 1, .stepStart firstStep true, .workerRun 1 false,
   .workerEffect 1 false, .workerEffect 1 true, .workerReturn 1 true, .tick 1, .stepComplete 1]

/-- The requests: the start. -/
def attemptEchoRequests : List (Request counterSignature) :=
  attemptEchoLabels.filterMap Action.requestOf

/-- The trace of the run. -/
def attemptEchoTrace : List (Obs counterSignature) := scheduleEvents attemptEchoProgram attemptEchoLabels

/-- The run is executable; its trace has `fx(1)` with index 1, then `fx(2)`
with indexes 1 and 2; `erase` keeps `fx(2)` with index 2. -/
theorem attemptEcho_trace :
    ((system attemptEchoProgram).exec (system attemptEchoProgram).init attemptEchoLabels).isSome =
        true ∧
      attemptEchoTrace =
        [.start 0 0, .fx firstStep .execute 1 1, .fx firstStep .execute 2 1,
          .fx firstStep .execute 2 2, .closed .completed []] ∧
      eraseRepeats attemptEchoTrace =
        [.start 0 0, .fx firstStep .execute 1, .fx firstStep .execute 2, .closed .completed []] ∧
      attemptEchoRequests = [.startFlow 0 0 0] := by
  decide +kernel

/-- No failure-free run of the program's Flow emits `fx(2)`: every effect it
computes is `1`. -/
theorem attemptEcho_freeTrace_hasNoTwo (requests : List (Dex.Request counterSignature))
    (freeTrace : List (Dex.Obs counterSignature))
    (isFree : IsFreeFiniteTrace attemptEchoProgram.failureFreeFlow counterEnvironment requests
      freeTrace) :
    Dex.Obs.fx firstStep .execute 2 ∉ freeTrace := by
  intro isMember
  obtain ⟨arguments, index, isComputed⟩ := freeTrace_fx_isComputed _ _ _ _ isFree _ _ _ isMember
  simp only [Arguments.compute, Dex.Task.compute, Arguments.toTask, Program.failureFreeFlow,
    attemptEchoProgram, emitAllThenCompleteFlow, AttemptContext.first] at isComputed
  cases index with
  | zero => simp at isComputed
  | succ index => simp at isComputed

/-- **H3 is needed.** The program reads `ctxA`, so H3 fails; H2, SYNC, H1, H4,
and the admissible prefix hold, and the trace is in `Tr_fail` except for H3.
Both safety conclusions fail. `erase` of the trace keeps `fx(2)` with index
2, a first occurrence, so it is not a failure-free trace
(`safety_failure_forward`). The trace is a repetition of no failure-free
trace (`safety_failure_repetition`). No failure-free run emits `fx(2)`. -/
theorem attemptSensitive_isNotRepetition :
    ¬ IgnoresAttemptContext attemptEchoProgram ∧
    HasNoFailurePolicy attemptEchoProgram ∧ IsSyncOnly attemptEchoProgram ∧
    IsFailureFiniteTrace attemptEchoProgram counterEnvironment attemptEchoRequests attemptEchoTrace ∧
    attemptEchoTrace =
      [.start 0 0, .fx firstStep .execute 1 1, .fx firstStep .execute 2 1,
        .fx firstStep .execute 2 2, .closed .completed []] ∧
    ¬ IsFreeFiniteTrace attemptEchoProgram.failureFreeFlow counterEnvironment
        (eraseRetries attemptEchoRequests) (eraseRepeats attemptEchoTrace) ∧
    ¬ ∃ freeTrace,
      IsFreeFiniteTrace attemptEchoProgram.failureFreeFlow counterEnvironment
        (eraseRetries attemptEchoRequests) freeTrace ∧
      IsRepetition (attemptEchoTrace.map Obs.toDex) freeTrace := by
  obtain ⟨isSome, isTrace, isErased, isRequests⟩ := attemptEcho_trace
  refine ⟨?_, trivial, trivial, ?_, isTrace, ?_, ?_⟩
  · intro ignoresAttemptContext
    have isSame := congrArg
      (fun flow : Dex.FlowDef counterSignature =>
        ((flow.stepDef 0).execute { stepExecutionId := firstStep, fromStepExecutionId := .flowStart }
          none { attributes := [] } none).1)
      (ignoresAttemptContext (AttemptContext.first counterSignature)
        { (AttemptContext.first counterSignature) with attempt := 2 })
    simp [attemptEchoProgram, emitAllThenCompleteFlow, AttemptContext.first] at isSame
  · refine ⟨attemptEchoLabels, scheduleFinal attemptEchoProgram attemptEchoLabels,
      exec_of_isSome _ _ isSome, hasNoExhaustion_of_check _ (by decide +kernel),
      hasNoNonRetryableFailure_of_check _ (by decide +kernel), rfl, ?_⟩
    rw [isRequests]
    exact start_isAdmissibleOutcomePrefix [] (fun _ isMember => by cases isMember)
  · intro isFree
    refine attemptEcho_freeTrace_hasNoTwo _ _ isFree ?_
    rw [isErased]
    simp
  · rintro ⟨freeTrace, isFree, isRepetition⟩
    refine attemptEcho_freeTrace_hasNoTwo _ freeTrace isFree (isRepetition_mem isRepetition _ ?_)
    rw [isTrace]
    simp [Obs.toDex]

/-! ## H1 and H4: exhaustion -/

/-- A failure-free program: the only Step emits `7` and completes the Flow. -/
def completingProgram : Program counterSignature := Program.ofFlow (emitThenCompleteFlow 7)

/-- The Flow never decides ForceFail. -/
theorem completingFlow_hasNoForceFail : FlowHasNoForceFail completingProgram.failureFreeFlow := by
  intro _ _ _ _ _ message isFail
  simp [completingProgram, Program.ofFlow, Program.failureFreeFlow, emitThenCompleteFlow] at isFail

/-- Start; the only attempt emits `7`, then fails with the decision
`decision`. -/
def exhaustionLabels (decision : RetryDecision) : List (Action counterSignature) :=
  [.request (.startFlow 0 0 0), .tick 1, .stepStart firstStep false, .workerRun 0 true,
   .workerEffect 0 true, .tick 1, .attemptFailed firstStep decision]

/-- The requests: the start. -/
def exhaustionRequests (decision : RetryDecision) : List (Request counterSignature) :=
  (exhaustionLabels decision).filterMap Action.requestOf

/-- The trace of the run. -/
def exhaustionTrace (decision : RetryDecision) : List (Obs counterSignature) :=
  scheduleEvents completingProgram (exhaustionLabels decision)

/-- The trace of an exhausted run, for both reasons H1 and H4 exclude. -/
theorem exhaustion_trace (reason : ExhaustionReason)
    (isReason : reason = .maximumAttempts ∨ reason = .nonRetryable) :
    ((system completingProgram).exec (system completingProgram).init
        (exhaustionLabels (.exhausted reason))).isSome = true ∧
      exhaustionTrace (.exhausted reason) =
        [.start 0 0, .fx firstStep .execute 7 1, .closed (.failed exhaustionMessage) []] ∧
      exhaustionRequests (.exhausted reason) = [.startFlow 0 0 0] := by
  rcases isReason with rfl | rfl <;> decide +kernel

/-- The safety conclusions fail for a trace with a Failed close of a program
whose Flow never decides ForceFail. -/
theorem failedClose_isNotFailureFree (trace : List (Obs counterSignature))
    (requests : List (Request counterSignature))
    (isMember : Obs.closed (.failed exhaustionMessage) [] ∈ trace) :
    ¬ IsFreeFiniteTrace completingProgram.failureFreeFlow counterEnvironment (eraseRetries requests)
        (eraseRepeats trace) ∧
    ¬ ∃ freeTrace,
      IsFreeFiniteTrace completingProgram.failureFreeFlow counterEnvironment
        (eraseRetries requests) freeTrace ∧
      IsRepetition (trace.map Obs.toDex) freeTrace := by
  have isMapped : Dex.Obs.closed (.failed exhaustionMessage) [] ∈ trace.map Obs.toDex :=
    List.mem_map.mpr ⟨_, isMember, rfl⟩
  refine ⟨fun isFree => ?_, fun ⟨freeTrace, isFree, isRepetition⟩ => ?_⟩
  · refine freeTrace_hasNoFailedClose _ completingFlow_hasNoForceFail _ _ _ isFree
      exhaustionMessage [] ?_
    have keepsClose : ∀ events : List (Obs counterSignature),
        Obs.closed (.failed exhaustionMessage) [] ∈ events → ∀ earlier,
          Dex.Obs.closed (.failed exhaustionMessage) [] ∈ eraseRepeatsAfter earlier events := by
      intro events
      induction events with
      | nil => intro isEmptyMember; cases isEmptyMember
      | cons event laterEvents inductionHypothesis =>
        intro isEventsMember earlier
        unfold eraseRepeatsAfter
        rcases List.mem_cons.mp isEventsMember with isEvent | isLater
        · subst isEvent
          simp [Obs.isRepeatAfter, Obs.toDex]
        · exact List.mem_append_right _ (inductionHypothesis isLater _)
    exact keepsClose trace isMember []
  · exact freeTrace_hasNoFailedClose _ completingFlow_hasNoForceFail _ _ _ isFree
      exhaustionMessage [] (isRepetition_mem isRepetition _ isMapped)

/-- **H1 is needed.** The run satisfies H2, SYNC, H3, H4, and the admissible
prefix, and violates only H1: [ATTEMPT-EXHAUSTED] closes the Flow with
`closed(failed "WORKER_API_FAIL")`. No failure-free run of the program emits
a Failed close, so `erase` of the trace is not a failure-free trace
(`safety_failure_forward` fails) and the trace is a repetition of none
(`safety_failure_repetition` fails). -/
theorem exhaustion_isNotFailureFree :
    IgnoresAttemptContext completingProgram ∧ HasNoFailurePolicy completingProgram ∧
    IsSyncOnly completingProgram ∧
    (system completingProgram).exec (system completingProgram).init
        (exhaustionLabels (.exhausted .maximumAttempts)) =
      some (exhaustionTrace (.exhausted .maximumAttempts),
        scheduleFinal completingProgram (exhaustionLabels (.exhausted .maximumAttempts))) ∧
    ¬ HasNoExhaustion (exhaustionLabels (.exhausted .maximumAttempts)) ∧
    HasNoNonRetryableFailure (exhaustionLabels (.exhausted .maximumAttempts)) ∧
    IsAdmissibleOutcomePrefix counterEnvironment (exhaustionRequests (.exhausted .maximumAttempts)) ∧
    exhaustionTrace (.exhausted .maximumAttempts) =
      [.start 0 0, .fx firstStep .execute 7 1, .closed (.failed exhaustionMessage) []] ∧
    ¬ IsFreeFiniteTrace completingProgram.failureFreeFlow counterEnvironment
        (eraseRetries (exhaustionRequests (.exhausted .maximumAttempts)))
        (eraseRepeats (exhaustionTrace (.exhausted .maximumAttempts))) ∧
    ¬ ∃ freeTrace,
      IsFreeFiniteTrace completingProgram.failureFreeFlow counterEnvironment
        (eraseRetries (exhaustionRequests (.exhausted .maximumAttempts))) freeTrace ∧
      IsRepetition ((exhaustionTrace (.exhausted .maximumAttempts)).map Obs.toDex) freeTrace := by
  obtain ⟨isSome, isTrace, isRequests⟩ := exhaustion_trace .maximumAttempts (Or.inl rfl)
  have notFree := failedClose_isNotFailureFree (exhaustionTrace (.exhausted .maximumAttempts))
    (exhaustionRequests (.exhausted .maximumAttempts)) (by rw [isTrace]; simp)
  refine ⟨fun _ _ => rfl, trivial, trivial, exec_of_isSome _ _ isSome, ?_,
    hasNoNonRetryableFailure_of_check _ (by decide +kernel), ?_, isTrace, notFree.1, notFree.2⟩
  · intro hasNoExhaustion
    have isNotExhausting := hasNoExhaustion (.attemptFailed firstStep (.exhausted .maximumAttempts))
      (by simp [exhaustionLabels])
    simp [Action.isExhausting, RetryDecision.isExhausted] at isNotExhausting
  · rw [isRequests]
    exact start_isAdmissibleOutcomePrefix [] (fun _ isMember => by cases isMember)

/-- In the model a non-retryable failure is an exhaustion: H4 is a case of
H1, so no run violates H4 without violating H1. -/
theorem isNonRetryableFailure_isExhausting (label : Action counterSignature)
    (isNonRetryable : label.isNonRetryableFailure = true) : label.isExhausting = true := by
  unfold Action.isNonRetryableFailure at isNonRetryable
  split at isNonRetryable <;> simp_all [Action.isExhausting, RetryDecision.isExhausted]

/-- **H4, the non-retryable form.** The same run with a non-retryable
failure violates H4, and with it H1; the safety conclusions fail as for
H1. -/
theorem nonRetryableFailure_isNotFailureFree :
    ¬ HasNoNonRetryableFailure (exhaustionLabels (.exhausted .nonRetryable)) ∧
    ¬ HasNoExhaustion (exhaustionLabels (.exhausted .nonRetryable)) ∧
    exhaustionTrace (.exhausted .nonRetryable) =
      [.start 0 0, .fx firstStep .execute 7 1, .closed (.failed exhaustionMessage) []] ∧
    ¬ ∃ freeTrace,
      IsFreeFiniteTrace completingProgram.failureFreeFlow counterEnvironment
        (eraseRetries (exhaustionRequests (.exhausted .nonRetryable))) freeTrace ∧
      IsRepetition ((exhaustionTrace (.exhausted .nonRetryable)).map Obs.toDex) freeTrace := by
  obtain ⟨_, isTrace, _⟩ := exhaustion_trace .nonRetryable (Or.inr rfl)
  have notFree := failedClose_isNotFailureFree (exhaustionTrace (.exhausted .nonRetryable))
    (exhaustionRequests (.exhausted .nonRetryable)) (by rw [isTrace]; simp)
  have isMember : Action.attemptFailed firstStep (.exhausted .nonRetryable) ∈
      exhaustionLabels (.exhausted .nonRetryable) := by
    simp [exhaustionLabels]
  refine ⟨fun hasNone => ?_, fun hasNone => ?_, isTrace, notFree.2⟩
  · have isNot := hasNone _ isMember
    simp [Action.isNonRetryableFailure] at isNot
  · have isNot := hasNone _ isMember
    simp [Action.isExhausting, RetryDecision.isExhausted] at isNot

/-! ## D7: a publication re-sent after a lost response -/

/-- Start, then the client sends the message `9` to Channel `5` twice: the
first response was lost, and PublishToChannel has no request ID. -/
def resentPublicationLabels : List (Action counterSignature) :=
  [.request (.startFlow 0 0 0), .tick 1, .request (.publish 5 9), .tick 1,
   .request (.publish 5 9)]

/-- The requests: the start and two publications. -/
def resentPublicationRequests : List (Request counterSignature) :=
  resentPublicationLabels.filterMap Action.requestOf

/-- The DexF trace of the run. -/
def resentPublicationTrace : List (Obs counterSignature) :=
  scheduleEvents completingProgram resentPublicationLabels

/-- The same schedule in the failure-free model. -/
def resentPublicationDexLabels : List (Dex.Action counterSignature) :=
  [.request (.startFlow 0 0), .tick 1, .request (.publish 5 9), .tick 1, .request (.publish 5 9)]

/-- The schedule with one publication, as the client meant it. -/
def singlePublicationDexLabels : List (Dex.Action counterSignature) :=
  [.request (.startFlow 0 0), .tick 1, .request (.publish 5 9)]

/-- The traces of both schedules, by evaluation. -/
theorem resentPublication_traces :
    ((system completingProgram).exec (system completingProgram).init
        resentPublicationLabels).isSome = true ∧
      resentPublicationTrace = [.start 0 0, .pub 5 9, .pub 5 9] ∧
      resentPublicationRequests = [.startFlow 0 0 0, .publish 5 9, .publish 5 9] ∧
      ((Dex.system completingProgram.failureFreeFlow).exec
          (Dex.system completingProgram.failureFreeFlow).init resentPublicationDexLabels).map
          Prod.fst = some [.start 0 0, .pub 5 9, .pub 5 9] ∧
      ((Dex.system completingProgram.failureFreeFlow).exec
          (Dex.system completingProgram.failureFreeFlow).init singlePublicationDexLabels).map
          Prod.fst = some [.start 0 0, .pub 5 9] := by
  decide +kernel

/-- **D7: a re-sent publication is a second request.** In the failure-free
model the two publications give two `pub` events, a trace for the word with
both. In DexF the run is in `Tr_fail` for an outcome word with both
publications, and `w↓` keeps both. No failure-free trace of the
one-publication word is that trace or has it as a repetition, because a
repetition inserts only `fx` events and such a trace has at most one `pub`.
So the theorem says nothing about the client's intent to publish once. -/
theorem resentPublication_isTwoRequests :
    IsFreeFiniteTrace completingProgram.failureFreeFlow counterEnvironment
      [.startFlow 0 0, .publish 5 9, .publish 5 9] [.start 0 0, .pub 5 9, .pub 5 9] ∧
    IsFailureFiniteTrace completingProgram counterEnvironment resentPublicationRequests
      resentPublicationTrace ∧
    eraseRetries resentPublicationRequests = [.startFlow 0 0, .publish 5 9, .publish 5 9] ∧
    resentPublicationTrace.map Obs.toDex = [.start 0 0, .pub 5 9, .pub 5 9] ∧
    ¬ ∃ freeTrace,
      IsFreeFiniteTrace completingProgram.failureFreeFlow counterEnvironment
        [.startFlow 0 0, .publish 5 9] freeTrace ∧
      (freeTrace = resentPublicationTrace.map Obs.toDex ∨
        IsRepetition (resentPublicationTrace.map Obs.toDex) freeTrace) := by
  obtain ⟨isSome, isTrace, isRequests, isDexTrace, _⟩ := resentPublication_traces
  have isTwoPubs : (resentPublicationTrace.map Obs.toDex).countP Dex.Obs.isPubEvent = 2 := by
    rw [isTrace]; rfl
  refine ⟨?_, ?_, by rw [isRequests]; rfl, by rw [isTrace]; rfl, ?_⟩
  · cases isExec : (Dex.system completingProgram.failureFreeFlow).exec
        (Dex.system completingProgram.failureFreeFlow).init resentPublicationDexLabels with
    | none => rw [isExec] at isDexTrace; cases isDexTrace
    | some result =>
      rw [isExec] at isDexTrace
      simp only [Option.map_some, Option.some.injEq] at isDexTrace
      refine ⟨resentPublicationDexLabels, result.2, ?_, rfl, ?_⟩
      · rw [isExec, ← isDexTrace]
      · refine Or.inr ⟨0, [.publish 5 9, .publish 5 9], rfl, trivial, ?_⟩
        intro request isMember
        simp at isMember
        exact ⟨5, 9, isMember, trivial⟩
  · refine ⟨resentPublicationLabels, scheduleFinal completingProgram resentPublicationLabels,
      exec_of_isSome _ _ isSome, hasNoExhaustion_of_check _ (by decide +kernel),
      hasNoNonRetryableFailure_of_check _ (by decide +kernel), rfl, ?_⟩
    rw [isRequests]
    exact start_isAdmissibleOutcomePrefix _ (fun request isMember => by
      simp at isMember
      exact ⟨5, 9, isMember⟩)
  · rintro ⟨freeTrace, isFree, isSameOrRepetition⟩
    have atMostOne := freeTrace_pubCount_le _ _ _ _ isFree
    have hasTwo : freeTrace.countP Dex.Obs.isPubEvent = 2 := by
      rcases isSameOrRepetition with isSame | isRepetition
      · rw [isSame, isTwoPubs]
      · have sameNonFx := isRepetition_filter_not_fx isRepetition
        have keepsPubs : ∀ trace : List (Dex.Obs counterSignature),
            trace.countP Dex.Obs.isPubEvent =
              (trace.filter (fun event => !Dex.Obs.isFxEvent event)).countP Dex.Obs.isPubEvent := by
          intro trace
          induction trace with
          | nil => rfl
          | cons event laterEvents inductionHypothesis =>
            cases event <;> simp [List.countP_cons, Dex.Obs.isFxEvent,
              Dex.Obs.isPubEvent, inductionHypothesis]
        rw [keepsPubs freeTrace, ← sameNonFx, ← keepsPubs, isTwoPubs]
    rw [hasTwo] at atMostOne
    simp [Dex.Request.isPublication] at atMostOne

end DexExpressiveness.DexF.Examples.Counterexamples
