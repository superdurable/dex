import DexExpressiveness.Invariants
import DexExpressiveness.L.Invariant
import DexExpressiveness.TraceSets

/-!
# The simulation relation between an L program and its translated Flow

`Related program lConfig dexConfig` links an L configuration to a Dex
configuration of `⟦program⟧`. It holds only the clauses that mention both
sides. The facts about one side alone are separate:

- `L.Invariant` (`L/Invariant.lean`) for the L configuration;
- `ConfigInvariant` and `TasksAreDistinct` (`Invariants.lean`) for the Dex
  configuration.

The correspondence is step for step. L takes exactly one clock-stamped action
per Dex commit of the translated program (spec §8.1 "Clock and timing
model"), so:

- every L label is simulated by one Dex commit or Worker effect, preceded by
  the Worker steps that run and return the handler (`IsSimulatedByDex`);
- every Dex action is simulated by one L label, except running and returning
  a handler, which L does not see (`IsSimulatedByL`).

The definitions of this file are fixed. The proofs of the step simulations
are in `Simulation/*.lean`; they may add lemmas but may not change these
definitions. `Examples/Lockstep.lean` tests the step correspondence, and
`Examples/RelatedCheck.lean` tests `Related` and `L.Invariant` themselves, on
pseudo-random schedules.
-/

namespace DexExpressiveness

open DexExpressiveness.L DexExpressiveness.Translation

/-! ## Threads and Steps -/

/-- The thread a Step input belongs to. The start Step runs `main`. -/
def threadOfInput : Option DexVal → Tid
  | some (.thread core) => core.tid
  | some (.timedThread core _ _) => core.tid
  | _ => []

/-- The effects a task has not emitted yet. -/
def pendingEffects (flow : Dex.FlowDef signature) (task : Dex.Task signature) : List Val :=
  match task.phase with
  | .started => (task.compute flow).1
  | .running remainingEffects _ => remainingEffects
  | .returned _ => []

/-- The kind, the number of timers, and the Channel conditions of the wait
that a blocked thread's Step has registered (spec §8.3). -/
def expectedWaitShape (self : Tid) : BlockedOn → Dex.WaitKind × Nat × List Chan
  | .lock mutex => (.all, 0, [Chan.mutexToken mutex])
  | .recv chan => (.any, 0, [Chan.external chan, Chan.cancelRequest self])
  | .join thread => (.any, 0, [Chan.joinDone thread, Chan.cancelRequest self])
  | .park cv => (.all, 0, [Chan.cvWake cv self])
  | .timed _ _ none => (.any, 1, [Chan.cancelRequest self])
  | .timed _ _ (some chan) => (.any, 1, [Chan.external chan, Chan.cancelRequest self])

/-- The Step that carries a thread has the Step type, input, and options the
translation gives it (spec §8.2): the start Step while `main` boots, the
`Timed` Step numbered by the thread's timer number after Enter, and otherwise
the program point of the thread's code, with the load selection of
`moveToPoint`. -/
def StepCarriesThread (program : Program) (thread : L.Thread)
    (record : Dex.StepRecord signature) : Prop :=
  match thread.phase, thread.timer with
  | .bootPending, _ =>
    record.id.stepType = StepType.start ∧
    ∃ input, record.input = some (DexVal.value input) ∧
      thread.core.locals = [(program.mainParam, input)]
  | .bootRunning, _ =>
    record.id.stepType = StepType.start ∧
    ∃ input, record.input = some (DexVal.value input) ∧
      thread.core.locals = [(program.mainParam, input)]
  | _, some (number, seconds) =>
    record.id = { stepType := StepType.timed, number := number } ∧
    record.input = some (DexVal.timedThread thread.core thread.code seconds)
  | _, none =>
    record.id.stepType = StepType.point thread.code ∧
    record.input = some (DexVal.thread thread.core) ∧
    record.options.executeLoadMaps = executeLoadMaps thread.core thread.code

/-- The Step's current invocation runs as a task with method `method` that
satisfies `Holds`. -/
def HasRunningTask (config : Dex.Config signature) (record : Dex.StepRecord signature)
    (method : Dex.Method) (Holds : Dex.Task signature → Prop) : Prop :=
  ∃ task, task ∈ config.tasks ∧
    record.invocation = some { method := method, state := .running task.id } ∧
    task.step = record.id ∧ task.method = method ∧ task.input = record.input ∧ Holds task

/-- How a thread's wait was decided, read from condition results. -/
def decodeOutcome (thread : L.Thread) (results : Option (Dex.ConditionResults signature)) :
    WakeOutcome :=
  if thread.timer.isSome then decodeTimedOutcome thread.code results
  else decodeBlockedOutcome thread.code results

/-- The phase of a thread matches the state of the Step that carries it and
of that Step's task (spec §8.2: issue at [STEP-START], `out(a)` at
[WORKER-EFFECT], continuation at [STEP-COMPLETE-EXECUTE]). -/
def PhaseMatchesStep (program : Program) (lConfig : L.Config) (config : Dex.Config signature)
    (thread : L.Thread) (record : Dex.StepRecord signature) : Prop :=
  let waitsFirst : Bool := thread.timer.isSome || isBlockingHead thread.code
  match thread.phase with
  | .bootPending => record.invocation = some { method := .execute, state := .pending }
  | .bootRunning =>
    HasRunningTask config record .execute (fun task => pendingEffects ⟦program⟧ task = [])
  | .ready =>
    record.conditionResults = none ∧
    record.invocation =
      some { method := if waitsFirst then .waitFor else .execute, state := .pending }
  | .running loaded =>
    HasRunningTask config record .execute (fun task =>
      pendingEffects ⟦program⟧ task = [] ∧ task.conditionResults = none ∧
      (match beginKind thread.core thread.code with
        | .load source => readShared task.snapshot source = loaded
        | _ => True) ∧
      -- The waiter list that the Execute of `wait`, `signal`, or `broadcast`
      -- read at Start is still current, because the thread holds the mutex
      -- (spec §8.3: "`waiters/cv` is read and written only under `m`").
      ∀ cv, cvOperand thread.core thread.code = some cv →
        readWaiters task.snapshot cv = (lConfig.cvQueues.get? cv).getD [])
  | .emitting effect =>
    HasRunningTask config record .execute (fun task =>
      pendingEffects ⟦program⟧ task = [effect] ∧ task.conditionResults = none)
  | .emitted =>
    HasRunningTask config record .execute (fun task =>
      pendingEffects ⟦program⟧ task = [] ∧ task.conditionResults = none)
  | .waitStarted => HasRunningTask config record .waitFor (fun _ => True)
  | .blocked on _ =>
    record.phase = .waiting ∧ record.invocation = none ∧
    ∃ waitState, record.waitState = some waitState ∧
      waitState.wait.kind = (expectedWaitShape thread.core.tid on).1 ∧
      waitState.wait.timerSeconds.length = (expectedWaitShape thread.core.tid on).2.1 ∧
      waitState.wait.channels = (expectedWaitShape thread.core.tid on).2.2 ∧
      waitState.timersDone = waitState.wait.timerSeconds.map (fun _ => false)
  | .woken outcome =>
    record.phase = .execute ∧
    record.invocation = some { method := .execute, state := .pending } ∧
    decodeOutcome thread record.conditionResults = outcome
  | .resuming outcome =>
    HasRunningTask config record .execute (fun task =>
      pendingEffects ⟦program⟧ task = [] ∧ decodeOutcome thread task.conditionResults = outcome)
  | .halted => False

/-! ## The relation -/

/-- The part of the relation that holds while the program runs. -/
structure RunningRelated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (run : Dex.Run signature) : Prop where
  /-- Shared variables are Attributes (spec §8.2). -/
  shared : ∀ name, readShared { attributes := run.attributes } name = lConfig.readShared name
  /-- `waiters/cv` is the condition variable's queue (spec §8.3). -/
  cvQueues : ∀ cv,
    readWaiters { attributes := run.attributes } cv = (lConfig.cvQueues.get? cv).getD []
  /-- An external Channel holds the queued external messages (spec §8.1, "External input"). -/
  inputQueues : ∀ chan,
    run.pendingMessages (Chan.external chan) = (lConfig.pendingInput chan).map DexVal.value
  /-- A mutex's token Channel holds one token exactly while the mutex is
  unlocked (spec §8.3 "Mutex"), once the start Step has published the tokens. -/
  mutexTokens : (∀ thread, thread ∈ lConfig.threads →
      thread.phase ≠ .bootPending ∧ thread.phase ≠ .bootRunning) →
    ∀ mutex, (run.pendingMessages (Chan.mutexToken mutex)).length =
      if lConfig.mutexOwners.get? mutex = some none then 1 else 0
  /-- `join/t` holds one message exactly while `t` is ended and not joined. -/
  joinTokens : ∀ thread, (run.pendingMessages (Chan.joinDone thread)).length =
    if lConfig.joinable.contains thread then 1 else 0
  /-- A pending cancel request means the flag is set. -/
  cancelRequestsAreFlags : ∀ thread,
    run.pendingMessages (Chan.cancelRequest thread) ≠ [] → lConfig.cancelFlags.contains thread = true
  /-- A set flag is a pending cancel request, unless the thread is gone or has observed it. -/
  cancelFlagsAreRequests : ∀ thread, lConfig.cancelFlags.contains thread = true →
    run.pendingMessages (Chan.cancelRequest thread) ≠ [] ∨
    lConfig.findThread? thread = none ∨
    ∃ current, lConfig.findThread? thread = some current ∧
      (current.phase = .woken .canceled ∨ current.phase = .resuming .canceled)
  /-- `cv/w` holds the wakes of a waiter that has not parked yet. -/
  cvWakes : ∀ cv waiter, (run.pendingMessages (Chan.cvWake cv waiter)).length =
    lConfig.cvWakes.count (cv, waiter)
  /-- The armed Dex timers are the armed L timers: same number, same deadline (spec §8.2). -/
  timers : ∀ timer fireTime, run.timers.get? timer = some fireTime ↔
    ∃ thread chan since, thread ∈ lConfig.threads ∧
      thread.phase = .blocked (.timed timer.step.number fireTime chan) since ∧
      timer.step.stepType = StepType.timed ∧ timer.index = 0
  /-- The timer counter is the `Timed` Step counter (spec §8.2: "the timer
  number `n` is its Step number"). -/
  timerCounter : (run.counters.get? StepType.timed).getD 0 = lConfig.timerCounter
  /-- Before the close the Flow has collected no output, so the close carries
  exactly the one tagged exit value (spec §5.3). -/
  outputsEmpty : run.outputs = []
  /-- Every thread that has not halted is carried by one active Step. -/
  threadsHaveSteps : ∀ thread, thread ∈ lConfig.threads → thread.phase ≠ .halted →
    ∃ record, record ∈ run.steps ∧ record.phase.isActive = true ∧
      StepCarriesThread program thread record ∧
      PhaseMatchesStep program lConfig dexConfig thread record
  /-- Every active Step carries one thread. -/
  stepsHaveThreads : ∀ record, record ∈ run.steps → record.phase.isActive = true →
    ∃ thread, thread ∈ lConfig.threads ∧ thread.phase ≠ .halted ∧
      StepCarriesThread program thread record
  /-- Two active Steps never carry the same thread. -/
  activeStepsHaveDistinctThreads : ∀ first, first ∈ run.steps → ∀ second, second ∈ run.steps →
    first.phase.isActive = true → second.phase.isActive = true →
    threadOfInput first.input = threadOfInput second.input → first = second
  /-- A Step with an invocation is active. -/
  invocationsAreActive : ∀ record, record ∈ run.steps → record.invocation ≠ none →
    record.phase.isActive = true
  /-- Blocked threads are ordered as their Steps' waits (spec §7 item 4: FIFO
  by registration). -/
  blockingOrder : ∀ first second firstRecord secondRecord firstOn firstSince secondOn secondSince
      firstWait secondWait,
    first ∈ lConfig.threads → second ∈ lConfig.threads →
    firstRecord ∈ run.steps → secondRecord ∈ run.steps →
    firstRecord.phase.isActive = true → secondRecord.phase.isActive = true →
    StepCarriesThread program first firstRecord → StepCarriesThread program second secondRecord →
    first.phase = .blocked firstOn firstSince → second.phase = .blocked secondOn secondSince →
    firstRecord.waitState = some firstWait → secondRecord.waitState = some secondWait →
    (firstSince < secondSince ↔ firstWait.registeredSeq < secondWait.registeredSeq)
  /-- While the Flow runs no Step is canceled, so every task is current. -/
  tasksAreCurrent : ∀ task, task ∈ dexConfig.tasks → dexConfig.isTaskCurrent task = true

/-- The part of the relation that holds after the close. -/
structure ClosedRelated (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) (run : Dex.Run signature) : Prop where
  /-- The late emits are the effects that the remaining tasks have not
  emitted (spec §8.1: "issued emits may still deliver"). -/
  lateEmits : (dexConfig.tasks.flatMap (pendingEffects ⟦program⟧)).Perm lConfig.lateEmits
  /-- The close canceled every Step, so no task is current. -/
  tasksAreStale : ∀ task, task ∈ dexConfig.tasks → dexConfig.isTaskCurrent task = false
  /-- No Step has an invocation. -/
  noInvocation : ∀ record, record ∈ run.steps → record.invocation = none
  /-- No timer is armed. -/
  noTimer : run.timers = []

/-- The simulation relation between an L configuration and a Dex
configuration of the translated Flow. -/
structure Related (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) : Prop where
  /-- The clocks agree (spec §8.1, "Clock and timing model"). -/
  clock : lConfig.clock = dexConfig.clock
  /-- The last L clock stamp is the last Dex commit timestamp: one stamped L
  action per Dex commit. -/
  stamp : lConfig.lastStampAt = dexConfig.lastCommitAt
  /-- Not started, running, and closed correspond. -/
  status :
    match lConfig.status, dexConfig.run with
    | .notStarted, none => True
    | .running, some run => run.status = .running
    | .closed, some run => ∃ status, run.status = .closed status
    | _, _ => False
  /-- Before the start no task exists. -/
  notStarted : dexConfig.run = none → dexConfig.tasks = []
  /-- While the program runs, the data and the threads correspond. -/
  running : ∀ run, dexConfig.run = some run → run.status = .running →
    RunningRelated program lConfig dexConfig run
  /-- After the close, the late emits correspond. -/
  closed : ∀ run status, dexConfig.run = some run → run.status = .closed status →
    ClosedRelated program lConfig dexConfig run

/-- The initial configurations are related. -/
theorem related_initial (program : Program) :
    Related program (L.system program).init (Dex.system ⟦program⟧).init :=
  { clock := rfl
    stamp := rfl
    status := trivial
    notStarted := fun _ => rfl
    running := fun run hasRun => by cases hasRun
    closed := fun run status hasRun => by cases hasRun }

/-! ## The two step simulations, as statements -/

/-- A Dex action that an admissible run can take from this configuration: a
start of the entry Step with an encoded literal, or, once the Flow has
started, a publication of an encoded literal to an external Channel (spec §5.3). -/
def IsAdmissibleAction (config : Dex.Config signature) : Dex.Action signature → Prop
  | .request (.startFlow stepType input) =>
    stepType = StepType.start ∧ ∃ literal : Literal, input = DexVal.value literal.toVal
  | .request (.publish chan message) =>
    config.run ≠ none ∧
    ∃ (externalChan : ExtChan) (literal : Literal),
      chan = Chan.external externalChan ∧ message = DexVal.value literal.toVal
  | _ => True

/-- The L step `lConfig --label--> lNext` with events `lEvents` is simulated
by the translated Flow from `dexConfig`: a finite sequence of Dex actions has
the same projected events, its only request is the encoded request of the
label, and it ends in a configuration related to `lNext`. -/
def IsSimulatedByDex (program : Program) (dexConfig : Dex.Config signature) (label : L.Label)
    (lEvents : List Event) (lNext : L.Config) : Prop :=
  ∃ actions dexEvents dexNext,
    (Dex.system ⟦program⟧).exec dexConfig actions = some (dexEvents, dexNext) ∧
    dexEvents.filterMap project = lEvents ∧
    actions.filterMap (Dex.system ⟦program⟧).requestOf =
      ((L.system program).requestOf label).toList.map encodeRequest ∧
    Related program lNext dexNext

/-- The Dex step with events `dexEvents` that ends in `dexNext` is simulated
by the L program from `lConfig`: at most one L label has the same projected
events and ends in a configuration related to `dexNext`. -/
def IsSimulatedByL (program : Program) (lConfig : L.Config)
    (dexEvents : List (Dex.Obs signature)) (dexNext : Dex.Config signature) : Prop :=
  ∃ labels lNext,
    labels.length ≤ 1 ∧
    (L.system program).exec lConfig labels = some (dexEvents.filterMap project, lNext) ∧
    Related program lNext dexNext

/-- The hypotheses that every step simulation lemma takes: the two
configurations are related, and each satisfies the invariant of its side. -/
structure IsSimulationState (program : Program) (lConfig : L.Config)
    (dexConfig : Dex.Config signature) : Prop where
  /-- The configurations are related. -/
  isRelated : Related program lConfig dexConfig
  /-- The L configuration satisfies the L invariant. -/
  lInvariant : L.Invariant program lConfig
  /-- The Dex configuration satisfies the Dex invariant. -/
  dexInvariant : ConfigInvariant program dexConfig
  /-- Task IDs are unique. -/
  tasksDistinct : TasksAreDistinct dexConfig

end DexExpressiveness
