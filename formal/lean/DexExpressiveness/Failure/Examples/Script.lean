import DexExpressiveness.Failure.Check
import DexExpressiveness.Failure.Observation
import DexExpressiveness.Examples.Schedule

/-!
# Scripts for DexF example runs

Test tooling, not part of the theorem. A script is a list of instructions,
each resolved against the configuration it runs in: "start the pending Step",
"fail the running attempt", "the first task emits". This keeps the example
files free of hand-written Step execution IDs and task IDs. An example
theorem is about the run of the resolved labels, whichever way they were
produced.
-/

namespace DexExpressiveness.DexF.Examples

open DexExpressiveness DexExpressiveness.Translation

/-- The signature of translated programs. -/
abbrev σ := Translation.signature

/-- One instruction of a script. -/
inductive Instruction
  /-- A literal action. -/
  | act (action : Action σ)
  /-- [STEP-START] of the first Step whose invocation is Pending and not in
  Backoff, with the snapshot flag the state gives. -/
  | startPending
  /-- [ATTEMPT-LOST] of the first running invocation, with this backoff. -/
  | failRunning (backoffMicros : Nat)
  /-- [ATTEMPT-RETURNED] of the first running invocation. -/
  | returnRunning
  /-- A Worker step of the real task at this position of `Θ ++ Z`; the flag is
  the one the state gives. -/
  | workerRun (position : Nat)
  /-- As `workerRun`, for an effect. -/
  | workerEffect (position : Nat)
  /-- As `workerRun`, for the return. -/
  | workerReturn (position : Nat)
  /-- The Complete rule of the real task at this position. -/
  | complete (position : Nat)
  /-- [RESULT-STALE] of the real task at this position. -/
  | stale (position : Nat)
  /-- The drop of the real task at this position. -/
  | abandon (position : Nat)
  /-- [RECOVER-INTERRUPT] of the first element of `I`, with this backoff. -/
  | interruptFirst (backoffMicros : Nat)
  /-- [TIMER-FIRE] of the first due timer, with the flag the state gives. -/
  | fireFirst

/-- The first Step whose invocation is in the given state, with its attempt. -/
def firstStep? (config : Config σ) (isWanted : Dex.InvocationState → Attempt σ → Bool) :
    Option (Dex.StepExecId σ × Attempt σ) :=
  match config.run with
  | none => none
  | some run =>
    run.base.steps.findSome? (fun record =>
      match record.invocation, run.findAttempt? record.id with
      | some invocation, some attempt =>
        if isWanted invocation.state attempt then some (record.id, attempt) else none
      | _, _ => none)

/-- The real task at a position of `Θ ++ Z`. -/
def taskAt? (config : Config σ) (position : Nat) : Option (Task σ) :=
  config.realTasks[position]?

/-- Resolve an instruction to an action; `.tick 0`, which is never enabled,
when it names nothing. -/
def Instruction.resolve (config : Config σ) : Instruction → Action σ
  | .act action => action
  | .startPending =>
    match firstStep? config (fun state attempt =>
        decide (state = .pending) && !attempt.isBackoff) with
    | some (stepId, attempt) => .stepStart stepId attempt.snapshot.isSome
    | none => .tick 0
  | .failRunning backoffMicros =>
    match firstStep? config (fun state _ => match state with | .running _ => true | _ => false) with
    | some (stepId, _) => .attemptFailed stepId (.retryAfter backoffMicros)
    | none => .tick 0
  | .returnRunning =>
    match firstStep? config (fun state _ => match state with | .running _ => true | _ => false) with
    | some (stepId, _) => .attemptReturned stepId
    | none => .tick 0
  | .workerRun position =>
    match taskAt? config position with
    | some task =>
      .workerRun task.base.id
        (match config.ghost.findInvocation? task.key with
         | some history => history.progress.isNotRun
         | none => false)
    | none => .tick 0
  | .workerEffect position =>
    match taskAt? config position with
    | some task =>
      .workerEffect task.base.id (decide (config.ghost.emittedCountOf task.key < task.emittedCount + 1))
    | none => .tick 0
  | .workerReturn position =>
    match taskAt? config position with
    | some task =>
      .workerReturn task.base.id
        (match config.ghost.findInvocation? task.key with
         | some history => !history.progress.isReturned
         | none => false)
    | none => .tick 0
  | .complete position =>
    match taskAt? config position with
    | some task => .stepComplete task.base.id
    | none => .tick 0
  | .stale position =>
    match taskAt? config position with
    | some task => .resultStale task.base.id (config.isLastOfCanceled task)
    | none => .tick 0
  | .abandon position =>
    match taskAt? config position with
    | some task => .workerAbandon task.base.id (config.isLastOfCanceled task)
    | none => .tick 0
  | .interruptFirst backoffMicros =>
    match config.interrupts.head? with
    | some interrupt => .recoverInterrupt interrupt (.retryAfter backoffMicros)
    | none => .tick 0
  | .fireFirst =>
    match config.run with
    | some run =>
      .timerFire 1 (((run.dueTimers config.clock).take 1).any DueTimer.isWait)
    | none => .tick 0

/-- The outcome of one step of a script: its label, its events, and the
results of the checks before and after it. -/
structure ScriptStep where
  /-- The resolved label. -/
  action : Action σ
  /-- The events of the step. -/
  events : List (Obs σ)
  /-- Whether `IsStepSimulated` holds for the step, and its progress Boolean
  is the image of the transition map. -/
  isSimulated : Bool
  /-- The clauses of `Invariant` that fail after the step. -/
  failingClauses : List String
  /-- Whether the step is a Worker step of a task in `Z`. -/
  isZombieStep : Bool

/-- Whether an action is a Worker step of a task in `Z`. -/
def isZombieStep (config : Config σ) : Action σ → Bool
  | .workerRun taskId _ | .workerEffect taskId _ | .workerReturn taskId _
  | .workerAbandon taskId _ | .stepComplete taskId | .resultStale taskId _ =>
    (config.findZombie? taskId).isSome
  | _ => false

/-- Run a script from `config`: resolve each instruction, take its step, and
check the simulation and the invariant. `none` when a resolved label is not
enabled. -/
def runScript (program : Program σ) : Config σ → List Instruction →
    Option (List ScriptStep × Config σ)
  | config, [] => some ([], config)
  | config, instruction :: laterInstructions =>
    let action := instruction.resolve config
    match step program config action with
    | none => none
    | some (events, next) =>
      match runScript program next laterInstructions with
      | none => none
      | some (laterSteps, final) =>
        some ({ action := action, events := events
                isSimulated := checkStepSimulated program config action &&
                  checkProgressMatches program config action
                failingClauses := checkInvariant program next
                isZombieStep := isZombieStep config action } :: laterSteps, final)

/-- A short name of an action, for messages. -/
def describeAction : Action σ → String
  | .tick duration => s!"tick {duration}"
  | .request (.startFlow requestId _ _) => s!"startFlow {requestId}"
  | .request (.publish _ _) => "publish"
  | .startRetry requestId _ _ => s!"startRetry {requestId}"
  | .engineReject _ => "engineReject"
  | .stepStart stepId isSnapshotReused => s!"stepStart #{stepId.number} reuses={isSnapshotReused}"
  | .workerRun taskId isFirst => s!"workerRun t{taskId} first={isFirst}"
  | .workerEffect taskId isFirst => s!"workerEffect t{taskId} first={isFirst}"
  | .workerReturn taskId isFirst => s!"workerReturn t{taskId} first={isFirst}"
  | .workerAbandon taskId isLast => s!"workerAbandon t{taskId} last={isLast}"
  | .stepComplete taskId => s!"stepComplete t{taskId}"
  | .resultStale taskId isLast => s!"resultStale t{taskId} last={isLast}"
  | .timerFire count isWaitFired => s!"timerFire {count} wait={isWaitFired}"
  | .attemptFailed stepId decision =>
    s!"attemptFailed #{stepId.number} exhausted={decision.isExhausted}"
  | .attemptReturned stepId => s!"attemptReturned #{stepId.number}"
  | .attemptHeartbeat stepId _ => s!"attemptHeartbeat #{stepId.number}"
  | .crash => "crash"
  | .recover => "recover"
  | .recoverInterrupt interrupt decision =>
    s!"recoverInterrupt t{interrupt.taskId} exhausted={decision.isExhausted}"
  | .internalCommit => "internalCommit"

/-- Run a script as far as it goes, for messages: the description, the
simulation check, and the failing clauses of every step taken, then the
description of the first label that is not enabled, if any. -/
def traceScript (program : Program σ) : Config σ → List Instruction →
    List (String × Bool × List String) × Option String
  | _, [] => ([], none)
  | config, instruction :: laterInstructions =>
    let action := instruction.resolve config
    match step program config action with
    | none => ([], some (describeAction action))
    | some (_, next) =>
      let later := traceScript program next laterInstructions
      ((describeAction action, checkStepSimulated program config action,
          checkInvariant program next) :: later.1, later.2)

/-- The labels of a script run. -/
def scriptLabels (program : Program σ) (instructions : List Instruction) : List (Action σ) :=
  match runScript program (Config.initial σ) instructions with
  | some (steps, _) => steps.map (fun scriptStep => scriptStep.action)
  | none => []

/-- Whether every instruction of a script resolves to an enabled label, every
step is simulated, and the invariant holds after every step. -/
def isScriptPassing (program : Program σ) (instructions : List Instruction) : Bool :=
  match runScript program (Config.initial σ) instructions with
  | some (steps, _) =>
    steps.all (fun scriptStep => scriptStep.isSimulated && scriptStep.failingClauses.isEmpty)
  | none => false

/-- Whether some step of a script run satisfies `isWanted`. -/
def scriptHasStep (program : Program σ) (instructions : List Instruction)
    (isWanted : ScriptStep → Bool) : Bool :=
  match runScript program (Config.initial σ) instructions with
  | some (steps, _) => steps.any isWanted
  | none => false

/-- The events of a finite run of DexF, or `[]` when a label is not enabled. -/
def failureTraceOf (program : Program σ) (labels : List (Action σ)) : List (Obs σ) :=
  match (system program).exec (system program).init labels with
  | some (events, _) => events
  | none => []

/-- A Boolean check that a request is a publication of an encoded literal to
an external Channel. -/
def isEncodedOutcomePublication : Request σ → Bool
  | .publish (Chan.external _) (DexVal.value value) => (L.Literal.ofVal? value).isSome
  | _ => false

/-- A Boolean check that a request list is an admissible outcome-word prefix
of a translated program: empty, or the start of the entry Step with an
encoded literal followed by its retries and by publications of encoded
literals to external Channels (spec §11.3). -/
def isEncodedOutcomePrefix : List (Request σ) → Bool
  | [] => true
  | .startFlow requestId StepType.start (DexVal.value input) :: laterRequests =>
    (L.Literal.ofVal? input).isSome && laterRequests.all (fun request =>
      (Request.startFlow requestId StepType.start (DexVal.value input)).isRetryOf request ||
        isEncodedOutcomePublication request)
  | _ => false

/-- The Boolean check implies admissibility. -/
theorem isAdmissibleOutcomePrefix_of_check (requests : List (Request σ))
    (passes : isEncodedOutcomePrefix requests = true) :
    IsAdmissibleOutcomePrefix environment requests := by
  unfold isEncodedOutcomePrefix at passes
  split at passes
  · exact Or.inl rfl
  · rename_i requestId input laterRequests
    simp only [Bool.and_eq_true] at passes
    obtain ⟨inputIsLiteral, laterPass⟩ := passes
    obtain ⟨literal, isLiteral⟩ := Option.isSome_iff_exists.mp inputIsLiteral
    refine Or.inr ⟨requestId, DexVal.value input, laterRequests, rfl,
      ⟨literal, by rw [L.Literal.toVal_of_ofVal? input literal isLiteral]⟩, ?_⟩
    intro request isMember
    have requestPasses := List.all_eq_true.mp laterPass request isMember
    simp only [Bool.or_eq_true] at requestPasses
    cases requestPasses with
    | inl isRetry => exact Or.inl isRetry
    | inr isPublication =>
      unfold isEncodedOutcomePublication at isPublication
      split at isPublication
      · rename_i chan value
        obtain ⟨messageLiteral, isMessageLiteral⟩ := Option.isSome_iff_exists.mp isPublication
        exact Or.inr ⟨_, _, rfl, chan, messageLiteral, rfl,
          by rw [L.Literal.toVal_of_ofVal? value messageLiteral isMessageLiteral]⟩
      · cases isPublication
  · cases passes

/-- The Dex actions that the transition map gives a script run, in order. -/
def dexScheduleOf (program : Program σ) (labels : List (Action σ)) : List (Dex.Action σ) :=
  (labels.foldl (fun (state : Config σ × List (Dex.Action σ)) label =>
    match step program state.1 label with
    | some (_, next) => (next, state.2 ++ dexActionsOf program state.1 label)
    | none => state) (Config.initial σ, [])).2

end DexExpressiveness.DexF.Examples
