import DexExpressiveness.Failure.Relation
import DexExpressiveness.Failure.Repeats

/-!
# DexF: the system, outcome words, fairness, and the hypotheses (spec §11.1, §11.3, §11.5)

- `system`: DexF as a `TransitionSystem`, with the progress rule of spec
  §11.5.
- Outcome words and their admissibility, "`E_adm` with retries" (spec
  §11.3), and `w↓`, the word with its retries deleted (spec §11.5).
- Fairness for DexF: F0 to F6 restated as in spec §11.5, and FF, FR, FI.
- The hypotheses H1 to H4, SYNC (D2), the clock assumption D6, and HP, each
  a named predicate on runs or programs.
- The trace sets `Tr_fail` and `Tr_free` that the theorems compare.

The definitions of this file are fixed for Stage 2.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## The system -/

/-- The client request a label gives its final outcome (spec §11.1 D7: "an
accepted commit, a rejection by apply, or a rejection by the engine before
any proposal"). A [START-RETRY] gives a StartFlow request its outcome. -/
def Action.requestOf : Action σ → Option (Request σ)
  | .request clientRequest => some clientRequest
  | .startRetry requestId stepType input => some (.startFlow requestId stepType input)
  | .engineReject clientRequest => some clientRequest
  | _ => none

/-- The progress rule of spec §11.5: "a DexF step is a **progress step** if
and only if its image under the transition map of Section 11.6 is a Dex
progress step, that is, a non-empty Dex step other than [TICK]." The labels
carry the classification that the state decides (`Failure/Rules.lean`), so
this reads the label alone. [ATTEMPT-EXHAUSTED] has no image; it emits
`closed(...)` and counts as progress, and H1 excludes it. -/
def Action.isProgress : Action σ → Bool
  | .tick _ => false
  | .request _ => true
  | .startRetry _ _ _ => false
  | .engineReject _ => true
  | .stepStart _ isSnapshotReused => !isSnapshotReused
  | .workerRun _ isFirstRun => isFirstRun
  | .workerEffect _ isFirstOccurrence => isFirstOccurrence
  | .workerReturn _ isFirstReturn => isFirstReturn
  | .workerAbandon _ isLastOfCanceled => isLastOfCanceled
  | .stepComplete _ => true
  | .resultStale _ isLastOfCanceled => isLastOfCanceled
  | .timerFire _ isWaitTimerFired => isWaitTimerFired
  | .attemptFailed _ decision => decision.isExhausted
  | .attemptReturned _ => false
  | .attemptHeartbeat _ _ => false
  | .crash => false
  | .recover => false
  | .recoverInterrupt _ decision => decision.isExhausted
  | .internalCommit => false

/-- DexF of one program as a transition system (spec §11). The program is
closed when the root Flow's run is closed. -/
def system (program : Program σ) : TransitionSystem (Config σ) (Action σ) (Obs σ) (Request σ) :=
  { init := Config.initial σ
    step := step program
    requestOf := Action.requestOf
    isProgress := Action.isProgress
    isClosed := fun config =>
      match config.run with
      | none => false
      | some run =>
        match run.base.status with
        | .running => false
        | .closed _ => true }

/-! ## Outcome words (spec §11.1 D7, §11.3, §11.5) -/

/-- Whether `later` is a retry of the StartFlow request `first` (spec §11.3):
"a StartFlow request ... whose request ID equals the first one's, with
`ignore_already_started_error` set" (always set in the model). -/
def Request.isRetryOf : Request σ → Request σ → Bool
  | .startFlow firstRequestId _ _, .startFlow laterRequestId _ _ =>
    decide (firstRequestId = laterRequestId)
  | _, _ => false

/-- An admissible outcome word (spec §11.3, "`E_adm` with retries"): "Its
first StartFlow is the start of Section 5.3, and it is the first request of
the word. Any number of retries may follow anywhere in the word. Every other
request is a PublishToChannel as in Section 5.3." The word is the outcome
word of D7: requests in the order of their final outcomes. -/
def IsAdmissibleOutcomeWord (environment : Dex.Environment σ) (word : Sequence (Request σ)) :
    Prop :=
  (∃ requestId input, environment.IsProgramInput input ∧
    word 0 = some (.startFlow requestId environment.entryStep input)) ∧
  (∀ position request, word (position + 1) = some request →
    (∃ first, word 0 = some first ∧ first.isRetryOf request = true) ∨
    ∃ chan value, request = .publish chan value ∧ environment.IsExternalMessage chan value) ∧
  (∀ position, word position = none → word (position + 1) = none)

/-- A finite prefix of an admissible outcome word: the requests a finite
DexF run gave outcomes to. -/
def IsAdmissibleOutcomePrefix (environment : Dex.Environment σ) (requests : List (Request σ)) :
    Prop :=
  requests = [] ∨
  ∃ requestId input laterRequests,
    requests = .startFlow requestId environment.entryStep input :: laterRequests ∧
    environment.IsProgramInput input ∧
    ∀ request, request ∈ laterRequests →
      (Request.startFlow requestId environment.entryStep input).isRetryOf request = true ∨
      ∃ chan value, request = .publish chan value ∧ environment.IsExternalMessage chan value

/-- `w↓` of a finite outcome word (spec §11.5): "the Dex word obtained by
deleting its retries. The first StartFlow is not a retry, so `w↓` keeps
it." -/
def eraseRetries : List (Request σ) → List (Dex.Request σ)
  | [] => []
  | first :: laterRequests =>
    first.toDex :: (laterRequests.filter (fun request => !first.isRetryOf request)).map
      Request.toDex

/-- The requests that `w↓` keeps at a position of the outcome word `word`. -/
def retryErasureChunk (word : Sequence (Request σ)) (position : Nat) : List (Dex.Request σ) :=
  match word position with
  | none => []
  | some request =>
    match position, word 0 with
    | _ + 1, some first => if first.isRetryOf request then [] else [request.toDex]
    | _, _ => [request.toDex]

/-- `erased` is `w↓` of the finite or infinite outcome word `word` (spec
§11.5). -/
def IsRetryErasure (word : Sequence (Request σ)) (erased : Sequence (Dex.Request σ)) : Prop :=
  IsConcatenation (retryErasureChunk word) erased

/-! ## Fairness for DexF (spec §11.5)

"Fairness for DexF replaces F2 to F5 of Section 5.5 and adds FF, FR, and
FI." F0 is `TransitionSystem.RequestsArrive`, and a DexF run for `w` "gives
every request of `w`, retries included, its final outcome in the order of
`w`" (`TransitionSystem.IsRunFor` with `Action.requestOf`). -/

/-- The fire time of an armed timer, wait or retry. -/
def Run.fireTimeOf? (run : Run σ) : DueTimer σ → Option Nat
  | .wait timer => run.base.timers.get? timer
  | .retry timer => run.retryTimers.get? timer

namespace Fairness

variable (run : InfiniteRun (Config σ) (Action σ))

/-- F1, time diverges: "every partition clock exceeds every bound". -/
def TimeDiverges : Prop :=
  ∀ bound, ∃ index, bound < (run.states index).clock

/-- The timer, wait or retry, is armed and due before transition `index`. -/
def IsTimerArmedAndDue (timer : DueTimer σ) (index : Nat) : Prop :=
  ∃ flowRun fireTime,
    (run.states index).run = some flowRun ∧
    flowRun.fireTimeOf? timer = some fireTime ∧
    fireTime ≤ (run.states index).clock

/-- F2, timers fire: "A wait timer or retry timer that stays armed and due is
eventually fired." -/
def TimersFire : Prop :=
  ∀ timer : DueTimer σ, EventuallyFails (IsTimerArmedAndDue run timer)

/-- The invocation of method `method` of the Step is startable before
transition `index`: the server is up, and it is Pending, not in Backoff. -/
def IsInvocationStartable (stepId : Dex.StepExecId σ) (method : Dex.Method) (index : Nat) :
    Prop :=
  (run.states index).isUp = true ∧
  ∃ flowRun record attempt,
    (run.states index).run = some flowRun ∧
    flowRun.base.findStep? stepId = some record ∧
    record.invocation = some { method := method, state := .pending } ∧
    flowRun.findAttempt? stepId = some attempt ∧
    attempt.isBackoff = false

/-- F3, Steps start: "a pending invocation that stays startable while `up`
eventually starts". "F3, F5, and FI constrain the run only from there [the
point after which `up` holds forever]; a down period only delays them." -/
def StepsStart : Prop :=
  ∀ (stepId : Dex.StepExecId σ) (method : Dex.Method),
    EventuallyFails (IsInvocationStartable run stepId method)

/-- The real task, in `Θ` or `Z`, has not returned before transition `index`. -/
def IsTaskUnsettled (taskId : Nat) (index : Nat) : Prop :=
  ∃ task,
    (run.states index).findRealTask? taskId = some task ∧
    ∀ response, task.base.phase ≠ .returned response

/-- F4, Workers settle: "Every task of `Θ` eventually becomes `Returned`,
leaves `Θ` by a failure rule, or is dropped by [WORKER-ABANDON]. Every zombie
task eventually emits all its effects and returns, or is dropped after some
prefix of them." A task that leaves `Θ` by a failure rule is a zombie, so
both parts say that a real task does not stay unreturned forever. -/
def WorkersSettle : Prop :=
  ∀ taskId, EventuallyFails (IsTaskUnsettled run taskId)

/-- The real task is `Returned` before transition `index`. -/
def IsTaskReturned (taskId : Nat) (index : Nat) : Prop :=
  ∃ task response,
    (run.states index).findRealTask? taskId = some task ∧ task.base.phase = .returned response

/-- F5, results commit: "Every `Returned` task of `Θ` or `Z` eventually
leaves by a Complete rule or by [RESULT-STALE]. The commit needs `up`; a
zombie whose result can no longer reach any server is dropped instead
(F4)." -/
def ResultsCommit : Prop :=
  ∀ taskId, EventuallyFails (IsTaskReturned run taskId)

/-- F6, messages move. The fragment has no outbox, pending close, or RPC, so
F6 holds trivially, as in the failure-free model. -/
def MessagesMove : Prop := True

/-- Whether a label is a failure that FF counts: [CRASH], [ATTEMPT-LOST] (or
[ATTEMPT-EXHAUSTED]), [ATTEMPT-RETURNED]. -/
def Action.isCountedFailure : Action σ → Bool
  | .crash => true
  | .attemptFailed _ _ => true
  | .attemptReturned _ => true
  | _ => false

/-- FF, finitely many failures: "A run has finitely many [CRASH],
[ATTEMPT-LOST], and [ATTEMPT-RETURNED] transitions." -/
def FailuresAreFinite : Prop :=
  ∃ bound, ∀ index, bound ≤ index → Action.isCountedFailure (run.labels index) = false

/-- FR, the server comes back: "Every [CRASH] is eventually followed by
[RECOVER]." -/
def ServerRecovers : Prop :=
  ∀ index, run.labels index = .crash → ∃ later, index < later ∧ run.labels later = .recover

/-- FI, interrupts commit: "Every element of `I` eventually takes
[RECOVER-INTERRUPT]." An element leaves `I` only by its interrupt or by
[CRASH]; by FF and FR the last [RECOVER] is followed by interrupts only. -/
def InterruptsCommit : Prop :=
  ∀ interrupt : Interrupt σ, EventuallyFails (fun index => interrupt ∈ (run.states index).interrupts)

end Fairness

/-- A fair DexF run: F1 to F6 as restated in spec §11.5, and FF, FR, FI. F0
is part of `TransitionSystem.IsFairMaximalTrace`. -/
def IsFair (run : InfiniteRun (Config σ) (Action σ)) : Prop :=
  Fairness.TimeDiverges run ∧ Fairness.TimersFire run ∧ Fairness.StepsStart run ∧
  Fairness.WorkersSettle run ∧ Fairness.ResultsCommit run ∧ Fairness.MessagesMove ∧
  Fairness.FailuresAreFinite run ∧ Fairness.ServerRecovers run ∧ Fairness.InterruptsCommit run

/-! ## The hypotheses (spec §11.1, §11.3, §11.5) -/

/-- H1, no exhaustion, for a finite run: "A run contains no
[ATTEMPT-EXHAUSTED] transition." -/
def HasNoExhaustion (labels : List (Action σ)) : Prop :=
  ∀ label, label ∈ labels → label.isExhausting = false

/-- H1 for an infinite run. -/
def RunHasNoExhaustion (run : InfiniteRun (Config σ) (Action σ)) : Prop :=
  ∀ index, (run.labels index).isExhausting = false

/-- H2, no failure policies: "No Step option of the program sets the WaitFor
policy ProceedOnFailure or the Execute policy ProceedToConfiguredStep."
Holds by construction: `Dex.StepOptions` has no failure policy, and
[ATTEMPT-EXHAUSTED] implements the branch without one. Under H1 a policy is
never consulted, because policies act only on the retry decision Exhausted
(`step_results.rs:377-456`). Lean decision: stated as a named predicate that
is always true, so that the theorems list it. -/
def HasNoFailurePolicy (_program : Program σ) : Prop := True

/-- H3, user code is insensitive to attempts: "`U`'s effects and result do
not depend on `ctxA`." -/
def IgnoresAttemptContext (program : Program σ) : Prop :=
  ∀ first second : AttemptContext σ, program first = program second

/-- Whether a label is a non-retryable failure. -/
def Action.isNonRetryableFailure : Action σ → Bool
  | .attemptFailed _ (.exhausted .nonRetryable) => true
  | .recoverInterrupt _ (.exhausted .nonRetryable) => true
  | _ => false

/-- H4, no non-retryable failures, for a finite run: "No attempt failure is
non-retryable. A non-retryable failure exhausts at once." In the model it is
a case of H1. -/
def HasNoNonRetryableFailure (labels : List (Action σ)) : Prop :=
  ∀ label, label ∈ labels → label.isNonRetryableFailure = false

/-- H4 for an infinite run. -/
def RunHasNoNonRetryableFailure (run : InfiniteRun (Config σ) (Action σ)) : Prop :=
  ∀ index, (run.labels index).isNonRetryableFailure = false

/-- D2, SYNC only: "Every invocation's pinned durability is SYNC." Holds by
construction: DexF has only the SYNC start, where "a SYNC attempt reaches its
Worker only after its start commits"; [STEP-START] creates the task in the
same transition as its commit, and [CRASH] cannot undo a commit. Lean
decision: a named predicate that is always true. -/
def IsSyncOnly (_program : Program σ) : Prop := True

/-- D6, the wall clock (spec §11.1), in the model: every timer that a
TimersFired command fires is due at the command's stamp ("the premise
`Tm(i) ≤ t_{p(f)}` of [TIMER-FIRE] holds"). The premise of `timerFire`
enforces it; the predicate names the assumption that justifies that
premise. -/
def FiredTimersAreDue (run : InfiniteRun (Config σ) (Action σ)) : Prop :=
  ∀ index count isWaitTimerFired flowRun timer fireTime,
    run.labels index = .timerFire count isWaitTimerFired →
    (run.states index).run = some flowRun →
    timer ∈ (flowRun.dueTimers (run.states index).clock).take count →
    flowRun.fireTimeOf? timer = some fireTime →
    fireTime ≤ (run.states index).clock

/-- HP, retries come before the purge (spec §11.3): "the command of every
retry ... is ordered before the RunPurge of the original run." The model has
no RunPurge, so HP holds by construction; this predicate states its
consequence: every StartFlow request that reaches a run takes
[START-RETRY]. -/
def RetriesPrecedePurge (run : InfiniteRun (Config σ) (Action σ)) : Prop :=
  ∀ index requestId stepType input,
    (run.labels index).requestOf = some (.startFlow requestId stepType input) →
    (run.states index).run.isSome = true →
    run.labels index = .startRetry requestId stepType input

/-- The hypotheses on an infinite run: H1, H4, D6, and HP. -/
def IsHypothesisRun (run : InfiniteRun (Config σ) (Action σ)) : Prop :=
  RunHasNoExhaustion run ∧ RunHasNoNonRetryableFailure run ∧ FiredTimersAreDue run ∧
  RetriesPrecedePurge run

/-! ## The trace sets (spec §11.5)

"`Tr_fail(P)` is the same for DexF, over runs that satisfy H1 to H4, D2, D6,
and the non-empty task ID assumption, with inputs from `E_adm` extended by
[START-RETRY] ... and runs that satisfy HP." Task IDs are minted natural
numbers, so the non-empty task ID assumption holds by construction. H2, H3,
and D2 are hypotheses of the theorems on the program. -/

/-- `Tr_fail`, finite traces: a finite DexF trace, with ghost indexes, of a
run that satisfies H1 and H4, for an admissible outcome-word prefix
`requests`. -/
def IsFailureFiniteTrace (program : Program σ) (environment : Dex.Environment σ)
    (requests : List (Request σ)) (trace : List (Obs σ)) : Prop :=
  ∃ labels final,
    (system program).exec (system program).init labels = some (trace, final) ∧
    HasNoExhaustion labels ∧ HasNoNonRetryableFailure labels ∧
    requests = labels.filterMap Action.requestOf ∧
    IsAdmissibleOutcomePrefix environment requests

/-- `Tr_free`, finite traces: a finite Dex trace for the admissible prefix
`requests` (spec §5.4, before projection). -/
def IsFreeFiniteTrace (flow : Dex.FlowDef σ) (environment : Dex.Environment σ)
    (requests : List (Dex.Request σ)) (trace : List (Dex.Obs σ)) : Prop :=
  ∃ labels final,
    (Dex.system flow).exec (Dex.system flow).init labels = some (trace, final) ∧
    requests = labels.filterMap (Dex.system flow).requestOf ∧
    environment.IsAdmissiblePrefix requests

/-- `Tr_fail`, fair maximal traces with markers: the whole DexF trace, with
ghost indexes, of a fair run for the outcome word `word` that satisfies H1,
H4, D6, and HP. -/
def IsFailureFairMaximalTrace (program : Program σ) (word : Sequence (Request σ))
    (trace : Sequence (Obs σ)) (marker : Marker) : Prop :=
  (system program).IsFairMaximalTrace (fun run => IsFair run ∧ IsHypothesisRun run) some word
    trace marker

end DexExpressiveness.DexF
