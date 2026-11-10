import DexExpressiveness.Failure.Equality
import DexExpressiveness.Failure.Observation

/-!
# Executable checks of the failure simulation

Test tooling, not part of the theorem. `checkStepSimulated` decides
`IsStepSimulated` for one enabled step. `checkInvariant` is the Boolean
mirror of `Invariant` (`Failure/Relation.lean`), clause by clause; it returns
the names of the clauses that fail. A mirror must accept exactly what its
clause accepts on the checked names: a stricter mirror hides gaps from the
inductiveness fuzz (README, "Inductiveness: gaps found and fixed").
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature} [DecidableEq σ.Val] [DecidableEq σ.Fx] [DecidableEq σ.MapSelection]

/-- The name of a clause when it fails. -/
def failsUnless (name : String) (holds : Bool) : List String :=
  if holds then [] else [name]

/-- Decide `IsStepSimulated` for a step that is enabled; `true` for a step
that is not. -/
def checkStepSimulated (program : Program σ) (config : Config σ) (action : Action σ) : Bool :=
  match step program config action with
  | none => true
  | some (events, next) =>
    decide ((Dex.system program.failureFreeFlow).exec (abstractConfig config)
        (dexActionsOf program config action) =
      some (eraseStepEvents config events, abstractConfig next))

/-- Decide that the progress Boolean of an enabled label is the progress rule
of spec §11.5: its image under the transition map has a Dex progress step
(`isProgress_eq_dexProgress`). `true` for a step that is not enabled. -/
def checkProgressMatches (program : Program σ) (config : Config σ) (action : Action σ) : Bool :=
  match step program config action with
  | none => true
  | some _ =>
    action.isProgress ==
      (dexActionsOf program config action).any (Dex.system program.failureFreeFlow).isProgress

/-! ## The mirror of `Invariant` -/

/-- Mirror of `IsStampAtMost`. -/
def isStampAtMost : Option Nat → Option Nat → Bool
  | none, _ => true
  | some _, none => false
  | some stamp, some last => decide (stamp ≤ last)

/-- Mirror of `HistoryIsConsistent`. -/
def isHistoryConsistent (flow : Dex.FlowDef σ) (key : InvocationKey σ)
    (history : InvocationHistory σ) : Bool :=
  match history.arguments with
  | none =>
    history.progress.isNotRun && decide (history.emittedCount = 0) &&
      !decide (history.outcome = .committed)
  | some arguments =>
    let computed := arguments.compute flow key
    (match history.progress with
     | .notRun => decide (history.emittedCount = 0)
     | .ran effects response =>
       decide (effects = computed.1) && decide (response = computed.2) &&
         decide (history.emittedCount ≤ computed.1.length)
     | .returned response =>
       decide (response = computed.2) && decide (history.emittedCount = computed.1.length)) &&
    (!decide (history.outcome = .committed) || history.progress.isReturned)

/-- Mirror of `TaskFollowsHistory`. -/
def isTaskFollowingHistory (flow : Dex.FlowDef σ) (task : Task σ) (history : InvocationHistory σ) :
    Bool :=
  match history.arguments with
  | none => false
  | some arguments =>
    let computed := arguments.compute flow task.key
    decide (task.base.context = arguments.context) && decide (task.base.input = arguments.input) &&
    decide (task.base.snapshot = arguments.snapshot) &&
    decide (task.base.conditionResults = arguments.conditionResults) &&
    decide (task.emittedCount ≤ history.emittedCount) &&
    match task.base.phase with
    | .started => decide (task.emittedCount = 0)
    | .running remaining response =>
      decide (remaining = computed.1.drop task.emittedCount) && decide (response = computed.2) &&
        decide (task.emittedCount ≤ computed.1.length) && !history.progress.isNotRun
    | .returned response =>
      decide (response = computed.2) && decide (task.emittedCount = computed.1.length) &&
        history.progress.isReturned

/-- Mirror of `AttemptMatchesRecord`. -/
def isAttemptMatchingRecord (run : Run σ) (ghost : Ghost σ) (record : Dex.StepRecord σ) : Bool :=
  match record.invocation with
  | none => (run.findAttempt? record.id).isNone
  | some invocation =>
    match run.findAttempt? record.id, ghost.findInvocation? (record.id, invocation.method) with
    | some attempt, some history =>
      decide (history.outcome = .live) && decide (attempt.method = invocation.method) &&
      decide (1 ≤ attempt.number) &&
      (match invocation.state with
       | .running _ => !attempt.isBackoff && attempt.snapshot.isSome
       | .pending => true) &&
      (match attempt.snapshot, history.arguments with
       | some snapshot, some arguments =>
         decide (snapshot = arguments.snapshot) && decide (arguments.input = record.input) &&
         decide (arguments.conditionResults = record.conditionResults) &&
         decide (arguments.context =
           { stepExecutionId := record.id, fromStepExecutionId := record.origin })
       | none, none => true
       | _, _ => false) &&
      (!attempt.isBackoff ||
        (decide (invocation.state = .pending) &&
          run.retryTimers.contains
            { step := record.id, method := invocation.method, attempt := attempt.number }))
    | _, _ => false

/-- Mirror of `RunInvariant`, with failing clause names. -/
def checkRunInvariant (config : Config σ) (run : Run σ) : List String :=
  let ghost := config.ghost
  let steps := run.base.steps
  let running : List (Dex.StepRecord σ × Dex.Method × Nat) := steps.filterMap (fun record =>
    match record.invocation with
    | some { method := method, state := .running taskId } => some (record, method, taskId)
    | _ => none)
  failsUnless "stepIdsNodup" (steps.map (fun record => record.id)).Nodup ++
  failsUnless "waitTimersNodup" (run.base.timers.map (fun entry => entry.1)).Nodup ++
  failsUnless "retryTimersNodup" (run.retryTimers.map (fun entry => entry.1)).Nodup ++
  failsUnless "attemptKeysNodup" (run.attempts.map (fun entry => entry.1)).Nodup ++
  failsUnless "attemptsHaveInvocations" (run.attempts.all (fun entry =>
    steps.any (fun record => decide (record.id = entry.1) && record.invocation.isSome))) ++
  failsUnless "normalIndexValues"
    (decide (ghost.normalIndex.map (fun entry => entry.2) = List.range' 1 ghost.normalCount)) ++
  failsUnless "normalIndexKeysIncrease"
    (decide ((ghost.normalIndex.map (fun entry => entry.1)).Pairwise (· < ·))) ++
  failsUnless "normalIndexKeysTaken"
    (ghost.normalIndex.all (fun entry => decide (entry.1 ≤ run.base.seq))) ++
  failsUnless "waitSeqsIndexed" (steps.all (fun record =>
    match record.waitState with
    | some waitState => (ghost.normalIndex.get? waitState.registeredSeq).isSome
    | none => true)) ++
  failsUnless "attemptsMatch" (steps.all (isAttemptMatchingRecord run ghost)) ++
  failsUnless "liveIsCurrent" (ghost.invocations.all (fun entry =>
    !decide (entry.2.outcome = .live) ||
      steps.any (fun record => decide (record.id = entry.1.1) &&
        match record.invocation with
        | some invocation => decide (invocation.method = entry.1.2)
        | none => false))) ++
  failsUnless "runningTaskIdsBelow" (running.all (fun entry => decide (entry.2.2 < config.nextTaskId))) ++
  failsUnless "runningTaskMatches" (running.all (fun entry =>
    config.realTasks.all (fun task =>
      !decide (task.base.id = entry.2.2) ||
        (decide (task.key = (entry.1.id, entry.2.1)) &&
          match run.findAttempt? entry.1.id with
          | some attempt => decide (task.attemptContext.attempt = attempt.number)
          | none => false)))) ++
  failsUnless "runningIsTracked" (running.all (fun entry =>
    config.tasks.any (fun task => decide (task.base.id = entry.2.2)) || !config.isUp ||
      match run.findAttempt? entry.1.id with
      | some attempt =>
        config.interrupts.contains
          { step := entry.1.id, method := entry.2.1, attempt := attempt.number, taskId := entry.2.2 }
      | none => false))

/-- Mirror of `Invariant`, with failing clause names. -/
def checkInvariant (program : Program σ) (config : Config σ) : List String :=
  let ghost := config.ghost
  let flow := program.failureFreeFlow
  failsUnless "realTaskIdsNodup" (config.realTasks.map (fun task => task.base.id)).Nodup ++
  failsUnless "realTaskIdsBelow"
    (config.realTasks.all (fun task => decide (task.base.id < config.nextTaskId))) ++
  failsUnless "interruptsNotInTasks" (config.interrupts.all (fun interrupt =>
    config.tasks.all (fun task => !decide (task.base.id = interrupt.taskId)))) ++
  failsUnless "interruptsBelow"
    (config.interrupts.all (fun interrupt => decide (interrupt.taskId < config.nextTaskId))) ++
  failsUnless "downIsEmpty" (config.isUp || (config.tasks.isEmpty && config.interrupts.isEmpty)) ++
  failsUnless "lastNormalCommitAtMost" (isStampAtMost ghost.lastNormalCommitAt config.lastCommitAt) ++
  failsUnless "notStarted" (config.run.isSome ||
    (config.realTasks.isEmpty && config.interrupts.isEmpty && decide (config.nextTaskId = 0) &&
      ghost.invocations.isEmpty && decide (ghost.normalCount = 0) && ghost.normalIndex.isEmpty &&
      ghost.lastNormalCommitAt.isNone && decide (ghost.nextVirtualTaskId = 0))) ++
  failsUnless "ghostKeysNodup" (ghost.invocations.map (fun entry => entry.1)).Nodup ++
  failsUnless "virtualTaskIdsNodup"
    ((ghost.invocations.filterMap (fun entry => entry.2.arguments)).map
      (fun arguments => arguments.virtualTaskId)).Nodup ++
  failsUnless "virtualTaskIdsBelow" (ghost.invocations.all (fun entry =>
    match entry.2.arguments with
    | some arguments => decide (arguments.virtualTaskId < ghost.nextVirtualTaskId)
    | none => true)) ++
  failsUnless "historiesConsistent"
    (ghost.invocations.all (fun entry => isHistoryConsistent flow entry.1 entry.2)) ++
  failsUnless "realTasksFollowHistory" (config.realTasks.all (fun task =>
    match ghost.findInvocation? task.key with
    | some history => isTaskFollowingHistory flow task history
    | none => false)) ++
  failsUnless "liveTasksPassFence" (config.tasks.all (fun task =>
    match ghost.findInvocation? task.key with
    | some history => !decide (history.outcome = .live) || config.isFencePassed task
    | none => true)) ++
  match config.run with
  | none => []
  | some run => checkRunInvariant config run

end DexExpressiveness.DexF
