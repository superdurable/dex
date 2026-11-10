import DexExpressiveness.Failure.Abstraction

/-!
# The simulation of DexF by Dex: the transition map and the invariants (spec §11.6)

The simulation relation is functional: a DexF configuration `config` is
related to the Dex configuration `abstractConfig config` when `config`
satisfies `Invariant program config`. Each DexF step maps to the list of Dex
actions of `dexActionsOf` (spec §11.6, "Transition map"); the empty list is a
stutter. `IsStepSimulated` says that this list runs from the abstraction of
the configuration before the step to the abstraction of the configuration
after it, with the step's events less the repeated `fx` events.

The definitions of this file are fixed for Stage 2. The step lemmas of
`Failure/Statements.lean` are stated with them; the Boolean mirrors of
`Failure/Check.lean` test them.
-/

namespace DexExpressiveness.DexF

variable {σ : Dex.Signature}

/-! ## The transition map -/

/-- The virtual task ID of the invocation that a real task runs. -/
def Config.virtualTaskIdOf (config : Config σ) (taskId : Nat) : Option Nat :=
  (config.findRealTask? taskId).bind (fun task =>
    (config.ghost.argumentsOf task.key).map (fun arguments => arguments.virtualTaskId))

/-- One Dex action on the virtual task of a real task's invocation. -/
def Config.onVirtualTask (config : Config σ) (taskId : Nat) (action : Nat → Dex.Action σ) :
    List (Dex.Action σ) :=
  match config.virtualTaskIdOf taskId with
  | some virtualTaskId => [action virtualTaskId]
  | none => []

/-- The number of wait timers among the first `count` due timers: the length
of the wait-timer sub-list of `D` (spec §11.3, "Mapping": "Section 11.6 maps
the command to [TIMER-FIRE] of that sub-list"). -/
def Config.firedWaitTimerCount (config : Config σ) (count : Nat) : Nat :=
  match config.run with
  | none => 0
  | some run => (((run.dueTimers config.clock).take count).filter DueTimer.isWait).length

/-- The [WORKER-ABANDON] steps of the virtual tasks that a commit leaves
without a real task (spec §11.6: "Cancel or close of a Step whose attempts
are all gone: the same commit, then [WORKER-ABANDON] of the virtual task"),
in virtual task ID order. -/
def abandonedVirtualTasks (before after : Config σ) : List (Dex.Action σ) :=
  let abandoned := after.ghost.invocations.filterMap (fun entry =>
    match entry.2.arguments, before.ghost.findInvocation? entry.1 with
    | some arguments, some previous =>
      if decide (previous.outcome = .live) && decide (entry.2.outcome = .canceled) &&
          !after.hasVirtualTask entry.1 entry.2 then
        some arguments.virtualTaskId
      else none
    | _, _ => none)
  (sortStable (fun first second => decide (first ≤ second)) abandoned).map
    Dex.Action.workerAbandon

/-- The transition map of spec §11.6: the Dex actions a DexF step maps to.
The empty list is a stutter.

| DexF step | Dex actions |
| --- | --- |
| [TICK], a request, [ENGINE-REJECT] | the same step |
| [STEP-START] that computes a snapshot | [STEP-START] |
| a Worker step with the first-occurrence Boolean set | that step of the virtual task |
| the drop or stale result of the last real task of a canceled invocation | [WORKER-ABANDON] or [STEP-COMPLETE-STALE] of the virtual task |
| a Complete rule | the Complete rule of the virtual task, then [WORKER-ABANDON] of each virtual task left without a real task |
| [TIMER-FIRE] that fires a wait timer | [TIMER-FIRE] of the wait-timer sub-list |
| every other step | stutter | -/
def dexActionsOf (program : Program σ) (config : Config σ) : Action σ → List (Dex.Action σ)
  | .tick duration => [.tick duration]
  | .request request => [.request request.toDex]
  | .startRetry _ _ _ => []
  | .engineReject request => [.request request.toDex]
  | .stepStart stepId isSnapshotReused => if isSnapshotReused then [] else [.stepStart stepId]
  | .workerRun taskId isFirstRun =>
    if isFirstRun then config.onVirtualTask taskId Dex.Action.workerRun else []
  | .workerEffect taskId isFirstOccurrence =>
    if isFirstOccurrence then config.onVirtualTask taskId Dex.Action.workerEffect else []
  | .workerReturn taskId isFirstReturn =>
    if isFirstReturn then config.onVirtualTask taskId Dex.Action.workerReturn else []
  | .workerAbandon taskId isLastOfCanceled =>
    if isLastOfCanceled then config.onVirtualTask taskId Dex.Action.workerAbandon else []
  | .resultStale taskId isLastOfCanceled =>
    if isLastOfCanceled then config.onVirtualTask taskId Dex.Action.stepCompleteStale else []
  | .stepComplete taskId =>
    config.onVirtualTask taskId Dex.Action.stepComplete ++
      match step program config (.stepComplete taskId) with
      | some (_, next) => abandonedVirtualTasks config next
      | none => []
  | .timerFire count isWaitTimerFired =>
    if isWaitTimerFired then [.timerFire (config.firedWaitTimerCount count)] else []
  | .attemptFailed _ _ => []
  | .attemptReturned _ => []
  | .attemptHeartbeat _ _ => []
  | .crash => []
  | .recover => []
  | .recoverInterrupt _ _ => []
  | .internalCommit => []

/-- The events of one DexF step with the repeated `fx` removed and the ghost
index erased. An `fx(e, m, x)ʲ` is repeated when `j ≤ h(e, m)` before the
step (spec §11.6: "The same with `j ≤ h(e, m)`: stutter; the event is
repeated"). `effectHistory_of_exec` (`Failure/Statements.lean`) ties this to
the trace form of spec §11.4. -/
def eraseStepEvents (config : Config σ) (events : List (Obs σ)) : List (Dex.Obs σ) :=
  events.filterMap (fun event =>
    match event with
    | .fx step method effect index =>
      if index ≤ config.ghost.emittedCountOf (step, method) then none
      else some (.fx step method effect)
    | other => some other.toDex)

/-- The history of the `fx` events of a finite run that ends in `config`
(spec §11.4, "First occurrences come in order 1, 2, 3, …"): every `fx(e, m,
x)ʲ` of `events` has `1 ≤ j ≤ h(e, m)` and the label `fx⃗(e, m)[j]`, and
every index from 1 to `h(e, m)` occurs. So an `fx` of a later step is
repeated in the trace form of spec §11.4 exactly when `eraseStepEvents`
drops it, and it then equals, in source and label, the first occurrence of
its index. -/
def HasEffectHistory (program : Program σ) (config : Config σ) (events : List (Obs σ)) : Prop :=
  (∀ step method effect index, Obs.fx step method effect index ∈ events →
    1 ≤ index ∧ index ≤ config.ghost.emittedCountOf (step, method) ∧
      ∃ arguments, config.ghost.argumentsOf (step, method) = some arguments ∧
        (arguments.compute program.failureFreeFlow (step, method)).1[index - 1]? = some effect) ∧
  (∀ step method index, 1 ≤ index → index ≤ config.ghost.emittedCountOf (step, method) →
    ∃ effect, Obs.fx step method effect index ∈ events)

/-- The DexF step `config --action--> next` with events `events` is simulated
by Dex: the Dex actions of the transition map run from `α(config)` to
`α(next)`, and their events are the step's events less the repeated ones. -/
def IsStepSimulated (program : Program σ) (config : Config σ) (action : Action σ)
    (events : List (Obs σ)) (next : Config σ) : Prop :=
  (Dex.system program.failureFreeFlow).exec (abstractConfig config)
      (dexActionsOf program config action) =
    some (eraseStepEvents config events, abstractConfig next)

/-- How a DexF step maps under α: to a Dex commit or tick (`normal`), to a
stutter (`failure`), or to a Worker step of a virtual task or a stutter
(`worker`). The work packages of `Failure/Statements.lean` follow this
split. -/
inductive SimulationKind
  /-- A normal command, [TICK], a request, or [ENGINE-REJECT]. -/
  | normal
  /-- A failure rule, [START-RETRY], a reused snapshot, a retry-only
  TimersFired, or an internal commit. -/
  | failure
  /-- A Worker step, a drop, or a stale result. -/
  | worker
  deriving DecidableEq, Repr

/-- The simulation kind of a DexF action. -/
def Action.simulationKind : Action σ → SimulationKind
  | .tick _ => .normal
  | .request _ => .normal
  | .engineReject _ => .normal
  | .stepComplete _ => .normal
  | .stepStart _ isSnapshotReused => if isSnapshotReused then .failure else .normal
  | .timerFire _ isWaitTimerFired => if isWaitTimerFired then .normal else .failure
  | .workerRun _ _ => .worker
  | .workerEffect _ _ => .worker
  | .workerReturn _ _ => .worker
  | .workerAbandon _ _ => .worker
  | .resultStale _ _ => .worker
  | .startRetry _ _ _ => .failure
  | .attemptFailed _ _ => .failure
  | .attemptReturned _ => .failure
  | .attemptHeartbeat _ _ => .failure
  | .crash => .failure
  | .recover => .failure
  | .recoverInterrupt _ _ => .failure
  | .internalCommit => .failure

/-- Whether an action carries the retry decision Exhausted: an
[ATTEMPT-EXHAUSTED] transition, or an ignored interrupt that carries it (the
same transition as with RetryAt). H1 excludes these labels. -/
def Action.isExhausting : Action σ → Bool
  | .attemptFailed _ decision => decision.isExhausted
  | .recoverInterrupt _ decision => decision.isExhausted
  | _ => false

/-! ## The invariants (spec §11.6, "Invariants that `Related` needs") -/

/-- `stamp` is at most `last`, for optional stamps. -/
def IsStampAtMost : Option Nat → Option Nat → Prop
  | none, _ => True
  | some _, none => False
  | some stamp, some last => stamp ≤ last

/-- The ghost part of I3 for one invocation: `v` and `h` agree with
`fx⃗(e, m)`, and an invocation without `par` never started. "Every task's
phase is at most `v(e, m)`. No live task needs to witness `h` or `v`." A
committed invocation has returned (I1). -/
def HistoryIsConsistent (flow : Dex.FlowDef σ) (key : InvocationKey σ)
    (history : InvocationHistory σ) : Prop :=
  match history.arguments with
  | none =>
    history.progress.isNotRun = true ∧ history.emittedCount = 0 ∧
      history.outcome ≠ .committed
  | some arguments =>
    (match history.progress with
     | .notRun => history.emittedCount = 0
     | .ran effects response =>
       effects = (arguments.compute flow key).1 ∧ response = (arguments.compute flow key).2 ∧
         history.emittedCount ≤ (arguments.compute flow key).1.length
     | .returned response =>
       response = (arguments.compute flow key).2 ∧
         history.emittedCount = (arguments.compute flow key).1.length) ∧
    (history.outcome = .committed → history.progress.isReturned = true)

/-- I2 and I3 for one real task. I2: "Every attempt of `(e, m)`, in `Θ` or
`Z`, computes `U` on the same arguments up to `ctxA`." I3: "Every task of
`(e, m)` in `Θ` or `Z` has emitted a prefix of `fx⃗(e, m)` of length at most
`h(e, m)`. A task that returns has emitted the whole list. Every task's phase
is at most `v(e, m)`." -/
def TaskFollowsHistory (flow : Dex.FlowDef σ) (task : Task σ) (history : InvocationHistory σ) :
    Prop :=
  ∃ arguments, history.arguments = some arguments ∧
    task.base.context = arguments.context ∧ task.base.input = arguments.input ∧
    task.base.snapshot = arguments.snapshot ∧
    task.base.conditionResults = arguments.conditionResults ∧
    task.emittedCount ≤ history.emittedCount ∧
    match task.base.phase with
    | .started => task.emittedCount = 0
    | .running remaining response =>
      remaining = (arguments.compute flow task.key).1.drop task.emittedCount ∧
        response = (arguments.compute flow task.key).2 ∧
        task.emittedCount ≤ (arguments.compute flow task.key).1.length ∧
        history.progress.isNotRun = false
    | .returned response =>
      response = (arguments.compute flow task.key).2 ∧
        task.emittedCount = (arguments.compute flow task.key).1.length ∧
        history.progress.isReturned = true

/-- I4 and I6, and the attempt bookkeeping, for one Step record. A Step
without an invocation has no attempt state. A Step with an invocation has a
live history and an attempt state of the same method. A running invocation
holds a snapshot and is not in Backoff. I4: "An invocation that holds a
snapshot ... its snapshot is the snapshot of its first start. An invocation
without a snapshot has never started" (and its history says so). I6, in the
form liveness needs: a Backoff invocation is pending and its retry timer is
armed. -/
def AttemptMatchesRecord (run : Run σ) (ghost : Ghost σ) (record : Dex.StepRecord σ) : Prop :=
  match record.invocation with
  | none => run.findAttempt? record.id = none
  | some invocation =>
    ∃ attempt history,
      run.findAttempt? record.id = some attempt ∧
      ghost.findInvocation? (record.id, invocation.method) = some history ∧
      history.outcome = .live ∧
      attempt.method = invocation.method ∧ 1 ≤ attempt.number ∧
      (∀ taskId, invocation.state = .running taskId →
        attempt.isBackoff = false ∧ attempt.snapshot.isSome = true) ∧
      (match attempt.snapshot, history.arguments with
       | some snapshot, some arguments =>
         snapshot = arguments.snapshot ∧ arguments.input = record.input ∧
           arguments.conditionResults = record.conditionResults ∧
           arguments.context = { stepExecutionId := record.id, fromStepExecutionId := record.origin }
       | none, none => True
       | _, _ => False) ∧
      (attempt.isBackoff = true →
        invocation.state = .pending ∧
          run.retryTimers.contains
            { step := record.id, method := invocation.method, attempt := attempt.number } = true)

/-- The invariant of a started run. -/
structure RunInvariant (config : Config σ) (run : Run σ) : Prop where
  /-- Step execution IDs are unique (`findStep?` reads the first match,
  `updateStep` changes every match). -/
  stepIdsNodup : (run.base.steps.map (fun record => record.id)).Nodup
  /-- Armed wait timers have distinct IDs. -/
  waitTimersNodup : (run.base.timers.map (fun entry => entry.1)).Nodup
  /-- Armed retry timers have distinct IDs. -/
  retryTimersNodup : (run.retryTimers.map (fun entry => entry.1)).Nodup
  /-- Attempt states have distinct keys. -/
  attemptKeysNodup : (run.attempts.map (fun entry => entry.1)).Nodup
  /-- Every attempt state belongs to a Step with an invocation. -/
  attemptsHaveInvocations : ∀ entry, entry ∈ run.attempts →
    ∃ record, record ∈ run.base.steps ∧ record.id = entry.1 ∧ record.invocation.isSome = true
  /-- I5, `ζ`: "`ζ` maps the run `seq` of the `k`-th applied normal command to
  `k`, and it is strictly order-preserving." Its values are `1, …, n`. -/
  normalIndexValues :
    config.ghost.normalIndex.map (fun entry => entry.2) = List.range' 1 config.ghost.normalCount
  /-- The domain of `ζ` is strictly increasing in list order. -/
  normalIndexKeysIncrease : (config.ghost.normalIndex.map (fun entry => entry.1)).Pairwise (· < ·)
  /-- The domain of `ζ` holds `seq` values already taken. -/
  normalIndexKeysTaken : ∀ entry, entry ∈ config.ghost.normalIndex → entry.1 ≤ run.base.seq
  /-- I5, `sws`: every stored wait, also of a finished Step, was registered by
  a normal command, so `ζ` relabels it. -/
  waitSeqsIndexed : ∀ record waitState, record ∈ run.base.steps →
    record.waitState = some waitState →
    (config.ghost.normalIndex.get? waitState.registeredSeq).isSome = true
  /-- I1, I4, I6 per Step record. -/
  attemptsMatch : ∀ record, record ∈ run.base.steps → AttemptMatchesRecord run config.ghost record
  /-- I1: a live history is the current invocation of its Step. -/
  liveIsCurrent : ∀ key history, config.ghost.findInvocation? key = some history →
    history.outcome = .live →
    ∃ record state, record ∈ run.base.steps ∧ record.id = key.1 ∧
      record.invocation = some { method := key.2, state := state }
  /-- A running invocation names a fresh-enough task ID. -/
  runningTaskIdsBelow : ∀ record method taskId, record ∈ run.base.steps →
    record.invocation = some { method := method, state := .running taskId } →
    taskId < config.nextTaskId
  /-- A real task with the task ID of a running invocation is its attempt. -/
  runningTaskMatches : ∀ record method taskId task, record ∈ run.base.steps →
    record.invocation = some { method := method, state := .running taskId } →
    task ∈ config.realTasks → task.base.id = taskId →
    task.key = (record.id, method) ∧
      ∃ attempt, run.findAttempt? record.id = some attempt ∧
        task.attemptContext.attempt = attempt.number
  /-- I7, the form liveness needs: the attempt of a running invocation is in
  `Θ`, or the server is down, or the attempt is in `I`. -/
  runningIsTracked : ∀ record method taskId, record ∈ run.base.steps →
    record.invocation = some { method := method, state := .running taskId } →
    (∃ task, task ∈ config.tasks ∧ task.base.id = taskId) ∨ config.isUp = false ∨
      ∃ attempt, run.findAttempt? record.id = some attempt ∧
        ({ step := record.id, method := method, attempt := attempt.number, taskId := taskId } :
          Interrupt σ) ∈ config.interrupts

/-- The invariants I1 to I7 of spec §11.6, with the structural facts that the
exact commutation with α needs. Every reachable configuration satisfies it
(work package F4, `invariant_initial` and `invariant_step`), for programs
that satisfy H3. -/
structure Invariant (program : Program σ) (config : Config σ) : Prop where
  /-- Real task IDs are unique across `Θ` and `Z`. -/
  realTaskIdsNodup : (config.realTasks.map (fun task => task.base.id)).Nodup
  /-- Real task IDs are below the next fresh ID. -/
  realTaskIdsBelow : ∀ task, task ∈ config.realTasks → task.base.id < config.nextTaskId
  /-- I7: "Each element `(e, m, a, t)` of `I` ... `t ∉ dom(Θ)`." -/
  interruptsNotInTasks : ∀ interrupt task, interrupt ∈ config.interrupts → task ∈ config.tasks →
    task.base.id ≠ interrupt.taskId
  /-- I7: the task ID of an interrupt was minted, so no later [STEP-START]
  reuses it. -/
  interruptsBelow : ∀ interrupt, interrupt ∈ config.interrupts →
    interrupt.taskId < config.nextTaskId
  /-- A down server has no `Θ` and no `I`: [CRASH] moved `Θ` to `Z` and lost `I`. -/
  downIsEmpty : config.isUp = false → config.tasks = [] ∧ config.interrupts = []
  /-- The last normal stamp is at most the last log stamp. -/
  lastNormalCommitAtMost : IsStampAtMost config.ghost.lastNormalCommitAt config.lastCommitAt
  /-- Before the start nothing is in flight and the ghosts are initial. -/
  notStarted : config.run = none →
    config.realTasks = [] ∧ config.interrupts = [] ∧ config.nextTaskId = 0 ∧
      config.ghost.invocations = [] ∧ config.ghost.normalCount = 0 ∧
      config.ghost.normalIndex = [] ∧ config.ghost.lastNormalCommitAt = none ∧
      config.ghost.nextVirtualTaskId = 0
  /-- Ghost invocations have distinct keys. -/
  ghostKeysNodup : (config.ghost.invocations.map (fun entry => entry.1)).Nodup
  /-- Virtual task IDs are distinct, also of committed and canceled invocations. -/
  virtualTaskIdsNodup :
    ((config.ghost.invocations.filterMap (fun entry => entry.2.arguments)).map
      (fun arguments => arguments.virtualTaskId)).Nodup
  /-- Virtual task IDs are below the next one. -/
  virtualTaskIdsBelow : ∀ entry arguments, entry ∈ config.ghost.invocations →
    entry.2.arguments = some arguments → arguments.virtualTaskId < config.ghost.nextVirtualTaskId
  /-- I3, the ghost part. -/
  historiesConsistent : ∀ entry, entry ∈ config.ghost.invocations →
    HistoryIsConsistent program.failureFreeFlow entry.1 entry.2
  /-- I2 and I3 for every real task. -/
  realTasksFollowHistory : ∀ task, task ∈ config.realTasks →
    ∃ history, config.ghost.findInvocation? task.key = some history ∧
      TaskFollowsHistory program.failureFreeFlow task history
  /-- A task of `Θ` whose invocation is live passes the fence. -/
  liveTasksPassFence : ∀ task history, task ∈ config.tasks →
    config.ghost.findInvocation? task.key = some history → history.outcome = .live →
    config.isFencePassed task = true
  /-- The invariant of the started run. -/
  runInvariant : ∀ run, config.run = some run → RunInvariant config run

/-- The simulation relation: `dexConfig` is the abstraction of `config`, and
`config` satisfies the invariants. -/
def Related (program : Program σ) (config : Config σ) (dexConfig : Dex.Config σ) : Prop :=
  Invariant program config ∧ dexConfig = abstractConfig config

end DexExpressiveness.DexF
